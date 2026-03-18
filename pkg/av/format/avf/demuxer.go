package avf

import (
	"context"
	"io"
	"os"

	"github.com/vtpl1/vrtc/pkg/av"
)

type Option func(*Demuxer)

type Demuxer struct{}

// Close implements [av.DemuxCloser].
func (d *Demuxer) Close() error {
	panic("unimplemented")
}

// GetCodecs implements [av.DemuxCloser].
func (d *Demuxer) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	panic("unimplemented")
}

// ReadPacket implements [av.DemuxCloser].
func (d *Demuxer) ReadPacket(ctx context.Context) (av.Packet, error) {
	panic("unimplemented")
}

func New(r io.Reader, opts ...Option) *Demuxer {
	return &Demuxer{}
}

// Open opens the named AVF file and returns a ready Demuxer.
func Open(path string, opts ...Option) (*Demuxer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	return New(f, opts...), nil
}
