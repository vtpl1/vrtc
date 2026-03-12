package av

import (
	"context"
)

type AVFFrameMuxer interface {
	WriteFrame(ctx context.Context, frm AVFFrame) error
}

type AVFFrameMuxCloser interface {
	AVFFrameMuxer
	Closer
}
