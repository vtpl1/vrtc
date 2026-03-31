package liverecservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql" // register mysql driver
	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/fmp4"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/internal/httprouter"
	"github.com/vtpl1/vrtc/pkg/channel"
	"github.com/vtpl1/vrtc/pkg/lifecycle"
	"github.com/vtpl1/vrtc/pkg/playback"
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

	demuxerFactory := av.DemuxerFactory(
		func(ctx context.Context, sourceID string) (av.DemuxCloser, error) {
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
	rm := recorder.New(sm, schedProvider, recIndex, 30*time.Second)
	if err := rm.Start(ctx); err != nil {
		return fmt.Errorf("liverecservice: recording manager start: %w", err)
	}

	defer func() { _ = rm.Stop() }()

	// -----------------------------------------------------------------------
	// Playback router
	// -----------------------------------------------------------------------
	pbRouter := playback.New(recIndex)

	startTime := time.Now()
	ct := &connTracker{}

	// -----------------------------------------------------------------------
	// Periodic health logging
	// -----------------------------------------------------------------------
	startHealthLogger(ctx, sm, rm, ct, startTime, 60*time.Second)

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

	api := humachi.New(router, huma.DefaultConfig("LiveRecService API", "1.0.0"))

	// ── Huma JSON endpoints ──────────────────────────────────────────────

	huma.Get(api, "/health", func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		snap := collectHealth(ctx, sm, rm, ct, startTime)

		return &healthOutput{Body: snap}, nil
	})

	huma.Get(
		api,
		"/stats/producers",
		func(ctx context.Context, _ *struct{}) (*producerStatsOutput, error) {
			return &producerStatsOutput{Body: sm.GetRelayStats(ctx)}, nil
		},
	)

	huma.Get(
		api,
		"/recordings/{channelID}",
		func(ctx context.Context, input *timebarInput) (*timebarOutput, error) {
			return handleTimebar(ctx, input, recIndex)
		},
	)

	// ── Streaming / WebSocket endpoints (raw Chi) ────────────────────────

	router.Get("/live/{channelID}", func(w http.ResponseWriter, req *http.Request) {
		defer ct.trackHTTPLive()()

		liveHTTPHandler(req.Context(), w, chi.URLParam(req, "channelID"), sm)
	})

	router.Get("/recorded/{channelID}", func(w http.ResponseWriter, req *http.Request) {
		defer ct.trackHTTPRecorded()()

		recordedHTTPHandler(req.Context(), w, req, chi.URLParam(req, "channelID"), pbRouter)
	})

	router.Get("/ws/live", func(w http.ResponseWriter, req *http.Request) {
		defer ct.trackWSLive()()

		httprouter.WSHandler(req.Context(), w, req, sm)
	})

	router.Get("/ws/recorded", func(w http.ResponseWriter, req *http.Request) {
		defer ct.trackWSRecorded()()

		wsRecordedHandler(req.Context(), w, req, pbRouter)
	})

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

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	lifecycle.WaitForTerminationRequest(errChan)
	cancel()

	return nil
}

// newChannelProvider constructs the ChannelProvider selected by cfg.ChannelSource.
//
//nolint:dupl // symmetric with newScheduleProvider by design
func newChannelProvider(
	ctx context.Context,
	c LiveRecordingConfig,
) (channel.ChannelProvider, error) {
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

// liveHTTPHandler streams a live fMP4 feed to the HTTP client.
func liveHTTPHandler(
	ctx context.Context,
	w http.ResponseWriter,
	channelID string,
	sm av.RelayHub,
) {
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return fmp4.NewMuxer(flushWriter{w: w, f: flusher}), nil
	})

	handle, err := sm.Consume(ctx, channelID, av.ConsumeOptions{
		MuxerFactory: muxerFactory,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}
	defer handle.Close(ctx)

	<-ctx.Done()
}

// recordedHTTPHandler streams a recorded fMP4 segment to the HTTP client.
func recordedHTTPHandler(
	ctx context.Context,
	w http.ResponseWriter,
	req *http.Request,
	channelID string,
	pb *playback.Router,
) {
	from, to, err := parseTimeRange(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	pbReq := playback.Request{ChannelID: channelID, From: from, To: to}
	factory := pb.RecordedDemuxerFactory(pbReq)

	playSM := relayhub.New(factory, nil)
	if err := playSM.Start(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	defer playSM.Stop() //nolint:errcheck

	// Set Content-Type before first Write so Go's HTTP server includes it in
	// the implicit 200 response; do NOT call WriteHeader yet — if Consume
	// fails we still want to send a non-200 error response.
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	done := make(chan struct{})
	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return &notifyMuxer{
			MuxCloser: fmp4.NewMuxer(flushWriter{w: w, f: flusher}),
			onClose:   func() { close(done) },
		}, nil
	})

	handle, err := playSM.Consume(ctx, channelID, av.ConsumeOptions{
		MuxerFactory: muxerFactory,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}
	defer handle.Close(ctx)

	select {
	case <-ctx.Done():
	case <-done:
	}
}

// wsRecordedHandler serves a recorded stream over WebSocket MSE.
func wsRecordedHandler(
	ctx context.Context,
	w http.ResponseWriter,
	req *http.Request,
	pb *playback.Router,
) {
	channelID := req.URL.Query().Get("sourceID")

	from, to, err := parseTimeRange(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	pbReq := playback.Request{ChannelID: channelID, From: from, To: to}
	factory := pb.RecordedDemuxerFactory(pbReq)

	playSM := relayhub.New(factory, nil)
	if err := playSM.Start(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	defer playSM.Stop() //nolint:errcheck

	httprouter.WSHandler(ctx, w, req, playSM)
}

// parseTimeRange extracts optional from/to RFC3339 query parameters.
func parseTimeRange(req *http.Request) (from, to time.Time, err error) {
	if s := req.URL.Query().Get("from"); s != "" {
		from, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, fmt.Errorf("invalid 'from': %w", err)
		}
	}

	if s := req.URL.Query().Get("to"); s != "" {
		to, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, fmt.Errorf("invalid 'to': %w", err)
		}
	}

	return from, to, nil
}

// flushWriter wraps an http.ResponseWriter and flushes after each Write.
type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}

	return n, err
}

// notifyMuxer wraps a MuxCloser and calls onClose when Close is invoked.
type notifyMuxer struct {
	av.MuxCloser

	onClose func()
}

func (m *notifyMuxer) Close() error {
	err := m.MuxCloser.Close()
	if m.onClose != nil {
		m.onClose()
	}

	return err
}

// ── Huma I/O types ───────────────────────────────────────────────────────────

// healthOutput wraps the health snapshot for Huma.
type healthOutput struct {
	Body healthSnapshot
}

// producerStatsOutput wraps the relay-stats slice for Huma.
type producerStatsOutput struct {
	Body []av.RelayStats
}

// timebarInput captures path and query parameters for the recordings endpoint.
type timebarInput struct {
	ChannelID string `path:"channelID"`
	From      string `                 doc:"Start of time window (RFC 3339)" example:"2026-01-01T00:00:00Z" query:"from"`
	To        string `                 doc:"End of time window (RFC 3339)"   example:"2026-01-02T00:00:00Z" query:"to"`
}

// timebarSegment is one recorded segment as returned by the timebar endpoint.
type timebarSegment struct {
	ID        string    `json:"id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Status    string    `json:"status"`
	SizeBytes int64     `json:"sizeBytes"`
	FilePath  string    `json:"filePath"`
}

// timebarBody is the JSON envelope returned by GET /recordings/{channelID}.
type timebarBody struct {
	ChannelID string           `json:"channelId"`
	From      *time.Time       `json:"from,omitempty"`
	To        *time.Time       `json:"to,omitempty"`
	Segments  []timebarSegment `json:"segments"`
}

// timebarOutput wraps the timebar body for Huma.
type timebarOutput struct {
	Body timebarBody
}

// handleTimebar implements the recordings endpoint logic.
func handleTimebar(
	ctx context.Context,
	input *timebarInput,
	index recorder.RecordingIndex,
) (*timebarOutput, error) {
	var from, to time.Time

	var err error

	if input.From != "" {
		from, err = time.Parse(time.RFC3339, input.From)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid 'from': " + err.Error())
		}
	}

	if input.To != "" {
		to, err = time.Parse(time.RFC3339, input.To)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid 'to': " + err.Error())
		}
	}

	entries, err := index.QueryByChannel(ctx, input.ChannelID, from, to)
	if err != nil {
		log.Error().Err(err).Str("channel", input.ChannelID).Msg("timebar: query")

		return nil, huma.Error500InternalServerError("index query failed")
	}

	segments := make([]timebarSegment, len(entries))
	for i, e := range entries {
		segments[i] = timebarSegment{
			ID:        e.ID,
			Start:     e.StartTime,
			End:       e.EndTime,
			Status:    e.Status,
			SizeBytes: e.SizeBytes,
			FilePath:  e.FilePath,
		}
	}

	resp := timebarBody{
		ChannelID: input.ChannelID,
		Segments:  segments,
	}

	if !from.IsZero() {
		resp.From = &from
	}

	if !to.IsZero() {
		resp.To = &to
	}

	return &timebarOutput{Body: resp}, nil
}
