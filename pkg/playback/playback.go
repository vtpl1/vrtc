// Package playback routes a client request to either a live stream or a
// recorded segment, depending on whether the time range is specified.
package playback

import (
	"context"
	"errors"
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

// RecordedDemuxerFactory returns a DemuxerFactory that opens the first fMP4
// segment matching req. The producerID argument passed by the StreamManager is
// ignored; ChannelID from the request is used instead.
//
// The returned factory calls index.QueryByChannel, picks the first entry, and
// returns an fmp4.Demuxer reading from the segment file. The file is closed
// when the demuxer is closed.
func (r *Router) RecordedDemuxerFactory(req Request) av.DemuxerFactory {
	return func(ctx context.Context, _ string) (av.DemuxCloser, error) {
		entries, err := r.index.QueryByChannel(ctx, req.ChannelID, req.From, req.To)
		if err != nil {
			return nil, err
		}

		if len(entries) == 0 {
			return nil, ErrNoRecordingsFound
		}

		f, err := os.Open(entries[0].FilePath)
		if err != nil {
			return nil, err
		}

		return &fileDemuxer{
			Demuxer: fmp4.NewDemuxer(f),
			f:       f,
		}, nil
	}
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
