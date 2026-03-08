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
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
)

// compile-time check: *avf.Muxer satisfies av.MuxCloser and av.CodecChanger.
var _ av.MuxCloser = (*avf.Muxer)(nil)
var _ av.CodecChanger = (*avf.Muxer)(nil)

// ── lifecycle error tests ─────────────────────────────────────────────────────

func TestMuxer_WriteHeader_Idempotency(t *testing.T) {
	m := avf.NewMuxer(io.Discard)
	ctx := context.Background()
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}

	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("first WriteHeader: %v", err)
	}

	if err := m.WriteHeader(ctx, streams); err == nil {
		t.Fatal("second WriteHeader should return error")
	}
}

func TestMuxer_WriteTrailer_Idempotency(t *testing.T) {
	m := avf.NewMuxer(io.Discard)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("first WriteTrailer: %v", err)
	}

	if err := m.WriteTrailer(ctx, nil); err == nil {
		t.Fatal("second WriteTrailer should return error")
	}
}

func TestMuxer_WritePacket_BeforeWriteHeader(t *testing.T) {
	m := avf.NewMuxer(io.Discard)
	pkt := av.Packet{Idx: 0, KeyFrame: true, Data: []byte{0x65}, CodecType: av.H264}

	if err := m.WritePacket(context.Background(), pkt); err == nil {
		t.Fatal("WritePacket before WriteHeader should return error")
	}
}

// ── output structure tests ────────────────────────────────────────────────────

// TestMuxer_NoFileHeader verifies that the AVF format has no file-level header:
// WriteHeader writes zero bytes; bytes only appear on WritePacket.
func TestMuxer_NoFileHeader(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("WriteHeader must not write bytes; got %d bytes", buf.Len())
	}
}

// TestMuxer_KeyFrameEmitsConnectHeader verifies that a video keyframe causes a
// CONNECT_HEADER frame to be written immediately before the I_FRAME.
func TestMuxer_KeyFrameEmitsConnectHeader(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	pkt := av.Packet{
		Idx:       0,
		KeyFrame:  true,
		DTS:       33 * time.Millisecond,
		Data:      []byte{0x65, 0xDE, 0xAD},
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	// There must be at least two frames: CONNECT_HEADER + I_FRAME.
	frames := parseFrames(t, buf.Bytes())
	if len(frames) < 2 {
		t.Fatalf("want ≥2 frames, got %d", len(frames))
	}

	if frames[0].frameType != 3 { // frameTypeConnectHeader
		t.Errorf("frame 0: want CONNECT_HEADER (3), got %d", frames[0].frameType)
	}

	if frames[1].frameType != 1 { // frameTypeIFrame
		t.Errorf("frame 1: want I_FRAME (1), got %d", frames[1].frameType)
	}
}

// TestMuxer_NonKeyFrameNoConnectHeader verifies that a P_FRAME does not
// cause a CONNECT_HEADER to be written.
func TestMuxer_NonKeyFrameNoConnectHeader(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// First write a keyframe so the codec state is established.
	kfPkt := av.Packet{
		Idx: 0, KeyFrame: true, DTS: 0, Data: []byte{0x65}, CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, kfPkt); err != nil {
		t.Fatalf("WritePacket kf: %v", err)
	}

	sizeBefore := buf.Len()

	// Now write a P_FRAME.
	pfPkt := av.Packet{
		Idx: 0, KeyFrame: false, DTS: 33 * time.Millisecond, Data: []byte{0x41}, CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pfPkt); err != nil {
		t.Fatalf("WritePacket pf: %v", err)
	}

	added := buf.Bytes()[sizeBefore:]
	frames := parseFrames(t, added)

	if len(frames) != 1 {
		t.Fatalf("want 1 frame for P_FRAME, got %d", len(frames))
	}

	if frames[0].frameType != 2 { // frameTypePFrame
		t.Errorf("want P_FRAME (2), got %d", frames[0].frameType)
	}
}

// TestMuxer_AudioFrame verifies that an audio packet is written as AUDIO_FRAME (16).
func TestMuxer_AudioFrame(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: pcm.NewPCMMulawCodecData()}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	audioData := []byte{0xAA, 0xBB, 0xCC}
	pkt := av.Packet{Idx: 0, DTS: 0, Data: audioData, CodecType: av.PCM_MULAW}

	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	frames := parseFrames(t, buf.Bytes())
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}

	if frames[0].frameType != 16 { // frameTypeAudioFrame
		t.Errorf("want AUDIO_FRAME (16), got %d", frames[0].frameType)
	}

	if !bytes.Equal(frames[0].data, audioData) {
		t.Errorf("audio data mismatch: want %v, got %v", audioData, frames[0].data)
	}
}

// TestMuxer_CurrentFrameOff verifies that the CurrentFrameOff trailer field
// holds the byte offset of the frame's own start.
func TestMuxer_CurrentFrameOff(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: pcm.NewPCMMulawCodecData()}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	for i, data := range [][]byte{{0xAA}, {0xBB, 0xCC}} {
		pkt := av.Packet{Idx: 0, DTS: time.Duration(i) * 20 * time.Millisecond, Data: data}
		if err := m.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("WritePacket[%d]: %v", i, err)
		}
	}

	frames := parseFrames(t, buf.Bytes())
	if len(frames) != 2 {
		t.Fatalf("want 2 frames, got %d", len(frames))
	}

	// Frame 0 starts at offset 0; its CurrentFrameOff must be 0.
	if frames[0].currentFrameOff != 0 {
		t.Errorf("frame 0 CurrentFrameOff: want 0, got %d", frames[0].currentFrameOff)
	}

	// Frame 1 starts right after frame 0: 40 + len(data[0]) = 40 + 1 = 41.
	want1 := int64(40 + len([]byte{0xAA}))
	if frames[1].currentFrameOff != want1 {
		t.Errorf("frame 1 CurrentFrameOff: want %d, got %d", want1, frames[1].currentFrameOff)
	}
}

// TestMuxer_RefFrameOff_BeforeConnectHeader verifies that all frames before
// the first CONNECT_HEADER carry RefFrameOff = -1.
func TestMuxer_RefFrameOff_BeforeConnectHeader(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	// Audio-only stream: no CONNECT_HEADER is ever written.
	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: pcm.NewPCMMulawCodecData()}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.WritePacket(ctx, av.Packet{Idx: 0, Data: []byte{0xAA}}); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	frames := parseFrames(t, buf.Bytes())
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}

	if frames[0].refFrameOff != -1 {
		t.Errorf("RefFrameOff before any CONNECT_HEADER: want -1, got %d", frames[0].refFrameOff)
	}
}

// TestMuxer_RefFrameOff_AfterConnectHeader verifies that all frames after a
// CONNECT_HEADER carry RefFrameOff pointing to that CONNECT_HEADER's start.
func TestMuxer_RefFrameOff_AfterConnectHeader(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Write a keyframe — triggers CONNECT_HEADER then I_FRAME.
	pkt := av.Packet{Idx: 0, KeyFrame: true, DTS: 0, Data: []byte{0x65}, CodecType: av.H264}
	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket kf: %v", err)
	}

	frames := parseFrames(t, buf.Bytes())
	if len(frames) < 2 {
		t.Fatalf("want ≥2 frames, got %d", len(frames))
	}

	connectHdrStart := frames[0].currentFrameOff // = 0

	// CONNECT_HEADER itself points to its own start.
	if frames[0].refFrameOff != connectHdrStart {
		t.Errorf("CONNECT_HEADER RefFrameOff: want %d, got %d",
			connectHdrStart, frames[0].refFrameOff)
	}

	// I_FRAME must also point to the CONNECT_HEADER.
	if frames[1].refFrameOff != connectHdrStart {
		t.Errorf("I_FRAME RefFrameOff: want %d (CONNECT_HEADER offset), got %d",
			connectHdrStart, frames[1].refFrameOff)
	}
}

// TestMuxer_StartCodePrefix verifies that the 4-byte start code "\x00\x00\x00\x01"
// is prepended to I_FRAME and P_FRAME payloads (§6.2).
func TestMuxer_StartCodePrefix(t *testing.T) {
	var buf bytes.Buffer
	m := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	payload := []byte{0x65, 0xDE}
	pkt := av.Packet{Idx: 0, KeyFrame: true, DTS: 0, Data: payload, CodecType: av.H264}

	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	frames := parseFrames(t, buf.Bytes())
	// frames[0] = CONNECT_HEADER, frames[1] = I_FRAME
	iFrame := frames[len(frames)-1]

	if len(iFrame.data) < 4 {
		t.Fatalf("I_FRAME payload too short: %d bytes", len(iFrame.data))
	}

	wantPrefix := []byte{0x00, 0x00, 0x00, 0x01}
	if !bytes.Equal(iFrame.data[:4], wantPrefix) {
		t.Errorf("I_FRAME prefix: want %v, got %v", wantPrefix, iFrame.data[:4])
	}

	if !bytes.Equal(iFrame.data[4:], payload) {
		t.Errorf("I_FRAME NALU data: want %v, got %v", payload, iFrame.data[4:])
	}
}

// ── Close tests ───────────────────────────────────────────────────────────────

func TestMuxer_Close_ClosesUnderlying(t *testing.T) {
	cw := &closingWriter{}
	m := avf.NewMuxer(cw)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !cw.closed {
		t.Error("Close did not call Close() on the underlying writer")
	}
}

func TestMuxer_Close_NonCloserWriter(t *testing.T) {
	m := avf.NewMuxer(io.Discard)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close on non-Closer writer: %v", err)
	}
}

// ── round-trip test ───────────────────────────────────────────────────────────

// TestRoundTrip_H264_Audio muxes H264+audio packets into an AVF byte stream
// then demuxes them back and verifies the output matches the input.
func TestRoundTrip_H264_Audio(t *testing.T) {
	h264 := makeH264Codec(t)
	g711 := pcm.NewPCMMulawCodecData()

	streams := []av.Stream{
		{Idx: 0, Codec: h264},
		{Idx: 1, Codec: g711},
	}

	const vidDur = 33 * time.Millisecond
	const audDur = 20 * time.Millisecond

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
		{Idx: 1, DTS: 0, Duration: audDur, Data: []byte{0xAA, 0xBB}, CodecType: av.PCM_MULAW},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
	}

	// ── mux ──────────────────────────────────────────────────────────────────
	var buf bytes.Buffer

	mux := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := mux.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("mux WriteHeader: %v", err)
	}

	for _, pkt := range inPkts {
		if err := mux.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("mux WritePacket: %v", err)
		}
	}

	if err := mux.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("mux WriteTrailer: %v", err)
	}

	// ── demux ─────────────────────────────────────────────────────────────────
	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	gotStreams, err := dmx.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("dmx GetCodecs: %v", err)
	}

	if len(gotStreams) != 2 {
		t.Fatalf("want 2 streams, got %d", len(gotStreams))
	}

	if gotStreams[0].Codec.Type() != av.H264 {
		t.Errorf("stream 0: want H264, got %v", gotStreams[0].Codec.Type())
	}

	if gotStreams[1].Codec.Type() != av.PCM_MULAW {
		t.Errorf("stream 1: want PCM_MULAW, got %v", gotStreams[1].Codec.Type())
	}

	var outPkts []av.Packet

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("dmx ReadPacket: %v", err)
		}

		outPkts = append(outPkts, pkt)
	}

	// Expect all input packets back (CONNECT_HEADERs are skipped by demuxer).
	if len(outPkts) != len(inPkts) {
		t.Fatalf("want %d packets, got %d", len(inPkts), len(outPkts))
	}

	for i, want := range inPkts {
		got := outPkts[i]

		if got.KeyFrame != want.KeyFrame {
			t.Errorf("pkt %d: KeyFrame want %v got %v", i, want.KeyFrame, got.KeyFrame)
		}

		if got.DTS != want.DTS {
			t.Errorf("pkt %d: DTS want %v got %v", i, want.DTS, got.DTS)
		}

		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("pkt %d: Data want %v got %v", i, want.Data, got.Data)
		}
	}
}

// TestRoundTrip_CodecChange verifies that WriteCodecChange followed by a
// keyframe causes the demuxer to receive NewCodecs on the next packet.
func TestRoundTrip_CodecChange(t *testing.T) {
	h264 := makeH264Codec(t)

	var buf bytes.Buffer
	mux := avf.NewMuxer(&buf)
	ctx := context.Background()

	if err := mux.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	writeKF := func(dts time.Duration, data []byte) {
		t.Helper()
		pkt := av.Packet{Idx: 0, KeyFrame: true, DTS: dts, Data: data, CodecType: av.H264}
		if err := mux.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("WritePacket: %v", err)
		}
	}

	writeKF(0, []byte{0x65, 0x01})

	// Signal codec change then write another keyframe.
	if err := mux.WriteCodecChange(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteCodecChange: %v", err)
	}

	writeKF(33*time.Millisecond, []byte{0x65, 0x02})

	// Demux and verify the second keyframe carries NewCodecs.
	dmx := avf.New(bytes.NewReader(buf.Bytes()))

	if _, err := dmx.GetCodecs(ctx); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	var pkts []av.Packet

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}

		pkts = append(pkts, pkt)
	}

	if len(pkts) < 2 {
		t.Fatalf("want ≥2 packets, got %d", len(pkts))
	}

	if pkts[1].NewCodecs == nil {
		t.Error("second keyframe packet: expected NewCodecs to be non-nil after codec change")
	}
}

// ── frame parser (test helper) ────────────────────────────────────────────────

type parsedFrame struct {
	mediaType      uint32
	frameType      uint32
	refFrameOff    int64
	currentFrameOff int64
	data           []byte
}

// parseFrames parses raw AVF bytes into a slice of parsedFrame descriptors.
func parseFrames(t *testing.T, data []byte) []parsedFrame {
	t.Helper()

	var frames []parsedFrame
	r := bytes.NewReader(data)

	for r.Len() >= 40 {
		var hdr [32]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break
		}

		if string(hdr[0:4]) != "00dc" {
			t.Errorf("bad magic: %q", string(hdr[0:4]))
			break
		}

		refFrameOff := int64(binary.BigEndian.Uint64(hdr[4:12]))
		mediaType := binary.BigEndian.Uint32(hdr[12:16])
		frameType := binary.BigEndian.Uint32(hdr[16:20])
		frameSize := binary.BigEndian.Uint32(hdr[28:32])

		payload := make([]byte, frameSize)
		if _, err := io.ReadFull(r, payload); err != nil {
			t.Errorf("parseFrames: short payload: %v", err)
			break
		}

		var trailer [8]byte
		if _, err := io.ReadFull(r, trailer[:]); err != nil {
			t.Errorf("parseFrames: short trailer: %v", err)
			break
		}

		currentFrameOff := int64(binary.BigEndian.Uint64(trailer[:]))

		frames = append(frames, parsedFrame{
			mediaType:       mediaType,
			frameType:       frameType,
			refFrameOff:     refFrameOff,
			currentFrameOff: currentFrameOff,
			data:            payload,
		})
	}

	return frames
}

// ── shared helpers ────────────────────────────────────────────────────────────

type closingWriter struct {
	bytes.Buffer
	closed bool
}

func (cw *closingWriter) Close() error {
	cw.closed = true
	return nil
}

func makeH264Codec(t *testing.T) h264parser.CodecData {
	t.Helper()

	c, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(minimalAVCRecord)
	if err != nil {
		t.Fatalf("h264parser: %v", err)
	}

	return c
}
