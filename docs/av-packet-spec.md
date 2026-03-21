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
    // ── Identity / routing ────────────────────────────────────────────────
    Idx      uint16    // stream index; matches Stream.Idx from GetCodecs
    CodecType CodecType // codec of this packet

    // FrameID is a stable identity assigned by the source device or stream.
    // Comparable across sessions for the same source. 0 = not assigned.
    FrameID int64

    // ── Flags ─────────────────────────────────────────────────────────────
    KeyFrame        bool // true iff this is an IDR/keyframe video packet
    IsDiscontinuity bool // DTS does not follow from the previous packet

    // ── Timing ────────────────────────────────────────────────────────────
    DTS           time.Duration // decode timestamp; monotonically non-decreasing
    PTSOffset     time.Duration // PTS = DTS + PTSOffset; zero for non-B-frame codecs
    Duration      time.Duration // nominal presentation duration; never 0 from a well-behaved demuxer
    WallClockTime time.Time     // wall-clock capture/arrival time; zero = not set

    // ── Payload ───────────────────────────────────────────────────────────
    // Data is raw NALU bytes with no prefix of any kind.
    // See §4 for the full format contract.
    Data []byte

    // Metadata carries object-detection analytics. nil when not available.
    Metadata *avf.PVAData

    // ── Codec change ──────────────────────────────────────────────────────
    // NewCodecs is non-nil on the I_FRAME packet that immediately follows a
    // CONNECT_HEADER sequence. Contains only the streams whose codec changed.
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

- **H.264 / H.265 video** — single raw NALU bytes. No `\x00\x00\x00\x01`
  Annex-B start code, no 4-byte AVCC length prefix. The NALU header byte is
  at `Data[0]`.
- **MJPEG** — complete JPEG frame bytes.
- **Audio** — raw encoded samples. No container framing (no ADTS header for
  AAC; demuxer strips it).
- **Empty (`nil` or `len == 0`)** — valid only for a pure codec-change
  notification packet (`KeyFrame == true`, `NewCodecs != nil`, no media data).

Every muxer is responsible for adding its own framing at the write boundary:

| Muxer / transport | Framing added |
|-------------------|---------------|
| AVF muxer | prepend `\x00\x00\x00\x01` (Annex-B) |
| fMP4 / MP4 muxer | wrap with 4-byte BE length (AVCC) |
| MPEG-TS / HLS | prepend `\x00\x00\x00\x01` (Annex-B) |
| RTP packetizer | raw NALU slice passed directly |

Every demuxer is responsible for stripping that framing before emitting:

| Demuxer | Stripping applied |
|---------|-------------------|
| AVF demuxer | strip 4-byte start code (`stripVideoPrefix`) |
| fMP4 demuxer | strip 4-byte AVCC length prefix (`normalizeVideoFromAVCC`) |
| MP4 demuxer | strip 4-byte AVCC length prefix *(gap — not yet implemented)* |
| RTP reassembler | raw NALU slices assembled into single `Data` |

### 3.11 Metadata

Typed pointer to `avf.PVAData` carrying object-detection analytics (vehicle
count, people count, bounding boxes, etc.). `nil` when no analytics are
attached. Must not be inspected by muxers that do not understand it — treat
as opaque and forward or discard.

### 3.12 NewCodecs

Non-nil on the `I_FRAME` packet that immediately follows a `CONNECT_HEADER`
sequence (mid-stream codec change). Contains only the `Stream` entries that
changed. Receivers must update their per-stream `CodecData` state. Streams
not listed are unchanged.

A `NewCodecs` packet with `Data == nil` and `KeyFrame == true` is a pure
codec-change notification; no media data should be decoded from it.

---

## 4. Removed field: IsParamSetNALU

`IsParamSetNALU bool` has been removed. Per the AVF frame spec (Option A),
`CONNECT_HEADER` frames **never** produce an `av.Packet`. Codec parameter sets
are communicated exclusively through `NewCodecs` on the `I_FRAME` that follows.
Any code path that previously set `IsParamSetNALU = true` is a bug.

---

## 5. Invariants

1. `Idx` must match a `Stream.Idx` from `GetCodecs` or a `NewCodecs` update.
2. `DTS` is monotonically non-decreasing per stream index.
3. `Duration > 0` from any well-behaved demuxer.
4. `Data[0]` is the NALU header byte for H.264/H.265 video packets with data.
5. `KeyFrame == true` implies video codec or `NewCodecs != nil`.
6. `NewCodecs != nil` implies `KeyFrame == true`.
7. `FrameID == 0` must not be treated as a meaningful frame identity.
