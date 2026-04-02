package edgeview

import (
	"time"
)

// TimelineEntry represents a single segment in the recording timebar.
type TimelineEntry struct {
	ID         string    `json:"id"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"duration_ms"` //nolint:tagliatelle
	SizeBytes  int64     `json:"size_bytes"`  //nolint:tagliatelle
	Status     string    `json:"status"`
	HasMotion  bool      `json:"has_motion"`  //nolint:tagliatelle
	HasObjects bool      `json:"has_objects"` //nolint:tagliatelle
}
