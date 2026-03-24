# Access Unit Assembly from live555 Callbacks (H.264 / H.265)

Research and verification performed 2026-03-22. Updated API names 2026-03-24.

## 1. How live555 Delivers NAL Units

**One complete NAL unit per `afterGettingFrame()` callback.** This is the fundamental delivery contract.

- **STAP-A / AP (Aggregation Packets):** live555 deaggregates them internally. Each enclosed NAL unit triggers a separate callback, all sharing the same `fPresentationTime`.
- **FU-A / FU (Fragmentation Units):** live555 reassembles fragments internally. `afterGettingFrame()` is only called once the full NAL unit is reconstructed.
- **Packet loss in fragmented NAL:** If any RTP fragment is lost, live555 discards the entire NAL unit (`fPacketLossInFragmentedFrame`).

### H.264 vs H.265 Differences

| Aspect | H.264 (RFC 6184) | H.265 (RFC 7798) |
|--------|-------------------|-------------------|
| NAL header size | 1 byte | 2 bytes |
| NAL type extraction | `header[0] & 0x1F` | `(header[0] & 0x7E) >> 1` |
| Aggregation packet | STAP-A (type 24) | AP (type 48) |
| Fragmentation unit | FU-A (type 28) | FU (type 49) |
| Parameter sets | SPS (7), PPS (8) | VPS (32), SPS (33), PPS (34) |

## 2. Access Unit Boundary Detection

Three complementary signals are used in combination for maximum camera compatibility:

### Signal 1: PTS Change (Primary)

**Presentation time (PTS) is the primary boundary marker.**

- Per RFC 6184 Section 5.1: "NAL units with the same RTP timestamp are part of the same access unit."
- All NAL units belonging to the same video frame share the same RTP timestamp, translated by live555 into the same `fPresentationTime`.
- The RTP marker bit (M bit) marks the last RTP *packet* of an AU but is consumed internally by live555 and not the primary mechanism for client-side AU detection.

**PTS Drift:** Some IP cameras exhibit PTS drift where timestamps for NAL units within the same AU differ by a small amount (typically < 1ms) due to clock imprecision. A 1ms tolerance window is applied when comparing timestamps.

### Signal 2: Access Unit Delimiter (AUD) NAL Unit

Per H.264 Section 7.4.1.2.3 and the H.265 spec, the AUD (H.264 type 9, H.265 type 35) **must be the first NAL unit** of an access unit when present. Receiving an AUD definitively signals the start of a new AU.

- Most IP cameras do **not** send AUD in RTP streams (it's considered unnecessary overhead).
- Some cameras and software encoders do send AUD — when detected, it triggers an immediate flush.
- The AUD itself is **discarded** from the output buffer (it carries no video payload, and some downstream decoders reject it in non-TS contexts).

### Signal 3: Non-VCL NAL After VCL (Spec-Based)

Per H.264 Section 7.4.1.2.3, a new AU is detected when certain non-VCL NAL types appear **after** VCL NAL units of the previous picture:

| H.264 boundary types | H.265 boundary types |
|----------------------|---------------------|
| SEI (6) | VPS (32) |
| SPS (7) | SPS (33) |
| PPS (8) | PPS (34) |
| AUD (9) | AUD (35) |
| Types 14-18 | SEI Prefix (39) |
| | Types 41-44, 48-55 |

This handles the rare case where a camera sends non-VCL NALs for the next AU with the same PTS as the previous frame's VCL NALs. A `have_vcl_in_au_` flag prevents false boundary detection at stream startup.

### Combined Algorithm

```
State: au_buffer_, pending_pts_, pending_pts_set_, have_vcl_in_au_, current_au_frame_type_

For each NAL unit received with presentation_time:
  1. Extract nal_type from NAL header

  2. Boundary detection (three signals, checked in order):
     a. PTS change (1ms tolerance) → flush AU, update pending_pts_
     b. AUD NAL → flush AU, update pending_pts_, discard AUD, return
     c. Non-VCL NAL after VCL in current AU → flush AU

  3. Initialize pending_pts_ if first NAL

  4. Classify NAL type → update current_au_frame_type_, set is_idr/is_vcl flags

  5. PARAM_SET / KEY separation:
     - If IDR + in-band params accumulated → flush params as AVGRABBER_FRAME_PARAM_SET
     - Else if IDR + no in-band params + first IDR slice → publish out-of-band
       params (from SDP) as standalone AVGRABBER_FRAME_PARAM_SET
     - Set frame type to AVGRABBER_FRAME_KEY

  6. Track VCL state (have_vcl_in_au_)

  7. Append NAL unit (with Annex B start code) to au_buffer_
```

### Important: `pending_pts_` Must Be Updated After PTS Flush

When Signal 1 (PTS change) triggers a flush, `pending_pts_` **must** be updated to the new `presentation_time`. Otherwise the stale value causes every subsequent NAL with the new PTS to trigger another spurious flush, splitting each NAL into its own "AU".

## 3. SPS/PPS/VPS Handling

### Delivery Varies by Camera Vendor

| Vendor | Typical Behavior |
|--------|-----------------|
| **Hikvision** | Sends SPS/PPS in-band with every IDR. Some models send incorrect `sprop-parameter-sets` in SDP. |
| **Dahua** | Generally sends SPS/PPS in-band with IDR frames. |
| **Axis** | Often sends SPS/PPS **only** in SDP, never repeating in-band. |
| **Bosch** | Generally well-behaved, ONVIF-compliant since firmware 4.10. |
| **Generic ONVIF** | Varies: some in-band, some SDP-only, some both. |

### Best Practice: Separate PARAM_SET and KEY Frames

Parameter sets (SPS/PPS/VPS) are **always** published as a standalone
`AVGRABBER_FRAME_PARAM_SET` frame, never merged with the IDR data. This gives
consumers a clean separation between decoder configuration and video data.

**On every IDR:**
1. If in-band SPS/PPS/VPS have accumulated in the AU buffer, flush them as an `AVGRABBER_FRAME_PARAM_SET` frame first.
2. If no in-band params are present (e.g. Axis cameras), publish out-of-band params from SDP as an `AVGRABBER_FRAME_PARAM_SET` frame.
3. The IDR slice(s) then form their own `AVGRABBER_FRAME_KEY` AU.

**Guards against duplicate PARAM_SET:**
- If in-band params were already flushed, skip the out-of-band publish (avoids double PARAM_SET).
- For multi-slice IDR frames (`SPS PPS IDR IDR`), only publish params before the first IDR slice (use `have_vcl_in_au_` as guard).

### Consumer Output Sequence

For a typical keyframe, the consumer receives two separate frames:
```
Frame 1 — AVGRABBER_FRAME_PARAM_SET:
  [start_code + VPS]   (H.265 only)
  [start_code + SPS]
  [start_code + PPS]

Frame 2 — AVGRABBER_FRAME_KEY:
  [start_code + IDR slice]
  [start_code + IDR slice]   (if multi-slice)
```

## 4. Annex B Byte Stream Format

- Prepend **4-byte start code** `0x00 0x00 0x00 0x01` before each NAL unit.
- live555 delivers raw NAL bytes (already containing emulation prevention bytes from the encoder).

## 5. Frame Type Classification

### H.264

| NAL Type | Value | Frame Classification |
|----------|-------|---------------------|
| SPS | 7 | `AVGRABBER_FRAME_PARAM_SET` |
| PPS | 8 | `AVGRABBER_FRAME_PARAM_SET` |
| IDR Slice | 5 | `AVGRABBER_FRAME_KEY` |
| Non-IDR Slice | 1 | `AVGRABBER_FRAME_DELTA` |
| SEI | 6 | `AVGRABBER_FRAME_DELTA` + `AVGRABBER_FLAG_HAS_SEI` |

### H.265

| NAL Type | Value | Frame Classification |
|----------|-------|---------------------|
| VPS | 32 | `AVGRABBER_FRAME_PARAM_SET` |
| SPS | 33 | `AVGRABBER_FRAME_PARAM_SET` |
| PPS | 34 | `AVGRABBER_FRAME_PARAM_SET` |
| IDR_W_RADL | 19 | `AVGRABBER_FRAME_KEY` |
| IDR_N_LP | 20 | `AVGRABBER_FRAME_KEY` |
| CRA_NUT | 21 | `AVGRABBER_FRAME_KEY` |
| BLA_W_LP | 16 | `AVGRABBER_FRAME_KEY` |
| BLA_W_RADL | 17 | `AVGRABBER_FRAME_KEY` |
| BLA_N_LP | 18 | `AVGRABBER_FRAME_KEY` |
| TRAIL_R / TRAIL_N | 1 / 0 | `AVGRABBER_FRAME_DELTA` |
| SEI_PREFIX | 39 | `AVGRABBER_FRAME_DELTA` + `AVGRABBER_FLAG_HAS_SEI` |

Frame type hierarchy (never downgraded within an AU):
`AVGRABBER_FRAME_PARAM_SET` > `AVGRABBER_FRAME_KEY` > `AVGRABBER_FRAME_DELTA`.

The current implementation does not separately classify B-slices, so ordinary
non-key video AUs are emitted as `AVGRABBER_FRAME_DELTA`. The public API
reserves `AVGRABBER_FRAME_UNKNOWN` (255) as a future-compatible fallback value.

## 6. Common Pitfalls

1. **Incorrect SDP profile IDs** — Many cameras publish wrong H.264 profile IDs. Don't rely on SDP profile for decoding.
2. **Missing `sprop-parameter-sets`** — Some cameras omit this from SDP entirely. Must handle receiving SPS/PPS only in-band.
3. **Multiple IDR slices** — Some HD cameras send `SPS PPS IDR IDR`. Only publish parameter sets before the first IDR slice; let subsequent slices accumulate into the same `AVGRABBER_FRAME_KEY` AU.
4. **STAP-A with SPS+PPS+IDR** — Some cameras pack these into a single RTP packet. live555 deaggregates into separate callbacks with the same PTS.
5. **SPS/PPS change mid-stream** — Some cameras change resolution dynamically, sending new SPS/PPS. Pass these through as a new `AVGRABBER_FRAME_PARAM_SET`.
6. **PTS drift** — Some cameras have slightly varying timestamps for NAL units in the same AU. Use tolerance-based comparison (1ms).
7. **Stale `pending_pts_` after flush** — When a PTS change triggers a flush, the pending timestamp must be updated to the new value. Failing to do so causes every subsequent NAL to trigger another spurious flush.
8. **Double PARAM_SET** — When in-band SPS/PPS are flushed as `AVGRABBER_FRAME_PARAM_SET`, `current_au_frame_type_` resets. Without a guard, the out-of-band publish path also fires, producing a duplicate. Track whether in-band params were already flushed.

## References

- [RFC 6184 — RTP Payload Format for H.264 Video](https://datatracker.ietf.org/doc/html/rfc6184)
- [RFC 7798 — RTP Payload Format for HEVC](https://datatracker.ietf.org/doc/html/rfc7798)
- [live555 FAQ](http://www.live555.com/liveMedia/faq.html)
- [ITU-T H.264 — Annex B Byte Stream Format](https://www.itu.int/rec/T-REC-H.264)
- [ITU-T H.265 — Annex B Byte Stream Format](https://www.itu.int/rec/T-REC-H.265)
