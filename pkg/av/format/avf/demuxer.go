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
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/mjpeg"
	"github.com/vtpl1/vrtc/pkg/avf"
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
	buffered []avf.Frame
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

	// connectHeaderBuf accumulates Annex-B CONNECT_HEADER data after probe.
	// Each CONNECT_HEADER frame's Data is appended here; when the next video
	// data frame arrives the buffer is parsed to detect a codec change.
	connectHeaderBuf []byte

	// pendingNALUs holds packets that were split from a multi-NALU frame and
	// have not yet been returned by ReadPacket.
	pendingNALUs []av.Packet

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

		switch frm.FrameType {
		case avf.AUDIO_FRAME:
			if !d.disableAudio && d.audioCodec == nil {
				d.audioCodec = parseAudioCodec(frm.MediaType, frm.Data)
			}
		case avf.CONNECT_HEADER:
			connectHeader = append(connectHeader, frm.Data...)
			appendingToConnectHeader = true
		case avf.NON_REF_FRAME, avf.I_FRAME, avf.P_FRAME:
			// Data frames terminate a CONNECT_HEADER accumulation sequence.
			if appendingToConnectHeader {
				appendingToConnectHeader = false
				d.videoCodec = parseVideoCodec(frm.MediaType, connectHeader)
			} else if frm.FrameType == avf.I_FRAME {
				// MJPEG does not use CONNECT_HEADER; detect from first I_FRAME.
				if d.videoCodec == nil && frm.MediaType == avf.MJPG {
					d.videoCodec = mjpeg.CodecData{}
				}
			}
		case avf.UNKNOWN_FRAME:
			// Skip silently — does NOT terminate CONNECT_HEADER accumulation (R1).
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

		switch frm.FrameType {
		case avf.AUDIO_FRAME:
			if d.audioCodec == nil {
				d.audioCodec = parseAudioCodec(frm.MediaType, frm.Data)
			}
		case avf.H_FRAME, avf.I_FRAME, avf.P_FRAME, avf.CONNECT_HEADER, avf.UNKNOWN_FRAME:
			// not audio — skip
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

		// Drain split NALUs from the previous multi-NALU frame first.
		if len(d.pendingNALUs) > 0 {
			pkt := d.pendingNALUs[0]
			d.pendingNALUs = d.pendingNALUs[1:]

			return pkt, nil
		}

		var frm avf.Frame

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

		// For video data frames, split multi-NALU Annex-B access units into
		// individual single-NALU frames before converting to packets.
		switch frm.FrameType {
		case avf.I_FRAME, avf.P_FRAME, avf.NON_REF_FRAME:
			split := avf.SplitFrame(frm)
			if len(split) > 1 {
				// Convert all split frames; collect valid packets.
				var pkts []av.Packet
				for _, sf := range split {
					pkt, skip := d.frameToPacket(sf)
					if skip {
						continue
					}
					pkts = append(pkts, pkt)
				}
				if len(pkts) == 0 {
					continue
				}
				first := pkts[0]
				if len(d.pendingCodecChange) > 0 {
					first.NewCodecs = d.pendingCodecChange
					d.pendingCodecChange = nil
				}
				d.pendingNALUs = pkts[1:]
				return first, nil
			}
		}

		pkt, skip := d.frameToPacket(frm)

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
func (d *Demuxer) readFrame() (avf.Frame, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		return avf.Frame{}, err
	}

	if hdr[0] != '0' || hdr[1] != '0' || hdr[2] != 'd' || hdr[3] != 'c' {
		return avf.Frame{}, fmt.Errorf("%w: got %q", ErrBadMagic, string(hdr[0:4]))
	}

	// refFrameOff at [4:12] — not needed for demuxing.
	mediaType := binary.BigEndian.Uint32(hdr[12:16])
	frameType := binary.BigEndian.Uint32(hdr[16:20])
	timestamp := int64(binary.BigEndian.Uint64(hdr[20:28]))
	frameSize := binary.BigEndian.Uint32(hdr[28:32])

	if frameSize > maxFrameSize {
		return avf.Frame{}, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, frameSize)
	}

	data := make([]byte, frameSize)
	if _, err := io.ReadFull(d.r, data); err != nil {
		return avf.Frame{}, err
	}

	// Discard the trailing CurrentFrameOff field.
	var trailer [frameTrailerSize]byte
	if _, err := io.ReadFull(d.r, trailer[:]); err != nil {
		return avf.Frame{}, err
	}

	d.frameCount++

	return avf.Frame{
		BasicFrame: avf.BasicFrame{
			MediaType: avf.MediaType(mediaType),
			FrameType: avf.FrameType(frameType),
			TimeStamp: timestamp,
		},
		Data:    data,
		FrameID: d.frameCount,
	}, nil
}

// ── Codec detection ───────────────────────────────────────────────────────────

func isVideoMediaType(mt avf.MediaType) bool {
	switch mt {
	case avf.MJPG, avf.MPEG, avf.H264, avf.H265:
		return true
	case avf.G711U,
		avf.G711A,
		avf.L16,
		avf.AAC,
		avf.UNKNOWN,
		avf.G722,
		avf.G726,
		avf.OPUS,
		avf.MP2L2:
		return false
	}

	return false
}

// ── Packet assembly ───────────────────────────────────────────────────────────

// frameToPacket converts one avf.Frame into an av.Packet.
// skip=true means the frame carries no decodable media (e.g. CONNECT_HEADER).
func (d *Demuxer) frameToPacket(frm avf.Frame) (pkt av.Packet, skip bool) {
	dts := time.Duration(frm.TimeStamp) * time.Millisecond

	switch frm.FrameType {
	case avf.CONNECT_HEADER:
		// Accumulate per-NALU CONNECT_HEADER data. Codec change detection
		// is deferred to the next video data frame (I/P/NON_REF) so that
		// all parameter set NALUs (SPS+PPS or VPS+SPS+PPS) are collected
		// before attempting a parse.
		if isVideoMediaType(frm.MediaType) && d.probed {
			d.connectHeaderBuf = append(d.connectHeaderBuf, frm.Data...)
		}

		return av.Packet{}, true

	case avf.I_FRAME, avf.P_FRAME, avf.NON_REF_FRAME:
		if !isVideoMediaType(frm.MediaType) || d.videoCodec == nil {
			return av.Packet{}, true
		}

		// If CONNECT_HEADER(s) preceded this frame, attempt a codec update.
		if len(d.connectHeaderBuf) > 0 {
			if newCodec := parseVideoCodec(frm.MediaType, d.connectHeaderBuf); newCodec != nil {
				d.videoCodec = newCodec
				d.rebuildStreams()
				d.pendingCodecChange = d.streams
			}

			d.connectHeaderBuf = d.connectHeaderBuf[:0]
		}

		// Clamp to enforce strictly-increasing DTS. Rare camera clock glitches
		// can produce a backward step in the video timestamp; clamping to
		// lastVideoTS+1 keeps downstream consumers (fMP4, LL-HLS) valid.
		ts := frm.TimeStamp
		if d.lastVideoTS != 0 && ts <= d.lastVideoTS {
			ts = d.lastVideoTS + 1
		}

		var dur time.Duration
		if d.lastVideoTS != 0 {
			dur = time.Duration(ts-d.lastVideoTS) * time.Millisecond
		}

		d.lastVideoTS = ts

		return av.Packet{
			Idx:           d.videoIdx,
			KeyFrame:      frm.FrameType == avf.I_FRAME,
			DTS:           time.Duration(ts) * time.Millisecond,
			WallClockTime: time.UnixMilli(ts),
			Duration:      dur,
			CodecType:     d.videoCodec.Type(),
			FrameID:       frm.FrameID,
			Data:          stripVideoPrefix(frm.Data),
		}, false

	case avf.AUDIO_FRAME:
		// Honour the disableAudio option: skip all audio frames including
		// late-detection ones so that audio packets are never emitted when
		// the caller has opted out of audio.
		if d.disableAudio {
			return av.Packet{}, true
		}

		if d.audioCodec == nil {
			// Late audio codec detection for streams that had no audio during probe.
			if c := parseAudioCodec(frm.MediaType, frm.Data); c != nil {
				d.audioCodec = c
				d.rebuildStreams()
				d.pendingCodecChange = d.streams
			} else {
				return av.Packet{}, true
			}
		}

		data := frm.Data

		// Strip ADTS header from AAC frames when present.
		if frm.MediaType == avf.AAC && len(data) >= 7 &&
			data[0] == 0xFF && data[1]&0xF6 == 0xF0 {
			if _, hdrLen, _, _, adtsErr := aacparser.ParseADTSHeader(
				data,
			); adtsErr == nil &&
				hdrLen < len(data) {
				data = data[hdrLen:]
			}
		}

		var dur time.Duration
		if d.lastAudioTS != 0 && frm.TimeStamp > d.lastAudioTS {
			dur = time.Duration(frm.TimeStamp-d.lastAudioTS) * time.Millisecond
		}

		d.lastAudioTS = frm.TimeStamp

		return av.Packet{
			Idx:           d.audioIdx,
			DTS:           dts,
			WallClockTime: time.UnixMilli(frm.TimeStamp),
			Duration:      dur,
			CodecType:     d.audioCodec.Type(),
			FrameID:       frm.FrameID,
			Data:          data,
		}, false

	case avf.UNKNOWN_FRAME:
		return av.Packet{}, true
	}

	return av.Packet{}, true
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
