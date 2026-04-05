package edgeview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/format/fmp4"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/pkg/metrics"
)

var (
	errInvalidTimeRange    = errors.New("end must be greater than or equal to start")
	errInvalidStartRFC3339 = errors.New("invalid start, expected RFC3339")
	errInvalidEndRFC3339   = errors.New("invalid end, expected RFC3339")
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
	collector      *metrics.Collector
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

// WithMetricsCollector enables KPI metrics collection.
func WithMetricsCollector(c *metrics.Collector) HTTPHandlerOption {
	return func(h *HTTPHandler) { h.collector = c }
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

	if h.collector != nil {
		r.Use(metrics.ChiMiddleware(h.collector))
	}

	// ── Huma API (auto-generated OpenAPI + /docs UI) ────────────────────
	cfg := huma.DefaultConfig("Edge View API", "1.0.0")
	cfg.Info.Description = "LAN-accessible API for live view, recorded playback, and camera management."
	api := humachi.New(r, cfg)

	// Define Bearer auth once; apply it globally so every operation
	// inherits it (individual ops can override with an empty Security
	// slice to opt out, e.g. health checks).
	if api.OpenAPI().Components == nil {
		api.OpenAPI().Components = &huma.Components{}
	}

	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth": {
			Type:   "http",
			Scheme: "bearer",
		},
	}

	api.OpenAPI().Security = []map[string][]string{
		{"bearerAuth": {}},
	}

	// JSON endpoints — registered with explicit operation metadata.
	h.registerJSONOps(api)
	h.registerCameraOps(api)

	// CSV import/export (raw chi handlers — binary content, not JSON).
	h.registerCameraCSVRoutes(r)

	// Document streaming endpoints in OpenAPI (handled by raw chi handlers below).
	h.registerStreamingDocs(api)

	// Redirect trailing-slash docs URL to the canonical path.
	r.Get("/docs/", http.RedirectHandler("/docs", http.StatusMovedPermanently).ServeHTTP)

	// ── Streaming endpoints (raw chi — not auto-documentable) ───────────
	r.Get("/api/cameras/{cameraId}/stream", h.httpStream)
	r.HandleFunc("/api/cameras/ws/stream", h.wsStream)

	return r
}

func (h *HTTPHandler) registerJSONOps(api huma.API) {
	h.registerCameraViewOps(api)
	h.registerStatsOps(api)
	h.registerHealthOps(api)
}

func (h *HTTPHandler) registerCameraViewOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listCameras",
		Method:      "GET",
		Path:        "/api/cameras",
		Summary:     "List all cameras on this edge device",
		Tags:        []string{"Camera"},
	}, h.humaListCameras)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraTimeline",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/timeline",
		Summary:     "Get recording timeline for a camera",
		Tags:        []string{"Camera"},
	}, h.humaGetTimeline)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraRecordings",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/recordings",
		Summary:     "Get recording segments for timebar display",
		Tags:        []string{"Camera"},
	}, h.humaRecordingTimeline)
}

func (h *HTTPHandler) registerStatsOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listCameraStats",
		Method:      "GET",
		Path:        "/api/cameras/stats",
		Summary:     "Per-camera stream ingestion metrics",
		Tags:        []string{"Camera"},
	}, h.humaProducerStats)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraStats",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/stats",
		Summary:     "Stream ingestion metrics for a single camera",
		Tags:        []string{"Camera"},
	}, h.humaCameraStatsByID)

	huma.Register(api, huma.Operation{
		OperationID: "getSystemStats",
		Method:      "GET",
		Path:        "/api/cameras/stats/summary",
		Summary:     "Aggregated system-wide camera stats",
		Tags:        []string{"Camera"},
	}, h.humaSystemStats)

	if h.collector != nil {
		huma.Register(api, huma.Operation{
			OperationID: "getMetrics",
			Method:      "GET",
			Path:        "/api/metrics",
			Summary:     "KPI metrics with histograms and system snapshots",
			Tags:        []string{"System"},
		}, h.humaMetrics)
	}
}

func (h *HTTPHandler) registerHealthOps(api huma.API) {
	noAuth := []map[string][]string{{}} // override global security — no auth required

	huma.Register(api, huma.Operation{
		OperationID: "getHealthz",
		Method:      "GET",
		Path:        "/healthz",
		Summary:     "Basic health check",
		Tags:        []string{"System"},
		Security:    noAuth,
	}, h.humaHealthz)

	huma.Register(api, huma.Operation{
		OperationID: "getHealth",
		Method:      "GET",
		Path:        "/health",
		Summary:     "System health stats",
		Tags:        []string{"System"},
		Security:    noAuth,
	}, h.humaHealth)
}

// registerStreamingDocs adds streaming/WebSocket endpoints to the OpenAPI spec
// as documentation-only entries. The actual handlers are on the chi router.
//

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
		OperationID: "streamCamera",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/stream",
		Summary:     "fMP4 video stream (live + recorded)",
		Description: "Unified HTTP chunked fMP4 stream. Omit start for live; provide start (RFC3339) for recorded playback.",
		Tags:        []string{"Camera"},
		Parameters: []*huma.Param{
			{Name: "cameraId", In: "path", Required: true, Schema: &huma.Schema{Type: "string"}},
			{
				Name:        "start",
				In:          "query",
				Description: "Start time (RFC3339). Omit for live mode.",
				Schema:      &huma.Schema{Type: "string", Format: "date-time"},
			},
		},
		Responses: streamResp,
	})

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "streamCameraWs",
		Method:      "GET",
		Path:        "/api/cameras/ws/stream",
		Summary:     "WebSocket MSE stream (live + recorded)",
		Description: "Unified WebSocket endpoint. Omit start for live; provide start (RFC3339) for recorded playback. " +
			"Seek commands switch between modes transparently. Send {\"type\":\"mse\"} to start streaming.",
		Tags: []string{"Camera"},
		Parameters: []*huma.Param{
			{Name: "cameraId", In: "query", Required: true, Schema: &huma.Schema{Type: "string"}},
			{
				Name:        "start",
				In:          "query",
				Description: "Start time (RFC3339). Omit for live mode.",
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
		if r.URL.Path == "/healthz" || r.URL.Path == "/health" ||
			r.URL.Path == "/docs" || r.URL.Path == "/docs/" ||
			r.URL.Path == "/openapi.json" || r.URL.Path == "/openapi.yaml" {
			next.ServeHTTP(w, r)

			return
		}

		// If no auth token configured, allow all.
		if h.authToken == "" {
			next.ServeHTTP(w, r)

			return
		}

		// RFC 6750: prefer Authorization: Bearer header.
		// Allow ?token= query param only for WebSocket upgrades (browsers
		// cannot set custom headers on the WS handshake).
		token := r.Header.Get("Authorization")
		if token == "" && isWebSocketUpgrade(r) {
			token = "Bearer " + r.URL.Query().Get("token")
		}

		if token != "Bearer "+h.authToken {
			w.Header().Set("WWW-Authenticate", `Bearer realm="edge-view"`)
			writeProblem(w, http.StatusUnauthorized, "missing or invalid bearer token")

			return
		}

		next.ServeHTTP(w, r)
	})
}

// isWebSocketUpgrade returns true when the request carries the standard
// WebSocket upgrade headers (Connection: Upgrade + Upgrade: websocket).
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// writeProblem writes an RFC 9457 problem+json response. Huma-registered
// operations produce this automatically; this helper covers raw chi handlers
// (auth, CSV, streaming) so every error surface uses the same format.
// problemDetail is the RFC 9457 problem detail structure used by writeProblem.
type problemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	body, err := json.Marshal(problemDetail{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
	if err != nil {
		http.Error(w, detail, status)

		return
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ── Huma I/O types ──────────────────────────────────────────────────────────

// paginatedInput provides common pagination query params for list endpoints.
type paginatedInput struct {
	Limit  int `doc:"Maximum items per page (default: 100, max: 1000)" maximum:"1000" minimum:"1" query:"limit"`
	Offset int `doc:"Number of items to skip (default: 0)"                            minimum:"0" query:"offset"`
}

func (p paginatedInput) effectiveLimit() int {
	if p.Limit <= 0 {
		return 100
	}

	if p.Limit > 1000 {
		return 1000
	}

	return p.Limit
}

// listCamerasInput captures pagination params for the cameras list.
type listCamerasInput struct {
	paginatedInput
}

// camerasOutput wraps the camera list for Huma.
type camerasOutput struct {
	Body struct {
		Items      []*CameraInfo `json:"items"`
		TotalCount int           `json:"totalCount"`
		Limit      int           `json:"limit"`
		Offset     int           `json:"offset"`
	}
}

// timelineInput captures path and query parameters for the timeline endpoint.
type timelineInput struct {
	paginatedInput

	CameraID string `doc:"Camera identifier"                      path:"cameraId"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)"                 query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"                       query:"end"`
}

// timelineSummary is a simplified timeline entry for the /api/timeline response.
type timelineSummary struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"durationMs"`
	HasEvents  bool      `json:"hasEvents"`
}

// timelineOutput wraps the timeline entries for Huma.
type timelineOutput struct {
	Body struct {
		Items      []timelineSummary `json:"items"`
		TotalCount int               `json:"totalCount"`
		Limit      int               `json:"limit"`
		Offset     int               `json:"offset"`
	}
}

// recordingTimelineInput captures path and query parameters for the recordings endpoint.
type recordingTimelineInput struct {
	paginatedInput

	CameraID string `doc:"Camera identifier"                      path:"cameraId"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)"                 query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"                       query:"end"`
}

// recordingTimelineOutput wraps the recording timeline entries for Huma.
type recordingTimelineOutput struct {
	Body struct {
		Items      []TimelineEntry `json:"items"`
		TotalCount int             `json:"totalCount"`
		Limit      int             `json:"limit"`
		Offset     int             `json:"offset"`
	}
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
	Body struct {
		Items      []av.RelayStats `json:"items"`
		TotalCount int             `json:"totalCount"`
	}
}

// ── Huma handlers ───────────────────────────────────────────────────────────

func (h *HTTPHandler) humaListCameras(
	ctx context.Context,
	input *listCamerasInput,
) (*camerasOutput, error) {
	all := h.svc.ListCameras(ctx)
	total := len(all)
	limit := input.effectiveLimit()
	offset := min(input.Offset, total)

	end := min(offset+limit, total)

	out := &camerasOutput{}
	out.Body.Items = all[offset:end]
	out.Body.TotalCount = total
	out.Body.Limit = limit
	out.Body.Offset = offset

	return out, nil
}

func (h *HTTPHandler) humaGetTimeline(
	ctx context.Context,
	input *timelineInput,
) (*timelineOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	queryStart := time.Now()

	entries, tErr := h.svc.Timeline(ctx, input.CameraID, start, end)
	if tErr != nil {
		return nil, huma.Error500InternalServerError(tErr.Error())
	}

	if h.collector != nil {
		h.collector.RecordTimelineQuery(time.Since(queryStart), input.CameraID)
	}

	result := make([]timelineSummary, len(entries))
	for i, e := range entries {
		result[i] = timelineSummary{
			Start:      e.StartTime,
			End:        e.EndTime,
			DurationMs: e.EndTime.Sub(e.StartTime).Milliseconds(),
			HasEvents:  e.HasEvents,
		}
	}

	total := len(result)
	limit := input.effectiveLimit()

	offset := min(input.Offset, total)

	endIdx := min(offset+limit, total)

	out := &timelineOutput{}
	out.Body.Items = result[offset:endIdx]
	out.Body.TotalCount = total
	out.Body.Limit = limit
	out.Body.Offset = offset

	return out, nil
}

func (h *HTTPHandler) humaRecordingTimeline(
	ctx context.Context,
	input *recordingTimelineInput,
) (*recordingTimelineOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	queryStart := time.Now()

	entries, tErr := h.svc.Timeline(ctx, input.CameraID, start, end)
	if tErr != nil {
		return nil, huma.Error500InternalServerError(tErr.Error())
	}

	if h.collector != nil {
		h.collector.RecordTimelineQuery(time.Since(queryStart), input.CameraID)
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

	total := len(result)
	limit := input.effectiveLimit()

	offset := min(input.Offset, total)

	endIdx := min(offset+limit, total)

	out := &recordingTimelineOutput{}
	out.Body.Items = result[offset:endIdx]
	out.Body.TotalCount = total
	out.Body.Limit = limit
	out.Body.Offset = offset

	return out, nil
}

func (h *HTTPHandler) humaHealthz(_ context.Context, _ *struct{}) (*healthzOutput, error) {
	out := &healthzOutput{}
	out.Body.Status = "ok"

	return out, nil
}

func (h *HTTPHandler) humaHealth(ctx context.Context, _ *struct{}) (*healthOutput, error) {
	return &healthOutput{Body: h.collectHealth(ctx)}, nil
}

// metricsInput captures optional query params for the metrics endpoint.
type metricsInput struct {
	Since string `doc:"Lookback duration (e.g. 1h, 30m). Default: 1h" query:"since"`
}

// metricsOutput wraps the metrics response for Huma.
type metricsOutput struct {
	Body metrics.MetricsResponse
}

func (h *HTTPHandler) humaMetrics(
	ctx context.Context,
	input *metricsInput,
) (*metricsOutput, error) {
	since := time.Hour

	if input.Since != "" {
		if d, err := time.ParseDuration(input.Since); err == nil {
			since = d
		}
	}

	resp, err := h.collector.Store().Query(ctx, metrics.QueryOpts{Since: since})
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}

	resp.Uptime = h.collector.Uptime().Round(time.Second).String()
	resp.Relays = h.collector.RelayMetrics(ctx)

	return &metricsOutput{Body: *resp}, nil
}

// cameraStatsInput captures the cameraId path param for per-camera stats.
type cameraStatsInput struct {
	CameraID string `doc:"Camera identifier" path:"cameraId"`
}

// cameraStatsOutput wraps a single relay's stats.
type cameraStatsOutput struct {
	Body av.RelayStats
}

func (h *HTTPHandler) humaCameraStatsByID(
	ctx context.Context,
	input *cameraStatsInput,
) (*cameraStatsOutput, error) {
	stats, ok := h.svc.Hub().GetRelayStatsByID(ctx, input.CameraID)
	if !ok {
		return nil, huma.Error404NotFound("camera not streaming")
	}

	return &cameraStatsOutput{Body: stats}, nil
}

func (h *HTTPHandler) humaProducerStats(
	ctx context.Context,
	_ *struct{},
) (*producerStatsOutput, error) {
	all := h.svc.Hub().GetRelayStats(ctx)
	out := &producerStatsOutput{}
	out.Body.Items = all
	out.Body.TotalCount = len(all)

	return out, nil
}

// systemStatsOutput wraps the aggregated system stats for Huma.
type systemStatsOutput struct {
	Body SystemStats
}

// SystemStats aggregates metrics across all cameras.
type SystemStats struct {
	TotalCameras     int     `json:"totalCameras"`
	StreamingCameras int     `json:"streamingCameras"`
	RecordingCameras int     `json:"recordingCameras"`
	TotalPacketsRead uint64  `json:"totalPacketsRead"`
	TotalBytesRead   uint64  `json:"totalBytesRead"`
	TotalKeyFrames   uint64  `json:"totalKeyFrames"`
	TotalDropped     uint64  `json:"totalDropped"`
	AvgFPS           float64 `json:"avgFps"`
	TotalBitrateBps  float64 `json:"totalBitrateBps"`
	ActiveSegments   int     `json:"activeSegments"`
	ActiveViewers    int     `json:"activeViewers"`
}

func (h *HTTPHandler) humaSystemStats(
	ctx context.Context,
	_ *struct{},
) (*systemStatsOutput, error) {
	relays := h.svc.Hub().GetRelayStats(ctx)
	cameras := h.svc.ListCameras(ctx)

	var stats SystemStats

	stats.TotalCameras = len(cameras)

	for _, c := range cameras {
		if c.Recording {
			stats.RecordingCameras++
		}
	}

	var fpsSum float64

	for _, rs := range relays {
		stats.StreamingCameras++
		stats.TotalPacketsRead += rs.PacketsRead
		stats.TotalBytesRead += rs.BytesRead
		stats.TotalKeyFrames += rs.KeyFrames
		stats.TotalDropped += rs.DroppedPackets
		stats.TotalBitrateBps += rs.BitrateBps
		fpsSum += rs.ActualFPS
	}

	if stats.StreamingCameras > 0 {
		stats.AvgFPS = fpsSum / float64(stats.StreamingCameras)
	}

	if h.segmentCounter != nil {
		stats.ActiveSegments = h.segmentCounter.ActiveCount()
	}

	stats.ActiveViewers = h.svc.ViewerCount()

	return &systemStatsOutput{Body: stats}, nil
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

// httpStream serves an fMP4-over-HTTP stream. Without a start query param it
// delivers live video from the main relay hub; with start it plays recorded
// segments via a per-session relay hub.
func (h *HTTPHandler) httpStream(w http.ResponseWriter, r *http.Request) {
	cameraID := chi.URLParam(r, "cameraId")
	if cameraID == "" {
		writeProblem(w, http.StatusBadRequest, "missing cameraId")

		return
	}

	start, err := parsePlaybackStart(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())

		return
	}

	if start.IsZero() {
		h.httpStreamLive(w, r, cameraID)
	} else {
		h.httpStreamRecorded(w, r, cameraID, start)
	}
}

func (h *HTTPHandler) httpStreamLive(
	w http.ResponseWriter,
	r *http.Request,
	cameraID string,
) {
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")

	flusher, _ := w.(http.Flusher)

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		return fmp4.NewMuxer(&flushWriter{w: w, f: flusher}), nil
	})

	defer h.svc.TrackConsumer()()

	ctx := r.Context()
	errCh := make(chan error, 1)
	consumeStart := time.Now()

	handle, err := h.svc.Hub().Consume(ctx, cameraID, av.ConsumeOptions{
		MuxerFactory: muxerFactory,
		ErrChan:      errCh,
	})
	if err != nil {
		writeProblem(w, http.StatusNotFound, err.Error())

		return
	}

	if h.collector != nil {
		h.collector.RecordLiveViewStartup(time.Since(consumeStart), cameraID)
	}

	defer func() { _ = handle.Close(ctx) }()

	select {
	case <-ctx.Done():
	case muxErr := <-errCh:
		h.log.Warn().Err(muxErr).Str("camera", cameraID).Msg("stream: live muxer error")
	}
}

func (h *HTTPHandler) httpStreamRecorded(
	w http.ResponseWriter,
	r *http.Request,
	cameraID string,
	start time.Time,
) {
	ctx := r.Context()

	playSM := relayhub.New(
		h.svc.RecordedDemuxerFactory(cameraID, start, time.Time{}),
		nil,
		relayhub.WithMaxConsumers(1),
	)
	if err := playSM.Start(ctx); err != nil {
		writeProblem(w, http.StatusInternalServerError, err.Error())

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

	errCh := make(chan error, 1)
	consumeStart := time.Now()

	handle, err := playSM.Consume(ctx, cameraID, av.ConsumeOptions{
		MuxerFactory: muxerFactory,
		ErrChan:      errCh,
	})
	if err != nil {
		writeProblem(w, http.StatusNotFound, err.Error())

		return
	}

	if h.collector != nil {
		h.collector.RecordPlaybackStartup(time.Since(consumeStart), cameraID)
	}

	defer func() { _ = handle.Close(ctx) }()

	select {
	case <-ctx.Done():
	case <-done:
	case muxErr := <-errCh:
		h.log.Warn().Err(muxErr).Str("camera", cameraID).Msg("stream: playback muxer error")
	}
}

// ── Time parsing ────────────────────────────────────────────────────────────

// parsePlaybackStart extracts the optional start query param for playback.
// A zero time means live mode; a non-zero time starts recorded playback in
// follow mode (matching the WebSocket endpoint behaviour).
func parsePlaybackStart(r *http.Request) (time.Time, error) {
	s := r.URL.Query().Get("start")
	if s == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errInvalidStartRFC3339
	}

	return t, nil
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
