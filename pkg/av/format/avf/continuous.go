package avf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
)

var reDateTime = regexp.MustCompile(`\d{8}_\d{6}`)

// ContinuousDemuxer reads AVF files from a directory in sorted order, looping
// forever once the directory listing is exhausted. Timestamps are rewritten so
// that the output DTS stream is strictly monotonic across file boundaries.
//
// Use NewContinuous or OpenContinuous to construct; call GetCodecs once, then
// loop on ReadPacket. Close stops playback and closes the current file.
type ContinuousDemuxer struct {
	baseDir string
	opts    []Option // forwarded to each per-file Demuxer

	files   []string // sorted *.avf paths discovered at construction time
	fileIdx int      // index into files of the currently open file

	current *Demuxer // demuxer for the current file; nil before first open

	// DTS continuity across file boundaries.
	// After each file the first packet of the next file is re-based so that:
	//   adjustedDTS = (rawDTS - rawFileBase) + dtsAccum
	dtsAccum    time.Duration // where the next file's timeline must start
	rawFileBase time.Duration // raw DTS of the first packet from the current file
	lastDTS     time.Duration // adjusted DTS of the most recently emitted packet
	lastDur     time.Duration // Duration of the most recently emitted packet
	fileSeenPkt bool          // true once we have seen at least one packet from the current file

	// Codec state: set by GetCodecs from the first file, then updated
	// mid-stream via Packet.NewCodecs whenever the codec changes.
	streams []av.Stream
	probed  bool
}

// NewContinuous returns a ContinuousDemuxer that reads from baseDir using the
// provided options for each per-file Demuxer.
func NewContinuous(baseDir string, opts ...Option) (*ContinuousDemuxer, error) {
	files, err := globAVF(baseDir)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("avf: no *.avf files found in %q", baseDir)
	}

	return &ContinuousDemuxer{
		baseDir: baseDir,
		opts:    opts,
		files:   files,
	}, nil
}

// Files returns the sorted list of *.avf paths that will be played in order.
func (d *ContinuousDemuxer) Files() []string { return d.files }

// GetCodecs opens the first file, probes its codec parameters, and returns the
// initial stream list. It must be called exactly once before ReadPacket.
func (d *ContinuousDemuxer) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	if d.probed {
		return d.streams, nil
	}

	if err := d.openFile(ctx, 0); err != nil {
		return nil, err
	}

	streams, err := d.current.GetCodecs(ctx)
	if err != nil {
		return nil, fmt.Errorf("avf continuous: GetCodecs %s: %w", d.files[0], err)
	}

	d.streams = streams
	d.probed = true

	return streams, nil
}

// ReadPacket returns the next av.Packet with a monotonically increasing DTS.
// When a file ends the next file is opened transparently. When the directory
// is exhausted, playback wraps around to the first file.
// Packet.NewCodecs is set whenever the codec changes at a file boundary.
func (d *ContinuousDemuxer) ReadPacket(ctx context.Context) (av.Packet, error) {
	for {
		if ctx.Err() != nil {
			return av.Packet{}, ctx.Err()
		}

		pkt, err := d.current.ReadPacket(ctx)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return av.Packet{}, fmt.Errorf("avf continuous: %s: %w", d.files[d.fileIdx], err)
			}

			// Current file is done — advance to next, wrapping around.
			if err2 := d.advanceFile(ctx); err2 != nil {
				return av.Packet{}, err2
			}

			continue
		}

		return d.adjustPacket(pkt), nil
	}
}

// Close closes the underlying file demuxer.
func (d *ContinuousDemuxer) Close() error {
	if d.current != nil {
		return d.current.Close()
	}

	return nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// openFile opens files[idx], replacing any currently open demuxer.
func (d *ContinuousDemuxer) openFile(ctx context.Context, idx int) error {
	if d.current != nil {
		_ = d.current.Close()
		d.current = nil
	}

	dmx, err := Open(d.files[idx], d.opts...)
	if err != nil {
		return fmt.Errorf("avf continuous: open %s: %w", d.files[idx], err)
	}

	d.current = dmx
	d.fileIdx = idx
	d.fileSeenPkt = false

	// GetCodecs must be called on each new demuxer before ReadPacket.
	if d.probed {
		if _, err := dmx.GetCodecs(ctx); err != nil {
			return fmt.Errorf("avf continuous: GetCodecs %s: %w", d.files[idx], err)
		}
	}

	return nil
}

// advanceFile moves to the next file (wrapping), accumulates the DTS offset,
// and returns any codec-change information via d.pendingStreams.
func (d *ContinuousDemuxer) advanceFile(ctx context.Context) error {
	// After the last packet of the finished file, the next packet must start
	// at lastDTS + lastDuration so there is no gap or overlap.
	d.dtsAccum = d.lastDTS + d.lastDur

	nextIdx := (d.fileIdx + 1) % len(d.files)

	if err := d.openFile(ctx, nextIdx); err != nil {
		return err
	}

	// Capture the codec list of the new file so we can signal a change if
	// the streams differ from the previously advertised ones.
	newStreams := d.current.streams // populated by GetCodecs inside openFile
	if !streamsEqual(d.streams, newStreams) {
		d.streams = newStreams
		// Signal via the next emitted packet's NewCodecs field.
		d.current.pendingCodecChange = newStreams
	}

	return nil
}

// adjustPacket rewrites pkt.DTS so the output timeline is continuous.
// On the first packet of each file the raw DTS is recorded as the base;
// subsequent packets are shifted by (rawDTS - rawFileBase) + dtsAccum.
func (d *ContinuousDemuxer) adjustPacket(pkt av.Packet) av.Packet {
	if !d.fileSeenPkt {
		d.rawFileBase = pkt.DTS
		d.fileSeenPkt = true
	}

	pkt.DTS = (pkt.DTS - d.rawFileBase) + d.dtsAccum
	d.lastDTS = pkt.DTS
	d.lastDur = pkt.Duration

	return pkt
}

// globAVF returns sorted *.avf paths in dir.
func globAVF(dir string) ([]string, error) {
	var paths []string

	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !e.IsDir() && filepath.Ext(e.Name()) == ".avf" {
			paths = append(paths, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("avf: scan %q: %w", dir, err)
	}

	sort.Slice(paths, func(i, j int) bool {
		return fileDateTime(paths[i]).Before(fileDateTime(paths[j]))
	})

	return paths, nil
}

// fileDateTime extracts the first "20060102_150405" timestamp from a file path.
// Returns the zero time if no match is found, so unparseable names sort first.
func fileDateTime(path string) time.Time {
	m := reDateTime.FindString(filepath.Base(path))
	if m == "" {
		return time.Time{}
	}

	t, err := time.ParseInLocation("20060102_150405", m, time.Local)
	if err != nil {
		return time.Time{}
	}

	return t
}

// streamsEqual reports whether two stream lists have the same codec types.
func streamsEqual(a, b []av.Stream) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].Codec.Type() != b[i].Codec.Type() {
			return false
		}
	}

	return true
}
