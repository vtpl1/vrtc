package main

import (
	"testing"
	"time"
)

func TestComputeLatencyStats(t *testing.T) {
	t.Parallel()

	got := computeLatencyStats([]time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	})

	if got.P50 != 30*time.Millisecond {
		t.Fatalf("expected P50 30ms, got %v", got.P50)
	}

	if got.Max != 50*time.Millisecond {
		t.Fatalf("expected Max 50ms, got %v", got.Max)
	}

	if got.Avg != 30*time.Millisecond {
		t.Fatalf("expected Avg 30ms, got %v", got.Avg)
	}
}

func TestComputeLatencyStats_Empty(t *testing.T) {
	t.Parallel()

	got := computeLatencyStats(nil)
	if got != (LatencyStats{}) {
		t.Fatalf("expected zero-value stats for empty input, got %+v", got)
	}
}

func TestDurationPercentile_Interpolates(t *testing.T) {
	t.Parallel()

	durations := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
	}

	got := durationPercentile(durations, 0.50)
	if got != 25*time.Millisecond {
		t.Fatalf("expected interpolated percentile 25ms, got %v", got)
	}
}
