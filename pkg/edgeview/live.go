package edgeview

import (
	"context"
	"errors"
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
	"github.com/vtpl1/vrtc/pkg/recorder"
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
	log      zerolog.Logger
	hub      av.RelayHub
	recIndex recorder.RecordingIndex // nil if no recording available
	bufProv  PacketBufferProvider    // nil if no recording available
	chanW    channel.ChannelWriter   // nil if no channel CRUD available
	cameras  map[string]*CameraInfo

	mu sync.RWMutex

	// activeConsumers tracks live consumer handles for viewer counting.
	consumerMu      sync.RWMutex
	activeConsumers int
}

// CameraInfo describes a camera available on this relay.
type CameraInfo struct {
	CameraID   string `json:"camera_id"` //nolint:tagliatelle
	Name       string `json:"name"`
	Codec      string `json:"codec"`
	Resolution string `json:"resolution"`
	FPS        int    `json:"fps"`
	Recording  bool   `json:"recording"`
	Analytics  bool   `json:"analytics"`
	State      string `json:"state"`
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

// ServiceOption configures optional Service dependencies.
type ServiceOption func(*Service)

// WithChannelWriter enables channel CRUD endpoints.
func WithChannelWriter(cw channel.ChannelWriter) ServiceOption {
	return func(s *Service) { s.chanW = cw }
}

// Hub returns the underlying relay hub for direct consumer attachment.
func (s *Service) Hub() av.RelayHub {
	return s.hub
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

// ListCameras returns all cameras on this relay.
func (s *Service) ListCameras() []*CameraInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*CameraInfo, 0, len(s.cameras))
	for _, c := range s.cameras {
		info := *c
		result = append(result, &info)
	}

	return result
}

// HasPlayback returns whether this relay has recording access for playback.
func (s *Service) HasPlayback() bool {
	return s.recIndex != nil
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
// mode and polls the index for new segments.
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

		if len(entries) == 0 {
			return nil, errNoRecordingsFound
		}

		first, err := openFMP4File(entries[0].FilePath)
		if err != nil {
			return nil, err
		}

		seenIDs := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			seenIDs[e.ID] = struct{}{}
		}

		// Resolve the packet buffer for near-live tail.
		var buf *packetbuf.Buffer
		if follow && s.bufProv != nil {
			buf = s.bufProv.PacketBuffer(channelID)
		}

		src := &indexSource{
			entries:   entries,
			idx:       1, // first entry already opened above
			seenIDs:   seenIDs,
			follow:    follow,
			recIndex:  s.recIndex,
			channelID: channelID,
			liveBuf:   buf,
		}

		return chain.NewChainingDemuxer(first, src), nil
	}
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
	usedBuf   bool              // true once we've returned the buffer demuxer
}

const pollInterval = 1 * time.Second

func (s *indexSource) Next(ctx context.Context) (av.DemuxCloser, error) {
	if s.idx < len(s.entries) {
		entry := s.entries[s.idx]
		s.idx++

		return openFMP4File(entry.FilePath)
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
	if s.liveBuf != nil && !s.usedBuf {
		s.usedBuf = true

		since := time.Now().Add(-30 * time.Second) // overlap generously
		if len(s.entries) > 0 {
			since = s.entries[len(s.entries)-1].EndTime
		}

		return s.liveBuf.Demuxer(since), nil
	}

	// No buffer available — keep polling disk.
	return s.waitForNext(ctx)
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

	if s.idx < len(s.entries) {
		entry := s.entries[s.idx]
		s.idx++

		return openFMP4File(entry.FilePath)
	}

	return nil, nil //nolint:nilnil // signals "no new segments; try buffer fallback"
}

// waitForNext polls the index until at least one new segment is available.
func (s *indexSource) waitForNext(ctx context.Context) (av.DemuxCloser, error) {
	var afterTime time.Time
	if len(s.entries) > 0 {
		afterTime = s.entries[len(s.entries)-1].EndTime
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}

		newEntries, err := s.recIndex.QueryByChannel(ctx, s.channelID, afterTime, time.Time{})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			continue
		}

		for _, e := range newEntries {
			if _, seen := s.seenIDs[e.ID]; !seen {
				s.seenIDs[e.ID] = struct{}{}
				s.entries = append(s.entries, e)
			}
		}

		if s.idx < len(s.entries) {
			entry := s.entries[s.idx]
			s.idx++

			return openFMP4File(entry.FilePath)
		}
	}
}

// openFMP4File opens a segment file and returns a DemuxCloser wrapping fmp4.Demuxer.
func openFMP4File(path string) (av.DemuxCloser, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from trusted recording index
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
// The caller must Start and defer Stop on the returned hub.
func NewPlaybackHub(factory av.DemuxerFactory) *relayhub.RelayHub {
	return relayhub.New(factory, nil)
}
