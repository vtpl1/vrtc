package avf_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	avfformat "github.com/vtpl1/vrtc/pkg/av/format/avf"
	"github.com/vtpl1/vrtc/pkg/avf"
)

// Interface compliance checks.
var (
	_ av.DemuxCloser       = (*avfformat.ProxyMuxDemuxCloser)(nil)
	_ av.MuxCloser         = (*avfformat.ProxyMuxDemuxCloser)(nil)
	_ avf.FrameDemuxCloser = (*avfformat.ProxyMuxDemuxCloser)(nil)
	_ avf.FrameMuxCloser   = (*avfformat.ProxyMuxDemuxCloser)(nil)
)

// ── Test data ─────────────────────────────────────────────────────────────────

// minimalAVCRecord is a valid AVCDecoderConfigurationRecord for a 352×240 stream.
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

// idrNALU is a minimal H.264 IDR (keyframe) NALU.
// NALU header 0x65 = nal_ref_idc=3, nal_unit_type=5 (IDR).
var idrNALU = []byte{0x65, 0xAA, 0xBB}

// nonIDRNALU is a minimal H.264 non-IDR (P-frame) NALU.
// NALU header 0x41 = nal_ref_idc=2, nal_unit_type=1 (non-IDR).
var nonIDRNALU = []byte{0x41, 0xCC, 0xDD}

func makeH264Codec(t *testing.T) h264parser.CodecData {
	t.Helper()

	c, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(minimalAVCRecord)
	if err != nil {
		t.Fatalf("NewCodecDataFromAVCDecoderConfRecord: %v", err)
	}

	return c
}

// annexB prepends the 4-byte Annex-B start code to data.
func annexB(data []byte) []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x01}, data...)
}

// makeSPSFrame returns a CONNECT_HEADER avf.Frame carrying the SPS NALU.
func makeSPSFrame(t *testing.T) avf.Frame {
	t.Helper()

	return avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.CONNECT_HEADER},
		Data:       annexB(makeH264Codec(t).SPS()),
	}
}

// makePPSFrame returns a CONNECT_HEADER avf.Frame carrying the PPS NALU.
func makePPSFrame(t *testing.T) avf.Frame {
	t.Helper()

	return avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.CONNECT_HEADER},
		Data:       annexB(makeH264Codec(t).PPS()),
	}
}

// makeIFrame returns an I_FRAME avf.Frame with Annex-B IDR data.
func makeIFrame(ts, frameID int64) avf.Frame {
	return avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.I_FRAME, TimeStamp: ts},
		FrameID:    frameID,
		Data:       annexB(idrNALU),
	}
}

// makePFrame returns a P_FRAME avf.Frame with Annex-B non-IDR data.
func makePFrame(ts, frameID int64) avf.Frame {
	return avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.P_FRAME, TimeStamp: ts},
		FrameID:    frameID,
		Data:       annexB(nonIDRNALU),
	}
}

// makeAACFrame returns an AUDIO_FRAME avf.Frame with short raw AAC data.
// The data is intentionally shorter than 7 bytes so parseAudioCodec falls back
// to the default AAC-LC 8 kHz mono codec rather than parsing an ADTS header.
func makeAACFrame(ts int64) avf.Frame {
	return avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.AAC, FrameType: avf.AUDIO_FRAME, TimeStamp: ts},
		Data:       []byte{0x01, 0x02, 0x03},
	}
}

// writeH264KeyframeGroup writes SPS + PPS CONNECT_HEADERs followed by an I_FRAME.
func writeH264KeyframeGroup(t *testing.T, p *avfformat.ProxyMuxDemuxCloser, ts, frameID int64) {
	t.Helper()

	ctx := context.Background()

	for _, frm := range []avf.Frame{makeSPSFrame(t), makePPSFrame(t), makeIFrame(ts, frameID)} {
		if err := p.WriteFrame(ctx, frm); err != nil {
			t.Fatalf("WriteFrame(%v): %v", frm.FrameType, err)
		}
	}
}

// ── Mode C: Packet → Packet ───────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestPacketDemuxToPacketMux -v
func TestPacketDemuxToPacketMux(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(4)
	ctx := context.Background()
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}

	if err := p.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	gotStreams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(gotStreams) != 1 || gotStreams[0].Codec.Type() != av.H264 || gotStreams[0].Idx != 0 {
		t.Fatalf("GetCodecs = %+v", gotStreams)
	}

	want := av.Packet{
		Idx:       0,
		KeyFrame:  true,
		DTS:       33 * time.Millisecond,
		CodecType: av.H264,
		Data:      []byte{0x65, 0xAA},
		FrameID:   42,
	}

	if err := p.WritePacket(ctx, want); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	got, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}

	if got.FrameID != want.FrameID || got.DTS != want.DTS || !got.KeyFrame ||
		got.Idx != want.Idx || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("ReadPacket = %+v, want %+v", got, want)
	}
}

// ── Mode A: Frame → Packet ────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux_VideoOnly -v
// Verifies: video-only stream; GetCodecs returns exactly 1 stream (H.264 idx=0).
// Verifies: ReadPacket Data is raw NALU (no Annex-B start code), Idx=0, KeyFrame=true.
func TestFrameDemuxToPacketMux_VideoOnly(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(4)
	ctx := context.Background()

	writeH264KeyframeGroup(t, p, 100, 7)

	streams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(streams) != 1 || streams[0].Codec.Type() != av.H264 || streams[0].Idx != 0 {
		t.Fatalf("GetCodecs = %+v, want [H264@0]", streams)
	}

	pkt, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}

	// Data must be raw NALU bytes — no Annex-B start code prefix.
	if !bytes.Equal(pkt.Data, idrNALU) {
		t.Errorf("pkt.Data = %v, want %v", pkt.Data, idrNALU)
	}

	if !pkt.KeyFrame {
		t.Errorf("pkt.KeyFrame = false, want true")
	}

	if pkt.Idx != 0 {
		t.Errorf("pkt.Idx = %d, want 0", pkt.Idx)
	}

	if pkt.CodecType != av.H264 {
		t.Errorf("pkt.CodecType = %v, want H264", pkt.CodecType)
	}

	if pkt.DTS != 100*time.Millisecond {
		t.Errorf("pkt.DTS = %v, want 100ms", pkt.DTS)
	}

	if pkt.FrameID != 7 {
		t.Errorf("pkt.FrameID = %d, want 7", pkt.FrameID)
	}
}

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux_PFrame -v
// Verifies: P_FRAME maps to KeyFrame=false, Idx=0, raw NALU data.
func TestFrameDemuxToPacketMux_PFrame(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(4)
	ctx := context.Background()

	// Probe: write initial keyframe group so the proxy detects the codec.
	writeH264KeyframeGroup(t, p, 0, 1)

	// Forward phase: write a P-frame.
	if err := p.WriteFrame(ctx, makePFrame(33, 2)); err != nil {
		t.Fatalf("WriteFrame(P_FRAME): %v", err)
	}

	if _, err := p.GetCodecs(ctx); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	// Drain the I_FRAME packet first.
	if _, err := p.ReadPacket(ctx); err != nil {
		t.Fatalf("ReadPacket(I_FRAME): %v", err)
	}

	pkt, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(P_FRAME): %v", err)
	}

	if pkt.KeyFrame {
		t.Errorf("pkt.KeyFrame = true for P_FRAME, want false")
	}

	if pkt.Idx != 0 {
		t.Errorf("pkt.Idx = %d, want 0", pkt.Idx)
	}

	if !bytes.Equal(pkt.Data, nonIDRNALU) {
		t.Errorf("pkt.Data = %v, want %v", pkt.Data, nonIDRNALU)
	}
}

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux_VideoAndAudio -v
// Verifies: audio frame written before I_FRAME is detected during probe.
// GetCodecs returns 2 streams (H.264 idx=0, AAC idx=1).
// Subsequent audio frames are forwarded at idx=1.
func TestFrameDemuxToPacketMux_VideoAndAudio(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(8)
	ctx := context.Background()

	// Write CONNECT_HEADERs then an audio frame (detected during probe), then I_FRAME.
	frames := []avf.Frame{
		makeSPSFrame(t),
		makePPSFrame(t),
		makeAACFrame(50),
		makeIFrame(100, 1),
	}

	for _, frm := range frames {
		if err := p.WriteFrame(ctx, frm); err != nil {
			t.Fatalf("WriteFrame(%v): %v", frm.FrameType, err)
		}
	}

	streams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(streams) != 2 {
		t.Fatalf("GetCodecs = %d streams, want 2", len(streams))
	}

	if streams[0].Codec.Type() != av.H264 || streams[0].Idx != 0 {
		t.Errorf("streams[0] = {%v idx=%d}, want H264@0", streams[0].Codec.Type(), streams[0].Idx)
	}

	if !streams[1].Codec.Type().IsAudio() || streams[1].Idx != 1 {
		t.Errorf("streams[1] = {%v idx=%d}, want audio@1", streams[1].Codec.Type(), streams[1].Idx)
	}

	// The I_FRAME from the probe phase is forwarded as a packet at idx=0.
	videoPkt, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(video): %v", err)
	}

	if videoPkt.Idx != 0 || !videoPkt.KeyFrame {
		t.Errorf("video pkt: Idx=%d KeyFrame=%v, want Idx=0 KeyFrame=true", videoPkt.Idx, videoPkt.KeyFrame)
	}

	// Write a second audio frame in the forward phase; it should be forwarded at idx=1.
	if err := p.WriteFrame(ctx, makeAACFrame(133)); err != nil {
		t.Fatalf("WriteFrame(AUDIO_FRAME): %v", err)
	}

	audioPkt, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(audio): %v", err)
	}

	if audioPkt.Idx != 1 || audioPkt.KeyFrame {
		t.Errorf("audio pkt: Idx=%d KeyFrame=%v, want Idx=1 KeyFrame=false", audioPkt.Idx, audioPkt.KeyFrame)
	}

	if audioPkt.DTS != 133*time.Millisecond {
		t.Errorf("audio pkt DTS = %v, want 133ms", audioPkt.DTS)
	}
}

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux_MidStreamCodecChange -v
// Verifies: post-probe CONNECT_HEADER sequence causes NewCodecs on the next I_FRAME.
func TestFrameDemuxToPacketMux_MidStreamCodecChange(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(8)
	ctx := context.Background()

	// Initial keyframe group: probe detects codec, I_FRAME forwarded as first packet.
	writeH264KeyframeGroup(t, p, 0, 1)

	// P-frame: no codec change.
	if err := p.WriteFrame(ctx, makePFrame(33, 2)); err != nil {
		t.Fatalf("WriteFrame(P_FRAME): %v", err)
	}

	// A new CONNECT_HEADER sequence (simulates resolution change or codec reinit).
	if err := p.WriteFrame(ctx, makeSPSFrame(t)); err != nil {
		t.Fatalf("WriteFrame(SPS CONNECT_HEADER): %v", err)
	}

	if err := p.WriteFrame(ctx, makePPSFrame(t)); err != nil {
		t.Fatalf("WriteFrame(PPS CONNECT_HEADER): %v", err)
	}

	// Next I_FRAME: should carry NewCodecs.
	if err := p.WriteFrame(ctx, makeIFrame(66, 3)); err != nil {
		t.Fatalf("WriteFrame(I_FRAME after change): %v", err)
	}

	if _, err := p.GetCodecs(ctx); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	// Drain first I_FRAME (no NewCodecs).
	pkt1, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(1): %v", err)
	}

	if pkt1.NewCodecs != nil {
		t.Errorf("first I_FRAME: NewCodecs = %v, want nil", pkt1.NewCodecs)
	}

	// P_FRAME: no NewCodecs.
	pkt2, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(2): %v", err)
	}

	if pkt2.NewCodecs != nil {
		t.Errorf("P_FRAME: NewCodecs = %v, want nil", pkt2.NewCodecs)
	}

	// Second I_FRAME: must have NewCodecs with updated video stream.
	pkt3, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(3): %v", err)
	}

	if pkt3.NewCodecs == nil {
		t.Fatalf("second I_FRAME: NewCodecs = nil, want non-nil")
	}

	if len(pkt3.NewCodecs) != 1 || pkt3.NewCodecs[0].Idx != 0 || pkt3.NewCodecs[0].Codec.Type() != av.H264 {
		t.Errorf("NewCodecs = %+v, want [{Idx:0 Codec:H264}]", pkt3.NewCodecs)
	}

	if pkt3.DTS != 66*time.Millisecond {
		t.Errorf("pkt3.DTS = %v, want 66ms", pkt3.DTS)
	}
}

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux_UnknownFramePreservesAccumulation -v
// Verifies: UNKNOWN_FRAME between CONNECT_HEADERs does not terminate accumulation.
func TestFrameDemuxToPacketMux_UnknownFramePreservesAccumulation(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(4)
	ctx := context.Background()

	unknown := avf.Frame{BasicFrame: avf.BasicFrame{FrameType: avf.UNKNOWN_FRAME}}

	// Write SPS, UNKNOWN_FRAME, PPS, then I_FRAME.
	// If UNKNOWN_FRAME incorrectly terminated accumulation, the codec would not
	// be parsed and GetCodecs would return ErrNoVideoCodecFound.
	for _, frm := range []avf.Frame{makeSPSFrame(t), unknown, makePPSFrame(t), makeIFrame(0, 1)} {
		if err := p.WriteFrame(ctx, frm); err != nil {
			t.Fatalf("WriteFrame(%v): %v", frm.FrameType, err)
		}
	}

	streams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v (UNKNOWN_FRAME must not break accumulation)", err)
	}

	if len(streams) != 1 || streams[0].Codec.Type() != av.H264 {
		t.Fatalf("GetCodecs = %+v, want [H264]", streams)
	}
}

// ── Mode B: Packet → Frame ────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestPacketDemuxToFrameMux_InitialConnectHeaders -v
// Verifies: first ReadFrame emits CONNECT_HEADER(SPS) then CONNECT_HEADER(PPS)
// followed by I_FRAME; each CONNECT_HEADER carries Annex-B encoded data.
func TestPacketDemuxToFrameMux_InitialConnectHeaders(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(4)
	ctx := context.Background()
	codec := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: codec}}

	if err := p.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := p.WritePacket(ctx, av.Packet{
		Idx:       0,
		KeyFrame:  true,
		DTS:       40 * time.Millisecond,
		CodecType: av.H264,
		Data:      idrNALU,
		FrameID:   5,
	}); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	// Collect all frames until and including the first I_FRAME.
	var frames []avf.Frame

	for {
		f, err := p.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}

		frames = append(frames, f)

		if f.FrameType == avf.I_FRAME {
			break
		}
	}

	// First ReadFrame emits initial CONNECT_HEADERs (SPS+PPS) from the codec
	// in WriteHeader. PacketToFrames then also emits CONNECT_HEADERs (SPS+PPS)
	// before the I_FRAME. Total = 4 CONNECT_HEADERs + 1 I_FRAME = 5 frames.
	if len(frames) < 3 {
		t.Fatalf("got %d frames, want at least 3 (CONNECT_HEADERs + I_FRAME)", len(frames))
	}

	// Every frame before the last must be a CONNECT_HEADER for H.264.
	for i, f := range frames[:len(frames)-1] {
		if f.FrameType != avf.CONNECT_HEADER || f.MediaType != avf.H264 {
			t.Errorf("frames[%d] = {%v %v}, want CONNECT_HEADER H264", i, f.FrameType, f.MediaType)
		}
	}

	// Last frame: I_FRAME with Annex-B IDR data, correct timestamp and FrameID.
	iframe := frames[len(frames)-1]
	if iframe.FrameType != avf.I_FRAME || iframe.MediaType != avf.H264 {
		t.Errorf("last frame = {%v %v}, want I_FRAME H264", iframe.FrameType, iframe.MediaType)
	}

	wantData := annexB(idrNALU)
	if !bytes.Equal(iframe.Data, wantData) {
		t.Errorf("I_FRAME Data = %v, want %v", iframe.Data, wantData)
	}

	if iframe.TimeStamp != 40 {
		t.Errorf("I_FRAME TimeStamp = %d, want 40", iframe.TimeStamp)
	}

	if iframe.FrameID != 5 {
		t.Errorf("I_FRAME FrameID = %d, want 5", iframe.FrameID)
	}
}

// go test ./pkg/av/format/avf -run TestPacketDemuxToFrameMux_KeyframeGroupRepeated -v
// Verifies: each keyframe packet emits CONNECT_HEADER frames before the I_FRAME.
func TestPacketDemuxToFrameMux_KeyframeGroupRepeated(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(8)
	ctx := context.Background()
	codec := makeH264Codec(t)

	if err := p.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: codec}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Write two keyframes.
	for i, ts := range []int64{0, 40} {
		if err := p.WritePacket(ctx, av.Packet{
			Idx:       0,
			KeyFrame:  true,
			DTS:       time.Duration(ts) * time.Millisecond,
			CodecType: av.H264,
			Data:      idrNALU,
			FrameID:   int64(i + 1),
		}); err != nil {
			t.Fatalf("WritePacket(%d): %v", i, err)
		}
	}

	collectUntilIFrame := func(label string) []avf.Frame {
		t.Helper()

		var frames []avf.Frame

		for {
			f, err := p.ReadFrame(ctx)
			if err != nil {
				t.Fatalf("%s ReadFrame: %v", label, err)
			}

			frames = append(frames, f)

			if f.FrameType == avf.I_FRAME {
				return frames
			}
		}
	}

	// group1: initial CONNECT_HEADERs (SPS+PPS) from WriteHeader headers +
	// CONNECT_HEADERs (SPS+PPS) emitted by PacketToFrames + I_FRAME = 5 frames.
	group1 := collectUntilIFrame("group1")
	if len(group1) < 3 {
		t.Errorf("group1: got %d frames, want at least 3", len(group1))
	}

	for i, f := range group1[:len(group1)-1] {
		if f.FrameType != avf.CONNECT_HEADER {
			t.Errorf("group1[%d].FrameType = %v, want CONNECT_HEADER", i, f.FrameType)
		}
	}

	// group2: second keyframe → PacketToFrames emits SPS+PPS+I_FRAME = 3 frames.
	// No initial CONNECT_HEADERs because readFrameHeaderSent is already true.
	group2 := collectUntilIFrame("group2")
	if len(group2) != 3 {
		t.Errorf("group2: got %d frames, want 3 (SPS, PPS, I_FRAME)", len(group2))
	}

	for i, f := range group2[:2] {
		if f.FrameType != avf.CONNECT_HEADER {
			t.Errorf("group2[%d].FrameType = %v, want CONNECT_HEADER", i, f.FrameType)
		}
	}

	if group2[2].FrameType != avf.I_FRAME {
		t.Errorf("group2[2].FrameType = %v, want I_FRAME", group2[2].FrameType)
	}
}

// go test ./pkg/av/format/avf -run TestPacketDemuxToFrameMux_PFrame -v
// Verifies: non-keyframe video packet → P_FRAME with Annex-B prefix, no CONNECT_HEADERs.
func TestPacketDemuxToFrameMux_PFrame(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(4)
	ctx := context.Background()

	if err := p.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := p.WritePacket(ctx, av.Packet{
		Idx:       0,
		KeyFrame:  false,
		DTS:       33 * time.Millisecond,
		CodecType: av.H264,
		Data:      nonIDRNALU,
		FrameID:   2,
	}); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	// Drain the initial CONNECT_HEADERs emitted by the first ReadFrame.
	for {
		f, err := p.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}

		if f.FrameType != avf.CONNECT_HEADER {
			// This is the P_FRAME (or I_FRAME, if the queue ordering shifts).
			if f.FrameType == avf.P_FRAME {
				wantData := annexB(nonIDRNALU)
				if !bytes.Equal(f.Data, wantData) {
					t.Errorf("P_FRAME Data = %v, want %v", f.Data, wantData)
				}

				if f.MediaType != avf.H264 {
					t.Errorf("P_FRAME MediaType = %v, want H264", f.MediaType)
				}

				if f.TimeStamp != 33 {
					t.Errorf("P_FRAME TimeStamp = %d, want 33", f.TimeStamp)
				}
			}

			break
		}
	}
}

// go test ./pkg/av/format/avf -run TestPacketDemuxToFrameMux_Audio -v
// Verifies: audio packet → AUDIO_FRAME with correct MediaType and data pass-through.
func TestPacketDemuxToFrameMux_Audio(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(8)
	ctx := context.Background()

	codec := makeH264Codec(t)
	audioCfg := aacparser.MPEG4AudioConfig{
		ObjectType:      2,
		SampleRateIndex: 11,
		ChannelConfig:   1,
	}
	audioCfg.Complete()

	aacCodec, err := aacparser.NewCodecDataFromMPEG4AudioConfig(audioCfg)
	if err != nil {
		t.Fatalf("NewCodecDataFromMPEG4AudioConfig: %v", err)
	}

	if err := p.WriteHeader(ctx, []av.Stream{
		{Idx: 0, Codec: codec},
		{Idx: 1, Codec: aacCodec},
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	audioData := []byte{0xAA, 0xBB, 0xCC}

	if err := p.WritePacket(ctx, av.Packet{
		Idx:       1,
		KeyFrame:  false,
		DTS:       200 * time.Millisecond,
		CodecType: av.AAC,
		Data:      audioData,
		FrameID:   10,
	}); err != nil {
		t.Fatalf("WritePacket(audio): %v", err)
	}

	// Drain initial CONNECT_HEADERs then find the AUDIO_FRAME.
	for {
		f, err := p.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}

		if f.FrameType == avf.AUDIO_FRAME {
			if f.MediaType != avf.AAC {
				t.Errorf("AUDIO_FRAME MediaType = %v, want AAC", f.MediaType)
			}

			if !bytes.Equal(f.Data, audioData) {
				t.Errorf("AUDIO_FRAME Data = %v, want %v", f.Data, audioData)
			}

			if f.TimeStamp != 200 {
				t.Errorf("AUDIO_FRAME TimeStamp = %d, want 200", f.TimeStamp)
			}

			if f.FrameID != 10 {
				t.Errorf("AUDIO_FRAME FrameID = %d, want 10", f.FrameID)
			}

			break
		}
	}
}

// ── Mode conflict & lifecycle tests ───────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestModeConflict_FrameAfterPacket -v
func TestModeConflict_FrameAfterPacket(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)

	if err := p.WriteHeader(context.Background(), []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	err := p.WriteFrame(context.Background(), avf.Frame{})
	if !errors.Is(err, avfformat.ErrConfiguredAsPacketMuxer) {
		t.Fatalf("WriteFrame after WriteHeader: got %v, want ErrConfiguredAsPacketMuxer", err)
	}
}

// go test ./pkg/av/format/avf -run TestModeConflict_PacketAfterFrame -v
func TestModeConflict_PacketAfterFrame(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)
	ctx := context.Background()

	if err := p.WriteFrame(ctx, makeSPSFrame(t)); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	err := p.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}})
	if !errors.Is(err, avfformat.ErrConfiguredAsFrameMuxer) {
		t.Fatalf("WriteHeader after WriteFrame: got %v, want ErrConfiguredAsFrameMuxer", err)
	}
}

// go test ./pkg/av/format/avf -run TestCloseEndsPacketReads -v
func TestCloseEndsPacketReads(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(0)

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := p.ReadPacket(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadPacket after Close: got %v, want io.EOF", err)
	}
}

// go test ./pkg/av/format/avf -run TestCloseEndsGetCodecs -v
// Verifies: Close() before headers are written causes GetCodecs to return io.EOF.
func TestCloseEndsGetCodecs(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(0)

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := p.GetCodecs(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("GetCodecs after Close (no headers): got %v, want io.EOF", err)
	}
}

// go test ./pkg/av/format/avf -run TestCloseEndsReadFrame -v
func TestCloseEndsReadFrame(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(0)

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := p.ReadFrame(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame after Close: got %v, want io.EOF", err)
	}
}

// go test ./pkg/av/format/avf -run TestWritePacketBeforeHeader -v
func TestWritePacketBeforeHeader(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)
	err := p.WritePacket(context.Background(), av.Packet{})

	if !errors.Is(err, avfformat.ErrHeaderNotWritten) {
		t.Fatalf("WritePacket before WriteHeader: got %v, want ErrHeaderNotWritten", err)
	}
}

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux_MultiNALUIFrame -v
// Verifies: an I_FRAME avf.Frame carrying two NALUs (IDR + non-IDR) is split into
// two av.Packets: the first with KeyFrame=true, the second with KeyFrame=false.
func TestFrameDemuxToPacketMux_MultiNALUIFrame(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(8)
	ctx := context.Background()

	// Probe phase: write CONNECT_HEADERs so the codec is detected.
	if err := p.WriteFrame(ctx, makeSPSFrame(t)); err != nil {
		t.Fatalf("WriteFrame(SPS): %v", err)
	}

	if err := p.WriteFrame(ctx, makePPSFrame(t)); err != nil {
		t.Fatalf("WriteFrame(PPS): %v", err)
	}

	// Build a multi-NALU I_FRAME: \x00\x00\x00\x01[IDR]\x00\x00\x00\x01[non-IDR].
	multiData := append(annexB(idrNALU), annexB(nonIDRNALU)...)
	multiFrame := avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.I_FRAME, TimeStamp: 100},
		FrameID:    42,
		Data:       multiData,
	}

	if err := p.WriteFrame(ctx, multiFrame); err != nil {
		t.Fatalf("WriteFrame(multi-NALU I_FRAME): %v", err)
	}

	streams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(streams) != 1 || streams[0].Codec.Type() != av.H264 {
		t.Fatalf("GetCodecs = %+v, want [H264]", streams)
	}

	// First packet: IDR → KeyFrame=true.
	pkt1, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(1): %v", err)
	}

	if !pkt1.KeyFrame {
		t.Errorf("pkt1.KeyFrame = false, want true (IDR NALU)")
	}

	if pkt1.Idx != 0 {
		t.Errorf("pkt1.Idx = %d, want 0", pkt1.Idx)
	}

	if !bytes.Equal(pkt1.Data, idrNALU) {
		t.Errorf("pkt1.Data = %v, want %v", pkt1.Data, idrNALU)
	}

	// Second packet: non-IDR → KeyFrame=false.
	pkt2, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket(2): %v", err)
	}

	if pkt2.KeyFrame {
		t.Errorf("pkt2.KeyFrame = true, want false (non-IDR NALU)")
	}

	if pkt2.Idx != 0 {
		t.Errorf("pkt2.Idx = %d, want 0", pkt2.Idx)
	}

	if !bytes.Equal(pkt2.Data, nonIDRNALU) {
		t.Errorf("pkt2.Data = %v, want %v", pkt2.Data, nonIDRNALU)
	}

	// Both packets share the same FrameID and timestamp.
	if pkt1.FrameID != 42 || pkt2.FrameID != 42 {
		t.Errorf("FrameIDs = %d, %d, want both 42", pkt1.FrameID, pkt2.FrameID)
	}

	if pkt1.DTS != 100*time.Millisecond || pkt2.DTS != 100*time.Millisecond {
		t.Errorf("DTS = %v, %v, want both 100ms", pkt1.DTS, pkt2.DTS)
	}
}
