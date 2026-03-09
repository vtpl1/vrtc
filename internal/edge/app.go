package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
	"github.com/vtpl1/vrtc/pkg/av/format/llhls"
	"github.com/vtpl1/vrtc/pkg/av/streammanager3"
)

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
		return fmt.Errorf("streamFilterMuxer: no supported streams")
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
func Run(siteID, nodeID string, cfg Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	producerID := cfg.Edge.StoragePath // AVF base directory (from config)

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

			slog.Info("llhls muxer created", "consumer", consumerID)

			return mx, nil
		},
	)

	muxerRemover := av.MuxerRemover(func(_ context.Context, consumerID string) error {
		consumersMu.Lock()
		delete(consumers, consumerID)
		consumersMu.Unlock()

		slog.Info("llhls muxer removed", "consumer", consumerID)

		return nil
	})

	// ── AVF continuous demuxer factory ───────────────────────────────────────

	demuxerFactory := av.DemuxerFactory(func(_ context.Context, _ string) (av.DemuxCloser, error) {
		dmx, err := avf.NewContinuous(producerID)
		if err != nil {
			return nil, fmt.Errorf("avf continuous %q: %w", producerID, err)
		}

		slog.Info("avf continuous demuxer opened", "dir", producerID)

		return dmx, nil
	})

	demuxerRemover := av.DemuxerRemover(func(_ context.Context, _ string) error {
		slog.Info("avf continuous demuxer closed", "dir", producerID)

		return nil
	})

	// ── stream manager ────────────────────────────────────────────────────────

	sm := streammanager3.New(demuxerFactory, demuxerRemover)

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

				var idle []string

				for id, e := range consumers {
					if e.idleSince() > consumerIdleTimeout {
						idle = append(idle, id)
					}
				}

				consumersMu.RUnlock()

				for _, id := range idle {
					slog.Info("removing idle consumer", "consumer", id)
					_ = sm.RemoveConsumer(ctx, producerID, id)
					// muxerRemover deletes from the map; double-delete is harmless.
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

		if err := sm.AddConsumer(r.Context(), producerID, consumerID,
			muxerFactory, muxerRemover, errCh); err != nil {
			consumersMu.Lock()
			delete(consumers, consumerID)
			consumersMu.Unlock()

			return nil, fmt.Errorf("add consumer %q: %w", consumerID, err)
		}

		slog.Info("consumer added", "consumer", consumerID, "dir", producerID)

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
			slog.Error("ensureConsumer", "consumer", consumerID, "err", err)
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

	addr := fmt.Sprintf(":%d", cfg.API.Listen)
	slog.Info("edge node starting",
		"site", siteID, "node", nodeID,
		"addr", addr, "avf_dir", producerID)

	srv := &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
	}

	go func() {
		<-ctx.Done()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()

		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	return nil
}
