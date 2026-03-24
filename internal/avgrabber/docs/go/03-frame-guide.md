# Frame Guide

## Frame anatomy

Every call to `NextFrame()` returns one `Frame`:

```
Frame
├── FrameType     int32    — classification (PARAM_SET / KEY / DELTA / AUDIO / UNKNOWN)
├── MediaType     int32    — VIDEO (0) or AUDIO (1)
├── FrameSize     int32    — exact payload length in bytes
├── CodecType     uint8    — H264, H265, AAC, G711U, G711A, G722, G726, Opus, …
├── Flags         uint8    — bitmask: NTPSynced | Discontinuity | Keyframe | HasSEI
├── WallClockMS   int64    — server wall clock, ms since Unix epoch
├── NTPMS         int64    — camera NTP ms (valid when FlagNTPSynced is set)
├── PTSTicks      int64    — presentation timestamp in stream clock ticks (never wraps)
├── DTSTicks      int64    — decode timestamp (== PTSTicks for IP cameras without B-frames)
├── DurationTicks uint32   — audio: samples per frame; video: 0
└── Data          []byte   — exactly FrameSize bytes of payload
```

## Frame ordering

The library delivers frames in stream arrival order. You will always see this
pattern at stream start, after reconnect, or after a parameter-set change:

```
PARAM_SET   ← apply this to configure your decoder / muxer init segment
KEY         ← first random-access frame; safe muxer segment start
DELTA
DELTA
…
PARAM_SET   ← parameter sets changed (or reconnect)
KEY
DELTA
…
```

Audio frames are interleaved with video according to real arrival timing:

```
PARAM_SET (video)
KEY       (video)
AUDIO
DELTA     (video)
AUDIO
DELTA     (video)
AUDIO
…
```

**Rules your code must follow:**

1. Never assume two consecutive frames are the same type.
2. Keep the most recently seen `PARAM_SET` payload; apply it when a `KEY` arrives.
3. A new `PARAM_SET` replaces the previous one — update your codec configuration.
4. Never merge `PARAM_SET` payload into a `KEY` payload silently.

## Video payload format — Annex-B

All video frames (`PARAM_SET`, `KEY`, `DELTA`) are in **Annex-B** format.

Every NAL unit is prefixed with a 4-byte start code:

```
00 00 00 01 [NAL unit bytes]
00 00 00 01 [NAL unit bytes]
…
```

### PARAM_SET payload

Carries only the decoder parameter sets — no slice data.

**H.264:**
```
00 00 00 01 67 [SPS bytes]
00 00 00 01 68 [PPS bytes]
```
NAL type = `nal[0] & 0x1F` → SPS = 7, PPS = 8

**H.265:**
```
00 00 00 01 40 [VPS bytes]   (NAL type = (nal[0] & 0x7E) >> 1 → 32)
00 00 00 01 42 [SPS bytes]   (NAL type 33)
00 00 00 01 44 [PPS bytes]   (NAL type 34)
```

### KEY payload

Contains IDR/BLA/CRA slice data only. Parameter sets are **not** embedded —
they arrive as the preceding `PARAM_SET` frame.

**H.264 IDR:**
```
00 00 00 01 65 [IDR slice bytes]
```
NAL type = 5

**H.265 IDR:**
```
00 00 00 01 26 [IDR slice bytes]    (NAL type 19 = IDR_W_RADL)
```

Both `FrameType == FrameTypeKey` and `Flags & FlagKeyframe != 0` will be set.

### DELTA payload

Non-key video frame. Contains P-slice or similar data (NAL type 1 for H.264).

```
00 00 00 01 61 [slice bytes]
```

`FlagHasSEI` may also be set if SEI NAL units are present in the same access unit.

### Converting Annex-B → AVCC / HVCC (required for fMP4)

fMP4 stores video in AVCC (H.264) or HVCC (H.265) format: each NAL unit is
preceded by a **4-byte big-endian length** instead of a start code.

```go
// AnnexBToAVCC replaces Annex-B start codes with 4-byte length prefixes.
// Works for both H.264 (AVCC) and H.265 (HVCC).
func AnnexBToAVCC(annexB []byte) []byte {
    out := make([]byte, 0, len(annexB))
    start := 0
    for i := 0; i < len(annexB)-3; {
        if annexB[i] == 0 && annexB[i+1] == 0 && annexB[i+2] == 0 && annexB[i+3] == 1 {
            if i > start {
                nal := annexB[start:i]
                length := len(nal)
                out = append(out,
                    byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
                out = append(out, nal...)
            }
            start = i + 4
            i += 4
        } else {
            i++
        }
    }
    // last NAL unit
    if start < len(annexB) {
        nal := annexB[start:]
        length := len(nal)
        out = append(out,
            byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
        out = append(out, nal...)
    }
    return out
}
```

## Audio payload formats

Audio frames always have `MediaType == MediaAudio` and `FrameType == FrameTypeAudio`.

The payload format depends on `CodecType`:

| `CodecType` | Constant | Format |
|-------------|----------|--------|
| 6 | `CodecAAC` | ADTS-framed AAC. 7-byte ADTS header prepended by library. Strip for fMP4 `mdat`. |
| 3 | `CodecG711U` | Raw 8-bit µ-law PCM at 8 kHz. |
| 4 | `CodecG711A` | Raw 8-bit A-law PCM at 8 kHz. |
| 9 | `CodecG722` | Raw G.722 ADPCM. 8 kHz RTP clock; decodes to 16 kHz. |
| 10 | `CodecG726` | Raw G.726 ADPCM at 8 kHz. |
| 11 | `CodecOpus` | Raw Opus packets. 48 kHz RTP clock. |
| 5 | `CodecL16` | Raw signed 16-bit PCM. |

### Stripping the AAC ADTS header

For fMP4 muxing, AAC samples go directly into `mdat` without the ADTS wrapper.
The `AudioSpecificConfig` (needed for the `esds` box) lives in the first two
bytes of the ADTS header's audio-specific config field — parse it from the
first PARAM_SET-adjacent audio frame.

```go
// StripADTS removes the 7-byte (or 9-byte, if CRC present) ADTS header.
// Returns the raw AAC frame and the AudioSpecificConfig extracted from the header.
func StripADTS(adts []byte) (rawAAC []byte, asc []byte, err error) {
    if len(adts) < 7 {
        return nil, nil, errors.New("too short for ADTS header")
    }
    if adts[0] != 0xFF || adts[1]&0xF0 != 0xF0 {
        return nil, nil, errors.New("not an ADTS frame")
    }
    headerLen := 7
    if adts[1]&0x01 == 0 { // protection_absent == 0 means CRC present
        headerLen = 9
    }
    // Build AudioSpecificConfig (2 bytes) from ADTS fields:
    //   objectType  = ((adts[2] >> 6) & 0x3) + 1
    //   samplingIdx = (adts[2] >> 2) & 0xF
    //   channels    = ((adts[2] & 0x1) << 2) | ((adts[3] >> 6) & 0x3)
    objectType  := ((adts[2] >> 6) & 0x3) + 1
    samplingIdx := (adts[2] >> 2) & 0xF
    channels    := ((adts[2] & 0x1) << 2) | ((adts[3] >> 6) & 0x3)
    asc = []byte{
        (objectType << 3) | (samplingIdx >> 1),
        (samplingIdx << 7) | (channels << 3),
    }
    return adts[headerLen:], asc, nil
}
```

## Flags reference

```go
const (
    FlagNTPSynced     = 0x01
    FlagDiscontinuity = 0x02
    FlagKeyframe      = 0x04
    FlagHasSEI        = 0x10
)
```

| Flag | When set | What to do |
|------|----------|-----------|
| `FlagNTPSynced` | RTCP SR packet received; `NTPMS` is anchored to camera wall clock | Trust `NTPMS` for A/V sync and `prft` box |
| `FlagDiscontinuity` | Reconnect, packet-loss burst, or clock jump > 1 s | Close current fMP4 fragment; start fresh; signal tfhd discontinuity bit |
| `FlagKeyframe` | Frame is a random-access point | Begin new fMP4 fragment; safe HLS segment boundary |
| `FlagHasSEI` | SEI NAL units are present in this access unit | Inspect for SEI type if needed; otherwise pass through |

## Decision tree for frame dispatch

```go
func dispatch(f *Frame, muxer Muxer) {
    if f.IsDiscontinuity() {
        muxer.OnDiscontinuity()
    }
    switch f.MediaType {
    case MediaVideo:
        switch f.FrameType {
        case FrameTypeParamSet:
            muxer.OnParamSet(f)   // update/init codec config
        case FrameTypeKey:
            muxer.OnKeyFrame(f)   // flush fragment, start new one
        case FrameTypeDelta:
            muxer.OnDeltaFrame(f) // append to current fragment
        }
    case MediaAudio:
        muxer.OnAudioFrame(f)
    }
}
```
