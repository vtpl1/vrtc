# AVF File Format Specification

**Format:** Audio/Video Frame (AVF) — proprietary container
**Byte order:** Big-endian throughout

---

## 1. Overview

AVF is a flat, frame-sequential binary container for multiplexed audio and video streams. There is **no file-level header** — the file begins immediately with the first frame record. Every frame is self-contained and self-delimiting.

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

Each frame record has the following layout. All multi-byte integers are **big-endian**.

```
Offset  Size  Type    Field
──────  ────  ──────  ─────────────────────────────────────────────────
0       4     bytes   Magic / Signature  ("00dc" — ASCII 0x30 0x30 0x64 0x63)
4       8     int64   RefFrameOff        (byte offset of the first frame of the last CONNECT_HEADER; -1 if none yet)
12      4     uint32  MediaType          (codec identifier, see §4)
16      4     uint32  FrameType          (frame classification, see §5)
20      8     int64   TimeStamp          (presentation timestamp, milliseconds)
28      4     uint32  FrameSize          (byte length of the payload that follows)
32      N     bytes   Data               (raw payload, N = FrameSize)
32+N    8     int64   CurrentFrameOff    (byte offset of this frame from start of file)
```

**Total fixed overhead per frame:** 40 bytes
**Total on-disk record size:** `40 + FrameSize` bytes

The magic `"00dc"` appears at the beginning of **every** frame record, not just at the file start.

---

## 4. MediaType Field

| Value | Constant(s)                              | Description           |
|-------|------------------------------------------|-----------------------|
| 0     | `MJPG`                                   | Motion JPEG           |
| 1     | `MPEG`                                   | MPEG video            |
| 2     | `H264`                                   | H.264 / AVC           |
| 3     | `G711U`, `PCMU`, `MLAW`, `PCM`, `G711`  | G.711 µ-law audio     |
| 4     | `G711A`, `PCMA`, `ALAW`                 | G.711 A-law audio     |
| 5     | `L16`                                    | PCM 16-bit linear     |
| 6     | `AAC`                                    | AAC audio             |
| 7     | `UNKNOWN`                                | Unknown/unsupported   |
| 8     | `H265`                                   | H.265 / HEVC          |
| 9     | `G722`                                   | G.722 audio           |
| 10    | `G726`                                   | G.726 audio           |
| 11    | `OPUS`                                   | Opus audio            |
| 12    | `MP2L2`                                  | MPEG-1 Layer II audio |

---

## 5. FrameType Field

| Value | Constant         | Description                                                           |
|-------|------------------|-----------------------------------------------------------------------|
| 0     | `H_FRAME`        | Non-reference / header frame                                          |
| 1     | `I_FRAME`        | Keyframe (H.264/H.265)                                                |
| 2     | `P_FRAME`        | Other frames                                                          |
| 3     | `CONNECT_HEADER` | Codec parameter sets (SPS/PPS for H.264; VPS/SPS/PPS for H.265)      |
| 16    | `AUDIO_FRAME`    | Audio sample data                                                     |
| 17    | `UNKNOWN_FRAME`  | Unknown frame type                                                    |

---

## 6. Payload Encoding (Data Field)

### 6.1 Video — CONNECT_HEADER frames

Contains raw NALUs in **Annex-B format** (start codes present). The reader detects and extracts SPS/PPS (H.264) or VPS/SPS/PPS (H.265) NALUs from these frames for codec initialisation.

All I_FRAMES should be preceded with the CONNECT_HEADER information (SPS/PPS for H.264; VPS/SPS/PPS for H.265)

### 6.2 Video — I_FRAME and P_FRAME frames

Contains a **4-byte length prefix** followed by the NALU data. The first 4 bytes are stripped by the reader before passing data to the decoder. The writer prepends a 4-byte start code (`\x00\x00\x00\x01`) to Annex-B data before storing.

```
[4 bytes: length or start code] [NALU bytes ...]
```

### 6.3 Audio frames (FrameType = AUDIO_FRAME)

Raw encoded audio samples with **no additional framing**. The entire `Data` field is the audio payload.

---

## 7. Offset Fields

### 7.1 CurrentFrameOff

Stores the **byte offset of this frame record** from the start of the file. Enables O(1) random access and reverse iteration without scanning.

The writer tracks a running `currentFrameOffset` counter, initialised to `0`, and increments it by the full frame record size after each write:

```
currentFrameOffset += 4 + (8+4+4+8+4) + FrameSize + 8
                    = 40 + FrameSize
```

### 7.2 RefFrameOff

---

## 8. Constraints and Limits

| Parameter              | Default Value          | Notes                                      |
|------------------------|------------------------|--------------------------------------------|
| Max frame size         | 3,145,728 bytes (3 MB) | Configurable; frames exceeding this are rejected |
| Video codec probe size | 10,000 frames          | `VideoProbeSize = 200 * 50`                |
| Audio codec probe size | 500 frames             | `AudioProbeSize = 10 * 50`                 |
| Internal read buffer   | 2,097,152 bytes (2 MB) | `bufio.NewReaderSize(in, 2048*1024)`       |

---
