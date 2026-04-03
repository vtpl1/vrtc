package metrics

import (
	"context"
	"runtime"
	"time"

	"github.com/vtpl1/vrtc-sdk/av"
)

const snapshotInterval = 30 * time.Second

// ActiveSegmentCounter returns the number of active recording segments.
type ActiveSegmentCounter interface {
	ActiveCount() int
}

// ViewerCounter returns the active viewer count.
type ViewerCounter interface {
	ViewerCount() int
}

// Collector instruments specific code paths and periodically snapshots system stats.
type Collector struct {
	store      *Store
	hub        av.RelayHub
	recManager ActiveSegmentCounter
	viewSvc    ViewerCounter
	startTime  time.Time
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewCollector creates a Collector and starts the periodic snapshot goroutine.
func NewCollector(
	store *Store,
	hub av.RelayHub,
	recManager ActiveSegmentCounter,
	viewSvc ViewerCounter,
) *Collector {
	ctx, cancel := context.WithCancel(context.Background())

	c := &Collector{
		store:      store,
		hub:        hub,
		recManager: recManager,
		viewSvc:    viewSvc,
		startTime:  time.Now(),
		cancel:     cancel,
		done:       make(chan struct{}),
	}

	go c.snapshotLoop(ctx)

	return c
}

// Store returns the underlying metrics store for direct queries.
func (c *Collector) Store() *Store { return c.store }

// RecordLiveViewStartup records the latency of a live view Consume() call.
func (c *Collector) RecordLiveViewStartup(d time.Duration, cameraID string) {
	c.store.RecordLatency(MetricLiveViewStartupMs, d, map[string]string{"camera_id": cameraID})
}

// RecordRTSPSessionSetup records demuxer factory latency.
func (c *Collector) RecordRTSPSessionSetup(d time.Duration, sourceID string) {
	c.store.RecordLatency(MetricRTSPSessionSetupMs, d, map[string]string{"source_id": sourceID})
}

// RecordConsumerAdd records time to attach a new consumer.
func (c *Collector) RecordConsumerAdd(d time.Duration, consumerID string) {
	c.store.RecordLatency(MetricConsumerAddMs, d, map[string]string{"consumer_id": consumerID})
}

// RecordAPIResponse records per-endpoint HTTP latency.
func (c *Collector) RecordAPIResponse(d time.Duration, method, path string, status int) {
	c.store.RecordLatency(MetricAPIResponseMs, d, map[string]string{
		"method": method,
		"path":   path,
	})

	_ = status // available for future per-status tracking
}

// RecordFragmentGap records a DTS discontinuity at a fragment boundary.
func (c *Collector) RecordFragmentGap(d time.Duration, cameraID string) {
	c.store.RecordLatency(MetricFragmentGapMs, d, map[string]string{"camera_id": cameraID})
}

// RecordRecordingGap records a gap between consecutive segments.
func (c *Collector) RecordRecordingGap(d time.Duration, channelID string) {
	c.store.RecordCounter(
		MetricRecordingGapSeconds,
		d.Seconds(),
		map[string]string{"channel_id": channelID},
	)
}

// Uptime returns the service uptime.
func (c *Collector) Uptime() time.Duration {
	return time.Since(c.startTime)
}

// RelayMetrics returns derived KPIs from current relay stats.
func (c *Collector) RelayMetrics(ctx context.Context) []RelayMetrics {
	stats := c.hub.GetRelayStats(ctx)
	result := make([]RelayMetrics, len(stats))

	for i, rs := range stats {
		var lossRate float64
		if rs.PacketsRead > 0 {
			lossRate = float64(rs.DroppedPackets) / float64(rs.PacketsRead)
		}

		var uptime float64
		if !rs.StartedAt.IsZero() {
			uptime = time.Since(rs.StartedAt).Seconds()
		}

		result[i] = RelayMetrics{
			SourceID:      rs.ID,
			FrameLossRate: lossRate,
			ActualFPS:     rs.ActualFPS,
			BitrateBps:    rs.BitrateBps,
			ConsumerCount: rs.ConsumerCount,
			UptimeSeconds: uptime,
		}
	}

	return result
}

// Stop cancels the snapshot goroutine and waits for it to exit.
func (c *Collector) Stop() {
	c.cancel()
	<-c.done
}

func (c *Collector) snapshotLoop(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.takeSnapshot(ctx)
		}
	}
}

func (c *Collector) takeSnapshot(ctx context.Context) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	relays := c.hub.GetRelayStats(ctx)

	var totalPackets, totalDropped uint64

	var fpsSum, bitrateSum float64

	for _, rs := range relays {
		totalPackets += rs.PacketsRead
		totalDropped += rs.DroppedPackets
		fpsSum += rs.ActualFPS
		bitrateSum += rs.BitrateBps
	}

	var avgFPS float64
	if len(relays) > 0 {
		avgFPS = fpsSum / float64(len(relays))
	}

	activeSegments := 0
	if c.recManager != nil {
		activeSegments = c.recManager.ActiveCount()
	}

	activeViewers := 0
	if c.viewSvc != nil {
		activeViewers = c.viewSvc.ViewerCount()
	}

	c.store.RecordSnapshot(Snapshot{
		Timestamp:      time.Now().UTC(),
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocMB:    float64(ms.Alloc) / (1 << 20),
		ActiveRelays:   len(relays),
		ActiveViewers:  activeViewers,
		ActiveSegments: activeSegments,
		TotalPackets:   totalPackets,
		TotalDropped:   totalDropped,
		AvgFPS:         avgFPS,
		TotalBitrate:   bitrateSum,
	})
}
