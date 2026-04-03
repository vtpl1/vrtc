package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagTarget      string
	flagConcurrency int
	flagDuration    time.Duration
	flagOutputJSON  bool
)

func main() {
	root := &cobra.Command{
		Use:   "loadtest",
		Short: "Load testing tool for vrtc liverecservice",
	}

	root.PersistentFlags().
		StringVarP(&flagTarget, "target", "t", "http://localhost:8080", "Base URL of the service")
	root.PersistentFlags().
		IntVarP(&flagConcurrency, "concurrency", "c", 10, "Number of concurrent clients")
	root.PersistentFlags().
		DurationVarP(&flagDuration, "duration", "d", 30*time.Second, "Test duration")
	root.PersistentFlags().
		BoolVar(&flagOutputJSON, "json", false, "Output as JSON instead of table")

	root.AddCommand(streamCmd(), burstCmd(), apiCmd(), recordingCmd(), playbackCmd(), timelineCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
