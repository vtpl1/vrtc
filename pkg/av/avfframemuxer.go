package av

import (
	"context"
)

type AVFFrameDemuxer interface {
	ReadFrame(ctx context.Context) (AVFFrame, error)
}

type AVFFrameDemuxCloser interface {
	AVFFrameDemuxer
	Closer
}
