# av.Packet Specification

**Package:** `github.com/vtpl1/vrtc/pkg/av`

---

## 1. Purpose

`av.Packet` is the **universal unit of compressed media** flowing through the
pipeline. It is codec-agnostic and container-agnostic. Every demuxer produces
`av.Packet` values; every muxer consumes them.

---

## 2. Struct definition

```go
type Packet struct {
    // ── Flags ─────────────────────────────────────────────────────────────
    KeyFrame        bool // true iff this is an IDR/keyframe video packet; always false for audio
    IsDiscontinuity bool // DTS does not follow from the previous packet; receivers must reinitialise timing

    // ── Identity / routing ────────────────────────────────────────────────
    Idx       uint16    // stream index; matches Stream.Idx from GetCodecs
    CodecType CodecType // codec of this packet

    // FrameID is a stable identity assigned by the source device or stream.
    // Comparable across sessions for the same source. 0 means not assigned.
    FrameID int64

    // ── Timing ────────────────────────────────────────────────────────────
    DTS           time.Duration // decode timestamp; monotonically non-decreasing within a stream
    PTSOffset     time.Duration // PTS = DTS + PTSOffset; zero for codecs without B-frames
    Duration      time.Duration // nominal presentation duration; never 0 from a well-behaved demuxer
    WallClockTime time.Time     // wall-clock capture/arrival time; zero means not set

    // ── Payload ───────────────────────────────────────────────────────────
    // Data carries the compressed media payload:
    //   H.264/H.265 video — AVCC format: one or more NALUs, each prefixed with
    //                        a 4-byte big-endian length (ISO 14496-15).
    //   Audio             — raw encoded samples, no container framing
    //                        (ADTS stripped for AAC).
    //   Empty (nil/len=0) — valid only for a pure codec-change notification
    //                        (KeyFrame==true, NewCodecs!=nil, no media data).
    Data []byte

    // PVAData carries per-frame object-detection analytics (vehicle count,
    // people count, bounding boxes, etc.). nil when analytics are absent.
    PVAData *PVAData

    // ── Codec change ──────────────────────────────────────────────────────
    // NewCodecs is non-nil on the keyframe packet that immediately follows a
    // parameter-set change. Contains only the streams whose codec changed.
    // Receivers must update per-stream codec state when this is non-nil.
    NewCodecs []Stream
}
```

---

## 3. Field specifications

### 3.1 Idx

Identifies the stream this packet belongs to. Must match a `Stream.Idx` value
returned by `GetCodecs`. Do not assume `0 = video` or `1 = audio` — always
resolve stream identity via the `Stream` list.

### 3.2 CodecType

The codec that produced `Data`. Redundant with the `Stream` returned by
`GetCodecs`, but available on the packet for fast dispatch without a map
lookup.

### 3.3 FrameID

A stable identity for this frame, assigned by the **source device or stream**
(not the demuxer). Two packets with the same `FrameID` from the same source
represent the same captured frame. `FrameID == 0` means the source did not
assign a value; receivers must not treat `0` as a meaningful identity.

### 3.4 KeyFrame

`true` for IDR frames (H.264/H.265) and every MJPEG frame. Always `false` for
audio packets.

### 3.5 IsDiscontinuity

Set to `true` when the DTS of this packet does not follow from the previous
packet in the same stream — for example after a seek, a clock jump, or a
stream restart. Receivers must reinitialise any timing state (buffers, PTS
counters) when this is `true`.

### 3.6 DTS

Decode timestamp as a `time.Duration` from stream start. Monotonically
non-decreasing within a stream; the demuxer clamps any backward step from
a camera clock glitch to `lastDTS + 1ms`.

### 3.7 PTSOffset

`PTS = DTS + PTSOffset`. Zero for all codecs that do not use B-frames.
Non-zero only for H.264/H.265 streams that contain B-frames.

### 3.8 Duration

Nominal presentation duration of this packet. **Never `0`** from a
well-behaved demuxer:

- **First packet of a stream** — the demuxer derives a nominal duration from
  codec metadata:
  - Video: frame rate from SPS (`num_units_in_tick` / `time_scale`)
  - Audio: `AudioCodecData.PacketDuration(data)`
- **Subsequent packets** — inter-frame diff: `DTS[n] - DTS[n-1]`

A receiver that gets `Duration == 0` should treat it as "unknown" and apply
its own estimate.

### 3.9 WallClockTime

Wall-clock capture or arrival time (e.g. derived from NTP or the device's
real-time clock). `time.Time{}` (zero value) means not set. Must not be used
for synchronisation when zero.

### 3.10 Data

Compressed payload. **Format contract:**

- **H.264 / H.265 video** — **AVCC format** (ISO 14496-15): one or more NALUs,
  each prefixed with a 4-byte big-endian length. This is the native format for
  MP4/fMP4 containers and the standard internal representation in Go media
  pipelines (Joy4, go2rtc).
  **Forbidden:** Annex-B start codes (`\x00\x00\x00\x01`) in `Data`.
  Parameter-set NALUs (SPS/PPS/VPS) must not appear in sample data — they
  belong in `CodecData` / `NewCodecs`.
- **MJPEG** — complete JPEG frame bytes.
- **Audio** — raw encoded samples. No container framing (no ADTS header for
  AAC; demuxer strips it).
- **Empty (`nil` or `len == 0`)** — valid only for a pure codec-change
  notification packet (`KeyFrame == true`, `NewCodecs != nil`, no media data).

Demuxer output format:

| Demuxer | Data format |
|---------|-------------|
| avgrabber (RTSP) | AVCC (converted from Annex-B, param-set NALUs stripped) |
| fMP4 demuxer | AVCC (native MP4 format, passed through as-is) |
| MP4 demuxer | AVCC (native MP4 format, passed through as-is) |

Muxer expectations:

| Muxer / transport | Expectation |
|-------------------|-------------|
| fMP4 / MP4 muxer | AVCC — written directly into mdat |
| MPEG-TS / HLS | Convert AVCC → Annex-B at write boundary |
| RTP packetizer | Extract raw NALUs from AVCC length-prefix framing |

### 3.11 PVAData

Per-frame object-detection analytics (`*av.PVAData`): vehicle count, people
count, bounding boxes, reference dimensions. `nil` when analytics are absent.
The fmp4 muxer serialises non-nil PVAData into emsg boxes; the fmp4 demuxer
deserialises them back.

### 3.12 NewCodecs

Non-nil on the keyframe packet that immediately follows a parameter-set change
(mid-stream codec change). Contains only the `Stream` entries that changed.
Receivers must update their per-stream `CodecData` state. Streams not listed
are unchanged.

A `NewCodecs` packet with `Data == nil` and `KeyFrame == true` is a pure
codec-change notification; no media data should be decoded from it.

---

## 4. Removed fields

- `IsParamSetNALU bool` — removed. Codec parameter sets are communicated
  exclusively through `NewCodecs` on the keyframe that follows.
- `StreamMeta` — removed. Was never populated by any demuxer.
- `Metadata any` — replaced by the strongly-typed `PVAData *PVAData`.

---

## 5. Invariants

1. `Idx` must match a `Stream.Idx` from `GetCodecs` or a `NewCodecs` update.
2. `DTS` is monotonically non-decreasing per stream index.
3. `Duration > 0` from any well-behaved demuxer.
4. `Data` is AVCC-framed for H.264/H.265 video — 4-byte BE length prefix per NALU.
5. `Data` must not contain Annex-B start codes or parameter-set NALUs.
6. `KeyFrame == true` implies video codec or `NewCodecs != nil`.
7. `NewCodecs != nil` implies `KeyFrame == true`.
8. `FrameID == 0` must not be treated as a meaningful frame identity.
