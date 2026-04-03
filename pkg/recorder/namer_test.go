package recorder_test

import (
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/recorder"
)

func TestSegmentPath(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 25, 14, 30, 0, 0, time.UTC)
	got := recorder.SegmentPath("/data/recordings", "cam-1", ts)
	want := "/data/recordings/cam-1/2026-03-25/14/143000.fmp4"

	if got != want {
		t.Errorf("SegmentPath = %q, want %q", got, want)
	}
}

func TestSegmentPathFinal(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 3, 25, 14, 30, 0, 0, time.UTC)
	end := time.Date(2026, 3, 25, 14, 35, 0, 0, time.UTC)
	got := recorder.SegmentPathFinal("/data/recordings", "cam-1", start, end)
	want := "/data/recordings/cam-1/2026-03-25/14/143000_143500.fmp4"

	if got != want {
		t.Errorf("SegmentPathFinal = %q, want %q", got, want)
	}
}
