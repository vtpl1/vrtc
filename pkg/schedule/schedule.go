// Package schedule provides types and interfaces for reading recording
// schedules from pluggable sources (JSON file, database, etc.).
package schedule

import (
	"context"
	"slices"
	"time"
)

// Schedule describes one recording directive: which channel to record,
// when to record it, where to store segments, and how long each segment is.
type Schedule struct {
	ID             string         `json:"id"`
	ChannelID      string         `json:"channel_id"`      //nolint:tagliatelle
	StoragePath    string         `json:"storage_path"`    //nolint:tagliatelle
	SegmentMinutes int            `json:"segment_minutes"` //nolint:tagliatelle // 0 = no rotation
	StartAt        time.Time      `json:"start_at"`        //nolint:tagliatelle // zero = always active
	EndAt          time.Time      `json:"end_at"`          //nolint:tagliatelle // zero = no end
	DaysOfWeek     []time.Weekday `json:"days_of_week"`    //nolint:tagliatelle // empty = every day

	// Retention limits — at least one should be non-zero to bound storage use.
	// Both limits are enforced independently on every recorder poll tick.
	MaxAgeDays   int     `json:"max_age_days"`   //nolint:tagliatelle // delete segments older than N days; 0 = no limit
	MaxStorageGB float64 `json:"max_storage_gb"` //nolint:tagliatelle // delete oldest segments when total exceeds N GB; 0 = no limit
}

// ScheduleProvider is the single interface all schedule sources must satisfy.
// Implementations are expected to be safe for concurrent use.
type ScheduleProvider interface {
	// ListSchedules returns all schedules known to this provider.
	ListSchedules(ctx context.Context) ([]Schedule, error)

	// Close releases any held resources.
	Close() error
}

// IsActive reports whether s should be recording at time now.
// It checks the optional StartAt/EndAt window and the DaysOfWeek constraint.
func IsActive(s Schedule, now time.Time) bool {
	if !s.StartAt.IsZero() && now.Before(s.StartAt) {
		return false
	}

	if !s.EndAt.IsZero() && !now.Before(s.EndAt) {
		return false
	}

	if len(s.DaysOfWeek) == 0 {
		return true
	}

	today := now.Weekday()

	return slices.Contains(s.DaysOfWeek, today)
}
