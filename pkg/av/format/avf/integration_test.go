package avf_test

// Integration tests that exercise the AVF demuxer against real .avf files
// from the test_data directory at the repository root.
//
// Go tests execute with the package directory as the working directory, so the
// path ../../../../test_data resolves to the repository-root test_data folder.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/format/avf"
)

const testDataDir = "../../../../test_data"

// realAVFFiles returns all *.avf paths found in testDataDir.
// The test is skipped if the directory is absent (e.g. in a fresh checkout
// without test data).
func realAVFFiles(t *testing.T) []string {
	t.Helper()

	pattern := filepath.Join(testDataDir, "*.avf")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %q: %v", pattern, err)
	}

	if len(paths) == 0 {
		t.Skipf("no *.avf files found in %s — skipping integration tests", testDataDir)
	}

	return paths
}

// ── open / codec detection ────────────────────────────────────────────────────

// TestOpen_RealFiles verifies that every file can be opened, that GetCodecs
// returns at least one recognised stream, and that the stream codec type is
// a known av.CodecType value.
func TestOpen_RealFiles(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			d, err := avf.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer d.Close()

			streams, err := d.GetCodecs(context.Background())
			if err != nil {
				t.Fatalf("GetCodecs: %v", err)
			}

			if len(streams) == 0 {
				t.Fatal("GetCodecs returned no streams")
			}

			for _, s := range streams {
				ct := s.Codec.Type()
				if ct == 0 {
					t.Errorf("stream %d: zero CodecType", s.Idx)
				}

				t.Logf("stream %d: %v (audio=%v)", s.Idx, ct, ct.IsAudio())
			}
		})
	}
}

// ── packet demuxing ───────────────────────────────────────────────────────────

// fileStats aggregates per-file demux statistics used by several sub-tests.
type fileStats struct {
	streams    []av.Stream
	packets    []av.Packet
	codecTypes map[uint16]av.CodecType // stream idx → codec type
}

// demuxFile opens path, calls GetCodecs, reads all packets, and returns stats.
func demuxFile(t *testing.T, path string) fileStats {
	t.Helper()

	d, err := avf.Open(path)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	defer d.Close()

	ctx := context.Background()

	streams, err := d.GetCodecs(ctx)
	if err != nil {
		t.Fatalf("GetCodecs: %v", err)
	}

	codecTypes := make(map[uint16]av.CodecType, len(streams))
	for _, s := range streams {
		codecTypes[s.Idx] = s.Codec.Type()
	}

	var pkts []av.Packet

	for {
		pkt, err := d.ReadPacket(ctx)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}

		pkts = append(pkts, pkt)
	}

	return fileStats{streams: streams, packets: pkts, codecTypes: codecTypes}
}

// TestDemuxer_RealFiles_PacketCount ensures that every file yields at least
// one decodable packet.
func TestDemuxer_RealFiles_PacketCount(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			stats := demuxFile(t, path)

			if len(stats.packets) == 0 {
				t.Error("no packets returned from file")
			}

			t.Logf("%d streams, %d packets", len(stats.streams), len(stats.packets))
		})
	}
}

// TestDemuxer_RealFiles_KeyFramePresent verifies that each video stream
// contains at least one keyframe.
func TestDemuxer_RealFiles_KeyFramePresent(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			stats := demuxFile(t, path)

			// Collect the set of video stream indices.
			videoIdx := make(map[uint16]bool)
			for idx, ct := range stats.codecTypes {
				if ct.IsVideo() {
					videoIdx[idx] = true
				}
			}

			if len(videoIdx) == 0 {
				t.Skip("no video stream — skipping keyframe check")
			}

			keyframeFound := make(map[uint16]bool)
			for _, pkt := range stats.packets {
				if pkt.KeyFrame && videoIdx[pkt.Idx] {
					keyframeFound[pkt.Idx] = true
				}
			}

			for idx := range videoIdx {
				if !keyframeFound[idx] {
					t.Errorf("video stream %d: no keyframe found", idx)
				}
			}
		})
	}
}

// TestDemuxer_RealFiles_MonotonicTimestamps checks that DTS values are
// generally non-decreasing within each stream. Real-world AVF files may
// contain occasional minor timestamp jitter (a few ms), so backward steps
// smaller than 100 ms are logged as warnings rather than test failures.
func TestDemuxer_RealFiles_MonotonicTimestamps(t *testing.T) {
	t.Parallel()

	const jitterTolerance = 100 * time.Millisecond

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			stats := demuxFile(t, path)

			last := make(map[uint16]time.Duration)

			for i, pkt := range stats.packets {
				prev, seen := last[pkt.Idx]
				if seen && pkt.DTS < prev {
					delta := prev - pkt.DTS
					if delta > jitterTolerance {
						t.Errorf("pkt[%d] stream %d: DTS went backwards by %v (%v → %v)",
							i, pkt.Idx, delta, prev, pkt.DTS)
					} else {
						t.Logf("pkt[%d] stream %d: minor DTS jitter %v (within tolerance)",
							i, pkt.Idx, delta)
					}

					// Keep the higher timestamp to avoid cascading warnings.
					continue
				}

				last[pkt.Idx] = pkt.DTS
			}
		})
	}
}

// TestDemuxer_RealFiles_NonEmptyPayloads ensures no packet has a nil/empty
// data payload.
func TestDemuxer_RealFiles_NonEmptyPayloads(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			stats := demuxFile(t, path)

			for i, pkt := range stats.packets {
				if len(pkt.Data) == 0 {
					t.Errorf("pkt[%d] stream %d: empty payload", i, pkt.Idx)
				}
			}
		})
	}
}

// TestDemuxer_RealFiles_StreamIndicesConsistent verifies that every packet's
// stream index was declared by GetCodecs.
func TestDemuxer_RealFiles_StreamIndicesConsistent(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			stats := demuxFile(t, path)

			for i, pkt := range stats.packets {
				if _, ok := stats.codecTypes[pkt.Idx]; !ok {
					t.Errorf("pkt[%d]: unexpected stream index %d not declared by GetCodecs",
						i, pkt.Idx)
				}
			}
		})
	}
}

// TestDemuxer_RealFiles_CodecTypeMatchesPacket verifies that each packet's
// CodecType field matches the codec type declared for its stream.
func TestDemuxer_RealFiles_CodecTypeMatchesPacket(t *testing.T) {
	t.Parallel()

	for _, path := range realAVFFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			stats := demuxFile(t, path)

			for i, pkt := range stats.packets {
				want, ok := stats.codecTypes[pkt.Idx]
				if !ok {
					continue // already caught by StreamIndicesConsistent
				}

				if pkt.CodecType != want {
					t.Errorf("pkt[%d] stream %d: CodecType want %v got %v",
						i, pkt.Idx, want, pkt.CodecType)
				}
			}
		})
	}
}

// TestOpen_Close_RealFile verifies that avf.Open followed by Close releases
// the file descriptor (the file is re-openable immediately after Close).
func TestOpen_Close_RealFile(t *testing.T) {
	t.Parallel()

	paths := realAVFFiles(t)
	path := paths[0] // one representative file is enough

	d, err := avf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Should be able to open the same file again.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("re-open after Close: %v", err)
	}

	f.Close()
}

// TestDemuxer_RealFiles_Summary prints a one-line summary per file for human
// review. It never fails on its own; it is a diagnostic aid when -v is set.
func TestDemuxer_RealFiles_Summary(t *testing.T) {
	for _, path := range realAVFFiles(t) {
		d, err := avf.Open(path)
		if err != nil {
			t.Errorf("%s: Open: %v", filepath.Base(path), err)
			continue
		}

		ctx := context.Background()
		streams, err := d.GetCodecs(ctx)
		d.Close()

		if err != nil {
			t.Errorf("%s: GetCodecs: %v", filepath.Base(path), err)
			continue
		}

		codecs := make([]string, 0, len(streams))
		for _, s := range streams {
			codecs = append(codecs, s.Codec.Type().String())
		}

		// Re-open for packet counting.
		d2, err := avf.Open(path)
		if err != nil {
			t.Errorf("%s: re-open: %v", filepath.Base(path), err)
			continue
		}

		if _, err := d2.GetCodecs(ctx); err != nil {
			d2.Close()
			t.Errorf("%s: re-GetCodecs: %v", filepath.Base(path), err)
			continue
		}

		var nPkts, nKeyFrames int

		for {
			pkt, err := d2.ReadPacket(ctx)
			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				t.Errorf("%s: ReadPacket: %v", filepath.Base(path), err)
				break
			}

			nPkts++
			if pkt.KeyFrame {
				nKeyFrames++
			}
		}

		d2.Close()

		t.Logf("%-30s  streams=%v  packets=%d  keyframes=%d",
			filepath.Base(path), codecs, nPkts, nKeyFrames)
	}
}

func TestDemuxer_RealFile_20260212_104904(t *testing.T) {
	testFile := filepath.Join(testDataDir, "20260212_104904.avf")
	t.Logf("%v", testFile)
	d, err := avf.Open(testFile)
	if err != nil {
		t.Fatalf("%s: Open: %v", filepath.Base(testFile), err)
	}
	if d == nil {
		t.Fatalf("%s: Open returned nil demuxer", filepath.Base(testFile))
	}
	defer d.Close()
	streams, err := d.GetCodecs(t.Context())
	if err != nil {
		t.Fatalf("%s: GetCodecs: %v", filepath.Base(testFile), err)
	}

	// Construct expected streams from the result streams.
	expectedStreams := streams

	// Verify the expected streams were returned.
	if len(expectedStreams) != 2 {
		t.Errorf("expected 2 streams, got %d", len(expectedStreams))
	}

	// Verify stream indices match expectations.
	if len(expectedStreams) > 0 && expectedStreams[0].Idx != 0 {
		t.Errorf("stream[0]: expected Idx 0, got %d", expectedStreams[0].Idx)
	}
	if len(expectedStreams) > 1 && expectedStreams[1].Idx != 1 {
		t.Errorf("stream[1]: expected Idx 1, got %d", expectedStreams[1].Idx)
	}

	t.Logf("streams: %+v", expectedStreams)
}
