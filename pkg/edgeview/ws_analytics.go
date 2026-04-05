package edgeview

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/vtpl1/vrtc/pkg/pva"
)

// wsAnalytics handles the /api/cameras/ws/analytics WebSocket endpoint.
//
// Protocol:
//   - Client connects with query param: cameraId=<id>
//   - Server streams FrameAnalytics JSON text frames as analytics arrive.
//   - Server closes the connection when the camera has no more analytics or the
//     context is cancelled.
//
// This endpoint delivers pure analytics without video, suitable for dashboards
// and the analytics-tool ingestion simulator.
func (h *HTTPHandler) wsAnalytics(w http.ResponseWriter, r *http.Request) {
	if h.analyticsHub == nil {
		http.Error(w, "analytics not enabled", http.StatusServiceUnavailable)

		return
	}

	cameraID, err := parseWSCameraID(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())

		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"*"},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Error().Err(err).Str("cameraId", cameraID).Msg("ws analytics: accept failed")

		return
	}

	defer func() { _ = conn.CloseNow() }()

	ctx := r.Context()
	ch := h.analyticsHub.Subscribe(cameraID)

	defer h.analyticsHub.Unsubscribe(cameraID, ch)

	for {
		select {
		case <-ctx.Done():
			return
		case fa, ok := <-ch:
			if !ok {
				return
			}

			data, merr := json.Marshal(fa)
			if merr != nil {
				continue
			}

			if werr := conn.Write(ctx, websocket.MessageText, data); werr != nil {
				return
			}
		}
	}
}

// WithAnalyticsHub sets the analytics hub used by the /ws/analytics endpoint.
func WithAnalyticsHub(hub *pva.AnalyticsHub) HTTPHandlerOption {
	return func(h *HTTPHandler) { h.analyticsHub = hub }
}
