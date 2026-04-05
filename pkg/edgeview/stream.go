package edgeview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/chain"
	"github.com/vtpl1/vrtc-sdk/av/format/fmp4"
	"github.com/vtpl1/vrtc-sdk/av/packetbuf"
	"github.com/vtpl1/vrtc-sdk/av/relayhub"
	"github.com/vtpl1/vrtc/pkg/channel"
	"github.com/vtpl1/vrtc/pkg/metrics"
	"github.com/vtpl1/vrtc/pkg/pva"
	"github.com/vtpl1/vrtc/pkg/pva/persistence"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

// Playback modes returned by ResolvePlaybackStart.
const (
	PlaybackModeRecorded       = "recorded"
	PlaybackModeFirstAvailable = "first_available"
	PlaybackModeLive           = "live"
)

var (
	errNoRecordingIndex  = errors.New("no recording index available on this relay")
	errNoRecordingsFound = errors.New("no recordings found for the given range")
)

// PacketBufferProvider returns the near-live replay buffer for a camera.
// Returns nil if the camera is not recording.
type PacketBufferProvider interface {
	PacketBuffer(channelID string) *packetbuf.Buffer
}

// Service provides live view and playback on any media relay instance (edge or cloud).
// It delegates stream consumption to the underlying relay hub via vrtc-sdk's
// fmp4/mse/chain packages. Live and recorded HTTP/WS handlers attach consumers
// directly to the hub rather than maintaining per-viewer frame channels.
type Service struct {
	log               zerolog.Logger
	hub               av.RelayHub
	recIndex          recorder.RecordingIndex // nil if no recording available
	bufProv           PacketBufferProvider    // nil if no recording available
	chanW             channel.ChannelWriter   // nil if no channel CRUD available
	recProv           ActiveRecordingProvider // nil if no recording manager
	collector         *metrics.Collector      // nil if metrics not configured
	cameras           map[string]*CameraInfo
	analyticsRelayHub av.RelayHub // nil if analytics hub not configured

	// Analytics-enriched recorded playback dependencies.
	persistReader      *persistence.Reader // nil if analytics persistence not configured
	liveAnalyticsStore *pva.AnalyticsStore // nil if analytics store not configured

	mu sync.RWMutex

	// activeConsumers tracks live consumer handles for viewer counting.
	consumerMu      sync.RWMutex
	activeConsumers int
}

// CameraInfo describes a camera available on this relay.
type CameraInfo struct {
	CameraID   string `doc:"Unique camera/channel identifier"            json:"cameraId"`
	Name       string `doc:"Human-readable camera name"                  json:"name"`
	Codec      string `doc:"Active video codec (e.g. H264, H265)"        json:"codec"`
	Resolution string `doc:"Video resolution (e.g. 1920x1080)"           json:"resolution"`
	FPS        int    `doc:"Configured frame rate"                       json:"fps"`
	Recording  bool   `doc:"Whether recording is active for this camera" json:"recording"`
	Analytics  bool   `doc:"Whether analytics processing is active"      json:"analytics"`
	State      string `doc:"Camera state (e.g. active, offline)"         json:"state"`
}

// NewService creates a view service attached to a media relay hub.
// recIndex, bufProv, and chanW may be nil if not available.
func NewService(
	log zerolog.Logger,
	hub av.RelayHub,
	recIndex recorder.RecordingIndex,
	bufProv PacketBufferProvider,
	opts ...ServiceOption,
) *Service {
	s := &Service{
		log:      log,
		hub:      hub,
		recIndex: recIndex,
		bufProv:  bufProv,
		cameras:  make(map[string]*CameraInfo),
	}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// ActiveRecordingProvider returns which channels are actively recording.
type ActiveRecordingProvider interface {
	ActiveChannels() map[string]bool
}

// ServiceOption configures optional Service dependencies.
type ServiceOption func(*Service)

// WithChannelWriter enables channel CRUD endpoints.
func WithChannelWriter(cw channel.ChannelWriter) ServiceOption {
	return func(s *Service) { s.chanW = cw }
}

// WithRecordingProvider sets the provider for active recording status.
func WithRecordingProvider(rp ActiveRecordingProvider) ServiceOption {
	return func(s *Service) { s.recProv = rp }
}

// WithCollector enables KPI metrics collection on streaming paths.
func WithCollector(c *metrics.Collector) ServiceOption {
	return func(s *Service) { s.collector = c }
}

// WithAnalyticsRelayHub sets the analytics relay hub for delayed,
// analytics-enriched streaming.
func WithAnalyticsRelayHub(hub av.RelayHub) ServiceOption {
	return func(s *Service) { s.analyticsRelayHub = hub }
}

// WithPersistenceReader enables analytics-enriched recorded playback by
// providing the SQLite persistence reader for historical analytics lookup.
func WithPersistenceReader(r *persistence.Reader) ServiceOption {
	return func(s *Service) { s.persistReader = r }
}

// WithLiveAnalyticsStore sets the in-memory analytics store used as a
// fallback source during the recorded-to-live playback transition.
func WithLiveAnalyticsStore(store *pva.AnalyticsStore) ServiceOption {
	return func(s *Service) { s.liveAnalyticsStore = store }
}

// Hub returns the underlying relay hub for direct consumer attachment.
func (s *Service) Hub() av.RelayHub {
	return s.hub
}

// SetAnalyticsRelayHub sets the analytics relay hub after construction.
// This is a setter rather than an option because the analytics hub depends
// on viewSvc.RecordedDemuxerFactory, creating a circular init dependency.
func (s *Service) SetAnalyticsRelayHub(hub av.RelayHub) {
	s.mu.Lock()
	s.analyticsRelayHub = hub
	s.mu.Unlock()
}

// AnalyticsRelayHub returns the analytics relay hub, or nil if not configured.
func (s *Service) AnalyticsRelayHub() av.RelayHub {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.analyticsRelayHub
}

// PersistReader returns the persistence reader, or nil if not configured.
func (s *Service) PersistReader() *persistence.Reader {
	return s.persistReader
}

// LiveAnalyticsSource returns a pva.Source backed by the live analytics store
// for the given channel. Returns nil if the live store is not configured.
func (s *Service) LiveAnalyticsSource(channelID string) pva.Source {
	if s.liveAnalyticsStore == nil {
		return nil
	}

	return s.liveAnalyticsStore.SourceFor(channelID)
}

// RecIndex returns the recording index, or nil if unavailable.
func (s *Service) RecIndex() recorder.RecordingIndex {
	return s.recIndex
}

// ChannelWriter returns the channel writer, or nil if CRUD is not available.
func (s *Service) ChannelWriter() channel.ChannelWriter {
	return s.chanW
}

// RegisterCamera makes a camera available for live view / playback.
func (s *Service) RegisterCamera(info *CameraInfo) {
	s.mu.Lock()
	s.cameras[info.CameraID] = info
	s.mu.Unlock()
}

// UnregisterCamera removes a camera from the in-memory list.
func (s *Service) UnregisterCamera(cameraID string) {
	s.mu.Lock()
	delete(s.cameras, cameraID)
	s.mu.Unlock()
}

// ListCameras returns all cameras enriched with live relay stats and recording status.
func (s *Service) ListCameras(ctx context.Context) []*CameraInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build active recording lookup.
	var activeRec map[string]bool
	if s.recProv != nil {
		activeRec = s.recProv.ActiveChannels()
	}

	result := make([]*CameraInfo, 0, len(s.cameras))

	for _, c := range s.cameras {
		info := *c
		info.Recording = activeRec[info.CameraID]

		if rs, ok := s.hub.GetRelayStatsByID(ctx, info.CameraID); ok {
			info.FPS = int(rs.ActualFPS)
			info.State = "streaming"

			for _, st := range rs.Streams {
				if st.Width > 0 {
					info.Codec = st.CodecType.String()
					info.Resolution = fmt.Sprintf("%dx%d", st.Width, st.Height)

					break
				}
			}
		}

		result = append(result, &info)
	}

	return result
}

// HasPlayback returns whether this relay has recording access for playback.
func (s *Service) HasPlayback() bool {
	return s.recIndex != nil
}

// ResolvePlaybackStart determines the actual start time and mode for a
// playback request. It handles three cases:
//   - Normal: recordings exist in the requested range → mode "recorded".
//   - First-available: no recordings in range but earlier ones exist → mode
//     "first_available" with resolvedFrom set to the earliest segment start.
//   - Live: the requested start is beyond the latest recording → mode "live".
//
// The caller should use the returned mode to decide whether to create a
// recorded playback relay or attach to the live hub.
func (s *Service) ResolvePlaybackStart(
	ctx context.Context, channelID string, from, to time.Time,
) (resolvedFrom time.Time, mode string, err error) {
	if s.recIndex == nil {
		return time.Time{}, "", errNoRecordingIndex
	}

	// Check latest recording to detect future-seek.
	last, lerr := s.recIndex.LastAvailable(ctx, channelID)
	if lerr != nil {
		// No recordings at all for this channel.
		if from.After(time.Now()) {
			return time.Time{}, PlaybackModeLive, nil
		}

		return time.Time{}, "", errNoRecordingsFound
	}

	if from.After(last.EndTime) {
		return time.Time{}, PlaybackModeLive, nil
	}

	// Normal range query.
	entries, qerr := s.recIndex.QueryByChannel(ctx, channelID, from, to)
	if qerr != nil {
		return time.Time{}, "", qerr
	}

	if len(entries) > 0 {
		// If from falls in a gap before the first returned segment, snap
		// to the segment's start so the client knows the actual playback
		// position.
		actual := from
		if from.Before(entries[0].StartTime) {
			actual = entries[0].StartTime
		}

		return actual, PlaybackModeRecorded, nil
	}

	// No recordings in the requested range — fall back to first available.
	first, ferr := s.recIndex.FirstAvailable(ctx, channelID)
	if ferr != nil {
		return time.Time{}, "", errNoRecordingsFound
	}

	return first.StartTime, PlaybackModeFirstAvailable, nil
}

// Timeline returns the recording timeline for a camera.
func (s *Service) Timeline(
	ctx context.Context,
	cameraID string,
	start, end time.Time,
) ([]recorder.RecordingEntry, error) {
	if s.recIndex == nil {
		return nil, errNoRecordingIndex
	}

	entries, err := s.recIndex.QueryByChannel(ctx, cameraID, start, end)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// ViewerCount returns the number of active live consumers.
func (s *Service) ViewerCount() int {
	s.consumerMu.RLock()
	defer s.consumerMu.RUnlock()

	return s.activeConsumers
}

// TrackConsumer increments the active consumer count. Returns a function
// that decrements it when called (for use with defer).
func (s *Service) TrackConsumer() func() {
	s.consumerMu.Lock()
	s.activeConsumers++
	s.consumerMu.Unlock()

	return func() {
		s.consumerMu.Lock()
		s.activeConsumers--
		s.consumerMu.Unlock()
	}
}

// RecordedDemuxerFactory returns a DemuxerFactory that plays all fMP4 segments
// matching the given channel and time range. When to is zero, it enters follow
// mode and polls the index for new segments. In follow mode, if no segments
// exist, it falls back to the live packet buffer for near-live replay.
func (s *Service) RecordedDemuxerFactory(channelID string, from, to time.Time) av.DemuxerFactory {
	follow := to.IsZero()

	return func(ctx context.Context, _ string) (av.DemuxCloser, error) {
		if s.recIndex == nil {
			return nil, errNoRecordingIndex
		}

		entries, err := s.recIndex.QueryByChannel(ctx, channelID, from, to)
		if err != nil {
			return nil, err
		}

		first, startIdx, err := openFirstSegment(entries, false)
		if err != nil {
			return nil, err
		}

		if first == nil {
			if dmx := s.liveFallback(channelID, from, follow); dmx != nil {
				return dmx, nil
			}

			return nil, errNoRecordingsFound
		}

		seenIDs := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			seenIDs[e.ID] = struct{}{}
		}

		src := &indexSource{
			entries:   entries,
			idx:       startIdx,
			seenIDs:   seenIDs,
			follow:    follow,
			recIndex:  s.recIndex,
			channelID: channelID,
			bufProv:   s.bufProv,
			collector: s.collector,
		}

		return chain.NewChainingDemuxer(first, src), nil
	}
}

// AnalyticsRecordedDemuxerFactory returns a DemuxerFactory like
// RecordedDemuxerFactory but with analytics enrichment: each segment's demuxer
// is wrapped with WallClockStampingDemuxer (so packets gain a wall-clock time),
// and the entire chain is wrapped with MetadataMerger using a CompositeSource
// (PersistenceSource for historical data, live AnalyticsStore for the near-live
// tail transition).
func (s *Service) AnalyticsRecordedDemuxerFactory(
	channelID string,
	from, to time.Time,
	reader *persistence.Reader,
	liveSource pva.Source,
) av.DemuxerFactory {
	follow := to.IsZero()

	return func(ctx context.Context, _ string) (av.DemuxCloser, error) {
		if s.recIndex == nil {
			return nil, errNoRecordingIndex
		}

		entries, err := s.recIndex.QueryByChannel(ctx, channelID, from, to)
		if err != nil {
			return nil, err
		}

		first, startIdx, err := openFirstSegment(entries, true)
		if err != nil {
			return nil, err
		}

		if first == nil {
			if dmx := s.liveFallback(channelID, from, follow); dmx != nil {
				return wrapWithAnalyticsMerger(dmx, reader, channelID, liveSource), nil
			}

			return nil, errNoRecordingsFound
		}

		seenIDs := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			seenIDs[e.ID] = struct{}{}
		}

		src := &indexSource{
			entries:        entries,
			idx:            startIdx,
			seenIDs:        seenIDs,
			follow:         follow,
			recIndex:       s.recIndex,
			channelID:      channelID,
			bufProv:        s.bufProv,
			collector:      s.collector,
			wallClockStamp: true,
		}

		dmx := chain.NewChainingDemuxer(first, src)

		return wrapWithAnalyticsMerger(dmx, reader, channelID, liveSource), nil
	}
}

// wrapWithAnalyticsMerger wraps a demuxer with MetadataMerger using a
// CompositeSource (persistence primary, live fallback).
func wrapWithAnalyticsMerger(
	dmx av.DemuxCloser,
	reader *persistence.Reader,
	channelID string,
	liveSource pva.Source,
) av.DemuxCloser {
	persistSource := pva.NewPersistenceSource(reader, channelID)

	source := pva.Source(persistSource)
	if liveSource != nil {
		source = &pva.CompositeSource{Primary: persistSource, Fallback: liveSource}
	}

	return pva.NewMetadataMerger(dmx, source)
}

// liveFallback returns a buffer demuxer for near-live replay when no disk
// segments are available. Returns nil if no buffer is available.
func (s *Service) liveFallback(channelID string, from time.Time, follow bool) av.DemuxCloser {
	if !follow || s.bufProv == nil {
		return nil
	}

	if buf := s.bufProv.PacketBuffer(channelID); buf != nil {
		return buf.Demuxer(from)
	}

	return nil
}

// openFirstSegment finds the first openable segment in entries, skipping those
// deleted by retention. Returns the demuxer, the next index, and any error.
// Returns (nil, 0, nil) when no entries exist or all were deleted.
// When wallClockStamp is true, wraps the demuxer with WallClockStampingDemuxer.
func openFirstSegment(
	entries []recorder.RecordingEntry,
	wallClockStamp bool,
) (av.DemuxCloser, int, error) {
	for i, e := range entries {
		dmx, err := openFMP4File(e.FilePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, 0, err
		}

		if wallClockStamp && !e.StartTime.IsZero() {
			dmx = pva.NewWallClockStampingDemuxer(dmx, e.StartTime)
		}

		return dmx, i + 1, nil
	}

	return nil, 0, nil
}

// indexSource implements chain.SegmentSource for the edge recording index.
// When a liveBuf is set, it transitions to the packet buffer after exhausting
// disk segments, providing seamless recorded-to-live playback.
type indexSource struct {
	entries   []recorder.RecordingEntry
	idx       int
	seenIDs   map[string]struct{}
	follow    bool
	recIndex  recorder.RecordingIndex
	channelID string
	liveBuf   *packetbuf.Buffer // near-live tail; nil if not available
	bufProv   PacketBufferProvider
	collector *metrics.Collector // nil if metrics not configured
	usedBuf   bool               // true once we've returned the buffer demuxer

	// wallClockStamp wraps each segment's demuxer with a WallClockStampingDemuxer
	// so that recorded packets gain a WallClockTime. Required for analytics-
	// enriched recorded playback (the PersistenceSource needs wall-clock to match).
	wallClockStamp bool

	// Gap detection: track the end time of the last segment so the
	// ChainingDemuxer can set IsDiscontinuity on the first packet when
	// a wall-clock gap > gapThreshold is detected.
	lastSegEnd time.Time
	lastGap    time.Duration
}

const (
	pollInterval = 1 * time.Second
	// gapThreshold is the minimum wall-clock gap between consecutive segments
	// that triggers a discontinuity marker on the first packet.
	gapThreshold = 5 * time.Second
)

// Compile-time interface checks.
var (
	_ chain.GapDetector           = (*indexSource)(nil)
	_ chain.SeekableSegmentSource = (*indexSource)(nil)
)

// LastGap returns the wall-clock gap detected at the last segment transition.
// Returns zero when there is no significant gap. Implements chain.GapDetector.
func (s *indexSource) LastGap() time.Duration { return s.lastGap }

// OpenAt implements chain.SeekableSegmentSource. It finds and opens the segment
// containing the given wall-clock timestamp, and resets the iteration cursor so
// that subsequent Next calls continue from the segment after it.
// Returns io.EOF if no segment covers the timestamp.
func (s *indexSource) OpenAt(ctx context.Context, ts time.Time) (av.DemuxCloser, error) {
	// Query for segments that overlap ts and onward. The SQL uses:
	//   end_time >= ts (from bound) — ordered by start_time ASC.
	// This finds the segment containing ts, or the next segment after a gap.
	entries, err := s.recIndex.QueryByChannel(ctx, s.channelID, ts, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("openat query: %w", err)
	}

	if len(entries) == 0 {
		return nil, io.EOF
	}

	// Try to open the first available segment, skipping those deleted by retention.
	for i, entry := range entries {
		dmx, oerr := openFMP4File(entry.FilePath)
		if oerr != nil {
			if os.IsNotExist(oerr) {
				continue
			}

			return nil, oerr
		}

		// Reset the iteration cursor so Next() continues from after this segment.
		seenIDs := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			seenIDs[e.ID] = struct{}{}
		}

		// Also preserve any IDs already seen before the seek.
		for id := range s.seenIDs {
			seenIDs[id] = struct{}{}
		}

		s.entries = entries
		s.idx = i + 1
		s.seenIDs = seenIDs

		// Update gap tracking.
		s.lastGap = 0

		if !entry.EndTime.IsZero() {
			s.lastSegEnd = entry.EndTime
		}

		if s.wallClockStamp && !entry.StartTime.IsZero() {
			dmx = pva.NewWallClockStampingDemuxer(dmx, entry.StartTime)
		}

		return dmx, nil
	}

	return nil, io.EOF
}

func (s *indexSource) Next(ctx context.Context) (av.DemuxCloser, error) {
	for s.idx < len(s.entries) {
		entry := s.entries[s.idx]
		s.idx++

		dmx, err := s.openEntry(entry)
		if err != nil {
			if os.IsNotExist(err) {
				continue // segment deleted by retention — skip
			}

			return nil, err
		}

		return dmx, nil
	}

	if !s.follow {
		return nil, io.EOF
	}

	// Try to get new disk segments first.
	dmx, err := s.waitForNextWithTimeout(ctx)
	if dmx != nil || err != nil {
		return dmx, err
	}

	// No new disk segments — transition to the packet buffer for near-live.
	// Resolve lazily so that a camera that started recording after the
	// factory was created is picked up.
	if dmx := s.tryLiveTransition(); dmx != nil {
		return dmx, nil
	}

	// No buffer available — keep polling disk.
	return s.waitForNext(ctx)
}

// tryLiveTransition attempts to transition from disk segments to the near-live
// packet buffer. Returns the live demuxer, or nil if no transition occurred.
func (s *indexSource) tryLiveTransition() av.DemuxCloser {
	if s.usedBuf || s.bufProv == nil {
		return nil
	}

	buf := s.bufProv.PacketBuffer(s.channelID)
	if buf == nil {
		return nil
	}

	transStart := time.Now()
	s.liveBuf = buf
	s.usedBuf = true

	since := time.Now().Add(-30 * time.Second) // overlap generously
	if len(s.entries) > 0 {
		since = s.entries[len(s.entries)-1].EndTime
	}

	if s.collector != nil {
		s.collector.RecordRecToLiveTransition(time.Since(transStart), s.channelID)
	}

	return s.liveBuf.Demuxer(since)
}

// openEntry opens a segment and updates gap tracking. Returns the demuxer or
// an error. Returns os.IsNotExist-matchable error if the file was deleted.
func (s *indexSource) openEntry(entry recorder.RecordingEntry) (av.DemuxCloser, error) {
	openStart := time.Now()

	dmx, err := openFMP4File(entry.FilePath)
	if err != nil {
		return nil, err
	}

	if s.collector != nil {
		s.collector.RecordSegmentOpen(time.Since(openStart), entry.FilePath)
	}

	// Detect wall-clock gap between this segment and the previous one.
	s.lastGap = 0
	if !s.lastSegEnd.IsZero() && !entry.StartTime.IsZero() {
		gap := entry.StartTime.Sub(s.lastSegEnd)
		if gap >= gapThreshold {
			s.lastGap = gap

			if s.collector != nil {
				s.collector.RecordFragmentGap(gap, s.channelID)
			}
		}
	}

	if !entry.EndTime.IsZero() {
		s.lastSegEnd = entry.EndTime
	}

	if s.wallClockStamp && !entry.StartTime.IsZero() {
		dmx = pva.NewWallClockStampingDemuxer(dmx, entry.StartTime)
	}

	return dmx, nil
}

// waitForNextWithTimeout does one quick poll for new segments and returns
// (nil, nil) if none are found, allowing the caller to fall back to the
// packet buffer.
func (s *indexSource) waitForNextWithTimeout(ctx context.Context) (av.DemuxCloser, error) {
	var afterTime time.Time
	if len(s.entries) > 0 {
		afterTime = s.entries[len(s.entries)-1].EndTime
	}

	newEntries, err := s.recIndex.QueryByChannel(ctx, s.channelID, afterTime, time.Time{})
	if err != nil {
		return nil, nil //nolint:nilerr,nilnil // transient; fall back to buffer
	}

	for _, e := range newEntries {
		if _, seen := s.seenIDs[e.ID]; !seen {
			s.seenIDs[e.ID] = struct{}{}
			s.entries = append(s.entries, e)
		}
	}

	for s.idx < len(s.entries) {
		entry := s.entries[s.idx]
		s.idx++

		dmx, err := s.openEntry(entry)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, err
		}

		return dmx, nil
	}

	return nil, nil //nolint:nilnil // signals "no new segments; try buffer fallback"
}

// waitForNext polls the index until at least one new segment is available.
func (s *indexSource) waitForNext(ctx context.Context) (av.DemuxCloser, error) {
	var afterTime time.Time
	if len(s.entries) > 0 {
		afterTime = s.entries[len(s.entries)-1].EndTime
	}

	timer := time.NewTimer(pollInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}

		newEntries, err := s.recIndex.QueryByChannel(ctx, s.channelID, afterTime, time.Time{})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			timer.Reset(pollInterval)

			continue
		}

		for _, e := range newEntries {
			if _, seen := s.seenIDs[e.ID]; !seen {
				s.seenIDs[e.ID] = struct{}{}
				s.entries = append(s.entries, e)
			}
		}

		for s.idx < len(s.entries) {
			entry := s.entries[s.idx]
			s.idx++

			dmx, err := s.openEntry(entry)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}

				return nil, err
			}

			return dmx, nil
		}

		timer.Reset(pollInterval)
	}
}

// openFMP4File opens a segment file and returns a DemuxCloser wrapping fmp4.Demuxer.
func openFMP4File(path string) (av.DemuxCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	return &fileDemuxer{Demuxer: fmp4.NewDemuxer(f), f: f}, nil
}

// fileDemuxer wraps an fmp4.Demuxer together with its backing *os.File so
// that Close() closes both.
type fileDemuxer struct {
	*fmp4.Demuxer

	f *os.File
}

func (d *fileDemuxer) Close() error {
	return errors.Join(d.Demuxer.Close(), d.f.Close())
}

// NewPlaybackHub creates a temporary relayhub for playback of recorded segments.
// The hub enforces a single-consumer limit to prevent leaky delivery mode.
// The caller must Start and defer Stop on the returned hub.
func NewPlaybackHub(factory av.DemuxerFactory) *relayhub.RelayHub {
	return relayhub.New(factory, nil, relayhub.WithMaxConsumers(1))
}
