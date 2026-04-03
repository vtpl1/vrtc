package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

func timelineCmd() *cobra.Command {
	var (
		cameraID string
		lookback time.Duration
	)

	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Measure timeline/recordings query latency under concurrent load",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cameraID == "" {
				return errors.New("--camera-id is required")
			}

			return runTimelineTest(cameraID, lookback)
		},
	}

	cmd.Flags().StringVar(&cameraID, "camera-id", "", "Camera ID to query")
	cmd.Flags().DurationVar(&lookback, "lookback", time.Hour, "How far back to query")

	return cmd
}

func runTimelineTest(cameraID string, lookback time.Duration) error {
	now := time.Now().UTC()
	start := now.Add(-lookback)

	url := fmt.Sprintf("%s/api/cameras/%s/recordings?start=%s&end=%s",
		flagTarget, cameraID,
		start.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)

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
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				d, err := timelineOnce(ctx, url)

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
		TestName:   fmt.Sprintf("Timeline: %s (%d clients)", cameraID, flagConcurrency),
		Duration:   flagDuration,
		Clients:    flagConcurrency,
		Requests:   total,
		Errors:     int(errorCount.Load()),
		Latencies:  stats,
		Throughput: float64(total) / flagDuration.Seconds(),
	})

	return nil
}

func timelineOnce(ctx context.Context, url string) (time.Duration, error) {
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
