package avgrabber

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/vtpl1/vrtc-sdk/av"
)

// ── splitAnnexB ─────────────────────────────────────────────────────────────

func TestSplitAnnexB_FourByteStartCodes(t *testing.T) {
	t.Parallel()

	// Two NALUs with 4-byte start codes.
	data := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, // SPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, // PPS
	}

	nals := splitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("want 2 NALUs, got %d", len(nals))
	}

	if nals[0][0] != 0x67 {
		t.Errorf("NAL 0: want SPS (0x67), got 0x%02X", nals[0][0])
	}

	if nals[1][0] != 0x68 {
		t.Errorf("NAL 1: want PPS (0x68), got 0x%02X", nals[1][0])
	}
}

func TestSplitAnnexB_ThreeByteStartCodes(t *testing.T) {
	t.Parallel()

	data := []byte{
		0x00, 0x00, 0x01, 0x65, 0xAA, // IDR
		0x00, 0x00, 0x01, 0x41, 0xBB, // non-IDR
	}

	nals := splitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("want 2 NALUs, got %d", len(nals))
	}

	if nals[0][0] != 0x65 {
		t.Errorf("NAL 0: want IDR (0x65), got 0x%02X", nals[0][0])
	}

	if nals[1][0] != 0x41 {
		t.Errorf("NAL 1: want non-IDR (0x41), got 0x%02X", nals[1][0])
	}
}

func TestSplitAnnexB_MixedStartCodes(t *testing.T) {
	t.Parallel()

	// 4-byte + 3-byte mixed.
	data := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, // 4-byte SPS
		0x00, 0x00, 0x01, 0x68, 0xCE, // 3-byte PPS
	}

	nals := splitAnnexB(data)
	if len(nals) != 2 {
		t.Fatalf("want 2 NALUs, got %d", len(nals))
	}
}

func TestSplitAnnexB_NoStartCodes(t *testing.T) {
	t.Parallel()

	// Raw NALU without any start code — the trailing bytes after start=0
	// are returned as a single entry (start < len(data) path).
	data := []byte{0x65, 0xAA, 0xBB, 0xCC}

	nals := splitAnnexB(data)
	if len(nals) != 1 {
		t.Fatalf("want 1 NALU for raw data, got %d", len(nals))
	}

	if !bytes.Equal(nals[0], data) {
		t.Errorf("data mismatch: got %v", nals[0])
	}
}

func TestSplitAnnexB_Empty(t *testing.T) {
	t.Parallel()

	nals := splitAnnexB(nil)
	if len(nals) != 0 {
		t.Errorf("want 0 NALUs for nil, got %d", len(nals))
	}

	nals = splitAnnexB([]byte{})
	if len(nals) != 0 {
		t.Errorf("want 0 NALUs for empty, got %d", len(nals))
	}
}

func TestSplitAnnexB_SingleNALU(t *testing.T) {
	t.Parallel()

	data := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xDE, 0xAD}

	nals := splitAnnexB(data)
	if len(nals) != 1 {
		t.Fatalf("want 1 NALU, got %d", len(nals))
	}

	if !bytes.Equal(nals[0], []byte{0x65, 0xDE, 0xAD}) {
		t.Errorf("NAL data mismatch: %v", nals[0])
	}
}

func TestSplitAnnexB_ThreeNALUs_H265(t *testing.T) {
	t.Parallel()

	// VPS + SPS + PPS for H.265 (NAL types 32, 33, 34).
	vps := []byte{0x40, 0x01, 0x0C} // (32 << 1) | 0 = 0x40
	sps := []byte{0x42, 0x01, 0x01} // (33 << 1) | 0 = 0x42
	pps := []byte{0x44, 0x01}       // (34 << 1) | 0 = 0x44

	var data []byte

	sc := []byte{0x00, 0x00, 0x00, 0x01}
	data = append(data, sc...)
	data = append(data, vps...)
	data = append(data, sc...)
	data = append(data, sps...)
	data = append(data, sc...)
	data = append(data, pps...)

	nals := splitAnnexB(data)
	if len(nals) != 3 {
		t.Fatalf("want 3 NALUs, got %d", len(nals))
	}
}

// ── isParamSetNALU ──────────────────────────────────────────────────────────

func TestIsParamSetNALU_H264(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		nalu byte
		want bool
	}{
		{"SPS", 0x67, true},          // type 7
		{"PPS", 0x68, true},          // type 8
		{"IDR", 0x65, false},         // type 5
		{"non-IDR", 0x41, false},     // type 1
		{"SEI", 0x06, false},         // type 6
		{"SPS_with_NRI", 0x27, true}, // type 7 with nal_ref_idc=1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := isParamSetNALU([]byte{tt.nalu}, CodecH264)
			if got != tt.want {
				t.Errorf("isParamSetNALU(0x%02X, H264) = %v, want %v", tt.nalu, got, tt.want)
			}
		})
	}
}

func TestIsParamSetNALU_H265(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		nalu byte
		want bool
	}{
		{"VPS", 0x40, true},         // (32 << 1) = 0x40
		{"SPS", 0x42, true},         // (33 << 1) = 0x42
		{"PPS", 0x44, true},         // (34 << 1) = 0x44
		{"IDR_W_RADL", 0x26, false}, // (19 << 1) = 0x26
		{"TRAIL_R", 0x02, false},    // (1 << 1)  = 0x02
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := isParamSetNALU([]byte{tt.nalu}, CodecH265)
			if got != tt.want {
				t.Errorf("isParamSetNALU(0x%02X, H265) = %v, want %v", tt.nalu, got, tt.want)
			}
		})
	}
}

func TestIsParamSetNALU_EmptyNAL(t *testing.T) {
	t.Parallel()

	if isParamSetNALU(nil, CodecH264) {
		t.Error("nil should not be a param set")
	}

	if isParamSetNALU([]byte{}, CodecH264) {
		t.Error("empty should not be a param set")
	}
}

func TestIsParamSetNALU_UnknownCodec(t *testing.T) {
	t.Parallel()

	// Unknown codec type should never report param set.
	if isParamSetNALU([]byte{0x67}, CodecMJPEG) {
		t.Error("MJPEG should not have param sets")
	}
}

// ── videoPayloadToAVCC ──────────────────────────────────────────────────────

func TestVideoPayloadToAVCC_SingleNALU_AnnexB(t *testing.T) {
	t.Parallel()

	// Single IDR NALU in Annex-B.
	data := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xDE, 0xAD}

	avcc := videoPayloadToAVCC(data, CodecH264)

	// Expected: 4-byte length (3) + IDR bytes.
	if len(avcc) != 7 {
		t.Fatalf("want 7 bytes, got %d", len(avcc))
	}

	naluLen := binary.BigEndian.Uint32(avcc[:4])
	if naluLen != 3 {
		t.Errorf("NALU length = %d, want 3", naluLen)
	}

	if !bytes.Equal(avcc[4:], []byte{0x65, 0xDE, 0xAD}) {
		t.Errorf("NALU data mismatch: %v", avcc[4:])
	}
}

func TestVideoPayloadToAVCC_RawNALU_NoStartCode(t *testing.T) {
	t.Parallel()

	// Raw NALU without start code — should be wrapped in AVCC.
	data := []byte{0x65, 0xDE, 0xAD, 0xBE, 0xEF}

	avcc := videoPayloadToAVCC(data, CodecH264)

	if len(avcc) != 9 {
		t.Fatalf("want 9 bytes, got %d", len(avcc))
	}

	naluLen := binary.BigEndian.Uint32(avcc[:4])
	if naluLen != 5 {
		t.Errorf("NALU length = %d, want 5", naluLen)
	}

	if !bytes.Equal(avcc[4:], data) {
		t.Errorf("data mismatch")
	}
}

func TestVideoPayloadToAVCC_StripsParamSets_H264(t *testing.T) {
	t.Parallel()

	// SPS + PPS + IDR in Annex-B. Only IDR should survive.
	sc := []byte{0x00, 0x00, 0x00, 0x01}

	var data []byte

	data = append(data, sc...)
	data = append(data, 0x67, 0x42, 0x00) // SPS
	data = append(data, sc...)
	data = append(data, 0x68, 0xCE) // PPS
	data = append(data, sc...)
	data = append(data, 0x65, 0xDE, 0xAD) // IDR

	avcc := videoPayloadToAVCC(data, CodecH264)

	// Only the IDR should remain: 4-byte length + 3 bytes.
	if len(avcc) != 7 {
		t.Fatalf("want 7 bytes (IDR only), got %d", len(avcc))
	}

	if avcc[4] != 0x65 {
		t.Errorf("first NALU byte = 0x%02X, want 0x65 (IDR)", avcc[4])
	}
}

func TestVideoPayloadToAVCC_StripsParamSets_H265(t *testing.T) {
	t.Parallel()

	sc := []byte{0x00, 0x00, 0x00, 0x01}

	var data []byte

	data = append(data, sc...)
	data = append(data, 0x40, 0x01) // VPS (type 32)
	data = append(data, sc...)
	data = append(data, 0x42, 0x01) // SPS (type 33)
	data = append(data, sc...)
	data = append(data, 0x44, 0x01) // PPS (type 34)
	data = append(data, sc...)
	data = append(data, 0x26, 0xFF, 0xEE) // IDR_W_RADL (type 19)

	avcc := videoPayloadToAVCC(data, CodecH265)

	// Only IDR should remain.
	if len(avcc) != 7 {
		t.Fatalf("want 7 bytes (IDR only), got %d", len(avcc))
	}

	if avcc[4] != 0x26 {
		t.Errorf("first NALU byte = 0x%02X, want 0x26 (IDR_W_RADL)", avcc[4])
	}
}

func TestVideoPayloadToAVCC_MultiNALU_AccessUnit(t *testing.T) {
	t.Parallel()

	// SEI + IDR — both should survive (SEI is not a param set).
	sc := []byte{0x00, 0x00, 0x00, 0x01}

	var data []byte

	data = append(data, sc...)
	data = append(data, 0x06, 0x05, 0x01) // SEI (type 6)
	data = append(data, sc...)
	data = append(data, 0x65, 0xDE, 0xAD) // IDR (type 5)

	avcc := videoPayloadToAVCC(data, CodecH264)

	// Two NALUs: SEI (3+4=7 bytes) + IDR (3+4=7 bytes) = 14 bytes total.
	if len(avcc) != 14 {
		t.Fatalf("want 14 bytes (SEI + IDR), got %d", len(avcc))
	}

	// First NALU: SEI.
	len1 := binary.BigEndian.Uint32(avcc[:4])
	if len1 != 3 {
		t.Errorf("SEI NALU length = %d, want 3", len1)
	}

	if avcc[4] != 0x06 {
		t.Errorf("first NALU = 0x%02X, want SEI (0x06)", avcc[4])
	}

	// Second NALU: IDR.
	len2 := binary.BigEndian.Uint32(avcc[7:11])
	if len2 != 3 {
		t.Errorf("IDR NALU length = %d, want 3", len2)
	}

	if avcc[11] != 0x65 {
		t.Errorf("second NALU = 0x%02X, want IDR (0x65)", avcc[11])
	}
}

// ── avCodecType ─────────────────────────────────────────────────────────────

func TestAvCodecType_Video(t *testing.T) {
	t.Parallel()

	tests := []struct {
		codec uint8
		want  av.CodecType
	}{
		{CodecH264, av.H264},
		{CodecH265, av.H265},
		{CodecMJPEG, av.MJPEG},
		{CodecUnknown, av.UNKNOWN},
	}

	for _, tt := range tests {
		f := &Frame{MediaType: MediaVideo, CodecType: tt.codec}
		got := avCodecType(f)

		if got != tt.want {
			t.Errorf("avCodecType(video, %d) = %v, want %v", tt.codec, got, tt.want)
		}
	}
}

func TestAvCodecType_Audio(t *testing.T) {
	t.Parallel()

	tests := []struct {
		codec uint8
		want  av.CodecType
	}{
		{CodecAAC, av.AAC},
		{CodecG711U, av.PCM_MULAW},
		{CodecG711A, av.PCM_ALAW},
		{CodecOpus, av.OPUS},
		{CodecL16, av.PCM},     // fallback to PCM
		{CodecG722, av.PCM},    // fallback to PCM
		{CodecG726, av.PCM},    // fallback to PCM
		{CodecUnknown, av.PCM}, // fallback to PCM
	}

	for _, tt := range tests {
		f := &Frame{MediaType: MediaAudio, CodecType: tt.codec}
		got := avCodecType(f)

		if got != tt.want {
			t.Errorf("avCodecType(audio, %d) = %v, want %v", tt.codec, got, tt.want)
		}
	}
}
