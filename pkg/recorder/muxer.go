package recorder

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
)

// fmp4FileMuxer wraps an fmp4.Muxer writing to an os.File.
// It satisfies av.MuxCloser and invokes an onClose callback after the file
// is finalised so that RecordingManager can insert the completed entry into
// the RecordingIndex without the muxer needing to know about the index.
type fmp4FileMuxer struct {
	inner     *fmp4.Muxer
	path      string
	startTime time.Time
	onClose   func(path string, start, end time.Time, sizeBytes int64)
}

// newFMP4FileMuxer creates the output file at path, constructs an fmp4.Muxer
// writing to it, and returns the wrapper.  onClose is called from Close()
// after the file has been fully written and closed.
func newFMP4FileMuxer(
	path string,
	startTime time.Time,
	onClose func(string, time.Time, time.Time, int64),
) (av.MuxCloser, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("recorder: create segment file %q: %w", path, err)
	}

	return &fmp4FileMuxer{
		inner:     fmp4.NewMuxer(f),
		path:      path,
		startTime: startTime,
		onClose:   onClose,
	}, nil
}

func (m *fmp4FileMuxer) WriteHeader(ctx context.Context, streams []av.Stream) error {
	return m.inner.WriteHeader(ctx, streams)
}

func (m *fmp4FileMuxer) WritePacket(ctx context.Context, pkt av.Packet) error {
	return m.inner.WritePacket(ctx, pkt)
}

func (m *fmp4FileMuxer) WriteTrailer(ctx context.Context, upstreamErr error) error {
	return m.inner.WriteTrailer(ctx, upstreamErr)
}

// Close flushes any remaining fragment data and closes the underlying file.
// It then reads the final file size and calls the onClose callback.
// The first error encountered is returned; the callback is always invoked.
func (m *fmp4FileMuxer) Close() error {
	endTime := time.Now().UTC()

	// inner.Close() calls WriteTrailer (if not yet called) then closes the file.
	err := m.inner.Close()

	var sizeBytes int64

	if fi, statErr := os.Stat(m.path); statErr == nil {
		sizeBytes = fi.Size()
	}

	if m.onClose != nil {
		m.onClose(m.path, m.startTime, endTime, sizeBytes)
	}

	return err
}
