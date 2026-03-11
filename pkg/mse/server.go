// Package mse implements an av.MuxCloser that multiplexes fMP4 fragments to
// WebSocket clients for consumption by the browser's Media Source Extensions API.
//
// Protocol (per connection):
//
//  1. Client sends:  {"type":"mse","value":""}
//  2. Server sends:  {"type":"mse","value":"video/mp4; codecs=\"hvc1.1.6.L153.B0,flac\""} (text)
//  3. Server sends:  fMP4 init segment (binary)
//  4. Server sends:  fMP4 media fragments (binary) as they are produced
//     For each packet with non-nil Extra, server also sends {"type":"mse","value":<extra>} (text)
package mse

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
)

// wsMessage is the JSON envelope used for all text-channel messages.
type wsMessage struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// outFrame is a single WebSocket frame queued for delivery to a client.
type outFrame struct {
	kind websocket.MessageType // websocket.TextMessage or websocket.BinaryMessage
	data []byte
}

// Server is a WebSocket server and an av.MuxCloser.
//
// Call New (or mount ServeHTTP on an existing mux) and then drive it exactly
// like any other av.MuxCloser: WriteHeader → WritePacket* → WriteTrailer → Close.
type Server struct {
	// mu serialises fmp4.Muxer writes and the shared buf.
	mu      sync.Mutex
	buf     bytes.Buffer
	mux     *fmp4.Muxer
	streams []av.Stream // current codec state

	// codecsReady is closed exactly once when WriteHeader succeeds.
	// Clients block on it until codec info is available.
	codecsReady chan struct{}

	// codecStr and initSeg are set by WriteHeader and updated by WriteCodecChange.
	codecsMu sync.RWMutex
	codecStr string
	initSeg  []byte

	closed    chan struct{}
	closeOnce sync.Once
}

// New creates a Server that starts listening on addr.
// Clients connect to ws://addr/ and follow the MSE streaming protocol.
func New() (*Server, error) {
	s := newServer()

	return s, nil
}

func newServer() *Server {
	s := &Server{
		codecsReady: make(chan struct{}),
		closed:      make(chan struct{}),
	}
	s.mux = fmp4.NewMuxer(&s.buf)

	return s
}

// ── av.MuxCloser ───────────────────────────────────────────────────────────────

// WriteHeader declares all streams, writes the fMP4 init segment, and unblocks
// any WebSocket clients that are waiting for codec information.
func (s *Server) WriteHeader(ctx context.Context, streams []av.Stream) error {
	s.mu.Lock()
	s.streams = cloneStreams(streams)
	s.buf.Reset()
	err := s.mux.WriteHeader(ctx, streams)
	data := cloneBytes(s.buf.Bytes())
	s.mu.Unlock()

	s.codecsMu.Lock()
	s.codecStr = buildCodecString(streams)
	s.initSeg = data
	s.codecsMu.Unlock()

	// Unblock waiting clients (idempotent — select prevents double-close).
	select {
	case <-s.codecsReady: // already closed
	default:
		close(s.codecsReady)
	}

	if len(data) > 0 {
		s.broadcast(outFrame{websocket.MessageBinary, data})
	}

	return err
}

// WritePacket buffers a sample; flushes and broadcasts a binary fMP4 fragment on
// each video keyframe (or immediately for audio-only streams). If pkt.Extra is
// non-nil it is marshalled to JSON and broadcast as a text message.
func (s *Server) WritePacket(ctx context.Context, pkt av.Packet) error {
	s.mu.Lock()
	s.buf.Reset()
	err := s.mux.WritePacket(ctx, pkt)
	data := cloneBytes(s.buf.Bytes())
	s.mu.Unlock()

	// Per-frame metadata: send before the binary so the client can prepare.
	if pkt.Extra != nil {
		if meta, jerr := json.Marshal(wsMessage{Type: "mse", Value: pkt.Extra}); jerr == nil {
			s.broadcast(outFrame{websocket.MessageText, meta})
		}
	}

	if len(data) > 0 {
		s.broadcast(outFrame{websocket.MessageBinary, data})
	}

	return err
}

// WriteTrailer flushes any buffered samples and broadcasts the final fragment.
func (s *Server) WriteTrailer(ctx context.Context, upstreamErr error) error {
	s.mu.Lock()
	s.buf.Reset()
	err := s.mux.WriteTrailer(ctx, upstreamErr)
	data := cloneBytes(s.buf.Bytes())
	s.mu.Unlock()

	if len(data) > 0 {
		s.broadcast(outFrame{websocket.MessageBinary, data})
	}

	return err
}

// WriteCodecChange implements av.CodecChanger. It flushes the current fragment,
// broadcasts the codec-change data to existing clients, and stores a fresh init
// segment and updated codec string for clients that connect after the change.
func (s *Server) WriteCodecChange(ctx context.Context, changed []av.Stream) error {
	s.mu.Lock()

	for _, c := range changed {
		for i, existing := range s.streams {
			if existing.Idx == c.Idx {
				s.streams[i] = c

				break
			}
		}
	}

	updatedStreams := cloneStreams(s.streams)

	s.buf.Reset()
	err := s.mux.WriteCodecChange(ctx, changed)
	data := cloneBytes(s.buf.Bytes())
	s.mu.Unlock()

	// Rebuild a clean init-only segment for late joiners (no fragment prefix).
	var initBuf bytes.Buffer

	detachedCtx := context.WithoutCancel(ctx)

	timeOutCtx, cancel := context.WithTimeout(detachedCtx, 5*time.Second)
	defer cancel()

	tmp := fmp4.NewMuxer(&initBuf)
	if herr := tmp.WriteHeader(timeOutCtx, updatedStreams); herr == nil {
		s.codecsMu.Lock()
		s.codecStr = buildCodecString(updatedStreams)
		s.initSeg = cloneBytes(initBuf.Bytes())
		s.codecsMu.Unlock()
	}

	if len(data) > 0 {
		s.broadcast(outFrame{websocket.MessageBinary, data})
	}

	return err
}

// Close flushes remaining samples, shuts down the HTTP server, and closes all
// active WebSocket connections.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		_ = s.WriteTrailer(context.Background(), nil)
		close(s.closed)
	})

	return nil
}

// buildCodecString builds a MIME type + codecs string from the stream list.
// Codecs with a Tag() method (H.264, H.265, AAC) are used directly; FLAC is
// added as "flac".
func buildCodecString(streams []av.Stream) string {
	type tagger interface{ Tag() string }

	var parts []string

	for _, s := range streams {
		switch c := s.Codec.(type) {
		case tagger:
			parts = append(parts, c.Tag())
		case pcm.FLACCodecData:
			_ = c

			parts = append(parts, "flac")
		}
	}

	if len(parts) == 0 {
		return `video/mp4; codecs=""`
	}

	return `video/mp4; codecs="` + strings.Join(parts, ",") + `"`
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}

	c := make([]byte, len(b))
	copy(c, b)

	return c
}

func cloneStreams(ss []av.Stream) []av.Stream {
	c := make([]av.Stream, len(ss))
	copy(c, ss)

	return c
}

func (s *Server) broadcast(_ outFrame) {
	panic("unimplemented")
}
