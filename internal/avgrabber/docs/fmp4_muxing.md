# Storing AVGrabber Frames in fMP4

AVGrabber frames can be stored in a fragmented MP4 (fMP4 / ISOBMFF) container.
The payload format and timing metadata require a few well-defined transformations.

## Payload format conversions

### Video (H.264 / H.265)

| AVGrabber output | fMP4 requirement | Action |
|------------------|-----------------|--------|
| Annex-B start codes (`00 00 00 01`) | AVCC/HVCC length-prefixed NAL units | Replace each start code with a 4-byte big-endian NAL unit length |
| `PARAM_SET` frame (SPS+PPS / VPS+SPS+PPS) | `avcC` / `hvcC` box inside `moov` | Parse parameter sets from the payload and write the init segment |
| `KEY` + `DELTA` frames | `moof` + `mdat` per fragment | Accumulate one GOP per fragment (PARAM_SET → next KEY) |

### Audio (AAC)

| AVGrabber output | fMP4 requirement | Action |
|------------------|-----------------|--------|
| ADTS-framed AAC (7-byte header per packet) | Raw AAC in `mdat`; codec config in `esds` box | Strip the 7-byte ADTS header; extract AudioSpecificConfig from the first ADTS header |
| G.711 / G.722 / G.726 / Opus | Supported via `mp4a` / `Opus` box | Supported in fMP4; AAC is most broadly compatible |

## Timing metadata mapping

All three timing fields in `AVGrabberFrameHeader` have standard or sanctioned
fMP4 representations — no information needs to be discarded.

| `AVGrabberFrameHeader` field | fMP4 mechanism | Notes |
|------------------------------|----------------|-------|
| `pts_ticks` | `trun` sample PTS; `mdhd` timescale = `video_clock_rate` (90000 Hz) for video, `audio_sample_rate` for audio | 64-bit unwrapped — never wraps; maps directly to the native media timeline |
| `dts_ticks` | `trun` sample DTS | Equal to `pts_ticks` for I/P-only cameras; enables correct `ctts` construction if B-frames are ever used |
| `duration_ticks` | `trun` sample duration | Audio: set to `audio_samples_per_frame` (e.g. 1024 for AAC); video: compute from consecutive `pts_ticks` deltas |
| `ntp_ms` (when `FLAG_NTP_SYNCED`) | **`prft` box** (ISO 14496-12 §8.16.5) | Designed for this: maps fragment media time to a 64-bit NTP timestamp |
| `wall_clock_ms` | `prft` box (alternate reference) or custom `uuid` box | Use as the reference clock when NTP is not synced |
| `flags` (DISCONTINUITY, NTP_SYNCED, …) | `tfhd` discontinuity bit + custom `uuid` box per `moof` | `FLAG_DISCONTINUITY` maps to `tfhd` bit `0x00010000`; remaining flags go in the `uuid` box |

### `prft` box (Producer Reference Time)

One `prft` box per fragment is sufficient. Players and recorders use it to
anchor the entire fragment to wall time. DASH live streams already rely on this.

```
prft {
  reference_track_id  = <video track id>
  ntp_timestamp       = <64-bit NTP derived from ntp_ms>   // when FLAG_NTP_SYNCED
  media_time          = <pts_ticks of the first sample in this fragment>
}
```

### Discontinuity signal

`FLAG_DISCONTINUITY` maps to the standard `discontinuity` bit in `tfhd`
(bit `0x00010000`). This is the correct signal to decoders that timestamps are
not continuous across the fragment boundary (reconnect, packet-loss burst, etc.).

### Per-frame precision via a timed metadata track

If per-frame timing precision is required, add a second track in the same
`moov` with `hdlr` type `meta`. Each sample is time-aligned to the
corresponding video frame via shared `trun` composition offsets and carries a
small payload (binary struct or JSON) containing all four fields:

```json
{ "wall_clock_ms": 1700000000123, "ntp_ms": 1700000000100,
  "pts_ticks": 123456789, "dts_ticks": 123456789,
  "duration_ticks": 3000, "flags": 5 }
```

## Fragment boundary strategy

```
PARAM_SET  →  write / update init segment (moov) if SPS/PPS changed
KEY        →  close previous moof fragment, open new moof
DELTA ...  →  append samples to current moof
KEY        →  close fragment, open next moof
```

## Round-trip fidelity

Using the mechanisms above, all `AVGrabberFrameHeader` timing fields can be
fully reconstructed from the fMP4 on playback:

| Field | Reconstructed from |
|-------|--------------------|
| `pts_ticks` | `trun` composition offset + `mdhd` timescale |
| `ntp_ms` | `prft.ntp_timestamp` |
| `wall_clock_ms` | `prft` (wall-clock variant) or metadata track |
| `flags` | `tfhd` discontinuity bit + `uuid` box |

## Recommended libraries

| Library | Language | Notes |
|---------|----------|-------|
| mp4ff | Go | Good fMP4 write support, handles `avcC`/`hvcC` |
| shiguredo/mp4 | Go | Lightweight, actively maintained |
| Bento4 | C++ | Solid fMP4 muxer |
| gpac/isom | C | Full ISOBMFF, used in GPAC/MP4Box |
