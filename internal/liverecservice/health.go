package liverecservice

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

// healthMemory contains Go runtime memory counters.
type healthMemory struct {
	AllocMB   float64 `json:"alloc_mb"`
	SysMB     float64 `json:"sys_mb"`
	HeapInuse float64 `json:"heap_inuse_mb"`
	GCRuns    uint32  `json:"gc_runs"`
}

// healthStreams summarises the live StreamManager state.
type healthStreams struct {
	ActiveProducers int `json:"active_producers"`
}

// healthRecorder summarises the RecordingManager state.
type healthRecorder struct {
	ActiveSegments int `json:"active_segments"`
}

// healthSnapshot is the JSON body returned by GET /health and logged periodically.
type healthSnapshot struct {
	Status    string         `json:"status"`
	UptimeSec int64          `json:"uptime_seconds"`
	Goroutines int           `json:"goroutines"`
	Memory    healthMemory   `json:"memory"`
	Streams   healthStreams   `json:"streams"`
	Recorder  healthRecorder `json:"recorder"`
	Timestamp time.Time      `json:"timestamp"`
}

func collectHealth(
	ctx context.Context,
	sm av.StreamManager,
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
			ActiveProducers: sm.GetActiveProducersCount(ctx),
		},
		Recorder: healthRecorder{
			ActiveSegments: rm.ActiveCount(),
		},
		Timestamp: time.Now().UTC(),
	}
}

// healthHandler serves GET /health as a JSON snapshot.
func healthHandler(
	ctx context.Context,
	w http.ResponseWriter,
	sm av.StreamManager,
	rm *recorder.RecordingManager,
	startTime time.Time,
) {
	snap := collectHealth(ctx, sm, rm, startTime)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	if err := json.NewEncoder(w).Encode(snap); err != nil {
		log.Error().Err(err).Msg("health: encode response")
	}
}

// startHealthLogger logs a health snapshot at the given interval until ctx is cancelled.
func startHealthLogger(
	ctx context.Context,
	sm av.StreamManager,
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
