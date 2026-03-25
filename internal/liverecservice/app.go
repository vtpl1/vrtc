package liverecservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql" // register mysql driver
	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/internal/httprouter"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
	"github.com/vtpl1/vrtc/pkg/av/streammanager3"
	"github.com/vtpl1/vrtc/pkg/channel"
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
)

// Run starts the live-recording service. It blocks until ctx is cancelled.
func Run(_ string, cfg Config) error {
	c := cfg.LiveRecordingConfig

	// Default values for new fields.
	if c.APIListen == "" {
		c.APIListen = ":8080"
	}

	// Root context: cancelled on SIGINT / SIGTERM so the service shuts down
	// gracefully when the process receives a stop signal.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if c.RecordingIndexPath == "" {
		// Recording index is always required; wait for a signal and exit cleanly.
		<-ctx.Done()

		return nil
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
		func(ctx context.Context, producerID string) (av.DemuxCloser, error) {
			ch, err := chanProvider.GetChannel(ctx, producerID)
			if err != nil {
				return nil, fmt.Errorf("liverecservice: channel %q: %w", producerID, err)
			}

			d, err := avgrabber.NewDemuxer(avgrabber.Config{URL: ch.StreamURL, Audio: true})
			if err != nil {
				return nil, fmt.Errorf("liverecservice: open stream %q: %w", ch.StreamURL, err)
			}

			return d, nil
		},
	)

	sm := streammanager3.New(demuxerFactory, nil)
	if err := sm.Start(ctx); err != nil {
		return fmt.Errorf("liverecservice: stream manager start: %w", err)
	}
	defer sm.Stop() //nolint:errcheck

	// -----------------------------------------------------------------------
	// Recording manager
	// -----------------------------------------------------------------------
	rm := recorder.New(sm, schedProvider, recIndex, 30*time.Second)
	if err := rm.Start(ctx); err != nil {
		return fmt.Errorf("liverecservice: recording manager start: %w", err)
	}
	defer rm.Stop() //nolint:errcheck

	// -----------------------------------------------------------------------
	// Playback router
	// -----------------------------------------------------------------------
	pbRouter := playback.New(recIndex)

	// -----------------------------------------------------------------------
	// HTTP / WebSocket API
	// -----------------------------------------------------------------------
	r := chi.NewRouter()

	// GET /live/{channelID} — chunked fMP4 over HTTP
	r.Get("/live/{channelID}", func(w http.ResponseWriter, req *http.Request) {
		channelID := chi.URLParam(req, "channelID")
		liveHTTPHandler(req.Context(), w, channelID, sm)
	})

	// GET /recorded/{channelID}?from=RFC3339&to=RFC3339 — chunked fMP4 over HTTP
	r.Get("/recorded/{channelID}", func(w http.ResponseWriter, req *http.Request) {
		channelID := chi.URLParam(req, "channelID")
		recordedHTTPHandler(req.Context(), w, req, channelID, pbRouter)
	})

	// GET /ws/live?producerID=…&consumerID=… — MSE over WebSocket (live)
	r.Get("/ws/live", func(w http.ResponseWriter, req *http.Request) {
		httprouter.WSHandler(req.Context(), w, req, sm)
	})

	// GET /ws/recorded?producerID=…&consumerID=…&from=RFC3339&to=RFC3339
	r.Get("/ws/recorded", func(w http.ResponseWriter, req *http.Request) {
		wsRecordedHandler(req.Context(), w, req, pbRouter)
	})

	srv := &http.Server{
		Addr:              c.APIListen,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()

		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
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
	sm av.StreamManager,
) {
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return fmp4.NewMuxer(flushWriter{w: w, f: flusher}), nil
	})

	handle, err := sm.Consume(ctx, channelID, av.ConsumeOptions{
		ConsumerID:   fmt.Sprintf("http-live-%d", time.Now().UnixNano()),
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

	playSM := streammanager3.New(factory, nil)
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
		ConsumerID:   fmt.Sprintf("http-rec-%d", time.Now().UnixNano()),
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
	channelID := req.URL.Query().Get("producerID")

	from, to, err := parseTimeRange(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	pbReq := playback.Request{ChannelID: channelID, From: from, To: to}
	factory := pb.RecordedDemuxerFactory(pbReq)

	playSM := streammanager3.New(factory, nil)
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
