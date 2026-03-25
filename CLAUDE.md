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

**Go module:** `github.com/vtpl1/vrtc` | **Go version:** 1.25

## Architecture Overview

### Services (cmd/ → internal/)

Three runnable binaries, each with a `cmd/<name>/main.go` entry point wired to an `internal/<name>/` implementation:

| Binary | Purpose |
|--------|---------|
| `edge` | Main streaming edge node; MySQL + MongoDB; YAML config |
| `cloud` | Cloud coordination node; gRPC-based; YAML config |
| `liverecservice` | Live recording; MySQL; JSON config |

Services load `.env` via `godotenv`, then read their config via `pkg/configpath`.

### Core AV Pipeline (`pkg/av/`)

The central data model: encoded frames flow as `av.Packet` in-memory.

**Codec types** (`pkg/av/codec/`): H.264, H.265, AAC, OPUS, MJPEG, PCM — each with a dedicated parser for bitstream manipulation (SPS/PPS/VPS extraction, NALU handling).

**Format containers** (`pkg/av/format/`):

| Package | Read | Write | Notes |
|---------|------|-------|-------|
| `fmp4` | `Demuxer` | `Muxer` | Fragmented MP4 for streaming |
| `mp4` | `Demuxer` | `Muxer` | Standard MP4 |
| `llhls` | — | `Muxer` | Low-Latency HLS |

## Linting Rules

`.golangci.yml` enables ~30 linters. Key constraints:
- Max line length: **180 characters**
- Max cyclomatic complexity: **30** (codec/bitstream parsers are exempt from some checks)
- All-caps constants are kept verbatim for C header compatibility
