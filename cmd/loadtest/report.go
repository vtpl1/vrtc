package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"time"
)

// TestResult holds the outcome of a load test run.
type TestResult struct {
	TestName   string        `json:"testName"`
	Duration   time.Duration `json:"duration"`
	Clients    int           `json:"clients"`
	Requests   int           `json:"requests"`
	Errors     int           `json:"errors"`
	Latencies  LatencyStats  `json:"latencies"`
	Throughput float64       `json:"throughput"` // req/s or bytes/s depending on test
}

// LatencyStats holds percentile data.
type LatencyStats struct {
	P50 time.Duration `json:"p50"`
	P95 time.Duration `json:"p95"`
	P99 time.Duration `json:"p99"`
	Max time.Duration `json:"max"`
	Avg time.Duration `json:"avg"`
}

func computeLatencyStats(durations []time.Duration) LatencyStats {
	if len(durations) == 0 {
		return LatencyStats{}
	}

	slices.Sort(durations)

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}

	return LatencyStats{
		P50: durationPercentile(durations, 0.50),
		P95: durationPercentile(durations, 0.95),
		P99: durationPercentile(durations, 0.99),
		Max: durations[len(durations)-1],
		Avg: sum / time.Duration(len(durations)),
	}
}

func durationPercentile(sorted []time.Duration, p float64) time.Duration {
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))

	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}

	frac := idx - float64(lower)

	return time.Duration(float64(sorted[lower])*(1-frac) + float64(sorted[upper])*frac)
}

func printResult(r TestResult) {
	if flagOutputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)

		return
	}

	errorRate := 0.0
	if r.Requests > 0 {
		errorRate = float64(r.Errors) / float64(r.Requests) * 100
	}

	fmt.Println("┌─────────────────────────────────────────┐")
	fmt.Printf("│ %-39s │\n", r.TestName)
	fmt.Println("├─────────────────────────────────────────┤")
	fmt.Printf("│ Duration:    %-26s │\n", r.Duration.Round(time.Millisecond))
	fmt.Printf("│ Clients:     %-26d │\n", r.Clients)
	fmt.Printf("│ Requests:    %-26d │\n", r.Requests)
	fmt.Printf("│ Errors:      %-22d %4.1f%% │\n", r.Errors, errorRate)
	fmt.Printf("│ Throughput:  %-22.1f req/s │\n", r.Throughput)
	fmt.Println("├─────────────────────────────────────────┤")
	fmt.Printf("│ P50:         %-26s │\n", r.Latencies.P50.Round(time.Microsecond))
	fmt.Printf("│ P95:         %-26s │\n", r.Latencies.P95.Round(time.Microsecond))
	fmt.Printf("│ P99:         %-26s │\n", r.Latencies.P99.Round(time.Microsecond))
	fmt.Printf("│ Max:         %-26s │\n", r.Latencies.Max.Round(time.Microsecond))
	fmt.Printf("│ Avg:         %-26s │\n", r.Latencies.Avg.Round(time.Microsecond))
	fmt.Println("└─────────────────────────────────────────┘")
}
