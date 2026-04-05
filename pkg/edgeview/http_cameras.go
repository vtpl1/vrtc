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
		Items      []channel.Channel `json:"items"`
		TotalCount int               `json:"totalCount"`
		Limit      int               `json:"limit"`
		Offset     int               `json:"offset"`
	}
}

type listCameraConfigsInput struct {
	paginatedInput
}

type channelIDInput struct {
	ID string `doc:"Channel identifier" path:"id"`
}

type channelOutput struct {
	Body *channelResponse
}

type channelResponse struct {
	channel.Channel

	StreamURL string `json:"streamUrl"`
	WSURL     string `json:"wsUrl"`
}

func newChannelResponse(ch channel.Channel) *channelResponse {
	return &channelResponse{
		Channel:   ch,
		StreamURL: "/api/cameras/" + ch.ID + "/stream",
		WSURL:     "/api/cameras/ws/stream?cameraId=" + ch.ID,
	}
}

type saveChannelInput struct {
	ID   string `doc:"Channel identifier" path:"id"`
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
		OperationID: "listCameraConfigs",
		Method:      "GET",
		Path:        "/api/cameras/config",
		Summary:     "List all registered cameras with full details",
		Description: "Returns a paginated list of all registered camera channel configurations including " +
			"stream URL, credentials, site ID, and extra metadata fields. " +
			"Unlike the `/api/cameras` endpoint which returns live status, " +
			"this returns the persisted configuration.",
		Tags: []string{"Camera"},
	}, func(ctx context.Context, input *listCameraConfigsInput) (*channelListOutput, error) {
		channels, err := cw.ListChannels(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list cameras", err)
		}

		limit := input.effectiveLimit()
		offset := input.Offset

		total := len(channels)
		if offset > total {
			offset = total
		}

		end := min(offset+limit, total)

		out := &channelListOutput{}
		out.Body.Items = channels[offset:end]
		out.Body.TotalCount = total
		out.Body.Limit = limit
		out.Body.Offset = offset

		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "upsertCamera",
		Method:      "PUT",
		Path:        "/api/cameras/{id}",
		Summary:     "Create or update a camera",
		Description: "Creates a new camera channel or updates an existing one. The channel ID in the path " +
			"is authoritative. A `streamUrl` (RTSP URL) is required. " +
			"The response includes generated convenience URLs for the HTTP stream and WebSocket endpoints.",
		Tags: []string{"Camera"},
	}, func(ctx context.Context, input *saveChannelInput) (*channelOutput, error) {
		ch := input.Body
		ch.ID = input.ID

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
		OperationID: "getCameraConfig",
		Method:      "GET",
		Path:        "/api/cameras/{id}",
		Summary:     "Get a camera by ID",
		Description: "Returns the full channel configuration for a single camera including stream URL, " +
			"credentials, site ID, extra metadata, and generated convenience URLs.",
		Tags: []string{"Camera"},
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
		OperationID: "deleteCamera",
		Method:      "DELETE",
		Path:        "/api/cameras/{id}",
		Summary:     "Delete a camera",
		Description: "Deletes a camera channel configuration. The camera is also removed from the " +
			"in-memory camera list. Returns 204 on success, 404 if the camera does not exist.",
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
		"id",
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

func (h *HTTPHandler) registerCameraCSVDocs(api huma.API) {
	if h.svc.ChannelWriter() == nil {
		return
	}

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "exportCamerasCSV",
		Method:      "GET",
		Path:        "/api/cameras/export.csv",
		Summary:     "Export camera configuration as CSV",
		Description: "Downloads all camera channel configurations as a CSV file. " +
			"Columns: id, name, ip_address, manufacturer, model, username, password, rtsp_main, rtsp_sub.",
		Tags: []string{"Camera"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Camera configuration CSV export",
				Content: map[string]*huma.MediaType{
					"text/csv": {
						Schema: &huma.Schema{Type: "string", Format: "binary"},
					},
				},
			},
		},
	})

	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "importCamerasCSV",
		Method:      "POST",
		Path:        "/api/cameras/import.csv",
		Summary:     "Import camera configuration from CSV",
		Description: "Bulk-imports camera channel configurations from a CSV file. " +
			"Expected columns: id, name, ip_address, manufacturer, model, username, password, rtsp_main, rtsp_sub. " +
			"Existing cameras with matching IDs are updated; new IDs are created.",
		Tags: []string{"Camera"},
		RequestBody: &huma.RequestBody{
			Required: true,
			Content: map[string]*huma.MediaType{
				"text/csv": {
					Schema: &huma.Schema{Type: "string", Format: "binary"},
				},
			},
		},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Import summary",
				Content: map[string]*huma.MediaType{
					"application/json": {
						Schema: &huma.Schema{
							Type: "object",
							Properties: map[string]*huma.Schema{
								"imported": {Type: "integer"},
							},
							Required: []string{"imported"},
						},
					},
				},
			},
		},
	})
}

// exportCSV writes all channels as a CSV file.
func (h *HTTPHandler) exportCSV(w http.ResponseWriter, r *http.Request) {
	cw := h.svc.ChannelWriter()

	channels, err := cw.ListChannels(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, err.Error())

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
		writeProblem(w, http.StatusBadRequest, "failed to read body")

		return
	}

	reader := csv.NewReader(bytes.NewReader(body))

	header, err := reader.Read()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "empty or invalid CSV")

		return
	}

	colIdx := buildColumnIndex(header)
	if colIdx["id"] < 0 || colIdx["rtsp_main"] < 0 {
		writeProblem(
			w,
			http.StatusBadRequest,
			"CSV must have at least 'id' and 'rtsp_main' columns",
		)

		return
	}

	imported, saveErr := h.saveCSVRecords(r.Context(), reader, colIdx)
	if saveErr != nil {
		writeProblem(w, http.StatusInternalServerError, saveErr.Error())

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
		ch.Name,
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
		"id": -1, "name": -1, "ip_address": -1, "manufacturer": -1, "model": -1,
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
		ID:        get("id"),
		Name:      get("name"),
		StreamURL: get("rtsp_main"),
		Username:  get("username"),
		Password:  get("password"),
		SiteID:    1,
		Extra:     extra,
	}
}
