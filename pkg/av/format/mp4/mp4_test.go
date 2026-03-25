package mp4_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
	"github.com/vtpl1/vrtc/pkg/av/format/mp4"
)

// ── codec fixtures ────────────────────────────────────────────────────────────

// minimalAVCRecord is a synthetic AVCDecoderConfigurationRecord for a 320×240
// H.264 Baseline stream, shared with the fmp4 unit tests.
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
		t.Fatalf("h264parser: %v", err)
	}

	return c
}

// minimalAAC is a 2-byte AudioSpecificConfig for AAC-LC 44100 Hz stereo.
// ObjectType=2 (LC), SampleRateIndex=4 (44100), ChannelConfig=2 (stereo).
var minimalAAC = []byte{0x12, 0x10}

func makeAACCodec(t *testing.T) aacparser.CodecData {
	t.Helper()

	c, err := aacparser.NewCodecDataFromMPEG4AudioConfigBytes(minimalAAC)
	if err != nil {
		t.Fatalf("aacparser: %v", err)
	}

	return c
}

// ── format registry ───────────────────────────────────────────────────────────

// formatSpec describes one container format with factories for its muxer
// and demuxer. Both factories accept *bytes.Reader (which satisfies both
// io.Reader and io.ReadSeeker, as required by the respective constructors).
type formatSpec struct {
	name       string
	newMuxer   func(w io.Writer) av.MuxCloser
	newDemuxer func(r *bytes.Reader) av.DemuxCloser
}

// allFormats lists the supported container formats in a canonical order.
// Tests iterate over this slice to generate all source×destination combinations.
var allFormats = []formatSpec{
	{
		name:       "fmp4",
		newMuxer:   func(w io.Writer) av.MuxCloser { return fmp4.NewMuxer(w) },
		newDemuxer: func(r *bytes.Reader) av.DemuxCloser { return fmp4.NewDemuxer(r) },
	},
	{
		name:       "mp4",
		newMuxer:   func(w io.Writer) av.MuxCloser { return mp4.NewMuxer(w) },
		newDemuxer: func(r *bytes.Reader) av.DemuxCloser { return mp4.NewDemuxer(r) },
	},
}

// ── pipeline helpers ──────────────────────────────────────────────────────────

// muxFmt muxes packets using the given format spec and returns the raw bytes.
func muxFmt(t *testing.T, f formatSpec, streams []av.Stream, pkts []av.Packet) []byte {
	t.Helper()

	var buf bytes.Buffer

	m := f.newMuxer(&buf)
	ctx := context.Background()

	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("[%s] WriteHeader: %v", f.name, err)
	}

	for i, pkt := range pkts {
		if err := m.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("[%s] WritePacket[%d]: %v", f.name, i, err)
		}
	}

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("[%s] WriteTrailer: %v", f.name, err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("[%s] MuxClose: %v", f.name, err)
	}

	return buf.Bytes()
}

func TestMP4Moov_NoMvex(t *testing.T) {
	t.Parallel()

	// Non-fragmented MP4 must NOT contain an mvex box.
	h264 := makeH264Codec(t)
	data := muxFmt(t, allFormats[1], []av.Stream{{Idx: 0, Codec: h264}}, nil)

	if bytes.Contains(data, []byte("mvex")) {
		t.Error("non-fragmented mp4 moov must not contain mvex")
	}
}

func TestMP4Moov_ContainsAvcC(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	data := muxFmt(t, allFormats[1], []av.Stream{{Idx: 0, Codec: h264}}, nil)

	if !bytes.Contains(data, []byte("avcC")) {
		t.Error("mp4 moov does not contain avcC box")
	}
}

func TestMP4Moov_ContainsEsds(t *testing.T) {
	t.Parallel()

	aac := makeAACCodec(t)
	data := muxFmt(t, allFormats[1], []av.Stream{{Idx: 0, Codec: aac}}, nil)

	if !bytes.Contains(data, []byte("esds")) {
		t.Error("mp4 moov does not contain esds box")
	}
}
