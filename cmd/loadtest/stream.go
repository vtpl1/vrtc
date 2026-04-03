package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

func streamCmd() *cobra.Command {
	var cameraID string

	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Simulate parallel streaming clients on /api/cameras/{id}/live",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cameraID == "" {
				return errors.New("--camera-id is required")
			}

			return runStreamTest(cameraID)
		},
	}

	cmd.Flags().StringVar(&cameraID, "camera-id", "", "Camera ID to stream from")

	return cmd
}

func runStreamTest(cameraID string) error {
	url := fmt.Sprintf("%s/api/cameras/%s/live", flagTarget, cameraID)
	ctx, cancel := context.WithTimeout(context.Background(), flagDuration)

	defer cancel()

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		latencies  []time.Duration
		totalBytes atomic.Int64
		errorCount atomic.Int32
	)

	for range flagConcurrency {
		wg.Go(func() {
			start := time.Now()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				errorCount.Add(1)

				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errorCount.Add(1)

				return
			}

			defer resp.Body.Close()

			ttfb := time.Since(start)

			mu.Lock()

			latencies = append(latencies, ttfb)
			mu.Unlock()

			if resp.StatusCode != http.StatusOK {
				errorCount.Add(1)

				return
			}

			// Read stream until context is cancelled.
			buf := make([]byte, 32*1024)
			for {
				n, readErr := resp.Body.Read(buf)
				totalBytes.Add(int64(n))

				if readErr != nil {
					break
				}
			}
		})
	}

	wg.Wait()

	elapsed := flagDuration

	mu.Lock()
	stats := computeLatencyStats(latencies)
	requestCount := len(latencies)
	mu.Unlock()

	printResult(TestResult{
		TestName:   fmt.Sprintf("Stream: %s (%d clients)", cameraID, flagConcurrency),
		Duration:   elapsed,
		Clients:    flagConcurrency,
		Requests:   requestCount,
		Errors:     int(errorCount.Load()),
		Latencies:  stats,
		Throughput: float64(totalBytes.Load()) / elapsed.Seconds() / 1024, // KB/s
	})

	return nil
}
