package edgeview

import (
	"time"
)

// TimelineEntry represents a single segment in the recording timebar.
type TimelineEntry struct {
	ID         string    `doc:"Recording segment identifier"                                 json:"id"`
	Start      time.Time `doc:"Segment start time (RFC3339)"                                 json:"start"`
	End        time.Time `doc:"Segment end time (RFC3339)"                                   json:"end"`
	DurationMs int64     `doc:"Segment duration in milliseconds"                             json:"durationMs"`
	SizeBytes  int64     `doc:"Segment file size in bytes"                                   json:"sizeBytes"`
	Status     string    `doc:"Segment status: complete, interrupted, corrupted, or deleted" json:"status"`
	HasMotion  bool      `doc:"Whether motion was detected during this segment"              json:"hasMotion"`
	HasObjects bool      `doc:"Whether objects were detected during this segment"            json:"hasObjects"`
}
