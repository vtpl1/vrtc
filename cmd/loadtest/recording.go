package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

func recordingCmd() *cobra.Command {
	var (
		cameraID string
		lookback time.Duration
	)

	cmd := &cobra.Command{
		Use:   "recording",
		Short: "Verify recording continuity by checking for timeline gaps",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cameraID == "" {
				return errors.New("--camera-id is required")
			}

			return runRecordingTest(cameraID, lookback)
		},
	}

	cmd.Flags().StringVar(&cameraID, "camera-id", "", "Camera ID to check")
	cmd.Flags().DurationVar(&lookback, "lookback", time.Hour, "How far back to check")

	return cmd
}

type timelineEntry struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs int64     `json:"duration_ms"` //nolint:tagliatelle
}

func runRecordingTest(cameraID string, lookback time.Duration) error {
	now := time.Now().UTC()
	start := now.Add(-lookback)

	url := fmt.Sprintf("%s/api/cameras/%s/recordings?start=%s&end=%s",
		flagTarget, cameraID,
		start.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var entries []timelineEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	// Analyze gaps.
	var (
		gapCount int
		maxGap   time.Duration
		totalGap time.Duration
	)

	for i := 1; i < len(entries); i++ {
		gap := entries[i].Start.Sub(entries[i-1].End)
		if gap > 2*time.Second { // > 2s counts as a gap
			gapCount++
			totalGap += gap

			if gap > maxGap {
				maxGap = gap
			}
		}
	}

	// Compute uptime.
	var recordedTime time.Duration
	for _, e := range entries {
		recordedTime += time.Duration(e.DurationMs) * time.Millisecond
	}

	uptimeRatio := 0.0
	if lookback > 0 {
		uptimeRatio = float64(recordedTime) / float64(lookback) * 100
	}

	if flagOutputJSON {
		result := map[string]any{
			"camera_id":     cameraID,
			"lookback":      lookback.String(),
			"segments":      len(entries),
			"gaps":          gapCount,
			"max_gap":       maxGap.String(),
			"total_gap":     totalGap.String(),
			"recorded_time": recordedTime.String(),
			"uptime_pct":    uptimeRatio,
		}

		enc := json.NewEncoder(printWriter)
		enc.SetIndent("", "  ")

		return enc.Encode(result)
	}

	fmt.Println("┌─────────────────────────────────────────┐")
	fmt.Printf("│ Recording: %-28s │\n", cameraID)
	fmt.Println("├─────────────────────────────────────────┤")
	fmt.Printf("│ Lookback:      %-24s │\n", lookback)
	fmt.Printf("│ Segments:      %-24d │\n", len(entries))
	fmt.Printf("│ Gaps (>2s):    %-24d │\n", gapCount)
	fmt.Printf("│ Max gap:       %-24s │\n", maxGap.Round(time.Millisecond))
	fmt.Printf("│ Total gap:     %-24s │\n", totalGap.Round(time.Millisecond))
	fmt.Printf("│ Recorded time: %-24s │\n", recordedTime.Round(time.Second))
	fmt.Printf("│ Uptime:        %-22.2f %% │\n", uptimeRatio)

	// KPI check
	if uptimeRatio >= 99.9 {
		fmt.Println("│ Status:        PASS                     │")
	} else {
		fmt.Println("│ Status:        FAIL (< 99.9%)           │")
	}

	fmt.Println("└─────────────────────────────────────────┘")

	return nil
}

var printWriter = writerStdout{}

type writerStdout struct{}

func (writerStdout) Write(p []byte) (int, error) {
	return fmt.Print(string(p))
}
