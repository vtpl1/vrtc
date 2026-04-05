package edge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql" // register mysql driver
	"github.com/rs/zerolog/log"
	"github.com/soheilhy/cmux"
	"github.com/vtpl1/vrtc-sdk/av"
	grpcserver "github.com/vtpl1/vrtc-sdk/av/format/grpc"
	pb "github.com/vtpl1/vrtc-sdk/av/format/grpc/gen/avtransportv1"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/pkg/channel"
	"github.com/vtpl1/vrtc/pkg/edgeview"
	"github.com/vtpl1/vrtc/pkg/lifecycle"
	"github.com/vtpl1/vrtc/pkg/metrics"
	"github.com/vtpl1/vrtc/pkg/pva"
	"github.com/vtpl1/vrtc/pkg/recorder"
	"github.com/vtpl1/vrtc/pkg/schedule"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
)

const AppName = "edge"

var (
	errChannelFilePathRequired  = errors.New("channel_source=file requires channel_file_path")
	errScheduleFilePathRequired = errors.New("schedule_source=file requires schedule_file_path")
	errIndexPathRequired        = errors.New("edge: recording_index_path is required")
)

// Run starts the live-recording service. It blocks until ctx is cancelled.
//
//nolint:funlen,maintidx // server-lifecycle wiring cannot be split cleanly
func Run(appName string, cfg Config) error {
	c := cfg.LiveRecordingConfig

	// Default values for new fields.
	if c.APIListen == "" {
		c.APIListen = ":8080"
	}

	analyticsDelay := 5 * time.Second

	if c.AnalyticsDelay != "" {
		if d, err := time.ParseDuration(c.AnalyticsDelay); err == nil {
			analyticsDelay = d
		}
	}

	analyticsMaxWait := 7 * time.Second

	if c.AnalyticsMaxWait != "" {
		if d, err := time.ParseDuration(c.AnalyticsMaxWait); err == nil {
			analyticsMaxWait = d
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error)

	if c.RecordingIndexPath == "" {
		return errIndexPathRequired
	}

	// -----------------------------------------------------------------------
	// Providers
	// -----------------------------------------------------------------------
	chanProvider, err := newChannelProvider(ctx, c)
	if err != nil {
		return fmt.Errorf("edge: channel provider: %w", err)
	}
	defer chanProvider.Close()

	schedProvider, err := newScheduleProvider(ctx, c)
	if err != nil {
		return fmt.Errorf("edge: schedule provider: %w", err)
	}
	defer schedProvider.Close()

	recIndex := recorder.NewSQLiteIndex(c.RecordingIndexPath)
	defer recIndex.Close()

	// -----------------------------------------------------------------------
	// Analytics pipeline (store + hub + gRPC ingestion server)
	// -----------------------------------------------------------------------
	analyticsStore := pva.NewAnalyticsStore(30 * time.Second)
	analyticsHub := pva.NewAnalyticsHub()
	analyticsPipeline := pva.NewAnalyticsPipeline(analyticsStore, analyticsHub)

	var metricsCollector *metrics.Collector // set later; safe to capture in closure

	demuxerFactory := av.DemuxerFactory(
		func(ctx context.Context, sourceID string) (av.DemuxCloser, error) {
			start := time.Now()

			ch, err := chanProvider.GetChannel(ctx, sourceID)
			if err != nil {
				return nil, fmt.Errorf("edge: channel %q: %w", sourceID, err)
			}

			d, err := avgrabber.NewDemuxer(avgrabber.Config{
				URL:      ch.StreamURL,
				Username: ch.Username,
				Password: ch.Password,
				Audio:    true,
			})
			if err != nil {
				return nil, fmt.Errorf("edge: open stream %q: %w", ch.StreamURL, err)
			}

			if metricsCollector != nil {
				metricsCollector.RecordRTSPSessionSetup(time.Since(start), sourceID)
			}

			// Return the raw demuxer — analytics injection is handled by the
			// analytics relay hub via BlockingMerger, not on the live path.
			return d, nil
		},
	)

	sm := relayhub.New(demuxerFactory, nil)
	if err := sm.Start(ctx); err != nil {
		return fmt.Errorf("edge: stream manager start: %w", err)
	}

	defer func() { _ = sm.Stop() }()

	// -----------------------------------------------------------------------
	// Recording manager
	// -----------------------------------------------------------------------
	rm := recorder.New(sm, schedProvider, recIndex, 30*time.Second,
		recorder.WithDefaultRecording(
			channelAdapter{chanProvider},
			filepath.Dir(c.RecordingIndexPath),
		),
	)
	if err := rm.Start(ctx); err != nil {
		return fmt.Errorf("edge: recording manager start: %w", err)
	}

	defer func() { _ = rm.Stop() }()

	// -----------------------------------------------------------------------
	// Edge view service
	// -----------------------------------------------------------------------
	viewSvc := edgeview.NewService(log.Logger, sm, recIndex, sm,
		edgeview.WithChannelWriter(chanProvider),
		edgeview.WithRecordingProvider(rm),
	)

	// -----------------------------------------------------------------------
	// Analytics relay hub (delayed, analytics-enriched)
	// -----------------------------------------------------------------------
	analyticsDemuxerFactory := pva.NewAnalyticsDemuxerFactory(
		viewSvc.RecordedDemuxerFactory,
		sm,
		analyticsStore,
		analyticsHub,
		analyticsDelay,
		analyticsMaxWait,
	)

	analyticsRelayHub := relayhub.New(analyticsDemuxerFactory, nil)
	if err := analyticsRelayHub.Start(ctx); err != nil {
		return fmt.Errorf("edge: analytics relay hub start: %w", err)
	}

	defer func() { _ = analyticsRelayHub.Stop() }()

	viewSvc.SetAnalyticsRelayHub(analyticsRelayHub)

	// Register channels as cameras for the camera listing endpoint.
	if channels, chErr := chanProvider.ListChannels(ctx); chErr == nil {
		for i := range channels {
			viewSvc.RegisterCamera(&edgeview.CameraInfo{
				CameraID: channels[i].ID,
				Name:     channels[i].Name,
				State:    "active",
			})
		}
	}

	startTime := time.Now()

	// -----------------------------------------------------------------------
	// KPI Metrics
	// -----------------------------------------------------------------------
	metricsDBPath := filepath.Join(filepath.Dir(c.RecordingIndexPath), "metrics.db")

	metricsStore, err := metrics.New(metricsDBPath, 7*24*time.Hour, 500_000)
	if err != nil {
		log.Warn().Err(err).Msg("edge: metrics store disabled")
	}

	if metricsStore != nil {
		defer metricsStore.Close()

		metricsCollector = metrics.NewCollector(metricsStore, sm, rm, viewSvc)

		rm.SetMetricsCollector(metricsCollector)
		defer metricsCollector.Stop()
	}

	// -----------------------------------------------------------------------
	// Periodic health logging
	// -----------------------------------------------------------------------
	startHealthLogger(ctx, sm, rm, startTime, 60*time.Second)

	// -----------------------------------------------------------------------
	// HTTP / WebSocket API (Chi + Huma)
	// -----------------------------------------------------------------------
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))
	router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// All JSON, streaming, and WebSocket endpoints via edgeview (includes
	// auto-generated OpenAPI spec at /openapi.json and docs UI at /docs).
	handlerOpts := []edgeview.HTTPHandlerOption{
		edgeview.WithSegmentCounter(rm),
		edgeview.WithAnalyticsHub(analyticsHub),
	}
	if metricsCollector != nil {
		handlerOpts = append(handlerOpts, edgeview.WithMetricsCollector(metricsCollector))
	}

	viewHandler := edgeview.NewHTTPHandler(viewSvc, log.Logger, c.AuthToken, handlerOpts...)
	router.Mount("/", viewHandler.Router())

	srv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// -----------------------------------------------------------------------
	// cmux: multiplex HTTP/1.1 and gRPC (HTTP/2) on the same port.
	// -----------------------------------------------------------------------
	lis, lisErr := (&net.ListenConfig{}).Listen(ctx, "tcp", c.APIListen)
	if lisErr != nil {
		return fmt.Errorf("edge: listen %s: %w", c.APIListen, lisErr)
	}

	mx := cmux.New(lis)
	grpcL := mx.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"),
	)
	httpL := mx.Match(cmux.Any())

	// gRPC analytics ingestion server.
	grpcSrv := grpc.NewServer()
	pb.RegisterAnalyticsIngestionServiceServer(
		grpcSrv,
		grpcserver.NewAnalyticsIngestionServer(analyticsPipeline.Handle),
	)

	go func() {
		if err := grpcSrv.Serve(grpcL); err != nil {
			log.Error().Err(err).Msg("edge: analytics gRPC server error")
		}
	}()

	go func() {
		if err := srv.Serve(httpL); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("http server: %w", err)
		}
	}()

	go func() {
		if err := mx.Serve(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Error().Err(err).Msg("edge: cmux serve error")
		}
	}()

	go func() {
		<-ctx.Done()
		grpcSrv.GracefulStop()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()

		_ = srv.Shutdown(shutCtx) //nolint:contextcheck
	}()

	log.Info().Str("appName", appName).Str("addr", c.APIListen).Msg("edge starting")

	lifecycle.WaitForTerminationRequest(errChan)
	log.Info().Str("appName", appName).Msg("termination signal received, shutting down gracefully")
	cancel()
	log.Info().Str("appName", appName).Msg("shutdown complete")

	return nil
}

// newChannelProvider constructs the ChannelWriter selected by cfg.ChannelSource.
//
//nolint:dupl // symmetric with newScheduleProvider by design
func newChannelProvider(
	ctx context.Context,
	c LiveRecordingConfig,
) (channel.ChannelWriter, error) {
	switch c.ChannelSource {
	case "mysql":
		db, err := sql.Open("mysql", c.MySQLConfig.DSN(c.ChannelDB))
		if err != nil {
			return nil, fmt.Errorf("open mysql: %w", err)
		}

		if err := db.PingContext(ctx); err != nil {
			db.Close()

			return nil, fmt.Errorf("ping mysql: %w", err)
		}

		return channel.NewMySQLProvider(db), nil

	case "mongo":
		client, err := mongo.Connect(options.Client().ApplyURI(c.MongoConfig.URI))
		if err != nil {
			return nil, fmt.Errorf("connect mongo: %w", err)
		}

		coll := client.Database(c.MongoConfig.Database).Collection("channels")

		return channel.NewMongoProvider(coll), nil

	default: // "file" or ""
		if c.ChannelFilePath == "" {
			return nil, errChannelFilePathRequired
		}

		return channel.NewFileProvider(c.ChannelFilePath), nil
	}
}

// newScheduleProvider constructs the ScheduleProvider selected by cfg.ScheduleSource.
//
//nolint:dupl // symmetric with newChannelProvider by design
func newScheduleProvider(
	ctx context.Context,
	c LiveRecordingConfig,
) (schedule.ScheduleProvider, error) {
	switch c.ScheduleSource {
	case "mysql":
		db, err := sql.Open("mysql", c.MySQLConfig.DSN(c.ScheduleDB))
		if err != nil {
			return nil, fmt.Errorf("open mysql: %w", err)
		}

		if err := db.PingContext(ctx); err != nil {
			db.Close()

			return nil, fmt.Errorf("ping mysql: %w", err)
		}

		return schedule.NewMySQLProvider(db), nil

	case "mongo":
		client, err := mongo.Connect(options.Client().ApplyURI(c.MongoConfig.URI))
		if err != nil {
			return nil, fmt.Errorf("connect mongo: %w", err)
		}

		coll := client.Database(c.MongoConfig.Database).Collection("recording_schedules")

		return schedule.NewMongoProvider(coll), nil

	default: // "file" or ""
		if c.ScheduleFilePath == "" {
			return nil, errScheduleFilePathRequired
		}

		return schedule.NewFileProvider(c.ScheduleFilePath), nil
	}
}

// channelAdapter adapts channel.ChannelProvider to recorder.ChannelSource.
type channelAdapter struct {
	p channel.ChannelProvider
}

func (a channelAdapter) ListChannels(ctx context.Context) ([]recorder.Channel, error) {
	chs, err := a.p.ListChannels(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]recorder.Channel, len(chs))
	for i, ch := range chs {
		out[i] = recorder.Channel{ID: ch.ID}
	}

	return out, nil
}
