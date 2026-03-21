# AVF Wire Format Specification

**Format:** Audio/Video Frame (AVF) — proprietary container
**Byte order:** Big-endian throughout

> For the in-memory `avf.Frame` struct see [`avf-frame-spec.md`](avf-frame-spec.md).
> For `av.Packet` see [`av-packet-spec.md`](av-packet-spec.md).
> For conversion between Frame and Packet see [`frame-packet-conversion-spec.md`](frame-packet-conversion-spec.md).

---

## 1. Overview

AVF is a flat, frame-sequential binary container for multiplexed audio and
video streams. There is **no file-level header** — the file begins immediately
with the first frame record. Every frame record is self-contained and
self-delimiting.

---

## 2. File Structure

```
┌──────────────────────┐
│  Frame Record 0      │  ← file offset 0
├──────────────────────┤
│  Frame Record 1      │
├──────────────────────┤
│  Frame Record 2      │
│  ...                 │
└──────────────────────┘
```

There is no end-of-file marker. Reading continues until `io.EOF`.

---

## 3. Frame Record Layout

Each frame record has the following layout. All multi-byte integers are
**big-endian**.

```
Offset  Size  Type    Field
──────  ────  ──────  ──────────────────────────────────────────────────────
0       4     bytes   Magic           ("00dc" — 0x30 0x30 0x64 0x63)
4       8     int64   RefFrameOff     byte offset of the first CONNECT_HEADER
                                      of the current keyframe group; -1 if none
12      4     uint32  MediaType       codec identifier (§4)
16      4     uint32  FrameType       frame classification (§5)
20      8     int64   TimeStamp       presentation timestamp, milliseconds
28      4     uint32  FrameSize       byte length of the payload that follows
32      N     bytes   Data            raw payload, N = FrameSize (§6)
32+N    8     int64   CurrentFrameOff byte offset of this frame from file start
```

**Total fixed overhead per frame:** 40 bytes
**Total on-disk record size:** `40 + FrameSize` bytes

The magic `"00dc"` appears at the start of **every** frame record, not only at
the file start.

---

## 4. MediaType Field

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

## 5. FrameType Field

| Value | Constant | Description | Reader action |
|-------|----------|-------------|---------------|
| 0 | `NON_REF_FRAME` | Generic video frame. Used by camera firmware that does not signal I/P distinction. Also covers H.264/H.265 auxiliary NALUs (SEI, AUD, filler). | Emit as non-keyframe video packet |
| 1 | `I_FRAME` | Intra-coded keyframe. IDR for H.264/H.265; every frame for MJPEG. | Emit as keyframe video packet |
| 2 | `P_FRAME` | Inter-coded frame. Covers all non-IDR decodable video NALUs including B-frames. | Emit as non-keyframe video packet |
| 3 | `CONNECT_HEADER` | One codec parameter set NALU (see §6.1). | Accumulate; do not emit as packet |
| 4–15 | *(reserved)* | Not defined. | Skip silently |
| 16 | `AUDIO_FRAME` | Raw encoded audio payload. | Emit as audio packet |
| 17 | `UNKNOWN_FRAME` | Unrecognized frame type. | Skip silently |

### FrameType rules

- **R1** Each `CONNECT_HEADER` carries **exactly one** parameter set NALU.
- **R2** `CONNECT_HEADER` frames are never emitted as `av.Packet`. Codec
  changes travel via `av.Packet.NewCodecs` on the following `I_FRAME`.
- **R3** `UNKNOWN_FRAME` is skipped in both probe and decode. It does **not**
  terminate a `CONNECT_HEADER` accumulation sequence.
- **R4** Reserved values (4–15) are skipped silently.

---

## 6. Payload Encoding (Data Field)

### 6.1 CONNECT_HEADER frames

Each `CONNECT_HEADER` carries **exactly one** parameter set NALU in Annex-B
format:

```
Data = \x00\x00\x00\x01 + <raw NALU bytes>
```

A keyframe group is a sequence of consecutive frame records:

**H.264 (3 frames):**
```
CONNECT_HEADER  MediaType=H264  Data = \x00\x00\x00\x01 + SPS_NALU
CONNECT_HEADER  MediaType=H264  Data = \x00\x00\x00\x01 + PPS_NALU
I_FRAME         MediaType=H264  Data = \x00\x00\x00\x01 + IDR_NALU
```

**H.265 (4 frames):**
```
CONNECT_HEADER  MediaType=H265  Data = \x00\x00\x00\x01 + VPS_NALU
CONNECT_HEADER  MediaType=H265  Data = \x00\x00\x00\x01 + SPS_NALU
CONNECT_HEADER  MediaType=H265  Data = \x00\x00\x00\x01 + PPS_NALU
I_FRAME         MediaType=H265  Data = \x00\x00\x00\x01 + IDR_NALU
```

**MJPEG:** No `CONNECT_HEADER` frames. Every frame is an `I_FRAME`.

The reader accumulates consecutive `CONNECT_HEADER` bytes and parses the full
codec only when the first subsequent video frame (`I_FRAME`, `P_FRAME`, or
`NON_REF_FRAME`) arrives.

### 6.2 I_FRAME, P_FRAME, NON_REF_FRAME

```
Data = \x00\x00\x00\x01 + <NALU bytes>
```

The 4-byte Annex-B start code is stripped by the reader before populating
`av.Packet.Data` (which carries raw NALU bytes — no prefix).

### 6.3 AUDIO_FRAME

Raw encoded audio samples with no additional framing. The entire `Data` field
is the audio payload. AAC frames may include an ADTS header; the reader strips
it before populating `av.Packet.Data`.

---

## 7. Offset Fields

### 7.1 CurrentFrameOff

Stores the **byte offset of this frame record** from the start of the file.
Enables O(1) random access and reverse iteration without a full scan.

The writer maintains a running counter initialised to `0` and increments it
after each write:

```
currentFrameOff += 40 + FrameSize
```

### 7.2 RefFrameOff

Stores the byte offset of the **first `CONNECT_HEADER`** (SPS) of the current
keyframe group. All frame records in a keyframe group — all `CONNECT_HEADER`
frames and the `I_FRAME` — carry the same `RefFrameOff` value.

The writer sets `lastConnectHdrOff = currentOffset` immediately before writing
the first `CONNECT_HEADER` of a group, and uses that value unchanged for all
subsequent records in the group.

`RefFrameOff = -1` means no `CONNECT_HEADER` has been written yet.

---

## 8. Constraints and Limits

| Parameter | Value | Notes |
|-----------|-------|-------|
| Max frame size | 3,145,728 bytes (3 MB) | Frames exceeding this are rejected by the reader |
| Video codec probe size | 10,000 frames | `videoProbeSize = 200 * 50` |
| Audio codec probe size | 500 frames | `audioProbeSize = 10 * 50` (secondary scan after video probe) |
| Internal read buffer | 2,097,152 bytes (2 MB) | `bufio.NewReaderSize(r, 2*1024*1024)` |

---

## 9. Reading Algorithm (Probe Phase)

```
1. Read frames until videoCodec found OR videoProbeSize reached:
     AUDIO_FRAME      → attempt audioCodec detection
     CONNECT_HEADER   → append Data to connectHeaderBuf; set accumulating=true
     I_FRAME/P_FRAME/NON_REF_FRAME (while accumulating):
                      → parse videoCodec from connectHeaderBuf
                      → set accumulating=false
     I_FRAME (MJPEG, not accumulating):
                      → videoCodec = mjpeg.CodecData{}
     UNKNOWN_FRAME    → skip (do NOT terminate accumulation)

2. If audioCodec not yet found: read up to audioProbeSize more frames:
     AUDIO_FRAME      → attempt audioCodec detection

3. If neither videoCodec nor audioCodec found → return ErrNoCodecFound
```

---

## 10. Reading Algorithm (Decode Phase)

```
For each frame record:
    CONNECT_HEADER   → accumulate; if codec changed → set pendingCodecChange
                       → skip (no packet emitted)
    I_FRAME / P_FRAME / NON_REF_FRAME:
                     → emit av.Packet (KeyFrame = FrameType==I_FRAME)
                     → attach pendingCodecChange as NewCodecs if pending
    AUDIO_FRAME      → emit av.Packet (audio)
    UNKNOWN_FRAME    → skip
    reserved (4–15)  → skip
```
