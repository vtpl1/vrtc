package edgeview

import (
	"context"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
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
		LiveURL:     "/live/" + ch.ID,
		PlaybackURL: "/playback/" + ch.ID,
		WSURL:       "/ws/live?camera_id=" + ch.ID,
		WSRecURL:    "/ws/recorded?camera_id=" + ch.ID,
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
		Path:        "/cameras",
		Summary:     "List all registered cameras",
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
		Path:          "/cameras",
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
		Path:        "/cameras/{id}",
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
		Path:          "/cameras/{id}",
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
