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

func playbackCmd() *cobra.Command {
	var (
		cameraID string
		lookback time.Duration
	)

	cmd := &cobra.Command{
		Use:   "playback",
		Short: "Simulate concurrent playback clients on /api/cameras/{id}/playback",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cameraID == "" {
				return errors.New("--camera-id is required")
			}

			return runPlaybackTest(cameraID, lookback)
		},
	}

	cmd.Flags().StringVar(&cameraID, "camera-id", "", "Camera ID to play back")
	cmd.Flags().DurationVar(&lookback, "lookback", 5*time.Minute, "How far back to start playback")

	return cmd
}

func runPlaybackTest(cameraID string, lookback time.Duration) error {
	now := time.Now().UTC()
	start := now.Add(-lookback)

	url := fmt.Sprintf("%s/api/cameras/%s/playback?start=%s&end=%s",
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
		totalBytes atomic.Int64
		errorCount atomic.Int32
	)

	for range flagConcurrency {
		wg.Go(func() {
			reqStart := time.Now()

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

			ttfb := time.Since(reqStart)

			mu.Lock()

			latencies = append(latencies, ttfb)
			mu.Unlock()

			if resp.StatusCode != http.StatusOK {
				errorCount.Add(1)

				return
			}

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

	mu.Lock()
	stats := computeLatencyStats(latencies)
	requestCount := len(latencies)
	mu.Unlock()

	printResult(TestResult{
		TestName: fmt.Sprintf(
			"Playback: %s (%d clients, %s lookback)",
			cameraID,
			flagConcurrency,
			lookback,
		),
		Duration:   flagDuration,
		Clients:    flagConcurrency,
		Requests:   requestCount,
		Errors:     int(errorCount.Load()),
		Latencies:  stats,
		Throughput: float64(totalBytes.Load()) / flagDuration.Seconds() / 1024,
	})

	return nil
}
