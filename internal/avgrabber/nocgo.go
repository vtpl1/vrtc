//go:build !cgo

// Package avgrabber provides RTSP demuxing via the AVGrabber C library.
// When CGO is disabled (e.g. cross-compilation), all operations return
// ErrCGORequired and no real RTSP session is opened.
package avgrabber

import (
	"context"
	"errors"

	"github.com/vtpl1/vrtc/pkg/av"
)

// ErrCGORequired is returned by all avgrabber operations when the binary was
// built without CGO (CGO_ENABLED=0). RTSP streaming is unavailable.
var ErrCGORequired = errors.New("avgrabber: CGO is required for RTSP support")

// Init is a no-op when CGO is disabled.
func Init() {}

// Deinit is a no-op when CGO is disabled.
func Deinit() {}

// Version returns zeros when CGO is disabled.
func Version() (major, minor, patch int) { return 0, 0, 0 }

// Demuxer is a stub that satisfies av.DemuxCloser and av.Pauser.
type Demuxer struct{}

// NewDemuxer always returns ErrCGORequired when CGO is disabled.
func NewDemuxer(_ Config) (*Demuxer, error) { return nil, ErrCGORequired }

func (d *Demuxer) GetCodecs(_ context.Context) ([]av.Stream, error) {
	return nil, ErrCGORequired
}

func (d *Demuxer) ReadPacket(_ context.Context) (av.Packet, error) {
	return av.Packet{}, ErrCGORequired
}

func (d *Demuxer) Pause(_ context.Context) error  { return ErrCGORequired }
func (d *Demuxer) Resume(_ context.Context) error { return ErrCGORequired }
func (d *Demuxer) IsPaused() bool                 { return false }
func (d *Demuxer) Close() error                   { return nil }
