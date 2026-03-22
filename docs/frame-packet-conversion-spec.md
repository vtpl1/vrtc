# Frame ↔ Packet Conversion Specification

**Packages involved:**
- `github.com/vtpl1/vrtc/pkg/avf` — `avf.Frame`
- `github.com/vtpl1/vrtc/pkg/av` — `av.Packet`

Related specs: [`avf-frame-spec.md`](avf-frame-spec.md), [`av-packet-spec.md`](av-packet-spec.md)

---

## 1. Fundamental constraint: CONNECT_HEADER frames are never packets

Per Option A of the packet spec, a `CONNECT_HEADER` frame **never** produces
an `av.Packet`. Codec parameter sets travel exclusively through `av.Packet.NewCodecs`
on the `I_FRAME` that follows the CONNECT_HEADER sequence.

Any call to `FrameToPacket` with a `CONNECT_HEADER` frame returns `(zero, false)`
and the caller must skip it.

---

## 2. Direction 1: Frame → Packet (1:1, stateful)

### 2.1 Function signature

```go
// FrameToPacket converts one media Frame to an av.Packet.
//
// Returns (pkt, true)  when the frame produces a packet.
// Returns (zero, false) for CONNECT_HEADER and UNKNOWN_FRAME — caller must skip.
//
// idx is supplied by the caller; the demuxer or proxy owns stream index assignment.
// codec is the current CodecData for this stream and is used to:
//   - derive nominal Duration when Frame.DurationMs == 0 (first packet of stream)
//   - populate NewCodecs when the codec changed since the last keyframe
func FrameToPacket(frm *Frame, idx uint16, codec av.CodecData) (av.Packet, bool)
```

### 2.2 FrameType mapping

| Frame.FrameType | Packet produced | Packet.KeyFrame |
|-----------------|-----------------|-----------------|
| `CONNECT_HEADER` | none — skip | — |
| `I_FRAME` | yes | `true` |
| `P_FRAME` | yes | `false` |
| `NON_REF_FRAME` | yes | `false` |
| `AUDIO_FRAME` | yes | `false` |
| `UNKNOWN_FRAME` | none — skip | — |
| reserved (4–15) | none — skip | — |

### 2.3 Field mapping

| Frame field | → | Packet field | Notes |
|-------------|---|--------------|-------|
| `MediaType` | → | `CodecType` | via `MediaType → CodecType` map |
| `TimeStamp` (ms) | → | `DTS` | `time.Duration(TimeStamp) * time.Millisecond` |
| `TimeStamp` (ms) | → | `WallClockTime` | `time.UnixMilli(TimeStamp)` |
| `DurationMs` | → | `Duration` | if `DurationMs == 0`: derive nominal from `codec` |
| `FrameID` | → | `FrameID` | direct copy |
| `Data[4:]` | → | `Data` | H.264/H.265 video: strip 4-byte Annex-B start code; frame is guaranteed single-NALU after `SplitFrame` |
| `Data` | → | `Data` | Audio: as-is; strip ADTS header for AAC |
| `Pvadata` | → | `Metadata` | `&frame.Pvadata` (or nil if zero value) |
| *(caller)* | → | `Idx` | assigned by demuxer / proxy |
| *(codec change state)* | → | `NewCodecs` | set by caller when codec changed |

### 2.4 Nominal Duration derivation (first packet)

When `Frame.DurationMs == 0`, the caller derives a nominal duration from `codec`:

- **Video** (`av.VideoCodecData`): use `TimeScale` and frame-rate fields from
  SPS. Formula: `(num_units_in_tick * 2) / time_scale` converted to
  `time.Duration`. If SPS does not carry timing info, fall back to `33ms`
  (≈ 30 fps).
- **Audio** (`av.AudioCodecData`): call `codec.PacketDuration(frm.Data)`.

### 2.5 CONNECT_HEADER accumulation (caller responsibility)

The caller (demuxer or proxy) must maintain a small state machine:

```
state: accumulating = false, connectHeaderData []byte

on CONNECT_HEADER frame:
    connectHeaderData = append(connectHeaderData, frm.Data...)
    accumulating = true
    → skip (no packet)

on I_FRAME / P_FRAME / NON_REF_FRAME (while accumulating == true):
    codec = parseCodec(mediaType, connectHeaderData)
    connectHeaderData = nil
    accumulating = false
    if codec changed:
        nextPacket.NewCodecs = updatedStreams
    → call FrameToPacket normally

on UNKNOWN_FRAME:
    → skip; do NOT reset accumulation state
```

---

## 3. Direction 2: Packet → Frames (1:N)

### 3.1 Function signature

```go
// PacketToFrames converts one av.Packet to the sequence of avf.Frame records
// it produces on the wire.
//
// For a video keyframe this emits CONNECT_HEADER frames (one per parameter set
// NALU) followed by an I_FRAME. For all other packets it emits exactly one frame.
//
// codec must be non-nil for H.264/H.265 keyframes — it provides the SPS/PPS/VPS.
// If pkt.NewCodecs is non-nil, the updated codec from NewCodecs is used for
// CONNECT_HEADER generation.
//
// Returns an empty slice for packets that cannot be converted (unknown codec).
func PacketToFrames(pkt av.Packet, codec av.CodecData) []Frame
```

### 3.2 Output frame sequences

| Packet condition | Frames emitted (in order) |
|------------------|--------------------------|
| H.264 keyframe | `CONNECT_HEADER(SPS)`, `CONNECT_HEADER(PPS)`, `I_FRAME` |
| H.265 keyframe | `CONNECT_HEADER(VPS)`, `CONNECT_HEADER(SPS)`, `CONNECT_HEADER(PPS)`, `I_FRAME` |
| MJPEG keyframe | `I_FRAME` only — no CONNECT_HEADERs |
| non-keyframe video | `P_FRAME` |
| audio | `AUDIO_FRAME` |
| unknown codec | *(empty — nothing emitted)* |

### 3.3 Field mapping

| Packet field | → | Frame field | Notes |
|--------------|---|-------------|-------|
| `CodecType` | → | `MediaType` | via `CodecType → MediaType` map |
| `DTS.Milliseconds()` | → | `TimeStamp` | |
| `Duration.Milliseconds()` | → | `DurationMs` | |
| `FrameID` | → | `FrameID` | direct copy |
| `Data` (raw NALU) | → | `Data` | H.264/H.265: prepend `\x00\x00\x00\x01`; audio: as-is |
| `Metadata` | → | `Pvadata` | dereference if non-nil, else zero `PVAData{}` |
| `NewCodecs` | → | codec source | use updated codec for CONNECT_HEADER generation |
| `Idx` | → | *(discarded)* | Frame has no stream index field |

### 3.4 CONNECT_HEADER Data format

Each `CONNECT_HEADER` frame's `Data` field contains **one** parameter set NALU
in Annex-B format:

```
Data = \x00\x00\x00\x01 + <raw NALU bytes>
```

The NALU bytes come from the `CodecData`:
- H.264: `codec.(h264parser.CodecData).SPS()`, then `.PPS()`
- H.265: `codec.(h265parser.CodecData).VPS()`, then `.SPS()`, then `.PPS()`

### 3.5 RefFrameOff rule (muxer responsibility)

All frames in a keyframe group share the same `RefFrameOff` value, pointing to
the file offset of the **first** `CONNECT_HEADER` in the group (the SPS frame).
The muxer records `lastConnectHdrOff = currentOffset` before writing the first
`CONNECT_HEADER` and uses that value unchanged for all subsequent frames in the
group.

---

## 4. Existing functions and their status

| Function | Location | Status |
|----------|----------|--------|
| `FrameToAVPacket` | `pkg/avf/frame.go` | **Replace** with `FrameToPacket` |
| `AVPacketToFrame` | `pkg/avf/frame.go` | **Replace** with `PacketToFrames` |
| `frameToPacket` | `pkg/av/format/avf/demuxer.go` | **Fix** per §2 (accumulation, Duration, FrameID) |
| `pktToAVFFrame` | `pkg/av/format/avf/proxy.go` | **Replace** with `PacketToFrames` |

---

## 5. Migration checklist

Before implementing any muxer, demuxer, or proxy that converts between
`avf.Frame` and `av.Packet`, verify:

- [ ] `CONNECT_HEADER` frames are never passed to `FrameToPacket`
- [ ] `UNKNOWN_FRAME` and reserved types (4–15) are silently skipped
- [ ] `Duration` is never `0` in emitted packets (nominal fallback applied)
- [ ] `Data` in emitted `av.Packet` is raw NALU — no start code, no length prefix
- [ ] `Data` in emitted `avf.Frame` for video has 4-byte Annex-B start code
- [ ] For keyframe packets, `PacketToFrames` emits `CONNECT_HEADER` frames
      before the `I_FRAME`
- [ ] `FrameID = 0` is forwarded as-is; receivers must not treat it as identity
- [ ] `NewCodecs` is set on the `I_FRAME` packet when codec changed; not before
- [ ] The MP4 demuxer strips AVCC length prefixes before emitting `av.Packet.Data`
- [ ] Multi-NALU AVF wire frames are split via `avf.SplitFrame` before calling
      `FrameToPacket` (see §6)

---

## 6. NALU splitting for multi-NALU AVF wire records

### 6.1 Background

Legacy IP cameras pack entire H.264/H.265 **access units** — multiple NALUs
concatenated in Annex-B format — into a single `I_FRAME`, `P_FRAME`, or
`NON_REF_FRAME` wire record. Examples:

- `\x00\x00\x00\x01[SEI]\x00\x00\x00\x01[IDR]` — SEI + keyframe
- `\x00\x00\x00\x01[SPS]\x00\x00\x00\x01[PPS]\x00\x00\x00\x01[IDR]` — param sets + keyframe

Passing such a record directly to `FrameToPacket` would violate the
single-NALU invariant of `av.Packet.Data`.

### 6.2 SplitFrame function

```go
// SplitFrame splits an I_FRAME/P_FRAME/NON_REF_FRAME that contains a
// multi-NALU Annex-B access unit into individual single-NALU frames.
//
// Returns [frm] unchanged for single-NALU frames, non-video frames, or
// non-H.264/H.265 codecs (MJPEG, audio).
//
// Inline parameter-set NALUs (SPS/PPS for H.264; VPS/SPS/PPS for H.265)
// are silently dropped — they duplicate the preceding CONNECT_HEADER records.
//
// DurationMs is assigned only to the last output frame; all others get 0.
func SplitFrame(frm Frame) []Frame
```

### 6.3 NALU classification after splitting

| NALU content | FrameType assigned | Emitted as av.Packet? |
|---|---|---|
| H.264 SPS / PPS | `CONNECT_HEADER` | No — dropped by `SplitFrame` |
| H.265 VPS / SPS / PPS | `CONNECT_HEADER` | No — dropped by `SplitFrame` |
| H.264 IDR (type 5) | `I_FRAME` | Yes, `KeyFrame=true` |
| H.265 IRAP (IDR/BLA/CRA) | `I_FRAME` | Yes, `KeyFrame=true` |
| H.264/H.265 non-IDR VCL | `P_FRAME` | Yes, `KeyFrame=false` |
| SEI, AUD, filler | `NON_REF_FRAME` | Yes, `KeyFrame=false` |

### 6.4 Usage pattern (demuxer and proxy)

```go
// Before calling FrameToPacket, split any multi-NALU video data frames.
split := avf.SplitFrame(frm)   // no-op for single-NALU or non-H264/H265
for i, sf := range split {
    pkt, ok := FrameToPacket(&sf, idx, codec)
    if !ok {
        continue
    }
    if i == 0 && codecChanged {
        pkt.NewCodecs = updatedStreams
    }
    emit(pkt)
}
```

All output frames from a single `SplitFrame` call share the same `TimeStamp`,
`FrameID`, `MediaType`, `StreamMeta`, and `Pvadata`. Only the last frame
carries the original `DurationMs`; all others get `DurationMs = 0`.
