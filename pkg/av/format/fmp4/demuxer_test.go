package fmp4_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
)

// compile-time check: *fmp4.Demuxer satisfies av.DemuxCloser.
var _ av.DemuxCloser = (*fmp4.Demuxer)(nil)

// ── helpers ───────────────────────────────────────────────────────────────────

// muxToBytes writes a complete fMP4 stream for the given streams and packets,
// returning the raw bytes.
func muxToBytes(t *testing.T, streams []av.Stream, pkts []av.Packet) []byte {
	t.Helper()

	var buf bytes.Buffer
	mux := fmp4.NewMuxer(&buf)
	ctx := context.Background()

	if err := mux.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("mux WriteHeader: %v", err)
	}

	for _, pkt := range pkts {
		if err := mux.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("mux WritePacket: %v", err)
		}
	}

	if err := mux.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("mux WriteTrailer: %v", err)
	}

	return buf.Bytes()
}

// readAllPackets drains a Demuxer until io.EOF and returns the packets.
func readAllPackets(t *testing.T, dmx *fmp4.Demuxer) []av.Packet {
	t.Helper()

	ctx := context.Background()
	var pkts []av.Packet

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}

		pkts = append(pkts, pkt)
	}

	return pkts
}

// ── GetCodecs tests ───────────────────────────────────────────────────────────

func TestDemuxer_GetCodecs_H264(t *testing.T) {
	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}
	data := muxToBytes(t, streams, nil) // no packets, just the init segment

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))
	got, err := dmx.GetCodecs(context.Background())

	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("want 1 stream, got %d", len(got))
	}

	if got[0].Codec.Type() != av.H264 {
		t.Errorf("want H264 codec, got %v", got[0].Codec.Type())
	}
}

func TestDemuxer_GetCodecs_AAC(t *testing.T) {
	aac := makeAACCodec(t)
	streams := []av.Stream{{Idx: 0, Codec: aac}}
	data := muxToBytes(t, streams, nil)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))
	got, err := dmx.GetCodecs(context.Background())

	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("want 1 stream, got %d", len(got))
	}

	if got[0].Codec.Type() != av.AAC {
		t.Errorf("want AAC codec, got %v", got[0].Codec.Type())
	}
}

func TestDemuxer_GetCodecs_MultiStream(t *testing.T) {
	h264 := makeH264Codec(t)
	aac := makeAACCodec(t)
	streams := []av.Stream{
		{Idx: 0, Codec: h264},
		{Idx: 1, Codec: aac},
	}
	data := muxToBytes(t, streams, nil)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))
	got, err := dmx.GetCodecs(context.Background())

	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 streams, got %d", len(got))
	}

	if got[0].Codec.Type() != av.H264 {
		t.Errorf("stream 0: want H264, got %v", got[0].Codec.Type())
	}

	if got[1].Codec.Type() != av.AAC {
		t.Errorf("stream 1: want AAC, got %v", got[1].Codec.Type())
	}
}

func TestDemuxer_GetCodecs_NoMoov(t *testing.T) {
	dmx := fmp4.NewDemuxer(bytes.NewReader([]byte{}))
	_, err := dmx.GetCodecs(context.Background())

	if !errors.Is(err, fmp4.ErrNoMoovBox) {
		t.Errorf("want ErrNoMoovBox, got %v", err)
	}
}

// ── ReadPacket / round-trip tests ─────────────────────────────────────────────

func TestDemuxer_ReadPacket_VideoOnlyRoundTrip(t *testing.T) {
	const frameDur = 33 * time.Millisecond

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	// Three frames: keyframe, non-keyframe, keyframe.
	// The muxer flushes on the second keyframe; WriteTrailer flushes the rest.
	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: frameDur, Data: []byte{0x01, 0x02, 0x03}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: frameDur, Duration: frameDur, Data: []byte{0x04, 0x05, 0x06}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * frameDur, Duration: frameDur, Data: []byte{0x07, 0x08, 0x09}, CodecType: av.H264},
	}

	data := muxToBytes(t, streams, inPkts)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))

	got, err := dmx.GetCodecs(context.Background())
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(got) != 1 || got[0].Codec.Type() != av.H264 {
		t.Fatalf("unexpected streams: %v", got)
	}

	outPkts := readAllPackets(t, dmx)

	if len(outPkts) != len(inPkts) {
		t.Fatalf("want %d packets, got %d", len(inPkts), len(outPkts))
	}

	for i, want := range inPkts {
		got := outPkts[i]

		if got.KeyFrame != want.KeyFrame {
			t.Errorf("pkt %d: KeyFrame want %v got %v", i, want.KeyFrame, got.KeyFrame)
		}

		if got.DTS != want.DTS {
			t.Errorf("pkt %d: DTS want %v got %v", i, want.DTS, got.DTS)
		}

		if got.Duration != want.Duration {
			t.Errorf("pkt %d: Duration want %v got %v", i, want.Duration, got.Duration)
		}

		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("pkt %d: Data want %v got %v", i, want.Data, got.Data)
		}
	}
}

func TestDemuxer_ReadPacket_AudioOnlyRoundTrip(t *testing.T) {
	// 20ms maps exactly to 882 ticks at 44100 Hz (20*44100/1000=882 integer).
	const frameDur = 20 * time.Millisecond

	aac := makeAACCodec(t)
	streams := []av.Stream{{Idx: 0, Codec: aac}}

	inPkts := []av.Packet{
		{Idx: 0, DTS: 0, Duration: frameDur, Data: []byte{0xAA, 0xBB}, CodecType: av.AAC},
		{Idx: 0, DTS: frameDur, Duration: frameDur, Data: []byte{0xCC, 0xDD}, CodecType: av.AAC},
	}

	data := muxToBytes(t, streams, inPkts)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	outPkts := readAllPackets(t, dmx)

	if len(outPkts) != len(inPkts) {
		t.Fatalf("want %d packets, got %d", len(inPkts), len(outPkts))
	}

	for i, want := range inPkts {
		got := outPkts[i]

		if got.DTS != want.DTS {
			t.Errorf("pkt %d: DTS want %v got %v", i, want.DTS, got.DTS)
		}

		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("pkt %d: Data want %v got %v", i, want.Data, got.Data)
		}
	}
}

func TestDemuxer_ReadPacket_VideoAndAudioRoundTrip(t *testing.T) {
	const vidDur = 33 * time.Millisecond
	const audDur = 21 * time.Millisecond

	h264 := makeH264Codec(t)
	aac := makeAACCodec(t)
	streams := []av.Stream{
		{Idx: 0, Codec: h264},
		{Idx: 1, Codec: aac},
	}

	// Two video keyframes + non-key + audio packets.
	// Fragment 1 is flushed at second video keyframe:
	//   contains [video@0(key), audio@0, video@33ms(non-key)].
	// WriteTrailer flushes fragment 2: [video@66ms(key)].
	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x01}, CodecType: av.H264},
		{Idx: 1, DTS: 0, Duration: audDur, Data: []byte{0xA1}, CodecType: av.AAC},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x03}, CodecType: av.H264},
	}

	data := muxToBytes(t, streams, inPkts)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))
	got, err := dmx.GetCodecs(context.Background())

	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 streams, got %d", len(got))
	}

	outPkts := readAllPackets(t, dmx)

	// 4 input packets → 4 output packets (possibly reordered by DTS within each fragment).
	if len(outPkts) != 4 {
		t.Fatalf("want 4 packets, got %d", len(outPkts))
	}

	// Verify DTS ordering: demuxer sorts by DTS within each fragment.
	for i := 1; i < len(outPkts); i++ {
		if outPkts[i].DTS < outPkts[i-1].DTS {
			t.Errorf("DTS not non-decreasing at index %d: %v < %v", i, outPkts[i].DTS, outPkts[i-1].DTS)
		}
	}

	// Verify all expected data bytes appear.
	dataSet := map[byte]bool{}
	for _, pkt := range outPkts {
		if len(pkt.Data) > 0 {
			dataSet[pkt.Data[0]] = true
		}
	}

	for _, b := range []byte{0x01, 0x02, 0x03, 0xA1} {
		if !dataSet[b] {
			t.Errorf("packet with data byte 0x%02X missing from output", b)
		}
	}
}

func TestDemuxer_ReadPacket_PTSOffset(t *testing.T) {
	const frameDur = 33 * time.Millisecond
	const ptsOff = 66 * time.Millisecond

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, PTSOffset: ptsOff, Duration: frameDur, Data: []byte{0x01}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: frameDur, PTSOffset: ptsOff, Duration: frameDur, Data: []byte{0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * frameDur, Duration: frameDur, Data: []byte{0x03}, CodecType: av.H264},
	}

	data := muxToBytes(t, streams, inPkts)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	outPkts := readAllPackets(t, dmx)

	if len(outPkts) != 3 {
		t.Fatalf("want 3 packets, got %d", len(outPkts))
	}

	// PTSOffset should survive the round-trip.
	if outPkts[0].PTSOffset != ptsOff {
		t.Errorf("pkt 0 PTSOffset: want %v, got %v", ptsOff, outPkts[0].PTSOffset)
	}

	if outPkts[1].PTSOffset != ptsOff {
		t.Errorf("pkt 1 PTSOffset: want %v, got %v", ptsOff, outPkts[1].PTSOffset)
	}
}

func TestDemuxer_ReadPacket_KeyFrameFlags(t *testing.T) {
	const frameDur = 33 * time.Millisecond

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: frameDur, Data: []byte{0x01}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: frameDur, Duration: frameDur, Data: []byte{0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * frameDur, Duration: frameDur, Data: []byte{0x03}, CodecType: av.H264},
	}

	data := muxToBytes(t, streams, inPkts)
	dmx := fmp4.NewDemuxer(bytes.NewReader(data))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	outPkts := readAllPackets(t, dmx)

	if len(outPkts) != 3 {
		t.Fatalf("want 3 packets, got %d", len(outPkts))
	}

	wantKey := []bool{true, false, true}

	for i, want := range wantKey {
		if outPkts[i].KeyFrame != want {
			t.Errorf("pkt %d: KeyFrame want %v, got %v", i, want, outPkts[i].KeyFrame)
		}
	}
}

// ── Close tests ───────────────────────────────────────────────────────────────

func TestDemuxer_Close_ClosesUnderlying(t *testing.T) {
	closed := false

	rc := &closingReader{
		r:      bytes.NewReader([]byte{}),
		onClose: func() { closed = true },
	}

	dmx := fmp4.NewDemuxer(rc)
	if err := dmx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !closed {
		t.Error("underlying reader was not closed")
	}
}

func TestDemuxer_Close_NonCloserReader(t *testing.T) {
	// bytes.Reader does not implement io.Closer; Close must still return nil.
	dmx := fmp4.NewDemuxer(bytes.NewReader([]byte{}))
	if err := dmx.Close(); err != nil {
		t.Errorf("Close on non-Closer: want nil, got %v", err)
	}
}

// ── EOF behaviour ─────────────────────────────────────────────────────────────

func TestDemuxer_ReadPacket_EOF(t *testing.T) {
	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}
	data := muxToBytes(t, streams, nil) // init segment only, no fragments

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	_, err := dmx.ReadPacket(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("want io.EOF, got %v", err)
	}
}

// ── Context cancellation ──────────────────────────────────────────────────────

func TestDemuxer_ReadPacket_CancelledContext(t *testing.T) {
	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}
	data := muxToBytes(t, streams, nil)

	dmx := fmp4.NewDemuxer(bytes.NewReader(data))

	if _, err := dmx.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := dmx.ReadPacket(ctx)
	if err == nil {
		t.Error("want error from cancelled context, got nil")
	}
}

// ── closingReader helper ──────────────────────────────────────────────────────

type closingReader struct {
	r       io.Reader
	onClose func()
}

func (c *closingReader) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (c *closingReader) Close() error {
	c.onClose()

	return nil
}
