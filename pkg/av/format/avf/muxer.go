package avf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
)

// Sentinel errors returned by the muxer.
var (
	// ErrMuxHeaderAlreadyWritten is returned when WriteHeader is called more than once.
	ErrMuxHeaderAlreadyWritten = errors.New("avf: WriteHeader already called")
	// ErrMuxTrailerAlreadyWritten is returned when WriteTrailer is called more than once.
	ErrMuxTrailerAlreadyWritten = errors.New("avf: WriteTrailer already called")
	// ErrMuxHeaderNotWritten is returned when WritePacket is called before WriteHeader.
	ErrMuxHeaderNotWritten = errors.New("avf: WriteHeader not called")
)

// writeBufferSize is the internal bufio-equivalent write buffer size.
// AVF has no mandatory write buffer; we use a bytes.Buffer flush boundary instead.
const writeBufferSize = 2 * 1024 * 1024

// ── streamInfo ────────────────────────────────────────────────────────────────

// streamInfo holds per-stream muxing state.
type streamInfo struct {
	codec     av.CodecData
	mediaType uint32
	isVideo   bool
}

// ── Muxer ─────────────────────────────────────────────────────────────────────

// Muxer writes packets into an AVF container and implements av.MuxCloser.
// Create with NewMuxer or Create; call WriteHeader once, WritePacket for each
// packet, then WriteTrailer when done.
type Muxer struct {
	w  io.Writer
	wc io.Closer // non-nil when w also implements io.Closer

	streams map[uint16]streamInfo

	// byte offset of the current write position in the output file
	currentOffset int64
	// byte offset of the most recently written CONNECT_HEADER frame (-1 = none)
	lastConnectHdrOff int64

	written bool
	closed  bool
}

// NewMuxer returns a Muxer that writes AVF data to w.
// If w implements io.Closer, Close will delegate to it.
func NewMuxer(w io.Writer) *Muxer {
	m := &Muxer{
		w:                 w,
		streams:           make(map[uint16]streamInfo),
		lastConnectHdrOff: -1,
	}

	if wc, ok := w.(io.Closer); ok {
		m.wc = wc
	}

	return m
}

// Create opens (or truncates) the named file and returns a ready Muxer.
func Create(path string) (*Muxer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	return &Muxer{
		w:                 f,
		wc:                f,
		streams:           make(map[uint16]streamInfo),
		lastConnectHdrOff: -1,
	}, nil
}

// WriteHeader records codec information for the declared streams. Per the AVF
// specification there is no file-level header; no bytes are written to the
// underlying writer at this stage. The first bytes are emitted by WritePacket.
func (m *Muxer) WriteHeader(_ context.Context, streams []av.Stream) error {
	if m.written {
		return ErrMuxHeaderAlreadyWritten
	}

	for _, s := range streams {
		mt, ok := mediaTypeForCodec(s.Codec.Type())
		if !ok {
			continue // skip unsupported codecs silently
		}

		m.streams[s.Idx] = streamInfo{
			codec:     s.Codec,
			mediaType: mt,
			isVideo:   s.Codec.Type().IsVideo(),
		}
	}

	m.written = true

	return nil
}

// WritePacket serialises one compressed packet into the AVF stream.
// For video keyframes a CONNECT_HEADER is emitted immediately before the
// I_FRAME so that every keyframe is self-contained per §6.1 of the spec.
func (m *Muxer) WritePacket(_ context.Context, pkt av.Packet) error {
	if !m.written {
		return ErrMuxHeaderNotWritten
	}

	if m.closed {
		return ErrMuxTrailerAlreadyWritten
	}

	si, ok := m.streams[pkt.Idx]
	if !ok {
		return nil // unknown stream index — skip gracefully
	}

	tsMs := pkt.DTS.Milliseconds()

	if si.isVideo {
		return m.writeVideoPacket(si, pkt, tsMs)
	}

	return m.writeAudioPacket(si, tsMs, pkt.Data)
}

// WriteTrailer finalises the AVF stream. The AVF format has no end-of-file
// marker, so this call only changes the internal lifecycle state and optionally
// flushes the underlying writer if it implements io.WriteFlusher.
func (m *Muxer) WriteTrailer(_ context.Context, _ error) error {
	if m.closed {
		return ErrMuxTrailerAlreadyWritten
	}

	m.closed = true

	return nil
}

// Close calls WriteTrailer (best-effort) and then closes the underlying writer
// if it implements io.Closer.
func (m *Muxer) Close() error {
	if !m.closed {
		_ = m.WriteTrailer(context.Background(), nil)
	}

	if m.wc != nil {
		return m.wc.Close()
	}

	return nil
}

// WriteCodecChange implements av.CodecChanger. It updates the stored codec for
// each listed stream. The next keyframe write will automatically emit the
// updated CONNECT_HEADER.
func (m *Muxer) WriteCodecChange(_ context.Context, changed []av.Stream) error {
	if !m.written || m.closed {
		return nil
	}

	for _, s := range changed {
		mt, ok := mediaTypeForCodec(s.Codec.Type())
		if !ok {
			continue
		}

		m.streams[s.Idx] = streamInfo{
			codec:     s.Codec,
			mediaType: mt,
			isVideo:   s.Codec.Type().IsVideo(),
		}
	}

	return nil
}

// ── per-frame write helpers ───────────────────────────────────────────────────

func (m *Muxer) writeVideoPacket(si streamInfo, pkt av.Packet, tsMs int64) error {
	if pkt.KeyFrame && si.mediaType != mediaTypeMJPG {
		// Emit CONNECT_HEADER before every I_FRAME (§6.1).
		hdrData := buildConnectHeaderPayload(si.codec)
		if len(hdrData) > 0 {
			// The CONNECT_HEADER's RefFrameOff points to itself (it IS the
			// current parameter set reference).
			m.lastConnectHdrOff = m.currentOffset
			if err := m.writeFrame(
				si.mediaType,
				frameTypeConnectHeader,
				tsMs,
				hdrData,
			); err != nil {
				return err
			}
		}
	}

	frameType := frameTypePFrame
	if pkt.KeyFrame {
		frameType = frameTypeIFrame
	}

	// §6.2: video payload is stored with a 4-byte start-code prefix.
	payload := prependStartCode(pkt.Data)

	return m.writeFrame(si.mediaType, frameType, tsMs, payload)
}

func (m *Muxer) writeAudioPacket(si streamInfo, tsMs int64, data []byte) error {
	return m.writeFrame(si.mediaType, frameTypeAudioFrame, tsMs, data)
}

// writeFrame emits one complete AVF frame record:
//
//	header(32) + payload(N) + trailer(8) = 40 + N bytes.
func (m *Muxer) writeFrame(mediaType, frameType uint32, tsMs int64, data []byte) error {
	frameStart := m.currentOffset

	// Build the 32-byte frame header in one allocation.
	var hdr [frameHeaderSize]byte

	hdr[0], hdr[1], hdr[2], hdr[3] = '0', '0', 'd', 'c'
	binary.BigEndian.PutUint64(hdr[4:12], uint64(m.lastConnectHdrOff))
	binary.BigEndian.PutUint32(hdr[12:16], mediaType)
	binary.BigEndian.PutUint32(hdr[16:20], frameType)
	binary.BigEndian.PutUint64(hdr[20:28], uint64(tsMs))
	binary.BigEndian.PutUint32(hdr[28:32], uint32(len(data)))

	// Build the 8-byte trailer (CurrentFrameOff = this frame's start).
	var trailer [frameTrailerSize]byte
	binary.BigEndian.PutUint64(trailer[:], uint64(frameStart))

	// Write header + payload + trailer as a single gather-write where possible.
	if _, err := m.w.Write(hdr[:]); err != nil {
		return err
	}

	if len(data) > 0 {
		if _, err := m.w.Write(data); err != nil {
			return err
		}
	}

	if _, err := m.w.Write(trailer[:]); err != nil {
		return err
	}

	m.currentOffset += int64(40 + len(data))

	return nil
}

// ── codec helpers ─────────────────────────────────────────────────────────────

// mediaTypeForCodec maps an av.CodecType to the corresponding AVF MediaType
// constant (§4). Returns (0, false) for unsupported/unknown types.
func mediaTypeForCodec(ct av.CodecType) (uint32, bool) {
	switch ct {
	case av.H264:
		return mediaTypeH264, true
	case av.H265:
		return mediaTypeH265, true
	case av.MJPEG:
		return mediaTypeMJPG, true
	case av.PCM_MULAW:
		return mediaTypeG711U, true
	case av.PCM_ALAW:
		return mediaTypeG711A, true
	case av.PCM, av.PCML:
		return mediaTypeL16, true
	case av.AAC:
		return mediaTypeAAC, true
	case av.OPUS:
		return mediaTypeOPUS, true
	case av.MP3:
		return mediaTypeMP2L2, true
	}

	return 0, false
}

// buildConnectHeaderPayload returns the Annex-B encoded parameter sets for
// the given video codec (H.264: SPS+PPS; H.265: VPS+SPS+PPS).
// Returns nil for codec types that do not use a CONNECT_HEADER (e.g. MJPEG).
func buildConnectHeaderPayload(codec av.CodecData) []byte {
	switch c := codec.(type) {
	case h264parser.CodecData:
		var b bytes.Buffer
		writeAnnexBNALU(&b, c.SPS())
		writeAnnexBNALU(&b, c.PPS())

		return b.Bytes()

	case h265parser.CodecData:
		var b bytes.Buffer
		writeAnnexBNALU(&b, c.VPS())
		writeAnnexBNALU(&b, c.SPS())
		writeAnnexBNALU(&b, c.PPS())

		return b.Bytes()
	}

	return nil
}

var startCode = []byte{0x00, 0x00, 0x00, 0x01}

// writeAnnexBNALU appends a 4-byte start code followed by nalu to b.
func writeAnnexBNALU(b *bytes.Buffer, nalu []byte) {
	if len(nalu) == 0 {
		return
	}

	b.Write(startCode)
	b.Write(nalu)
}

// prependStartCode returns a new slice with a 4-byte start code prepended.
// Per §6.2 the writer stores I_FRAME / P_FRAME data as "\x00\x00\x00\x01" + NALU.
func prependStartCode(data []byte) []byte {
	out := make([]byte, 4+len(data))
	copy(out[:4], startCode)
	copy(out[4:], data)

	return out
}
