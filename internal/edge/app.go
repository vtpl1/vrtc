package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/llhls"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/internal/avgrabber"
	"github.com/vtpl1/vrtc/pkg/edgeview"
	"github.com/vtpl1/vrtc/pkg/lifecycle"
	"github.com/vtpl1/vrtc/pkg/pva"
)

var errNoSupportedStreams = errors.New("streamFilterMuxer: no supported streams")

const (
	// hlsPrefix is the URL path prefix for all LL-HLS endpoints.
	hlsPrefix = "/hls/"

	// consumerIdleTimeout is how long a consumer lives without any incoming
	// request before it is automatically removed.
	consumerIdleTimeout = 10 * time.Second

	// idleSweepInterval controls how often the idle-sweep goroutine runs.
	idleSweepInterval = 5 * time.Second
)

// supportedCodecs are the codec types that the llhls/fmp4 muxer can handle.
// All other codecs (G.711 µ-law/A-law, SPEEX, …) are silently dropped.
//
//nolint:gochecknoglobals
var supportedCodecs = map[av.CodecType]bool{
	av.H264: true,
	av.H265: true,
	av.AAC:  true,
}

// streamFilterMuxer wraps an llhls.Muxer and silently drops streams/packets
// whose codec is not in supportedCodecs. It remaps packet Idx values so that
// the inner muxer always sees a contiguous, zero-based stream index.
type streamFilterMuxer struct {
	inner *llhls.Muxer
	remap map[uint16]uint16 // outer Idx → inner Idx
}

func newStreamFilterMuxer(inner *llhls.Muxer) *streamFilterMuxer {
	return &streamFilterMuxer{
		inner: inner,
		remap: make(map[uint16]uint16),
	}
}

func (f *streamFilterMuxer) WriteHeader(ctx context.Context, streams []av.Stream) error {
	var filtered []av.Stream

	for _, s := range streams {
		if supportedCodecs[s.Codec.Type()] {
			innerIdx := uint16(len(filtered))
			f.remap[s.Idx] = innerIdx
			s.Idx = innerIdx
			filtered = append(filtered, s)
		}
	}

	if len(filtered) == 0 {
		return errNoSupportedStreams
	}

	return f.inner.WriteHeader(ctx, filtered)
}

func (f *streamFilterMuxer) WritePacket(ctx context.Context, pkt av.Packet) error {
	innerIdx, ok := f.remap[pkt.Idx]
	if !ok {
		return nil // silently drop unsupported stream
	}

	pkt.Idx = innerIdx

	return f.inner.WritePacket(ctx, pkt)
}

func (f *streamFilterMuxer) WriteTrailer(ctx context.Context, upstreamError error) error {
	return f.inner.WriteTrailer(ctx, upstreamError)
}

func (f *streamFilterMuxer) WriteCodecChange(ctx context.Context, changed []av.Stream) error {
	var filtered []av.Stream

	for _, s := range changed {
		if innerIdx, ok := f.remap[s.Idx]; ok {
			s.Idx = innerIdx
			filtered = append(filtered, s)
		}
	}

	if len(filtered) == 0 {
		return nil
	}

	return f.inner.WriteCodecChange(ctx, filtered)
}

func (f *streamFilterMuxer) Close() error {
	return f.inner.Close()
}

// Handler delegates to the inner llhls.Muxer's HTTP handler.
func (f *streamFilterMuxer) Handler(prefix string) http.Handler {
	return f.inner.Handler(prefix)
}

// consumerEntry tracks one LL-HLS consumer that was auto-created on first request.
type consumerEntry struct {
	handle      av.ConsumerHandle
	muxer       *streamFilterMuxer
	mu          sync.Mutex
	lastRequest time.Time
}

func (e *consumerEntry) touch() {
	e.mu.Lock()
	e.lastRequest = time.Now()
	e.mu.Unlock()
}

func (e *consumerEntry) idleSince() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()

	return time.Since(e.lastRequest)
}

// Run starts the edge node and blocks until ctx is cancelled.
//
//nolint:maintidx,funlen // server-lifecycle wiring cannot be split cleanly
func Run(appName, appMode string, cfg Config) error {
	log.Info().Msgf("%+v", cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error)

	sourceID := cfg.Edge.StreamAddr

	// ── LL-HLS muxer registry ─────────────────────────────────────────────────

	var (
		consumersMu sync.RWMutex
		consumers   = make(map[string]*consumerEntry)
	)

	hlsCfg := llhls.DefaultConfig()

	muxerFactory := av.MuxerFactory(
		func(_ context.Context, consumerID string) (av.MuxCloser, error) {
			mx := newStreamFilterMuxer(llhls.NewMuxer(hlsCfg))

			consumersMu.Lock()
			if e, ok := consumers[consumerID]; ok {
				e.muxer = mx
				e.touch()
			}
			consumersMu.Unlock()

			log.Info().Str("consumer", consumerID).Msg("llhls muxer created")

			return mx, nil
		},
	)

	muxerRemover := av.MuxerRemover(func(_ context.Context, consumerID string) error {
		consumersMu.Lock()
		delete(consumers, consumerID)
		consumersMu.Unlock()

		log.Info().Str("consumer", consumerID).Msg("llhls muxer removed")

		return nil
	})

	// ── demuxer factory: avgrabber RTSP ──────────────────────────────────────

	avgrabber.Init()

	defer avgrabber.Deinit()

	proto := avgrabber.ProtoTCP
	if cfg.Edge.RTSPProto == "udp" {
		proto = avgrabber.ProtoUDP
	}

	rtspCfg := avgrabber.Config{
		URL:      cfg.Edge.StreamAddr,
		Username: cfg.Edge.RTSPUsername,
		Password: cfg.Edge.RTSPPassword,
		Protocol: int32(proto),
		Audio:    true,
	}

	demuxerFactory := av.DemuxerFactory(
		func(_ context.Context, _ string) (av.DemuxCloser, error) {
			dmx, err := avgrabber.NewDemuxer(rtspCfg)
			if err != nil {
				return nil, fmt.Errorf("avgrabber open %q: %w", rtspCfg.URL, err)
			}

			log.Info().Str("url", rtspCfg.URL).Msg("avgrabber demuxer opened")

			return pva.NewMetadataMerger(dmx, pva.NilSource{}), nil
		},
	)

	demuxerRemover := av.DemuxerRemover(func(_ context.Context, _ string) error {
		log.Info().Str("url", rtspCfg.URL).Msg("avgrabber demuxer closed")

		return nil
	})

	// ── stream manager ────────────────────────────────────────────────────────

	sm := relayhub.New(demuxerFactory, demuxerRemover)

	if err := sm.Start(ctx); err != nil {
		return fmt.Errorf("stream manager start: %w", err)
	}

	defer func() { _ = sm.Stop() }()

	// ── idle consumer sweep ───────────────────────────────────────────────────

	go func() {
		ticker := time.NewTicker(idleSweepInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				consumersMu.RLock()

				type idleConsumer struct {
					id     string
					handle av.ConsumerHandle
				}

				var idle []idleConsumer

				for id, e := range consumers {
					if e.idleSince() > consumerIdleTimeout {
						idle = append(idle, idleConsumer{id: id, handle: e.handle})
					}
				}

				consumersMu.RUnlock()

				for _, entry := range idle {
					if entry.handle == nil {
						continue
					}

					log.Info().Str("consumer", entry.id).Msg("removing idle consumer")
					_ = entry.handle.Close(ctx)
				}
			}
		}
	}()

	// ── HTTP handler ──────────────────────────────────────────────────────────

	// ensureConsumer returns the consumerEntry for consumerID, creating and
	// registering it with the stream manager on the first call.
	ensureConsumer := func(r *http.Request, consumerID string) (*consumerEntry, error) {
		// Fast path: consumer already exists.
		consumersMu.RLock()

		e, ok := consumers[consumerID]

		consumersMu.RUnlock()

		if ok {
			e.touch()

			return e, nil
		}

		// Slow path: create a new entry and add it to the stream manager.
		consumersMu.Lock()
		// Re-check under write lock (another goroutine may have beaten us).
		e, ok = consumers[consumerID]
		if !ok {
			e = &consumerEntry{lastRequest: time.Now()}
			consumers[consumerID] = e
		}
		consumersMu.Unlock()

		if ok {
			// Already created by a concurrent request.
			e.touch()

			return e, nil
		}

		errCh := make(chan error, 1)

		handle, err := sm.Consume(r.Context(), sourceID, av.ConsumeOptions{
			ConsumerID:   consumerID,
			MuxerFactory: muxerFactory,
			MuxerRemover: muxerRemover,
			ErrChan:      errCh,
		})
		if err != nil {
			consumersMu.Lock()
			delete(consumers, consumerID)
			consumersMu.Unlock()

			return nil, fmt.Errorf("consume %q: %w", consumerID, err)
		}

		consumersMu.Lock()
		e.handle = handle
		consumersMu.Unlock()

		log.Info().Str("consumer", consumerID).Str("dir", sourceID).Msg("consumer added")

		return e, nil
	}

	mux := http.NewServeMux()

	// corsMiddleware adds permissive CORS headers so browser-based players
	// (e.g. hls.js on a different origin) can reach the HLS endpoints.
	corsMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Range")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)

				return
			}

			next.ServeHTTP(w, r)
		})
	}

	// GET /hls/<consumerID>/index.m3u8   → playlist  (auto-creates consumer)
	// GET /hls/<consumerID>/init.mp4     → init segment
	// GET /hls/<consumerID>/part*.mp4    → LL-HLS parts
	// GET /hls/<consumerID>/seg*.mp4     → complete segments
	mux.HandleFunc(hlsPrefix, func(w http.ResponseWriter, r *http.Request) {
		// Extract the consumerID (first path segment after /hls/).
		rest := strings.TrimPrefix(r.URL.Path, hlsPrefix)

		before, _, ok := strings.Cut(rest, "/")
		if !ok {
			http.NotFound(w, r)

			return
		}

		consumerID := before
		if consumerID == "" {
			http.NotFound(w, r)

			return
		}

		e, err := ensureConsumer(r, consumerID)
		if err != nil {
			log.Error().Str("consumer", consumerID).Err(err).Msg("ensureConsumer")
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)

			return
		}

		// Wait briefly for the muxer to be assigned by the factory goroutine.
		deadline := time.Now().Add(2 * time.Second)

		for {
			consumersMu.RLock()

			mx := e.muxer

			consumersMu.RUnlock()

			if mx != nil {
				mx.Handler(hlsPrefix+consumerID).ServeHTTP(w, r)

				return
			}

			if time.Now().After(deadline) {
				http.Error(w, "stream not ready", http.StatusServiceUnavailable)

				return
			}

			time.Sleep(50 * time.Millisecond)
		}
	})

	// ── edgeview (live/playback/health endpoints) ────────────────────────

	viewSvc := edgeview.NewService(log.Logger, sm, nil, nil)
	viewHandler := edgeview.NewHTTPHandler(viewSvc, log.Logger, "")
	chiRouter := viewHandler.Router()

	// Compose: ServeMux handles /hls/; edgeview chi router
	// handles /health, /api/cameras, and unified streaming endpoints. ServeMux returns
	// 404 for unmatched patterns, so we wrap with a fallback.
	httpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(rec, r)

		if rec.status == http.StatusNotFound && !rec.written {
			chiRouter.ServeHTTP(w, r)
		}
	})

	addr := fmt.Sprintf(":%d", cfg.API.Listen)
	log.Info().
		Str("appName", appName).
		Str("appMode", appMode).
		Str("addr", addr).
		Str("rtsp_url", sourceID).
		Msg("edge node starting")

	srv := &http.Server{
		Addr:              addr,
		Handler:           corsMiddleware(httpHandler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()

		_ = srv.Shutdown(shutCtx) //nolint:contextcheck
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	lifecycle.WaitForTerminationRequest(errChan)
	cancel()

	return nil
}

// statusRecorder wraps http.ResponseWriter to capture the status code
// without writing to the underlying writer when the status is 404.
// This enables fallback routing from ServeMux to chi.
type statusRecorder struct {
	http.ResponseWriter

	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	if code != http.StatusNotFound {
		r.written = true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == http.StatusNotFound && !r.written {
		return len(b), nil // swallow 404 body from ServeMux
	}

	r.written = true

	return r.ResponseWriter.Write(b)
}
