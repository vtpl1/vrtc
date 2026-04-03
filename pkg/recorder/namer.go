package recorder

import (
	"fmt"
	"time"
)

// SegmentPath returns the file path for an in-progress recording segment.
//
// Format:  <base>/<channelID>/<YYYY-MM-DD>/<HH>/<HHmmss>.fmp4
// Example: /data/recordings/cam-1/2026-03-25/14/143000.fmp4.
func SegmentPath(base, channelID string, startTime time.Time) string {
	t := startTime.UTC()
	date := t.Format("2006-01-02")
	hour := fmt.Sprintf("%02d", t.Hour())
	filename := t.Format("150405") + ".fmp4"

	return fmt.Sprintf("%s/%s/%s/%s/%s", base, channelID, date, hour, filename)
}

// SegmentPathFinal returns the file path for a completed recording segment.
//
// Format:  <base>/<channelID>/<YYYY-MM-DD>/<HH>/<HHmmss>_<HHmmss>.fmp4
// Example: /data/recordings/cam-1/2026-03-25/14/143000_143500.fmp4.
func SegmentPathFinal(base, channelID string, startTime, endTime time.Time) string {
	t := startTime.UTC()
	e := endTime.UTC()
	date := t.Format("2006-01-02")
	hour := fmt.Sprintf("%02d", t.Hour())
	filename := t.Format("150405") + "_" + e.Format("150405") + ".fmp4"

	return fmt.Sprintf("%s/%s/%s/%s/%s", base, channelID, date, hour, filename)
}
