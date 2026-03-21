package avf

import "context"

type FrameDemuxer interface {
	ReadFrame(ctx context.Context) (Frame, error)
}

type FrameDemuxCloser interface {
	FrameDemuxer
	Closer
}
