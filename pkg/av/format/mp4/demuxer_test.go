package mp4_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/mp4"
)

// ── mp4 demuxer lifecycle tests ───────────────────────────────────────────────

func TestMP4Demuxer_GetCodecs_NoMoov(t *testing.T) {
	t.Parallel()

	d := mp4.NewDemuxer(bytes.NewReader([]byte{}))
	_, err := d.GetCodecs(context.Background())

	if !errors.Is(err, mp4.ErrNoMoovBox) {
		t.Errorf("want ErrNoMoovBox, got %v", err)
	}
}

func TestMP4Demuxer_ReadPacket_EOF(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)

	// Mux a file with no packets (empty mdat).
	data := muxFmt(t, allFormats[2], []av.Stream{{Idx: 0, Codec: h264}}, nil)

	d := mp4.NewDemuxer(bytes.NewReader(data))

	if _, err := d.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	_, err := d.ReadPacket(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("want io.EOF, got %v", err)
	}
}

func TestMP4Demuxer_ReadPacket_CancelledContext(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	data := muxFmt(t, allFormats[2], []av.Stream{{Idx: 0, Codec: h264}}, nil)

	d := mp4.NewDemuxer(bytes.NewReader(data))

	if _, err := d.GetCodecs(context.Background()); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.ReadPacket(ctx)
	if err == nil {
		t.Error("want error from cancelled context, got nil")
	}
}

func TestMP4Demuxer_Close_ClosesUnderlying(t *testing.T) {
	t.Parallel()

	closed := false
	cr := &closingReadSeeker{
		r:       bytes.NewReader([]byte{}),
		onClose: func() { closed = true },
	}

	d := mp4.NewDemuxer(cr)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !closed {
		t.Error("underlying reader was not closed")
	}
}

func TestMP4Demuxer_Close_NonCloserReader(t *testing.T) {
	t.Parallel()

	d := mp4.NewDemuxer(bytes.NewReader([]byte{}))
	if err := d.Close(); err != nil {
		t.Errorf("Close on non-Closer: want nil, got %v", err)
	}
}

// TestMP4Demuxer_GetCodecs_MultiCodec verifies that a file with both H.264
// and AAC tracks reports both streams after GetCodecs.
func TestMP4Demuxer_GetCodecs_MultiCodec(t *testing.T) {
	t.Parallel()

	streams := []av.Stream{
		{Idx: 0, Codec: makeH264Codec(t)},
		{Idx: 1, Codec: makeAACCodec(t)},
	}

	data := muxFmt(t, allFormats[2], streams, nil)

	d := mp4.NewDemuxer(bytes.NewReader(data))

	got, err := d.GetCodecs(context.Background())
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

// TestMP4Demuxer_KeyFrameFlags verifies that keyframe information is preserved
// in a standalone mp4→mp4 round trip.
func TestMP4Demuxer_KeyFrameFlags(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
	}

	data := muxFmt(t, allFormats[2], streams, inPkts)
	_, outPkts := demuxFmt(t, allFormats[2], data)

	if len(outPkts) != len(inPkts) {
		t.Fatalf("want %d packets, got %d", len(inPkts), len(outPkts))
	}

	wantKeys := []bool{true, false, true}

	for i, wantKey := range wantKeys {
		if outPkts[i].KeyFrame != wantKey {
			t.Errorf("pkt[%d] KeyFrame: want %v, got %v", i, wantKey, outPkts[i].KeyFrame)
		}
	}
}
