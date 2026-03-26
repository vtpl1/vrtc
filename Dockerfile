FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w \
        -X github.com/vtpl1/vrtc/pkg/appinfo.Version=$(git describe --tags --always 2>/dev/null || echo dev) \
        -X github.com/vtpl1/vrtc/pkg/appinfo.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) \
        -X github.com/vtpl1/vrtc/pkg/appinfo.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /vrtc-edge ./cmd/edge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /vrtc-edge /usr/local/bin/vrtc-edge
EXPOSE 8080 8081 9090 9091 9092
ENTRYPOINT ["vrtc-edge"]
