# AVF Frame Specification

**Package:** `github.com/vtpl1/vrtc/pkg/avf`
**Byte order (on-disk):** Big-endian throughout

---

## 1. Purpose

`avf.Frame` is the **in-memory representation** of one decoded AVF frame record.
It does **not** mirror the on-disk layout 1:1 — wire artifacts (`FrameSize`,
`RefFrameOff`, `CurrentFrameOff`, magic bytes) are consumed during decode and
are not retained in the struct.

For the on-disk binary layout see [`avf_spec.md`](../avf_spec.md).

---

## 2. Struct definition

```go
// BasicFrame holds the three fields that are present in every AVF frame header.
// FrameSize is intentionally absent — it is a wire-only length prefix;
// use len(Frame.Data) at runtime.
type BasicFrame struct {
    MediaType MediaType // codec identifier (§3)
    FrameType FrameType // frame classification (§4)
    TimeStamp int64     // presentation timestamp, milliseconds since Unix epoch
}

// StreamMeta carries optional per-frame metadata supplied by the stream source
// (e.g. received over a network transport). All fields are zero when the frame
// was decoded from an AVF file.
type StreamMeta struct {
    Bitrate         int32
    Fps             int32
    MotionAvailable int8
}

// Frame is the in-memory representation of one decoded AVF frame record.
type Frame struct {
    BasicFrame

    // FrameID is a stable identity assigned by the source device or stream.
    // It is comparable across sessions. 0 means "not assigned by source".
    FrameID int64

    // DurationMs is the frame's presentation duration in milliseconds.
    // Computed by the consumer (demuxer or proxy). 0 until set.
    DurationMs int64

    // Data is the decoded frame payload. Format depends on FrameType (§5).
    Data []byte

    // StreamMeta carries stream-source metadata. Zero when frame is from file.
    StreamMeta StreamMeta

    // Pvadata carries object-detection analytics for this frame.
    // Zero-valued when no analytics data is available.
    Pvadata PVAData
}
```

---

## 3. MediaType values

| Value | Constant(s) | Description |
|-------|-------------|-------------|
| 0 | `MJPG` | Motion JPEG |
| 1 | `MPEG` | MPEG video |
| 2 | `H264` | H.264 / AVC |
| 3 | `G711U`, `PCMU`, `MLAW`, `PCM`, `G711` | G.711 µ-law audio |
| 4 | `G711A`, `PCMA`, `ALAW` | G.711 A-law audio |
| 5 | `L16` | PCM 16-bit linear |
| 6 | `AAC` | AAC audio |
| 7 | `UNKNOWN` | Unknown / unsupported |
| 8 | `H265` | H.265 / HEVC |
| 9 | `G722` | G.722 audio |
| 10 | `G726` | G.726 audio |
| 11 | `OPUS` | Opus audio |
| 12 | `MP2L2` | MPEG-1 Layer II audio |

---

## 4. FrameType values

| Value | Constant | Meaning | Demuxer action |
|-------|----------|---------|----------------|
| 0 | `NON_REF_FRAME` | Generic video frame. Used by camera firmware that does not signal I/P distinction. Also covers H.264/H.265 auxiliary NALUs (SEI, AUD, filler) packed into a single frame record. | Emit as non-keyframe video packet |
| 1 | `I_FRAME` | Intra-coded keyframe. IDR for H.264/H.265; every frame for MJPEG. | Emit as keyframe video packet |
| 2 | `P_FRAME` | Inter-coded frame. Covers all non-IDR decodable video NALUs including B-frames. | Emit as non-keyframe video packet |
| 3 | `CONNECT_HEADER` | One codec parameter set NALU in Annex-B format. See §5.1. | Accumulate; do not emit as packet |
| 4–15 | *(reserved)* | Not currently defined. | Skip silently |
| 16 | `AUDIO_FRAME` | Raw encoded audio payload. No additional framing. | Emit as audio packet |
| 17 | `UNKNOWN_FRAME` | Unrecognized or unsupported frame type. | Skip silently |

### FrameType behavioral rules

- **R1** `UNKNOWN_FRAME` is skipped in **both** probe and decode — it never
  terminates a `CONNECT_HEADER` accumulation sequence.
- **R2** `CONNECT_HEADER` frames are **never** converted to `av.Packet`.
  Codec parameter sets travel exclusively via `av.Packet.NewCodecs` on the
  `I_FRAME` that follows.
- **R3** `FrameTypeFromPktData` returns `UNKNOWN_FRAME` (not `NON_REF_FRAME`)
  for codec types not present in its switch.

---

## 5. Data field format by FrameType

### 5.1 CONNECT_HEADER

Each `CONNECT_HEADER` frame carries **exactly one** parameter set NALU in
**Annex-B format** (`\x00\x00\x00\x01` + raw NALU bytes).

A keyframe group on disk is a sequence of consecutive frames:

**H.264:**
```
CONNECT_HEADER  MediaType=H264  Data = \x00\x00\x00\x01 + SPS_NALU
CONNECT_HEADER  MediaType=H264  Data = \x00\x00\x00\x01 + PPS_NALU
I_FRAME         MediaType=H264  Data = \x00\x00\x00\x01 + IDR_NALU
```

**H.265:**
```
CONNECT_HEADER  MediaType=H265  Data = \x00\x00\x00\x01 + VPS_NALU
CONNECT_HEADER  MediaType=H265  Data = \x00\x00\x00\x01 + SPS_NALU
CONNECT_HEADER  MediaType=H265  Data = \x00\x00\x00\x01 + PPS_NALU
I_FRAME         MediaType=H265  Data = \x00\x00\x00\x01 + IDR_NALU
```

**MJPEG:** No `CONNECT_HEADER` frames. Every frame is an `I_FRAME`.

The demuxer accumulates consecutive `CONNECT_HEADER` bytes and parses the full
codec only when the first subsequent video frame (`I_FRAME`, `P_FRAME`, or
`NON_REF_FRAME`) arrives.

### 5.2 I_FRAME and P_FRAME / NON_REF_FRAME

The canonical format is **exactly one NALU in Annex-B format**:

```
Data = \x00\x00\x00\x01 + single raw NALU bytes
```

The 4-byte start code is stripped by `FrameToPacket` before populating
`av.Packet.Data` (which carries a single raw NALU — see packet spec).

**Multi-NALU wire records (legacy cameras):** Some camera firmware packs an
entire access unit (multiple NALUs — e.g. SEI + IDR, or inline SPS + PPS + IDR)
into a single I_FRAME or P_FRAME wire record. This is a **wire-only artefact**.
The AVF demuxer and proxy **must** detect and split such records into individual
single-NALU `Frame` values using `avf.SplitFrame` before emitting packets.
Inline parameter-set NALUs (SPS/PPS for H.264; VPS/SPS/PPS for H.265) are
silently dropped during splitting — they duplicate the `CONNECT_HEADER` records
that precede keyframe groups.

### 5.3 AUDIO_FRAME

Data = raw encoded audio samples. No additional framing.
AAC frames may carry an ADTS header; `FrameToPacket` strips it.

---

## 6. RefFrameOff (on-disk only)

The on-disk wire field `RefFrameOff` points to the first `CONNECT_HEADER` of
the current keyframe group (i.e. the SPS frame). All frames in a keyframe
group — all `CONNECT_HEADER`s and the `I_FRAME` — carry the same `RefFrameOff`
value. This field is consumed during decode and is **not** stored in the
in-memory `Frame` struct.

---

## 7. Invariants

1. `len(Data) == 0` is valid for `CONNECT_HEADER` frames with no payload.
2. For `I_FRAME` and `P_FRAME`/`NON_REF_FRAME`, `len(Data) >= 5`
   (4-byte start code + at least 1 NALU byte).
3. After demuxing, each `I_FRAME`/`P_FRAME`/`NON_REF_FRAME` contains
   **exactly one NALU** in Annex-B format. Multi-NALU wire records must be
   split by the demuxer or proxy using `avf.SplitFrame` before further processing.
4. `FrameID == 0` means the source did not assign a stable identity.
5. `DurationMs == 0` means the consumer has not yet computed the duration
   (valid only before the consumer sets it).
6. `StreamMeta` fields are informational only and must not influence codec
   detection or frame routing logic.
