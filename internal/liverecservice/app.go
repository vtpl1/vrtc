package liverecservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql" // register mysql driver
	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/pkg/channel"
	"github.com/vtpl1/vrtc/pkg/edgeview"
	"github.com/vtpl1/vrtc/pkg/lifecycle"
	"github.com/vtpl1/vrtc/pkg/metrics"
	"github.com/vtpl1/vrtc/pkg/recorder"
	"github.com/vtpl1/vrtc/pkg/schedule"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const AppName = "entrypoint_live_recording"

var (
	errChannelFilePathRequired  = errors.New("channel_source=file requires channel_file_path")
	errScheduleFilePathRequired = errors.New("schedule_source=file requires schedule_file_path")
	errIndexPathRequired        = errors.New("liverecservice: recording_index_path is required")
)

// Run starts the live-recording service. It blocks until ctx is cancelled.
//
//nolint:funlen // server-lifecycle wiring cannot be split cleanly
func Run(appName string, cfg Config) error {
	c := cfg.LiveRecordingConfig

	// Default values for new fields.
	if c.APIListen == "" {
		c.APIListen = ":8080"
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
		return fmt.Errorf("liverecservice: channel provider: %w", err)
	}
	defer chanProvider.Close()

	schedProvider, err := newScheduleProvider(ctx, c)
	if err != nil {
		return fmt.Errorf("liverecservice: schedule provider: %w", err)
	}
	defer schedProvider.Close()

	recIndex := recorder.NewFileIndex(c.RecordingIndexPath)
	defer recIndex.Close()

	var metricsCollector *metrics.Collector // set later; safe to capture in closure

	demuxerFactory := av.DemuxerFactory(
		func(ctx context.Context, sourceID string) (av.DemuxCloser, error) {
			start := time.Now()

			ch, err := chanProvider.GetChannel(ctx, sourceID)
			if err != nil {
				return nil, fmt.Errorf("liverecservice: channel %q: %w", sourceID, err)
			}

			d, err := avgrabber.NewDemuxer(avgrabber.Config{
				URL:      ch.StreamURL,
				Username: ch.Username,
				Password: ch.Password,
				Audio:    true,
			})
			if err != nil {
				return nil, fmt.Errorf("liverecservice: open stream %q: %w", ch.StreamURL, err)
			}

			if metricsCollector != nil {
				metricsCollector.RecordRTSPSessionSetup(time.Since(start), sourceID)
			}

			return d, nil
		},
	)

	sm := relayhub.New(demuxerFactory, nil)
	if err := sm.Start(ctx); err != nil {
		return fmt.Errorf("liverecservice: stream manager start: %w", err)
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
		return fmt.Errorf("liverecservice: recording manager start: %w", err)
	}

	defer func() { _ = rm.Stop() }()

	// -----------------------------------------------------------------------
	// Edge view service
	// -----------------------------------------------------------------------
	viewSvc := edgeview.NewService(log.Logger, sm, recIndex, nil,
		edgeview.WithChannelWriter(chanProvider),
		edgeview.WithRecordingProvider(rm),
	)

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
		log.Warn().Err(err).Msg("liverecservice: metrics store disabled")
	}

	if metricsStore != nil {
		defer metricsStore.Close()

		metricsCollector = metrics.NewCollector(metricsStore, sm, rm, viewSvc)
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
	}
	if metricsCollector != nil {
		handlerOpts = append(handlerOpts, edgeview.WithMetricsCollector(metricsCollector))
	}

	viewHandler := edgeview.NewHTTPHandler(viewSvc, log.Logger, "", handlerOpts...)
	router.Mount("/", viewHandler.Router())

	srv := &http.Server{
		Addr:              c.APIListen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()

		_ = srv.Shutdown(shutCtx) //nolint:contextcheck
	}()

	log.Info().Str("appName", appName).Str("addr", c.APIListen).Msg("liverecservice starting")

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("http server: %w", err)
		}
	}()

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
