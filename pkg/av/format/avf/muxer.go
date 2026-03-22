package avf

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/avf"
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

// ── streamInfo ────────────────────────────────────────────────────────────────

// streamInfo holds per-stream muxing state.
type streamInfo struct {
	codec     av.CodecData
	mediaType avf.MediaType
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

// writeVideoPacket writes one video av.Packet to the AVF stream.
// pkt.Data must contain exactly one raw NALU (no start code, no length prefix)
// per the av.Packet single-NALU invariant. This function prepends the 4-byte
// Annex-B start code (\x00\x00\x00\x01) when constructing the wire frame.
// For H.264/H.265 keyframes it first emits the CONNECT_HEADER group via
// writeConnectHeaderFrames (one frame per parameter set NALU).
func (m *Muxer) writeVideoPacket(si streamInfo, pkt av.Packet, tsMs int64) error {
	if pkt.KeyFrame && si.mediaType != avf.MJPG {
		// Emit one CONNECT_HEADER per parameter set NALU before every I_FRAME
		// (H.264: SPS, PPS; H.265: VPS, SPS, PPS). Per §6.1 each CONNECT_HEADER
		// carries exactly one NALU in Annex-B format.
		if err := m.writeConnectHeaderFrames(si, tsMs); err != nil {
			return err
		}
	}

	frameType := avf.P_FRAME
	if pkt.KeyFrame {
		frameType = avf.I_FRAME
	}

	// §6.2: video payload is stored with a 4-byte start-code prefix.
	payload := prependStartCode(pkt.Data)

	return m.writeFrame(si.mediaType, frameType, tsMs, payload)
}

func (m *Muxer) writeAudioPacket(si streamInfo, tsMs int64, data []byte) error {
	return m.writeFrame(si.mediaType, avf.AUDIO_FRAME, tsMs, data)
}

// writeFrame emits one complete AVF frame record:
//
//	header(32) + payload(N) + trailer(8) = 40 + N bytes.
func (m *Muxer) writeFrame(
	mediaType avf.MediaType,
	frameType avf.FrameType,
	tsMs int64,
	data []byte,
) error {
	frameStart := m.currentOffset

	// Build the 32-byte frame header in one allocation.
	var hdr [frameHeaderSize]byte

	hdr[0], hdr[1], hdr[2], hdr[3] = '0', '0', 'd', 'c'
	binary.BigEndian.PutUint64(hdr[4:12], uint64(m.lastConnectHdrOff))
	binary.BigEndian.PutUint32(hdr[12:16], uint32(mediaType))
	binary.BigEndian.PutUint32(hdr[16:20], uint32(frameType))
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

// writeConnectHeaderFrames emits one CONNECT_HEADER frame per parameter set
// NALU for the stream's codec. Each CONNECT_HEADER carries exactly one NALU in
// Annex-B format (\x00\x00\x00\x01 + raw NALU bytes). Sets lastConnectHdrOff
// to the offset of the first CONNECT_HEADER written (the SPS/VPS frame).
// No-op for codecs without
// parameter sets (e.g. MJPEG).
func (m *Muxer) writeConnectHeaderFrames(si streamInfo, tsMs int64) error {
	var nalus [][]byte

	switch c := si.codec.(type) {
	case h264parser.CodecData:
		nalus = [][]byte{c.SPS(), c.PPS()}
	case h265parser.CodecData:
		nalus = [][]byte{c.VPS(), c.SPS(), c.PPS()}
	default:
		return nil
	}

	// Set lastConnectHdrOff to point at the first CONNECT_HEADER of this group.
	m.lastConnectHdrOff = m.currentOffset

	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}

		if err := m.writeFrame(si.mediaType, avf.CONNECT_HEADER, tsMs, prependStartCode(nalu)); err != nil {
			return err
		}
	}

	return nil
}

// ── codec helpers ─────────────────────────────────────────────────────────────

// mediaTypeForCodec maps an av.CodecType to the corresponding AVF MediaType
// constant (§4). Returns (0, false) for unsupported/unknown types.
func mediaTypeForCodec(ct av.CodecType) (avf.MediaType, bool) {
	switch ct {
	case av.H264:
		return avf.H264, true
	case av.H265:
		return avf.H265, true
	case av.MJPEG:
		return avf.MJPG, true
	case av.PCM_MULAW:
		return avf.G711U, true
	case av.PCM_ALAW:
		return avf.G711A, true
	case av.PCM, av.PCML:
		return avf.L16, true
	case av.AAC:
		return avf.AAC, true
	case av.OPUS:
		return avf.OPUS, true
	case av.MP3:
		return avf.MP2L2, true
	}

	return 0, false
}

// prependStartCode returns a new slice with a 4-byte start code prepended.
// Per §6.2 the writer stores I_FRAME / P_FRAME data as "\x00\x00\x00\x01" + NALU.
func prependStartCode(data []byte) []byte {
	out := make([]byte, 4+len(data))
	out[0], out[1], out[2], out[3] = 0x00, 0x00, 0x00, 0x01
	copy(out[4:], data)

	return out
}
