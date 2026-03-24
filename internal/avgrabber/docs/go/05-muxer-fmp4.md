# fMP4 / LL-HLS Muxer Guide

Fragmented MP4 (fMP4 / ISOBMFF) is the container format used by MPEG-DASH,
HLS (fMP4 variant), and LL-HLS (CMAF). This document describes exactly how to
map AVGrabber frames to fMP4 box structures.

## Recommended Go libraries

| Library | Notes |
|---------|-------|
| [github.com/Eyevinn/mp4ff](https://github.com/Eyevinn/mp4ff) | Good fMP4 write support; handles `avcC`, `hvcC`, `esds` |
| [github.com/shiguredo/mp4](https://github.com/shiguredo/mp4) | Lightweight, actively maintained |

Both libraries understand ISOBMFF box structure. The mapping below applies
regardless of which library you choose.

---

## Box structure overview

```
ftyp   — file type (once at start of file)
moov   — init segment (written once, or updated when PARAM_SET changes)
  mvhd
  trak (video)
    tkhd
    mdia
      mdhd  — timescale = VideoClockRate (typically 90000)
      hdlr  — 'vide'
      minf → stbl → stsd → avc1/hvc1 → avcC/hvcC
  trak (audio, if enabled)
    tkhd
    mdia
      mdhd  — timescale = AudioSampleRate
      hdlr  — 'soun'
      minf → stbl → stsd → mp4a → esds  (AAC)
                              → samr/ulaw/alaw (G.711)
  mvex → trex (one per track)

[repeated for each GOP / segment]
moof
  mfhd  — sequence number
  traf (video)
    tfhd — track ID, base data offset
    tfdt — baseMediaDecodeTime = DTSTicks of first sample
    trun — sample entries (size, duration, composition offset, flags)
  traf (audio)
    tfhd
    tfdt
    trun
mdat  — video samples (AVCC), then audio samples
```

---

## Step 1 — Init segment (triggered by PARAM_SET)

Write or rewrite `moov` when you receive a `PARAM_SET` frame.

### H.264 — build `avcC`

Parse SPS and PPS NAL units from the `PARAM_SET` Annex-B payload:

```go
// SplitAnnexB splits Annex-B bytes into individual NAL units (without start codes).
func SplitAnnexB(data []byte) [][]byte {
    var nals [][]byte
    start := 0
    for i := 0; i < len(data)-3; {
        if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
            if i > start {
                nals = append(nals, data[start:i])
            }
            start = i + 4
            i += 4
        } else {
            i++
        }
    }
    if start < len(data) {
        nals = append(nals, data[start:])
    }
    return nals
}

// H264ParamSets splits a PARAM_SET payload into (sps, pps) lists.
func H264ParamSets(data []byte) (sps, pps [][]byte) {
    for _, nal := range SplitAnnexB(data) {
        if len(nal) == 0 {
            continue
        }
        nalType := nal[0] & 0x1F
        switch nalType {
        case 7:
            sps = append(sps, nal)
        case 8:
            pps = append(pps, nal)
        }
    }
    return
}
```

Feed the resulting `sps` and `pps` slices into your library's `AVCDecoderConfigurationRecord`
(avcC box) builder. Width and height come from `StreamInfo.Width` / `StreamInfo.Height`.

### H.265 — build `hvcC`

```go
// H265ParamSets splits a PARAM_SET payload into (vps, sps, pps) lists.
func H265ParamSets(data []byte) (vps, sps, pps [][]byte) {
    for _, nal := range SplitAnnexB(data) {
        if len(nal) == 0 {
            continue
        }
        nalType := (nal[0] & 0x7E) >> 1
        switch nalType {
        case 32:
            vps = append(vps, nal)
        case 33:
            sps = append(sps, nal)
        case 34:
            pps = append(pps, nal)
        }
    }
    return
}
```

### AAC — build `esds`

Extract `AudioSpecificConfig` from the first ADTS-framed audio frame (see
`StripADTS` in [03-frame-guide.md](03-frame-guide.md)):

```go
rawAAC, asc, _ := StripADTS(firstAudioFrame.Data)
// asc is the 2-byte AudioSpecificConfig for the esds box.
_ = rawAAC
```

### Track timescales

```
mdhd.timescale for video  = StreamInfo.VideoClockRate   (typically 90000)
mdhd.timescale for audio  = StreamInfo.AudioSampleRate  (e.g. 8000, 48000)
```

---

## Step 2 — Media segments (moof + mdat)

### Fragment boundary strategy

Open a new fragment at every `KEY` frame (which is always preceded by a
`PARAM_SET` in the stream). This gives one GOP per fragment.

```
receive PARAM_SET → update/cache codec config (may rewrite moov)
receive KEY       → flush previous fragment; open new moof
receive DELTA     → append to current fragment's trun
receive AUDIO     → append to audio trun in current fragment
```

### trun sample entry fields

For each frame appended to a fragment:

| trun field | Source |
|-----------|--------|
| `sample_size` | `Frame.FrameSize` |
| `sample_duration` | Video: `PTSTicks[n+1] − PTSTicks[n]` (compute on next frame); Audio: `Frame.DurationTicks` |
| `sample_composition_time_offset` | `Frame.PTSTicks − Frame.DTSTicks` (zero for most cameras) |
| `sample_flags` | Set sync-sample bit when `FlagKeyframe` is set |

Because video `sample_duration` requires the *next* frame's PTS, you buffer
one frame before writing to trun, or write the trun at fragment close using
the last known duration.

### tfdt

```
tfdt.baseMediaDecodeTime = DTSTicks of the first sample in this fragment
```

### mdat payload

**Video samples:** convert Annex-B → AVCC/HVCC using `AnnexBToAVCC` (see
[03-frame-guide.md](03-frame-guide.md)) before writing to `mdat`.

**Audio samples:**
- AAC: strip the 7-byte ADTS header (`StripADTS`), write raw AAC.
- G.711 / G.722 / G.726 / Opus: write payload bytes directly.

### Discontinuity

When `FlagDiscontinuity` is set on a frame:

1. Flush and close the current fragment.
2. Set `tfhd` flag `0x00010000` (discontinuity) on the next `traf`.
3. Reset PTS delta tracking.
4. Log a warning with `Frame.WallClockMS` for diagnostics.

### NTP — `prft` box

Write one `prft` box per `moof` when `FlagNTPSynced` is true:

```
prft {
  reference_track_id = <video track ID>
  ntp_timestamp      = Frame.NTPMS converted to 64-bit NTP seconds.fraction
  media_time         = PTSTicks of first sample in this moof
}
```

Convert `NTPMS` (Unix epoch ms) to NTP 64-bit format:
```go
// NTP epoch starts 1900-01-01; Unix epoch starts 1970-01-01.
const ntpUnixDiff = 70*365*24*3600 + 17*24*3600 // seconds (17 leap years)

func MSToNTP64(ms int64) uint64 {
    seconds := uint64(ms/1000) + ntpUnixDiff
    fraction := uint64(ms%1000) * (1 << 32) / 1000
    return (seconds << 32) | fraction
}
```

---

## Step 3 — LL-HLS (CMAF) additions

LL-HLS uses the same fMP4 box structure but with:

1. **Shorter fragments** — target 500 ms or less per CMAF chunk.
2. **Independent segments** — each chunk must be independently decodable
   (start at a keyframe, or use `styp` + `prft` + `moof` + `mdat`).
3. **Partial segments** — you may emit a `moof`+`mdat` with a single sample
   immediately as it arrives, then close the chunk. This is the low-latency
   path.

```
Chunk boundary options:
  - Fixed-duration: flush every N frames or every T ms of wall clock
  - Keyframe-aligned: flush at every KEY frame (simplest; latency = GOP duration)
  - Sub-GOP: flush every P frames within a GOP (lower latency; requires
    `EXT-X-PART` tags referencing partial chunks)
```

HLS manifest additions for LL-HLS:

```
#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-TARGET=0.5
#EXT-X-PART-INF:PART-TARGET=0.5

#EXT-X-PART:DURATION=0.5,URI="seg001_part1.mp4",INDEPENDENT=YES
#EXT-X-PART:DURATION=0.5,URI="seg001_part2.mp4"
#EXTINF:2.0,
seg001.mp4
```

The `INDEPENDENT=YES` attribute marks a partial segment that starts at a
keyframe — set this whenever `FlagKeyframe` is set on the first frame in
the chunk.

---

## Complete fragment write sequence (pseudocode)

```go
type FragmentBuilder struct {
    videoSamples []Sample
    audioSamples []Sample
    seqNum       uint32
    firstPTS     int64
    firstDTS     int64
}

func (b *FragmentBuilder) Flush(w io.Writer) {
    // 1. Finalise sample durations for video (shift from next PTS).
    // 2. Build moof: mfhd(seqNum) + traf(video) + traf(audio).
    //    - tfhd: flags, track ID, base data offset
    //    - tfdt: baseMediaDecodeTime = b.firstDTS
    //    - trun: one entry per sample
    // 3. Build mdat: all AVCC video bytes, then raw audio bytes.
    // 4. Write moof, then mdat (moof references mdat by offset).
    b.videoSamples = nil
    b.audioSamples = nil
    b.seqNum++
}

func (b *FragmentBuilder) AddVideo(f *Frame) {
    b.videoSamples = append(b.videoSamples, Sample{
        Data:       AnnexBToAVCC(f.Data),
        PTSTicks:   f.PTSTicks,
        DTSTicks:   f.DTSTicks,
        Flags:      f.Flags,
        FrameSize:  f.FrameSize,
    })
    if len(b.videoSamples) == 1 {
        b.firstDTS = f.DTSTicks
    }
}

func (b *FragmentBuilder) AddAudio(f *Frame, isAAC bool) {
    data := f.Data
    if isAAC {
        raw, _, _ := StripADTS(f.Data)
        data = raw
    }
    b.audioSamples = append(b.audioSamples, Sample{
        Data:          data,
        DurationTicks: f.DurationTicks,
        FrameSize:     int32(len(data)),
    })
}
```
