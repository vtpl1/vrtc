package av

import "fmt"

// MediaType field values (§4).
const (
	AVFMediaTypeMJPG    = uint32(0)
	AVFMediaTypeMPEG    = uint32(1)
	AVFMediaTypeH264    = uint32(2)
	AVFMediaTypeG711U   = uint32(3) // PCM µ-law (G.711 µ-law)
	AVFMediaTypeG711A   = uint32(4) // PCM A-law (G.711 A-law)
	AVFMediaTypeL16     = uint32(5) // PCM 16-bit linear
	AVFMediaTypeAAC     = uint32(6)
	AVFMediaTypeUnknown = uint32(7)
	AVFMediaTypeH265    = uint32(8)
	AVFMediaTypeG722    = uint32(9)
	AVFMediaTypeG726    = uint32(10)
	AVFMediaTypeOPUS    = uint32(11)
	AVFMediaTypeMP2L2   = uint32(12)
)

// FrameType field values (§5).
const (
	AVFFrameTypeHFrame        = uint32(0)  // Non-reference / header frame
	AVFFrameTypeIFrame        = uint32(1)  // Keyframe
	AVFFrameTypePFrame        = uint32(2)  // Non-keyframe
	AVFFrameTypeConnectHeader = uint32(3)  // Codec parameter sets (SPS/PPS/VPS)
	AVFFrameTypeAudioFrame    = uint32(16) // Audio sample data
)

// ── Frame ──────────────────────────────────────────────────────────────────

// AVFFrame holds one unparsed AVF frame record.
type AVFFrame struct {
	MediaType uint32
	FrameType uint32
	Timestamp int64 // milliseconds from the TimeStamp field
	Data      []byte

	FrameID   int64
	ExtraData []byte
}

func (m *AVFFrame) String() string {
	return fmt.Sprintf(
		"ID=%d Time=%dms Media=%d Frame=%d DataLen=%d",
		m.FrameID,
		m.Timestamp,
		m.MediaType,
		m.FrameType,
		len(m.Data),
	)
}
