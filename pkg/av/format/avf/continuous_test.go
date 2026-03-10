package avf_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
)

// minimalAVCRecord is a synthetic AVCDecoderConfigurationRecord shared by all
// continuous-demuxer tests.
var minimalAVCRecordCont = []byte{
	0x01, 0x42, 0x00, 0x1E, 0xFF, 0xE1, 0x00, 0x0F,
	0x67, 0x42, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0xA0,
	0x3D, 0xA1, 0x00, 0x00, 0x03, 0x00, 0x00, 0x01,
	0x00, 0x04, 0x68, 0xCE, 0x38, 0x80,
}

// buildAVFFiles writes nFiles synthetic AVF files into dir, each containing
// framesPerFile video frames at vidDur cadence. Each file's timestamps start
// at a large epoch offset (simulating real camera wall-clock timestamps) so
// that the continuous demuxer's normalisation is exercised.
func buildAVFFiles(t *testing.T, dir string, nFiles, framesPerFile int, vidDur time.Duration) {
	t.Helper()

	ctx := context.Background()

	codec, err := h264parser.NewCodecDataFromAVCDecoderConfRecord(minimalAVCRecordCont)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}

	streams := []av.Stream{{Idx: 0, Codec: codec}}

	for i := range nFiles {
		path := fmt.Sprintf("%s/file%03d.avf", dir, i)
		m, err := avf.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}

		if err := m.WriteHeader(ctx, streams); err != nil {
			t.Fatalf("WriteHeader %s: %v", path, err)
		}

		// Large per-file epoch offset simulates camera wall-clock DTS values.
		baseTS := time.Duration(i) * 3600 * time.Second

		for f := range framesPerFile {
			pkt := av.Packet{
				Idx:       0,
				KeyFrame:  f == 0,
				DTS:       baseTS + time.Duration(f)*vidDur,
				Duration:  vidDur,
				Data:      []byte{0x65, byte(i*framesPerFile + f)},
				CodecType: av.H264,
			}
			if err := m.WritePacket(ctx, pkt); err != nil {
				t.Fatalf("WritePacket %s/%d: %v", path, f, err)
			}
		}

		if err := m.WriteTrailer(ctx, nil); err != nil {
			t.Fatalf("WriteTrailer %s: %v", path, err)
		}

		_ = m.Close()
	}
}

// TestContinuousDemuxer_MonotonicDTS verifies that DTS values emitted across
// multiple synthetic files (and across a full loop) are strictly increasing
// with no unreasonable gaps.
func TestContinuousDemuxer_MonotonicDTS(t *testing.T) {
	t.Parallel()

	const (
		nFiles        = 3
		framesPerFile = 4
		vidDur        = 100 * time.Millisecond
	)

	dir := t.TempDir()
	buildAVFFiles(t, dir, nFiles, framesPerFile, vidDur)

	cdmx, err := avf.NewContinuous(dir)
	if err != nil {
		t.Fatalf("NewContinuous: %v", err)
	}
	defer cdmx.Close()

	ctx := context.Background()

	if _, err := cdmx.GetCodecs(ctx); err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	// Read two full passes through the directory to exercise the wrap-around.
	totalWant := nFiles * framesPerFile * 2
	pkts := make([]av.Packet, 0, totalWant)

	for len(pkts) < totalWant {
		pkt, err := cdmx.ReadPacket(ctx)
		if err != nil {
			t.Fatalf("ReadPacket[%d]: %v", len(pkts), err)
		}

		pkts = append(pkts, pkt)
	}

	// Strict monotonic DTS check.
	for i := 1; i < len(pkts); i++ {
		if pkts[i].DTS <= pkts[i-1].DTS {
			t.Errorf("DTS not monotonic at pkt[%d]: %v <= %v",
				i, pkts[i].DTS, pkts[i-1].DTS)
		}
	}

	// No large gaps at file/loop boundaries (allow up to 2× frame duration).
	for i := 1; i < len(pkts); i++ {
		gap := pkts[i].DTS - pkts[i-1].DTS
		if gap > 2*vidDur {
			t.Errorf("DTS gap at pkt[%d] (file boundary?): %v (want ≤ %v)",
				i, gap, 2*vidDur)
		}
	}

	t.Logf("emitted %d packets; DTS [%v … %v]",
		len(pkts), pkts[0].DTS, pkts[len(pkts)-1].DTS)
}

// TestContinuousDemuxer_RealFiles reads every video packet across all real
// *.avf files and verifies that the adjusted DTS is strictly monotonic,
// including at file-boundary transitions.
//
// Audio frames are excluded (WithDisableAudio).  The test simulates
// ContinuousDemuxer's DTS-normalization logic file-by-file so that it can
// also report which file and packet index triggered a regression.
func TestContinuousDemuxer_RealFiles(t *testing.T) {
	const testDataDir = "../../../../test_data"

	// Use ContinuousDemuxer only to obtain the canonical sorted file list.
	cdmx, err := avf.NewContinuous(testDataDir, avf.WithDisableAudio())
	if err != nil {
		t.Skipf("no avf files: %v", err)
	}
	_ = cdmx.Close()

	files := cdmx.Files()
	t.Logf("testing %d avf files", len(files))

	ctx := context.Background()

	// DTS-normalization state — mirrors ContinuousDemuxer internals.
	var (
		dtsAccum    time.Duration
		rawFileBase time.Duration
		lastDTS     time.Duration
		lastDur     time.Duration
		fileSeenPkt bool

		totalPkts      int
		withinFileRegs int // non-monotonic steps inside a single file
		boundaryRegs   int // non-monotonic steps at file transitions
	)

	for fi, path := range files {
		// At each file boundary record what the first adjDTS of this file
		// must exceed (the last adjDTS of the previous file).
		boundaryFloor := lastDTS + lastDur

		// Accumulate offset (same as advanceFile).
		if fi > 0 {
			dtsAccum = lastDTS + lastDur
		}
		fileSeenPkt = false

		dmx, err := avf.Open(path, avf.WithDisableAudio())
		if err != nil {
			t.Fatalf("file[%d] open %s: %v", fi, path, err)
		}

		if _, err := dmx.GetCodecs(ctx); err != nil {
			_ = dmx.Close()
			t.Fatalf("file[%d] GetCodecs %s: %v", fi, path, err)
		}

		var (
			filePkts    int
			firstAdjDTS time.Duration
			prevAdjDTS  time.Duration
			fileRegs    int
		)

		for {
			pkt, err := dmx.ReadPacket(ctx)
			if err != nil {
				break
			}

			if !fileSeenPkt {
				rawFileBase = pkt.DTS
				fileSeenPkt = true
			}

			adjDTS := (pkt.DTS - rawFileBase) + dtsAccum

			if filePkts == 0 {
				firstAdjDTS = adjDTS
				// ── file-boundary check ──────────────────────────────────
				if fi > 0 && firstAdjDTS <= boundaryFloor-lastDur {
					// First packet of new file must start after the last
					// adjusted DTS of the previous file.
					t.Errorf("boundary regression file[%d→%d]: firstAdjDTS=%v <= prevLastDTS=%v (accum=%v)",
						fi-1, fi, firstAdjDTS, lastDTS, dtsAccum)
					boundaryRegs++
				}
			} else if adjDTS <= prevAdjDTS {
				// ── within-file regression ───────────────────────────────
				fileRegs++
				withinFileRegs++
			}

			lastDTS = adjDTS
			lastDur = pkt.Duration
			prevAdjDTS = adjDTS
			filePkts++
			totalPkts++
		}

		_ = dmx.Close()
		t.Logf("file[%02d] %s — %d video pkts, first=%v last=%v within-file-regs=%d",
			fi, path, filePkts, firstAdjDTS, lastDTS, fileRegs)
	}

	t.Logf("total: %d video packets across %d files; within-file regressions=%d boundary-regressions=%d",
		totalPkts, len(files), withinFileRegs, boundaryRegs)
}
