# syntax=docker/dockerfile:1
# Multi-stage build for vrtc.

# ── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -ldflags="-s -w \
        -X github.com/vtpl1/vrtc/pkg/version.Version=$(git describe --tags --always 2>/dev/null || echo dev) \
        -X github.com/vtpl1/vrtc/pkg/version.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) \
        -X github.com/vtpl1/vrtc/pkg/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /out/vrtc ./cmd/vrtc

# ── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder /out/vrtc /usr/local/bin/vrtc

EXPOSE 8080 8081 9090 9091 9092

ENTRYPOINT ["/usr/local/bin/vrtc"]
CMD ["--role", "all"]
