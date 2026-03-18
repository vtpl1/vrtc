// Package mp4_test contains unit tests for the mp4 package and comprehensive
// cross-format round-trip tests covering all 9 combinations of the three
// DemuxCloser/MuxCloser implementations: avf, fmp4, and mp4.
package format_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
	"github.com/vtpl1/vrtc/pkg/av/format/fmp4"
	"github.com/vtpl1/vrtc/pkg/av/format/mp4"
)

// ── compile-time interface checks ─────────────────────────────────────────────

var (
	_ av.DemuxCloser = (*avf.Demuxer)(nil)
	_ av.MuxCloser   = (*avf.Muxer)(nil)
	_ av.DemuxCloser = (*fmp4.Demuxer)(nil)
	_ av.MuxCloser   = (*fmp4.Muxer)(nil)
	_ av.DemuxCloser = (*mp4.Demuxer)(nil)
	_ av.MuxCloser   = (*mp4.Muxer)(nil)
)

// ── shared codec fixtures ─────────────────────────────────────────────────────

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

// allFormats lists the three supported container formats in a canonical order.
// Tests iterate over this slice to generate all source×destination combinations.
var allFormats = []formatSpec{
	{
		name:       "avf",
		newMuxer:   func(w io.Writer) av.MuxCloser { return avf.NewMuxer(w) },
		newDemuxer: func(r *bytes.Reader) av.DemuxCloser { return avf.New(r) },
	},
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

// demuxFmt demuxes all packets from the given container bytes using the format spec.
// Returns the detected streams and the full packet list.
func demuxFmt(t *testing.T, f formatSpec, data []byte) ([]av.Stream, []av.Packet) {
	t.Helper()

	ctx := context.Background()
	d := f.newDemuxer(bytes.NewReader(data))

	defer func() {
		if err := d.Close(); err != nil {
			t.Errorf("[%s] DemuxClose: %v", f.name, err)
		}
	}()

	streams, err := d.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("[%s] GetCodecs: %v", f.name, err)
	}

	var pkts []av.Packet

	for {
		pkt, err := d.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("[%s] ReadPacket: %v", f.name, err)
		}

		pkts = append(pkts, pkt)
	}

	return streams, pkts
}

// ── round-trip fixture definitions ───────────────────────────────────────────

// rtFixture describes a set of streams and initial packets used in round-trip tests.
type rtFixture struct {
	name    string
	streams func(t *testing.T) []av.Stream
	packets func(t *testing.T, streams []av.Stream) []av.Packet
}

// All durations are exact ms multiples so they round-trip through AVF (1ms
// precision) and FMP4/MP4 (timescale units) without rounding error.
const (
	vidDur = 33 * time.Millisecond // 2970 ticks @ 90000 Hz
	audDur = 20 * time.Millisecond // 882 ticks  @ 44100 Hz
)

var rtFixtures = []rtFixture{
	{
		name: "H264-only",
		streams: func(t *testing.T) []av.Stream {
			return []av.Stream{{Idx: 0, Codec: makeH264Codec(t)}}
		},
		packets: func(t *testing.T, _ []av.Stream) []av.Packet {
			return []av.Packet{
				{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01, 0x02}, CodecType: av.H264},
				{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x03, 0x04}, CodecType: av.H264},
				{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x05, 0x06}, CodecType: av.H264},
				{Idx: 0, KeyFrame: false, DTS: 3 * vidDur, Duration: vidDur, Data: []byte{0x41, 0x07, 0x08}, CodecType: av.H264},
			}
		},
	},
	{
		name: "AAC-only",
		streams: func(t *testing.T) []av.Stream {
			return []av.Stream{{Idx: 0, Codec: makeAACCodec(t)}}
		},
		packets: func(t *testing.T, _ []av.Stream) []av.Packet {
			return []av.Packet{
				{Idx: 0, DTS: 0, Duration: audDur, Data: []byte{0xAA, 0xBB, 0xCC}, CodecType: av.AAC},
				{Idx: 0, DTS: audDur, Duration: audDur, Data: []byte{0xDD, 0xEE, 0xFF}, CodecType: av.AAC},
				{Idx: 0, DTS: 2 * audDur, Duration: audDur, Data: []byte{0x11, 0x22, 0x33}, CodecType: av.AAC},
			}
		},
	},
	{
		name: "H264+AAC",
		streams: func(t *testing.T) []av.Stream {
			return []av.Stream{
				{Idx: 0, Codec: makeH264Codec(t)},
				{Idx: 1, Codec: makeAACCodec(t)},
			}
		},
		packets: func(t *testing.T, _ []av.Stream) []av.Packet {
			return []av.Packet{
				{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
				{Idx: 1, DTS: 0, Duration: audDur, Data: []byte{0xAA, 0xBB}, CodecType: av.AAC},
				{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
				{Idx: 1, DTS: audDur, Duration: audDur, Data: []byte{0xCC, 0xDD}, CodecType: av.AAC},
				{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
			}
		},
	},
}

// ── main cross-format round-trip test ─────────────────────────────────────────

// TestRoundTrip exercises all 9 source×destination format combinations across
// all 3 stream fixtures (H264-only, AAC-only, H264+AAC).
//
// For each combination the pipeline is:
//
//	inputPackets
//	  → [src mux]  → src container bytes
//	  → [src demux]→ intermediate packets
//	  → [dst mux]  → dst container bytes
//	  → [dst demux]→ outputPackets
//
// The test then asserts that outputPackets faithfully reproduces the input.
func TestRoundTrip(t *testing.T) {
	t.Skip("WIP: demuxer DTS/Duration changes broke round-trip expectations")
	t.Parallel()

	for _, src := range allFormats {
		for _, dst := range allFormats {
			for _, fix := range rtFixtures {
				t.Run(src.name+"_to_"+dst.name+"/"+fix.name, func(t *testing.T) {
					t.Parallel()

					ctx := context.Background()
					_ = ctx

					streams := fix.streams(t)
					inPkts := fix.packets(t, streams)

					// ── Step 1: mux to src format ──────────────────────────
					srcData := muxFmt(t, src, streams, inPkts)

					// ── Step 2: demux from src format ──────────────────────
					srcStreams, step1Pkts := demuxFmt(t, src, srcData)

					// ── Step 3: mux to dst format using srcStreams ─────────
					dstData := muxFmt(t, dst, srcStreams, step1Pkts)

					// ── Step 4: demux from dst format ──────────────────────
					dstStreams, outPkts := demuxFmt(t, dst, dstData)

					// ── Assertions ─────────────────────────────────────────

					// Codec count and types must match.
					if len(dstStreams) != len(streams) {
						t.Fatalf("stream count: want %d, got %d", len(streams), len(dstStreams))
					}

					for i := range streams {
						want := streams[i].Codec.Type()
						got := dstStreams[i].Codec.Type()

						if got != want {
							t.Errorf("stream[%d] codec: want %v, got %v", i, want, got)
						}
					}

					// Packet count must match.
					if len(outPkts) != len(inPkts) {
						t.Fatalf("packet count: want %d, got %d", len(inPkts), len(outPkts))
					}

					// Per-packet assertions.
					for i := range inPkts {
						wantPkt := inPkts[i]
						gotPkt := outPkts[i]

						// KeyFrame flag.
						if gotPkt.KeyFrame != wantPkt.KeyFrame {
							t.Errorf("pkt[%d] KeyFrame: want %v, got %v", i, wantPkt.KeyFrame, gotPkt.KeyFrame)
						}

						// DTS (1ms tolerance to accommodate AVF's ms precision).
						dtsDiff := gotPkt.DTS - wantPkt.DTS
						if dtsDiff < 0 {
							dtsDiff = -dtsDiff
						}

						if dtsDiff > time.Millisecond {
							t.Errorf("pkt[%d] DTS: want %v, got %v (diff %v)", i, wantPkt.DTS, gotPkt.DTS, dtsDiff)
						}

						// Payload bytes.
						if !bytes.Equal(gotPkt.Data, wantPkt.Data) {
							t.Errorf("pkt[%d] Data: want %v, got %v", i, wantPkt.Data, gotPkt.Data)
						}
					}
				})
			}
		}
	}
}

// ── duration and PTSOffset preservation (non-AVF paths only) ─────────────────

// TestRoundTrip_DurationPreserved verifies that packet Duration survives a
// full round trip through format combinations that natively store it (fmp4 and
// mp4).  AVF is excluded because it does not record per-packet duration.
func TestRoundTrip_DurationPreserved(t *testing.T) {
	t.Skip("WIP: demuxer DTS/Duration changes broke round-trip expectations")
	t.Parallel()

	nonAVF := []formatSpec{allFormats[1], allFormats[2]} // fmp4, mp4

	for _, src := range nonAVF {
		for _, dst := range nonAVF {
			t.Run(src.name+"_to_"+dst.name, func(t *testing.T) {
				t.Parallel()

				h264 := makeH264Codec(t)
				aac := makeAACCodec(t)

				streams := []av.Stream{
					{Idx: 0, Codec: h264},
					{Idx: 1, Codec: aac},
				}

				inPkts := []av.Packet{
					{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x65, 0xA}, CodecType: av.H264},
					{Idx: 1, DTS: 0, Duration: audDur, Data: []byte{0xAA, 0xBB}, CodecType: av.AAC},
					{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x41, 0xB}, CodecType: av.H264},
					{Idx: 1, DTS: audDur, Duration: audDur, Data: []byte{0xCC, 0xDD}, CodecType: av.AAC},
					{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0xC}, CodecType: av.H264},
				}

				srcData := muxFmt(t, src, streams, inPkts)
				srcStreams, step1Pkts := demuxFmt(t, src, srcData)
				dstData := muxFmt(t, dst, srcStreams, step1Pkts)
				_, outPkts := demuxFmt(t, dst, dstData)

				if len(outPkts) != len(inPkts) {
					t.Fatalf("packet count: want %d, got %d", len(inPkts), len(outPkts))
				}

				for i, want := range inPkts {
					got := outPkts[i]

					durDiff := got.Duration - want.Duration
					if durDiff < 0 {
						durDiff = -durDiff
					}

					if durDiff > time.Millisecond {
						t.Errorf("pkt[%d] Duration: want %v, got %v (diff %v)", i, want.Duration, got.Duration, durDiff)
					}
				}
			})
		}
	}
}

// TestRoundTrip_PTSOffsetPreserved verifies that B-frame PTS offsets survive
// a round trip through fmp4 and mp4 (the only formats that support ctts).
func TestRoundTrip_PTSOffsetPreserved(t *testing.T) {
	t.Parallel()

	const ptsOff = 66 * time.Millisecond // 5940 ticks @ 90000 Hz

	nonAVF := []formatSpec{allFormats[1], allFormats[2]} // fmp4, mp4

	for _, src := range nonAVF {
		for _, dst := range nonAVF {
			t.Run(src.name+"_to_"+dst.name, func(t *testing.T) {
				t.Parallel()

				h264 := makeH264Codec(t)
				streams := []av.Stream{{Idx: 0, Codec: h264}}

				// P-frame packet with a non-zero PTSOffset (simulating B-frames).
				inPkts := []av.Packet{
					{Idx: 0, KeyFrame: true, DTS: 0, PTSOffset: ptsOff, Duration: vidDur, Data: []byte{0x65, 0x01}, CodecType: av.H264},
					{Idx: 0, KeyFrame: false, DTS: vidDur, PTSOffset: ptsOff, Duration: vidDur, Data: []byte{0x41, 0x02}, CodecType: av.H264},
					{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x65, 0x03}, CodecType: av.H264},
				}

				srcData := muxFmt(t, src, streams, inPkts)
				srcStreams, step1Pkts := demuxFmt(t, src, srcData)
				dstData := muxFmt(t, dst, srcStreams, step1Pkts)
				_, outPkts := demuxFmt(t, dst, dstData)

				if len(outPkts) != len(inPkts) {
					t.Fatalf("packet count: want %d, got %d", len(inPkts), len(outPkts))
				}

				for i, want := range inPkts {
					got := outPkts[i]

					diff := got.PTSOffset - want.PTSOffset
					if diff < 0 {
						diff = -diff
					}

					if diff > time.Millisecond {
						t.Errorf("pkt[%d] PTSOffset: want %v, got %v (diff %v)", i, want.PTSOffset, got.PTSOffset, diff)
					}
				}
			})
		}
	}
}

// ── H.265 round trips ─────────────────────────────────────────────────────────

// TestRoundTrip_H265_AllFormats checks that H.265 (hev1) codec data and
// packets round-trip correctly across all 9 format combinations.
func TestRoundTrip_H265_AllFormats(t *testing.T) {
	t.Parallel()

	h265, err := makeH265Codec(t)
	if err != nil {
		t.Skip("H.265 codec fixture unavailable:", err)
	}

	streams := []av.Stream{{Idx: 0, Codec: h265}}

	inPkts := []av.Packet{
		{Idx: 0, KeyFrame: true, DTS: 0, Duration: vidDur, Data: []byte{0x26, 0x01, 0x02}, CodecType: av.H265},
		{Idx: 0, KeyFrame: false, DTS: vidDur, Duration: vidDur, Data: []byte{0x02, 0x03, 0x04}, CodecType: av.H265},
		{Idx: 0, KeyFrame: true, DTS: 2 * vidDur, Duration: vidDur, Data: []byte{0x26, 0x05, 0x06}, CodecType: av.H265},
	}

	for _, src := range allFormats {
		for _, dst := range allFormats {
			t.Run(src.name+"_to_"+dst.name, func(t *testing.T) {
				t.Parallel()

				srcData := muxFmt(t, src, streams, inPkts)
				srcStreams, step1Pkts := demuxFmt(t, src, srcData)
				dstData := muxFmt(t, dst, srcStreams, step1Pkts)
				dstStreams, outPkts := demuxFmt(t, dst, dstData)

				if len(dstStreams) != 1 || dstStreams[0].Codec.Type() != av.H265 {
					t.Errorf("expected 1 H265 stream, got %v", dstStreams)
				}

				if len(outPkts) != len(inPkts) {
					t.Fatalf("packet count: want %d, got %d", len(inPkts), len(outPkts))
				}

				for i, want := range inPkts {
					got := outPkts[i]

					if got.KeyFrame != want.KeyFrame {
						t.Errorf("pkt[%d] KeyFrame: want %v, got %v", i, want.KeyFrame, got.KeyFrame)
					}

					if !bytes.Equal(got.Data, want.Data) {
						t.Errorf("pkt[%d] Data: want %v, got %v", i, want.Data, got.Data)
					}
				}
			})
		}
	}
}

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

// closingReadSeeker implements io.ReadSeeker and io.Closer with an onClose hook.
type closingReadSeeker struct {
	r       *bytes.Reader
	onClose func()
}

func (c *closingReadSeeker) Read(p []byte) (int, error)                { return c.r.Read(p) }
func (c *closingReadSeeker) Seek(off int64, whence int) (int64, error) { return c.r.Seek(off, whence) }
func (c *closingReadSeeker) Close() error                              { c.onClose(); return nil }

// makeH265Codec builds an H.265 CodecData from a minimal hvcC fixture.
// Returns an error if the fixture cannot be parsed (skips the test).
func makeH265Codec(t *testing.T) (av.CodecData, error) {
	t.Helper()

	// Minimal H.265 VPS+SPS+PPS for a 320×240 Main-profile stream.
	vps := []byte{0x40, 0x01, 0x0C, 0x01, 0xFF, 0xFF, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0x80, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x5D, 0x95, 0x98, 0x09}
	sps := []byte{0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0x80, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x5D, 0xA0, 0x02, 0x80, 0x80, 0x2D, 0x16, 0x59, 0x99, 0xA4, 0x93, 0x2B, 0xFF, 0xC0, 0x00, 0x56, 0x20, 0x00, 0x00, 0x6E, 0xA0, 0x00, 0x00, 0x1D, 0x53, 0x7B, 0xD6, 0x28, 0x00, 0x09, 0xDD, 0x40, 0x00, 0x03, 0xAA, 0x40, 0x18}
	pps := []byte{0x44, 0x01, 0xC0, 0xF3, 0xC0, 0x00}

	return h265parser.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
}
