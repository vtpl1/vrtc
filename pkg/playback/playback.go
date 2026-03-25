// Package playback routes a client request to either a live stream or a
// recorded segment, depending on whether the time range is specified.
package playback

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
	"github.com/vtpl1/vrtc/pkg/recorder"
)

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

// RecordedDemuxerFactory returns a DemuxerFactory that opens all fMP4 segments
// matching req and plays them back sequentially. DTS is adjusted across segment
// boundaries so it remains monotonically increasing. The first file is opened
// eagerly so callers get an immediate error if it is missing; subsequent files
// are opened lazily as each segment finishes.
func (r *Router) RecordedDemuxerFactory(req Request) av.DemuxerFactory {
	return func(ctx context.Context, _ string) (av.DemuxCloser, error) {
		entries, err := r.index.QueryByChannel(ctx, req.ChannelID, req.From, req.To)
		if err != nil {
			return nil, err
		}

		if len(entries) == 0 {
			return nil, ErrNoRecordingsFound
		}

		// Open the first file eagerly so the caller gets an immediate error if it
		// is missing or unreadable, rather than discovering this later inside the
		// producer's read loop.
		f, err := os.Open(entries[0].FilePath)
		if err != nil {
			return nil, err
		}

		return &chainingDemuxer{
			entries: entries,
			cur:     &fileDemuxer{Demuxer: fmp4.NewDemuxer(f), f: f},
		}, nil
	}
}

// chainingDemuxer plays a sequence of fMP4 files one after another.
// DTS values are adjusted at each segment boundary so they are monotonically
// increasing across the whole playback session.
type chainingDemuxer struct {
	entries []recorder.RecordingEntry
	idx     int           // index of the entry currently open in cur
	cur     *fileDemuxer  // currently open file demuxer
	dtsOff  time.Duration // cumulative DTS offset applied to packets from cur
	lastEnd time.Duration // DTS + Duration of the last packet emitted (after offset)
}

// GetCodecs reads the init segment of the first file and returns its streams.
func (c *chainingDemuxer) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	return c.cur.GetCodecs(ctx)
}

// ReadPacket returns the next packet across all chained segments.
// When one file reaches io.EOF the next file is opened transparently and DTS
// is offset so it continues from where the previous file ended.
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

		// Current segment exhausted — advance to the next one.
		_ = c.cur.Close()
		c.cur = nil
		c.idx++

		if c.idx >= len(c.entries) {
			return av.Packet{}, io.EOF
		}

		f, ferr := os.Open(c.entries[c.idx].FilePath)
		if ferr != nil {
			return av.Packet{}, ferr
		}

		c.cur = &fileDemuxer{Demuxer: fmp4.NewDemuxer(f), f: f}

		if _, ferr = c.cur.GetCodecs(ctx); ferr != nil {
			return av.Packet{}, ferr
		}

		// Offset new file's timestamps so playback continues seamlessly.
		c.dtsOff = c.lastEnd
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
