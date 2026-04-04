package recorder

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestSQLiteIndex(t *testing.T) *sqliteIndex {
	t.Helper()

	idx, ok := NewSQLiteIndex(t.TempDir()).(*sqliteIndex)
	if !ok {
		t.Fatal("expected sqlite index implementation")
	}

	t.Cleanup(func() {
		_ = idx.Close()
	})

	return idx
}

func TestSQLiteIndex_InsertQueryAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	idx := newTestSQLiteIndex(t)

	base := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	entries := []RecordingEntry{
		{
			ID:        "recording",
			ChannelID: "cam-1",
			StartTime: base,
			EndTime:   base.Add(1 * time.Minute),
			FilePath:  filepath.Join(t.TempDir(), "recording.mp4"),
			Status:    StatusRecording,
		},
		{
			ID:        "complete",
			ChannelID: "cam-1",
			StartTime: base.Add(2 * time.Minute),
			EndTime:   base.Add(3 * time.Minute),
			FilePath:  filepath.Join(t.TempDir(), "complete.mp4"),
			Status:    StatusComplete,
		},
		{
			ID:        "interrupted",
			ChannelID: "cam-1",
			StartTime: base.Add(4 * time.Minute),
			EndTime:   base.Add(5 * time.Minute),
			FilePath:  filepath.Join(t.TempDir(), "interrupted.mp4"),
			Status:    StatusInterrupted,
		},
	}

	for _, entry := range entries {
		if err := idx.Insert(ctx, entry); err != nil {
			t.Fatalf("Insert(%s): %v", entry.ID, err)
		}
	}

	got, err := idx.QueryByChannel(ctx, "cam-1", base, time.Time{})
	if err != nil {
		t.Fatalf("QueryByChannel: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 playable entries, got %d", len(got))
	}

	if got[0].ID != "complete" || got[1].ID != "interrupted" {
		t.Fatalf("unexpected query order: %+v", got)
	}

	if err := idx.Delete(ctx, "complete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err = idx.QueryByChannel(ctx, "cam-1", base, time.Time{})
	if err != nil {
		t.Fatalf("QueryByChannel after delete: %v", err)
	}

	if len(got) != 1 || got[0].ID != "interrupted" {
		t.Fatalf("expected only interrupted entry after delete, got %+v", got)
	}
}

func TestSQLiteIndex_QueryByChannel_ExcludesCorruptedSegments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	idx := newTestSQLiteIndex(t)
	base := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)

	for _, entry := range []RecordingEntry{
		{
			ID:        "corrupted",
			ChannelID: "cam-1",
			StartTime: base,
			EndTime:   base.Add(1 * time.Minute),
			FilePath:  filepath.Join(t.TempDir(), "corrupted.mp4"),
			Status:    StatusCorrupted,
		},
		{
			ID:        "complete",
			ChannelID: "cam-1",
			StartTime: base.Add(2 * time.Minute),
			EndTime:   base.Add(3 * time.Minute),
			FilePath:  filepath.Join(t.TempDir(), "complete.mp4"),
			Status:    StatusComplete,
		},
	} {
		if err := idx.Insert(ctx, entry); err != nil {
			t.Fatalf("Insert(%s): %v", entry.ID, err)
		}
	}

	got, err := idx.QueryByChannel(ctx, "cam-1", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("QueryByChannel: %v", err)
	}

	if len(got) != 1 || got[0].ID != "complete" {
		t.Fatalf("expected corrupted segments to be excluded from playback queries, got %+v", got)
	}
}

func TestSQLiteIndex_FirstLastAvailableAndAllCorrupted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	idx := newTestSQLiteIndex(t)
	base := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)

	first := RecordingEntry{
		ID:        "first",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(1 * time.Minute),
		FilePath:  filepath.Join(t.TempDir(), "first.mp4"),
		Status:    StatusComplete,
	}
	last := RecordingEntry{
		ID:        "last",
		ChannelID: "cam-1",
		StartTime: base.Add(10 * time.Minute),
		EndTime:   base.Add(11 * time.Minute),
		FilePath:  filepath.Join(t.TempDir(), "last.mp4"),
		Status:    StatusInterrupted,
	}

	for _, entry := range []RecordingEntry{first, last} {
		if err := idx.Insert(ctx, entry); err != nil {
			t.Fatalf("Insert(%s): %v", entry.ID, err)
		}
	}

	gotFirst, err := idx.FirstAvailable(ctx, "cam-1")
	if err != nil {
		t.Fatalf("FirstAvailable: %v", err)
	}

	if gotFirst.ID != first.ID {
		t.Fatalf("expected first entry %q, got %q", first.ID, gotFirst.ID)
	}

	gotLast, err := idx.LastAvailable(ctx, "cam-1")
	if err != nil {
		t.Fatalf("LastAvailable: %v", err)
	}

	if gotLast.ID != last.ID {
		t.Fatalf("expected last entry %q, got %q", last.ID, gotLast.ID)
	}

	idxCorrupted := newTestSQLiteIndex(t)
	if err := idxCorrupted.Insert(ctx, RecordingEntry{
		ID:        "corrupted-only",
		ChannelID: "cam-2",
		StartTime: base,
		EndTime:   base.Add(1 * time.Minute),
		FilePath:  filepath.Join(t.TempDir(), "corrupted-only.mp4"),
		Status:    StatusCorrupted,
	}); err != nil {
		t.Fatalf("Insert corrupted-only: %v", err)
	}

	_, err = idxCorrupted.FirstAvailable(ctx, "cam-2")
	if !errors.Is(err, ErrAllCorrupted) {
		t.Fatalf("expected ErrAllCorrupted, got %v", err)
	}
}

func TestSQLiteIndex_SealInterruptedAndSeekEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	idx := newTestSQLiteIndex(t)
	base := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)

	entry := RecordingEntry{
		ID:        "recording-segment",
		ChannelID: "cam-1",
		StartTime: base,
		EndTime:   base.Add(1 * time.Minute),
		FilePath:  filepath.Join(t.TempDir(), "segment.mp4"),
		Status:    StatusRecording,
	}

	if err := idx.Insert(ctx, entry); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := idx.SealInterrupted(ctx); err != nil {
		t.Fatalf("SealInterrupted: %v", err)
	}

	got, err := idx.QueryByChannel(ctx, "cam-1", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("QueryByChannel: %v", err)
	}

	if len(got) != 1 || got[0].Status != StatusInterrupted {
		t.Fatalf("expected interrupted entry after sealing, got %+v", got)
	}

	seekEntries := []SeekEntry{
		{DTSMS: 1000, ByteOffset: 128},
		{DTSMS: 2500, ByteOffset: 512},
	}

	if err := idx.InsertSeekEntries(ctx, "cam-1", entry.ID, seekEntries); err != nil {
		t.Fatalf("InsertSeekEntries: %v", err)
	}

	offset, err := idx.SeekInSegment(ctx, "cam-1", entry.ID, 2200)
	if err != nil {
		t.Fatalf("SeekInSegment: %v", err)
	}

	if offset != 128 {
		t.Fatalf("expected byte offset 128, got %d", offset)
	}
}

func TestSQLiteIndex_QueryAllChannelsAndCloseReopen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	idx := newTestSQLiteIndex(t)
	base := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)

	for i, channelID := range []string{"cam-1", "cam-2"} {
		if err := idx.Insert(ctx, RecordingEntry{
			ID:        channelID + "-entry",
			ChannelID: channelID,
			StartTime: base.Add(time.Duration(i) * time.Minute),
			EndTime:   base.Add(time.Duration(i+1) * time.Minute),
			FilePath:  filepath.Join(t.TempDir(), channelID+".mp4"),
			Status:    StatusComplete,
		}); err != nil {
			t.Fatalf("Insert(%s): %v", channelID, err)
		}
	}

	all, err := idx.QueryAllChannels(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("QueryAllChannels: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("expected 2 entries across channels, got %d", len(all))
	}

	baseDir := idx.baseDir
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, ok := NewSQLiteIndex(baseDir).(*sqliteIndex)
	if !ok {
		t.Fatal("expected reopened sqlite index implementation")
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	got, err := reopened.QueryByChannel(ctx, "cam-1", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("QueryByChannel after reopen: %v", err)
	}

	if len(got) != 1 || got[0].ID != "cam-1-entry" {
		t.Fatalf("unexpected reopened results: %+v", got)
	}
}
