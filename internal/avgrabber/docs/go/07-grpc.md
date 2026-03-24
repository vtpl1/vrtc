# gRPC Streaming Service

Instead of embedding the AVGrabber C library directly with cgo, you can run
a gRPC server that wraps the library and stream frames to one or more Go
clients over the network. This is useful when:

- The muxer process is on a different host from the RTSP client.
- You want to fan out one RTSP session to multiple consumers.
- You want to avoid cgo in your muxer process entirely.

## Proto definition

Source: `proto/avgrabber.proto`

```protobuf
service AVGrabberService {
  rpc OpenSession(SessionConfig)  returns (SessionHandle);
  rpc StreamFrames(SessionHandle) returns (stream MediaFrame);
  rpc GetStreamInfo(SessionHandle) returns (StreamInfo);
  rpc CloseSession(SessionHandle)  returns (google.protobuf.Empty);
}
```

## Generate Go code

```bash
# Install protoc plugins once:
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Generate from repo root:
protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/avgrabber.proto
```

This produces `proto/avgrabberv1/avgrabber.pb.go` and
`proto/avgrabberv1/avgrabber_grpc.pb.go`.

## Client usage

### Open session and stream frames

```go
package main

import (
    "context"
    "fmt"
    "io"
    "log"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/videonetics/avgrabber/proto/avgrabberv1"
)

func main() {
    conn, err := grpc.NewClient("localhost:50051",
        grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    client := pb.NewAVGrabberServiceClient(conn)
    ctx := context.Background()

    // 1. Open RTSP session on the server.
    handle, err := client.OpenSession(ctx, &pb.SessionConfig{
        Url:      "rtsp://192.168.1.10/stream1",
        Username: "admin",
        Password: "password",
        Protocol: pb.TransportProtocol_TRANSPORT_TCP,
        Audio:    true,
    })
    if err != nil {
        log.Fatal("OpenSession:", err)
    }
    defer client.CloseSession(ctx, handle)

    // 2. Stream frames.
    stream, err := client.StreamFrames(ctx, handle)
    if err != nil {
        log.Fatal("StreamFrames:", err)
    }

    for {
        frame, err := stream.Recv()
        if err == io.EOF {
            break
        }
        if err != nil {
            log.Fatal("Recv:", err)
        }
        handleFrame(frame)
    }
}

func handleFrame(f *pb.MediaFrame) {
    h := f.Header
    t := h.Timing
    fmt.Printf("type=%s media=%s codec=%s size=%d pts=%d\n",
        h.FrameType, h.MediaType, h.CodecType,
        h.FrameSize, t.PtsTicks)
    _ = f.Data // raw payload bytes
}
```

### Query stream info

```go
// Call after the first FRAME_PARAM_SET has been received (a second or two
// after OpenSession). Returns codes.FailedPrecondition if called too early.
info, err := client.GetStreamInfo(ctx, handle)
if err != nil {
    log.Println("stream info not ready yet:", err)
} else {
    fmt.Printf("codec=%s %dx%d @ %d fps  clock=%d Hz\n",
        info.VideoCodec, info.Width, info.Height, info.Fps,
        info.VideoClockRate)
    fmt.Printf("audio=%s %d Hz  %d samples/frame\n",
        info.AudioCodec, info.AudioSampleRate, info.AudioSamplesPerFrame)
}
```

## Proto → Go type mapping

The proto `MediaFrame` maps directly to the cgo `Frame` type:

| Proto field | Go (cgo) field | Notes |
|-------------|----------------|-------|
| `Header.FrameType` | `Frame.FrameType` | Use `pb.FrameType_FRAME_*` constants |
| `Header.MediaType` | `Frame.MediaType` | |
| `Header.CodecType` | `Frame.CodecType` | |
| `Header.FrameSize` | `Frame.FrameSize` | |
| `Header.Flags.Ntp_synced` | `Frame.Flags & FlagNTPSynced` | Proto uses bools; cgo uses bitmask |
| `Header.Flags.Discontinuity` | `Frame.Flags & FlagDiscontinuity` | |
| `Header.Flags.Keyframe` | `Frame.Flags & FlagKeyframe` | |
| `Header.Flags.HasSei` | `Frame.Flags & FlagHasSEI` | |
| `Header.Timing.WallClockMs` | `Frame.WallClockMS` | |
| `Header.Timing.NtpMs` | `Frame.NTPMS` | |
| `Header.Timing.PtsTicks` | `Frame.PTSTicks` | |
| `Header.Timing.DtsTicks` | `Frame.DTSTicks` | |
| `Header.Timing.DurationTicks` | `Frame.DurationTicks` | |
| `Data` | `Frame.Data` | |

## Error handling

| gRPC status | Meaning | What to do |
|-------------|---------|-----------|
| `OK` on stream end | Session closed cleanly | Exit loop |
| `UNAUTHENTICATED` | Camera rejected credentials | Fix credentials; do not retry |
| `UNAVAILABLE` | Session not found or already closed | Reopen |
| `FAILED_PRECONDITION` on `GetStreamInfo` | First PARAM_SET not yet arrived | Wait and retry |

```go
import (
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

frame, err := stream.Recv()
if err != nil {
    st, _ := status.FromError(err)
    switch st.Code() {
    case codes.OK:
        return nil // clean end
    case codes.Unauthenticated:
        return fmt.Errorf("auth failed: %w", err)
    default:
        return fmt.Errorf("stream error: %w", err)
    }
}
```

## Fan-out pattern

One server session → multiple independent gRPC `StreamFrames` calls.
Each caller receives its own copy of the frame stream. This is managed
server-side by the gRPC server implementation.

```
RTSP camera
    │
    ▼
AVGrabber gRPC server (one session per RTSP URL)
    ├── StreamFrames ──► client A (fMP4 muxer)
    ├── StreamFrames ──► client B (WebRTC sender)
    └── StreamFrames ──► client C (recorder)
```

## TLS / authentication

For production deployments, replace `insecure.NewCredentials()` with proper
TLS credentials:

```go
creds, err := credentials.NewClientTLSFromFile("ca.crt", "")
conn, err := grpc.NewClient("server:50051", grpc.WithTransportCredentials(creds))
```

Add a gRPC interceptor for token-based auth if the server requires it.
