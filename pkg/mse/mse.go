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
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
)

var (
	ErrFailedToCreateBinaryWriter = errors.New("failed to create binary writer")
	ErrFailedToCreateJSONWriter   = errors.New("failed to create JSON writer")
)

type (
	BinaryWriterFactory func() (io.WriteCloser, error)
	TextWriterFactory   func() (io.WriteCloser, error)
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

// MSEWriter is a WebSocket server and an av.MuxCloser.
//
// Call New (or mount ServeHTTP on an existing mux) and then drive it exactly
// like any other av.MuxCloser: WriteHeader → WritePacket* → WriteTrailer → Close.
type MSEWriter struct {
	binaryWriter  io.WriteCloser
	binaryFactory BinaryWriterFactory

	jsonWriter  io.WriteCloser
	jsonFactory TextWriterFactory

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

func NewFromWriters(binaryWriter, jsonWriter io.WriteCloser) (*MSEWriter, error) {
	m := &MSEWriter{
		binaryWriter: binaryWriter,
		jsonWriter:   jsonWriter,
		codecsReady:  make(chan struct{}),
		closed:       make(chan struct{}),
	}
	m.mux = fmp4.NewMuxer(&m.buf)

	return m, nil
}

func NewFromFactories(
	binaryFactory BinaryWriterFactory,
	jsonFactory TextWriterFactory,
) (*MSEWriter, error) {
	m := &MSEWriter{
		binaryFactory: binaryFactory,
		jsonFactory:   jsonFactory,
		codecsReady:   make(chan struct{}),
		closed:        make(chan struct{}),
	}
	m.mux = fmp4.NewMuxer(&m.buf)

	return m, nil
}

// ── av.MuxCloser ───────────────────────────────────────────────────────────────

// WriteHeader declares all streams, writes the fMP4 init segment, and unblocks
// any WebSocket clients that are waiting for codec information.
func (m *MSEWriter) WriteHeader(ctx context.Context, streams []av.Stream) error {
	m.mu.Lock()
	m.streams = cloneStreams(streams)
	m.buf.Reset()
	err := m.mux.WriteHeader(ctx, streams)
	data := cloneBytes(m.buf.Bytes())
	m.mu.Unlock()

	m.codecsMu.Lock()
	codecStr := buildCodecString(streams)
	m.codecStr = codecStr
	m.initSeg = data
	m.codecsMu.Unlock()

	// Unblock waiting clients (idempotent — select prevents double-close).
	select {
	case <-m.codecsReady: // already closed
	default:
		close(m.codecsReady)
	}

	if meta, jerr := json.Marshal(wsMessage{Type: "mse", Value: codecStr}); jerr == nil {
		if err := m.broadcast(outFrame{websocket.MessageText, meta}); err != nil {
			return err
		}
	}

	if len(data) > 0 {
		if err := m.broadcast(outFrame{websocket.MessageBinary, data}); err != nil {
			return err
		}
	}

	return err
}

// WritePacket buffers a sample; flushes and broadcasts a binary fMP4 fragment on
// each video keyframe (or immediately for audio-only streams). If pkt.Extra is
// non-nil it is marshalled to JSON and broadcast as a text message.
func (m *MSEWriter) WritePacket(ctx context.Context, pkt av.Packet) error {
	m.mu.Lock()
	m.buf.Reset()
	err := m.mux.WritePacket(ctx, pkt)
	data := cloneBytes(m.buf.Bytes())
	m.mu.Unlock()

	if len(data) > 0 {
		if err := m.broadcast(outFrame{websocket.MessageBinary, data}); err != nil {
			return err
		}
	}
	// Per-frame metadata: send before the binary so the client can prepare.
	if pkt.Extra != nil {
		if meta, jerr := json.Marshal(pkt.Extra); jerr == nil {
			if err := m.broadcast(outFrame{websocket.MessageText, meta}); err != nil {
				return err
			}
		}
	}

	return err
}

// WriteTrailer flushes any buffered samples and broadcasts the final fragment.
func (m *MSEWriter) WriteTrailer(ctx context.Context, upstreamErr error) error {
	m.mu.Lock()
	m.buf.Reset()
	err := m.mux.WriteTrailer(ctx, upstreamErr)
	data := cloneBytes(m.buf.Bytes())
	m.mu.Unlock()

	if len(data) > 0 {
		if err := m.broadcast(outFrame{websocket.MessageBinary, data}); err != nil {
			return err
		}
	}

	return err
}

// WriteCodecChange implements av.CodecChanger. It flushes the current fragment,
// broadcasts the codec-change data to existing clients, and stores a fresh init
// segment and updated codec string for clients that connect after the change.
func (m *MSEWriter) WriteCodecChange(ctx context.Context, changed []av.Stream) error {
	m.mu.Lock()

	for _, c := range changed {
		for i, existing := range m.streams {
			if existing.Idx == c.Idx {
				m.streams[i] = c

				break
			}
		}
	}

	updatedStreams := cloneStreams(m.streams)

	m.buf.Reset()
	err := m.mux.WriteCodecChange(ctx, changed)
	data := cloneBytes(m.buf.Bytes())
	m.mu.Unlock()

	// Rebuild a clean init-only segment for late joiners (no fragment prefix).
	var initBuf bytes.Buffer

	detachedCtx := context.WithoutCancel(ctx)

	timeOutCtx, cancel := context.WithTimeout(detachedCtx, 5*time.Second)
	defer cancel()

	tmp := fmp4.NewMuxer(&initBuf)
	if herr := tmp.WriteHeader(timeOutCtx, updatedStreams); herr == nil {
		m.codecsMu.Lock()
		m.codecStr = buildCodecString(updatedStreams)
		m.initSeg = cloneBytes(initBuf.Bytes())
		m.codecsMu.Unlock()
	}

	if err := m.broadcast(outFrame{websocket.MessageBinary, data}); err != nil {
		return err
	}

	return err
}

// Close flushes remaining samples, shuts down the HTTP server, and closes all
// active WebSocket connections.
func (m *MSEWriter) Close() error {
	m.closeOnce.Do(func() {
		_ = m.WriteTrailer(context.Background(), nil)
		close(m.closed)
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

func (m *MSEWriter) broadcast(frame outFrame) error {
	switch frame.kind {
	case websocket.MessageBinary:
		if m.binaryFactory != nil {
			w, err := m.binaryFactory()
			if err != nil {
				return errors.Join(ErrFailedToCreateBinaryWriter, err)
			}

			if w == nil {
				return ErrFailedToCreateBinaryWriter
			}

			m.binaryWriter = w
		}

		if _, err := m.binaryWriter.Write(frame.data); err != nil {
			return err
		}

		if m.binaryFactory != nil {
			err := m.binaryWriter.Close()
			m.binaryWriter = nil

			if err != nil {
				return err
			}
		}
	case websocket.MessageText:
		if m.jsonFactory != nil {
			w, err := m.jsonFactory()
			if err != nil {
				return errors.Join(ErrFailedToCreateJSONWriter, err)
			}

			if w == nil {
				return ErrFailedToCreateJSONWriter
			}

			m.jsonWriter = w
		}

		if _, err := m.jsonWriter.Write(frame.data); err != nil {
			return err
		}

		if m.jsonFactory != nil {
			err := m.jsonWriter.Close()
			m.jsonWriter = nil

			if err != nil {
				return err
			}
		}
	}

	return nil
}
