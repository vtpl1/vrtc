# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
# Format, lint, and build all targets (default)
make all

# Individual steps
make fmt          # Format with gofumpt
make lint         # Run golangci-lint with auto-fixes
make build        # Build all binaries (windows/amd64, linux/amd64, linux/arm64)
make test         # Run tests with race detector: go test -race -count=1 ./...
make test-edge-cgo # Run ./internal/edge with CGO enabled and AVGrabber/MSYS2 paths set
make update       # Update and tidy Go modules
make clean        # Remove build artifacts from bin/

# Run a single test package
go test -race -count=1 ./pkg/av/format/fmp4/...

# Run a single test by name
go test -race -count=1 -run TestName ./path/to/package/...
```

**Go module:** `github.com/vtpl1/vrtc` | **Go version:** 1.26

## Architecture Overview

### Services (cmd/ → internal/)

Three runnable binaries, each with a `cmd/<name>/main.go` entry point wired to an `internal/<name>/` implementation:

| Binary | Purpose |
|--------|---------|
| `edge` | Main streaming edge node; MySQL + MongoDB; YAML config |
| `cloud` | Cloud coordination node; gRPC-based; YAML config |
| `liverecservice` | Live recording; MySQL; JSON config |

Services load `.env` via `godotenv`, then read their config via `pkg/configpath`.

### Core AV Pipeline (`vrtc-sdk/av/`)

The central data model: encoded frames flow as `av.Packet` in-memory.

- **`av.Packet.Data`** for H.264/H.265 video is **AVCC format** (4-byte BE length prefix per NALU, ISO 14496-15). All demuxers produce AVCC; all muxers consume AVCC.
- **`av.Packet.Analytics`** carries optional `*av.FrameAnalytics` (object detections, aggregate counts). Serialised as JSON in fMP4 emsg boxes (`urn:vtpl:analytics:1`).

**Codec types** (`vrtc-sdk/av/codec/`): H.264, H.265, AAC, OPUS, MJPEG, PCM — each with a dedicated parser for bitstream manipulation (SPS/PPS/VPS extraction, NALU handling).

**Format containers** (`vrtc-sdk/av/format/`):

| Package | Read | Write | Notes |
|---------|------|-------|-------|
| `fmp4` | `Demuxer` | `Muxer` | Fragmented MP4 for streaming |
| `mp4` | `Demuxer` | `Muxer` | Standard MP4 |
| `llhls` | — | `Muxer` | Low-Latency HLS |

### RelayHub (`vrtc-sdk/av/relayhub/`)

The `RelayHub` coordinates relays (demuxers) and consumers (muxers). One relay per source (camera/RTSP URL) fans out packets to N consumers (HLS, MSE, recorder). Relays are created on-demand and reclaimed when idle.

- **Delivery policy**: 1 consumer → blocking write (back-pressure); 2+ consumers → leaky write (slow consumers drop frames, resync on next keyframe).
- **`WithMaxConsumers(n)`**: Limits consumers per relay. Used with `n=1` on recorded playback hubs to enforce single-consumer isolation (prevents leaky mode).
- **Packet buffer**: 30-second GOP replay buffer per relay for seamless recorded-to-live transition and instant keyframe on consumer attach.

### ChainingDemuxer (`vrtc-sdk/av/chain/`)

Chains multiple fMP4 segment demuxers into a single monotonic `av.DemuxCloser` stream. DTS values are adjusted at each segment boundary. Supports:

- **`SegmentSource`** interface: provides demuxers one at a time (live polling or fixed list).
- **`GapDetector`** interface: optional; when implemented by a source, ChainingDemuxer sets `IsDiscontinuity` on the first packet after a wall-clock gap > threshold.
- **`SeekableSegmentSource`** interface: optional; enables seek-to-timestamp within chained playback.

### Edge View (`pkg/edgeview/`)

Browser-facing HTTP/WebSocket server for live view and recorded playback. Key files:

| File | Purpose |
|------|---------|
| `stream.go` | `Service` struct, `ResolvePlaybackStart`, `RecordedDemuxerFactory`, `indexSource` |
| `ws_stream.go` | Unified `/ws/stream` WebSocket endpoint (live + recorded + seek) |
| `http_handler.go` | HTTP routes, unified `/api/cameras/{camera_id}/stream` endpoint, Huma OpenAPI |
| `http_cameras.go` | Camera CRUD + CSV import/export |
| `timeline.go` | Recording timeline for timebar display |

**Unified streaming endpoint**: A single `/ws/stream` (or `/api/cameras/{id}/stream` for HTTP) handles both live and recorded modes. Omit `start` param for live; provide `start` (RFC3339) for recorded. Seek commands switch modes transparently.

**Playback resolution**: `ResolvePlaybackStart` determines the actual mode:
- `recorded` — recordings exist in the requested range
- `first_available` — no recordings in range, falls back to earliest available
- `live` — requested time is beyond the latest recording

### Recorder (`pkg/recorder/`)

Manages fMP4 recording segments on disk with schedule-driven start/stop and automatic segment rotation.

| File | Purpose |
|------|---------|
| `recorder.go` | `RecordingManager` — poll loop, segment start/rotate/stop, retention enforcement |
| `index.go` | `RecordingIndex` interface — `Insert`, `QueryByChannel`, `FirstAvailable`, `LastAvailable`, `Delete`, `SealInterrupted` |
| `index_sqlite.go` | Per-channel SQLite implementation (WAL mode, LRU eviction at 100 DBs, composite indexes) |
| `retention.go` | Multi-tier retention: continuous/motion/object days, storage cap, disk-free threshold |

**Segment statuses**: `recording` → `complete` / `interrupted` / `corrupted` / `deleted`

**Scale design** (1000 cameras): per-channel SQLite with lazy init, `MaxOpenConns(2)`, LRU eviction when open DB count exceeds 100, `SealInterrupted` scans all channel subdirectories on startup.

### Terminology

These terms have distinct meanings — do not use them interchangeably:

| Term | Layer | Meaning |
|------|-------|---------|
| **Channel** | Config/metadata | A camera or stream source definition (ID, URL, credentials) |
| **Relay** | Runtime | The demuxer wrapper in RelayHub that reads packets and fans out to consumers |
| **Consumer** | Runtime | A downstream muxer sink attached to a relay (HLS, MSE, recorder) |
| **Stream** | Codec-level | A single audio/video track (index + codec config via `av.Stream`) |
| **sourceID** | Identifier | The key that identifies a relay's demuxer source (e.g. RTSP URL, camera ID) |

### Analytics Types (`vrtc-sdk/av/pva.go`)

| Type | Purpose |
|------|---------|
| `av.FrameAnalytics` | Per-frame analytics: detections, counts, frame correlation timestamps |
| `av.Detection` | Single detected object: bounding box, class, confidence, track ID |

BSON tags use `snake_case` (MongoDB); JSON tags use `camelCase` (API/emsg wire format).

## Linting Rules

`.golangci.yml` enables ~30 linters. Key constraints:
- Max line length: **180 characters**
- Max cyclomatic complexity: **30** (codec/bitstream parsers are exempt from some checks)
- All-caps constants are kept verbatim for C header compatibility
- JSON tags must be **camelCase** (enforced by `tagliatelle`)
