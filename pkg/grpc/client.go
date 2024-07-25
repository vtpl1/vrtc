package grpc

import "github.com/vtpl1/vrtc/pkg/core"

func NewClient(uri string) *Conn {
	return &Conn{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "grpc",
		},
		uri: uri,
	}
}
