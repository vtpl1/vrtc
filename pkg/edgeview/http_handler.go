package edgeview

import (
	"context"
	"errors"
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

var (
	errInvalidTimeRange    = errors.New("end must be greater than or equal to start")
	errInvalidStartRFC3339 = errors.New("invalid start, expected RFC3339")
	errInvalidEndRFC3339   = errors.New("invalid end, expected RFC3339")
)

// HealthSnapshot is the typed health response returned by /health.
type HealthSnapshot struct {
	Status         string       `example:"ok" json:"status"`
	UptimeSeconds  int64        `             json:"uptime_seconds"` //nolint:tagliatelle
	Goroutines     int          `             json:"goroutines"`
	Memory         HealthMemory `             json:"memory"`
	ActiveRelays   int          `             json:"active_relays"`   //nolint:tagliatelle
	ActiveViewers  int          `             json:"active_viewers"`  //nolint:tagliatelle
	ActiveSegments int          `             json:"active_segments"` //nolint:tagliatelle
	Timestamp      time.Time    `             json:"timestamp"`
}

// HealthMemory contains Go runtime memory counters.
type HealthMemory struct {
	AllocMB   float64 `json:"alloc_mb"`      //nolint:tagliatelle
	SysMB     float64 `json:"sys_mb"`        //nolint:tagliatelle
	HeapInuse float64 `json:"heap_inuse_mb"` //nolint:tagliatelle
	GCRuns    uint32  `json:"gc_runs"`       //nolint:tagliatelle
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

	// JSON endpoints — registered with explicit operation metadata.
	h.registerJSONOps(api)
	h.registerCameraOps(api)

	// Document streaming endpoints in OpenAPI (handled by raw chi handlers below).
	h.registerStreamingDocs(api)

	// ── Streaming endpoints (raw chi — not auto-documentable) ───────────
	r.Get("/live/{camera_id}", h.liveStream)
	r.Get("/playback/{camera_id}", h.playback)
	r.Get("/recorded/{camera_id}", h.playback)
	r.HandleFunc("/ws/live", h.wsLive)
	r.HandleFunc("/ws/recorded", h.wsRecorded)

	return r
}

func (h *HTTPHandler) registerJSONOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-view-cameras",
		Method:      "GET",
		Path:        "/api/cameras",
		Summary:     "List all cameras on this edge device",
		Tags:        []string{"View"},
	}, h.humaListCameras)

	huma.Register(api, huma.Operation{
		OperationID: "get-view-timeline",
		Method:      "GET",
		Path:        "/api/timeline/{camera_id}",
		Summary:     "Get recording timeline for a camera",
		Tags:        []string{"View"},
	}, h.humaGetTimeline)

	huma.Register(api, huma.Operation{
		OperationID: "get-recording-timeline-view",
		Method:      "GET",
		Path:        "/recordings/{camera_id}",
		Summary:     "Get recording segments for timebar display",
		Tags:        []string{"View"},
	}, h.humaRecordingTimeline)

	huma.Register(api, huma.Operation{
		OperationID: "get-healthz",
		Method:      "GET",
		Path:        "/healthz",
		Summary:     "Basic health check",
		Tags:        []string{"View"},
	}, h.humaHealthz)

	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      "GET",
		Path:        "/health",
		Summary:     "System health stats",
		Tags:        []string{"View"},
	}, h.humaHealth)

	huma.Register(api, huma.Operation{
		OperationID: "get-producer-stats",
		Method:      "GET",
		Path:        "/stats/producers",
		Summary:     "Per-stream ingestion metrics",
		Tags:        []string{"View"},
	}, h.humaProducerStats)
}

// registerStreamingDocs adds streaming/WebSocket endpoints to the OpenAPI spec
// as documentation-only entries. The actual handlers are on the chi router.
//
//nolint:funlen // OpenAPI operation definitions are inherently verbose.
func (h *HTTPHandler) registerStreamingDocs(api huma.API) {
	streamResp := map[string]*huma.Response{
		"200": {
			Description: "fMP4 video stream (video/mp4)",
			Content: map[string]*huma.MediaType{
				"video/mp4": {},
			},
		},
	}
	wsResp := map[string]*huma.Response{
		"101": {Description: "WebSocket upgrade for MSE streaming"},
	}

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "live-stream",
		Method:      "GET",
		Path:        "/live/{camera_id}",
		Summary:     "Live fMP4 video stream",
		Description: "HTTP chunked fMP4 stream for browser playback. Blocks until client disconnects.",
		Tags:        []string{"Streaming"},
		Parameters: []*huma.Param{
			{Name: "camera_id", In: "path", Required: true, Schema: &huma.Schema{Type: "string"}},
		},
		Responses: streamResp,
	})

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "playback-stream",
		Method:      "GET",
		Path:        "/playback/{camera_id}",
		Summary:     "Recorded playback fMP4 stream",
		Description: "HTTP chunked fMP4 playback for a time range. Query params: start, end (RFC3339). Omit end for follow mode.",
		Tags:        []string{"Streaming"},
		Parameters: []*huma.Param{
			{Name: "camera_id", In: "path", Required: true, Schema: &huma.Schema{Type: "string"}},
			{Name: "start", In: "query", Schema: &huma.Schema{Type: "string", Format: "date-time"}},
			{Name: "end", In: "query", Schema: &huma.Schema{Type: "string", Format: "date-time"}},
		},
		Responses: streamResp,
	})

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "recorded-stream",
		Method:      "GET",
		Path:        "/recorded/{camera_id}",
		Summary:     "Recorded playback fMP4 stream (by channel)",
		Description: "HTTP chunked fMP4 playback by channel ID. Query params: start, end (RFC3339).",
		Tags:        []string{"Streaming"},
		Parameters: []*huma.Param{
			{Name: "camera_id", In: "path", Required: true, Schema: &huma.Schema{Type: "string"}},
			{Name: "start", In: "query", Schema: &huma.Schema{Type: "string", Format: "date-time"}},
			{Name: "end", In: "query", Schema: &huma.Schema{Type: "string", Format: "date-time"}},
		},
		Responses: streamResp,
	})

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "ws-live",
		Method:      "GET",
		Path:        "/ws/live",
		Summary:     "WebSocket MSE live stream",
		Description: "WebSocket upgrade. Send {\"type\":\"mse\"} to start. Server sends binary fMP4 fragments. Query: camera_id.",
		Tags:        []string{"Streaming"},
		Parameters: []*huma.Param{
			{Name: "camera_id", In: "query", Schema: &huma.Schema{Type: "string"}},
		},
		Responses: wsResp,
	})

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "ws-recorded",
		Method:      "GET",
		Path:        "/ws/recorded",
		Summary:     "WebSocket MSE recorded playback",
		Description: "WebSocket upgrade for recorded MSE playback. Query: camera_id, start, end (RFC3339). Omit end for follow mode.",
		Tags:        []string{"Streaming"},
		Parameters: []*huma.Param{
			{Name: "camera_id", In: "query", Schema: &huma.Schema{Type: "string"}},
			{
				Name:        "start",
				In:          "query",
				Description: "Start time (RFC3339)",
				Schema:      &huma.Schema{Type: "string", Format: "date-time"},
			},
			{
				Name:        "end",
				In:          "query",
				Description: "End time (RFC3339, omit for follow mode)",
				Schema:      &huma.Schema{Type: "string", Format: "date-time"},
			},
		},
		Responses: wsResp,
	})
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
	CameraID string `doc:"Camera identifier"                      path:"camera_id"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)"                  query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"                        query:"end"`
}

// timelineEntry is a simplified timeline entry for the /api/timeline response.
type timelineEntry struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"duration_ms"` //nolint:tagliatelle
	HasEvents  bool      `json:"has_events"`  //nolint:tagliatelle
}

// timelineOutput wraps the timeline entries for Huma.
type timelineOutput struct {
	Body []timelineEntry
}

// recordingTimelineInput captures path and query parameters for the recordings endpoint.
type recordingTimelineInput struct {
	CameraID string `doc:"Camera identifier"                      path:"camera_id"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)"                  query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"                        query:"end"`
}

// recordingTimelineOutput wraps the recording timeline entries for Huma.
type recordingTimelineOutput struct {
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
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	entries, tErr := h.svc.Timeline(ctx, input.CameraID, start, end)
	if tErr != nil {
		return nil, huma.Error500InternalServerError(tErr.Error())
	}

	result := make([]timelineEntry, len(entries))
	for i, e := range entries {
		result[i] = timelineEntry{
			Start:      e.StartTime,
			End:        e.EndTime,
			DurationMs: e.EndTime.Sub(e.StartTime).Milliseconds(),
			HasEvents:  e.HasEvents,
		}
	}

	return &timelineOutput{Body: result}, nil
}

func (h *HTTPHandler) humaRecordingTimeline(
	ctx context.Context,
	input *recordingTimelineInput,
) (*recordingTimelineOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	entries, tErr := h.svc.Timeline(ctx, input.CameraID, start, end)
	if tErr != nil {
		return nil, huma.Error500InternalServerError(tErr.Error())
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

	return &recordingTimelineOutput{Body: result}, nil
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
	cameraID := chi.URLParam(r, "camera_id")
	if cameraID == "" {
		http.Error(w, "missing camera_id", http.StatusBadRequest)

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
	cameraID := chi.URLParam(r, "camera_id")
	if cameraID == "" {
		http.Error(w, "missing camera_id", http.StatusBadRequest)

		return
	}

	start, end, err := parsePlaybackRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	factory := h.svc.RecordedDemuxerFactory(cameraID, start, end)

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

	handle, err := playSM.Consume(ctx, cameraID, av.ConsumeOptions{
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

// ── Time parsing ────────────────────────────────────────────────────────────

// parsePlaybackRange extracts start/end query params for playback.
// If end is omitted, follow mode is enabled by returning a zero end time.
func parsePlaybackRange(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour)

	if s := r.URL.Query().Get("start"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, errInvalidStartRFC3339
		}

		start = t
	}

	endStr := r.URL.Query().Get("end")
	if endStr == "" {
		return start, time.Time{}, nil
	}

	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		return time.Time{}, time.Time{}, errInvalidEndRFC3339
	}

	if end.Before(start) {
		return time.Time{}, time.Time{}, errInvalidTimeRange
	}

	return start, end, nil
}

// parseOptionalTimeRange parses optional RFC3339 start/end strings, defaulting to last 24h.
func parseOptionalTimeRange(startStr, endStr string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour)
	end := now

	if startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			return time.Time{}, time.Time{}, errInvalidStartRFC3339
		}

		start = t
	}

	if endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			return time.Time{}, time.Time{}, errInvalidEndRFC3339
		}

		end = t
	}

	if end.Before(start) {
		return time.Time{}, time.Time{}, errInvalidTimeRange
	}

	return start, end, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

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
