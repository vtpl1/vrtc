package edgeview

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/fmp4"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
)

// HealthSnapshot is the typed health response returned by /health.
type HealthSnapshot struct {
	Status         string       `example:"ok" json:"status"`
	UptimeSeconds  int64        `             json:"uptimeSeconds"`
	Goroutines     int          `             json:"goroutines"`
	Memory         HealthMemory `             json:"memory"`
	ActiveRelays   int          `             json:"activeRelays"`
	ActiveViewers  int          `             json:"activeViewers"`
	ActiveSegments int          `             json:"activeSegments"`
	Timestamp      time.Time    `             json:"timestamp"`
}

// HealthMemory contains Go runtime memory counters.
type HealthMemory struct {
	AllocMB   float64 `json:"allocMb"`
	SysMB     float64 `json:"sysMb"`
	HeapInuse float64 `json:"heapInuseMb"`
	GCRuns    uint32  `json:"gcRuns"`
}

// ActiveSegmentCounter returns the number of recording segments currently in
// progress. Implementations should be goroutine-safe.
type ActiveSegmentCounter interface {
	ActiveCount() int
}

// HTTPHandler provides an HTTP server for browser-based live view and playback.
// Works on any media relay instance -- edge or cloud.
type HTTPHandler struct {
	svc            *Service
	log            zerolog.Logger
	authToken      string
	segmentCounter ActiveSegmentCounter
	startTime      time.Time
}

// NewHTTPHandler creates the HTTP handler for a media relay.
func NewHTTPHandler(
	svc *Service,
	log zerolog.Logger,
	authToken string,
	opts ...HTTPHandlerOption,
) *HTTPHandler {
	h := &HTTPHandler{
		svc:       svc,
		log:       log,
		authToken: authToken,
		startTime: time.Now(),
	}
	for _, opt := range opts {
		opt(h)
	}

	return h
}

// HTTPHandlerOption configures optional HTTPHandler dependencies.
type HTTPHandlerOption func(*HTTPHandler)

// WithSegmentCounter sets the active segment counter for /health.
func WithSegmentCounter(sc ActiveSegmentCounter) HTTPHandlerOption {
	return func(h *HTTPHandler) { h.segmentCounter = sc }
}

// Router returns a chi router with all LAN endpoints.
// JSON endpoints are registered via Huma (auto-generates OpenAPI spec at
// /openapi.json and interactive docs UI at /docs). Streaming and WebSocket
// endpoints are registered as raw chi handlers.
//

func (h *HTTPHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(h.authMiddleware)

	// ── Huma API (auto-generated OpenAPI + /docs UI) ────────────────────
	cfg := huma.DefaultConfig("Edge View API", "1.0.0")
	cfg.Info.Description = "LAN-accessible API for live view, recorded playback, and camera management."
	api := humachi.New(r, cfg)

	// JSON endpoints — fully auto-documented via Huma type introspection.
	huma.Get(api, "/api/cameras", h.humaListCameras)
	huma.Get(api, "/api/timeline/{cameraID}", h.humaGetTimeline)
	huma.Get(api, "/recordings/{channelID}", h.humaRecordingTimeline)
	huma.Get(api, "/healthz", h.humaHealthz)
	huma.Get(api, "/health", h.humaHealth)
	huma.Get(api, "/stats/producers", h.humaProducerStats)

	// ── Streaming endpoints (raw chi — not auto-documentable) ───────────
	r.Get("/live/{cameraID}", h.liveStream)
	r.Get("/playback/{cameraID}", h.playback)
	r.Get("/recorded/{channelID}", h.playback)
	r.HandleFunc("/ws/live", h.wsLive)
	r.HandleFunc("/ws/recorded", h.wsRecorded)

	return r
}

// authMiddleware validates the auth token for LAN access.
func (h *HTTPHandler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health check and docs are always allowed.
		if r.URL.Path == "/healthz" || r.URL.Path == "/docs" ||
			r.URL.Path == "/openapi.json" || r.URL.Path == "/openapi.yaml" {
			next.ServeHTTP(w, r)

			return
		}

		// If no auth token configured, allow all.
		if h.authToken == "" {
			next.ServeHTTP(w, r)

			return
		}

		token := r.Header.Get("Authorization")
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		if token != "Bearer "+h.authToken && token != h.authToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		next.ServeHTTP(w, r)
	})
}

// ── Huma I/O types ──────────────────────────────────────────────────────────

// camerasOutput wraps the camera list for Huma.
type camerasOutput struct {
	Body []*CameraInfo
}

// timelineInput captures path and query parameters for the timeline endpoint.
type timelineInput struct {
	CameraID string `doc:"Camera identifier"               path:"cameraID"`
	Start    string `doc:"Start of time window (RFC 3339)"                 example:"2026-01-01T00:00:00Z" query:"start"`
	End      string `doc:"End of time window (RFC 3339)"                   example:"2026-01-02T00:00:00Z" query:"end"`
}

// timelineEntryDTO is a simplified timeline entry for the /api/timeline response.
type timelineEntryDTO struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"durationMs"`
}

// timelineOutput wraps the timeline entries for Huma.
type timelineOutput struct {
	Body []timelineEntryDTO
}

// timebarInput captures path and query parameters for the recordings endpoint.
type timebarInput struct {
	ChannelID string `doc:"Channel identifier"              path:"channelID"`
	Start     string `doc:"Start of time window (RFC 3339)"                  example:"2026-01-01T00:00:00Z" query:"start"`
	End       string `doc:"End of time window (RFC 3339)"                    example:"2026-01-02T00:00:00Z" query:"end"`
}

// timebarOutput wraps the timeline entries for Huma.
type timebarOutput struct {
	Body []TimelineEntry
}

// healthzOutput wraps the healthz response for Huma.
type healthzOutput struct {
	Body struct {
		Status string `example:"ok" json:"status"`
	}
}

// healthOutput wraps the health response for Huma.
type healthOutput struct {
	Body HealthSnapshot
}

// producerStatsOutput wraps the producer stats for Huma.
type producerStatsOutput struct {
	Body []av.RelayStats
}

// ── Huma handlers ───────────────────────────────────────────────────────────

func (h *HTTPHandler) humaListCameras(_ context.Context, _ *struct{}) (*camerasOutput, error) {
	return &camerasOutput{Body: h.svc.ListCameras()}, nil
}

func (h *HTTPHandler) humaGetTimeline(
	ctx context.Context,
	input *timelineInput,
) (*timelineOutput, error) {
	start, end := parseTimeRangeStrings(input.Start, input.End)

	entries, err := h.svc.Timeline(ctx, input.CameraID, start, end)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}

	result := make([]timelineEntryDTO, len(entries))
	for i, e := range entries {
		result[i] = timelineEntryDTO{
			Start:      e.StartTime,
			End:        e.EndTime,
			DurationMs: e.EndTime.Sub(e.StartTime).Milliseconds(),
		}
	}

	return &timelineOutput{Body: result}, nil
}

func (h *HTTPHandler) humaRecordingTimeline(
	ctx context.Context,
	input *timebarInput,
) (*timebarOutput, error) {
	start, end := parseTimeRangeStrings(input.Start, input.End)

	entries, err := h.svc.Timeline(ctx, input.ChannelID, start, end)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}

	result := make([]TimelineEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, TimelineEntry{
			ID:         e.ID,
			Start:      e.StartTime,
			End:        e.EndTime,
			DurationMs: e.EndTime.Sub(e.StartTime).Milliseconds(),
			SizeBytes:  e.SizeBytes,
			Status:     e.Status,
			HasMotion:  e.HasMotion,
			HasObjects: e.HasObjects,
		})
	}

	return &timebarOutput{Body: result}, nil
}

func (h *HTTPHandler) humaHealthz(_ context.Context, _ *struct{}) (*healthzOutput, error) {
	out := &healthzOutput{}
	out.Body.Status = "ok"

	return out, nil
}

func (h *HTTPHandler) humaHealth(ctx context.Context, _ *struct{}) (*healthOutput, error) {
	return &healthOutput{Body: h.collectHealth(ctx)}, nil
}

func (h *HTTPHandler) humaProducerStats(
	ctx context.Context,
	_ *struct{},
) (*producerStatsOutput, error) {
	return &producerStatsOutput{Body: h.svc.Hub().GetRelayStats(ctx)}, nil
}

func (h *HTTPHandler) collectHealth(ctx context.Context) HealthSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	activeSegments := 0
	if h.segmentCounter != nil {
		activeSegments = h.segmentCounter.ActiveCount()
	}

	return HealthSnapshot{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
		Goroutines:    runtime.NumGoroutine(),
		Memory: HealthMemory{
			AllocMB:   float64(ms.Alloc) / (1 << 20),
			SysMB:     float64(ms.Sys) / (1 << 20),
			HeapInuse: float64(ms.HeapInuse) / (1 << 20),
			GCRuns:    ms.NumGC,
		},
		ActiveRelays:   h.svc.Hub().GetActiveRelayCount(ctx),
		ActiveViewers:  h.svc.ViewerCount(),
		ActiveSegments: activeSegments,
		Timestamp:      time.Now().UTC(),
	}
}

// ── Streaming handlers (raw chi — binary/WebSocket, not JSON) ───────────────

// liveStream serves an fMP4-over-HTTP live stream for browser playback.
func (h *HTTPHandler) liveStream(w http.ResponseWriter, r *http.Request) {
	cameraID := chi.URLParam(r, "cameraID")
	if cameraID == "" {
		http.Error(w, "missing cameraID", http.StatusBadRequest)

		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return fmp4.NewMuxer(&flushWriter{w: w, f: flusher}), nil
	})

	defer h.svc.TrackConsumer()()

	ctx := r.Context()

	handle, err := h.svc.Hub().Consume(ctx, cameraID, av.ConsumeOptions{
		MuxerFactory: muxerFactory,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}

	defer func() { _ = handle.Close(ctx) }()

	<-ctx.Done()
}

// playback serves recorded video for a time range as an fMP4-over-HTTP stream.
func (h *HTTPHandler) playback(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "cameraID")
	if channelID == "" {
		channelID = chi.URLParam(r, "channelID")
	}

	if channelID == "" {
		http.Error(w, "missing channelID", http.StatusBadRequest)

		return
	}

	start, end := parseTimeRange(r)

	factory := h.svc.RecordedDemuxerFactory(channelID, start, end)

	ctx := r.Context()

	playSM := relayhub.New(factory, nil)
	if err := playSM.Start(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	defer func() { _ = playSM.Stop() }()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	done := make(chan struct{})

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return &notifyMuxer{
			MuxCloser: fmp4.NewMuxer(&flushWriter{w: w, f: flusher}),
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

	defer func() { _ = handle.Close(ctx) }()

	select {
	case <-r.Context().Done():
	case <-done:
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// parseTimeRange extracts start/end query params from an HTTP request,
// defaulting to last 24h.
func parseTimeRange(r *http.Request) (time.Time, time.Time) {
	return parseTimeRangeStrings(
		r.URL.Query().Get("start"),
		r.URL.Query().Get("end"),
	)
}

// parseTimeRangeStrings parses RFC 3339 start/end strings, defaulting to last 24h.
func parseTimeRangeStrings(startStr, endStr string) (time.Time, time.Time) {
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour)
	end := now

	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			start = t
		}
	}

	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			end = t
		}
	}

	return start, end
}

// flushWriter wraps an http.ResponseWriter and flushes after each Write.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
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
