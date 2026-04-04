package edge

import (
	"context"
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

// healthMemory contains Go runtime memory counters.
type healthMemory struct {
	AllocMB   float64 `json:"allocMb"`
	SysMB     float64 `json:"sysMb"`
	HeapInuse float64 `json:"heapInuseMb"`
	GCRuns    uint32  `json:"gcRuns"`
}

// healthStreams summarises the live StreamManager state.
type healthStreams struct {
	ActiveProducers int `json:"activeProducers"`
}

// healthRecorder summarises the RecordingManager state.
type healthRecorder struct {
	ActiveSegments int `json:"activeSegments"`
}

// healthSnapshot is the JSON body returned by GET /health and logged periodically.
type healthSnapshot struct {
	Status     string         `json:"status"`
	UptimeSec  int64          `json:"uptimeSeconds"`
	Goroutines int            `json:"goroutines"`
	Memory     healthMemory   `json:"memory"`
	Streams    healthStreams  `json:"streams"`
	Recorder   healthRecorder `json:"recorder"`
	Timestamp  time.Time      `json:"timestamp"`
}

func collectHealth(
	ctx context.Context,
	sm av.RelayHub,
	rm *recorder.RecordingManager,
	startTime time.Time,
) healthSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return healthSnapshot{
		Status:     "ok",
		UptimeSec:  int64(time.Since(startTime).Seconds()),
		Goroutines: runtime.NumGoroutine(),
		Memory: healthMemory{
			AllocMB:   float64(ms.Alloc) / (1 << 20),
			SysMB:     float64(ms.Sys) / (1 << 20),
			HeapInuse: float64(ms.HeapInuse) / (1 << 20),
			GCRuns:    ms.NumGC,
		},
		Streams: healthStreams{
			ActiveProducers: sm.GetActiveRelayCount(ctx),
		},
		Recorder: healthRecorder{
			ActiveSegments: rm.ActiveCount(),
		},
		Timestamp: time.Now().UTC(),
	}
}

// startHealthLogger logs a health snapshot at the given interval until ctx is cancelled.
func startHealthLogger(
	ctx context.Context,
	sm av.RelayHub,
	rm *recorder.RecordingManager,
	startTime time.Time,
	interval time.Duration,
) {
	if interval <= 0 {
		interval = 60 * time.Second
	}

	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap := collectHealth(ctx, sm, rm, startTime)
				log.Info().
					Int64("uptime_seconds", snap.UptimeSec).
					Int("goroutines", snap.Goroutines).
					Float64("alloc_mb", snap.Memory.AllocMB).
					Float64("sys_mb", snap.Memory.SysMB).
					Int("active_producers", snap.Streams.ActiveProducers).
					Int("active_segments", snap.Recorder.ActiveSegments).
					Msg("health")
			}
		}
	}()
}
