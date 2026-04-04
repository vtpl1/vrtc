package recorder

import (
	"testing"
	"time"
)

func TestEvaluateRetention_ContinuousAge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	entries := []RecordingEntry{
		{ID: "old", StartTime: now.AddDate(0, 0, -8), SizeBytes: 100},
		{ID: "fresh", StartTime: now.AddDate(0, 0, -2), SizeBytes: 100},
	}

	got := EvaluateRetention(entries, RetentionPolicy{ContinuousDays: 7}, now)
	if len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("expected only old entry to be deleted, got %+v", got)
	}
}

func TestEvaluateRetention_MotionAndObjectOverrides(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	entries := []RecordingEntry{
		{ID: "plain", StartTime: now.AddDate(0, 0, -5)},
		{ID: "motion", StartTime: now.AddDate(0, 0, -5), HasMotion: true},
		{ID: "object", StartTime: now.AddDate(0, 0, -10), HasObjects: true},
	}

	got := EvaluateRetention(entries, RetentionPolicy{
		ContinuousDays: 2,
		MotionDays:     7,
		ObjectDays:     14,
	}, now)

	if len(got) != 1 || got[0].ID != "plain" {
		t.Fatalf("expected only plain entry to expire, got %+v", got)
	}
}

func TestEvaluateRetention_MaxStorageDeletesOldestFirst(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	gb := int64(bytesPerGB)
	entries := []RecordingEntry{
		{ID: "oldest", StartTime: now.Add(-3 * time.Hour), SizeBytes: gb},
		{ID: "middle", StartTime: now.Add(-2 * time.Hour), SizeBytes: gb},
		{ID: "newest", StartTime: now.Add(-1 * time.Hour), SizeBytes: gb},
	}

	got := EvaluateRetention(entries, RetentionPolicy{MaxStorageGB: 2}, now)
	if len(got) != 1 || got[0].ID != "oldest" {
		t.Fatalf("expected oldest entry to be deleted first, got %+v", got)
	}
}

func TestEvaluateRetention_MinFreeDiskDeletesEnoughEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	gb := int64(bytesPerGB)
	entries := []RecordingEntry{
		{ID: "first", StartTime: now.Add(-3 * time.Hour), SizeBytes: gb},
		{ID: "second", StartTime: now.Add(-2 * time.Hour), SizeBytes: gb},
		{ID: "third", StartTime: now.Add(-1 * time.Hour), SizeBytes: gb},
	}

	got := EvaluateRetention(entries, RetentionPolicy{
		MinFreeGB:     2,
		DiskFreeBytes: 0,
	}, now)

	if len(got) != 0 {
		t.Fatalf("expected DiskFreeBytes=0 to disable disk-free retention, got %+v", got)
	}

	got = EvaluateRetention(entries, RetentionPolicy{
		MinFreeGB:     2,
		DiskFreeBytes: gb / 2,
	}, now)

	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "second" {
		t.Fatalf("expected oldest two entries to be deleted to satisfy free-disk target, got %+v", got)
	}
}
