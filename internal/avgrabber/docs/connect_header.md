# PARAM_SET Frame Definition

Updated 2026-03-24.

## 1. Purpose

`AVGRABBER_FRAME_PARAM_SET` is a video frame type emitted by the library before
a decoder configuration change point.

It carries only codec parameter sets:

- H.264: SPS + PPS
- H.265: VPS + SPS + PPS

The payload is returned through the normal frame API. It is not exposed as a
separate RTSP/HTTP/application header field.

## 2. Where It Appears in the API

When a client reads a PARAM_SET frame via `avgrabber_next_frame()`:

- `header->media_type == AVGRABBER_MEDIA_VIDEO`
- `header->frame_type == AVGRABBER_FRAME_PARAM_SET`
- `header->codec_type == AVGRABBER_CODEC_H264` or `AVGRABBER_CODEC_H265`
- `avgrabber_frame_data()` returns the Annex-B parameter-set payload

`AVGRABBER_FRAME_PARAM_SET` has value `0`.

## 3. Byte Format

The payload is a plain Annex-B byte stream.

Each parameter-set NAL unit is prefixed with the four-byte start code:

```text
00 00 00 01
```

No extra library-specific wrapper, length prefix, JSON, or TLV is added.

### H.264 layout

```text
00 00 00 01 [SPS NAL bytes]
00 00 00 01 [PPS NAL bytes]
```

### H.265 layout

```text
00 00 00 01 [VPS NAL bytes]
00 00 00 01 [SPS NAL bytes]
00 00 00 01 [PPS NAL bytes]
```

If multiple parameter-set NAL units of the same class are present, they are
serialized in the order delivered by the source SDP or RTP stream.

## 4. Ordering Rules

Consumers must treat `AVGRABBER_FRAME_PARAM_SET` as applying to the video
frame(s) that follow it.

The library guarantees these rules:

1. `AVGRABBER_FRAME_PARAM_SET` is emitted as a standalone frame, never merged
   into `AVGRABBER_FRAME_KEY`.
2. A `AVGRABBER_FRAME_PARAM_SET` precedes the first keyframe after stream start.
3. A `AVGRABBER_FRAME_PARAM_SET` is emitted again after reconnect.
4. If the camera sends new in-band parameter sets mid-stream, they are emitted
   as a new standalone `AVGRABBER_FRAME_PARAM_SET` before the affected keyframe.

Typical sequence:

```text
AVGRABBER_FRAME_PARAM_SET
AVGRABBER_FRAME_KEY
AVGRABBER_FRAME_DELTA
AVGRABBER_FRAME_DELTA
...
AVGRABBER_FRAME_PARAM_SET
AVGRABBER_FRAME_KEY
```

## 5. Producer Sources

The library may build `AVGRABBER_FRAME_PARAM_SET` from either source:

- Out-of-band SDP `sprop-*` parameters
- In-band video NAL units received in RTP

For decoder consumers, both forms are normalized to the same output format:
Annex-B parameter-set NAL units in one standalone PARAM_SET frame.

## 6. Consumer Requirements

A consumer in any language should:

1. Detect `header->frame_type == AVGRABBER_FRAME_PARAM_SET`.
2. Store or immediately forward the payload bytes as decoder configuration.
3. Apply that configuration to the next keyframe / random-access frame.
4. Replace any previously cached configuration when a newer PARAM_SET arrives.

Do not treat `AVGRABBER_FRAME_PARAM_SET` as displayable video.

## 7. Minimal Parsing Guidance

Consumers that need to inspect the payload can parse it as standard Annex-B:

1. Split on `00 00 00 01`.
2. Ignore empty segments before the first start code.
3. For H.264, NAL type is `nal[0] & 0x1F`.
4. For H.265, NAL type is `(nal[0] & 0x7E) >> 1`.

Expected NAL types:

- H.264: SPS = 7, PPS = 8
- H.265: VPS = 32, SPS = 33, PPS = 34

## 8. Cross-Language Contract

This definition is intentionally language-neutral.

Any binding must preserve:

- `frame_type`
- `codec_type`
- `frame_size`
- exact payload bytes

Bindings must not:

- strip Annex-B start codes
- convert to AVCC/HVCC length-prefixed format implicitly
- merge `AVGRABBER_FRAME_PARAM_SET` into the following frame silently

If a target decoder requires AVCC/HVCC or another container-specific format,
the binding/application must perform that conversion explicitly.

## 9. Examples

### Example: H.264

```text
frame_type = AVGRABBER_FRAME_PARAM_SET
codec_type = AVGRABBER_CODEC_H264
payload    = 00 00 00 01 67 ... 00 00 00 01 68 ...
```

The next random-access picture is delivered separately:

```text
frame_type = AVGRABBER_FRAME_KEY
codec_type = AVGRABBER_CODEC_H264
payload    = 00 00 00 01 65 ...
```

### Example: H.265

```text
frame_type = AVGRABBER_FRAME_PARAM_SET
codec_type = AVGRABBER_CODEC_H265
payload    = 00 00 00 01 40 ... 00 00 00 01 42 ... 00 00 00 01 44 ...
```

The next random-access picture is delivered separately:

```text
frame_type = AVGRABBER_FRAME_KEY
codec_type = AVGRABBER_CODEC_H265
payload    = 00 00 00 01 26 ...
```

## 10. Compatibility Note

The term `CONNECT_HEADER` used in older documentation and the legacy API
corresponds to `AVGRABBER_FRAME_PARAM_SET` in the current API.

The stable contract is unchanged:

- H.264: all decoder parameter sets (SPS+PPS) in Annex-B, as a standalone frame
- H.265: all decoder parameter sets (VPS+SPS+PPS) in Annex-B, as a standalone frame
