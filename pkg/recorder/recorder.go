package recorder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/schedule"
)

// activeRec tracks one in-progress recording segment.
type activeRec struct {
	sched     schedule.Schedule
	handle    av.ConsumerHandle
	startTime time.Time
}

// RecordingManager polls a ScheduleProvider and maintains fMP4 recording
// segments on disk. It attaches / detaches consumers on the StreamManager
// as schedules become active or inactive and rotates segments when the
// configured SegmentMinutes threshold is reached.
type RecordingManager struct {
	sm           av.StreamManager
	schedules    schedule.ScheduleProvider
	index        RecordingIndex
	pollInterval time.Duration

	mu     sync.Mutex
	active map[string]*activeRec // key = scheduleID

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

// New creates a RecordingManager. Call Start to begin the poll loop.
//
//   - sm            — live StreamManager (must already be started)
//   - schedProvider — source of recording schedules
//   - index         — persistent store for completed segment metadata
//   - pollInterval  — how often to re-check schedules (e.g. 30 * time.Second)
func New(
	sm av.StreamManager,
	schedProvider schedule.ScheduleProvider,
	index RecordingIndex,
	pollInterval time.Duration,
) *RecordingManager {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}

	return &RecordingManager{
		sm:           sm,
		schedules:    schedProvider,
		index:        index,
		pollInterval: pollInterval,
		active:       make(map[string]*activeRec),
		done:         make(chan struct{}),
	}
}

// Start launches the background poll goroutine. ctx is used only to derive
// the internal context; the returned error is always nil.
func (rm *RecordingManager) Start(ctx context.Context) error {
	ctx, rm.cancel = context.WithCancel(
		ctx,
	)

	go rm.loop(ctx)

	return nil
}

// Stop signals the poll goroutine to exit and waits for all active recordings
// to be closed cleanly (up to 30 s). Safe to call multiple times.
func (rm *RecordingManager) Stop() error {
	rm.stopOnce.Do(func() {
		rm.cancel()
		<-rm.done
	})

	return nil
}

// loop is the background goroutine.
func (rm *RecordingManager) loop(ctx context.Context) {
	defer close(rm.done)

	// Run once immediately, then on each tick.
	rm.tick(ctx)

	ticker := time.NewTicker(rm.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			rm.stopAll(ctx)

			return
		case <-ticker.C:
			rm.tick(ctx)
		}
	}
}

// tick is one poll cycle.
func (rm *RecordingManager) tick(ctx context.Context) {
	schedules, err := rm.schedules.ListSchedules(ctx)
	if err != nil {
		slog.Error("recorder: list schedules", "err", err)

		return
	}

	now := time.Now().UTC()

	// Build set of currently-active schedule IDs so we can detect removals.
	activeIDs := make(map[string]struct{}, len(schedules))

	for _, s := range schedules {
		if !schedule.IsActive(s, now) {
			continue
		}

		activeIDs[s.ID] = struct{}{}

		rm.mu.Lock()
		ar, exists := rm.active[s.ID]
		rm.mu.Unlock()

		if !exists {
			rm.startSegment(ctx, s, now)

			continue
		}

		// Rotate if a segment duration is configured and elapsed.
		if s.SegmentMinutes > 0 {
			elapsed := now.Sub(ar.startTime)
			if elapsed >= time.Duration(s.SegmentMinutes)*time.Minute {
				rm.rotateSegment(ctx, ar, s, now)
			}
		}
	}

	// Stop recordings whose schedules are no longer active.
	rm.mu.Lock()

	var stale []*activeRec

	for id, ar := range rm.active {
		if _, ok := activeIDs[id]; !ok {
			stale = append(stale, ar)

			delete(rm.active, id)
		}
	}
	rm.mu.Unlock()

	for _, ar := range stale {
		rm.closeHandle(ctx, ar)
	}
}

// startSegment opens a new fMP4 file, attaches it to the StreamManager, and
// registers the activeRec. Errors are logged; the schedule is retried on the
// next tick.
func (rm *RecordingManager) startSegment(ctx context.Context, s schedule.Schedule, now time.Time) {
	path := SegmentPath(s.StoragePath, s.ChannelID, now)

	if err := os.MkdirAll(fmt.Sprintf("%s/%s", s.StoragePath, s.ChannelID), 0o750); err != nil {
		slog.Error("recorder: mkdir", "path", path, "err", err)

		return
	}

	consumerID := fmt.Sprintf("recorder-%s-%s", s.ID, now.Format("20060102T150405Z"))

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		onClose := func(filePath string, start, end time.Time, sizeBytes int64) { //nolint:contextcheck
			entry := RecordingEntry{
				ID:        consumerID,
				ChannelID: s.ChannelID,
				StartTime: start,
				EndTime:   end,
				FilePath:  filePath,
				SizeBytes: sizeBytes,
			}

			// onClose is called from the muxer's Close() which runs in a
			// detached goroutine after the stream context is cancelled, so
			// we use a fresh background context for the index write.
			if iErr := rm.index.Insert(context.Background(), entry); iErr != nil {
				slog.Error("recorder: index insert", "err", iErr)
			}
		}

		return newFMP4FileMuxer(path, now, onClose)
	})

	handle, err := rm.sm.Consume(ctx, s.ChannelID, av.ConsumeOptions{
		ConsumerID:   consumerID,
		MuxerFactory: muxerFactory,
	})
	if err != nil {
		slog.Error(
			"recorder: attach consumer",
			"schedule",
			s.ID,
			"channel",
			s.ChannelID,
			"err",
			err,
		)

		return
	}

	rm.mu.Lock()
	rm.active[s.ID] = &activeRec{sched: s, handle: handle, startTime: now}
	rm.mu.Unlock()
}

// rotateSegment closes the current segment and immediately starts a new one.
func (rm *RecordingManager) rotateSegment(
	ctx context.Context,
	ar *activeRec,
	s schedule.Schedule,
	now time.Time,
) {
	rm.mu.Lock()
	delete(rm.active, s.ID)
	rm.mu.Unlock()

	rm.closeHandle(ctx, ar)
	rm.startSegment(ctx, s, now)
}

// stopAll is called on shutdown: closes every active recording.
// parentCtx is already cancelled; a fresh timeout context is derived from
// context.WithoutCancel so that handle.Close can complete cleanly.
func (rm *RecordingManager) stopAll(parentCtx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 30*time.Second)
	defer cancel()

	rm.mu.Lock()

	all := make([]*activeRec, 0, len(rm.active))
	for _, ar := range rm.active {
		all = append(all, ar)
	}

	rm.active = make(map[string]*activeRec)
	rm.mu.Unlock()

	for _, ar := range all {
		rm.closeHandle(ctx, ar)
	}
}

func (rm *RecordingManager) closeHandle(ctx context.Context, ar *activeRec) {
	if err := ar.handle.Close(ctx); err != nil {
		slog.Error("recorder: close handle", "schedule", ar.sched.ID, "err", err)
	}
}
