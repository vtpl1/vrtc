package edgeview

import (
	"context"
	"fmt"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/vtpl1/vrtc/pkg/pva/persistence"
)

// WithAnalyticsReader sets the persistence reader for historical analytics endpoints.
func WithAnalyticsReader(r *persistence.Reader) HTTPHandlerOption {
	return func(h *HTTPHandler) { h.analyticsReader = r }
}

func (h *HTTPHandler) registerAnalyticsOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getCameraAnalytics",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/analytics",
		Summary:     "Query frame analytics by time range",
		Tags:        []string{"Analytics"},
	}, h.humaGetAnalytics)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraAnalyticsCounts",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/analytics/counts",
		Summary:     "Aggregated analytics counts bucketed by interval",
		Tags:        []string{"Analytics"},
	}, h.humaGetAnalyticsCounts)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraAnalyticsTrack",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/analytics/tracks/{trackId}",
		Summary:     "Frames containing a tracked object",
		Tags:        []string{"Analytics"},
	}, h.humaGetAnalyticsTrack)

	huma.Register(api, huma.Operation{
		OperationID: "getCameraAnalyticsEvents",
		Method:      "GET",
		Path:        "/api/cameras/{cameraId}/analytics/events",
		Summary:     "Event-flagged analytics frames",
		Tags:        []string{"Analytics"},
	}, h.humaGetAnalyticsEvents)
}

// ─── Input / Output types ───────────────────────────────────────────────────

//nolint:tagalign,golines // Huma query/path tags are easier to maintain without padded alignment.
type analyticsInput struct {
	paginatedInput

	CameraID      string `doc:"Camera identifier"                    path:"cameraId"`
	Start         string `doc:"Start time (RFC3339, default: 24h ago)" query:"start"`
	End           string `doc:"End time (RFC3339, default: now)"       query:"end"`
	ClassID       string `doc:"Filter by detection class ID"           query:"classId"`
	MinConfidence string `doc:"Minimum detection confidence (0-100)"   query:"minConfidence"`
}

type analyticsOutput struct {
	Body struct {
		Items      []persistence.FrameWithDetections `json:"items"`
		TotalCount int                               `json:"totalCount"`
		Limit      int                               `json:"limit"`
		Offset     int                               `json:"offset"`
	}
}

//nolint:tagalign,golines // Huma query/path tags are easier to maintain without padded alignment.
type analyticsCountsInput struct {
	CameraID string `doc:"Camera identifier"                          path:"cameraId"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)"    query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"          query:"end"`
	Interval int    `doc:"Bucket interval in seconds (default: 60)" query:"interval"`
}

type analyticsCountsOutput struct {
	Body struct {
		Items []persistence.CountBucket `json:"items"`
	}
}

//nolint:tagalign,golines // Huma query/path tags are easier to maintain without padded alignment.
type analyticsTrackInput struct {
	paginatedInput

	CameraID string `doc:"Camera identifier"                    path:"cameraId"`
	TrackID  int64  `doc:"Track identifier"                     path:"trackId"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)" query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"       query:"end"`
}

type analyticsTrackOutput struct {
	Body struct {
		Items      []persistence.FrameWithDetections `json:"items"`
		TotalCount int                               `json:"totalCount"`
		Limit      int                               `json:"limit"`
		Offset     int                               `json:"offset"`
	}
}

//nolint:tagalign,golines // Huma query/path tags are easier to maintain without padded alignment.
type analyticsEventsInput struct {
	paginatedInput

	CameraID string `doc:"Camera identifier"                    path:"cameraId"`
	Start    string `doc:"Start time (RFC3339, default: 24h ago)" query:"start"`
	End      string `doc:"End time (RFC3339, default: now)"       query:"end"`
}

type analyticsEventsOutput struct {
	Body struct {
		Items      []persistence.FrameWithDetections `json:"items"`
		TotalCount int                               `json:"totalCount"`
		Limit      int                               `json:"limit"`
		Offset     int                               `json:"offset"`
	}
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (h *HTTPHandler) humaGetAnalytics(
	ctx context.Context,
	input *analyticsInput,
) (*analyticsOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("invalid time range: %v", err))
	}

	classID, hasClassID, err := parseOptionalInt(input.ClassID, "classId")
	if err != nil {
		return nil, err
	}

	minConfidence, hasMinConfidence, err := parseOptionalInt(input.MinConfidence, "minConfidence")
	if err != nil {
		return nil, err
	}

	opts := persistence.QueryOpts{
		Limit:  input.effectiveLimit(),
		Offset: input.Offset,
	}
	if hasClassID {
		opts.ClassID = &classID
	}

	if hasMinConfidence {
		opts.MinConfidence = &minConfidence
	}

	frames, total, err := h.analyticsReader.QueryFrames(ctx, input.CameraID, start, end, opts)
	if err != nil {
		return nil, fmt.Errorf("query analytics: %w", err)
	}

	out := &analyticsOutput{}
	out.Body.Items = frames
	out.Body.TotalCount = total
	out.Body.Limit = input.effectiveLimit()
	out.Body.Offset = input.Offset

	if out.Body.Items == nil {
		out.Body.Items = []persistence.FrameWithDetections{}
	}

	return out, nil
}

func parseOptionalInt(value, field string) (int, bool, error) {
	if value == "" {
		return 0, false, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false, huma.Error400BadRequest(fmt.Sprintf("invalid %s: expected integer", field))
	}

	return parsed, true, nil
}

func (h *HTTPHandler) humaGetAnalyticsCounts(
	ctx context.Context,
	input *analyticsCountsInput,
) (*analyticsCountsOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("invalid time range: %v", err))
	}

	interval := input.Interval
	if interval <= 0 {
		interval = 60
	}

	buckets, err := h.analyticsReader.CountsByInterval(ctx, input.CameraID, start, end, interval)
	if err != nil {
		return nil, fmt.Errorf("query analytics counts: %w", err)
	}

	out := &analyticsCountsOutput{}
	out.Body.Items = buckets

	if out.Body.Items == nil {
		out.Body.Items = []persistence.CountBucket{}
	}

	return out, nil
}

func (h *HTTPHandler) humaGetAnalyticsTrack(
	ctx context.Context,
	input *analyticsTrackInput,
) (*analyticsTrackOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("invalid time range: %v", err))
	}

	opts := persistence.QueryOpts{
		Limit:  input.effectiveLimit(),
		Offset: input.Offset,
	}

	frames, total, err := h.analyticsReader.SearchByTrackID(
		ctx, input.CameraID, input.TrackID, start, end, opts,
	)
	if err != nil {
		return nil, fmt.Errorf("search analytics by track: %w", err)
	}

	out := &analyticsTrackOutput{}
	out.Body.Items = frames
	out.Body.TotalCount = total
	out.Body.Limit = input.effectiveLimit()
	out.Body.Offset = input.Offset

	if out.Body.Items == nil {
		out.Body.Items = []persistence.FrameWithDetections{}
	}

	return out, nil
}

func (h *HTTPHandler) humaGetAnalyticsEvents(
	ctx context.Context,
	input *analyticsEventsInput,
) (*analyticsEventsOutput, error) {
	start, end, err := parseOptionalTimeRange(input.Start, input.End)
	if err != nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("invalid time range: %v", err))
	}

	opts := persistence.QueryOpts{
		Limit:      input.effectiveLimit(),
		Offset:     input.Offset,
		EventsOnly: true,
	}

	frames, total, err := h.analyticsReader.QueryFrames(ctx, input.CameraID, start, end, opts)
	if err != nil {
		return nil, fmt.Errorf("search analytics events: %w", err)
	}

	out := &analyticsEventsOutput{}
	out.Body.Items = frames
	out.Body.TotalCount = total
	out.Body.Limit = input.effectiveLimit()
	out.Body.Offset = input.Offset

	if out.Body.Items == nil {
		out.Body.Items = []persistence.FrameWithDetections{}
	}

	return out, nil
}
