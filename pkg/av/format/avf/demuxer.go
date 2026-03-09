// Package avf implements a DemuxCloser for the AVF (Audio/Video Frame) container.
// The on-disk layout is specified in avf_spec.md at the repository root.
package avf

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	avcodec "github.com/vtpl1/vrtc/pkg/av/codec"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/mjpeg"
	"github.com/vtpl1/vrtc/pkg/av/codec/parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
)

type Option func(*Demuxer)

// Sentinel errors returned by the demuxer.
var (
	// ErrNoCodecFound is returned by GetCodecs when no decodable stream was found.
	ErrNoCodecFound = errors.New("avf: no decodable codec found in stream")
	// ErrFrameTooLarge is returned when a frame payload exceeds maxFrameSize.
	ErrFrameTooLarge = errors.New("avf: frame payload too large")
	// ErrBadMagic is returned when the frame magic bytes do not match "00dc".
	ErrBadMagic = errors.New("avf: bad frame magic")
)

// ── AVF format constants (§4 and §5 of the specification) ────────────────────

// MediaType field values (§4).
const (
	mediaTypeMJPG    = uint32(0)
	mediaTypeMPEG    = uint32(1)
	mediaTypeH264    = uint32(2)
	mediaTypeG711U   = uint32(3) // PCM µ-law (G.711 µ-law)
	mediaTypeG711A   = uint32(4) // PCM A-law (G.711 A-law)
	mediaTypeL16     = uint32(5) // PCM 16-bit linear
	mediaTypeAAC     = uint32(6)
	mediaTypeUnknown = uint32(7)
	mediaTypeH265    = uint32(8)
	mediaTypeG722    = uint32(9)
	mediaTypeG726    = uint32(10)
	mediaTypeOPUS    = uint32(11)
	mediaTypeMP2L2   = uint32(12)
)

// FrameType field values (§5).
const (
	frameTypeHFrame        = uint32(0)  // Non-reference / header frame
	frameTypeIFrame        = uint32(1)  // Keyframe
	frameTypePFrame        = uint32(2)  // Non-keyframe
	frameTypeConnectHeader = uint32(3)  // Codec parameter sets (SPS/PPS/VPS)
	frameTypeAudioFrame    = uint32(16) // Audio sample data
)

// ── Internal sizing constants (§8) ───────────────────────────────────────────

const (
	// frameHeaderSize: magic(4)+refFrameOff(8)+mediaType(4)+frameType(4)+timestamp(8)+frameSize(4).
	frameHeaderSize = 32
	// frameTrailerSize: currentFrameOff(8).
	frameTrailerSize = 8
	// maxFrameSize is the maximum allowed payload (3 MB).
	maxFrameSize = 3 * 1024 * 1024
	// videoProbeSize is the maximum number of frames to scan for a video codec, during this scan also scan for audio codec.
	videoProbeSize = 200 * 50
	// audioProbeSize is the maximum number of frames to scan for a audio codec, if the audio codec is not found during the video codec scan cycle.
	audioProbeSize = 4 * 50
	// readBufferSize is the internal bufio.Reader buffer size (2 MB).
	readBufferSize = 2 * 1024 * 1024
)

var avfMagic = [4]byte{'0', '0', 'd', 'c'}

// ── rawFrame ──────────────────────────────────────────────────────────────────

// rawFrame holds one unparsed AVF frame record.
type rawFrame struct {
	mediaType uint32
	frameType uint32
	timestamp int64 // milliseconds from the TimeStamp field
	data      []byte

	frameID int64
}

func (m *rawFrame) String() string {
	return fmt.Sprintf(
		"ID=%d Time=%dms Media=%d Frame=%d DataLen=%d",
		m.frameID,
		m.timestamp,
		m.mediaType,
		m.frameType,
		len(m.data),
	)
}

// ── Demuxer ───────────────────────────────────────────────────────────────────

// Demuxer reads the AVF container and implements av.DemuxCloser.
// Create with New or Open; call GetCodecs exactly once, then loop on ReadPacket.
type Demuxer struct {
	r  *bufio.Reader
	rc io.Closer // non-nil when the underlying reader also implements io.Closer

	// detected codec state
	videoCodec av.CodecData
	audioCodec av.CodecData

	// stream index assignment (set by GetCodecs)
	videoIdx uint16
	audioIdx uint16

	// stream list returned by GetCodecs
	streams []av.Stream

	// frames buffered during GetCodecs that have not yet been emitted
	buffered []rawFrame
	bufPos   int

	// pendingCodecChange is attached to the next emitted packet's NewCodecs field
	pendingCodecChange []av.Stream

	probed     bool // GetCodecs has run
	frameCount int64

	// lastVideoTS and lastAudioTS track the timestamp (ms) of the most recently
	// emitted video/audio packet so that packet Duration can be derived from the
	// difference between consecutive timestamps.
	lastVideoTS int64 // 0 = not yet set
	lastAudioTS int64 // 0 = not yet set

	disableAudio bool
}

// New creates a Demuxer that reads AVF data from r.
// If r implements io.Closer, Close will delegate to it.
func New(r io.Reader, opts ...Option) *Demuxer {
	d := &Demuxer{
		r: bufio.NewReaderSize(r, readBufferSize),
	}
	if rc, ok := r.(io.Closer); ok {
		d.rc = rc
	}

	for _, o := range opts {
		o(d)
	}

	return d
}

// Open opens the named AVF file and returns a ready Demuxer.
func Open(path string, opts ...Option) (*Demuxer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	return New(f, opts...), nil
}

func WithDisableAudio() Option {
	return func(d *Demuxer) {
		d.disableAudio = true
	}
}

// GetCodecs probes the stream to discover codec parameters and returns the
// stream list. It must be called exactly once before ReadPacket.
func (d *Demuxer) GetCodecs(_ context.Context) ([]av.Stream, error) {
	probeCount := 0
	connectHeader := make([]byte, 0)
	appendingToConnectHeader := false

	for probeCount < videoProbeSize {
		// Early exit once both streams are identified.
		if d.videoCodec != nil {
			break
		}

		frm, err := d.readFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		d.buffered = append(d.buffered, frm)
		probeCount++

		switch frm.frameType {
		case frameTypeAudioFrame:
			if !d.disableAudio && d.audioCodec == nil {
				d.audioCodec = parseAudioCodec(frm.mediaType, frm.data)
			}
		case frameTypeConnectHeader:
			connectHeader = append(connectHeader, frm.data...)
			appendingToConnectHeader = true
		default:
			if appendingToConnectHeader {
				appendingToConnectHeader = false
				d.videoCodec = parseVideoCodec(frm.mediaType, connectHeader)
			} else if frm.frameType == frameTypeIFrame {
				// MJPEG does not use CONNECT_HEADER; detect from first I_FRAME.
				if d.videoCodec == nil && frm.mediaType == mediaTypeMJPG {
					d.videoCodec = mjpeg.CodecData{}
				}
			}
		}
	}

	probeCount = 0
	for !d.disableAudio && probeCount < audioProbeSize {
		// Early exit once audio codec is identified.
		if d.audioCodec != nil {
			break
		}

		frm, err := d.readFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		d.buffered = append(d.buffered, frm)
		probeCount++

		switch frm.frameType {
		case frameTypeAudioFrame:
			if d.audioCodec == nil {
				d.audioCodec = parseAudioCodec(frm.mediaType, frm.data)
			}
		}
	}

	if d.videoCodec == nil && d.audioCodec == nil {
		return nil, ErrNoCodecFound
	}

	// Assign stream indices: video first, then audio.
	idx := uint16(0)
	if d.videoCodec != nil {
		d.videoIdx = idx
		d.streams = append(d.streams, av.Stream{Idx: idx, Codec: d.videoCodec})
		idx++
	}

	if !d.disableAudio && d.audioCodec != nil {
		d.audioIdx = idx
		d.streams = append(d.streams, av.Stream{Idx: idx, Codec: d.audioCodec})
	}

	d.probed = true

	return d.streams, nil
}

// ReadPacket returns the next av.Packet. It returns io.EOF when the stream ends.
// Packet.NewCodecs is non-nil when a mid-stream codec change was detected.
func (d *Demuxer) ReadPacket(ctx context.Context) (av.Packet, error) {
	for {
		if ctx.Err() != nil {
			return av.Packet{}, ctx.Err()
		}

		var frm rawFrame

		if d.bufPos < len(d.buffered) {
			frm = d.buffered[d.bufPos]
			d.bufPos++
		} else {
			var err error

			frm, err = d.readFrame()
			if err != nil {
				return av.Packet{}, err
			}
		}

		pkt, skip, err := d.frameToPacket(frm)
		if err != nil {
			return av.Packet{}, err
		}

		if skip {
			continue
		}

		if len(d.pendingCodecChange) > 0 {
			pkt.NewCodecs = d.pendingCodecChange
			d.pendingCodecChange = nil
		}

		return pkt, nil
	}
}

// Close closes the underlying reader if it implements io.Closer.
func (d *Demuxer) Close() error {
	if d.rc != nil {
		return d.rc.Close()
	}

	return nil
}

// ── Frame reading ─────────────────────────────────────────────────────────────

// readFrame reads one complete AVF frame record (header + payload + trailer).
func (d *Demuxer) readFrame() (rawFrame, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		return rawFrame{}, err
	}

	if hdr[0] != avfMagic[0] || hdr[1] != avfMagic[1] ||
		hdr[2] != avfMagic[2] || hdr[3] != avfMagic[3] {
		return rawFrame{}, fmt.Errorf("%w: got %q", ErrBadMagic, string(hdr[0:4]))
	}

	// refFrameOff at [4:12] — not needed for demuxing.
	mediaType := binary.BigEndian.Uint32(hdr[12:16])
	frameType := binary.BigEndian.Uint32(hdr[16:20])
	timestamp := int64(binary.BigEndian.Uint64(hdr[20:28]))
	frameSize := binary.BigEndian.Uint32(hdr[28:32])

	if frameSize > maxFrameSize {
		return rawFrame{}, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, frameSize)
	}

	data := make([]byte, frameSize)
	if _, err := io.ReadFull(d.r, data); err != nil {
		return rawFrame{}, err
	}

	// Discard the trailing CurrentFrameOff field.
	var trailer [frameTrailerSize]byte
	if _, err := io.ReadFull(d.r, trailer[:]); err != nil {
		return rawFrame{}, err
	}

	d.frameCount++

	return rawFrame{
		mediaType: mediaType,
		frameType: frameType,
		timestamp: timestamp,
		data:      data,
		frameID:   d.frameCount,
	}, nil
}

// ── Codec detection ───────────────────────────────────────────────────────────

func isVideoMediaType(mt uint32) bool {
	switch mt {
	case mediaTypeMJPG, mediaTypeMPEG, mediaTypeH264, mediaTypeH265:
		return true
	}

	return false
}

// parseVideoCodec builds codec data from a CONNECT_HEADER payload (Annex-B NALUs).
func parseVideoCodec(mediaType uint32, data []byte) av.CodecData {
	nalus, _ := parser.SplitNALUs(data)

	switch mediaType {
	case mediaTypeH264:
		var sps, pps []byte

		for _, nalu := range nalus {
			if len(nalu) == 0 {
				continue
			}

			if h264parser.IsSPSNALU(nalu) && sps == nil {
				sps = nalu
			} else if h264parser.IsPPSNALU(nalu) && pps == nil {
				pps = nalu
			}
		}

		if sps != nil && pps != nil {
			if c, err := h264parser.NewCodecDataFromSPSAndPPS(sps, pps); err == nil {
				return c
			}
		}

	case mediaTypeH265:
		var vps, sps, pps []byte

		for _, nalu := range nalus {
			if len(nalu) == 0 {
				continue
			}

			if h265parser.IsVPSNALU(nalu) && vps == nil {
				vps = nalu
			} else if h265parser.IsSPSNALU(nalu) && sps == nil {
				sps = nalu
			} else if h265parser.IsPPSNALU(nalu) && pps == nil {
				pps = nalu
			}
		}

		if vps != nil && sps != nil && pps != nil {
			if c, err := h265parser.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps); err == nil {
				return c
			}
		}
	}

	return nil
}

// parseAudioCodec infers audio codec data from the MediaType and the first
// audio frame payload (used to detect AAC parameters via ADTS header parsing).
func parseAudioCodec(mediaType uint32, data []byte) av.CodecData {
	switch mediaType {
	case mediaTypeG711U:
		return pcm.NewPCMMulawCodecData()

	case mediaTypeG711A:
		return pcm.NewPCMAlawCodecData()

	case mediaTypeOPUS:
		return avcodec.NewOpusCodecData(48000, av.ChStereo)

	case mediaTypeAAC:
		// Attempt ADTS header parse for sample rate / channel config.
		if len(data) >= 7 {
			if cfg, _, _, _, err := aacparser.ParseADTSHeader(data); err == nil {
				if c, err := aacparser.NewCodecDataFromMPEG4AudioConfig(cfg); err == nil {
					return c
				}
			}
		}

		// Fall back to AAC-LC 8 kHz mono.
		fallback := aacparser.MPEG4AudioConfig{
			ObjectType:      2,  // AAC-LC
			SampleRateIndex: 11, // 8000 Hz
			ChannelConfig:   1,  // mono (front-center)
		}
		fallback.Complete()

		if c, err := aacparser.NewCodecDataFromMPEG4AudioConfig(fallback); err == nil {
			return c
		}
	}

	return nil
}

// ── Packet assembly ───────────────────────────────────────────────────────────

// frameToPacket converts one rawFrame into an av.Packet.
// skip=true means the frame carries no decodable media (e.g. CONNECT_HEADER).
func (d *Demuxer) frameToPacket(frm rawFrame) (pkt av.Packet, skip bool, err error) {
	dts := time.Duration(frm.timestamp) * time.Millisecond

	switch frm.frameType {
	case frameTypeConnectHeader:
		// Codec parameter update — parse and queue a codec-change notification
		// so downstream decoders can re-initialise.
		if isVideoMediaType(frm.mediaType) && d.probed {
			if newCodec := parseVideoCodec(frm.mediaType, frm.data); newCodec != nil {
				d.videoCodec = newCodec
				d.rebuildStreams()
				d.pendingCodecChange = d.streams
			}
		}

		return av.Packet{}, true, nil

	case frameTypeIFrame, frameTypePFrame, frameTypeHFrame:
		if !isVideoMediaType(frm.mediaType) || d.videoCodec == nil {
			return av.Packet{}, true, nil
		}

		// Clamp to enforce strictly-increasing DTS. Rare camera clock glitches
		// can produce a backward step in the video timestamp; clamping to
		// lastVideoTS+1 keeps downstream consumers (fMP4, LL-HLS) valid.
		ts := frm.timestamp
		if d.lastVideoTS != 0 && ts <= d.lastVideoTS {
			ts = d.lastVideoTS + 1
		}

		var dur time.Duration
		if d.lastVideoTS != 0 {
			dur = time.Duration(ts-d.lastVideoTS) * time.Millisecond
		}

		d.lastVideoTS = ts

		return av.Packet{
			Idx:       d.videoIdx,
			KeyFrame:  frm.frameType == frameTypeIFrame,
			DTS:       time.Duration(ts) * time.Millisecond,
			Duration:  dur,
			CodecType: d.videoCodec.Type(),
			Data:      stripVideoPrefix(frm.data),
		}, false, nil

	case frameTypeAudioFrame:
		// Honour the disableAudio option: skip all audio frames including
		// late-detection ones so that audio packets are never emitted when
		// the caller has opted out of audio.
		if d.disableAudio {
			return av.Packet{}, true, nil
		}

		if d.audioCodec == nil {
			// Late audio codec detection for streams that had no audio during probe.
			if c := parseAudioCodec(frm.mediaType, frm.data); c != nil {
				d.audioCodec = c
				d.rebuildStreams()
				d.pendingCodecChange = d.streams
			} else {
				return av.Packet{}, true, nil
			}
		}

		data := frm.data

		// Strip ADTS header from AAC frames when present.
		if frm.mediaType == mediaTypeAAC && len(data) >= 7 &&
			data[0] == 0xFF && data[1]&0xF6 == 0xF0 {
			if _, hdrLen, _, _, adtsErr := aacparser.ParseADTSHeader(
				data,
			); adtsErr == nil &&
				hdrLen < len(data) {
				data = data[hdrLen:]
			}
		}

		var dur time.Duration
		if d.lastAudioTS != 0 && frm.timestamp > d.lastAudioTS {
			dur = time.Duration(frm.timestamp-d.lastAudioTS) * time.Millisecond
		}

		d.lastAudioTS = frm.timestamp

		return av.Packet{
			Idx:       d.audioIdx,
			DTS:       dts,
			Duration:  dur,
			CodecType: d.audioCodec.Type(),
			Data:      data,
		}, false, nil
	}

	return av.Packet{}, true, nil
}

// stripVideoPrefix removes the 4-byte length/start-code prefix from video
// I_FRAME and P_FRAME payloads as specified in §6.2.
func stripVideoPrefix(data []byte) []byte {
	if len(data) <= 4 {
		return data
	}

	return data[4:]
}

// rebuildStreams regenerates d.streams from the current codec state, preserving
// stream indices assigned during GetCodecs.
func (d *Demuxer) rebuildStreams() {
	d.streams = d.streams[:0]

	if d.videoCodec != nil {
		d.streams = append(d.streams, av.Stream{Idx: d.videoIdx, Codec: d.videoCodec})
	}

	if d.audioCodec != nil {
		d.streams = append(d.streams, av.Stream{Idx: d.audioIdx, Codec: d.audioCodec})
	}
}
