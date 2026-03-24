# Frame Definition

Updated 2026-03-24.

## 1. Scope

This document defines the public frame contract exposed by `avgrabber_api.h`.

A frame is the result of one successful `avgrabber_next_frame()` call. It
consists of two parts:

- an `AVGrabberFrameHeader` — fixed-size metadata struct
- a payload byte buffer — the raw encoded media data

The same logical contract applies to the push model
(`AVGrabberFrameCallback`): the callback receives the same header and data
pointers for the duration of the call.

## 2. Structure

A frame obtained from `avgrabber_next_frame()` is represented by an opaque
`AVGrabberFrame*` handle. The content is accessed through accessor functions:

```c
const AVGrabberFrameHeader* h = avgrabber_frame_header(frame);
const uint8_t*              d = avgrabber_frame_data(frame);
// process: h->frame_size bytes starting at d
avgrabber_frame_release(frame);
```

The two parts always describe the same media sample:

- `h` contains the metadata for the sample
- `d` contains exactly `h->frame_size` payload bytes

## 3. Ownership and Lifetime

`AVGrabberFrame*` is a library-owned object drawn from an internal pool.

The frame is valid — and the pointers returned by `avgrabber_frame_header()`
and `avgrabber_frame_data()` remain valid — from the `avgrabber_next_frame()`
return until `avgrabber_frame_release()` is called.

Rules:
- call `avgrabber_frame_release()` exactly once per frame
- all frames must be released before calling `avgrabber_close()`
- do not access `h` or `d` after calling `avgrabber_frame_release()`

In the push model (`AVGrabberFrameCallback`), the header and data pointers are
valid only for the duration of the callback invocation. Copy data before
returning if retention is needed; do not call `avgrabber_frame_release()`.

## 4. `AVGrabberFrameHeader`

`AVGrabberFrameHeader` is a 56-byte, naturally aligned struct (no `#pragma pack`):

```c
typedef struct {
  int32_t  frame_type;     // AVGRABBER_FRAME_* constant
  int32_t  media_type;     // AVGRABBER_MEDIA_VIDEO or AVGRABBER_MEDIA_AUDIO
  int32_t  frame_size;     // payload bytes; matches avgrabber_frame_data() length
  uint8_t  codec_type;     // AVGRABBER_CODEC_* constant
  uint8_t  flags;          // bitmask of AVGRABBER_FLAG_* bits
  uint8_t  _pad[2];        // always zero
  int64_t  wall_clock_ms;  // wall-clock ms anchored to system_clock::now()
  int64_t  ntp_ms;         // raw NTP ms from camera (0 = not yet RTCP-synced)
  int64_t  pts_ticks;      // PTS in stream clock ticks (64-bit, unwrapped)
  int64_t  dts_ticks;      // DTS in stream clock ticks (= pts_ticks for I/P-only)
  uint32_t duration_ticks; // sample duration in clock ticks (0 = unknown for video)
  uint32_t _pad2;          // always zero
} AVGrabberFrameHeader;    // sizeof == 56
```

Field meanings:

- `frame_type`: sample classification — see section 5
- `media_type`: `AVGRABBER_MEDIA_VIDEO` (0) or `AVGRABBER_MEDIA_AUDIO` (1)
- `frame_size`: exact payload size in bytes
- `codec_type`: codec identifier — `AVGRABBER_CODEC_H264`, `AVGRABBER_CODEC_H265`,
  `AVGRABBER_CODEC_AAC`, `AVGRABBER_CODEC_G711U`, `AVGRABBER_CODEC_OPUS`, etc.
- `flags`: bitmask; see section 6
- `wall_clock_ms`: local system wall clock, milliseconds since Unix epoch
- `ntp_ms`: camera NTP-derived time (only meaningful when `AVGRABBER_FLAG_NTP_SYNCED` is set)
- `pts_ticks`: 64-bit unwrapped PTS in stream clock ticks; timebase is `video_clock_rate`
  (typically 90000 Hz) for video and `audio_sample_rate` for audio; use as the primary
  timestamp for fMP4 `trun` composition offsets
- `dts_ticks`: DTS in the same timebase; equals `pts_ticks` for IP cameras that do not use
  B-frames (the common case)
- `duration_ticks`: sample duration for fMP4 `trun` entries; audio frames set this to
  `AVGrabberStreamInfo.audio_samples_per_frame` once known; video frames leave it 0

## 5. Frame Types

`frame_type` is one of:

| Constant | Value | Meaning |
|----------|-------|---------|
| `AVGRABBER_FRAME_PARAM_SET` | 0 | Decoder parameter sets (SPS+PPS / VPS+SPS+PPS), Annex-B |
| `AVGRABBER_FRAME_KEY` | 1 | IDR / random-access video keyframe, Annex-B |
| `AVGRABBER_FRAME_DELTA` | 2 | Non-key (delta) video frame, Annex-B |
| `AVGRABBER_FRAME_AUDIO` | 16 | Raw audio payload |
| `AVGRABBER_FRAME_UNKNOWN` | 255 | Unclassified frame; inspect `media_type` and `codec_type` |

See [frame_types.md](./frame_types.md) for full definitions.

## 6. Flags

`flags` is a bitmask:

| Constant | Bit | Meaning |
|----------|-----|---------|
| `AVGRABBER_FLAG_NTP_SYNCED` | 0x01 | `ntp_ms` is RTCP-anchored to camera wall clock |
| `AVGRABBER_FLAG_DISCONTINUITY` | 0x02 | Gap or clock reset preceded this frame |
| `AVGRABBER_FLAG_KEYFRAME` | 0x04 | Random-access video frame |
| `AVGRABBER_FLAG_HAS_SEI` | 0x10 | One or more SEI NAL units present in this access unit |

## 7. Payload

`avgrabber_frame_data()` returns exactly `h->frame_size` bytes.

### Video

If `h->media_type == AVGRABBER_MEDIA_VIDEO`, the payload is an Annex-B byte stream.

Each NAL unit is prefixed with the four-byte start code `0x00 0x00 0x00 0x01`.

- `AVGRABBER_FRAME_PARAM_SET`: SPS+PPS (H.264) or VPS+SPS+PPS (H.265)
- `AVGRABBER_FRAME_KEY`: keyframe slices only (parameter sets are in the preceding PARAM_SET frame)
- `AVGRABBER_FRAME_DELTA`: non-key video access unit

See [connect_header.md](./connect_header.md) and [key_frame.md](./key_frame.md).

### Audio

If `h->media_type == AVGRABBER_MEDIA_AUDIO`, the payload format depends on `h->codec_type`:

| `codec_type` | Format |
|---|---|
| `AVGRABBER_CODEC_AAC` | ADTS-framed AAC (7-byte header prepended by library) |
| `AVGRABBER_CODEC_G711U` | Raw 8-bit µ-law PCM, 8 kHz |
| `AVGRABBER_CODEC_G711A` | Raw 8-bit A-law PCM, 8 kHz |
| `AVGRABBER_CODEC_G722` | Raw G.722 ADPCM, 16 kHz encoded bandwidth |
| `AVGRABBER_CODEC_G726` | Raw G.726 ADPCM, 8 kHz |
| `AVGRABBER_CODEC_OPUS` | Raw Opus packets |
| `AVGRABBER_CODEC_L16` | Raw signed 16-bit PCM |

## 8. Ordering Rules

Frames are returned in stream arrival order. Important consequences:

- `AVGRABBER_FRAME_PARAM_SET` is emitted as a standalone frame before the first
  keyframe, after reconnect, and whenever the camera sends new parameter sets
- a random-access video point is represented as two successive frames:
  `AVGRABBER_FRAME_PARAM_SET` then `AVGRABBER_FRAME_KEY`
- ordinary predictive video arrives as `AVGRABBER_FRAME_DELTA`
- audio frames are interleaved according to stream arrival timing

Consumers must preserve frame order exactly as returned by repeated
`avgrabber_next_frame()` calls.

## 9. Consumer Rules

Consumers should:

1. inspect `header->media_type`
2. inspect `header->frame_type`
3. interpret the payload according to `codec_type` and the frame-type docs
4. preserve `AVGRABBER_FRAME_PARAM_SET` as a normal frame in any binding or file format

Consumers must not:

- assume every frame is displayable video
- strip Annex-B start codes from video implicitly
- merge `AVGRABBER_FRAME_PARAM_SET` into a following keyframe silently
- access `h` or `d` after calling `avgrabber_frame_release()`

## 10. Equivalent Go Contract Example

```go
// #cgo LDFLAGS: -lAudioVideoGrabber2
// #include "avgrabber_api.h"
import "C"
import "unsafe"

// Frame types
const (
    FrameTypeParamSet = C.AVGRABBER_FRAME_PARAM_SET // 0
    FrameTypeKey      = C.AVGRABBER_FRAME_KEY        // 1
    FrameTypeDelta    = C.AVGRABBER_FRAME_DELTA      // 2
    FrameTypeAudio    = C.AVGRABBER_FRAME_AUDIO      // 16
    FrameTypeUnknown  = C.AVGRABBER_FRAME_UNKNOWN    // 255
)

// Media types
const (
    MediaTypeVideo = C.AVGRABBER_MEDIA_VIDEO // 0
    MediaTypeAudio = C.AVGRABBER_MEDIA_AUDIO // 1
)

// Flags
const (
    FlagNTPSynced     = C.AVGRABBER_FLAG_NTP_SYNCED    // 0x01
    FlagDiscontinuity = C.AVGRABBER_FLAG_DISCONTINUITY // 0x02
    FlagKeyframe      = C.AVGRABBER_FLAG_KEYFRAME      // 0x04
    FlagHasSEI        = C.AVGRABBER_FLAG_HAS_SEI       // 0x10
)

type Frame struct {
    FrameType     int32
    MediaType     int32
    FrameSize     int32
    CodecType     uint8
    Flags         uint8
    WallClockMs   int64
    NtpMs         int64
    PTSTicks      int64
    DTSTicks      int64
    DurationTicks uint32
    Data          []byte
}

func nextFrame(session *C.AVGrabberSession, timeoutMs int32) (*Frame, int) {
    var cframe *C.AVGrabberFrame
    rc := C.avgrabber_next_frame(session, C.int32_t(timeoutMs), &cframe)
    if rc != C.AVGRABBER_OK {
        return nil, int(rc)
    }
    defer C.avgrabber_frame_release(cframe)

    h := C.avgrabber_frame_header(cframe)
    d := C.avgrabber_frame_data(cframe)
    sz := int(h.frame_size)

    f := &Frame{
        FrameType:     int32(h.frame_type),
        MediaType:     int32(h.media_type),
        FrameSize:     int32(h.frame_size),
        CodecType:     uint8(h.codec_type),
        Flags:         uint8(h.flags),
        WallClockMs:   int64(h.wall_clock_ms),
        NtpMs:         int64(h.ntp_ms),
        PTSTicks:      int64(h.pts_ticks),
        DTSTicks:      int64(h.dts_ticks),
        DurationTicks: uint32(h.duration_ticks),
        Data:          C.GoBytes(unsafe.Pointer(d), C.int(sz)),
    }
    return f, 0
}
```

What matters at the contract level:

- the Go value contains one metadata object and one matching payload slice
- `len(Data) == FrameSize`
- all `AVGrabberFrameHeader` fields are preserved without reinterpretation
- `AVGRABBER_FRAME_PARAM_SET` remains a standalone frame
- video remains Annex-B
- audio remains codec-native payload bytes
- `Flags` is handled as a bitmask, not collapsed to individual booleans

### Flag handling

```go
func (f *Frame) IsNTPSynced() bool     { return f.Flags&FlagNTPSynced != 0 }
func (f *Frame) IsDiscontinuity() bool { return f.Flags&FlagDiscontinuity != 0 }
func (f *Frame) IsKeyframe() bool      { return f.Flags&FlagKeyframe != 0 }
func (f *Frame) HasSEI() bool          { return f.Flags&FlagHasSEI != 0 }
```

Unknown future flag bits should round-trip unchanged so newer library versions
remain forward-compatible with older bindings.

## 11. Python ctypes Mapping

```python
import ctypes

class AVGrabberFrameHeader(ctypes.Structure):
    _fields_ = [
        ("frame_type",     ctypes.c_int32),
        ("media_type",     ctypes.c_int32),
        ("frame_size",     ctypes.c_int32),
        ("codec_type",     ctypes.c_uint8),
        ("flags",          ctypes.c_uint8),
        ("_pad",           ctypes.c_uint8 * 2),
        ("wall_clock_ms",  ctypes.c_int64),
        ("ntp_ms",         ctypes.c_int64),
        ("pts_ticks",      ctypes.c_int64),
        ("dts_ticks",      ctypes.c_int64),
        ("duration_ticks", ctypes.c_uint32),
        ("_pad2",          ctypes.c_uint32),
    ]

assert ctypes.sizeof(AVGrabberFrameHeader) == 56
```

## 12. Practical Summary

- `header->media_type` says whether the sample is video or audio
- `header->frame_type` says what kind of sample it is
- `avgrabber_frame_data()` returns exactly `header->frame_size` bytes of payload
- video payloads are Annex-B; audio payloads are codec-native
- ownership is explicit: release each frame with `avgrabber_frame_release()`
