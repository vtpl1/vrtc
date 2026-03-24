# Frame Types

Updated 2026-03-24.

This document defines the public `frame_type` values in `AVGrabberFrameHeader`
returned by `avgrabber_next_frame()` and `AVGrabberFrameCallback`.

See [frame.md](./frame.md) for the overall frame contract.

For `AVGRABBER_FRAME_PARAM_SET` and `AVGRABBER_FRAME_KEY`, see the dedicated
docs:

- [connect_header.md](./connect_header.md)
- [key_frame.md](./key_frame.md)

## 1. `AVGRABBER_FRAME_DELTA` (value 2)

`AVGRABBER_FRAME_DELTA` means a non-random-access video frame.

At the API boundary:

- `header->media_type == AVGRABBER_MEDIA_VIDEO`
- `header->frame_type == AVGRABBER_FRAME_DELTA`
- `header->codec_type == AVGRABBER_CODEC_H264` or `AVGRABBER_CODEC_H265`
- payload bytes are an Annex-B video access unit

### Byte format

The payload is Annex-B:

```text
00 00 00 01 [NAL]
00 00 00 01 [NAL]
...
```

### Current implementation behavior

`AVGRABBER_FRAME_DELTA` is the default classification for all non-key video
access units. That means:

- non-IDR H.264 access units are emitted as `AVGRABBER_FRAME_DELTA`
- non-random-access H.265 access units are emitted as `AVGRABBER_FRAME_DELTA`
- if an SEI NAL is present, `AVGRABBER_FLAG_HAS_SEI` is set while `frame_type`
  remains `AVGRABBER_FRAME_DELTA`

The library does not separately classify B-slices. Consumers must not assume
that `AVGRABBER_FRAME_DELTA` exclusively maps to P-slices at the codec level.

## 2. `AVGRABBER_FRAME_AUDIO` (value 16)

`AVGRABBER_FRAME_AUDIO` means a normal audio payload.

At the API boundary:

- `header->media_type == AVGRABBER_MEDIA_AUDIO`
- `header->frame_type == AVGRABBER_FRAME_AUDIO`
- `header->codec_type` identifies the audio codec
- payload bytes are raw codec data as returned by the library

### Audio codecs currently exposed

| `codec_type` constant | Format |
|---|---|
| `AVGRABBER_CODEC_AAC` (6) | ADTS-framed AAC (7-byte header prepended by library) |
| `AVGRABBER_CODEC_G711U` (3) | Raw 8-bit µ-law PCM, 8 kHz, mono |
| `AVGRABBER_CODEC_G711A` (4) | Raw 8-bit A-law PCM, 8 kHz, mono |
| `AVGRABBER_CODEC_G722` (9) | Raw G.722 ADPCM, 16 kHz encoded bandwidth |
| `AVGRABBER_CODEC_G726` (10) | Raw G.726 ADPCM, 8 kHz |
| `AVGRABBER_CODEC_OPUS` (11) | Raw Opus packets |
| `AVGRABBER_CODEC_L16` (5) | Raw signed 16-bit PCM |

Consumers must not interpret `AVGRABBER_FRAME_AUDIO` payloads as Annex-B video.

## 3. `AVGRABBER_FRAME_UNKNOWN` (value 255)

`AVGRABBER_FRAME_UNKNOWN` is a fallback type.

Meaning: a frame was accepted by the library but could not be assigned a more
specific `frame_type`.

### Current-status note

The current code path primarily emits:

- `AVGRABBER_FRAME_PARAM_SET`
- `AVGRABBER_FRAME_KEY`
- `AVGRABBER_FRAME_DELTA`
- `AVGRABBER_FRAME_AUDIO`

`AVGRABBER_FRAME_UNKNOWN` is not expected in normal operation, but consumers
should handle it defensively:

1. inspect `media_type`
2. inspect `codec_type`
3. log the frame for diagnostics
4. avoid assuming random-access semantics

## 4. Complete Frame Type Table

| Constant | Value | `media_type` | Description |
|---|---|---|---|
| `AVGRABBER_FRAME_PARAM_SET` | 0 | VIDEO | Decoder parameter sets (SPS+PPS or VPS+SPS+PPS), Annex-B |
| `AVGRABBER_FRAME_KEY` | 1 | VIDEO | IDR / random-access keyframe, Annex-B |
| `AVGRABBER_FRAME_DELTA` | 2 | VIDEO | Non-key (delta) video frame, Annex-B |
| `AVGRABBER_FRAME_AUDIO` | 16 | AUDIO | Raw audio payload (codec determined by `codec_type`) |
| `AVGRABBER_FRAME_UNKNOWN` | 255 | any | Unclassified frame |

## 5. Cross-Language Consumer Rules

Bindings in any language must preserve:

- `frame_type`
- `media_type`
- `codec_type`
- `frame_size`
- `flags`
- exact payload bytes

Bindings must not:

- reclassify `AVGRABBER_FRAME_DELTA` frames based on codec-internal slice types
- convert `AVGRABBER_FRAME_AUDIO` payloads into another container format implicitly
- infer keyframe status from `codec_type` alone

## 6. Practical Summary

For most consumers of the current library:

- `AVGRABBER_FRAME_PARAM_SET`: decoder configuration, apply before the next KEY frame
- `AVGRABBER_FRAME_KEY`: random-access video frame
- `AVGRABBER_FRAME_DELTA`: ordinary non-key video frame
- `AVGRABBER_FRAME_AUDIO`: audio payload
- `AVGRABBER_FRAME_UNKNOWN`: defensive fallback, should not appear in practice
