# vrtc — Specification Documents

All design and format specifications live here. Implementations must conform
to these documents. When adding or changing behaviour, update the relevant
spec first, then implement.

## Documents

| Document | Covers |
|----------|--------|
| [`avf-wire-format-spec.md`](avf-wire-format-spec.md) | AVF on-disk binary layout: frame record structure, MediaType/FrameType tables, payload encoding, offset fields, read/write algorithms |
| [`avf-frame-spec.md`](avf-frame-spec.md) | `avf.Frame` in-memory struct: field definitions, FrameType semantics, Data format per type, invariants |
| [`av-packet-spec.md`](av-packet-spec.md) | `av.Packet` in-memory struct: all field contracts, Data format (raw NALU), Duration derivation, NewCodecs semantics |
| [`frame-packet-conversion-spec.md`](frame-packet-conversion-spec.md) | `FrameToPacket` and `PacketToFrames` function signatures, field mappings, CONNECT_HEADER accumulation state machine, migration checklist |
| [`mse-websocket-api.md`](mse-websocket-api.md) | WebSocket-based MSE streaming API: endpoint, command protocol, frame types, browser consumption guide |

## Key invariants (quick reference)

- `CONNECT_HEADER` frames never produce an `av.Packet` (Option A).
- Each `CONNECT_HEADER` carries **exactly one** parameter set NALU in Annex-B format.
- `av.Packet.Data` is **raw NALU bytes** — no start code, no length prefix.
- `avf.Frame.Data` for video is **Annex-B** — `\x00\x00\x00\x01` + NALU.
- `av.Packet.Duration` is never `0` from a well-behaved demuxer.
- `av.Packet.IsParamSetNALU` has been removed.
- `av.Packet.Extra` has been replaced by typed `Metadata *avf.PVAData`.
- `avf.Frame.H_FRAME` has been renamed `NON_REF_FRAME`.
