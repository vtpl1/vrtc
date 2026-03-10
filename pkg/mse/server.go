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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
)

const (
	clientChanSize  = 32
	shutdownTimeout = 5 * time.Second
)

// wsMessage is the JSON envelope used for all text-channel messages.
type wsMessage struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// outFrame is a single WebSocket frame queued for delivery to a client.
type outFrame struct {
	kind int    // websocket.TextMessage or websocket.BinaryMessage
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

	// clients maps each active connection to its outbound channel.
	clientsMu sync.RWMutex
	clients   map[*websocket.Conn]chan outFrame

	upgrader   websocket.Upgrader
	httpServer *http.Server
	closed     chan struct{}
	closeOnce  sync.Once
}

// New creates a Server that starts listening on addr.
// Clients connect to ws://addr/ and follow the MSE streaming protocol.
func New(addr string) (*Server, error) {
	s := newServer()
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s,
	}

	go s.httpServer.ListenAndServe() //nolint:errcheck

	return s, nil
}

func newServer() *Server {
	s := &Server{
		clients:     make(map[*websocket.Conn]chan outFrame),
		codecsReady: make(chan struct{}),
		upgrader:    websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
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
		s.broadcast(outFrame{websocket.BinaryMessage, data})
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
			s.broadcast(outFrame{websocket.TextMessage, meta})
		}
	}

	if len(data) > 0 {
		s.broadcast(outFrame{websocket.BinaryMessage, data})
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
		s.broadcast(outFrame{websocket.BinaryMessage, data})
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
	tmp := fmp4.NewMuxer(&initBuf)
	if herr := tmp.WriteHeader(context.Background(), updatedStreams); herr == nil {
		s.codecsMu.Lock()
		s.codecStr = buildCodecString(updatedStreams)
		s.initSeg = cloneBytes(initBuf.Bytes())
		s.codecsMu.Unlock()
	}

	if len(data) > 0 {
		s.broadcast(outFrame{websocket.BinaryMessage, data})
	}

	return err
}

// Close flushes remaining samples, shuts down the HTTP server, and closes all
// active WebSocket connections.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		_ = s.WriteTrailer(context.Background(), nil)
		close(s.closed)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
	})

	return nil
}

// ── http.Handler ───────────────────────────────────────────────────────────────

// ServeHTTP upgrades incoming HTTP connections to WebSocket and implements the
// MSE streaming protocol. Mount the Server on any http.ServeMux:
//
//	http.Handle("/mse", mseServer)
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// ── 1. Read handshake ────────────────────────────────────────────────────
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}

	var req wsMessage
	if err := json.Unmarshal(raw, &req); err != nil || req.Type != "mse" {
		return
	}

	// ── 2. Wait for codec info (blocks until WriteHeader is called) ──────────
	select {
	case <-s.codecsReady:
	case <-s.closed:
		return
	case <-r.Context().Done():
		return
	}

	s.codecsMu.RLock()
	codecStr := s.codecStr
	initSeg := cloneBytes(s.initSeg)
	s.codecsMu.RUnlock()

	codecResp, _ := json.Marshal(wsMessage{Type: "mse", Value: codecStr})
	ch := make(chan outFrame, clientChanSize)

	// ── 3. Register and pre-seed channel atomically ──────────────────────────
	//
	// Holding the write lock while pre-seeding guarantees that concurrent
	// broadcasts cannot interleave with the codec-response / init-segment
	// messages: broadcasts block on RLock until we release the Lock here.
	s.clientsMu.Lock()
	s.clients[conn] = ch
	ch <- outFrame{websocket.TextMessage, codecResp} // {"type":"mse","value":"...codecs..."}
	ch <- outFrame{websocket.BinaryMessage, initSeg}  // fMP4 init segment
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()
	}()

	// ── 4. Read pump (gorilla/websocket requires concurrent reads) ────────────
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// ── 5. Write pump ─────────────────────────────────────────────────────────
	for {
		select {
		case frm := <-ch:
			if err := conn.WriteMessage(frm.kind, frm.data); err != nil {
				return
			}
		case <-s.closed:
			return
		}
	}
}

// ── internal ───────────────────────────────────────────────────────────────────

func (s *Server) broadcast(frm outFrame) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for _, ch := range s.clients {
		select {
		case ch <- frm:
		default: // client too slow; drop frame
		}
	}
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
