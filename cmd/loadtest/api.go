package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

var defaultEndpoints = []string{
	"/api/cameras",
	"/api/cameras/stats",
	"/api/cameras/stats/summary",
	"/health",
	"/api/metrics",
}

func apiCmd() *cobra.Command {
	var endpoints string

	cmd := &cobra.Command{
		Use:   "api",
		Short: "Hammer metadata API endpoints with concurrent requests",
		RunE: func(_ *cobra.Command, _ []string) error {
			eps := defaultEndpoints
			if endpoints != "" {
				eps = strings.Split(endpoints, ",")
			}

			return runAPITest(eps)
		},
	}

	cmd.Flags().
		StringVar(&endpoints, "endpoints", "", "Comma-separated endpoint paths (default: common endpoints)")

	return cmd
}

func runAPITest(endpoints []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), flagDuration)
	defer cancel()

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		latencies  []time.Duration
		errorCount atomic.Int32
		reqCount   atomic.Int32
	)

	for range flagConcurrency {
		wg.Go(func() {
			epIdx := 0

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				ep := endpoints[epIdx%len(endpoints)]
				epIdx++

				url := flagTarget + ep
				d, err := apiOnce(ctx, url)

				reqCount.Add(1)

				if err != nil {
					errorCount.Add(1)
				} else {
					mu.Lock()

					latencies = append(latencies, d)
					mu.Unlock()
				}
			}
		})
	}

	wg.Wait()

	mu.Lock()
	stats := computeLatencyStats(latencies)
	total := int(reqCount.Load())
	mu.Unlock()

	printResult(TestResult{
		TestName:   fmt.Sprintf("API: %d endpoints (%d clients)", len(endpoints), flagConcurrency),
		Duration:   flagDuration,
		Clients:    flagConcurrency,
		Requests:   total,
		Errors:     int(errorCount.Load()),
		Latencies:  stats,
		Throughput: float64(total) / flagDuration.Seconds(),
	})

	return nil
}

func apiOnce(ctx context.Context, url string) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return time.Since(start), fmt.Errorf("status %d", resp.StatusCode)
	}

	return time.Since(start), nil
}
