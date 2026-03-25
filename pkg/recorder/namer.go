package recorder

import (
	"path/filepath"
	"time"
)

// SegmentPath returns the absolute file path for a new recording segment.
//
// Format:  <base>/<channelID>/<channelID>_<startTime>.mp4
// Example: /data/recordings/cam-1/cam-1_20260325T143000Z.mp4.
func SegmentPath(base, channelID string, startTime time.Time) string {
	ts := startTime.UTC().Format("20060102T150405Z")

	return filepath.Join(base, channelID, channelID+"_"+ts+".mp4")
}
