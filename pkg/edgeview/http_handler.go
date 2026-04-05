package edgeview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/vtpl1/vrtc-sdk/av/pva"
	"github.com/vtpl1/vrtc-sdk/av/pva/persistence"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/pkg/metrics"
)

var (
	errInvalidTimeRange    = errors.New("end must be greater than or equal to start")
	errInvalidStartRFC3339 = errors.New("invalid start, expected RFC3339")
	errInvalidEndRFC3339   = errors.New("invalid end, expected RFC3339")
	errOpenAPIOutputDir    = errors.New("openapi output directory is required")
)

// HealthSnapshot is the typed health response returned by /health.
//
//nolint:tagalign // Huma/OpenAPI tags are easier to maintain without padded alignment.
type HealthSnapshot struct {
	Status         string       `doc:"Service status (always 'ok' when reachable)"          example:"ok" json:"status"`
	UptimeSeconds  int64        `doc:"Seconds since service start"                                       json:"uptimeSeconds"`
	Goroutines     int          `doc:"Current number of Go goroutines"                                   json:"goroutines"`
	Memory         HealthMemory `doc:"Go runtime memory statistics"                                      json:"memory"`
	ActiveRelays   int          `doc:"Number of active RTSP relay connections"                           json:"activeRelays"`
	ActiveViewers  int          `doc:"Number of active live stream viewers"                              json:"activeViewers"`
	ActiveSegments int          `doc:"Number of recording segments currently being written"              json:"activeSegments"`
	Timestamp      time.Time    `doc:"Server wall-clock time (UTC)"                                      json:"timestamp"`
}

// HealthMemory contains Go runtime memory counters.
type HealthMemory struct {
	AllocMB   float64 `doc:"Allocated heap memory (MB)"             json:"allocMb"`
	SysMB     float64 `doc:"Total memory obtained from the OS (MB)" json:"sysMb"`
	HeapInuse float64 `doc:"Heap memory in use (MB)"                json:"heapInuseMb"`
	GCRuns    uint32  `doc:"Number of completed GC cycles"          json:"gcRuns"`
}

// ActiveSegmentCounter returns the number of recording segments currently in
// progress. Implementations should be goroutine-safe.
type ActiveSegmentCounter interface {
	ActiveCount() int
}

// HTTPHandler provides an HTTP server for browser-based live view and playback.
// Works on any media relay instance -- edge or cloud.
type HTTPHandler struct {
	svc             *Service
	log             zerolog.Logger
	authToken       string
	segmentCounter  ActiveSegmentCounter
	collector       *metrics.Collector
	analyticsHub    *pva.AnalyticsHub
	analyticsReader *persistence.Reader
	startTime       time.Time
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
	r, _ := h.newRouter()

	return r
}

// OpenAPI returns the generated OpenAPI specification for this handler.
func (h *HTTPHandler) OpenAPI() *huma.OpenAPI {
	_, api := h.newRouter()

	return api.OpenAPI()
}

// ExportOpenAPI writes the generated OpenAPI spec to outputDir as JSON and YAML.
func (h *HTTPHandler) ExportOpenAPI(outputDir string) error {
	if outputDir == "" {
		return errOpenAPIOutputDir
	}

	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return err
	}

	spec := h.OpenAPI()

	jsonSpec, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}

	yamlSpec, err := spec.YAML()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(outputDir, "openapi.json"), jsonSpec, 0o600); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(outputDir, "openapi.yaml"), yamlSpec, 0o600); err != nil {
		return err
	}

	return nil
}

//nolint:funlen // Huma route registration is inherently verbose.
func (h *HTTPHandler) newRouter() (*chi.Mux, huma.API) {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(h.authMiddleware)

	if h.collector != nil {
		r.Use(metrics.ChiMiddleware(h.collector))
	}

	// ── Huma API (auto-generated OpenAPI + /docs UI) ────────────────────
	cfg := huma.DefaultConfig("Edge View API", "1.0.0")
	cfg.Info.Description = "LAN-accessible API for live view, recorded playback, camera management, and analytics.\n\n" +
		"## Architecture\n\n" +
		"The edge service runs a dual-hub streaming architecture:\n\n" +
		"- **Live Hub** — real-time, zero-delay fan-out from the RTSP source with a 30-second GOP replay buffer.\n" +
		"- **Analytics Hub** — delayed by ~5 seconds; each video frame is enriched with object-detection analytics " +
		"via a BlockingMerger that waits for inference results.\n\n" +
		"## Streaming\n\n" +
		"Two transports are available for video:\n\n" +
		"- **WebSocket MSE** (`/api/cameras/ws/stream`) — bidirectional; supports live, recorded playback, " +
		"seek/skip, pause/resume, and analytics-enriched mode. Send `{\"type\":\"mse\"}` after connect to start streaming.\n" +
		"- **HTTP chunked fMP4** (`/api/cameras/{cameraId}/stream`) — unidirectional; live or recorded, no seek support.\n\n" +
		"Omit the `start` query parameter for live mode; provide an RFC3339 timestamp for recorded playback.\n\n" +
		"## WebSocket Protocol\n\n" +
		"All client commands are JSON text frames with `\"type\": \"mse\"`. Supported values: " +
		"start (`\"\"`), `\"pause\"`, `\"resume\"`, `\"seek\"` (with `time` + optional `seq`), " +
		"`\"skip\"` (with `offset` + optional `seq`).\n\n" +
		"Server sends text frames (`mse`, `seeked`, `mode_change`, `playback_info`, `timing`, `error`) " +
		"and binary frames (fMP4 init segments + media fragments). " +
		"All `wallClock` fields use RFC3339 with millisecond precision.\n\n" +
		"## Analytics\n\n" +
		"Object-detection analytics are ingested via gRPC and exposed through REST endpoints " +
		"for historical queries, time-bucketed counts, event filtering, and per-track search. " +
		"A pure JSON WebSocket stream (`/api/cameras/ws/analytics`) delivers real-time detections without video."
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
	h.registerCameraCSVDocs(api)

	// Document streaming endpoints in OpenAPI (handled by raw chi handlers below).
	h.registerStreamingDocs(api)

	// Redirect trailing-slash docs URL to the canonical path.
	r.Get("/docs/", http.RedirectHandler("/docs", http.StatusMovedPermanently).ServeHTTP)

	// ── Streaming endpoints (raw chi — not auto-documentable) ───────────
	r.Get("/api/cameras/{cameraId}/stream", h.httpStream)
	r.HandleFunc("/api/cameras/ws/stream", h.wsStream)
	r.HandleFunc("/api/cameras/ws/analytics", h.wsAnalytics)

	return r, api
}

func (h *HTTPHandler) registerJSONOps(api huma.API) {
	h.registerCameraViewOps(api)
	h.registerStatsOps(api)
	h.registerHealthOps(api)

	if h.analyticsReader != nil {
		h.registerAnalyticsOps(api)
	}
}

func (h *HTTPHandler) registerCameraViewOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listCameras",
		Method:      "GET",
		Path:        "/api/cameras",
		Summary:     "List all cameras on this edge device",
		Description: "Returns a paginated list of cameras with live status including codec, resolution, FPS, " +
			"and whether recording and analytics are active. Use this to populate a camera grid or selector UI.",
		Tags: []string{"Camera"},
	}, h.humaListCameras)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraTimeline",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/timeline",
		Summary:     "Get recording timeline for a camera",
		Description: "Returns a simplified timeline of recording availability within a time range. " +
			"Each entry spans a contiguous recording period; gaps between entries represent periods " +
			"with no recording. Use this to render a timebar/scrubber UI. " +
			"Defaults to the last 24 hours if start/end are omitted.",
		Tags: []string{"Camera"},
	}, h.humaGetTimeline)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraRecordings",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/recordings",
		Summary:     "Get recording segments for timebar display",
		Description: "Returns detailed recording segment entries including segment ID, size, status " +
			"(`complete`, `interrupted`, `corrupted`), and motion/object flags. " +
			"Gaps between entries represent periods with no recording and should be rendered " +
			"as empty/grey regions on the timeline bar. " +
			"Defaults to the last 24 hours if start/end are omitted.",
		Tags: []string{"Camera"},
	}, h.humaRecordingTimeline)
}

func (h *HTTPHandler) registerStatsOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listCameraStats",
		Method:      "GET",
		Path:        "/api/cameras/stats",
		Summary:     "Per-camera stream ingestion metrics",
		Description: "Returns per-relay ingestion stats for every active camera: packets/bytes read, " +
			"key frames, dropped packets, actual FPS, bitrate, consumer count, and stream codec info. " +
			"Useful for monitoring ingestion health and diagnosing frame-loss issues.",
		Tags: []string{"Camera"},
	}, h.humaProducerStats)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraStats",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/stats",
		Summary:     "Stream ingestion metrics for a single camera",
		Description: "Returns detailed relay stats for a specific camera including packets/bytes read, " +
			"key frames, dropped packets, actual FPS, bitrate, rotation count, and per-stream codec info. " +
			"Returns 404 if the camera is not currently streaming.",
		Tags: []string{"Camera"},
	}, h.humaCameraStatsByID)

	huma.Register(api, huma.Operation{
		OperationID: "getSystemStats",
		Method:      "GET",
		Path:        "/api/cameras/stats/summary",
		Summary:     "Aggregated system-wide camera stats",
		Description: "Returns system-wide aggregates: total/streaming/recording camera counts, " +
			"total packets/bytes/key frames/dropped, average FPS, total bitrate, " +
			"active recording segments, and active live viewers.",
		Tags: []string{"Camera"},
	}, h.humaSystemStats)

	if h.collector != nil {
		huma.Register(api, huma.Operation{
			OperationID: "getMetrics",
			Method:      "GET",
			Path:        "/api/metrics",
			Summary:     "KPI metrics with histograms and system snapshots",
			Description: "Returns KPI metrics including latency histograms (p50/p95/p99/max/avg), " +
				"counters, periodic system snapshots (goroutines, heap, relay/viewer/segment counts, FPS, bitrate), " +
				"and per-relay metrics. The `since` parameter controls the lookback window (default: 1h).",
			Tags: []string{"System"},
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
		Description: "Lightweight liveness probe. Returns `{\"status\":\"ok\"}` when the service is running. " +
			"No authentication required. Suitable for Kubernetes liveness probes and load-balancer health checks.",
		Tags:     []string{"System"},
		Security: noAuth,
	}, h.humaHealthz)

	huma.Register(api, huma.Operation{
		OperationID: "getHealth",
		Method:      "GET",
		Path:        "/health",
		Summary:     "System health stats",
		Description: "Returns a detailed health snapshot including uptime, goroutine count, " +
			"Go runtime memory stats (alloc, sys, heap in-use, GC runs), active relay count, " +
			"active live viewer count, and active recording segment count. No authentication required.",
		Tags:     []string{"System"},
		Security: noAuth,
	}, h.humaHealth)
}

// registerStreamingDocs adds streaming/WebSocket endpoints to the OpenAPI spec
// as documentation-only entries. The actual handlers are on the chi router.
//

func (h *HTTPHandler) registerStreamingDocs(api huma.API) {
	api.OpenAPI().AddOperation(h.streamCameraOperation())
	api.OpenAPI().AddOperation(h.streamCameraWSOperation())

	if h.analyticsHub != nil {
		api.OpenAPI().AddOperation(h.streamCameraAnalyticsWSOperation())
	}
}

func (h *HTTPHandler) streamCameraOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "streamCamera",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/stream",
		Summary:     "fMP4 video stream (live + recorded)",
		Description: "Unified HTTP chunked fMP4 stream. Omit `start` for live; provide `start` (RFC3339) for recorded playback.\n\n" +
			"**Live mode:** Attaches to the live relay hub with multi-consumer fan-out. " +
			"A 30-second GOP replay buffer ensures an instant keyframe on connect.\n\n" +
			"**Recorded mode:** Creates a per-session relay hub (`MaxConsumers=1`, blocking delivery — no frame drops). " +
			"A ChainingDemuxer reads fMP4 segments from disk with monotonic DTS adjustment. " +
			"When segments are exhausted, playback transitions seamlessly to the live packet buffer (follow mode).\n\n" +
			"**Limitations:** HTTP streams are unidirectional — no seek, skip, pause/resume, or analytics enrichment. " +
			"Use the WebSocket endpoint for interactive playback.",
		Tags: []string{"Camera"},
		Parameters: []*huma.Param{
			{
				Name:     "cameraId",
				In:       "path",
				Required: true,
				Schema:   &huma.Schema{Type: "string", Description: "Camera/channel identifier"},
			},
			{
				Name:        "start",
				In:          "query",
				Description: "Playback start time (RFC3339). Omit for live mode. Example: `2026-04-04T14:00:00Z`",
				Schema:      &huma.Schema{Type: "string", Format: "date-time"},
			},
		},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Chunked fMP4 video stream. The response is a continuous stream of fMP4 fragments " +
					"(init segment followed by moof+mdat pairs) delivered as chunked transfer encoding.",
				Content: map[string]*huma.MediaType{
					"video/mp4": {},
				},
			},
		},
	}
}

//nolint:funlen // Huma OpenAPI operation with comprehensive protocol documentation.
func (h *HTTPHandler) streamCameraWSOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "streamCameraWs",
		Method:      "GET",
		Path:        "/api/cameras/ws/stream",
		Summary:     "WebSocket MSE stream (live + recorded)",
		Description: "Unified WebSocket endpoint for live and recorded video playback using Media Source Extensions (MSE).\n\n" +
			"## Connection\n\n" +
			"Omit `start` for live mode; provide `start` (RFC3339) for recorded playback. " +
			"After the WebSocket upgrade, the server sends an initial mode message " +
			"(`mode_change` for live, `playback_info` for recorded). " +
			"Send `{\"type\":\"mse\"}` to start media delivery.\n\n" +
			"## Client Commands (JSON text frames, `type: \"mse\"`)\n\n" +
			"| Value | Extra Fields | Description |\n" +
			"|-------|-------------|-------------|\n" +
			"| `\"\"` | — | Start streaming (required first command) |\n" +
			"| `\"pause\"` | — | Pause recorded playback (no-op in live mode) |\n" +
			"| `\"resume\"` | — | Resume recorded playback |\n" +
			"| `\"seek\"` | `time` (RFC3339 or `\"now\"`), `seq` (int64, optional) | Absolute seek to wall-clock time |\n" +
			"| `\"skip\"` | `offset` (Go duration, e.g. `\"-30s\"`), `seq` (int64, optional) | Relative seek from current position |\n\n" +
			"## Server Messages (text frames)\n\n" +
			"| `type` | Key Fields | Description |\n" +
			"|--------|-----------|-------------|\n" +
			"| `mse` | `value` (MIME codec string) | Codec negotiation — use with `addSourceBuffer` / `changeType` |\n" +
			"| `seeked` | `wallClock`, `mode`, `codecChanged`, `codecs?`, `gap?`, `seq` | Seek completed |\n" +
			"| `mode_change` | `mode`, `wallClock` | Initial mode (live) |\n" +
			"| `playback_info` | `mode`, `actualStartWallClock`, `wallClock` | Initial mode (recorded/first_available) |\n" +
			"| `timing` | `wallClock` | Continuous wall-clock sync on every fragment flush |\n" +
			"| `error` | `error` | Error message |\n\n" +
			"## Server Messages (binary frames)\n\n" +
			"Binary frames contain fMP4 data: first an init segment (`ftyp` + `moov`), then continuous " +
			"media fragments (`moof` + `mdat`). A new init segment is sent after every codec change.\n\n" +
			"## Seek Behavior\n\n" +
			"- **Into recorded footage:** Resolves to the nearest keyframe; `mode: \"recorded\"`.\n" +
			"- **Into a gap:** Snaps to the next available segment; `gap: true`.\n" +
			"- **Beyond recordings:** Switches to live; `mode: \"live\"`.\n" +
			"- **`time: \"now\"`:** Switches to live immediately.\n" +
			"- **Codec change:** `codecChanged: true` with new `codecs` string; client should call `changeType()`.\n" +
			"- **Debouncing:** Use monotonically increasing `seq` values; the server discards stale seeks.\n\n" +
			"All `wallClock` fields use RFC3339 with millisecond precision.",
		Tags: []string{"Camera"},
		Parameters: []*huma.Param{
			{
				Name:        "cameraId",
				In:          "query",
				Required:    true,
				Description: "Camera/channel identifier",
				Schema:      &huma.Schema{Type: "string"},
			},
			{
				Name:        "start",
				In:          "query",
				Description: "Playback start time (RFC3339). Omit for live mode. Example: `2026-04-04T14:00:00Z`",
				Schema:      &huma.Schema{Type: "string", Format: "date-time"},
			},
			{
				Name: "analytics",
				In:   "query",
				Description: "Set to `true` to receive analytics-enriched frames. " +
					"In live mode, adds ~5 seconds of latency as each frame waits for detection results. " +
					"In recorded mode, analytics are injected from persistent storage with negligible overhead.",
				Schema: &huma.Schema{Type: "string", Enum: []any{"true", "false"}},
			},
		},
		Responses: map[string]*huma.Response{
			"101": {
				Description: "WebSocket upgrade successful. The server sends mode information followed by media data after the client sends the start command.",
			},
		},
	}
}

func (h *HTTPHandler) streamCameraAnalyticsWSOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "streamCameraAnalyticsWs",
		Method:      "GET",
		Path:        "/api/cameras/ws/analytics",
		Summary:     "WebSocket analytics stream (JSON, no video)",
		Description: "Pure analytics JSON stream — no video data. " +
			"Subscribes to the AnalyticsHub pub/sub and receives `FrameAnalytics` JSON text frames " +
			"as detections arrive in real-time.\n\n" +
			"Each text frame contains a full analytics payload with detections (bounding boxes, class IDs, " +
			"confidence scores, track IDs), aggregate counts (people, vehicles, objects), " +
			"and timing fields (capture, inference timestamps as RFC3339 with milliseconds).\n\n" +
			"**Delivery:** non-blocking — slow consumers drop frames. No binary frames are sent.",
		Tags: []string{"Analytics"},
		Parameters: []*huma.Param{
			{
				Name:        "cameraId",
				In:          "query",
				Required:    true,
				Description: "Camera/channel identifier",
				Schema:      &huma.Schema{Type: "string"},
			},
		},
		Responses: map[string]*huma.Response{
			"101": {
				Description: "WebSocket upgrade successful. Analytics JSON text frames are sent as detections arrive.",
			},
		},
	}
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
//

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
	Start      time.Time `doc:"Recording period start time (RFC3339)"                json:"start"`
	End        time.Time `doc:"Recording period end time (RFC3339)"                  json:"end"`
	DurationMs int64     `doc:"Recording period duration in milliseconds"            json:"durationMs"`
	HasEvents  bool      `doc:"Whether analytics events occurred during this period" json:"hasEvents"`
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
	TotalCameras     int     `doc:"Total number of registered cameras"                       json:"totalCameras"`
	StreamingCameras int     `doc:"Cameras with an active RTSP relay"                        json:"streamingCameras"`
	RecordingCameras int     `doc:"Cameras with recording enabled"                           json:"recordingCameras"`
	TotalPacketsRead uint64  `doc:"Sum of packets read across all relays"                    json:"totalPacketsRead"`
	TotalBytesRead   uint64  `doc:"Sum of bytes read across all relays"                      json:"totalBytesRead"`
	TotalKeyFrames   uint64  `doc:"Sum of key frames received across all relays"             json:"totalKeyFrames"`
	TotalDropped     uint64  `doc:"Sum of dropped packets across all relays (leaky fan-out)" json:"totalDropped"`
	AvgFPS           float64 `doc:"Average actual FPS across all streaming cameras"          json:"avgFps"`
	TotalBitrateBps  float64 `doc:"Sum of bitrate (bits/sec) across all relays"              json:"totalBitrateBps"`
	ActiveSegments   int     `doc:"Recording segments currently being written"               json:"activeSegments"`
	ActiveViewers    int     `doc:"Active live stream viewers"                               json:"activeViewers"`
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
