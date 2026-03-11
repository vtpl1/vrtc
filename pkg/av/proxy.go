package av

import (
	"context"
	"errors"
	"io"
	"sync"
)

var ErrProxyIsClosing = errors.New("proxy is closing")

// ProxyMuxDemuxCloser is an in-memory pipe that implements both MuxCloser and
// DemuxCloser. Packets written through the MuxCloser side are delivered to the
// DemuxCloser side via a shared channel.
//
// Typical use: one goroutine writes via the Mux methods; another reads via the
// Demux methods.
//
// Lifecycle:
//  1. WriteHeader → unblocks GetCodecs on the demux side.
//  2. WritePacket → enqueues packets; ReadPacket dequeues them.
//  3. WriteTrailer / Close → closes the packet channel so ReadPacket returns io.EOF.
//
// Optional capabilities (Pauser, TimeSeeker, CodecChanger) are NOT forwarded;
// embed ProxyMuxDemuxCloser and implement them explicitly when needed.
type ProxyMuxDemuxCloser struct {
	packetsCloseOnce sync.Once // guards close(packets)
	packets          chan Packet
	streams          []Stream
	ready            chan struct{} // closed once WriteHeader stores streams
	closedCloseOnce  sync.Once
	closed           chan struct{} // flag that proxy is closing

}

// NewProxyMuxDemuxCloser creates a ProxyMuxDemuxCloser with a packet channel
// of the given buffer size (0 for unbuffered).
func NewProxyMuxDemuxCloser(bufSize int) *ProxyMuxDemuxCloser {
	return &ProxyMuxDemuxCloser{
		packets: make(chan Packet, bufSize),
		ready:   make(chan struct{}),
	}
}

// --- DemuxCloser ---

// GetCodecs blocks until WriteHeader has been called (or ctx is cancelled),
// then returns the stream list provided to WriteHeader.
func (p *ProxyMuxDemuxCloser) GetCodecs(ctx context.Context) ([]Stream, error) {
	select {
	case <-p.ready:
		return p.streams, nil
	case <-p.closed:
		return nil, errors.Join(io.EOF, ErrProxyIsClosing)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ReadPacket returns the next packet from the shared channel.
// Returns io.EOF when the mux side calls WriteTrailer or Close.
func (p *ProxyMuxDemuxCloser) ReadPacket(ctx context.Context) (Packet, error) {
	select {
	case pkt, ok := <-p.packets:
		if !ok {
			return Packet{}, errors.Join(io.EOF, ErrProxyIsClosing)
		}
		return pkt, nil
	case <-ctx.Done():
		return Packet{}, ctx.Err()
	}
}

// --- MuxCloser ---

// WriteHeader stores the stream list and unblocks any GetCodecs call.
func (p *ProxyMuxDemuxCloser) WriteHeader(_ context.Context, streams []Stream) error {
	p.streams = streams
	close(p.ready)
	return nil
}

// WritePacket enqueues a packet for the demux side to read.
func (p *ProxyMuxDemuxCloser) WritePacket(ctx context.Context, pkt Packet) error {
	select {
	case p.packets <- pkt:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WriteTrailer closes the packet channel, causing ReadPacket to return io.EOF.
func (p *ProxyMuxDemuxCloser) WriteTrailer(_ context.Context, _ error) error {
	p.packetsCloseOnce.Do(func() { close(p.packets) })
	return nil
}

// Close closes the packet channel, causing ReadPacket to return io.EOF.
func (p *ProxyMuxDemuxCloser) Close() error {
	p.packetsCloseOnce.Do(func() {
		close(p.packets)
	})
	p.closedCloseOnce.Do(func() {
		close(p.closed)
	})
	return nil
}
