package edgeview

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/vtpl1/vrtc/pkg/channel"
)

// ── Camera CRUD I/O types ───────────────────────────────────────────────────

type channelListOutput struct {
	Body struct {
		Channels []channel.Channel `json:"channels"`
		Count    int               `json:"count"`
	}
}

type channelIDInput struct {
	ID string `doc:"Channel identifier" path:"id"`
}

type channelOutput struct {
	Body *channelResponse
}

type channelResponse struct {
	channel.Channel

	LiveURL     string `json:"live_url"`                  //nolint:tagliatelle
	PlaybackURL string `json:"playback_url"`              //nolint:tagliatelle
	WSURL       string `json:"ws_url"`                    //nolint:tagliatelle
	WSRecURL    string `json:"ws_recorded_url,omitempty"` //nolint:tagliatelle
}

func newChannelResponse(ch channel.Channel) *channelResponse {
	return &channelResponse{
		Channel:     ch,
		LiveURL:     "/api/cameras/" + ch.ID + "/live",
		PlaybackURL: "/api/cameras/" + ch.ID + "/playback",
		WSURL:       "/api/cameras/ws/live?camera_id=" + ch.ID,
		WSRecURL:    "/api/cameras/ws/recorded?camera_id=" + ch.ID,
	}
}

type saveChannelInput struct {
	Body channel.Channel
}

// ── Camera CRUD registration ────────────────────────────────────────────────

//nolint:funlen // Huma CRUD registration is inherently verbose.
func (h *HTTPHandler) registerCameraOps(api huma.API) {
	cw := h.svc.ChannelWriter()
	if cw == nil {
		return // no channel writer configured — skip CRUD endpoints
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-cameras",
		Method:      "GET",
		Path:        "/api/cameras/all",
		Summary:     "List all registered cameras with full details",
		Tags:        []string{"Camera"},
	}, func(ctx context.Context, _ *struct{}) (*channelListOutput, error) {
		channels, err := cw.ListChannels(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list cameras", err)
		}

		out := &channelListOutput{}
		out.Body.Channels = channels
		out.Body.Count = len(channels)

		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "save-camera",
		Method:        "PUT",
		Path:          "/api/cameras",
		Summary:       "Create or update a camera",
		Tags:          []string{"Camera"},
		DefaultStatus: 201,
	}, func(ctx context.Context, input *saveChannelInput) (*channelOutput, error) {
		ch := input.Body
		if ch.ID == "" {
			return nil, huma.Error400BadRequest("id is required")
		}

		if ch.StreamURL == "" {
			return nil, huma.Error400BadRequest("stream_url is required")
		}

		if err := cw.SaveChannel(ctx, ch); err != nil {
			return nil, huma.Error500InternalServerError("", err)
		}

		// Update the in-memory camera list.
		h.svc.RegisterCamera(&CameraInfo{
			CameraID: ch.ID,
			Name:     ch.Name,
			State:    "active",
		})

		return &channelOutput{Body: newChannelResponse(ch)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-camera",
		Method:      "GET",
		Path:        "/api/cameras/{id}",
		Summary:     "Get a camera by ID",
		Tags:        []string{"Camera"},
	}, func(ctx context.Context, input *channelIDInput) (*channelOutput, error) {
		ch, err := cw.GetChannel(ctx, input.ID)
		if err != nil {
			if errors.Is(err, channel.ErrChannelNotFound) {
				return nil, huma.Error404NotFound("camera not found")
			}

			return nil, huma.Error500InternalServerError("", err)
		}

		return &channelOutput{Body: newChannelResponse(ch)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-camera",
		Method:        "DELETE",
		Path:          "/api/cameras/{id}",
		Summary:       "Delete a camera",
		Tags:          []string{"Camera"},
		DefaultStatus: 204,
	}, func(ctx context.Context, input *channelIDInput) (*struct{}, error) {
		if err := cw.DeleteChannel(ctx, input.ID); err != nil {
			if errors.Is(err, channel.ErrChannelNotFound) {
				return nil, huma.Error404NotFound("camera not found")
			}

			if strings.Contains(err.Error(), "not found") {
				return nil, huma.Error404NotFound("camera not found")
			}

			return nil, huma.Error500InternalServerError("", err)
		}

		// Remove from in-memory camera list.
		h.svc.UnregisterCamera(input.ID)

		return nil, nil //nolint:nilnil // Huma 204 convention: nil body + nil error = success
	})
}

// csvHeader returns the column order for channel CSV import/export.
func csvHeader() []string {
	return []string{
		"name",
		"ip_address",
		"manufacturer",
		"model",
		"username",
		"password",
		"rtsp_main",
		"rtsp_sub",
	}
}

// registerCameraCSVRoutes adds raw chi handlers for CSV export/import.
// Called from Router() after the Huma API is set up.
func (h *HTTPHandler) registerCameraCSVRoutes(r chi.Router) {
	cw := h.svc.ChannelWriter()
	if cw == nil {
		return
	}

	r.Get("/api/cameras/export.csv", h.exportCSV)
	r.Post("/api/cameras/import.csv", h.importCSV)
}

// exportCSV writes all channels as a CSV file.
func (h *HTTPHandler) exportCSV(w http.ResponseWriter, r *http.Request) {
	cw := h.svc.ChannelWriter()

	channels, err := cw.ListChannels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="cameras.csv"`)

	cw2 := csv.NewWriter(w)

	_ = cw2.Write(csvHeader())

	for _, ch := range channels {
		_ = cw2.Write(channelToRecord(ch))
	}

	cw2.Flush()
}

// importCSV reads a CSV file from the request body and saves each row as a channel.
//

func (h *HTTPHandler) importCSV(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB limit
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)

		return
	}

	reader := csv.NewReader(bytes.NewReader(body))

	header, err := reader.Read()
	if err != nil {
		http.Error(w, "empty or invalid CSV", http.StatusBadRequest)

		return
	}

	colIdx := buildColumnIndex(header)
	if colIdx["name"] < 0 || colIdx["rtsp_main"] < 0 {
		http.Error(
			w,
			"CSV must have at least 'name' and 'rtsp_main' columns",
			http.StatusBadRequest,
		)

		return
	}

	imported, saveErr := h.saveCSVRecords(r.Context(), reader, colIdx)
	if saveErr != nil {
		http.Error(w, saveErr.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"imported":%d}`, imported)
}

func (h *HTTPHandler) saveCSVRecords(
	ctx context.Context,
	reader *csv.Reader,
	colIdx map[string]int,
) (int, error) {
	cw := h.svc.ChannelWriter()

	var imported int

	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return imported, fmt.Errorf("csv read: %w", readErr)
		}

		ch := recordToChannel(record, colIdx)
		if ch.ID == "" || ch.StreamURL == "" {
			continue
		}

		if saveErr := cw.SaveChannel(ctx, ch); saveErr != nil {
			return imported, fmt.Errorf("failed to save %q: %w", ch.ID, saveErr)
		}

		h.svc.RegisterCamera(&CameraInfo{
			CameraID: ch.ID,
			Name:     ch.Name,
			State:    "active",
		})

		imported++
	}

	return imported, nil
}

func channelToRecord(ch channel.Channel) []string {
	return []string{
		ch.ID,
		ch.Extra["ip_address"],
		ch.Extra["manufacturer"],
		ch.Extra["model"],
		ch.Username,
		ch.Password,
		ch.StreamURL,
		ch.Extra["rtsp_sub"],
	}
}

func buildColumnIndex(header []string) map[string]int {
	idx := map[string]int{
		"name": -1, "ip_address": -1, "manufacturer": -1, "model": -1,
		"username": -1, "password": -1, "rtsp_main": -1, "rtsp_sub": -1,
	}

	for i, col := range header {
		col = strings.TrimSpace(strings.ToLower(col))
		if _, ok := idx[col]; ok {
			idx[col] = i
		}
	}

	return idx
}

func recordToChannel(record []string, colIdx map[string]int) channel.Channel {
	get := func(key string) string {
		i := colIdx[key]
		if i < 0 || i >= len(record) {
			return ""
		}

		return strings.TrimSpace(record[i])
	}

	extra := make(map[string]string)

	if v := get("ip_address"); v != "" {
		extra["ip_address"] = v
	}

	if v := get("manufacturer"); v != "" {
		extra["manufacturer"] = v
	}

	if v := get("model"); v != "" {
		extra["model"] = v
	}

	if v := get("rtsp_sub"); v != "" {
		extra["rtsp_sub"] = v
	}

	return channel.Channel{
		ID:        get("name"),
		Name:      get("name"),
		StreamURL: get("rtsp_main"),
		Username:  get("username"),
		Password:  get("password"),
		SiteID:    1,
		Extra:     extra,
	}
}
