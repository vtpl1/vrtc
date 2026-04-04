package edgeview

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readEdgeviewSource(t *testing.T, filename string) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	path := filepath.Join(filepath.Dir(currentFile), filename)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func TestWSStream_HasSeekLatencyInstrumentation(t *testing.T) {
	t.Parallel()

	src := readEdgeviewSource(t, "ws_stream.go")
	if !strings.Contains(src, "RecordSeekLatency(") {
		t.Fatal("seek operations are part of the KPI contract but ws_stream.go does not record seek_latency_ms")
	}
}

func TestWSStream_HasConsumerAddInstrumentation(t *testing.T) {
	t.Parallel()

	src := readEdgeviewSource(t, "ws_stream.go")
	if !strings.Contains(src, "RecordConsumerAdd(") {
		t.Fatal("consumer attach latency is part of the KPI contract but ws_stream.go does not record consumer_add_ms")
	}
}

func TestStream_HasSegmentOpenInstrumentation(t *testing.T) {
	t.Parallel()

	src := readEdgeviewSource(t, "stream.go")
	if !strings.Contains(src, "RecordSegmentOpen(") {
		t.Fatal("segment open time is part of the KPI contract but stream.go does not record segment_open_ms")
	}
}

func TestStream_HasRecordedToLiveTransitionInstrumentation(t *testing.T) {
	t.Parallel()

	src := readEdgeviewSource(t, "stream.go")
	if !strings.Contains(src, "RecordRecToLiveTransition(") {
		t.Fatal("recorded-to-live transition is part of the KPI contract but stream.go does not record rec_to_live_transition_ms")
	}
}

func TestStream_HasFragmentGapInstrumentation(t *testing.T) {
	t.Parallel()

	src := readEdgeviewSource(t, "stream.go")
	if !strings.Contains(src, "RecordFragmentGap(") {
		t.Fatal("fragment continuity is part of the KPI contract but stream.go does not record fragment_gap_ms")
	}
}
