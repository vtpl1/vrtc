package recorder

import (
	"path/filepath"
	"time"
)

// SegmentPath returns the absolute file path for a new recording segment.
//
// Format:  <base>/<channelID>/YYYY/MM/DD/HH/<channelID>_<startTime>.mp4
// Example: /data/recordings/cam-1/2026/03/25/14/cam-1_20260325T143000Z.mp4.
func SegmentPath(base, channelID string, startTime time.Time) string {
	t := startTime.UTC()

	return filepath.Join(
		base, channelID,
		t.Format("2006"), t.Format("01"), t.Format("02"), t.Format("15"),
		channelID+"_"+t.Format("20060102T150405Z")+".mp4",
	)
}
