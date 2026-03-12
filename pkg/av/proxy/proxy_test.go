package proxy_test

import (
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/proxy"
)

var (
	_ av.DemuxCloser         = (*proxy.ProxyMuxDemuxCloser)(nil)
	_ av.MuxCloser           = (*proxy.ProxyMuxDemuxCloser)(nil)
	_ av.AVFFrameDemuxCloser = (*proxy.ProxyMuxDemuxCloser)(nil)
	_ av.AVFFrameMuxCloser   = (*proxy.ProxyMuxDemuxCloser)(nil)
)
