package avf

import "github.com/vtpl1/vrtc/pkg/av"

type Filter interface {
	av.Demuxer
	FrameDemuxer
	av.Muxer
	FrameMuxer
	Closer
}
