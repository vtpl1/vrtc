package playback_test

import (
	"context"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/playback"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

// fakeIndex is a stub RecordingIndex backed by a fixed slice.
type fakeIndex struct {
	entries []recorder.RecordingEntry
}

func (f *fakeIndex) Insert(_ context.Context, _ recorder.RecordingEntry) error { return nil }

func (f *fakeIndex) QueryByChannel(
	_ context.Context,
	channelID string,
	from, to time.Time,
) ([]recorder.RecordingEntry, error) {
	var out []recorder.RecordingEntry

	for _, e := range f.entries {
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

func (f *fakeIndex) FirstAvailable(_ context.Context, channelID string) (recorder.RecordingEntry, error) {
	var (
		found  bool
		result recorder.RecordingEntry
	)

	for _, e := range f.entries {
		if e.ChannelID != channelID {
			continue
		}

		if !found || e.StartTime.Before(result.StartTime) {
			result = e
			found = true
		}
	}

	if !found {
		return recorder.RecordingEntry{}, recorder.ErrNoRecordings
	}

	return result, nil
}

func (f *fakeIndex) LastAvailable(_ context.Context, channelID string) (recorder.RecordingEntry, error) {
	var (
		found  bool
		result recorder.RecordingEntry
	)

	for _, e := range f.entries {
		if e.ChannelID != channelID {
			continue
		}

		if !found || e.StartTime.After(result.StartTime) {
			result = e
			found = true
		}
	}

	if !found {
		return recorder.RecordingEntry{}, recorder.ErrNoRecordings
	}

	return result, nil
}

func (f *fakeIndex) QueryAllChannels(
	_ context.Context,
	from, to time.Time,
) ([]recorder.RecordingEntry, error) {
	var out []recorder.RecordingEntry

	for _, e := range f.entries {
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

func (f *fakeIndex) Delete(_ context.Context, _ string) error { return nil }

func (f *fakeIndex) SealInterrupted(_ context.Context) error { return nil }

func (f *fakeIndex) Close() error { return nil }

func TestIsLive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  playback.Request
		want bool
	}{
		{"both zero is live", playback.Request{ChannelID: "cam-1"}, true},
		{"from set is not live", playback.Request{ChannelID: "cam-1", From: time.Now()}, false},
		{"to set is not live", playback.Request{ChannelID: "cam-1", To: time.Now()}, false},
		{
			"both set is not live",
			playback.Request{ChannelID: "cam-1", From: time.Now(), To: time.Now().Add(time.Hour)},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := playback.IsLive(tt.req); got != tt.want {
				t.Errorf("IsLive(%v) = %v, want %v", tt.req, got, tt.want)
			}
		})
	}
}

func TestRecordedDemuxerFactory_NoEntries(t *testing.T) {
	t.Parallel()

	idx := &fakeIndex{}
	router := playback.New(idx)

	factory := router.RecordedDemuxerFactory(playback.Request{ChannelID: "cam-1"})

	_, err := factory(context.Background(), "cam-1")
	if err == nil {
		t.Fatal("expected error when no entries found, got nil")
	}
}

func TestRecordedDemuxerFactory_WrongChannel(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	idx := &fakeIndex{
		entries: []recorder.RecordingEntry{
			{
				ChannelID: "cam-2",
				StartTime: now,
				EndTime:   now.Add(time.Minute),
				FilePath:  "/dev/null",
			},
		},
	}
	router := playback.New(idx)

	factory := router.RecordedDemuxerFactory(playback.Request{ChannelID: "cam-1"})

	_, err := factory(context.Background(), "cam-1")
	if err == nil {
		t.Fatal("expected error for missing channel, got nil")
	}
}

func TestRecordedDemuxerFactory_FileNotFound(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	idx := &fakeIndex{
		entries: []recorder.RecordingEntry{
			{
				ChannelID: "cam-1",
				StartTime: now,
				EndTime:   now.Add(time.Minute),
				FilePath:  "/nonexistent/path/segment.mp4",
			},
		},
	}
	router := playback.New(idx)

	factory := router.RecordedDemuxerFactory(playback.Request{ChannelID: "cam-1"})

	_, err := factory(context.Background(), "cam-1")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}
