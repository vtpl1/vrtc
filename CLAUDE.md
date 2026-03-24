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
make gen          # Regenerate protobuf: buf format -w && buf generate
make update       # Update and tidy Go modules
make clean        # Remove build artifacts from bin/

# Run a single test package
go test -race -count=1 ./pkg/av/format/avf/...

# Run a single test by name
go test -race -count=1 -run TestName ./path/to/package/...
```

**Go module:** `github.com/vtpl1/vrtc` | **Go version:** 1.25

## Architecture Overview

### Services (cmd/ → internal/)

Four runnable binaries, each with a `cmd/<name>/main.go` entry point wired to an `internal/<name>/` implementation:

| Binary | Purpose |
|--------|---------|
| `edge` | Main streaming edge node; MySQL + MongoDB; YAML config |
| `cloud` | Cloud coordination node; gRPC-based; YAML config |
| `liverecservice` | Live recording; MySQL; JSON config |
| `avftomp4` | CLI converter (Cobra); converts AVF files to MP4 |

Services load `.env` via `godotenv`, then read their config via `pkg/configpath`.

### Core AV Pipeline (`pkg/av/`)

The central data model: encoded frames flow as `av.Packet` in-memory and are stored/streamed as `avf.Frame` on disk/wire.

**Codec types** (`pkg/av/codec/`): H.264, H.265, AAC, OPUS, MJPEG, PCM — each with a dedicated parser for bitstream manipulation (SPS/PPS/VPS extraction, NALU handling).

**Format containers** (`pkg/av/format/`):

| Package | Read | Write | Notes |
|---------|------|-------|-------|
| `avf` | `Demuxer` | `Muxer` | Native AVF container; `ProxyMuxDemuxCloser` for bidirectional conversion |
| `fmp4` | `Demuxer` | `Muxer` | Fragmented MP4 for streaming |
| `mp4` | `Demuxer` | `Muxer` | Standard MP4 |
| `llhls` | — | `Muxer` | Low-Latency HLS |

### AVF Frame / Packet Conversion (critical invariants)

The specs in `docs/` define these contracts — do not violate them:

- **`CONNECT_HEADER` frames** carry exactly one parameter-set NALU in Annex-B format and **never** produce an `av.Packet` (codec config only).
- **`av.Packet.Data`** is raw NALU bytes — **no** Annex-B start code (`\x00\x00\x00\x01`).
- **`avf.Frame.Data`** for video **is** Annex-B encoded (start code + NALU).
- `avf.Frame.H_FRAME` was renamed to `NON_REF_FRAME`; `av.Packet.Extra` was replaced with `Metadata *avf.PVAData` for analytics.

See `docs/frame-packet-conversion-spec.md` for the full CONNECT_HEADER state machine and field mappings.

### gRPC Services

Proto definitions live in `interfaces/`; generated Go code lands in `gen/`. Two main service contracts: `central_service_frs.proto` and `stream_service_frs.proto`. Regenerate with `make gen` (requires `buf`).

## Linting Rules

`.golangci.yml` enables ~30 linters. Key constraints:
- Max line length: **180 characters**
- Max cyclomatic complexity: **30** (codec/bitstream parsers are exempt from some checks)
- All-caps constants are kept verbatim for C header compatibility
- Generated files (`gen/`) and some test files have lint exemptions
