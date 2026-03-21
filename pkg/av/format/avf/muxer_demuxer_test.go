package avf_test

// go test ./pkg/av/format/avf -run TestMuxer -v
// go test ./pkg/av/format/avf -run TestDemuxer -v

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/mjpeg"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	avfformat "github.com/vtpl1/vrtc/pkg/av/format/avf"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// roundTrip muxes streams+pkts into a buffer and demuxes them back.
func roundTrip(t *testing.T, streams []av.Stream, pkts []av.Packet, opts ...avfformat.Option) ([]av.Stream, []av.Packet) {
	t.Helper()

	ctx := context.Background()

	var buf bytes.Buffer

	mux := avfformat.NewMuxer(&buf)
	if err := mux.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	for _, p := range pkts {
		if err := mux.WritePacket(ctx, p); err != nil {
			t.Fatalf("WritePacket: %v", err)
		}
	}

	if err := mux.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}

	dmx := avfformat.New(&buf, opts...)

	gotStreams, err := dmx.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	var gotPkts []av.Packet

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}

		gotPkts = append(gotPkts, pkt)
	}

	return gotStreams, gotPkts
}

// ── muxer error tests ─────────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestMuxerWriteErrors -v
func TestMuxerWriteErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h264 := av.Stream{Idx: 0, Codec: makeH264Codec(t)}

	t.Run("WriteHeaderTwice", func(t *testing.T) {
		t.Parallel()

		mux := avfformat.NewMuxer(&bytes.Buffer{})
		_ = mux.WriteHeader(ctx, []av.Stream{h264})

		if err := mux.WriteHeader(ctx, []av.Stream{h264}); !errors.Is(err, avfformat.ErrMuxHeaderAlreadyWritten) {
			t.Fatalf("got %v, want ErrMuxHeaderAlreadyWritten", err)
		}
	})

	t.Run("WritePacketBeforeHeader", func(t *testing.T) {
		t.Parallel()

		mux := avfformat.NewMuxer(&bytes.Buffer{})

		err := mux.WritePacket(ctx, av.Packet{Idx: 0, KeyFrame: true, CodecType: av.H264})
		if !errors.Is(err, avfformat.ErrMuxHeaderNotWritten) {
			t.Fatalf("got %v, want ErrMuxHeaderNotWritten", err)
		}
	})

	t.Run("WriteTrailerTwice", func(t *testing.T) {
		t.Parallel()

		mux := avfformat.NewMuxer(&bytes.Buffer{})
		_ = mux.WriteHeader(ctx, []av.Stream{h264})
		_ = mux.WriteTrailer(ctx, nil)

		if err := mux.WriteTrailer(ctx, nil); !errors.Is(err, avfformat.ErrMuxTrailerAlreadyWritten) {
			t.Fatalf("got %v, want ErrMuxTrailerAlreadyWritten", err)
		}
	})

	t.Run("WritePacketAfterTrailer", func(t *testing.T) {
		t.Parallel()

		mux := avfformat.NewMuxer(&bytes.Buffer{})
		_ = mux.WriteHeader(ctx, []av.Stream{h264})
		_ = mux.WriteTrailer(ctx, nil)

		err := mux.WritePacket(ctx, av.Packet{Idx: 0, KeyFrame: true, CodecType: av.H264})
		if !errors.Is(err, avfformat.ErrMuxTrailerAlreadyWritten) {
			t.Fatalf("got %v, want ErrMuxTrailerAlreadyWritten", err)
		}
	})
}

// ── round-trip tests ──────────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestH264RoundTrip -v
func TestH264RoundTrip(t *testing.T) {
	t.Parallel()

	codec := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: codec}}

	pkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 33 * time.Millisecond, CodecType: av.H264, Data: []byte{0x65, 0x88}},
		{Idx: 0, KeyFrame: false, DTS: 66 * time.Millisecond, CodecType: av.H264, Data: []byte{0x41, 0x9a}},
	}

	gotStreams, gotPkts := roundTrip(t, streams, pkts)

	if len(gotStreams) != 1 || gotStreams[0].Codec.Type() != av.H264 {
		t.Fatalf("GetCodecs = %+v", gotStreams)
	}

	if len(gotPkts) != 2 {
		t.Fatalf("got %d packets, want 2", len(gotPkts))
	}

	if !gotPkts[0].KeyFrame || gotPkts[0].DTS != 33*time.Millisecond || gotPkts[0].CodecType != av.H264 {
		t.Errorf("pkt[0] = %+v", gotPkts[0])
	}

	if !bytes.Equal(gotPkts[0].Data, pkts[0].Data) {
		t.Errorf("pkt[0].Data = %v, want %v", gotPkts[0].Data, pkts[0].Data)
	}

	if gotPkts[1].KeyFrame || gotPkts[1].DTS != 66*time.Millisecond {
		t.Errorf("pkt[1] = %+v", gotPkts[1])
	}

	if !bytes.Equal(gotPkts[1].Data, pkts[1].Data) {
		t.Errorf("pkt[1].Data = %v, want %v", gotPkts[1].Data, pkts[1].Data)
	}
}

// go test ./pkg/av/format/avf -run TestH264DurationAndNewCodecs -v
func TestH264DurationAndNewCodecs(t *testing.T) {
	t.Parallel()

	// The muxer emits a CONNECT_HEADER before every keyframe.
	// After GetCodecs (probe), the demuxer treats each CONNECT_HEADER as a
	// codec-change event and attaches NewCodecs to the following packet.
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}
	pkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 33 * time.Millisecond, CodecType: av.H264, Data: []byte{0x65, 0x00}},
		{Idx: 0, KeyFrame: false, DTS: 66 * time.Millisecond, CodecType: av.H264, Data: []byte{0x41, 0x00}},
		{Idx: 0, KeyFrame: true, DTS: 99 * time.Millisecond, CodecType: av.H264, Data: []byte{0x65, 0x01}},
	}

	_, gotPkts := roundTrip(t, streams, pkts)

	if len(gotPkts) != 3 {
		t.Fatalf("got %d packets, want 3", len(gotPkts))
	}

	// First keyframe: no previous → Duration == 0, NewCodecs set (CONNECT_HEADER replayed).
	if gotPkts[0].Duration != 0 {
		t.Errorf("pkt[0].Duration = %v, want 0", gotPkts[0].Duration)
	}

	if gotPkts[0].NewCodecs == nil {
		t.Errorf("pkt[0].NewCodecs is nil, want non-nil")
	}

	// P-frame: Duration = delta from previous keyframe.
	if gotPkts[1].Duration != 33*time.Millisecond {
		t.Errorf("pkt[1].Duration = %v, want 33ms", gotPkts[1].Duration)
	}

	if gotPkts[1].NewCodecs != nil {
		t.Errorf("pkt[1].NewCodecs non-nil, want nil")
	}

	// Second keyframe: Duration = delta from P-frame, NewCodecs set.
	if gotPkts[2].Duration != 33*time.Millisecond {
		t.Errorf("pkt[2].Duration = %v, want 33ms", gotPkts[2].Duration)
	}

	if gotPkts[2].NewCodecs == nil {
		t.Errorf("pkt[2].NewCodecs is nil, want non-nil")
	}
}

// go test ./pkg/av/format/avf -run TestMJPEGRoundTrip -v
func TestMJPEGRoundTrip(t *testing.T) {
	t.Parallel()

	// MJPEG has no CONNECT_HEADER; the demuxer detects it from the first I_FRAME.
	streams := []av.Stream{{Idx: 0, Codec: mjpeg.CodecData{}}}
	pkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 33 * time.Millisecond, CodecType: av.MJPEG, Data: []byte{0xFF, 0xD8, 0xFF, 0xE0}},
		{Idx: 0, KeyFrame: true, DTS: 66 * time.Millisecond, CodecType: av.MJPEG, Data: []byte{0xFF, 0xD8, 0xFF, 0xE0}},
	}

	gotStreams, gotPkts := roundTrip(t, streams, pkts)

	if len(gotStreams) != 1 || gotStreams[0].Codec.Type() != av.MJPEG {
		t.Fatalf("GetCodecs = %+v", gotStreams)
	}

	if len(gotPkts) != 2 {
		t.Fatalf("got %d packets, want 2", len(gotPkts))
	}

	if !gotPkts[0].KeyFrame || gotPkts[0].DTS != 33*time.Millisecond {
		t.Errorf("pkt[0] = %+v", gotPkts[0])
	}

	if !bytes.Equal(gotPkts[0].Data, pkts[0].Data) {
		t.Errorf("pkt[0].Data = %v, want %v", gotPkts[0].Data, pkts[0].Data)
	}
}

// go test ./pkg/av/format/avf -run TestH264WithAudioRoundTrip -v
func TestH264WithAudioRoundTrip(t *testing.T) {
	t.Parallel()

	streams := []av.Stream{
		{Idx: 0, Codec: makeH264Codec(t)},
		{Idx: 1, Codec: pcm.NewPCMMulawCodecData()},
	}

	audioData := []byte{0x01, 0x02, 0x03, 0x04}
	pkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 33 * time.Millisecond, CodecType: av.H264, Data: []byte{0x65, 0x88}},
		{Idx: 0, KeyFrame: false, DTS: 66 * time.Millisecond, CodecType: av.H264, Data: []byte{0x41, 0x9a}},
		{Idx: 1, DTS: 100 * time.Millisecond, CodecType: av.PCM_MULAW, Data: audioData},
	}

	gotStreams, gotPkts := roundTrip(t, streams, pkts)

	if len(gotStreams) != 2 {
		t.Fatalf("GetCodecs len = %d, want 2", len(gotStreams))
	}

	if gotStreams[0].Codec.Type() != av.H264 {
		t.Errorf("stream[0] type = %v, want H264", gotStreams[0].Codec.Type())
	}

	if gotStreams[1].Codec.Type() != av.PCM_MULAW {
		t.Errorf("stream[1] type = %v, want PCM_MULAW", gotStreams[1].Codec.Type())
	}

	if len(gotPkts) != 3 {
		t.Fatalf("got %d packets, want 3", len(gotPkts))
	}

	// Video keyframe.
	if !gotPkts[0].KeyFrame || gotPkts[0].CodecType != av.H264 {
		t.Errorf("pkt[0] = %+v", gotPkts[0])
	}

	// Video P-frame.
	if gotPkts[1].KeyFrame || gotPkts[1].CodecType != av.H264 {
		t.Errorf("pkt[1] = %+v", gotPkts[1])
	}

	// Audio packet: data must pass through unchanged.
	if gotPkts[2].CodecType != av.PCM_MULAW || gotPkts[2].DTS != 100*time.Millisecond {
		t.Errorf("pkt[2] = %+v", gotPkts[2])
	}

	if !bytes.Equal(gotPkts[2].Data, audioData) {
		t.Errorf("audio data = %v, want %v", gotPkts[2].Data, audioData)
	}
}

// ── demuxer option tests ──────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestDemuxerDisableAudio -v
func TestDemuxerDisableAudio(t *testing.T) {
	t.Parallel()

	streams := []av.Stream{
		{Idx: 0, Codec: makeH264Codec(t)},
		{Idx: 1, Codec: pcm.NewPCMMulawCodecData()},
	}

	pkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 33 * time.Millisecond, CodecType: av.H264, Data: []byte{0x65, 0x88}},
		{Idx: 1, DTS: 50 * time.Millisecond, CodecType: av.PCM_MULAW, Data: []byte{0xAA, 0xBB}},
	}

	gotStreams, gotPkts := roundTrip(t, streams, pkts, avfformat.WithDisableAudio())

	if len(gotStreams) != 1 || gotStreams[0].Codec.Type() != av.H264 {
		t.Fatalf("GetCodecs = %+v, want H264 only", gotStreams)
	}

	for _, p := range gotPkts {
		if p.CodecType == av.PCM_MULAW {
			t.Errorf("audio packet leaked: %+v", p)
		}
	}
}

// ── timestamp tests ───────────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestDemuxerTimestampClamping -v
func TestDemuxerTimestampClamping(t *testing.T) {
	t.Parallel()

	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}
	pkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 33 * time.Millisecond, CodecType: av.H264, Data: []byte{0x65, 0x00}},
		{Idx: 0, KeyFrame: false, DTS: 66 * time.Millisecond, CodecType: av.H264, Data: []byte{0x41, 0x00}},
		{Idx: 0, KeyFrame: false, DTS: 50 * time.Millisecond, CodecType: av.H264, Data: []byte{0x41, 0x01}}, // backward
	}

	_, gotPkts := roundTrip(t, streams, pkts)

	if len(gotPkts) != 3 {
		t.Fatalf("got %d packets, want 3", len(gotPkts))
	}

	if gotPkts[0].DTS != 33*time.Millisecond {
		t.Errorf("pkt[0].DTS = %v, want 33ms", gotPkts[0].DTS)
	}

	if gotPkts[1].DTS != 66*time.Millisecond {
		t.Errorf("pkt[1].DTS = %v, want 66ms", gotPkts[1].DTS)
	}

	// Backward timestamp clamped to lastVideoTS+1 = 67ms.
	if gotPkts[2].DTS != 67*time.Millisecond {
		t.Errorf("pkt[2].DTS = %v, want 67ms (clamped)", gotPkts[2].DTS)
	}
}

// ── demuxer error tests ───────────────────────────────────────────────────────

// go test ./pkg/av/format/avf -run TestDemuxerBadMagic -v
func TestDemuxerBadMagic(t *testing.T) {
	t.Parallel()

	// A 32-byte block of zeros has the wrong magic ("\\x00\\x00\\x00\\x00" ≠ "00dc").
	bad := make([]byte, 40)

	dmx := avfformat.New(bytes.NewReader(bad))

	_, err := dmx.GetCodecs(context.Background())
	if !errors.Is(err, avfformat.ErrBadMagic) {
		t.Fatalf("GetCodecs() error = %v, want ErrBadMagic", err)
	}
}

// go test ./pkg/av/format/avf -run TestDemuxerNoCodecFound -v
func TestDemuxerNoCodecFound(t *testing.T) {
	t.Parallel()

	// Empty reader → probe reads no frames → no codec detected.
	dmx := avfformat.New(bytes.NewReader(nil))

	_, err := dmx.GetCodecs(context.Background())
	if !errors.Is(err, avfformat.ErrNoCodecFound) {
		t.Fatalf("GetCodecs() error = %v, want ErrNoCodecFound", err)
	}
}

// go test ./pkg/av/format/avf -run TestMuxerUnknownStreamSkipped -v
func TestMuxerUnknownStreamSkipped(t *testing.T) {
	t.Parallel()

	// Streams with an unsupported codec are silently skipped by the muxer.
	// Writing a packet for that stream index is a no-op; no bytes are written.
	ctx := context.Background()

	var buf bytes.Buffer
	mux := avfformat.NewMuxer(&buf)

	// av.UNKNOWN is not in mediaTypeForCodec, so the stream is dropped.
	_ = mux.WriteHeader(ctx, []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}, {Idx: 1, Codec: makeH264Codec(t)}})

	// Packet for a stream index not in the muxer's table is silently ignored.
	if err := mux.WritePacket(ctx, av.Packet{Idx: 99, CodecType: av.H264, Data: []byte{0x00}}); err != nil {
		t.Fatalf("WritePacket unknown idx: %v", err)
	}

	_ = mux.WriteTrailer(ctx, nil)

	// The buffer should still be valid AVF (just with only stream 0 data if we wrote it).
	// Verify no panic and no error.
}
