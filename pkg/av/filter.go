package av

type Filter interface {
	Demuxer
	AVFFrameDemuxer
	Muxer
	AVFFrameMuxer
	Closer
}
