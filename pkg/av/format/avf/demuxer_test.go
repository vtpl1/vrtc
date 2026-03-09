package avf_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
)

// compile-time check: *avf.Demuxer satisfies av.DemuxCloser.
var _ av.DemuxCloser = (*avf.Demuxer)(nil)

// ── AVF frame builder helpers ─────────────────────────────────────────────────

const avfMagic = "00dc"

// buildFrame writes one AVF frame record to buf.
func buildFrame(mediaType, frameType uint32, tsMs int64, data []byte, frameOff int64) []byte {
	var b bytes.Buffer

	b.WriteString(avfMagic)            // magic [0:4]
	writeInt64(&b, -1)                 // refFrameOff [4:12]
	writeUint32(&b, mediaType)         // mediaType [12:16]
	writeUint32(&b, frameType)         // frameType [16:20]
	writeInt64(&b, tsMs)               // timestamp [20:28]
	writeUint32(&b, uint32(len(data))) //nolint:gosec // frameSize [28:32]
	b.Write(data)                      // payload
	writeInt64(&b, frameOff)           // currentFrameOff

	return b.Bytes()
}

func writeUint32(b *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	b.Write(buf[:])
}

func writeInt64(b *bytes.Buffer, v int64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	b.Write(buf[:])
}

// minimalAVCRecord is a synthetic AVCDecoderConfigurationRecord for a 320×240
// H.264 Baseline-profile stream (same fixture used in the fmp4 tests).
var minimalAVCRecord = []byte{
	0x01,
	0x42, 0x00, 0x1E,
	0xFF,
	0xE1,
	0x00, 0x0F,
	0x67, 0x42, 0x00, 0x1E,
	0xAC, 0xD9, 0x40, 0xA0,
	0x3D, 0xA1, 0x00, 0x00,
	0x03, 0x00, 0x00,
	0x01,
	0x00, 0x04,
	0x68, 0xCE, 0x38, 0x80,
}

// annexBSPSPPS builds an Annex-B byte sequence containing the SPS and PPS
// NALUs extracted from minimalAVCRecord.
func annexBSPSPPS(t *testing.T) []byte {
	t.Helper()

	cd, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(minimalAVCRecord)
	if err != nil {
		t.Fatalf("parse AVC record: %v", err)
	}

	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 1})
	b.Write(cd.SPS())
	b.Write([]byte{0, 0, 0, 1})
	b.Write(cd.PPS())

	return b.Bytes()
}

// withLenPrefix prepends a 4-byte big-endian length (or any 4-byte sequence)
// to simulate the AVF §6.2 4-byte prefix that the demuxer strips.
func withLenPrefix(data []byte) []byte {
	out := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(data))) //nolint:gosec
	copy(out[4:], data)

	return out
}

// buildH264Stream assembles a minimal AVF byte stream:
//
//	CONNECT_HEADER → I_FRAME → P_FRAME → I_FRAME
func buildH264Stream(t *testing.T) []byte {
	t.Helper()

	spsppsBuf := annexBSPSPPS(t)

	var buf bytes.Buffer

	// CONNECT_HEADER
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0))
	// I_FRAME (4-byte prefix + payload)
	buf.Write(buildFrame(2, 1, 33, withLenPrefix([]byte{0x65, 0xDE}), 40+int64(len(spsppsBuf))))
	// P_FRAME
	buf.Write(buildFrame(2, 2, 66, withLenPrefix([]byte{0x41, 0x9A}), 0))
	// Second I_FRAME (triggers CONNECT_HEADER check at runtime)
	buf.Write(buildFrame(2, 3, 99, spsppsBuf, 0))
	buf.Write(buildFrame(2, 1, 99, withLenPrefix([]byte{0x65, 0xBE}), 0))

	return buf.Bytes()
}

// ── GetCodecs tests ───────────────────────────────────────────────────────────

func TestGetCodecs_H264(t *testing.T) {
	data := buildH264Stream(t)
	dmx := avf.New(bytes.NewReader(data))

	streams, err := dmx.GetCodecs(context.Background())
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(streams) != 1 {
		t.Fatalf("want 1 stream, got %d", len(streams))
	}

	if streams[0].Codec.Type() != av.H264 {
		t.Errorf("want H264, got %v", streams[0].Codec.Type())
	}
}

func TestGetCodecs_EmptyStream(t *testing.T) {
	dmx := avf.New(bytes.NewReader([]byte{}))

	_, err := dmx.GetCodecs(context.Background())
	if !errors.Is(err, avf.ErrNoCodecFound) {
		t.Errorf("want ErrNoCodecFound, got %v", err)
	}
}

func TestGetCodecs_AudioOnly_G711U(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(buildFrame(3, 16, 0, []byte{0xAA, 0xBB}, 0)) // G711U AUDIO_FRAME

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	streams, err := dmx.GetCodecs(context.Background())
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(streams) != 1 {
		t.Fatalf("want 1 stream, got %d", len(streams))
	}

	if streams[0].Codec.Type() != av.PCM_MULAW {
		t.Errorf("want PCM_MULAW, got %v", streams[0].Codec.Type())
	}
}

func TestGetCodecs_VideoAndAudio(t *testing.T) {
	spsppsBuf := annexBSPSPPS(t)

	var buf bytes.Buffer
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0))                    // H264 CONNECT_HEADER
	buf.Write(buildFrame(4, 16, 0, []byte{0xCC, 0xDD}, 0))          // G711A AUDIO_FRAME
	buf.Write(buildFrame(2, 1, 33, withLenPrefix([]byte{0x65}), 0)) // H264 I_FRAME

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	streams, err := dmx.GetCodecs(context.Background())
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(streams) != 2 {
		t.Fatalf("want 2 streams, got %d", len(streams))
	}

	if streams[0].Codec.Type() != av.H264 {
		t.Errorf("stream 0: want H264, got %v", streams[0].Codec.Type())
	}

	if streams[1].Codec.Type() != av.PCM_ALAW {
		t.Errorf("stream 1: want PCM_ALAW, got %v", streams[1].Codec.Type())
	}
}

// ── ReadPacket tests ──────────────────────────────────────────────────────────

func TestReadPacket_VideoRoundTrip(t *testing.T) {
	payload := []byte{0x65, 0xDE, 0xAD}
	spsppsBuf := annexBSPSPPS(t)

	var buf bytes.Buffer
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0))
	buf.Write(buildFrame(2, 1, 33, withLenPrefix(payload), 0))
	buf.Write(buildFrame(2, 2, 66, withLenPrefix(payload), 0))

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	pkts := readAll(t, dmx)

	// CONNECT_HEADER should be skipped; 2 video packets returned.
	if len(pkts) != 2 {
		t.Fatalf("want 2 packets, got %d", len(pkts))
	}

	if !pkts[0].KeyFrame {
		t.Error("pkt 0: want KeyFrame=true")
	}

	if pkts[1].KeyFrame {
		t.Error("pkt 1: want KeyFrame=false")
	}

	if !bytes.Equal(pkts[0].Data, payload) {
		t.Errorf("pkt 0 data: want %v, got %v", payload, pkts[0].Data)
	}
}

func TestReadPacket_Timestamps(t *testing.T) {
	spsppsBuf := annexBSPSPPS(t)
	payload := []byte{0x65, 0xAB}

	var buf bytes.Buffer
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0))
	buf.Write(buildFrame(2, 1, 100, withLenPrefix(payload), 0))
	buf.Write(buildFrame(2, 2, 200, withLenPrefix(payload), 0))

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	pkts := readAll(t, dmx)

	if len(pkts) != 2 {
		t.Fatalf("want 2 packets, got %d", len(pkts))
	}

	if pkts[0].DTS != 100*time.Millisecond {
		t.Errorf("pkt 0 DTS: want 100ms, got %v", pkts[0].DTS)
	}

	if pkts[1].DTS != 200*time.Millisecond {
		t.Errorf("pkt 1 DTS: want 200ms, got %v", pkts[1].DTS)
	}
}

func TestReadPacket_EOF(t *testing.T) {
	spsppsBuf := annexBSPSPPS(t)

	var buf bytes.Buffer
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0)) // only init, no media frames

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	_, err := dmx.ReadPacket(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("want io.EOF, got %v", err)
	}
}

func TestReadPacket_CancelledContext(t *testing.T) {
	spsppsBuf := annexBSPSPPS(t)

	var buf bytes.Buffer
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0))

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dmx.ReadPacket(ctx)
	if err == nil {
		t.Error("want error from cancelled context, got nil")
	}
}

func TestReadPacket_MidStreamCodecChange(t *testing.T) {
	spsppsBuf := annexBSPSPPS(t)
	payload := []byte{0x65, 0xAB}

	var buf bytes.Buffer
	buf.Write(buildFrame(2, 3, 0, spsppsBuf, 0))
	buf.Write(buildFrame(2, 1, 33, withLenPrefix(payload), 0))
	// Mid-stream CONNECT_HEADER (codec change).
	buf.Write(buildFrame(2, 3, 66, spsppsBuf, 0))
	buf.Write(buildFrame(2, 1, 66, withLenPrefix(payload), 0))

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	pkts := readAll(t, dmx)

	if len(pkts) != 2 {
		t.Fatalf("want 2 packets, got %d", len(pkts))
	}

	// Second packet should carry NewCodecs after the mid-stream CONNECT_HEADER.
	if pkts[1].NewCodecs == nil {
		t.Error("pkt 1: expected NewCodecs to be set after codec change")
	}
}

// ── Close tests ───────────────────────────────────────────────────────────────

func TestClose_ClosesUnderlying(t *testing.T) {
	closed := false
	rc := &closingReader{
		r:       bytes.NewReader([]byte{}),
		onClose: func() { closed = true },
	}

	dmx := avf.New(rc)
	if err := dmx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !closed {
		t.Error("underlying reader was not closed")
	}
}

func TestClose_NonCloserReader(t *testing.T) {
	dmx := avf.New(bytes.NewReader([]byte{}))
	if err := dmx.Close(); err != nil {
		t.Errorf("Close on non-Closer: want nil, got %v", err)
	}
}

// ── Error cases ───────────────────────────────────────────────────────────────

func TestBadMagic(t *testing.T) {
	// Frame with wrong magic bytes.
	var buf bytes.Buffer
	buf.WriteString("XXXX") // bad magic
	for range 28 {
		buf.WriteByte(0)
	}

	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	_, err := dmx.GetCodecs(context.Background())
	if !errors.Is(err, avf.ErrBadMagic) {
		t.Errorf("want ErrBadMagic, got %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readAll(t *testing.T, dmx *avf.Demuxer) []av.Packet {
	t.Helper()

	var pkts []av.Packet

	for {
		pkt, err := dmx.ReadPacket(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}

		pkts = append(pkts, pkt)
	}

	return pkts
}

type closingReader struct {
	r       io.Reader
	onClose func()
}

func (c *closingReader) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *closingReader) Close() error               { c.onClose(); return nil }
