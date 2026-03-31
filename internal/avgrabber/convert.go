package avgrabber

import (
	"encoding/binary"

	"github.com/vtpl1/vrtc-sdk/av"
)

// avCodecType maps avgrabber codec constants to av.CodecType.
func avCodecType(f *Frame) av.CodecType {
	if f.MediaType == MediaAudio {
		switch f.CodecType {
		case CodecAAC:
			return av.AAC
		case CodecG711U:
			return av.PCM_MULAW
		case CodecG711A:
			return av.PCM_ALAW
		case CodecOpus:
			return av.OPUS
		default:
			return av.PCM
		}
	}

	switch f.CodecType {
	case CodecH264:
		return av.H264
	case CodecH265:
		return av.H265
	case CodecMJPEG:
		return av.MJPEG
	default:
		return av.UNKNOWN
	}
}

// splitAnnexB splits an Annex-B byte stream into raw NAL units (start codes removed).
// Handles both 3-byte (00 00 01) and 4-byte (00 00 00 01) start codes.
func splitAnnexB(data []byte) [][]byte {
	var nals [][]byte

	start, i := 0, 0

	for i < len(data) {
		scLen := annexBStartCodeLen(data, i)
		if scLen == 0 {
			i++

			continue
		}

		if i > start {
			nals = append(nals, data[start:i])
		}

		start = i + scLen
		i = start
	}

	if start < len(data) {
		nals = append(nals, data[start:])
	}

	return nals
}

// annexBStartCodeLen returns the length of the Annex-B start code at data[i],
// or 0 if there is no start code at that position.
func annexBStartCodeLen(data []byte, i int) int {
	if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
		return 4
	}

	if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
		return 3
	}

	return 0
}

// videoPayloadToAVCC converts a raw video frame payload to AVCC format
// (4-byte big-endian length prefix per NALU), stripping any embedded
// parameter-set NALUs (SPS/PPS for H.264; VPS/SPS/PPS for H.265).
//
// The input may be:
//   - Annex-B with a leading start code:  00 00 00 01 [NALUs...]
//   - Annex-B without a leading start code (leading start code already removed):
//     [NALU bytes] 00 00 00 01 [more NALUs...]
//   - A single raw NALU with no start codes at all
//
// In all cases the function returns well-formed AVCC that contains only
// slice NALUs (IDR or non-IDR), suitable for embedding in an fMP4 sample.
func videoPayloadToAVCC(data []byte, codecType uint8) []byte {
	nalus := splitAnnexB(data)

	if len(nalus) == 0 {
		// No start codes found: treat the entire payload as a single raw NALU.
		out := make([]byte, 4+len(data))
		binary.BigEndian.PutUint32(out, uint32(len(data)))
		copy(out[4:], data)

		return out
	}

	var buf []byte

	for _, nal := range nalus {
		if len(nal) == 0 || isParamSetNALU(nal, codecType) {
			continue
		}

		var sz [4]byte
		binary.BigEndian.PutUint32(sz[:], uint32(len(nal)))
		buf = append(buf, sz[:]...)
		buf = append(buf, nal...)
	}

	return buf
}

// isParamSetNALU reports whether nal is a parameter-set NAL unit that should
// be excluded from sample data (it belongs in the codec init segment).
//
//   - H.264: SPS (type 7), PPS (type 8)
//   - H.265: VPS (type 32), SPS (type 33), PPS (type 34)
func isParamSetNALU(nal []byte, codecType uint8) bool {
	if len(nal) == 0 {
		return false
	}

	switch codecType {
	case CodecH264:
		t := nal[0] & 0x1F

		return t == 7 || t == 8

	case CodecH265:
		t := (nal[0] & 0x7E) >> 1

		return t == 32 || t == 33 || t == 34
	}

	return false
}
