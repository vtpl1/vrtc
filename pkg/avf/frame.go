package avf

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
)

type (
	MediaType uint32
	FrameType uint32
)

// ── MediaType constants ───────────────────────────────────────────────────────
// See docs/avf-frame-spec.md §3 and docs/avf-wire-format-spec.md §4.

const (
	MJPG       MediaType = 0
	MPEG       MediaType = 1
	H264       MediaType = 2
	MLAW       MediaType = 3
	PCMU       MediaType = 3
	PCM_MU_LAW MediaType = 3
	G711       MediaType = 3
	G711U      MediaType = 3
	ALAW       MediaType = 4
	PCMA       MediaType = 4
	PCM_ALAW   MediaType = 4
	G711A      MediaType = 4
	L16        MediaType = 5
	ACC        MediaType = 6 // Deprecated alias; use AAC.
	AAC        MediaType = 6
	UNKNOWN    MediaType = 7
	H265       MediaType = 8
	G722       MediaType = 9
	G726       MediaType = 10
	OPUS       MediaType = 11
	MP2L2      MediaType = 12
)

// ── FrameType constants ───────────────────────────────────────────────────────
// See docs/avf-frame-spec.md §4 and docs/avf-wire-format-spec.md §5.

const (
	// NON_REF_FRAME is a generic video frame used by camera firmware that does
	// not signal I/P frame distinction. Also covers H.264/H.265 auxiliary NALUs
	// (SEI, AUD, filler). Demuxer emits it as a non-keyframe video packet.
	NON_REF_FRAME FrameType = 0

	// I_FRAME is an intra-coded keyframe: IDR for H.264/H.265, every frame for MJPEG.
	I_FRAME FrameType = 1

	// P_FRAME is an inter-coded frame covering all non-IDR decodable video NALUs
	// including B-frames.
	P_FRAME FrameType = 2

	// CONNECT_HEADER carries exactly one codec parameter set NALU in Annex-B
	// format. Never emitted as an av.Packet; triggers codec detection/change.
	// H.264 keyframe group: CONNECT_HEADER(SPS), CONNECT_HEADER(PPS), I_FRAME.
	// H.265 keyframe group: CONNECT_HEADER(VPS), CONNECT_HEADER(SPS), CONNECT_HEADER(PPS), I_FRAME.
	CONNECT_HEADER FrameType = 3

	// Values 4–15 are reserved. Readers must skip them silently.

	// AUDIO_FRAME carries raw encoded audio samples with no additional framing.
	AUDIO_FRAME FrameType = 16

	// UNKNOWN_FRAME is an unrecognized frame type. Readers skip it silently.
	// It does NOT terminate a CONNECT_HEADER accumulation sequence.
	UNKNOWN_FRAME FrameType = 17
)

// H_FRAME is a deprecated alias for NON_REF_FRAME kept for transition.
// All new code must use NON_REF_FRAME.
const H_FRAME = NON_REF_FRAME

// ── Analytics types ───────────────────────────────────────────────────────────

// ObjectInfo represents a single detected object within a video frame.
type ObjectInfo struct {
	X uint32 `bson:"x" json:"x"`
	Y uint32 `bson:"y" json:"y"`
	W uint32 `bson:"w" json:"w"`
	H uint32 `bson:"h" json:"h"`
	T uint32 `bson:"t" json:"t"`
	C uint32 `bson:"c" json:"c"`
	I int64  `bson:"i" json:"i"`
	E bool   `bson:"e" json:"e"`
}

// PVAData carries object-detection analytics associated with a frame.
// Stored in av.Packet.Metadata in the AVF pipeline path.
type PVAData struct {
	SiteID           int32        `bson:"siteId"           json:"siteId"`
	ChannelID        int32        `bson:"channelId"        json:"channelId"`
	StartTimestamp   int64        `bson:"timeStamp"        json:"timeStamp"`
	EndTimestamp     int64        `bson:"timeStampEnd"     json:"timeStampEnd"`
	EncodedTimestamp int64        `bson:"timeStampEncoded" json:"timeStampEncoded"`
	FrameID          uint64       `bson:"frameId"          json:"frameId"`
	VehicleCount     int32        `bson:"vehicleCount"     json:"vehicleCount"`
	PeopleCount      int32        `bson:"peopleCount"      json:"peopleCount"`
	RefWidth         int32        `bson:"refWidth"         json:"refWidth"`
	RefHeight        int32        `bson:"refHeight"        json:"refHeight"`
	ObjectList       []ObjectInfo `bson:"objectList"       json:"objectList,omitempty"`
}

func (obj PVAData) String() string {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return ""
	}

	return string(jsonBytes)
}

// ── Frame struct ──────────────────────────────────────────────────────────────

// BasicFrame holds the three fields present in every AVF frame header.
// FrameSize is intentionally absent — it is a wire-only length prefix;
// use len(Frame.Data) at runtime.
// See docs/avf-frame-spec.md §2.
type BasicFrame struct {
	MediaType MediaType
	FrameType FrameType
	TimeStamp int64 // presentation timestamp, milliseconds
}

// StreamMeta carries optional per-frame metadata supplied by the stream source
// (e.g. received over a network transport). All fields are zero when the frame
// was decoded from an AVF file on disk.
type StreamMeta struct {
	Bitrate         int32
	Fps             int32
	MotionAvailable int8
}

// Frame is the in-memory representation of one decoded AVF frame record.
// Wire-only fields (Magic, RefFrameOff, FrameSize, CurrentFrameOff) are
// consumed during decode and are not retained here.
// See docs/avf-frame-spec.md for the full specification.
type Frame struct {
	BasicFrame

	// FrameID is a stable identity assigned by the source device or stream.
	// 0 means the source did not assign a value.
	FrameID int64

	// DurationMs is the frame's presentation duration in milliseconds.
	// Computed by the consumer (demuxer or proxy). 0 until set.
	DurationMs int64

	// Data is the decoded frame payload. Format by FrameType:
	//   CONNECT_HEADER:            \x00\x00\x00\x01 + single parameter set NALU
	//   I_FRAME / P_FRAME / NON_REF_FRAME: \x00\x00\x00\x01 + NALU bytes
	//   AUDIO_FRAME:               raw encoded audio samples
	Data []byte

	// StreamMeta carries stream-source metadata. Zero when frame is from file.
	StreamMeta StreamMeta

	// Pvadata carries object-detection analytics. Zero if not available.
	Pvadata PVAData
}

// ── Frame methods ─────────────────────────────────────────────────────────────

// CodecType returns the av.CodecType corresponding to this frame's MediaType.
func (m *Frame) CodecType() av.CodecType {
	switch m.MediaType {
	case MJPG:
		return av.MJPEG
	case MPEG:
		return av.UNKNOWN
	case H264:
		return av.H264
	case PCM_MU_LAW: // also G711U, MLAW, PCM, G711
		return av.PCM_MULAW
	case PCM_ALAW: // also ALAW, PCMA, G711A
		return av.PCM_ALAW
	case L16:
		return av.PCML
	case AAC: // also ACC
		return av.AAC
	case UNKNOWN:
		return av.UNKNOWN
	case H265:
		return av.H265
	case G722:
		return av.UNKNOWN
	case G726:
		return av.UNKNOWN
	case OPUS:
		return av.OPUS
	case MP2L2:
		return av.MP3
	}

	return av.UNKNOWN
}

// IsAudio reports whether this frame carries audio data.
func (m *Frame) IsAudio() bool {
	switch m.MediaType {
	case PCM_MU_LAW, PCM_ALAW, L16, AAC, G722, G726, OPUS, MP2L2:
		return true
	default:
		return false
	}
}

// IsKeyFrame reports whether this frame is an intra-coded keyframe.
func (m *Frame) IsKeyFrame() bool {
	return m.FrameType == I_FRAME
}

// IsDataNALU reports whether this frame carries decodable video data.
// True for I_FRAME, P_FRAME, and NON_REF_FRAME.
func (m *Frame) IsDataNALU() bool {
	return m.FrameType == I_FRAME || m.FrameType == P_FRAME || m.FrameType == NON_REF_FRAME
}

// IsVideo reports whether this frame carries video data.
func (m *Frame) IsVideo() bool {
	switch m.MediaType {
	case MJPG, MPEG, H264, H265:
		return true
	default:
		return false
	}
}

// String returns a compact human-readable description of the frame for logging.
func (m *Frame) String() string {
	var naluStr string

	switch {
	case m.FrameType == AUDIO_FRAME:
		naluStr = "AUDIO"
	case len(m.Data) > 4:
		// Data[0:4] is the Annex-B start code; NALU header is at Data[4].
		switch m.MediaType {
		case H265:
			nalu := av.H265NaluType(m.Data[4]>>1) & av.H265NALTypeMask
			naluStr = nalu.String()
		case H264:
			nalu := av.H264NaluType(m.Data[4]) & av.H264NALTypeMask
			naluStr = nalu.String()
		}
	default:
		naluStr = "EMPTY"
	}

	return fmt.Sprintf(
		"ID=%d Time=%dms Media=%s NALU=%s Duration=%d ms DataLen=%d",
		m.FrameID,
		m.TimeStamp,
		m.CodecType().String(),
		naluStr,
		m.DurationMs,
		len(m.Data),
	)
}

// ── Codec mapping ─────────────────────────────────────────────────────────────

// MediaTypeFromCodec maps an av.CodecType to the corresponding AVF MediaType.
// Returns UNKNOWN for codec types not represented in the AVF format.
func MediaTypeFromCodec(codec av.CodecType) MediaType {
	switch codec {
	case av.H264:
		return H264
	case av.H265:
		return H265
	case av.JPEG, av.MJPEG:
		return MJPG
	case av.AAC:
		return AAC
	case av.PCM_MULAW:
		return PCM_MU_LAW
	case av.PCM_ALAW:
		return PCM_ALAW
	case av.PCM, av.PCML:
		return L16
	case av.OPUS:
		return OPUS
	case av.MP3:
		return MP2L2
	}

	return UNKNOWN
}

// FrameTypeFromPktData determines the AVF FrameType for a raw packet payload.
// data must be raw NALU bytes (no start code, no AVCC length prefix).
// Returns UNKNOWN_FRAME for codec types not present in the switch.
// See docs/avf-frame-spec.md §4, rule R3.
func FrameTypeFromPktData(data []byte, codec av.CodecType) FrameType {
	switch codec {
	case av.H264:
		if h264parser.IsKeyFrame(data) {
			return I_FRAME
		}

		if h264parser.IsSPSNALU(data) || h264parser.IsPPSNALU(data) {
			return CONNECT_HEADER
		}

		if h264parser.IsDataNALU(data) {
			return P_FRAME
		}

		// Auxiliary NALUs (SEI, AUD, filler, end-of-sequence, etc.)
		return NON_REF_FRAME

	case av.H265:
		if h265parser.IsKeyFrame(data) {
			return I_FRAME
		}

		if h265parser.IsVPSNALU(data) || h265parser.IsSPSNALU(data) || h265parser.IsPPSNALU(data) {
			return CONNECT_HEADER
		}

		if h265parser.IsDataNALU(data) {
			return P_FRAME
		}

		// Auxiliary NALUs (SEI, AUD, filler, etc.)
		return NON_REF_FRAME

	case av.JPEG, av.MJPEG:
		return I_FRAME

	case av.AAC, av.PCM_MULAW, av.PCM_ALAW, av.SPEEX, av.PCM, av.PCML, av.OPUS, av.MP3,
		av.NELLYMOSER, av.ELD, av.FLAC:
		return AUDIO_FRAME
	}

	return UNKNOWN_FRAME
}

// ── Conversion: Frame → Packet ────────────────────────────────────────────────

// FrameToPacket converts one media Frame to an av.Packet.
//
// Returns (pkt, true) when the frame produces a packet.
// Returns (zero, false) for CONNECT_HEADER, UNKNOWN_FRAME, and reserved
// FrameType values (4–15) — the caller must skip these.
//
// idx is supplied by the caller; the demuxer or proxy owns stream index
// assignment. codec is used to derive a nominal Duration when DurationMs == 0.
// NewCodecs is not set here; the caller attaches it when a codec change is
// detected after accumulating CONNECT_HEADER frames.
//
// See docs/frame-packet-conversion-spec.md §2.
func FrameToPacket(frm *Frame, idx uint16, codec av.CodecData) (av.Packet, bool) {
	switch frm.FrameType {
	case CONNECT_HEADER, UNKNOWN_FRAME:
		return av.Packet{}, false

	case I_FRAME, P_FRAME, NON_REF_FRAME:
		if !frm.IsVideo() {
			return av.Packet{}, false
		}

		data := frm.Data
		if len(data) > 4 {
			data = data[4:] // strip 4-byte Annex-B start code → raw NALU
		}

		return av.Packet{
			Idx:           idx,
			KeyFrame:      frm.FrameType == I_FRAME,
			CodecType:     frm.CodecType(),
			DTS:           time.Duration(frm.TimeStamp) * time.Millisecond,
			WallClockTime: time.UnixMilli(frm.TimeStamp),
			Duration:      deriveDuration(frm.DurationMs, codec, nil),
			FrameID:       frm.FrameID,
			Data:          data,
			Metadata:      frm.Pvadata,
		}, true

	case AUDIO_FRAME:
		data := frm.Data

		// Strip ADTS header from AAC frames when present.
		if frm.MediaType == AAC && len(data) >= 7 &&
			data[0] == 0xFF && data[1]&0xF6 == 0xF0 {
			if _, hdrLen, _, _, err := aacparser.ParseADTSHeader(data); err == nil && hdrLen < len(data) {
				data = data[hdrLen:]
			}
		}

		return av.Packet{
			Idx:           idx,
			KeyFrame:      false,
			CodecType:     frm.CodecType(),
			DTS:           time.Duration(frm.TimeStamp) * time.Millisecond,
			WallClockTime: time.UnixMilli(frm.TimeStamp),
			Duration:      deriveDuration(frm.DurationMs, codec, data),
			FrameID:       frm.FrameID,
			Data:          data,
			Metadata:      frm.Pvadata,
		}, true
	}

	// Reserved values 4–15 and any other unrecognised types.
	return av.Packet{}, false
}

// deriveDuration returns frm.DurationMs as a Duration when non-zero.
// Otherwise it falls back to codec-derived nominal duration:
//   - Audio: AudioCodecData.PacketDuration(data)
//   - Video: 33 ms (≈ 30 fps) until av.VideoCodecData exposes SPS timing fields
func deriveDuration(durationMs int64, codec av.CodecData, audioData []byte) time.Duration {
	if durationMs != 0 {
		return time.Duration(durationMs) * time.Millisecond
	}

	if codec == nil {
		return 0
	}

	if ac, ok := codec.(av.AudioCodecData); ok {
		if d, err := ac.PacketDuration(audioData); err == nil && d > 0 {
			return d
		}
	}

	// TODO: extract frame rate from SPS via h264parser/h265parser once
	// av.VideoCodecData exposes num_units_in_tick / time_scale.
	if codec.Type().IsVideo() {
		return 33 * time.Millisecond
	}

	return 0
}

// ── Conversion: Packet → Frames ───────────────────────────────────────────────

// PacketToFrames converts one av.Packet to the sequence of avf.Frame records
// it produces on the wire.
//
// For an H.264/H.265 keyframe this emits one CONNECT_HEADER per parameter set
// (SPS, PPS for H.264; VPS, SPS, PPS for H.265) followed by an I_FRAME.
// For all other packets it emits exactly one frame.
// Returns an empty slice for packets that cannot be converted.
//
// codec must be non-nil for H.264/H.265 keyframes.
// When pkt.NewCodecs is non-nil the caller must pass the updated codec.
// See docs/frame-packet-conversion-spec.md §3.
func PacketToFrames(pkt av.Packet, codec av.CodecData) []Frame {
	mt := MediaTypeFromCodec(pkt.CodecType)
	tsMs := pkt.DTS.Milliseconds()
	durMs := pkt.Duration.Milliseconds()

	var pvadata PVAData
	if pva, ok := pkt.Metadata.(PVAData); ok {
		pvadata = pva
	}

	// H.264 / H.265 keyframe: emit CONNECT_HEADERs then I_FRAME.
	if pkt.KeyFrame && (pkt.CodecType == av.H264 || pkt.CodecType == av.H265) {
		headers := buildConnectHeaderFrames(mt, tsMs, codec)
		if len(headers) == 0 {
			return nil
		}

		iframe := Frame{
			BasicFrame: BasicFrame{
				MediaType: mt,
				FrameType: I_FRAME,
				TimeStamp: tsMs,
			},
			FrameID:    pkt.FrameID,
			DurationMs: durMs,
			Data:       prependStartCode(pkt.Data),
			Pvadata:    pvadata,
		}

		return append(headers, iframe)
	}

	// All other cases: exactly one frame.
	var ft FrameType
	var data []byte

	switch {
	case pkt.KeyFrame:
		// MJPEG and any other always-keyframe codec: no CONNECT_HEADERs.
		ft = I_FRAME
		data = pkt.Data

	case pkt.CodecType.IsVideo():
		ft = P_FRAME
		data = prependStartCode(pkt.Data)

	default:
		ft = AUDIO_FRAME
		data = pkt.Data
	}

	return []Frame{{
		BasicFrame: BasicFrame{
			MediaType: mt,
			FrameType: ft,
			TimeStamp: tsMs,
		},
		FrameID:    pkt.FrameID,
		DurationMs: durMs,
		Data:       data,
		Pvadata:    pvadata,
	}}
}

// buildConnectHeaderFrames returns one CONNECT_HEADER Frame per parameter set
// NALU (SPS+PPS for H.264; VPS+SPS+PPS for H.265), each in Annex-B format.
// Returns nil when codec is not an H.264 or H.265 CodecData.
func buildConnectHeaderFrames(mt MediaType, tsMs int64, codec av.CodecData) []Frame {
	var nalus [][]byte

	switch c := codec.(type) {
	case h264parser.CodecData:
		nalus = [][]byte{c.SPS(), c.PPS()}
	case h265parser.CodecData:
		nalus = [][]byte{c.VPS(), c.SPS(), c.PPS()}
	default:
		return nil
	}

	frames := make([]Frame, 0, len(nalus))

	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}

		frames = append(frames, Frame{
			BasicFrame: BasicFrame{
				MediaType: mt,
				FrameType: CONNECT_HEADER,
				TimeStamp: tsMs,
			},
			Data: prependStartCode(nalu),
		})
	}

	return frames
}

// prependStartCode returns a new slice with the 4-byte Annex-B start code
// (\x00\x00\x00\x01) prepended to data.
func prependStartCode(data []byte) []byte {
	out := make([]byte, 4+len(data))
	out[0], out[1], out[2], out[3] = 0x00, 0x00, 0x00, 0x01
	copy(out[4:], data)

	return out
}

// ── Deprecated conversion functions ──────────────────────────────────────────
// These remain only to keep existing callers compiling during migration.
// Replace all call sites with FrameToPacket / PacketToFrames, then remove.

// FrameToAVPacket is deprecated. Use FrameToPacket instead.
func FrameToAVPacket(frame *Frame) *av.Packet {
	var (
		data []byte
		idx  uint16
	)

	if frame.FrameType == AUDIO_FRAME {
		idx = 1
		data = frame.Data
	} else if len(frame.Data) > 4 {
		data = frame.Data[4:]
	} else {
		data = frame.Data
	}

	return &av.Packet{
		KeyFrame:      frame.FrameType == I_FRAME,
		Idx:           idx,
		DTS:           time.Duration(frame.TimeStamp) * time.Millisecond,
		WallClockTime: time.UnixMilli(frame.TimeStamp),
		Duration:      time.Duration(frame.DurationMs) * time.Millisecond,
		Data:          data,
		Metadata:      frame.Pvadata,
		FrameID:       frame.FrameID,
		CodecType:     frame.CodecType(),
	}
}

// AVPacketToFrame is deprecated. Use PacketToFrames instead.
func AVPacketToFrame(pkt *av.Packet) *Frame {
	data := pkt.Data

	if pkt.CodecType == av.H264 || pkt.CodecType == av.H265 {
		data = prependStartCode(pkt.Data)
	}

	var pvadata PVAData

	if pva, ok := pkt.Metadata.(PVAData); ok {
		pvadata = pva
	} else {
		pvadata = PVAData{
			StartTimestamp: pkt.WallClockTime.UnixMilli(),
			EndTimestamp:   pkt.WallClockTime.UnixMilli(),
		}
	}

	return &Frame{
		BasicFrame: BasicFrame{
			MediaType: MediaTypeFromCodec(pkt.CodecType),
			FrameType: FrameTypeFromPktData(pkt.Data, pkt.CodecType),
			TimeStamp: pkt.DTS.Milliseconds(),
		},
		FrameID:    pkt.FrameID,
		DurationMs: pkt.Duration.Milliseconds(),
		Data:       data,
		Pvadata:    pvadata,
	}
}
