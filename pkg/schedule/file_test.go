package schedule_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/schedule"
)

func writeTempScheduleFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "schedules.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	return path
}

func TestFileProvider_ListSchedules(t *testing.T) {
	t.Parallel()

	const content = `[
		{
			"id": "sched-1",
			"channelId": "cam-1",
			"storagePath": "/data/recordings",
			"segmentMinutes": 5,
			"daysOfWeek": [1,2,3,4,5]
		}
	]`

	p := schedule.NewFileProvider(writeTempScheduleFile(t, content))
	defer p.Close()

	schedules, err := p.ListSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}

	if len(schedules) != 1 {
		t.Fatalf("got %d schedules, want 1", len(schedules))
	}

	s := schedules[0]

	if s.ID != "sched-1" {
		t.Errorf("ID = %q, want sched-1", s.ID)
	}

	if s.ChannelID != "cam-1" {
		t.Errorf("ChannelID = %q, want cam-1", s.ChannelID)
	}

	if s.SegmentMinutes != 5 {
		t.Errorf("SegmentMinutes = %d, want 5", s.SegmentMinutes)
	}

	if len(s.DaysOfWeek) != 5 {
		t.Errorf("len(DaysOfWeek) = %d, want 5", len(s.DaysOfWeek))
	}
}

func TestFileProvider_StartEndAt(t *testing.T) {
	t.Parallel()

	const content = `[
		{
			"id": "sched-2",
			"channelId": "cam-1",
			"storagePath": "/data",
			"segmentMinutes": 0,
			"startAt": "2026-01-01T00:00:00Z",
			"endAt":   "2026-12-31T23:59:59Z"
		}
	]`

	p := schedule.NewFileProvider(writeTempScheduleFile(t, content))
	defer p.Close()

	schedules, err := p.ListSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}

	s := schedules[0]

	if s.StartAt.IsZero() {
		t.Error("StartAt is zero, expected parsed time")
	}

	if s.EndAt.IsZero() {
		t.Error("EndAt is zero, expected parsed time")
	}
}

func TestFileProvider_MissingFile(t *testing.T) {
	t.Parallel()

	p := schedule.NewFileProvider("/nonexistent/schedules.json")
	defer p.Close()

	_, err := p.ListSchedules(context.Background())
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestIsActive(t *testing.T) {
	t.Parallel()

	monday := time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC) // Monday

	tests := []struct {
		name   string
		sched  schedule.Schedule
		now    time.Time
		active bool
	}{
		{
			name:   "unconstrained always active",
			sched:  schedule.Schedule{ID: "s"},
			now:    monday,
			active: true,
		},
		{
			name:   "before start",
			sched:  schedule.Schedule{ID: "s", StartAt: monday.Add(time.Hour)},
			now:    monday,
			active: false,
		},
		{
			name:   "after end",
			sched:  schedule.Schedule{ID: "s", EndAt: monday.Add(-time.Hour)},
			now:    monday,
			active: false,
		},
		{
			name: "within window",
			sched: schedule.Schedule{
				ID:      "s",
				StartAt: monday.Add(-time.Hour),
				EndAt:   monday.Add(time.Hour),
			},
			now:    monday,
			active: true,
		},
		{
			name:   "matching day of week",
			sched:  schedule.Schedule{ID: "s", DaysOfWeek: []time.Weekday{time.Monday}},
			now:    monday,
			active: true,
		},
		{
			name: "non-matching day of week",
			sched: schedule.Schedule{
				ID:         "s",
				DaysOfWeek: []time.Weekday{time.Sunday, time.Saturday},
			},
			now:    monday,
			active: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := schedule.IsActive(tc.sched, tc.now); got != tc.active {
				t.Errorf("IsActive = %v, want %v", got, tc.active)
			}
		})
	}
}
