package liverecservice

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

// connTracker counts active client connections by transport and stream type.
// All methods are goroutine-safe.
type connTracker struct {
	httpLive     atomic.Int64
	httpRecorded atomic.Int64
	wsLive       atomic.Int64
	wsRecorded   atomic.Int64
}

// trackHTTPLive increments the live-HTTP counter and returns a release func.
func (t *connTracker) trackHTTPLive() func() {
	t.httpLive.Add(1)

	return func() { t.httpLive.Add(-1) }
}

// trackHTTPRecorded increments the recorded-HTTP counter and returns a release func.
func (t *connTracker) trackHTTPRecorded() func() {
	t.httpRecorded.Add(1)

	return func() { t.httpRecorded.Add(-1) }
}

// trackWSLive increments the live-WebSocket counter and returns a release func.
func (t *connTracker) trackWSLive() func() {
	t.wsLive.Add(1)

	return func() { t.wsLive.Add(-1) }
}

// trackWSRecorded increments the recorded-WebSocket counter and returns a release func.
func (t *connTracker) trackWSRecorded() func() {
	t.wsRecorded.Add(1)

	return func() { t.wsRecorded.Add(-1) }
}

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

// healthConsumers counts active client connections by type.
type healthConsumers struct {
	HTTPLive     int64 `json:"httpLive"`
	HTTPRecorded int64 `json:"httpRecorded"`
	WSLive       int64 `json:"wsLive"`
	WSRecorded   int64 `json:"wsRecorded"`
	Total        int64 `json:"total"`
}

// healthSnapshot is the JSON body returned by GET /health and logged periodically.
type healthSnapshot struct {
	Status     string          `json:"status"`
	UptimeSec  int64           `json:"uptimeSeconds"`
	Goroutines int             `json:"goroutines"`
	Memory     healthMemory    `json:"memory"`
	Streams    healthStreams   `json:"streams"`
	Recorder   healthRecorder  `json:"recorder"`
	Consumers  healthConsumers `json:"consumers"`
	Timestamp  time.Time       `json:"timestamp"`
}

func collectHealth(
	ctx context.Context,
	sm av.RelayHub,
	rm *recorder.RecordingManager,
	ct *connTracker,
	startTime time.Time,
) healthSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	httpLive := ct.httpLive.Load()
	httpRec := ct.httpRecorded.Load()
	wsLive := ct.wsLive.Load()
	wsRec := ct.wsRecorded.Load()

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
		Consumers: healthConsumers{
			HTTPLive:     httpLive,
			HTTPRecorded: httpRec,
			WSLive:       wsLive,
			WSRecorded:   wsRec,
			Total:        httpLive + httpRec + wsLive + wsRec,
		},
		Timestamp: time.Now().UTC(),
	}
}

// startHealthLogger logs a health snapshot at the given interval until ctx is cancelled.
func startHealthLogger(
	ctx context.Context,
	sm av.RelayHub,
	rm *recorder.RecordingManager,
	ct *connTracker,
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
				snap := collectHealth(ctx, sm, rm, ct, startTime)
				log.Info().
					Int64("uptime_seconds", snap.UptimeSec).
					Int("goroutines", snap.Goroutines).
					Float64("alloc_mb", snap.Memory.AllocMB).
					Float64("sys_mb", snap.Memory.SysMB).
					Int("active_producers", snap.Streams.ActiveProducers).
					Int("active_segments", snap.Recorder.ActiveSegments).
					Int64("consumers_http_live", snap.Consumers.HTTPLive).
					Int64("consumers_http_recorded", snap.Consumers.HTTPRecorded).
					Int64("consumers_ws_live", snap.Consumers.WSLive).
					Int64("consumers_ws_recorded", snap.Consumers.WSRecorded).
					Int64("consumers_total", snap.Consumers.Total).
					Msg("health")
			}
		}
	}()
}
