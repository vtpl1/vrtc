package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// jsonSchedule is the on-disk representation of a Schedule.
// It uses string fields for time.Time and []time.Weekday so that they can be
// expressed as RFC 3339 strings and plain integers respectively in JSON.
type jsonSchedule struct {
	ID             string `json:"id"`
	ChannelID      string `json:"channelId"`
	StoragePath    string `json:"storagePath"`
	SegmentMinutes int    `json:"segmentMinutes"`
	StartAt        string `json:"startAt,omitempty"` // RFC 3339; empty = zero
	EndAt          string `json:"endAt,omitempty"`   // RFC 3339; empty = zero
	DaysOfWeek     []int  `json:"daysOfWeek"`        // 0=Sun … 6=Sat
}

func (j jsonSchedule) toSchedule() (Schedule, error) {
	s := Schedule{
		ID:             j.ID,
		ChannelID:      j.ChannelID,
		StoragePath:    j.StoragePath,
		SegmentMinutes: j.SegmentMinutes,
	}

	if j.StartAt != "" {
		t, err := time.Parse(time.RFC3339, j.StartAt)
		if err != nil {
			return Schedule{}, fmt.Errorf("schedule %q: invalid start_at: %w", j.ID, err)
		}

		s.StartAt = t
	}

	if j.EndAt != "" {
		t, err := time.Parse(time.RFC3339, j.EndAt)
		if err != nil {
			return Schedule{}, fmt.Errorf("schedule %q: invalid end_at: %w", j.ID, err)
		}

		s.EndAt = t
	}

	for _, d := range j.DaysOfWeek {
		s.DaysOfWeek = append(s.DaysOfWeek, time.Weekday(d))
	}

	return s, nil
}

// fileProvider implements ScheduleProvider by reading a JSON file on every call.
//
// Example file format:
//
//	[
//	  {
//	    "id":             "sched-1",
//	    "channelId":      "cam-1",
//	    "storagePath":    "/data/recordings",
//	    "segmentMinutes": 5,
//	    "daysOfWeek":     [1,2,3,4,5]
//	  }
//	]
type fileProvider struct {
	path string
}

// NewFileProvider returns a ScheduleProvider backed by the JSON file at path.
// The file is re-read on every ListSchedules call.
func NewFileProvider(path string) ScheduleProvider {
	return &fileProvider{path: path}
}

func (p *fileProvider) ListSchedules(_ context.Context) ([]Schedule, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return nil, fmt.Errorf("schedule file provider: read %q: %w", p.path, err)
	}

	var raw []jsonSchedule
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("schedule file provider: parse %q: %w", p.path, err)
	}

	schedules := make([]Schedule, 0, len(raw))

	for _, r := range raw {
		s, err := r.toSchedule()
		if err != nil {
			return nil, err
		}

		schedules = append(schedules, s)
	}

	return schedules, nil
}

func (p *fileProvider) Close() error { return nil }
