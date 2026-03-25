package recorder

import "time"

// Metrics is a point-in-time snapshot of the recording system's health.
type Metrics struct {
	ActiveSegments int                     `json:"activeSegments"`
	TotalSegments  int                     `json:"totalSegments"`
	TotalSizeBytes int64                   `json:"totalSizeBytes"`
	DiskFreeBytes  int64                   `json:"diskFreeBytes"`
	DiskTotalBytes int64                   `json:"diskTotalBytes"`
	PerChannel     map[string]ChannelStats `json:"perChannel"`
	LastRetention  time.Time               `json:"lastRetention"`
}

// ChannelStats holds per-channel recording statistics.
type ChannelStats struct {
	Segments      int       `json:"segments"`
	TotalBytes    int64     `json:"totalBytes"`
	OldestSegment time.Time `json:"oldestSegment"`
	NewestSegment time.Time `json:"newestSegment"`
	Recording     bool      `json:"recording"`
	RingBufBytes  int64     `json:"ringBufBytes"`
}
