# vrtc — Specification Documents

All design and format specifications live here. Implementations must conform
to these documents. When adding or changing behaviour, update the relevant
spec first, then implement.

## Documents

| Document | Covers |
|----------|--------|
| [`av-packet-spec.md`](av-packet-spec.md) | `av.Packet` in-memory struct: all field contracts, Data format (AVCC), Duration derivation, NewCodecs semantics |
| [`mse-websocket-api.md`](mse-websocket-api.md) | WebSocket-based MSE streaming API: endpoint, command protocol, frame types, browser consumption guide |

## Key invariants (quick reference)

- `av.Packet.Data` for H.264/H.265 video is **AVCC format** — 4-byte BE length prefix per NALU (ISO 14496-15).
- `av.Packet.Duration` is never `0` from a well-behaved demuxer.
- `av.Packet.PVAData` carries optional `*av.PVAData` for object-detection analytics; `nil` when absent.
