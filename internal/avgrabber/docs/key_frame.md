# Key Frame Definition

Updated 2026-03-24.

## 1. Purpose

A key frame is a standalone random-access video frame returned by
`avgrabber_next_frame()`.

In this library, key frames are delivered as:

- `header->media_type == AVGRABBER_MEDIA_VIDEO`
- `header->frame_type == AVGRABBER_FRAME_KEY`
- `header->flags & AVGRABBER_FLAG_KEYFRAME` is non-zero
- payload is an Annex-B access unit

## 2. API Contract

When a client reads a key frame:

- `header->frame_type == AVGRABBER_FRAME_KEY`
- `header->codec_type == AVGRABBER_CODEC_H264` or `AVGRABBER_CODEC_H265`
- `header->frame_size` is the exact number of payload bytes
- `avgrabber_frame_data()` returns a complete Annex-B access unit

`AVGRABBER_FRAME_KEY` has value `1`.

## 3. Byte Format

The payload is an Annex-B byte stream.

Each NAL unit is prefixed with:

```text
00 00 00 01
```

The frame contains the keyframe access unit only. The library does not merge
codec parameter sets into the keyframe payload. Those are emitted separately as
a preceding `AVGRABBER_FRAME_PARAM_SET` frame.

Typical sequence:

```text
AVGRABBER_FRAME_PARAM_SET
AVGRABBER_FRAME_KEY
AVGRABBER_FRAME_DELTA
AVGRABBER_FRAME_DELTA
```

See [connect_header.md](./connect_header.md) for the parameter-set frame contract.

## 4. Codec-Specific Meaning

### H.264

`AVGRABBER_FRAME_KEY` means an access unit whose random-access picture is
carried by one or more IDR slices.

Key H.264 NAL type:

- IDR slice = `5`

Typical payload:

```text
00 00 00 01 [IDR slice]
00 00 00 01 [IDR slice]    ; optional additional slices
```

### H.265

`AVGRABBER_FRAME_KEY` means an access unit whose random-access picture is
carried by one of these NAL types:

- BLA_W_LP = `16`
- BLA_W_RADL = `17`
- BLA_N_LP = `18`
- IDR_W_RADL = `19`
- IDR_N_LP = `20`
- CRA_NUT = `21`

Typical payload:

```text
00 00 00 01 [IDR/BLA/CRA slice]
00 00 00 01 [additional slice]   ; optional
```

## 5. Producer Rules

The library guarantees:

1. A key frame is emitted as a standalone `AVGRABBER_FRAME_KEY`.
2. Any required SPS/PPS/VPS are emitted first as a standalone
   `AVGRABBER_FRAME_PARAM_SET`.
3. If a key frame spans multiple slices, all slices in that access unit are
   delivered in the same frame payload.
4. The payload preserves Annex-B start codes exactly as emitted by the library.

## 6. Consumer Requirements

A consumer in any language should:

1. Detect `header->frame_type == AVGRABBER_FRAME_KEY`.
2. Confirm with `header->flags & AVGRABBER_FLAG_KEYFRAME` as an additional
   signal, not as a substitute for checking `frame_type`.
3. If the decoder requires parameter sets, apply the most recent preceding
   `AVGRABBER_FRAME_PARAM_SET` before decoding this frame.
4. Treat the payload as Annex-B video data, not as AVCC/HVCC unless explicitly
   converted by the application.

Bindings must not:

- strip start codes silently
- merge a preceding `AVGRABBER_FRAME_PARAM_SET` into this payload implicitly
- drop `AVGRABBER_FLAG_KEYFRAME` from the frame metadata

## 7. Practical Interpretation

For most consumers, "key frame" in this library means:

- random-access frame
- safe decode start point after applying the latest `AVGRABBER_FRAME_PARAM_SET`
- represented as `AVGRABBER_FRAME_KEY` plus `AVGRABBER_FLAG_KEYFRAME`

It does not mean:

- a self-contained frame that already includes SPS/PPS/VPS
- an MP4/AVCC/HVCC sample
- a transport-layer marker

## 8. Examples

### Example: H.264

```text
frame_type = AVGRABBER_FRAME_KEY
codec_type = AVGRABBER_CODEC_H264
flags      = AVGRABBER_FLAG_KEYFRAME
payload    = 00 00 00 01 65 ... 00 00 00 01 65 ...
```

### Example: H.265

```text
frame_type = AVGRABBER_FRAME_KEY
codec_type = AVGRABBER_CODEC_H265
flags      = AVGRABBER_FLAG_KEYFRAME
payload    = 00 00 00 01 26 ...
```

## 9. Cross-Language Contract

This definition is language-neutral.

Any binding must preserve:

- `frame_type`
- `codec_type`
- `frame_size`
- `flags`
- exact payload bytes

If a target decoder expects another representation, conversion is the
responsibility of the binding or application layer.

## 10. Compatibility Note

The name `I_FRAME` used in older documentation and the legacy API corresponds
to `AVGRABBER_FRAME_KEY` in the current API.
