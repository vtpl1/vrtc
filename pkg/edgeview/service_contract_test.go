package edgeview

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

type serviceTestRelayHub struct {
	statsByID map[string]av.RelayStats
}

func (h serviceTestRelayHub) GetRelayStats(context.Context) []av.RelayStats {
	out := make([]av.RelayStats, 0, len(h.statsByID))
	for _, stat := range h.statsByID {
		out = append(out, stat)
	}

	return out
}

func (h serviceTestRelayHub) GetRelayStatsByID(_ context.Context, sourceID string) (av.RelayStats, bool) {
	stat, ok := h.statsByID[sourceID]

	return stat, ok
}

func (h serviceTestRelayHub) ListRelayIDs(context.Context) []string { return nil }

func (h serviceTestRelayHub) GetActiveRelayCount(context.Context) int { return len(h.statsByID) }

func (h serviceTestRelayHub) Consume(context.Context, string, av.ConsumeOptions) (av.ConsumerHandle, error) {
	return nil, nil
}

func (h serviceTestRelayHub) PauseRelay(context.Context, string) error { return nil }

func (h serviceTestRelayHub) ResumeRelay(context.Context, string) error { return nil }

func (h serviceTestRelayHub) Start(context.Context) error { return nil }

func (h serviceTestRelayHub) SignalStop() bool { return true }

func (h serviceTestRelayHub) WaitStop() error { return nil }

func (h serviceTestRelayHub) Stop() error { return nil }

type serviceTestIndex struct {
	entries []recorder.RecordingEntry
	queryFn func(context.Context, string, time.Time, time.Time) ([]recorder.RecordingEntry, error)
}

func (i *serviceTestIndex) Insert(context.Context, recorder.RecordingEntry) error { return nil }

func (i *serviceTestIndex) QueryByChannel(
	ctx context.Context,
	channelID string,
	from, to time.Time,
) ([]recorder.RecordingEntry, error) {
	if i.queryFn != nil {
		return i.queryFn(ctx, channelID, from, to)
	}

	out := make([]recorder.RecordingEntry, 0, len(i.entries))
	for _, e := range i.entries {
		if e.ChannelID != channelID {
			continue
		}

		if !from.IsZero() && e.EndTime.Before(from) {
			continue
		}

		if !to.IsZero() && e.StartTime.After(to) {
			continue
		}

		out = append(out, e)
	}

	return out, nil
}

func (i *serviceTestIndex) FirstAvailable(_ context.Context, channelID string) (recorder.RecordingEntry, error) {
	var (
		found bool
		best  recorder.RecordingEntry
	)

	for _, e := range i.entries {
		if e.ChannelID != channelID {
			continue
		}

		if !found || e.StartTime.Before(best.StartTime) {
			best = e
			found = true
		}
	}

	if !found {
		return recorder.RecordingEntry{}, recorder.ErrNoRecordings
	}

	return best, nil
}

func (i *serviceTestIndex) LastAvailable(_ context.Context, channelID string) (recorder.RecordingEntry, error) {
	var (
		found bool
		best  recorder.RecordingEntry
	)

	for _, e := range i.entries {
		if e.ChannelID != channelID {
			continue
		}

		if !found || e.EndTime.After(best.EndTime) {
			best = e
			found = true
		}
	}

	if !found {
		return recorder.RecordingEntry{}, recorder.ErrNoRecordings
	}

	return best, nil
}

func (i *serviceTestIndex) Delete(context.Context, string) error { return nil }

func (i *serviceTestIndex) QueryAllChannels(context.Context, time.Time, time.Time) ([]recorder.RecordingEntry, error) {
	return i.entries, nil
}

func (i *serviceTestIndex) SealInterrupted(context.Context) error { return nil }

func (i *serviceTestIndex) Close() error { return nil }

func TestResolvePlaybackStart_RecordedHit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	idx := &serviceTestIndex{
		entries: []recorder.RecordingEntry{
			{
				ID:        "seg-1",
				ChannelID: "cam-1",
				StartTime: now.Add(-10 * time.Minute),
				EndTime:   now.Add(-5 * time.Minute),
			},
		},
	}

	svc := NewService(log.Logger, serviceTestRelayHub{}, idx, nil)
	from := now.Add(-9 * time.Minute)

	gotTime, mode, err := svc.ResolvePlaybackStart(context.Background(), "cam-1", from, time.Time{})
	if err != nil {
		t.Fatalf("ResolvePlaybackStart returned error: %v", err)
	}

	if mode != PlaybackModeRecorded {
		t.Fatalf("expected recorded mode, got %q", mode)
	}

	if !gotTime.Equal(from) {
		t.Fatalf("expected resolved time %v, got %v", from, gotTime)
	}
}

func TestResolvePlaybackStart_FirstAvailableFallback(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	first := recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: now.Add(-30 * time.Minute),
		EndTime:   now.Add(-20 * time.Minute),
	}
	last := recorder.RecordingEntry{
		ID:        "seg-2",
		ChannelID: "cam-1",
		StartTime: now.Add(-5 * time.Minute),
		EndTime:   now.Add(-2 * time.Minute),
	}
	idx := &serviceTestIndex{
		entries: []recorder.RecordingEntry{first, last},
		queryFn: func(context.Context, string, time.Time, time.Time) ([]recorder.RecordingEntry, error) {
			return nil, nil
		},
	}

	svc := NewService(log.Logger, serviceTestRelayHub{}, idx, nil)

	gotTime, mode, err := svc.ResolvePlaybackStart(context.Background(), "cam-1", now.Add(-3*time.Minute), time.Time{})
	if err != nil {
		t.Fatalf("ResolvePlaybackStart returned error: %v", err)
	}

	if mode != PlaybackModeFirstAvailable {
		t.Fatalf("expected first_available mode, got %q", mode)
	}

	if !gotTime.Equal(first.StartTime) {
		t.Fatalf("expected fallback to %v, got %v", first.StartTime, gotTime)
	}
}

func TestResolvePlaybackStart_FutureFallsBackToLive(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	idx := &serviceTestIndex{
		entries: []recorder.RecordingEntry{
			{
				ID:        "seg-1",
				ChannelID: "cam-1",
				StartTime: now.Add(-30 * time.Minute),
				EndTime:   now.Add(-20 * time.Minute),
			},
		},
	}

	svc := NewService(log.Logger, serviceTestRelayHub{}, idx, nil)

	_, mode, err := svc.ResolvePlaybackStart(context.Background(), "cam-1", now.Add(5*time.Minute), time.Time{})
	if err != nil {
		t.Fatalf("ResolvePlaybackStart returned error: %v", err)
	}

	if mode != PlaybackModeLive {
		t.Fatalf("expected live mode, got %q", mode)
	}
}

func TestResolvePlaybackStart_NoRecordingIndex(t *testing.T) {
	t.Parallel()

	svc := NewService(log.Logger, serviceTestRelayHub{}, nil, nil)

	_, _, err := svc.ResolvePlaybackStart(context.Background(), "cam-1", time.Now().UTC(), time.Time{})
	if !errorsIs(err, errNoRecordingIndex) {
		t.Fatalf("expected errNoRecordingIndex, got %v", err)
	}
}

func TestTimeline_NoRecordingIndex(t *testing.T) {
	t.Parallel()

	svc := NewService(log.Logger, serviceTestRelayHub{}, nil, nil)

	_, err := svc.Timeline(context.Background(), "cam-1", time.Time{}, time.Time{})
	if !errorsIs(err, errNoRecordingIndex) {
		t.Fatalf("expected errNoRecordingIndex, got %v", err)
	}
}

func TestTrackConsumer_UpdatesViewerCount(t *testing.T) {
	t.Parallel()

	svc := NewService(log.Logger, serviceTestRelayHub{}, nil, nil)
	done := svc.TrackConsumer()

	if got := svc.ViewerCount(); got != 1 {
		t.Fatalf("expected viewer count 1 after TrackConsumer, got %d", got)
	}

	done()

	if got := svc.ViewerCount(); got != 0 {
		t.Fatalf("expected viewer count 0 after cleanup, got %d", got)
	}
}

func TestListCameras_EnrichesRelayStats(t *testing.T) {
	t.Parallel()

	hub := serviceTestRelayHub{
		statsByID: map[string]av.RelayStats{
			"cam-1": {
				ID:        "cam-1",
				ActualFPS: 25,
				Streams: []av.StreamInfo{
					{Width: 1920, Height: 1080, CodecType: av.H264},
				},
			},
		},
	}
	svc := NewService(log.Logger, hub, nil, nil)
	svc.RegisterCamera(&CameraInfo{CameraID: "cam-1", Name: "Front Door"})

	got := svc.ListCameras(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(got))
	}

	if got[0].State != "streaming" {
		t.Fatalf("expected streaming state, got %q", got[0].State)
	}

	if got[0].FPS != 25 {
		t.Fatalf("expected FPS 25, got %d", got[0].FPS)
	}

	if got[0].Resolution != "1920x1080" {
		t.Fatalf("expected resolution 1920x1080, got %q", got[0].Resolution)
	}
}

func errorsIs(err, target error) bool {
	return err != nil && target != nil && err.Error() == target.Error()
}
