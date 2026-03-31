// Package playback routes a client request to either a live stream or a
// recorded segment, depending on whether the time range is specified.
package playback

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc-sdk/av"
	"github.com/vtpl1/vrtc-sdk/av/chain"
	"github.com/vtpl1/vrtc-sdk/av/format/fmp4"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

const pollInterval = 1 * time.Second

// Request describes what the client wants to play.
type Request struct {
	ChannelID string
	From      time.Time // zero = no lower bound / live
	To        time.Time // zero = no upper bound / live
}

// IsLive reports whether req is a live-stream request (both From and To are zero).
func IsLive(req Request) bool {
	return req.From.IsZero() && req.To.IsZero()
}

// Router resolves playback requests against the recording index.
type Router struct {
	index recorder.RecordingIndex
}

// New returns a Router backed by the given RecordingIndex.
func New(index recorder.RecordingIndex) *Router {
	return &Router{index: index}
}

// RecordedDemuxerFactory returns a DemuxerFactory that plays all fMP4 segments
// matching req sequentially. When no time range is given (follow mode) it
// continues polling the index for newly completed segments instead of stopping
// at the end of the initial snapshot.
func (r *Router) RecordedDemuxerFactory(req Request) av.DemuxerFactory {
	// follow = play all recordings indefinitely (no upper time bound)
	follow := req.To.IsZero()

	return func(ctx context.Context, _ string) (av.DemuxCloser, error) {
		entries, err := r.index.QueryByChannel(ctx, req.ChannelID, req.From, req.To)
		if err != nil {
			return nil, err
		}

		if len(entries) == 0 {
			return nil, ErrNoRecordingsFound
		}

		// Open the first file eagerly to fail fast on missing/unreadable files.
		first, err := openFMP4File(entries[0].FilePath)
		if err != nil {
			return nil, err
		}

		seenIDs := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			seenIDs[e.ID] = struct{}{}
		}

		src := &indexSource{
			entries:   entries,
			idx:       1, // first entry already opened above
			seenIDs:   seenIDs,
			follow:    follow,
			index:     r.index,
			channelID: req.ChannelID,
		}

		return chain.NewChainingDemuxer(first, src), nil
	}
}

// indexSource implements chain.SegmentSource for the recording index.
// It iterates through a known list of entries, and in follow mode,
// polls the index for new segments when the list is exhausted.
type indexSource struct {
	entries   []recorder.RecordingEntry
	idx       int
	seenIDs   map[string]struct{}
	follow    bool
	index     recorder.RecordingIndex
	channelID string
}

func (s *indexSource) Next(ctx context.Context) (av.DemuxCloser, error) {
	if s.idx < len(s.entries) {
		entry := s.entries[s.idx]
		s.idx++

		return openFMP4File(entry.FilePath)
	}

	if !s.follow {
		return nil, io.EOF
	}

	return s.waitForNext(ctx)
}

// waitForNext polls the index until at least one new completed segment is
// available, then opens and returns it.
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

		newEntries, err := s.index.QueryByChannel(ctx, s.channelID, afterTime, time.Time{})
		if err != nil {
			log.Warn().Err(err).Str("channel", s.channelID).Msg("playback: poll index")

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

// openFMP4File opens a file and returns a DemuxCloser wrapping fmp4.Demuxer.
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
