package avf

import "context"

type AVFFrameDemuxer interface {
	ReadFrame(ctx context.Context) (Frame, error)
}

type AVFFrameDemuxCloser interface {
	AVFFrameDemuxer
	Closer
}
