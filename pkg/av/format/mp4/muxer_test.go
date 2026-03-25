package mp4_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/mp4"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// readBoxType reads an ISO BMFF box header from r and returns the 4-char box
// type, advancing r past the entire box.
func readBoxType(t *testing.T, r *bytes.Reader) string {
	t.Helper()

	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Fatalf("readBoxType: %v", err)
	}

	size := int(hdr[0])<<24 | int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
	typ := string(hdr[4:8])

	if size < 8 {
		t.Fatalf("readBoxType: box size %d < 8", size)
	}

	payload := make([]byte, size-8)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("readBoxType: read payload for %q: %v", typ, err)
	}

	return typ
}

// closingWriter is a bytes.Buffer that records whether Close was called.
type closingWriter struct {
	bytes.Buffer
	closed bool
}

func (cw *closingWriter) Close() error {
	cw.closed = true
	return nil
}

// All durations are exact ms multiples so they round-trip through AVF (1ms
// precision) and FMP4/MP4 (timescale units) without rounding error.
const (
	vidDur = 33 * time.Millisecond // 2970 ticks @ 90000 Hz
	audDur = 20 * time.Millisecond // 882 ticks  @ 44100 Hz
)

// ── mp4 package lifecycle tests ───────────────────────────────────────────────

func TestMP4Muxer_WriteHeader_Idempotency(t *testing.T) {
	t.Parallel()

	m := mp4.NewMuxer(io.Discard)
	ctx := context.Background()
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}

	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("first WriteHeader: %v", err)
	}

	if err := m.WriteHeader(ctx, streams); err == nil {
		t.Fatal("second WriteHeader should return error")
	}
}

func TestMP4Muxer_WriteTrailer_Idempotency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := mp4.NewMuxer(&buf)
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

func TestMP4Muxer_WritePacket_BeforeWriteHeader(t *testing.T) {
	t.Parallel()

	m := mp4.NewMuxer(io.Discard)
	pkt := av.Packet{Idx: 0, KeyFrame: true, Data: []byte{0x65}, CodecType: av.H264}

	if err := m.WritePacket(context.Background(), pkt); err == nil {
		t.Fatal("WritePacket before WriteHeader should return error")
	}
}

func TestMP4Muxer_WritePacket_AfterWriteTrailer(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	m := mp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}

	pkt := av.Packet{Idx: 0, KeyFrame: true, Data: []byte{0x65}, CodecType: av.H264}
	if err := m.WritePacket(ctx, pkt); err == nil {
		t.Fatal("WritePacket after WriteTrailer should return error")
	}
}

func TestMP4Muxer_Close_ClosesUnderlying(t *testing.T) {
	t.Parallel()

	cw := &closingWriter{}
	m := mp4.NewMuxer(cw)
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

func TestMP4Muxer_Close_NonCloserWriter(t *testing.T) {
	t.Parallel()

	m := mp4.NewMuxer(io.Discard)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close on non-Closer writer: %v", err)
	}
}

func TestMP4Muxer_Output_HasFtypAndMoovBeforeMdat(t *testing.T) {
	t.Parallel()

	// Verify moov-first (fast-start) layout: ftyp then moov then mdat.
	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
	}

	data := muxFmt(t, allFormats[1], streams, inPkts) // allFormats[1] = mp4
	r := bytes.NewReader(data)

	typ1 := readBoxType(t, r)
	if typ1 != "ftyp" {
		t.Errorf("box 0: want ftyp, got %q", typ1)
	}

	typ2 := readBoxType(t, r)
	if typ2 != "moov" {
		t.Errorf("box 1: want moov, got %q", typ2)
	}

	if r.Len() > 0 {
		typ3 := readBoxType(t, r)
		if typ3 != "mdat" {
			t.Errorf("box 2: want mdat, got %q", typ3)
		}
	}
}
