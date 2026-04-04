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

func burstCmd() *cobra.Command {
	var (
		cameraID string
		cycles   int
		pause    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "burst",
		Short: "Rapid connect/disconnect cycles to stress consumer lifecycle",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cameraID == "" {
				return errors.New("--camera-id is required")
			}

			return runBurstTest(cameraID, cycles, pause)
		},
	}

	cmd.Flags().StringVar(&cameraID, "camera-id", "", "Camera ID to burst-test")
	cmd.Flags().IntVar(&cycles, "cycles", 100, "Number of connect/disconnect cycles")
	cmd.Flags().DurationVar(&pause, "pause", 50*time.Millisecond, "Pause between cycles")

	return cmd
}

func runBurstTest(cameraID string, cycles int, pause time.Duration) error {
	url := fmt.Sprintf("%s/api/cameras/%s/stream", flagTarget, cameraID)

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		latencies  []time.Duration
		errorCount atomic.Int32
	)

	start := time.Now()

	for range flagConcurrency {
		wg.Go(func() {
			for range cycles {
				d, err := burstOnce(url)
				if err != nil {
					errorCount.Add(1)
				} else {
					mu.Lock()

					latencies = append(latencies, d)
					mu.Unlock()
				}

				time.Sleep(pause)
			}
		})
	}

	wg.Wait()

	elapsed := time.Since(start)
	totalCycles := flagConcurrency * cycles

	mu.Lock()
	stats := computeLatencyStats(latencies)
	mu.Unlock()

	printResult(TestResult{
		TestName: fmt.Sprintf(
			"Burst: %s (%d clients x %d cycles)",
			cameraID,
			flagConcurrency,
			cycles,
		),
		Duration:   elapsed,
		Clients:    flagConcurrency,
		Requests:   totalCycles,
		Errors:     int(errorCount.Load()),
		Latencies:  stats,
		Throughput: float64(totalCycles) / elapsed.Seconds(),
	})

	return nil
}

// burstOnce connects, reads a small amount, then disconnects. Returns TTFB.
func burstOnce(url string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}

	ttfb := time.Since(start)

	// Read a small chunk then close (simulates quick disconnect).
	buf := make([]byte, 4096)
	_, _ = resp.Body.Read(buf)
	resp.Body.Close()

	return ttfb, nil
}
