package fmp4_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
)

// minimalAVCRecord is a synthetic AVCDecoderConfigurationRecord for
// a 320×240 Baseline-profile H.264 stream (profile 66, level 30).
var minimalAVCRecord = []byte{
	0x01,             // configurationVersion
	0x42, 0x00, 0x1E, // profile_idc, constraint_flags, level_idc
	0xFF,       // lengthSizeMinusOne = 3
	0xE1,       // numSequenceParameterSets = 1
	0x00, 0x0F, // SPS length
	// SPS: 66 00 1E AC D9 40 A0 3D A1 00 00 03 00 00 03 (truncated but enough for parser)
	0x67, 0x42, 0x00, 0x1E,
	0xAC, 0xD9, 0x40, 0xA0,
	0x3D, 0xA1, 0x00, 0x00,
	0x03, 0x00, 0x00,
	0x01,       // numPictureParameterSets = 1
	0x00, 0x04, // PPS length
	0x68, 0xCE, 0x38, 0x80, // PPS
}

// minimalAAC is a 2-byte AudioSpecificConfig for AAC-LC 44100 Hz stereo.
// ObjectType=2 (LC), SampleRateIndex=4 (44100), ChannelConfig=2 (stereo)
// Bits: 00010 0100 0010 0 = 0x12 0x10
var minimalAAC = []byte{0x12, 0x10}

func makeH264Codec(t *testing.T) h264parser.CodecData {
	t.Helper()

	c, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(minimalAVCRecord)
	if err != nil {
		t.Fatalf("h264parser: %v", err)
	}

	return c
}

func makeAACCodec(t *testing.T) aacparser.CodecData {
	t.Helper()

	c, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes(minimalAAC)
	if err != nil {
		t.Fatalf("aacparser: %v", err)
	}

	return c
}

// readBox reads an ISO BMFF box header from b and returns (type, payload).
// It advances b past the box.
func readBox(t *testing.T, b *bytes.Reader) (string, []byte) {
	t.Helper()

	var hdr [8]byte
	if _, err := b.Read(hdr[:]); err != nil {
		t.Fatalf("readBox: read header: %v", err)
	}

	size := binary.BigEndian.Uint32(hdr[0:4])
	typ := string(hdr[4:8])

	if size < 8 {
		t.Fatalf("readBox: size %d < 8", size)
	}

	payload := make([]byte, size-8)
	if _, err := b.Read(payload); err != nil {
		t.Fatalf("readBox: read payload: %v", err)
	}

	return typ, payload
}

func TestWriteHeader_InitSegment(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	aac := makeAACCodec(t)

	streams := []av.Stream{
		{Idx: 0, Codec: h264},
		{Idx: 1, Codec: aac},
	}

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Verify ftyp + moov are present.
	r := bytes.NewReader(buf.Bytes())

	typ, _ := readBox(t, r)
	if typ != "ftyp" {
		t.Errorf("expected ftyp, got %q", typ)
	}

	typ, _ = readBox(t, r)
	if typ != "moov" {
		t.Errorf("expected moov, got %q", typ)
	}

	if r.Len() != 0 {
		t.Errorf("unexpected trailing bytes: %d", r.Len())
	}
}

func TestWriteHeader_Idempotency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}

	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("first WriteHeader: %v", err)
	}

	if err := m.WriteHeader(ctx, streams); err == nil {
		t.Fatal("second WriteHeader should return error")
	}
}

func TestWriteTrailer_BeforeAnyPacket(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	initLen := buf.Len()

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}

	// No samples → no fragment should be emitted.
	if buf.Len() != initLen {
		t.Errorf("unexpected bytes written: got %d want %d", buf.Len(), initLen)
	}
}

func TestFragment_VideoKeyframe(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	streams := []av.Stream{{Idx: 0, Codec: h264}}
	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	dur := 33 * time.Millisecond
	frameData := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xDE, 0xAD}

	// First keyframe: no flush yet (nothing to flush).
	pkt0 := av.Packet{
		KeyFrame:  true,
		Idx:       0,
		DTS:       0,
		Duration:  dur,
		Data:      frameData,
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt0); err != nil {
		t.Fatalf("WritePacket(kf0): %v", err)
	}

	sizeBefore := buf.Len()

	// Second keyframe triggers a flush of the first frame.
	pkt1 := av.Packet{
		KeyFrame:  true,
		Idx:       0,
		DTS:       time.Duration(dur),
		Duration:  dur,
		Data:      frameData,
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt1); err != nil {
		t.Fatalf("WritePacket(kf1): %v", err)
	}

	if buf.Len() == sizeBefore {
		t.Fatal("expected a fragment to be written on second keyframe")
	}

	// Verify moof+mdat structure in the emitted fragment.
	r := bytes.NewReader(buf.Bytes())
	readBox(t, r) // skip ftyp
	readBox(t, r) // skip moov

	typ, _ := readBox(t, r)
	if typ != "moof" {
		t.Errorf("expected moof, got %q", typ)
	}

	typ, _ = readBox(t, r)
	if typ != "mdat" {
		t.Errorf("expected mdat, got %q", typ)
	}
}

func TestFragment_AudioOnly(t *testing.T) {
	t.Parallel()

	aac := makeAACCodec(t)

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: aac}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	audioData := make([]byte, 128)
	pkt := av.Packet{
		Idx:       0,
		DTS:       0,
		Duration:  23 * time.Millisecond,
		Data:      audioData,
		CodecType: av.AAC,
	}

	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	// Audio-only: flush immediately on every packet.
	r := bytes.NewReader(buf.Bytes())
	readBox(t, r) // ftyp
	readBox(t, r) // moov

	typ, _ := readBox(t, r)
	if typ != "moof" {
		t.Errorf("expected moof, got %q", typ)
	}

	typ, moofPayload := readBox(t, r)
	if typ != "mdat" {
		t.Errorf("expected mdat, got %q", typ)
	}

	if len(moofPayload) != len(audioData) {
		t.Errorf("mdat payload len = %d, want %d", len(moofPayload), len(audioData))
	}
}

func TestWriteTrailer_FlushesRemaining(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Write one keyframe (no flush yet since no second keyframe arrives).
	pkt := av.Packet{
		KeyFrame:  true,
		Idx:       0,
		DTS:       0,
		Duration:  33 * time.Millisecond,
		Data:      []byte{0x00, 0x00, 0x00, 0x05, 0x65},
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	sizeBefore := buf.Len()

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}

	if buf.Len() == sizeBefore {
		t.Error("WriteTrailer should flush remaining samples")
	}
}

func TestWriteTrailer_Idempotency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("first WriteTrailer: %v", err)
	}

	if err := m.WriteTrailer(ctx, nil); err == nil {
		t.Fatal("second WriteTrailer should return error")
	}
}

func TestMoov_ContainsMvex(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	r := bytes.NewReader(buf.Bytes())
	readBox(t, r) // ftyp

	_, moovPayload := readBox(t, r) // moov

	// Walk moov children looking for mvex.
	found := false
	mr := bytes.NewReader(moovPayload)

	for mr.Len() > 0 {
		typ, _ := readBox(t, mr)
		if typ == "mvex" {
			found = true

			break
		}
	}

	if !found {
		t.Error("moov does not contain mvex")
	}
}

func TestMoovTrak_ContainsAvcC(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	data := buf.Bytes()
	// avcC bytes should appear somewhere in the output.
	avcCHeader := []byte("avcC")
	if !bytes.Contains(data, avcCHeader) {
		t.Error("init segment does not contain avcC box")
	}
}

// ── MuxCloser ─────────────────────────────────────────────────────────────────

// closingWriter wraps bytes.Buffer and records whether Close was called.
type closingWriter struct {
	bytes.Buffer
	closed bool
}

func (cw *closingWriter) Close() error {
	cw.closed = true

	return nil
}

func TestClose_ClosesUnderlying(t *testing.T) {
	t.Parallel()

	cw := &closingWriter{}
	m := fmp4.NewMuxer(cw)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !cw.closed {
		t.Error("Close did not call Close() on the underlying writer")
	}
}

func TestClose_BestEffortTrailer(t *testing.T) {
	t.Parallel()

	// Verify Close flushes remaining samples even if WriteTrailer wasn't called.
	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	pkt := av.Packet{
		KeyFrame:  true,
		Idx:       0,
		DTS:       0,
		Duration:  33 * time.Millisecond,
		Data:      []byte{0x00, 0x00, 0x00, 0x05, 0x65},
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	sizeBefore := buf.Len()

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The buffered keyframe should have been flushed.
	if buf.Len() == sizeBefore {
		t.Error("Close did not flush remaining samples")
	}
}

func TestClose_WithNonCloserWriter(t *testing.T) {
	t.Parallel()

	// io.Discard does not implement io.Closer; Close must still return nil.
	m := fmp4.NewMuxer(io.Discard)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close on non-Closer writer: %v", err)
	}
}

// ── CodecChanger ──────────────────────────────────────────────────────────────

func TestWriteCodecChange_EmitsNewInitSegment(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	h264 := makeH264Codec(t)
	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Feed one keyframe so there are buffered samples.
	pkt := av.Packet{
		KeyFrame:  true,
		Idx:       0,
		DTS:       0,
		Duration:  33 * time.Millisecond,
		Data:      []byte{0x00, 0x00, 0x00, 0x05, 0x65},
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	sizeBeforeChange := buf.Len()

	// Trigger a codec change.
	if err := m.WriteCodecChange(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteCodecChange: %v", err)
	}

	// A new init segment (ftyp+moov) must have been written.
	added := buf.Bytes()[sizeBeforeChange:]
	if len(added) < 8 {
		t.Fatalf("expected new init segment after codec change, got %d bytes", len(added))
	}

	// Walk added boxes: expect moof/mdat from the flushed fragment, then ftyp+moov.
	r := bytes.NewReader(added)
	sawMoov := false

	for r.Len() > 0 {
		typ, _ := readBox(t, r)
		if typ == "moov" {
			sawMoov = true

			break
		}
	}

	if !sawMoov {
		t.Error("no moov box found after codec change")
	}
}

func TestWriteCodecChange_UpdatesTimingContinuity(t *testing.T) {
	t.Parallel()

	// After a codec change the fragment sequence numbers must remain monotonic.
	var buf bytes.Buffer
	m := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	h264 := makeH264Codec(t)
	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	dur := 33 * time.Millisecond
	frame := []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88}

	// Write two keyframes → flush after second.
	for i := range 2 {
		pkt := av.Packet{
			KeyFrame:  true,
			Idx:       0,
			DTS:       time.Duration(i) * dur,
			Duration:  dur,
			Data:      frame,
			CodecType: av.H264,
		}
		if err := m.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("WritePacket[%d]: %v", i, err)
		}
	}

	if err := m.WriteCodecChange(ctx, []av.Stream{{Idx: 0, Codec: h264}}); err != nil {
		t.Fatalf("WriteCodecChange: %v", err)
	}

	// Write another keyframe after the codec change.
	pkt := av.Packet{
		KeyFrame:  true,
		Idx:       0,
		DTS:       2 * dur,
		Duration:  dur,
		Data:      frame,
		CodecType: av.H264,
	}
	if err := m.WritePacket(ctx, pkt); err != nil {
		t.Fatalf("WritePacket after change: %v", err)
	}

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}

	// Parse out all mfhd sequence numbers and verify monotonic increase.
	seqs := extractMFHDSeqNos(t, buf.Bytes())
	if len(seqs) < 2 {
		t.Fatalf("expected ≥2 mfhd, got %d", len(seqs))
	}

	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Errorf("mfhd seqNos not monotonic: %v", seqs)
		}
	}
}

func TestSatisfies_MuxCloser(t *testing.T) {
	t.Parallel()

	var _ av.MuxCloser = fmp4.NewMuxer(io.Discard)
}

func TestSatisfies_CodecChanger(t *testing.T) {
	t.Parallel()

	var _ av.CodecChanger = fmp4.NewMuxer(io.Discard)
}

// extractMFHDSeqNos walks raw fMP4 bytes and extracts the sequence_number
// from every mfhd (Movie Fragment Header) box found anywhere in the data.
func extractMFHDSeqNos(t *testing.T, data []byte) []uint32 {
	t.Helper()

	var seqs []uint32

	findMFHD(t, data, &seqs)

	return seqs
}

func findMFHD(t *testing.T, data []byte, seqs *[]uint32) {
	t.Helper()

	r := bytes.NewReader(data)

	for r.Len() >= 8 {
		var hdr [8]byte
		if _, err := r.Read(hdr[:]); err != nil {
			break
		}

		sz := int(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])

		if sz < 8 || sz > r.Len()+8 {
			break
		}

		payload := make([]byte, sz-8)
		if _, err := r.Read(payload); err != nil {
			break
		}

		if typ == "mfhd" && len(payload) >= 8 {
			// mfhd full-box: 4 bytes version+flags + 4 bytes sequence_number
			seq := binary.BigEndian.Uint32(payload[4:8])
			*seqs = append(*seqs, seq)
		}

		// Recurse into container boxes.
		switch typ {
		case "moov", "moof", "traf", "trak", "mdia", "minf", "stbl", "mvex":
			findMFHD(t, payload, seqs)
		}
	}
}

// TestEmsg_BoundingBoxes verifies that a packet carrying bounding-box JSON in
// its Extra field results in an emsg box appearing before the moof box in the
// flushed fragment, and that the emsg payload matches the original JSON.
func TestEmsg_BoundingBoxes(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	bboxJSON := []byte(`{"boxes":[{"label":"person","x":0.1,"y":0.2,"w":0.3,"h":0.4}]}`)

	fw, _, err := fmp4.NewFragmentWriter(streams)
	if err != nil {
		t.Fatalf("NewFragmentWriter: %v", err)
	}

	fw.WritePacket(av.Packet{
		Idx:      0,
		KeyFrame: true,
		DTS:      33 * time.Millisecond,
		Duration: 33 * time.Millisecond,
		Data:     []byte{0x65, 0x88},
		Extra:    bboxJSON,
	})

	fragment := fw.Flush()
	if fragment == nil {
		t.Fatal("Flush returned nil")
	}

	r := bytes.NewReader(fragment)

	// First box must be emsg.
	typ, payload := readBox(t, r)
	if typ != "emsg" {
		t.Fatalf("first box: want emsg, got %q", typ)
	}

	// emsg full-box: 1 byte version + 3 bytes flags = 4 bytes prefix.
	// version=1, so layout after prefix:
	//   scheme_id_uri (null-terminated)
	//   value         (null-terminated)
	//   timescale     uint32
	//   presentation_time uint64
	//   event_duration uint32
	//   id            uint32
	//   message_data  (rest)
	version := payload[0]
	if version != 1 {
		t.Errorf("emsg version: want 1, got %d", version)
	}

	// Skip version+flags (4 bytes), find end of scheme_id_uri and value strings.
	pos := 4
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	pos++ // skip null terminator of scheme_id_uri
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	pos++ // skip null terminator of value

	// timescale(4) + presentation_time(8) + event_duration(4) + id(4) = 20 bytes
	pos += 20

	if pos > len(payload) {
		t.Fatal("emsg payload too short")
	}

	got := payload[pos:]
	if !bytes.Equal(got, bboxJSON) {
		t.Errorf("emsg data mismatch:\n got  %s\n want %s", got, bboxJSON)
	}

	// Next box must be moof.
	typ, _ = readBox(t, r)
	if typ != "moof" {
		t.Errorf("second box: want moof, got %q", typ)
	}
}
