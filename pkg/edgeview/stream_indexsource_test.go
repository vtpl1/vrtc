package edgeview

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

func writeTempSegmentFile(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("not-a-real-fmp4"), 0o600); err != nil {
		t.Fatalf("write temp segment: %v", err)
	}

	return path
}

func TestResolvePlaybackStart_SnapsForwardToSegmentStart(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	entry := recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: now.Add(-10 * time.Minute),
		EndTime:   now.Add(-5 * time.Minute),
	}
	idx := &serviceTestIndex{
		entries: []recorder.RecordingEntry{entry},
	}

	svc := NewService(log.Logger, serviceTestRelayHub{}, idx, nil)
	requested := now.Add(-12 * time.Minute)

	gotTime, mode, err := svc.ResolvePlaybackStart(context.Background(), "cam-1", requested, time.Time{})
	if err != nil {
		t.Fatalf("ResolvePlaybackStart returned error: %v", err)
	}

	if mode != PlaybackModeRecorded {
		t.Fatalf("expected recorded mode, got %q", mode)
	}

	if !gotTime.Equal(entry.StartTime) {
		t.Fatalf("expected snap-forward to %v, got %v", entry.StartTime, gotTime)
	}
}

func TestIndexSourceOpenEntry_RecordsGapAboveThreshold(t *testing.T) {
	t.Parallel()

	entry := recorder.RecordingEntry{
		ID:        "seg-2",
		ChannelID: "cam-1",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(time.Minute),
		FilePath:  writeTempSegmentFile(t, "seg-2.fmp4"),
	}
	src := &indexSource{
		lastSegEnd: entry.StartTime.Add(-10 * time.Second),
	}

	dmx, err := src.openEntry(entry)
	if err != nil {
		t.Fatalf("openEntry returned error: %v", err)
	}
	defer func() { _ = dmx.Close() }()

	if src.LastGap() < gapThreshold {
		t.Fatalf("expected lastGap >= %v, got %v", gapThreshold, src.LastGap())
	}
}

func TestIndexSourceOpenAt_ResetsCursorAfterOpenedSegment(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	entries := []recorder.RecordingEntry{
		{
			ID:        "seg-1",
			ChannelID: "cam-1",
			StartTime: now.Add(-2 * time.Minute),
			EndTime:   now.Add(-time.Minute),
			FilePath:  writeTempSegmentFile(t, "seg-1.fmp4"),
		},
		{
			ID:        "seg-2",
			ChannelID: "cam-1",
			StartTime: now.Add(-time.Minute),
			EndTime:   now,
			FilePath:  writeTempSegmentFile(t, "seg-2.fmp4"),
		},
	}
	idx := &serviceTestIndex{entries: entries}
	src := &indexSource{
		recIndex:  idx,
		channelID: "cam-1",
		seenIDs:   map[string]struct{}{"older": {}},
	}

	dmx, err := src.OpenAt(context.Background(), now.Add(-90*time.Second))
	if err != nil {
		t.Fatalf("OpenAt returned error: %v", err)
	}
	defer func() { _ = dmx.Close() }()

	if src.idx != 1 {
		t.Fatalf("expected cursor idx 1 after opening first segment, got %d", src.idx)
	}

	if _, ok := src.seenIDs["older"]; !ok {
		t.Fatal("expected existing seenIDs to be preserved across OpenAt")
	}
}

func TestIndexSourceWaitForNextWithTimeout_AppendsUnseenEntries(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	first := recorder.RecordingEntry{
		ID:        "seg-1",
		ChannelID: "cam-1",
		StartTime: now.Add(-2 * time.Minute),
		EndTime:   now.Add(-time.Minute),
		FilePath:  writeTempSegmentFile(t, "seg-1.fmp4"),
	}
	second := recorder.RecordingEntry{
		ID:        "seg-2",
		ChannelID: "cam-1",
		StartTime: now.Add(-time.Minute),
		EndTime:   now,
		FilePath:  writeTempSegmentFile(t, "seg-2.fmp4"),
	}
	idx := &serviceTestIndex{
		queryFn: func(context.Context, string, time.Time, time.Time) ([]recorder.RecordingEntry, error) {
			return []recorder.RecordingEntry{first, second}, nil
		},
	}
	src := &indexSource{
		entries:   []recorder.RecordingEntry{first},
		idx:       1,
		seenIDs:   map[string]struct{}{first.ID: {}},
		recIndex:  idx,
		channelID: "cam-1",
	}

	dmx, err := src.waitForNextWithTimeout(context.Background())
	if err != nil {
		t.Fatalf("waitForNextWithTimeout returned error: %v", err)
	}
	if dmx == nil {
		t.Fatal("expected new demuxer for unseen entry, got nil")
	}
	defer func() { _ = dmx.Close() }()

	if len(src.entries) != 2 {
		t.Fatalf("expected 2 entries after appending unseen segment, got %d", len(src.entries))
	}
}

func TestIndexSourceNext_ReturnsEOFWhenNotFollow(t *testing.T) {
	t.Parallel()

	src := &indexSource{
		follow: false,
		idx:    0,
	}

	_, err := src.Next(context.Background())
	if err != io.EOF {
		t.Fatalf("expected io.EOF when not following and no entries remain, got %v", err)
	}
}
