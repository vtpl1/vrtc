# vrtc — Specification Documents

All design and format specifications live here. Implementations must conform
to these documents. When adding or changing behaviour, update the relevant
spec first, then implement.

## Documents

| Document | Covers |
|----------|--------|
| [`seek-protocol.md`](seek-protocol.md) | Unified WebSocket streaming & seek protocol: endpoint, commands, server messages, client implementation guide |
| [`kpi-targets.md`](kpi-targets.md) | Performance targets for streaming, recording, playback, scalability, and system health |

## Key invariants (quick reference)

- `av.Packet.Data` for H.264/H.265 video is **AVCC format** — 4-byte BE length prefix per NALU (ISO 14496-15).
- `av.Packet.Duration` is never `0` from a well-behaved demuxer.
- `av.Packet.Analytics` carries optional `*av.FrameAnalytics` for per-frame analytics (detections, counts); `nil` when absent.
