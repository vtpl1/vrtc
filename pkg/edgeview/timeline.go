package edgeview

import (
	"time"
)

// TimelineEntry represents a single segment in the recording timebar.
type TimelineEntry struct {
	ID         string    `json:"id"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"durationMs"`
	SizeBytes  int64     `json:"sizeBytes"`
	Status     string    `json:"status"`
	HasMotion  bool      `json:"hasMotion"`
	HasObjects bool      `json:"hasObjects"`
}
