package recorder

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/segment"
	"github.com/vtpl1/vrtc/pkg/schedule"
)

// StreamConsumer is the subset of av.RelayHub that RecordingManager needs.
// Both relayhub.RelayHub and any custom implementation satisfy this interface.
type StreamConsumer interface {
	Consume(ctx context.Context, sourceID string, opts av.ConsumeOptions) (av.ConsumerHandle, error)
}

// ScheduleSource is the subset of schedule.ScheduleProvider that
// RecordingManager needs. Any provider that can list schedules satisfies it.
type ScheduleSource interface {
	ListSchedules(ctx context.Context) ([]schedule.Schedule, error)
}

// ChannelSource is the subset of channel.ChannelProvider that
// RecordingManager needs for default-schedule generation.
type ChannelSource interface {
	ListChannels(ctx context.Context) ([]Channel, error)
}

// Channel mirrors the fields RecordingManager needs from channel.Channel.
type Channel struct {
	ID string
}

// activeRec tracks one in-progress recording segment.
type activeRec struct {
	sched     schedule.Schedule
	handle    av.ConsumerHandle
	muxer     *segment.SegmentMuxer // for BytesWritten() check
	startTime time.Time
}

// RecordingManager polls a ScheduleSource and maintains fMP4 recording
// segments on disk. It attaches / detaches consumers on the StreamConsumer
// as schedules become active or inactive and rotates segments when the
// configured SegmentMinutes or SegmentSizeMB threshold is reached.
type RecordingManager struct {
	sm           StreamConsumer
	schedules    ScheduleSource
	index        RecordingIndex
	pollInterval time.Duration

	channels           ChannelSource
	defaultStoragePath string

	mu          sync.Mutex
	active      map[string]*activeRec          // key = scheduleID
	ringBuffers map[string]*segment.RingBuffer // key = channelID
	failedStart map[string]string              // key = scheduleID, value = error message (warn once)

	lastRetention time.Time

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

// Option configures optional RecordingManager behaviour.
type Option func(*RecordingManager)

// WithDefaultRecording enables recording for channels that have no explicit
// schedule. channels provides the channel list; storagePath is the base
// directory for default segments.
func WithDefaultRecording(channels ChannelSource, storagePath string) Option {
	return func(rm *RecordingManager) {
		rm.channels = channels
		rm.defaultStoragePath = storagePath
	}
}

// New creates a RecordingManager. Call Start to begin the poll loop.
//
// sm can be a full av.RelayHub or any type with a Consume method.
// schedProvider can be a schedule.ScheduleProvider or any type with ListSchedules.
func New(
	sm StreamConsumer,
	schedProvider ScheduleSource,
	index RecordingIndex,
	pollInterval time.Duration,
	opts ...Option,
) *RecordingManager {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}

	rm := &RecordingManager{
		sm:           sm,
		schedules:    schedProvider,
		index:        index,
		pollInterval: pollInterval,
		active:       make(map[string]*activeRec),
		ringBuffers:  make(map[string]*segment.RingBuffer),
		failedStart:  make(map[string]string),
		done:         make(chan struct{}),
	}

	for _, o := range opts {
		o(rm)
	}

	return rm
}

// Start seals any recordings interrupted by a previous run, then launches the
// background poll goroutine.
func (rm *RecordingManager) Start(ctx context.Context) error {
	if err := rm.index.SealInterrupted(ctx); err != nil {
		log.Error().Err(err).Msg("recorder: seal interrupted recordings")
	}

	ctx, cancel := context.WithCancel(ctx) //nolint:gosec
	rm.cancel = cancel

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

// ActiveCount returns the number of recording segments currently in progress.
func (rm *RecordingManager) ActiveCount() int {
	rm.mu.Lock()
	n := len(rm.active)
	rm.mu.Unlock()

	return n
}

// ActiveChannels returns the set of channel IDs that are currently recording.
func (rm *RecordingManager) ActiveChannels() map[string]bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	result := make(map[string]bool, len(rm.active))
	for _, ar := range rm.active {
		result[ar.sched.ChannelID] = true
	}

	return result
}

// RingBuffer returns the ring buffer for a channel, or nil if not enabled.
func (rm *RecordingManager) RingBuffer(channelID string) *segment.RingBuffer {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	return rm.ringBuffers[channelID]
}

// Metrics returns a point-in-time snapshot of the recording system's health.
func (rm *RecordingManager) Metrics(ctx context.Context) Metrics {
	rm.mu.Lock()
	activeCount := len(rm.active)
	activeChannels := make(map[string]bool, activeCount)

	for _, ar := range rm.active {
		activeChannels[ar.sched.ChannelID] = true
	}

	ringBufSizes := make(map[string]int64, len(rm.ringBuffers))

	for ch, rb := range rm.ringBuffers {
		ringBufSizes[ch] = rb.SizeBytes()
	}

	lastRet := rm.lastRetention
	rm.mu.Unlock()

	// Query index for all channels' stats.
	allEntries, _ := rm.index.QueryByChannel(ctx, "", time.Time{}, time.Time{})

	perChannel, totalSize := buildPerChannelStats(allEntries, activeChannels, ringBufSizes)

	var diskFree, diskTotal int64

	// Use the first active schedule's storage path for disk check.
	rm.mu.Lock()
	for _, ar := range rm.active {
		diskFree, diskTotal, _ = CheckDiskSpace(ar.sched.StoragePath)

		break
	}
	rm.mu.Unlock()

	return Metrics{
		ActiveSegments: activeCount,
		TotalSegments:  len(allEntries),
		TotalSizeBytes: totalSize,
		DiskFreeBytes:  diskFree,
		DiskTotalBytes: diskTotal,
		PerChannel:     perChannel,
		LastRetention:  lastRet,
	}
}

func buildPerChannelStats(
	entries []RecordingEntry,
	activeChannels map[string]bool,
	ringBufSizes map[string]int64,
) (map[string]ChannelStats, int64) {
	perChannel := make(map[string]ChannelStats)

	var totalSize int64

	for _, e := range entries {
		cs := perChannel[e.ChannelID]
		cs.Segments++
		cs.TotalBytes += e.SizeBytes
		totalSize += e.SizeBytes

		if cs.OldestSegment.IsZero() || e.StartTime.Before(cs.OldestSegment) {
			cs.OldestSegment = e.StartTime
		}

		if e.StartTime.After(cs.NewestSegment) {
			cs.NewestSegment = e.StartTime
		}

		cs.Recording = activeChannels[e.ChannelID]
		cs.RingBufBytes = ringBufSizes[e.ChannelID]
		perChannel[e.ChannelID] = cs
	}

	return perChannel, totalSize
}

func (rm *RecordingManager) loop(ctx context.Context) {
	defer close(rm.done)

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

func (rm *RecordingManager) tick(ctx context.Context) {
	schedules, err := rm.schedules.ListSchedules(ctx)
	if err != nil {
		log.Error().Err(err).Msg("recorder: list schedules")

		return
	}

	// Append default schedules for channels not covered by any explicit schedule.
	schedules = rm.appendDefaults(ctx, schedules)

	now := time.Now().UTC()
	activeIDs := make(map[string]struct{}, len(schedules))

	var wg sync.WaitGroup

	for _, s := range schedules {
		if schedule.IsActive(s, now) {
			activeIDs[s.ID] = struct{}{}

			wg.Go(func() {
				rm.processSchedule(ctx, s, now)
			})
		}
	}

	wg.Wait()

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

	// Enforce retention.
	for _, s := range schedules {
		if rm.hasRetentionPolicy(s) {
			rm.enforceRetention(ctx, s, now)
		}
	}
}

func (rm *RecordingManager) processSchedule(
	ctx context.Context,
	s schedule.Schedule,
	now time.Time,
) {
	// Disk-full check before starting or continuing.
	if s.MinFreeGB > 0 || s.LowFreeGB > 0 {
		rm.checkDiskSpace(ctx, s, now)
	}

	rm.mu.Lock()
	ar, exists := rm.active[s.ID]
	rm.mu.Unlock()

	if !exists {
		// Check emergency disk-full: skip starting if below MinFreeGB.
		if s.MinFreeGB > 0 {
			avail, _, diskErr := CheckDiskSpace(s.StoragePath)
			if diskErr == nil && avail < int64(s.MinFreeGB*1024*1024*1024) {
				log.Warn().
					Str("schedule", s.ID).
					Float64("minFreeGb", s.MinFreeGB).
					Int64("availBytes", avail).
					Msg("recorder: disk below MinFreeGB, skipping new segment")

				return
			}
		}

		rm.startSegment(ctx, s, now)

		return
	}

	// Time-based rotation.
	if s.SegmentMinutes > 0 {
		if now.Sub(ar.startTime) >= time.Duration(s.SegmentMinutes)*time.Minute {
			rm.rotateSegment(ctx, ar, s, now)

			return
		}
	}

	// Size-based rotation.
	if s.SegmentSizeMB > 0 && ar.muxer != nil {
		if ar.muxer.BytesWritten() >= int64(s.SegmentSizeMB)*1024*1024 {
			rm.rotateSegment(ctx, ar, s, now)
		}
	}
}

func (rm *RecordingManager) hasRetentionPolicy(s schedule.Schedule) bool {
	return s.MaxAgeDays > 0 || s.MaxStorageGB > 0 || s.ContinuousDays > 0 ||
		s.MotionDays > 0 || s.ObjectDays > 0 || s.MinFreeGB > 0
}

// makeOnCloseCallback returns a SegmentCloseInfo handler that validates the
// completed segment, updates its status in the index, and logs any corruption.
//

func (rm *RecordingManager) makeOnCloseCallback(
	consumerID string,
	s schedule.Schedule,
) func(segment.SegmentCloseInfo) {
	return func(info segment.SegmentCloseInfo) {
		// Rename to final path: HHmmss.fmp4 → HHmmss_HHmmss.fmp4
		finalPath := SegmentPathFinal(s.StoragePath, s.ChannelID, info.Start, info.End)

		if err := os.Rename(info.Path, finalPath); err != nil {
			log.Warn().Err(err).
				Str("from", info.Path).
				Str("to", finalPath).
				Msg("recorder: rename segment failed, keeping original path")

			finalPath = info.Path
		}

		entry := RecordingEntry{
			ID:         consumerID,
			ChannelID:  s.ChannelID,
			StartTime:  info.Start,
			EndTime:    info.End,
			FilePath:   finalPath,
			SizeBytes:  info.SizeBytes,
			Status:     StatusComplete,
			HasMotion:  info.HasMotion,
			HasObjects: info.HasObjects,
		}

		// Downgrade to corrupted if validation failed.
		if info.ValidationError != nil {
			entry.Status = StatusCorrupted

			log.Warn().
				Err(info.ValidationError).
				Str("path", finalPath).
				Msg("recorder: segment corrupted")
		}

		if iErr := rm.index.Insert(context.Background(), entry); iErr != nil {
			log.Error().Err(iErr).Msg("recorder: index insert complete")
		}
	}
}

// segmentPreallocBytes returns the number of bytes to pre-allocate for a
// segment file, based on the schedule's size or duration hint.
func segmentPreallocBytes(s schedule.Schedule) int64 {
	if s.SegmentSizeMB > 0 {
		return int64(s.SegmentSizeMB) * 1024 * 1024
	}

	if s.SegmentMinutes > 0 {
		return int64(s.SegmentMinutes) * 60 * 1_200_000 // ~1.2 MB/s estimate
	}

	return 0
}

// registerActiveSegment inserts the "recording" status entry into the index
// and adds the segment to the active map.
func (rm *RecordingManager) registerActiveSegment(
	ctx context.Context,
	s schedule.Schedule,
	consumerID, path string,
	now time.Time,
	handle av.ConsumerHandle,
	muxerRef *segment.SegmentMuxer,
) {
	startEntry := RecordingEntry{
		ID:        consumerID,
		ChannelID: s.ChannelID,
		StartTime: now,
		FilePath:  path,
		Status:    StatusRecording,
	}

	if iErr := rm.index.Insert(ctx, startEntry); iErr != nil {
		log.Error().Err(iErr).Msg("recorder: index insert recording")
	}

	rm.mu.Lock()
	rm.active[s.ID] = &activeRec{
		sched:     s,
		handle:    handle,
		muxer:     muxerRef,
		startTime: now,
	}
	rm.mu.Unlock()
}

//nolint:funlen // segment start wiring cannot be split cleanly
func (rm *RecordingManager) startSegment(ctx context.Context, s schedule.Schedule, now time.Time) {
	path := SegmentPath(s.StoragePath, s.ChannelID, now)
	consumerID := fmt.Sprintf("recorder-%s-%s", s.ID, now.Format("20060102T150405Z"))

	profile := segment.StorageProfile(s.StorageProfile)
	if profile == "" {
		profile = segment.ProfileAuto
	}

	ring := rm.getOrCreateRingBuffer(s)

	var muxerRef *segment.SegmentMuxer

	onClose := rm.makeOnCloseCallback(consumerID, s)
	preallocBytes := segmentPreallocBytes(s)

	maxBytes := int64(0)
	if s.SegmentSizeMB > 0 {
		maxBytes = int64(s.SegmentSizeMB) * 1024 * 1024
	}

	muxerFactory := av.MuxerFactory(func(_ context.Context, _ string) (av.MuxCloser, error) {
		mux, err := segment.NewSegmentMuxer(
			path,
			now,
			profile,
			maxBytes,
			preallocBytes,
			ring,
			onClose,
		)
		if err != nil {
			return nil, err
		}

		muxerRef = mux

		return mux, nil
	})

	errChan := make(chan error, 1)

	handle, err := rm.sm.Consume(ctx, s.ChannelID, av.ConsumeOptions{
		ConsumerID:   consumerID,
		MuxerFactory: muxerFactory,
		ErrChan:      errChan,
	})
	if err != nil {
		errMsg := err.Error()

		rm.mu.Lock()
		prev := rm.failedStart[s.ID]
		rm.failedStart[s.ID] = errMsg
		rm.mu.Unlock()

		// Only warn once per distinct error to avoid log spam on every tick.
		if prev != errMsg {
			log.Warn().
				Err(err).
				Str("schedule", s.ID).
				Str("channel", s.ChannelID).
				Msg("recorder: skipping schedule, channel unavailable")
		}

		return
	}

	// Clear any previous failure on success.
	rm.mu.Lock()
	delete(rm.failedStart, s.ID)
	rm.mu.Unlock()

	log.Debug().
		Str("schedule", s.ID).
		Str("channel", s.ChannelID).
		Str("consumer", consumerID).
		Str("path", path).
		Msg("recorder: recording started")

	rm.registerActiveSegment(ctx, s, consumerID, path, now, handle, muxerRef)

	// Listen for async muxer errors (e.g. file creation failure, disk full).
	// Select on ctx.Done() so this goroutine exits when the segment is closed
	// during rotation, preventing a goroutine leak on the happy path.
	go func() {
		select {
		case <-ctx.Done():
			return
		case muxErr, ok := <-errChan:
			if !ok || muxErr == nil {
				return
			}

			log.Warn().
				Err(muxErr).
				Str("schedule", s.ID).
				Str("channel", s.ChannelID).
				Str("consumer", consumerID).
				Msg("recorder: muxer error during recording")
		}
	}()
}

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
		log.Error().Err(err).Str("schedule", ar.sched.ID).Msg("recorder: close handle")
	}

	log.Debug().
		Str("schedule", ar.sched.ID).
		Str("channel", ar.sched.ChannelID).
		Dur("duration", time.Since(ar.startTime)).
		Msg("recorder: recording stopped")
}

func (rm *RecordingManager) enforceRetention(
	ctx context.Context,
	s schedule.Schedule,
	now time.Time,
) {
	entries, err := rm.index.QueryByChannel(ctx, s.ChannelID, time.Time{}, time.Time{})
	if err != nil {
		log.Error().Err(err).Str("channel", s.ChannelID).Msg("recorder: retention query")

		return
	}

	// Build retention policy from schedule fields.
	continuousDays := s.ContinuousDays
	if continuousDays == 0 && s.MaxAgeDays > 0 {
		continuousDays = s.MaxAgeDays // backward compat
	}

	var diskFree int64

	if s.MinFreeGB > 0 {
		diskFree, _, _ = CheckDiskSpace(s.StoragePath)
	}

	policy := RetentionPolicy{
		ContinuousDays: continuousDays,
		MotionDays:     s.MotionDays,
		ObjectDays:     s.ObjectDays,
		MaxStorageGB:   s.MaxStorageGB,
		MinFreeGB:      s.MinFreeGB,
		DiskFreeBytes:  diskFree,
	}

	toDelete := EvaluateRetention(entries, policy, now)

	// Batch limit: delete up to 10 per tick.
	if len(toDelete) > 10 {
		toDelete = toDelete[:10]
	}

	for _, e := range toDelete {
		rm.deleteSegment(ctx, e)
	}

	if len(toDelete) > 0 {
		rm.mu.Lock()
		rm.lastRetention = now
		rm.mu.Unlock()
	}
}

func (rm *RecordingManager) deleteSegment(ctx context.Context, e RecordingEntry) {
	// Mark deleted in the index FIRST so that playback queries never return
	// a segment whose file has already been removed. If the process crashes
	// after the index update but before the file removal, the orphan file is
	// harmless and will be cleaned up by the next retention pass.
	if err := rm.index.Delete(ctx, e.ID); err != nil {
		log.Error().Err(err).Str("id", e.ID).Msg("recorder: mark segment deleted")

		return
	}

	if err := os.Remove(e.FilePath); err != nil && !os.IsNotExist(err) {
		log.Error().Err(err).Str("file", e.FilePath).Msg("recorder: delete segment file")
	}

	log.Info().
		Str("id", e.ID).
		Str("channel", e.ChannelID).
		Str("file", e.FilePath).
		Time("start", e.StartTime).
		Int64("sizeBytes", e.SizeBytes).
		Msg("recorder: segment deleted by retention policy")
}

func (rm *RecordingManager) checkDiskSpace(
	ctx context.Context,
	s schedule.Schedule,
	now time.Time,
) {
	avail, _, err := CheckDiskSpace(s.StoragePath)
	if err != nil {
		return
	}

	if s.LowFreeGB > 0 && avail < int64(s.LowFreeGB*1024*1024*1024) {
		log.Warn().
			Str("channel", s.ChannelID).
			Float64("lowFreeGb", s.LowFreeGB).
			Int64("availBytes", avail).
			Msg("recorder: disk below LowFreeGB, triggering aggressive retention")

		rm.enforceRetention(ctx, s, now)
		rm.enforceRetention(ctx, s, now) // double pass for aggressive cleanup
	}
}

func (rm *RecordingManager) getOrCreateRingBuffer(s schedule.Schedule) *segment.RingBuffer {
	if s.RingBufferSeconds <= 0 {
		return nil
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	rb, ok := rm.ringBuffers[s.ChannelID]
	if !ok {
		rb = segment.NewRingBuffer(time.Duration(s.RingBufferSeconds) * time.Second)
		rm.ringBuffers[s.ChannelID] = rb
	}

	return rb
}

const defaultSegmentMinutes = 5

// appendDefaults adds a synthetic always-active schedule for every channel
// that is not already covered by an explicit schedule.
func (rm *RecordingManager) appendDefaults(
	ctx context.Context,
	schedules []schedule.Schedule,
) []schedule.Schedule {
	if rm.channels == nil {
		return schedules
	}

	channels, err := rm.channels.ListChannels(ctx)
	if err != nil {
		log.Error().Err(err).Msg("recorder: list channels for defaults")

		return schedules
	}

	covered := make(map[string]struct{}, len(schedules))
	for _, s := range schedules {
		covered[s.ChannelID] = struct{}{}
	}

	for _, ch := range channels {
		if _, ok := covered[ch.ID]; ok {
			continue
		}

		schedules = append(schedules, schedule.Schedule{
			ID:             "default-" + ch.ID,
			ChannelID:      ch.ID,
			StoragePath:    rm.defaultStoragePath,
			SegmentMinutes: defaultSegmentMinutes,
		})

		log.Debug().Str("channel", ch.ID).Msg("recorder: using default schedule")
	}

	return schedules
}
