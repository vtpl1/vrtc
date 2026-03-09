package format_test

// Integration tests: AVF DemuxCloser → fMP4 MuxCloser pipeline.
//
// These tests verify that packets demuxed from an AVF source (both synthetic
// and real on-disk files) can be successfully re-muxed into a Fragmented MP4
// byte stream, and that the resulting output contains structurally valid ISO
// BMFF boxes.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
	"github.com/vtpl1/vrtc/pkg/av/format/llhls"
)

// ── codec fixtures ────────────────────────────────────────────────────────────

// minimalAVCRecord is a synthetic AVCDecoderConfigurationRecord for a 320×240
// H.264 Baseline stream, shared with the fmp4 and avf unit tests.
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

// ── AVF builder helpers ───────────────────────────────────────────────────────

// buildSyntheticAVF uses the AVF muxer to produce a self-contained AVF byte
// stream from streams and packets. Packets that need a Duration for fMP4 must
// have it set by the caller before passing them here.
func buildSyntheticAVF(t *testing.T, streams []av.Stream, pkts []av.Packet) []byte {
	t.Helper()

	var buf bytes.Buffer
	ctx := context.Background()

	m := avf.NewMuxer(&buf)

	if err := m.WriteHeader(ctx, streams); err != nil {
		t.Fatalf("avf mux WriteHeader: %v", err)
	}

	for _, pkt := range pkts {
		if err := m.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("avf mux WritePacket: %v", err)
		}
	}

	if err := m.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("avf mux WriteTrailer: %v", err)
	}

	return buf.Bytes()
}

// ── pipeline helper ───────────────────────────────────────────────────────────

// fmp4SupportedCodec reports whether a codec type is supported by the fMP4 muxer
// (natively or via transcoding).
func fmp4SupportedCodec(ct av.CodecType) bool {
	return ct == av.H264 || ct == av.H265 || ct == av.AAC ||
		ct == av.PCM_MULAW || ct == av.PCM_ALAW
}

// streamRoute describes how a single AVF stream maps into the fMP4 output.
type streamRoute struct {
	fmp4Idx uint16
	codec   av.CodecData
	encode  func([]byte) []byte // nil = passthrough
}

// pipeAVFtoFMP4 runs the full AVF→fMP4 pipeline using dmx and returns the
// fMP4 output bytes. If no stream is supported by the fMP4 muxer it returns
// (nil, false). perPktDuration is assigned to each packet whose Duration is
// zero (AVF does not carry sample duration). PCM_MULAW and PCM_ALAW streams
// are transcoded to FLAC before muxing.
func pipeAVFtoFMP4(
	t *testing.T,
	dmx *avf.Demuxer,
	perPktDuration time.Duration,
) ([]byte, bool) {
	t.Helper()

	ctx := context.Background()

	streams, err := dmx.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("avf GetCodecs: %v", err)
	}

	routes := make(map[uint16]*streamRoute)
	var fmp4Streams []av.Stream
	nextIdx := uint16(0)

	for _, s := range streams {
		switch s.Codec.Type() {
		case av.H264, av.H265, av.AAC:
			routes[s.Idx] = &streamRoute{fmp4Idx: nextIdx, codec: s.Codec}
			fmp4Streams = append(fmp4Streams, av.Stream{Idx: nextIdx, Codec: s.Codec})
			nextIdx++
		case av.PCM_MULAW, av.PCM_ALAW:
			audio, ok := s.Codec.(av.AudioCodecData)
			if !ok {
				continue
			}

			ct := s.Codec.Type()
			flacCodec := pcm.NewFLACCodecData(ct, uint32(audio.SampleRate()), audio.ChannelLayout())
			encoder := pcm.FLACEncoder(ct, uint32(audio.SampleRate()))

			if encoder == nil {
				continue // unsupported sample rate
			}

			routes[s.Idx] = &streamRoute{fmp4Idx: nextIdx, codec: flacCodec, encode: encoder}
			fmp4Streams = append(fmp4Streams, av.Stream{Idx: nextIdx, Codec: flacCodec})
			nextIdx++
		}
	}

	if len(fmp4Streams) == 0 {
		return nil, false
	}

	var out bytes.Buffer

	mx := fmp4.NewMuxer(&out)

	if err := mx.WriteHeader(ctx, fmp4Streams); err != nil {
		t.Fatalf("fmp4 WriteHeader: %v", err)
	}

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("avf ReadPacket: %v", err)
		}

		route := routes[pkt.Idx]
		if route == nil {
			continue
		}

		if pkt.Duration == 0 {
			pkt.Duration = perPktDuration
		}

		pkt.Idx = route.fmp4Idx

		if route.encode != nil {
			pkt.Data = route.encode(pkt.Data)
			pkt.CodecType = av.FLAC

			if fc, ok := route.codec.(pcm.FLACCodecData); ok {
				if d, err := fc.PacketDuration(pkt.Data); err == nil {
					pkt.Duration = d
				}
			}
		}

		if err := mx.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("fmp4 WritePacket: %v", err)
		}
	}

	if err := mx.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("fmp4 WriteTrailer: %v", err)
	}

	return out.Bytes(), true
}

// ── box validation helpers ────────────────────────────────────────────────────

// collectBoxTypes parses top-level ISO BMFF boxes from data and returns their
// four-character-code names in order.
func collectBoxTypes(t *testing.T, data []byte) []string {
	t.Helper()

	r := bytes.NewReader(data)
	var types []string

	for r.Len() >= 8 {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break
		}

		size := binary.BigEndian.Uint32(hdr[0:4])
		typ := string(hdr[4:8])

		if size < 8 {
			t.Errorf("box %q: invalid size %d", typ, size)
			break
		}

		types = append(types, typ)

		// Skip the rest of the box payload.
		if _, err := r.Seek(int64(size-8), io.SeekCurrent); err != nil {
			break
		}
	}

	return types
}

func containsBox(types []string, name string) bool {
	for _, t := range types {
		if t == name {
			return true
		}
	}

	return false
}

// ── synthetic integration tests ───────────────────────────────────────────────

// TestAVFtoFMP4_H264 verifies a synthetic H.264-only AVF stream can be fully
// demuxed and re-muxed into a valid fMP4 byte stream.
func TestAVFtoFMP4_H264(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	const vidDur = 33 * time.Millisecond

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x41, 0x03}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 3 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x04}, CodecType: av.H264},
	}

	avfData := buildSyntheticAVF(t, streams, inPkts)
	fmp4Data, ok := pipeAVFtoFMP4(t, avf.New(bytes.NewReader(avfData)), vidDur)

	if !ok {
		t.Fatal("no supported streams found")
	}

	if len(fmp4Data) == 0 {
		t.Fatal("fMP4 output is empty")
	}

	boxes := collectBoxTypes(t, fmp4Data)

	if !containsBox(boxes, "ftyp") {
		t.Errorf("fMP4 output missing ftyp box; got %v", boxes)
	}

	if !containsBox(boxes, "moov") {
		t.Errorf("fMP4 output missing moov box; got %v", boxes)
	}

	if !containsBox(boxes, "moof") {
		t.Errorf("fMP4 output missing moof box (no fragments emitted); got %v", boxes)
	}

	t.Logf("boxes: %v  total bytes: %d", boxes, len(fmp4Data))
}

// TestAVFtoFMP4_H264WithAAC verifies the pipeline for a mixed H.264+AAC stream.
// AVF supports PCM_MULAW but not AAC directly; here we use the AVF muxer for
// the video stream and pair it with a synthetic demux of an AAC-bearing stream
// by constructing the AVF bytes manually via avf.NewMuxer.
//
// Since avf.Muxer supports writing AAC packets transparently, this is a
// straightforward muxer→demuxer→fMP4 round-trip for the video track only;
// the audio track uses PCM_MULAW (unsupported by fMP4) to exercise the
// codec-filtering path.
func TestAVFtoFMP4_H264_AudioFiltered(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)

	// Stream 0 = H264 (supported by fMP4), Stream 1 = PCM_MULAW (unsupported).
	streams := []av.Stream{
		{Idx: 0, Codec: h264},
		// PCM_MULAW is valid AVF audio but unsupported by fMP4; the pipeline
		// should silently skip it.
		{Idx: 1, Codec: pcmMulawCodec{}},
	}

	const vidDur = 33 * time.Millisecond

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
		{Idx: 1, DTS: 0, Duration: 20 * time.Millisecond, Data: []byte{0xAA, 0xBB}, CodecType: av.PCM_MULAW},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
	}

	avfData := buildSyntheticAVF(t, streams, inPkts)
	fmp4Data, ok := pipeAVFtoFMP4(t, avf.New(bytes.NewReader(avfData)), vidDur)

	if !ok {
		t.Fatal("no supported streams found")
	}

	boxes := collectBoxTypes(t, fmp4Data)

	if !containsBox(boxes, "ftyp") || !containsBox(boxes, "moov") {
		t.Errorf("init segment incomplete; boxes=%v", boxes)
	}

	if !containsBox(boxes, "moof") {
		t.Errorf("no fragments emitted; boxes=%v", boxes)
	}

	// Re-demux fMP4 to verify only one stream (the H264 video) was written.
	fmx := fmp4.NewDemuxer(bytes.NewReader(fmp4Data))
	gotStreams, err := fmx.GetCodecs(context.Background())
	if err != nil {
		t.Fatalf("fmp4 GetCodecs after round-trip: %v", err)
	}

	if len(gotStreams) != 1 {
		t.Errorf("want 1 stream after filter, got %d", len(gotStreams))
	}

	if len(gotStreams) > 0 && gotStreams[0].Codec.Type() != av.H264 {
		t.Errorf("want H264 stream, got %v", gotStreams[0].Codec.Type())
	}
}

// TestAVFtoFMP4_MultipleKeyframeFragments verifies that two keyframes result
// in two separate moof fragments in the fMP4 output.
func TestAVFtoFMP4_MultipleKeyframeFragments(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	const vidDur = 33 * time.Millisecond

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: 3 * vidDur, Duration: vidDur, Data: []byte{0x41, 0x04}, CodecType: av.H264},
	}

	avfData := buildSyntheticAVF(t, streams, inPkts)
	fmp4Data, ok := pipeAVFtoFMP4(t, avf.New(bytes.NewReader(avfData)), vidDur)

	if !ok {
		t.Fatal("no supported streams found")
	}

	boxes := collectBoxTypes(t, fmp4Data)

	moofCount := 0
	for _, b := range boxes {
		if b == "moof" {
			moofCount++
		}
	}

	// Two keyframes → two fragments (first flushed on second keyframe, second
	// flushed on WriteTrailer).
	if moofCount < 2 {
		t.Errorf("want ≥2 moof boxes (one per keyframe group), got %d; boxes=%v",
			moofCount, boxes)
	}

	t.Logf("moof count: %d  boxes: %v", moofCount, boxes)
}

// TestAVFtoFMP4_RoundTrip verifies that packets survive the full
// AVF-mux → AVF-demux → fMP4-mux → fMP4-demux pipeline with codec type and
// keyframe flag preserved.
func TestAVFtoFMP4_RoundTrip(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	const vidDur = 33 * time.Millisecond

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0xAA}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0xBB}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x41, 0xCC}, CodecType: av.H264},
	}

	avfData := buildSyntheticAVF(t, streams, inPkts)
	fmp4Data, ok := pipeAVFtoFMP4(t, avf.New(bytes.NewReader(avfData)), vidDur)

	if !ok {
		t.Fatal("no supported streams found")
	}

	// Demux the fMP4 output and verify packets.
	ctx := context.Background()
	dmx := fmp4.NewDemuxer(bytes.NewReader(fmp4Data))

	gotStreams, err := dmx.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("fmp4 GetCodecs: %v", err)
	}

	if len(gotStreams) == 0 {
		t.Fatal("no streams after round-trip")
	}

	if gotStreams[0].Codec.Type() != av.H264 {
		t.Errorf("codec: want H264, got %v", gotStreams[0].Codec.Type())
	}

	var outPkts []av.Packet

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("fmp4 ReadPacket: %v", err)
		}

		outPkts = append(outPkts, pkt)
	}

	if len(outPkts) != len(inPkts) {
		t.Fatalf("want %d packets, got %d", len(inPkts), len(outPkts))
	}

	for i, want := range inPkts {
		got := outPkts[i]

		if got.KeyFrame != want.KeyFrame {
			t.Errorf("pkt[%d]: KeyFrame want %v got %v", i, want.KeyFrame, got.KeyFrame)
		}

		if got.CodecType != want.CodecType {
			t.Errorf("pkt[%d]: CodecType want %v got %v", i, want.CodecType, got.CodecType)
		}

		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("pkt[%d]: Data want %v got %v", i, want.Data, got.Data)
		}
	}
}

// ── real-file integration tests ───────────────────────────────────────────────

// testDataDir is the path to the on-disk AVF fixture files, relative to this
// package's directory (pkg/av/format/).
const testDataDir = "../../../test_data"

func realAVFFiles(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(testDataDir, "*.avf"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	if len(paths) == 0 {
		t.Skipf("no *.avf files in %s", testDataDir)
	}

	return paths
}

// TestAVFtoFMP4_RealFiles_UnsupportedCodecSkipped opens every real AVF file,
// filters its streams to the fMP4-supported subset, and verifies the pipeline
// either produces valid fMP4 output or correctly identifies that no supported
// stream is present.
func TestAVFtoFMP4_RealFiles_UnsupportedCodecSkipped(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			f, err := avf.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			avfStreams, err := f.GetCodecs(context.Background())
			f.Close()

			if err != nil {
				t.Fatalf("GetCodecs: %v", err)
			}

			// Count supported streams.
			nSupported := 0
			for _, s := range avfStreams {
				if fmp4SupportedCodec(s.Codec.Type()) {
					nSupported++
				}
			}

			// Re-open to feed pipeAVFtoFMP4.
			f2, err := avf.Open(path)
			if err != nil {
				t.Fatalf("re-Open: %v", err)
			}
			defer f2.Close()

			fmp4Data, ok := pipeAVFtoFMP4(t, f2, 20*time.Millisecond)

			if nSupported == 0 {
				if ok {
					t.Error("expected no supported streams, but pipeline returned output")
				} else {
					t.Logf("correctly identified no fMP4-supported streams in %s", filepath.Base(path))
				}

				return
			}

			// At least one supported stream — validate the output.
			if !ok {
				t.Fatal("expected fMP4 output but pipeline returned nothing")
			}

			boxes := collectBoxTypes(t, fmp4Data)

			if !containsBox(boxes, "ftyp") || !containsBox(boxes, "moov") {
				t.Errorf("fMP4 init segment missing; boxes=%v", boxes)
			}

			t.Logf("boxes=%v bytes=%d", boxes, len(fmp4Data))
		})
	}
}

// TestAVFtoFMP4_RealFiles_OutputNonEmpty verifies that for files with at least
// one fMP4-supported codec the output is non-empty and structurally parseable.
func TestAVFtoFMP4_RealFiles_OutputNonEmpty(t *testing.T) {
	t.Parallel()

	anySupported := false

	for _, path := range realAVFFiles(t) {
		f, err := avf.Open(path)
		if err != nil {
			t.Errorf("%s: Open: %v", filepath.Base(path), err)
			continue
		}

		fmp4Data, ok := pipeAVFtoFMP4(t, f, 20*time.Millisecond)
		f.Close()

		if !ok {
			continue // no supported or transcodeable streams
		}

		anySupported = true

		if len(fmp4Data) == 0 {
			t.Errorf("%s: fMP4 output empty", filepath.Base(path))
			continue
		}

		boxes := collectBoxTypes(t, fmp4Data)

		if !containsBox(boxes, "ftyp") || !containsBox(boxes, "moov") {
			t.Errorf("%s: init segment incomplete; boxes=%v", filepath.Base(path), boxes)
		}
	}

	if !anySupported {
		t.Log("all real files contain only unsupported codecs — fMP4 output validation skipped")
	}
}

// TestAVFtoFMP4_RealFiles converts every real .avf file to fMP4 and writes the
// result to test_data/fmp4/<name>.fmp4 for manual inspection (e.g. with
// ffprobe or mp4box).
//
// PCM_MULAW and PCM_ALAW audio streams are transcoded to FLAC before muxing.
// Files with no supported or transcodeable streams are skipped and logged.
// The test never fails because of an unsupported codec — it only fails if the
// pipeline errors or writes a structurally broken fMP4 file.
//
// Run with:
//
//	go test ./pkg/av/format/ -run TestAVFtoFMP4_RealFiles -v
func TestAVFtoFMP4_RealFiles(t *testing.T) {
	outDir := filepath.Join(testDataDir, "fmp4")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create output dir %s: %v", outDir, err)
	}

	t.Logf("fMP4 output directory: %s", outDir)

	paths := realAVFFiles(t)
	written, skipped := 0, 0

	for _, path := range paths {
		base := filepath.Base(path)
		stem := base[:len(base)-len(filepath.Ext(base))]
		outPath := filepath.Join(outDir, stem+".mp4")

		dmx, err := avf.Open(path)
		if err != nil {
			t.Errorf("%s: Open: %v", base, err)
			continue
		}

		fmp4Data, ok := pipeAVFtoFMP4(t, dmx, 20*time.Millisecond)
		dmx.Close()

		if !ok {
			t.Logf("SKIP %s — no fMP4-supported streams (codec unsupported)", base)
			skipped++
			continue
		}

		// Structural sanity check before writing.
		boxes := collectBoxTypes(t, fmp4Data)

		if !containsBox(boxes, "ftyp") || !containsBox(boxes, "moov") {
			t.Errorf("%s: fMP4 init segment incomplete; boxes=%v", base, boxes)
			continue
		}

		if err := os.WriteFile(outPath, fmp4Data, 0o644); err != nil {
			t.Errorf("%s: write %s: %v", base, outPath, err)
			continue
		}

		t.Logf("WROTE %s  boxes=%v  size=%d B", outPath, boxes, len(fmp4Data))
		written++
	}

	t.Logf("summary: %d written, %d skipped (unsupported codec), %d total",
		written, skipped, len(paths))

	if written == 0 {
		t.Logf("no fMP4 files were written — all real files were skipped or errored")
	}
}

// ── AVF → LLHLS integration tests ────────────────────────────────────────────

// pipeAVFtoLLHLS runs the full AVF demuxer → LL-HLS muxer pipeline and returns
// the muxer so callers can issue HTTP requests against its handler.
// It only muxes H.264/H.265/AAC streams; others are silently skipped.
// Returns (nil, false) when no supported stream is present.
func pipeAVFtoLLHLS(
	t *testing.T,
	dmx *avf.Demuxer,
	cfg llhls.Config,
) (*llhls.Muxer, bool) {
	t.Helper()

	ctx := context.Background()

	streams, err := dmx.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("avf GetCodecs: %v", err)
	}

	// Filter to streams the LLHLS muxer (via fMP4) supports.
	var hlsStreams []av.Stream
	supported := make(map[uint16]bool)

	for _, s := range streams {
		switch s.Codec.Type() {
		case av.H264, av.H265, av.AAC:
			hlsStreams = append(hlsStreams, s)
			supported[s.Idx] = true
		}
	}

	if len(hlsStreams) == 0 {
		return nil, false
	}

	mx := llhls.NewMuxer(cfg)

	if err := mx.WriteHeader(ctx, hlsStreams); err != nil {
		t.Fatalf("llhls WriteHeader: %v", err)
	}

	for {
		pkt, err := dmx.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("avf ReadPacket: %v", err)
		}

		if !supported[pkt.Idx] {
			continue
		}

		if err := mx.WritePacket(ctx, pkt); err != nil {
			t.Fatalf("llhls WritePacket: %v", err)
		}
	}

	if err := mx.WriteTrailer(ctx, nil); err != nil {
		t.Fatalf("llhls WriteTrailer: %v", err)
	}

	return mx, true
}

// TestAVFtoLLHLS_Synthetic verifies that a synthetic H.264 AVF stream produces
// a valid LL-HLS playlist with at least one part and a reachable init segment.
func TestAVFtoLLHLS_Synthetic(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	streams := []av.Stream{{Idx: 0, Codec: h264}}

	const vidDur = 100 * time.Millisecond // 10 fps

	// 3 keyframes → at least 2 complete parts (each keyframe boundary = new part).
	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
		{Idx: 0, KeyFrame: false, DTS: 3 * vidDur, Duration: vidDur, Data: []byte{0x41, 0x04}, CodecType: av.H264},
		{Idx: 0, KeyFrame: true, DTS: 4 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x05}, CodecType: av.H264},
	}

	avfData := buildSyntheticAVF(t, streams, inPkts)

	cfg := llhls.DefaultConfig()
	cfg.PartTarget = 150 * time.Millisecond // 1–2 frames per part at 10 fps

	mx, ok := pipeAVFtoLLHLS(t, avf.New(bytes.NewReader(avfData)), cfg)
	if !ok {
		t.Fatal("no supported streams found")
	}

	srv := httptest.NewServer(mx.Handler("/hls"))
	defer srv.Close()

	// init segment must be present and non-empty.
	resp, err := http.Get(srv.URL + "/hls/init.mp4")
	if err != nil {
		t.Fatalf("GET init.mp4: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init.mp4: want 200, got %d", resp.StatusCode)
	}

	initBody, _ := io.ReadAll(resp.Body)
	if len(initBody) == 0 {
		t.Fatal("init.mp4: empty response")
	}

	boxes := collectBoxTypes(t, initBody)
	if !containsBox(boxes, "ftyp") || !containsBox(boxes, "moov") {
		t.Errorf("init.mp4: missing ftyp/moov; boxes=%v", boxes)
	}

	// playlist must be present and contain required LL-HLS tags.
	resp2, err := http.Get(srv.URL + "/hls/index.m3u8")
	if err != nil {
		t.Fatalf("GET index.m3u8: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("index.m3u8: want 200, got %d", resp2.StatusCode)
	}

	playlist, _ := io.ReadAll(resp2.Body)
	playlistStr := string(playlist)

	for _, tag := range []string{
		"#EXTM3U",
		"#EXT-X-TARGETDURATION",
		"#EXT-X-SERVER-CONTROL",
		"#EXT-X-PART-INF",
		"#EXT-X-MAP",
	} {
		if !strings.Contains(playlistStr, tag) {
			t.Errorf("index.m3u8 missing tag %q", tag)
		}
	}

	t.Logf("init.mp4 boxes=%v size=%d", boxes, len(initBody))
	t.Logf("playlist:\n%s", playlistStr)
}

// TestAVFtoLLHLS_RealFiles runs the AVF→LLHLS pipeline on every real .avf file,
// verifies the playlist and init segment are structurally valid, and confirms
// that each #EXT-X-PART URI is reachable.
func TestAVFtoLLHLS_RealFiles(t *testing.T) {
	for _, path := range realAVFFiles(t) {
		base := filepath.Base(path)

		t.Run(base, func(t *testing.T) {
			dmx, err := avf.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer dmx.Close()

			cfg := llhls.DefaultConfig()
			cfg.PartTarget = 500 * time.Millisecond
			cfg.SegTarget = 2 * time.Second

			mx, ok := pipeAVFtoLLHLS(t, dmx, cfg)
			if !ok {
				t.Skipf("no LLHLS-supported streams in %s", base)
			}

			srv := httptest.NewServer(mx.Handler("/hls"))
			defer srv.Close()

			// ── init segment ──────────────────────────────────────────────────

			initResp, err := http.Get(srv.URL + "/hls/init.mp4")
			if err != nil {
				t.Fatalf("GET init.mp4: %v", err)
			}
			defer initResp.Body.Close()

			if initResp.StatusCode != http.StatusOK {
				t.Fatalf("init.mp4: want 200, got %d", initResp.StatusCode)
			}

			initBody, _ := io.ReadAll(initResp.Body)
			initBoxes := collectBoxTypes(t, initBody)

			if !containsBox(initBoxes, "ftyp") || !containsBox(initBoxes, "moov") {
				t.Errorf("init.mp4 missing ftyp/moov; boxes=%v", initBoxes)
			}

			// ── playlist ──────────────────────────────────────────────────────

			plResp, err := http.Get(srv.URL + "/hls/index.m3u8")
			if err != nil {
				t.Fatalf("GET index.m3u8: %v", err)
			}
			defer plResp.Body.Close()

			if plResp.StatusCode != http.StatusOK {
				t.Fatalf("index.m3u8: want 200, got %d", plResp.StatusCode)
			}

			playlist, _ := io.ReadAll(plResp.Body)
			playlistStr := string(playlist)

			for _, tag := range []string{"#EXTM3U", "#EXT-X-TARGETDURATION", "#EXT-X-MAP"} {
				if !strings.Contains(playlistStr, tag) {
					t.Errorf("playlist missing %q", tag)
				}
			}

			// ── part reachability ─────────────────────────────────────────────

			partCount := 0
			for _, line := range strings.Split(playlistStr, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "#EXT-X-PART:") {
					continue
				}

				// Extract URI= value.
				uri := ""
				for _, field := range strings.Split(strings.TrimPrefix(line, "#EXT-X-PART:"), ",") {
					if strings.HasPrefix(field, "URI=") {
						uri = strings.Trim(strings.TrimPrefix(field, "URI="), "\"")
					}
				}

				if uri == "" {
					continue
				}

				pr, err := http.Get(srv.URL + "/hls/" + uri)
				if err != nil {
					t.Errorf("GET part %s: %v", uri, err)
					continue
				}
				partBody, _ := io.ReadAll(pr.Body)
				pr.Body.Close()

				if pr.StatusCode != http.StatusOK {
					t.Errorf("part %s: want 200, got %d", uri, pr.StatusCode)
					continue
				}

				partBoxes := collectBoxTypes(t, partBody)
				if !containsBox(partBoxes, "moof") || !containsBox(partBoxes, "mdat") {
					t.Errorf("part %s missing moof/mdat; boxes=%v", uri, partBoxes)
				}

				partCount++
			}

			t.Logf("init=%d B  parts=%d  playlist=%d B",
				len(initBody), partCount, len(playlist))
		})
	}
}

// ── stub codec for testing the filter path ────────────────────────────────────

// pcmMulawCodec is a minimal av.CodecData stub for PCM µ-law, used to build
// synthetic multi-stream AVF data that exercises the stream-filter code path.
type pcmMulawCodec struct{}

func (pcmMulawCodec) Type() av.CodecType { return av.PCM_MULAW }
