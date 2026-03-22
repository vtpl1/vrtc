package avf_test

import (
	"bytes"
	"testing"

	"github.com/vtpl1/vrtc/pkg/avf"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// sc prepends the 4-byte Annex-B start code to data.
func sc(data []byte) []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x01}, data...)
}

// minimalSPS is an H.264 SPS NALU (profile_idc=0x42, level=30, single ref frame).
// NALU header: 0x67 = nal_ref_idc=3, nal_unit_type=7 (SPS).
var minimalSPS = []byte{0x67, 0x42, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0xA0, 0x3D, 0xA1}

// minimalPPS is a minimal H.264 PPS NALU.
// NALU header: 0x68 = nal_ref_idc=3, nal_unit_type=8 (PPS).
var minimalPPS = []byte{0x68, 0xCE, 0x38, 0x80}

// idrNALU is a minimal H.264 IDR (keyframe) NALU.
// NALU header: 0x65 = nal_ref_idc=3, nal_unit_type=5 (IDR).
var idrNALU = []byte{0x65, 0xAA, 0xBB}

// nonIDRNALU is a minimal H.264 non-IDR NALU.
// NALU header: 0x41 = nal_ref_idc=2, nal_unit_type=1 (non-IDR).
var nonIDRNALU = []byte{0x41, 0xCC, 0xDD}

// seiNALU is a minimal H.264 SEI NALU (auxiliary / non-ref).
// NALU header: 0x06 = nal_ref_idc=0, nal_unit_type=6 (SEI).
var seiNALU = []byte{0x06, 0x00}

func h264IFrame(data []byte) avf.Frame {
	return avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.I_FRAME},
		Data:       data,
		DurationMs: 33,
	}
}

// ── SplitFrame tests ──────────────────────────────────────────────────────────

// go test ./pkg/avf -run TestSplitFrame_SingleNALU -v
// A single-NALU I_FRAME is returned unchanged (same pointer-equal slice).
func TestSplitFrame_SingleNALU(t *testing.T) {
	t.Parallel()

	frm := h264IFrame(sc(idrNALU))
	out := avf.SplitFrame(frm)

	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}

	if !bytes.Equal(out[0].Data, frm.Data) {
		t.Errorf("Data changed for single-NALU frame")
	}

	if out[0].DurationMs != 33 {
		t.Errorf("DurationMs = %d, want 33", out[0].DurationMs)
	}
}

// go test ./pkg/avf -run TestSplitFrame_MultiNALU_SEIDR -v
// SEI + IDR → one NON_REF_FRAME (SEI) + one I_FRAME (IDR).
func TestSplitFrame_MultiNALU_SEIDR(t *testing.T) {
	t.Parallel()

	data := append(sc(seiNALU), sc(idrNALU)...)
	frm := avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.I_FRAME, TimeStamp: 200},
		FrameID:    7,
		DurationMs: 40,
		Data:       data,
	}

	out := avf.SplitFrame(frm)

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (SEI + IDR)", len(out))
	}

	// SEI → NON_REF_FRAME
	if out[0].FrameType != avf.NON_REF_FRAME {
		t.Errorf("out[0].FrameType = %v, want NON_REF_FRAME", out[0].FrameType)
	}

	if !bytes.Equal(out[0].Data, sc(seiNALU)) {
		t.Errorf("out[0].Data = %v, want %v", out[0].Data, sc(seiNALU))
	}

	// IDR → I_FRAME
	if out[1].FrameType != avf.I_FRAME {
		t.Errorf("out[1].FrameType = %v, want I_FRAME", out[1].FrameType)
	}

	if !bytes.Equal(out[1].Data, sc(idrNALU)) {
		t.Errorf("out[1].Data = %v, want %v", out[1].Data, sc(idrNALU))
	}

	// Metadata preserved on all frames.
	for i, f := range out {
		if f.TimeStamp != 200 {
			t.Errorf("out[%d].TimeStamp = %d, want 200", i, f.TimeStamp)
		}

		if f.FrameID != 7 {
			t.Errorf("out[%d].FrameID = %d, want 7", i, f.FrameID)
		}

		if f.MediaType != avf.H264 {
			t.Errorf("out[%d].MediaType = %v, want H264", i, f.MediaType)
		}
	}
}

// go test ./pkg/avf -run TestSplitFrame_MultiNALU_ParamSetsDropped -v
// SPS + PPS + IDR → inline param-set NALUs dropped; only I_FRAME emitted.
func TestSplitFrame_MultiNALU_ParamSetsDropped(t *testing.T) {
	t.Parallel()

	data := append(sc(minimalSPS), sc(minimalPPS)...)
	data = append(data, sc(idrNALU)...)
	frm := h264IFrame(data)

	out := avf.SplitFrame(frm)

	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 (SPS/PPS dropped, only IDR)", len(out))
	}

	if out[0].FrameType != avf.I_FRAME {
		t.Errorf("out[0].FrameType = %v, want I_FRAME", out[0].FrameType)
	}

	if !bytes.Equal(out[0].Data, sc(idrNALU)) {
		t.Errorf("out[0].Data = %v, want %v", out[0].Data, sc(idrNALU))
	}
}

// go test ./pkg/avf -run TestSplitFrame_AudioUnchanged -v
// AUDIO_FRAME is returned unchanged (SplitFrame is a no-op for audio).
func TestSplitFrame_AudioUnchanged(t *testing.T) {
	t.Parallel()

	frm := avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.AAC, FrameType: avf.AUDIO_FRAME},
		Data:       []byte{0xAA, 0xBB, 0xCC},
		DurationMs: 21,
	}

	out := avf.SplitFrame(frm)

	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}

	if !bytes.Equal(out[0].Data, frm.Data) {
		t.Errorf("audio data changed")
	}

	if out[0].DurationMs != 21 {
		t.Errorf("DurationMs = %d, want 21", out[0].DurationMs)
	}
}

// go test ./pkg/avf -run TestSplitFrame_DurationAssignment -v
// DurationMs is assigned only to the last output frame; all others get 0.
func TestSplitFrame_DurationAssignment(t *testing.T) {
	t.Parallel()

	data := append(sc(seiNALU), sc(idrNALU)...)
	frm := avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.I_FRAME},
		DurationMs: 33,
		Data:       data,
	}

	out := avf.SplitFrame(frm)

	if len(out) < 2 {
		t.Fatalf("expected at least 2 output frames, got %d", len(out))
	}

	for i := 0; i < len(out)-1; i++ {
		if out[i].DurationMs != 0 {
			t.Errorf("out[%d].DurationMs = %d, want 0", i, out[i].DurationMs)
		}
	}

	if out[len(out)-1].DurationMs != 33 {
		t.Errorf("last.DurationMs = %d, want 33", out[len(out)-1].DurationMs)
	}
}

// go test ./pkg/avf -run TestSplitFrame_ConnectHeaderUnchanged -v
// CONNECT_HEADER frames are returned unchanged regardless of content.
func TestSplitFrame_ConnectHeaderUnchanged(t *testing.T) {
	t.Parallel()

	frm := avf.Frame{
		BasicFrame: avf.BasicFrame{MediaType: avf.H264, FrameType: avf.CONNECT_HEADER},
		Data:       sc(minimalSPS),
	}

	out := avf.SplitFrame(frm)

	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}

	if out[0].FrameType != avf.CONNECT_HEADER {
		t.Errorf("FrameType changed for CONNECT_HEADER")
	}
}
