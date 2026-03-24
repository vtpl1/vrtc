# WebRTC Muxer Guide

This guide covers sending AVGrabber frames over WebRTC using
[pion/webrtc](https://github.com/pion/webrtc) v3.

WebRTC does not use a container format. Frames are RTP-packetised directly:
video becomes one or more RTP packets per access unit; audio is similarly
packetised. pion handles packetisation internally — you hand it raw
Annex-B video and raw audio payloads.

## Codec support in WebRTC

| AVGrabber codec | WebRTC support | Notes |
|----------------|---------------|-------|
| H.264 | Native | Most widely supported; pion packetises Annex-B directly |
| H.265 | Limited | Not in WebRTC spec; supported by some browsers via extensions |
| Opus | Native | Preferred audio codec for WebRTC |
| G.711 µ-law | Native (`PCMU`) | Supported; no transcoding needed |
| G.711 A-law | Native (`PCMA`) | Supported; no transcoding needed |
| AAC | Not native | Must transcode to Opus for standard WebRTC |
| G.722 | Supported | `telephone-event` or `G722` in SDP |

## Dependencies

```bash
go get github.com/pion/webrtc/v3
go get github.com/pion/rtp
go get github.com/pion/media
```

## Video track setup (H.264)

```go
import (
    "github.com/pion/webrtc/v3"
    "github.com/pion/webrtc/v3/pkg/media"
)

// Create a video track with H.264 codec.
videoTrack, err := webrtc.NewTrackLocalStaticSample(
    webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
    "video", "avgrabber-video",
)
if err != nil {
    return err
}

// Add to peer connection during offer/answer negotiation.
rtpSender, err := peerConn.AddTrack(videoTrack)
```

## Audio track setup

```go
// G.711 µ-law (PCMU) — no transcoding required.
audioTrack, err := webrtc.NewTrackLocalStaticSample(
    webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU},
    "audio", "avgrabber-audio",
)

// For Opus (requires transcoding from AAC/G.711):
audioTrack, err := webrtc.NewTrackLocalStaticSample(
    webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
    "audio", "avgrabber-audio",
)
```

## Sending video frames

pion's `WriteSample` accepts raw Annex-B for H.264 — no AVCC conversion needed.

```go
import "time"

var clockRate uint32 = 90000 // StreamInfo.VideoClockRate

func sendVideoFrame(f *Frame, track *webrtc.TrackLocalStaticSample) error {
    // Compute frame duration from consecutive PTSTicks deltas (see 04-timestamps.md).
    // For a simple start: assume constant frame rate from StreamInfo.FPS.
    var duration time.Duration
    if streamInfo.FPS > 0 {
        duration = time.Second / time.Duration(streamInfo.FPS)
    } else {
        duration = 33 * time.Millisecond // fallback: 30 fps
    }

    return track.WriteSample(media.Sample{
        Data:               f.Data, // Annex-B bytes — pion packetises internally
        Duration:           duration,
        PacketTimestamp:    uint32(f.PTSTicks), // 32-bit truncation; pion handles wrap
    })
}
```

For accurate per-frame duration use consecutive PTS deltas:

```go
var lastPTS int64

func videoDuration(f *Frame) time.Duration {
    if lastPTS == 0 {
        lastPTS = f.PTSTicks
        return 33 * time.Millisecond // first frame: estimate
    }
    deltaTicks := f.PTSTicks - lastPTS
    lastPTS = f.PTSTicks
    return time.Duration(deltaTicks) * time.Second / time.Duration(clockRate)
}
```

## Sending audio frames

### G.711 µ-law / A-law (no transcoding)

```go
var audioClockRate uint32 = 8000 // StreamInfo.AudioSampleRate for G.711

func sendAudioFrame(f *Frame, track *webrtc.TrackLocalStaticSample) error {
    durationTicks := f.DurationTicks
    if durationTicks == 0 {
        durationTicks = 160 // typical G.711 frame: 20 ms at 8 kHz
    }
    duration := time.Duration(durationTicks) * time.Second / time.Duration(audioClockRate)

    return track.WriteSample(media.Sample{
        Data:            f.Data,
        Duration:        duration,
        PacketTimestamp: uint32(f.PTSTicks),
    })
}
```

### AAC → Opus transcoding

Standard WebRTC does not support AAC. If the camera sends AAC you must
transcode to Opus before sending. The pion ecosystem has no built-in AAC
decoder; use a CGO-based library or a pure-Go AAC decoder:

```go
// Option 1: use github.com/gen2brain/malgo (miniaudio) via cgo
// Option 2: use github.com/nicholasgasior/go-audio for raw PCM, then encode to Opus

// Simplified pipeline:
//   AVGrabber AAC (ADTS) → strip ADTS → decode AAC to PCM → encode PCM to Opus → WriteSample
```

If transcoding complexity is unacceptable, configure `cfg.Audio = false` for
the AVGrabber session and manage audio separately, or target cameras with
Opus or G.711 support.

## Handling PARAM_SET (H.264 SPS/PPS)

pion/webrtc does not need PARAM_SET frames as a separate step — it
negotiates codec parameters during SDP exchange. However, if you are using
`TrackLocalStaticRTP` (manual RTP) rather than `TrackLocalStaticSample`,
you may need to send in-band SPS/PPS with each IDR using a STAP-A NAL:

```go
// For TrackLocalStaticSample users: PARAM_SET frames can be discarded.
// pion injects SPS/PPS from the SDP negotiation.

// For TrackLocalStaticRTP users: prepend SPS+PPS before each IDR.
func prependParamSets(paramSet, keyFrame []byte) []byte {
    out := make([]byte, len(paramSet)+len(keyFrame))
    copy(out, paramSet)
    copy(out[len(paramSet):], keyFrame)
    return out
}
```

## Discontinuity handling

When `FlagDiscontinuity` is set, RTP timestamps will jump. pion handles
RTP sequence number and timestamp wrap internally, but a large timestamp
jump may confuse some WebRTC receivers. Options:

1. **Ignore** — pion and most browsers tolerate timestamp discontinuities.
2. **Force a new RTP stream** — remove and re-add the track to the
   `PeerConnection`, triggering a new SSRC and fresh SDP negotiation.
3. **Send a PLI (Picture Loss Indication)** — signal the remote decoder to
   request a keyframe, which will arrive shortly after the reconnect anyway.

For typical use (live camera streaming), option 1 is usually sufficient.

## Complete send loop

```go
func sendLoop(session *Session, videoTrack, audioTrack *webrtc.TrackLocalStaticSample) error {
    var lastVideoPTS int64
    videoClockRate := uint32(90000) // update from StreamInfo after first PARAM_SET

    for {
        f, err := session.NextFrame(200)
        if err != nil {
            if IsNotReady(err) {
                continue
            }
            return err
        }

        if f.IsDiscontinuity() {
            lastVideoPTS = 0 // reset duration tracking
        }

        switch f.MediaType {
        case MediaVideo:
            switch f.FrameType {
            case FrameTypeParamSet:
                // TrackLocalStaticSample: discard or cache for manual RTP use.
                continue
            case FrameTypeKey, FrameTypeDelta:
                var dur time.Duration
                if lastVideoPTS > 0 {
                    delta := f.PTSTicks - lastVideoPTS
                    dur = time.Duration(delta) * time.Second / time.Duration(videoClockRate)
                } else {
                    dur = 33 * time.Millisecond
                }
                lastVideoPTS = f.PTSTicks

                if err := videoTrack.WriteSample(media.Sample{
                    Data:            f.Data,
                    Duration:        dur,
                    PacketTimestamp: uint32(f.PTSTicks),
                }); err != nil {
                    return err
                }
            }

        case MediaAudio:
            if f.CodecType == CodecAAC {
                continue // skip unless you have a transcoder
            }
            audioRate := uint32(8000) // update from StreamInfo
            dur := time.Duration(f.DurationTicks) * time.Second / time.Duration(audioRate)
            if err := audioTrack.WriteSample(media.Sample{
                Data:            f.Data,
                Duration:        dur,
                PacketTimestamp: uint32(f.PTSTicks),
            }); err != nil {
                return err
            }
        }
    }
}
```

## RTCP feedback — handling PLI / FIR

When a WebRTC receiver sends a Picture Loss Indication (PLI) or Full
Intra Request (FIR), you do not control the camera's keyframe schedule
directly through AVGrabber. Options:

1. **Wait** — the camera emits a keyframe on its natural interval.
2. **Reconnect the session** — `session.Stop()` + `session.Resume()` forces
   a reconnect, which always triggers a `PARAM_SET`+`KEY` sequence.

```go
// Read RTCP packets from the RTP sender.
go func() {
    buf := make([]byte, 1500)
    for {
        if _, _, err := rtpSender.Read(buf); err != nil {
            return
        }
        // PLI/FIR received — if low-latency IDR is required, reconnect.
    }
}()
```
