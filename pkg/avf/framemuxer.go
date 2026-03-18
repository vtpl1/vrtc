package avf

import "context"

type FrameMuxer interface {
	WriteFrame(ctx context.Context, frm Frame) error
}

type FrameMuxCloser interface {
	FrameMuxer
	Closer
}
