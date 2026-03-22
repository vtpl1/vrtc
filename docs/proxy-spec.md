# ProxyMuxDemuxCloser Specification

**Package:** `github.com/vtpl1/vrtc/pkg/av/format/avf`

Related specs: [`avf-frame-spec.md`](avf-frame-spec.md), [`av-packet-spec.md`](av-packet-spec.md),
[`frame-packet-conversion-spec.md`](frame-packet-conversion-spec.md)

---

## 1. Purpose

`ProxyMuxDemuxCloser` is an in-memory bridge that connects a media **writer** (muxer
side) to a media **reader** (demuxer side), handling all `avf.Frame ↔ av.Packet`
conversions internally and routing packets through a buffered channel.

---

## 2. Supported configurations

| Writer (muxer side) | Reader (demuxer side) | Mode |
|---|---|---|
| `WriteFrame(avf.Frame)` | `GetCodecs` + `ReadPacket` | **A** — Frame→Packet |
| `WriteHeader` + `WritePacket(av.Packet)` | `ReadFrame` | **B** — Packet→Frame |
| `WriteHeader` + `WritePacket(av.Packet)` | `GetCodecs` + `ReadPacket` | **C** — Packet→Packet |

Mixing writer sides (calling `WriteFrame` after `WriteHeader`/`WritePacket`, or
vice-versa) is a programming error; the second call returns
`ErrConfiguredAsPacketMuxer` or `ErrConfiguredAsFrameMuxer` respectively.

Mixing reader sides (calling `ReadFrame` after `GetCodecs`/`ReadPacket`, or
vice-versa) similarly returns `ErrConfiguredAsFrameDemuxer` or
`ErrConfiguredAsPacketDemuxer`.

---

## 3. Mode A — Frame → Packet

### 3.1 Probe phase

Frames are scanned (up to `videoProbeSize` frames) until video and audio codecs
are identified:

1. **CONNECT_HEADER** frames: their `Data` bytes are concatenated into an
   internal buffer. Accumulation continues across adjacent CONNECT_HEADER frames.
2. **Video data frames** (I_FRAME / P_FRAME / NON_REF_FRAME) that arrive after
   a CONNECT_HEADER sequence: `parseVideoCodec` is called on the accumulated
   buffer. The codec is set if parsing succeeds.
3. **MJPEG I_FRAME** without a preceding CONNECT_HEADER: codec is set directly
   to `mjpeg.CodecData{}`.
4. **AUDIO_FRAME**: `parseAudioCodec` is called on the first occurrence.
5. **UNKNOWN_FRAME**: silently skipped. Does **not** terminate CONNECT_HEADER
   accumulation.
6. Video probe ends when a codec is found **or** `videoProbeSize` frames have
   been scanned.
7. Audio probe ends immediately once the video probe completes (whether or not
   audio was found). Audio frames arriving after that are not detected.
8. **Audio-only streams are not supported.** If no video codec is found after
   `videoProbeSize` frames, `WriteFrame` returns `ErrNoVideoCodecFound` and
   `GetCodecs` returns `(nil, ErrNoVideoCodecFound)`.

### 3.2 Headers signal

Once both probes complete and a video codec was found, `writeHeaderFromCodecs`
builds the stream list and signals `GetCodecs` / `ReadPacket`:

- Stream index 0: video codec.
- Stream index 1: audio codec (only when one was found during probe).

### 3.3 Forward phase

After headers are signaled, each subsequent `WriteFrame` call converts the frame
to an `av.Packet` and enqueues it:

| Frame type | Action |
|---|---|
| `CONNECT_HEADER` | Accumulate into post-probe buffer; do not enqueue. |
| `UNKNOWN_FRAME` | Skip silently; does **not** terminate accumulation. |
| Video data (I/P/NON_REF) after accumulation | Parse accumulated bytes with `parseVideoCodec`. If successful, update `videoCodec` and attach `NewCodecs` to the first emitted packet (mid-stream codec change). |
| Video data / audio (no preceding accumulation) | Split via `avf.SplitFrame`, then convert each split frame via `FrameToPacket(sf, idx, codec)` and enqueue. |
| Audio when no audio stream | Drop silently. |

**NALU splitting**: Before calling `FrameToPacket`, video data frames are passed
through `avf.SplitFrame`. This splits multi-NALU Annex-B access units (packed by
legacy cameras) into individual single-NALU frames. Inline parameter-set NALUs
(SPS/PPS/VPS) are silently dropped. `NewCodecs` is attached to the **first**
packet of the split group. See `docs/frame-packet-conversion-spec.md §6`.

`FrameToPacket` handles:
- Stripping the 4-byte Annex-B start code from video `Data` → single raw NALU in `pkt.Data`.
- Stripping ADTS header from AAC `Data`.
- Setting `KeyFrame`, `DTS`, `WallClockTime`, `Duration`, `FrameID`, `Metadata`.

---

## 4. Mode B — Packet → Frame

- `WriteHeader(streams)` immediately signals headers.
- `WritePacket(pkt)` enqueues the packet.
- `WriteTrailer` is a no-op placeholder.
- First `ReadFrame` call waits for headers and then returns initial
  CONNECT_HEADER frames for the first video stream (one per parameter set NALU:
  SPS + PPS for H.264; VPS + SPS + PPS for H.265).
- Subsequent `ReadFrame` calls dequeue one `av.Packet` at a time and convert it
  via `PacketToFrames(pkt, codec)`. Extra frames (e.g. the CONNECT_HEADER frames
  emitted before a keyframe) are buffered in `readFramePending` and returned on
  successive calls.
- `PacketToFrames` handles:
  - H.264/H.265 keyframe → `CONNECT_HEADER(SPS)`, `CONNECT_HEADER(PPS)`, `I_FRAME`.
  - H.265 keyframe → `CONNECT_HEADER(VPS)`, `CONNECT_HEADER(SPS)`, `CONNECT_HEADER(PPS)`, `I_FRAME`.
  - MJPEG keyframe → `I_FRAME` only (no CONNECT_HEADERs).
  - Non-keyframe video → `P_FRAME` with Annex-B prefix prepended.
  - Audio → `AUDIO_FRAME`, data passed through as-is.

---

## 5. Mode C — Packet → Packet (pass-through)

- `WriteHeader` signals headers to `GetCodecs`.
- `WritePacket` enqueues packets to the channel.
- `ReadPacket` dequeues packets from the channel.
- No conversion is performed.

---

## 6. Close behaviour

`Close()` shuts down the proxy in a concurrency-safe way:

- Closes `headersAvailable` — unblocks any waiting `GetCodecs` call.
- Closes `packets` — unblocks any waiting `ReadPacket` or `ReadFrame` call.
- Closes `closing` — causes in-progress `WriteFrame`/`WritePacket` calls to
  return `io.EOF` or `ErrProxyIsClosing`.

`GetCodecs` returns `(nil, io.EOF)` when `Close()` is called before headers are
written. Once headers have been written, `GetCodecs` returns them normally.

---

## 7. Error catalogue

| Error | Meaning |
|---|---|
| `ErrConfiguredAsPacketMuxer` | `WriteFrame` called after `WriteHeader`/`WritePacket` |
| `ErrConfiguredAsFrameMuxer` | `WriteHeader`/`WritePacket` called after `WriteFrame` |
| `ErrConfiguredAsPacketDemuxer` | `ReadFrame` called after `GetCodecs`/`ReadPacket` |
| `ErrConfiguredAsFrameDemuxer` | `GetCodecs`/`ReadPacket` called after `ReadFrame` |
| `ErrNoVideoCodecFound` | Probe exhausted `videoProbeSize` frames without finding a video codec |
| `ErrEmptyHeader` | `WriteHeader` called with an empty stream list |
| `ErrHeaderNotWritten` | `WritePacket` called before `WriteHeader` |
| `ErrProxyIsClosing` | Write attempted on a closed proxy |

---

## 8. Invariants

1. Only one writer mode and one reader mode may be active per proxy instance.
2. Video stream is always at index 0 (in Mode A).
3. Audio stream is at index 1 when present (in Mode A).
4. `av.Packet.Data` emitted in Mode A contains raw NALU bytes — no start code,
   no length prefix.
5. `avf.Frame.Data` emitted in Mode B contains Annex-B encoded bytes
   (`\x00\x00\x00\x01` + NALU) for video.
6. `NewCodecs` is set on the packet produced by the first video data frame that
   follows a post-probe CONNECT_HEADER sequence (mid-stream codec change).
7. CONNECT_HEADER frames are never enqueued as packets.
8. UNKNOWN_FRAME never terminates CONNECT_HEADER accumulation.
