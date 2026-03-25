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
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
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
		f, err := os.Open(entries[0].FilePath)
		if err != nil {
			return nil, err
		}

		seenIDs := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			seenIDs[e.ID] = struct{}{}
		}

		return &chainingDemuxer{
			entries:   entries,
			cur:       &fileDemuxer{Demuxer: fmp4.NewDemuxer(f), f: f},
			seenIDs:   seenIDs,
			follow:    follow,
			index:     r.index,
			channelID: req.ChannelID,
		}, nil
	}
}

// chainingDemuxer plays a sequence of fMP4 files one after another.
// DTS values are adjusted at each segment boundary so they are monotonically
// increasing. In follow mode it polls the index for new segments after the
// initial list is exhausted, blocking until new recordings arrive or ctx ends.
type chainingDemuxer struct {
	entries   []recorder.RecordingEntry
	idx       int           // index of the entry currently open in cur
	cur       *fileDemuxer  // currently open file demuxer
	dtsOff    time.Duration // cumulative DTS offset applied to packets from cur
	lastEnd   time.Duration // DTS + Duration of the last packet emitted (after offset)
	seenIDs   map[string]struct{}
	follow    bool
	index     recorder.RecordingIndex
	channelID string
}

// GetCodecs reads the init segment of the first file and returns its streams.
func (c *chainingDemuxer) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	return c.cur.GetCodecs(ctx)
}

// ReadPacket returns the next packet across all chained segments.
// When one file reaches io.EOF the next file is opened transparently. In follow
// mode, when all known files are exhausted, it polls the index for new
// completed segments until one appears or ctx is cancelled.
func (c *chainingDemuxer) ReadPacket(ctx context.Context) (av.Packet, error) {
	for {
		pkt, err := c.cur.ReadPacket(ctx)
		if err == nil {
			pkt.DTS += c.dtsOff
			if end := pkt.DTS + pkt.Duration; end > c.lastEnd {
				c.lastEnd = end
			}

			return pkt, nil
		}

		if !errors.Is(err, io.EOF) {
			return av.Packet{}, err
		}

		// Current segment exhausted — advance to the next known entry.
		_ = c.cur.Close()
		c.cur = nil
		c.idx++

		if c.idx < len(c.entries) {
			if err := c.openIdx(ctx); err != nil {
				return av.Packet{}, err
			}

			continue
		}

		// All known entries played. In follow mode, poll for new ones.
		if !c.follow {
			return av.Packet{}, io.EOF
		}

		if err := c.waitForNext(ctx); err != nil {
			return av.Packet{}, err
		}
	}
}

// Close closes the currently open file demuxer.
func (c *chainingDemuxer) Close() error {
	if c.cur != nil {
		return c.cur.Close()
	}

	return nil
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

// openIdx opens entries[idx], calls GetCodecs to skip the init segment, and
// updates dtsOff so DTS continues from where the previous segment ended.
func (c *chainingDemuxer) openIdx(ctx context.Context) error {
	f, err := os.Open(c.entries[c.idx].FilePath)
	if err != nil {
		return err
	}

	c.cur = &fileDemuxer{Demuxer: fmp4.NewDemuxer(f), f: f}

	if _, err = c.cur.GetCodecs(ctx); err != nil {
		return err
	}

	c.dtsOff = c.lastEnd

	return nil
}

// waitForNext polls the index until at least one new completed segment is
// available, then appends it to entries and opens it. Returns ctx.Err() if
// the context is cancelled before a new segment appears.
func (c *chainingDemuxer) waitForNext(ctx context.Context) error {
	// Use the last known entry's EndTime as the lower bound so we only
	// retrieve segments newer than what we have already played.
	var afterTime time.Time
	if len(c.entries) > 0 {
		afterTime = c.entries[len(c.entries)-1].EndTime
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}

		newEntries, err := c.index.QueryByChannel(ctx, c.channelID, afterTime, time.Time{})
		if err != nil {
			log.Warn().Err(err).Str("channel", c.channelID).Msg("playback: poll index")

			continue
		}

		for _, e := range newEntries {
			if _, seen := c.seenIDs[e.ID]; !seen {
				c.seenIDs[e.ID] = struct{}{}
				c.entries = append(c.entries, e)
			}
		}

		if c.idx < len(c.entries) {
			return c.openIdx(ctx)
		}
	}
}
