package avf

import "github.com/vtpl1/vrtc/pkg/av"

type Filter interface {
	av.Demuxer
	AVFFrameDemuxer
	av.Muxer
	FrameMuxer
	Closer
}
