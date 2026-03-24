# Timestamps and Clock Domains

Every `Frame` carries four time values. Understanding which to use for which
purpose is critical for producing correct fMP4, LL-HLS, and WebRTC output.

## The four time values

| Field | Type | Domain | Description |
|-------|------|--------|-------------|
| `WallClockMS` | `int64` | Server system clock (ms since Unix epoch) | Always present. Anchored to `time.Now()` on the server. Use for real-time delivery and `prft` box fallback. |
| `NTPMS` | `int64` | Camera NTP clock (ms since Unix epoch) | Valid only when `FlagNTPSynced` is set. Derived from RTCP SR packets. Use for camera-side wall-clock and `prft` box. |
| `PTSTicks` | `int64` | Stream clock ticks | **Primary timestamp for muxing.** 64-bit, monotonically increasing, never wraps. |
| `DTSTicks` | `int64` | Stream clock ticks | Decode order timestamp. Equal to `PTSTicks` for the vast majority of IP cameras (no B-frames). |
| `DurationTicks` | `uint32` | Stream clock ticks | Audio: samples per frame (fixed; set from `StreamInfo.AudioSamplesPerFrame`). Video: 0 (derive from consecutive `PTSTicks` deltas). |

## Clock rates

`PTSTicks` and `DTSTicks` use the **RTP clock domain** of their track:

| Track | Clock rate | Source |
|-------|-----------|--------|
| Video | `StreamInfo.VideoClockRate` — typically **90000 Hz** | From SDP `a=rtpmap` |
| Audio | `StreamInfo.AudioSampleRate` — e.g. 8000, 16000, 48000 Hz | From SDP `a=rtpmap` |

G.722 is a special case: its RTP clock is 8000 Hz even though it encodes at
16 kHz audio bandwidth. Use `StreamInfo.AudioSampleRate` (which will be 8000)
as the timebase for `PTSTicks` on G.722 audio frames.

## Converting ticks to seconds / milliseconds

```go
func TicksToSeconds(ticks int64, clockRate uint32) float64 {
    return float64(ticks) / float64(clockRate)
}

func TicksToMS(ticks int64, clockRate uint32) int64 {
    return ticks * 1000 / int64(clockRate)
}
```

## Computing video frame duration

The library sets `DurationTicks = 0` for video frames. Compute it from
consecutive `PTSTicks` values:

```go
var prevPTS int64
var prevDuration uint32

func videoDuration(f *Frame, clockRate uint32) uint32 {
    if prevPTS == 0 {
        prevPTS = f.PTSTicks
        return 0
    }
    dur := uint32(f.PTSTicks - prevPTS)
    prevPTS = f.PTSTicks
    prevDuration = dur
    return dur
}
```

For the **last frame in a fragment**, use `prevDuration` as a best estimate.

At 30 fps with a 90 kHz clock: expected `DurationTicks = 90000 / 30 = 3000`.

## Discontinuity handling

When `FlagDiscontinuity` is set, the timestamp sequence has reset. This
happens on:

- Camera reconnect after a network outage
- Packet-loss burst exceeding 1 second
- Camera clock wrap or reset

**What to do:**

```go
if f.IsDiscontinuity() {
    // 1. Flush and close the current fMP4 fragment or HLS segment.
    // 2. Write tfhd discontinuity flag (bit 0x00010000) on the next moof.
    // 3. Reset any timestamp-delta tracking (prevPTS, etc.).
    // 4. For WebRTC: tolerate the RTP timestamp jump — pion handles this.
    prevPTS = 0
}
```

Do not drop the frame — it still carries valid media data. The discontinuity
flag applies to the boundary, not the frame itself.

## Per-muxer timestamp usage

### fMP4 / LL-HLS

```
mdhd.timescale  = StreamInfo.VideoClockRate   (video track)
                = StreamInfo.AudioSampleRate   (audio track)

trun sample entry:
  sample_duration             = PTSTicks[n+1] - PTSTicks[n]   (video)
                              = DurationTicks                  (audio)
  sample_composition_offset   = PTSTicks - DTSTicks            (= 0 for most cameras)
  sample_size                 = FrameSize

tfdt.baseMediaDecodeTime      = DTSTicks of first sample in fragment

prft.ntp_timestamp            = derived from NTPMS (when FlagNTPSynced)
prft.media_time               = PTSTicks of first sample in fragment
```

### WebRTC

RTP timestamps are 32-bit and use the same clock domain as `PTSTicks`.
Truncate to 32-bit; pion/webrtc handles wrap:

```go
// Video RTP timestamp: pts_ticks already in 90kHz domain.
rtpTS := uint32(f.PTSTicks)

// Audio RTP timestamp: pts_ticks in audio_sample_rate domain.
rtpTS := uint32(f.PTSTicks)
```

For `media.Sample` in pion:

```go
sample := media.Sample{
    Data:               f.Data,
    Duration:           time.Duration(f.DurationTicks) * time.Second / time.Duration(clockRate),
    PacketTimestamp:    uint32(f.PTSTicks),
}
track.WriteSample(sample)
```

## NTP and RTCP sync

`NTPMS` becomes valid once the camera sends an RTCP Sender Report (SR).
This typically happens within a few seconds of stream start but is not
guaranteed. Always check `FlagNTPSynced` before using `NTPMS`.

```go
if f.IsNTPSynced() && f.NTPMS > 0 {
    // Use f.NTPMS for the prft box or cross-session A/V sync.
}
```

`WallClockMS` is always present and is the server's `time.Now()` at the
moment the frame was assembled. It is a reliable fallback for real-time
delivery scheduling when RTCP is not yet synced.

## Timeline reconstruction after a gap

After `FlagDiscontinuity`, the new `PTSTicks` value is independent of the
previous sequence. Do not attempt to interpolate across the gap. Start a
new fragment/segment and let the `tfhd` discontinuity bit signal the
boundary to players.
