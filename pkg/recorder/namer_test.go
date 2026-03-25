package recorder_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/recorder"
)

func TestSegmentPath(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 25, 14, 30, 0, 0, time.UTC)
	got := recorder.SegmentPath("/data/recordings", "cam-1", ts)
	want := filepath.Join("/data/recordings", "cam-1", "cam-1_20260325T143000Z.mp4")

	if got != want {
		t.Errorf("SegmentPath = %q, want %q", got, want)
	}
}
