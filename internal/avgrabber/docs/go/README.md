# AVGrabber — Go Developer Guide

AVGrabber v5.1.2 is an RTSP demuxer library for H.264/H.265 IP cameras.
It exposes a C API that Go consumes via cgo. The library handles RTSP
negotiation, RTP reassembly, NAL unit grouping, and timestamp unwrapping.
Your Go code receives complete, ready-to-mux frames.

## Role of this library

```
IP camera (RTSP)
      │
      ▼
┌─────────────────────────────┐
│  AVGrabber  (this library)  │  ← RTSP client / demuxer
│  - RTSP/RTP transport       │
│  - NAL → access unit        │
│  - 64-bit timestamp unwrap  │
└──────────────┬──────────────┘
               │  AVGrabberFrame (Annex-B video, raw audio)
               │  AVGrabberFrameHeader (pts_ticks, duration_ticks, …)
               ▼
     Your Go muxer layer
        ├── fMP4 / CMAF
        ├── LL-HLS
        └── WebRTC (RTP)
```

## Documents in this folder

| File | Contents |
|------|----------|
| [01-quickstart.md](01-quickstart.md) | Build, link, runtime setup, minimal working example |
| [02-cgo-bindings.md](02-cgo-bindings.md) | Complete Go type layer — structs, constants, helper functions |
| [03-frame-guide.md](03-frame-guide.md) | Frame types, ordering rules, Annex-B format, audio codecs |
| [04-timestamps.md](04-timestamps.md) | Clock domains, pts_ticks, duration_ticks, discontinuity handling |
| [05-muxer-fmp4.md](05-muxer-fmp4.md) | Writing an fMP4 / LL-HLS (CMAF) muxer |
| [06-muxer-webrtc.md](06-muxer-webrtc.md) | Writing a WebRTC sender with pion/webrtc |
| [07-grpc.md](07-grpc.md) | Using the gRPC streaming service instead of direct cgo |

## Quick orientation

- All video frames arrive as **Annex-B** (four-byte `00 00 00 01` start codes).
  fMP4 requires **AVCC/HVCC** (length-prefix). You must convert.
- All timestamps are in **stream clock ticks** — `pts_ticks` is 64-bit and
  never wraps. Divide by `video_clock_rate` (usually 90000 Hz) or
  `audio_sample_rate` to get seconds.
- The frame sequence always follows: `PARAM_SET → KEY → DELTA … DELTA`
  then repeats at the next keyframe interval. Audio frames are interleaved.
- `AVGRABBER_ERR_NOT_READY` (10) is normal — it means the timeout elapsed
  with no frame. Keep looping.

## Related C/C++ documentation

The `docs/` parent folder contains codec-level reference documents that
apply regardless of language:

- `frame.md` — complete frame contract
- `frame_types.md` — per-type definitions
- `connect_header.md` — PARAM_SET frame details
- `key_frame.md` — KEY frame details
- `fmp4_muxing.md` — fMP4 timing and payload mapping reference
