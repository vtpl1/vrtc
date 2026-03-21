package avf_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	avfformat "github.com/vtpl1/vrtc/pkg/av/format/avf"
	"github.com/vtpl1/vrtc/pkg/avf"
)

// go test ./pkg/av/format/avf/proxy_test.go

var (
	_ av.DemuxCloser       = (*avfformat.ProxyMuxDemuxCloser)(nil)
	_ av.MuxCloser         = (*avfformat.ProxyMuxDemuxCloser)(nil)
	_ avf.FrameDemuxCloser = (*avfformat.ProxyMuxDemuxCloser)(nil)
	_ avf.FrameMuxCloser   = (*avfformat.ProxyMuxDemuxCloser)(nil)
)

var minimalAVCRecord = []byte{
	0x01,
	0x42, 0x00, 0x1E,
	0xFF,
	0xE1,
	0x00, 0x0F,
	0x67, 0x42, 0x00, 0x1E,
	0xAC, 0xD9, 0x40, 0xA0,
	0x3D, 0xA1, 0x00, 0x00,
	0x03, 0x00, 0x00,
	0x01,
	0x00, 0x04,
	0x68, 0xCE, 0x38, 0x80,
}

func makeH264Codec(t *testing.T) h264parser.CodecData {
	t.Helper()

	c, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(minimalAVCRecord)
	if err != nil {
		t.Fatalf("NewCodecDataFromAVCDecoderConfRecord() error = %v", err)
	}

	return c
}

func annexBSPSPPS(t *testing.T) []byte {
	t.Helper()

	c := makeH264Codec(t)

	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 1})
	b.Write(c.SPS())
	b.Write([]byte{0, 0, 0, 1})
	b.Write(c.PPS())

	return b.Bytes()
}

func withLenPrefix(data []byte) []byte {
	out := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(out[:4], uint32(len(data)))
	copy(out[4:], data)

	return out
}

// go test ./pkg/av/format/avf -run TestPacketDemuxToPacketMux -v
func TestPacketDemuxToPacketMux(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)
	ctx := context.Background()
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}

	if err := p.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}

	gotStreams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs() error = %v", err)
	}

	if len(gotStreams) != 1 || gotStreams[0].Codec.Type() != av.H264 {
		t.Fatalf("GetCodecs() = %+v", gotStreams)
	}

	want := av.Packet{
		Idx:       0,
		KeyFrame:  true,
		DTS:       33 * time.Millisecond,
		CodecType: av.H264,
		Data:      []byte{0x65, 0xAA},
		FrameID:   10,
	}

	if err := p.WritePacket(ctx, want); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	got, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}

	if got.FrameID != want.FrameID || got.DTS != want.DTS || !got.KeyFrame {
		t.Fatalf("ReadPacket() = %+v, want %+v", got, want)
	}
}

// go test ./pkg/av/format/avf -run TestFrameDemuxToPacketMux -v
func TestFrameDemuxToPacketMux(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)
	ctx := context.Background()

	if err := p.WriteFrame(ctx, avf.Frame{
		BasicFrame: avf.BasicFrame{
			MediaType: avf.H264,
			FrameType: avf.CONNECT_HEADER,
		},
		Data:    annexBSPSPPS(t),
		FrameID: 1,
	}); err != nil {
		t.Fatalf("WriteFrame(connect header) error = %v", err)
	}

	if err := p.WriteFrame(ctx, avf.Frame{
		BasicFrame: avf.BasicFrame{
			MediaType: avf.H264,
			FrameType: avf.I_FRAME,
			TimeStamp: 99,
		},
		Data:    withLenPrefix([]byte{0x65, 0xBE}),
		FrameID: 2,
	}); err != nil {
		t.Fatalf("WriteFrame(video) error = %v", err)
	}

	streams, err := p.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs() error = %v", err)
	}

	if len(streams) != 1 || streams[0].Codec.Type() != av.H264 {
		t.Fatalf("GetCodecs() = %+v", streams)
	}

	pkt, err := p.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}

	if !pkt.KeyFrame || pkt.CodecType != av.H264 || pkt.DTS != 99*time.Millisecond {
		t.Fatalf("ReadPacket() = %+v", pkt)
	}

	if !bytes.Equal(pkt.Data, []byte{0x65, 0xBE}) {
		t.Fatalf("ReadPacket() Data = %v", pkt.Data)
	}
}

// go test ./pkg/av/format/avf -run TestPacketDemuxToFrameMuxStartsWithConnectHeader -v
func TestPacketDemuxToFrameMuxStartsWithConnectHeader(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)
	ctx := context.Background()
	streams := []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}

	if err := p.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}

	first, err := p.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame(connect header) error = %v", err)
	}

	if first.FrameType != avf.CONNECT_HEADER || first.MediaType != avf.H264 {
		t.Fatalf("first ReadFrame() = %+v", first)
	}

	if err := p.WritePacket(ctx, av.Packet{
		Idx:       0,
		KeyFrame:  true,
		DTS:       40 * time.Millisecond,
		CodecType: av.H264,
		Data:      []byte{0x65, 0xAA},
		FrameID:   5,
	}); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	second, err := p.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame(keyframe connect header) error = %v", err)
	}

	if second.FrameType != avf.CONNECT_HEADER {
		t.Fatalf("second ReadFrame() = %+v, want CONNECT_HEADER", second)
	}

	third, err := p.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame(video frame) error = %v", err)
	}

	if third.FrameType != avf.I_FRAME || third.TimeStamp != 40 {
		t.Fatalf("third ReadFrame() = %+v", third)
	}

	if !bytes.Equal(third.Data, []byte{0, 0, 0, 1, 0x65, 0xAA}) {
		t.Fatalf("third ReadFrame() Data = %v", third.Data)
	}
}

// go test ./pkg/av/format/avf -run TestModeConflict -v
func TestModeConflict(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(1)

	if err := p.WriteHeader(
		context.Background(),
		[]av.Stream{{Idx: 0, Codec: makeH264Codec(t)}},
	); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}

	err := p.WriteFrame(context.Background(), avf.Frame{})
	if !errors.Is(err, avfformat.ErrConfiguredAsPacketMuxer) {
		t.Fatalf("WriteFrame() error = %v, want ErrConfiguredAsPacketMuxer", err)
	}
}

// go test ./pkg/av/format/avf -run TestCloseEndsPacketReads -v
func TestCloseEndsPacketReads(t *testing.T) {
	t.Parallel()

	p := avfformat.NewProxyMuxDemuxCloser(0)

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := p.ReadPacket(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadPacket() error = %v, want io.EOF", err)
	}
}
