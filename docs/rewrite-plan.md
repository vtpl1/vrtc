# Rewrite Plan: RTSP → Muxer Pipeline

## Overview

Complete rewrite of the AV pipeline. The AVGrabber C library
(`internal/avgrabber/libAudioVideoGrabber2.so`) is the authoritative RTSP
demuxer. Everything above it adapts, routes, and muxes its output.

Core principle: work directly with `av.Packet` at every layer — no
intermediate `avf.Frame` conversion. The existing `pkg/av` interfaces,
`pkg/av/streammanager3`, and `pkg/av/format/{fmp4,mp4,llhls}` are kept
as-is; the new work is the avgrabber CGO adapter, the PVA analytics
package, and a WebRTC placeholder.

---

## Full Data Flow

```
RTSP Camera
     │  (Annex-B frames over RTSP/RTP)
     ▼
libAudioVideoGrabber2.so  (C library — pull model, internal queue)
     │  AVGrabberFrame{FrameType, Annex-B, PTSTicks, DTSTicks, WallClockMS, Flags}
     ▼
internal/avgrabber/demuxer.go          implements av.DemuxCloser + av.Pauser
  GetCodecs()  → blocks until first PARAM_SET → parse SPS/PPS/VPS
                 → []av.Stream{h264parser.CodecData / h265parser.CodecData, aacparser.CodecData}
  ReadPacket() → KEY/DELTA/AUDIO → av.Packet{raw NALU, time.Duration DTS, FrameID, WallClockTime}
                 new PARAM_SET   → pkt.NewCodecs set on the next KEY frame
  Pause()      → avgrabber_stop()
  Resume()     → avgrabber_resume()
     │  av.Packet  (Metadata nil)
     ▼
pkg/pva/merger.go  MetadataMerger     implements av.DemuxCloser (decorator)
  GetCodecs()  → pass-through
  ReadPacket() → inner.ReadPacket() → Source.Fetch(pkt.FrameID, pkt.WallClockTime)
                                    → pkt.Metadata = *pva.PVAData  (nil if unavailable)
     │  av.Packet  (Metadata = *pva.PVAData or nil)
     ▼
pkg/av/streammanager3.StreamManager        THE BRIDGE
  ┌─ Producer (one per camera / RTSP URL)
  │    Start()         → demuxerFactory(ctx, producerID)
  │                       returns MetadataMerger(avgrabberDemuxer, analyticsSource)
  │    GetCodecs()     → waits on headersAvailable → returns []av.Stream
  │    readWriteLoop() → ReadPacket() → fan-out to consumers
  │       • 1 consumer  → blocking write (back-pressure, zero drops)
  │       • 2+ consumers → leaky write  (slow consumer drops, others unaffected)
  │    pkt.NewCodecs != nil → updates cached headers for late-joining consumers
  │
  └─ Consumer (one per downstream sink)
       Start()         → waits for headersAvailable → muxerFactory(ctx, consumerID)
       WriteHeader()   → muxer.WriteHeader(streams)       [writes ftyp+moov for fmp4]
       write loop:
         pkt.NewCodecs → muxer.(CodecChanger).WriteCodecChange()  [new moov mid-stream]
         WritePacket() → muxer.WritePacket(pkt)
       Close()         → muxer.WriteTrailer() + muxer.Close()
     │  (fan-out to N consumers)
     ├─────────────────┬──────────────────┬──────────────────┐
     ▼                 ▼                  ▼                  ▼
fmp4.Muxer        llhls.Muxer       webrtc.Sender      mp4.Muxer
WriteHeader→moov  WriteHeader→init  WriteHeader→SDP    WriteHeader→moov
WritePacket→moof  WritePacket→CMAF  WritePacket→RTP    WritePacket→sample
CodecChange→moov  CodecChange→seg   (placeholder)      CodecChange→moov
     │                 │
     ▼                 ▼
io.Writer          manifest.m3u8 +
(HTTP/DASH/file)   segment files
```

---

## Package Layout

```
internal/
  avgrabber/
    avgrabber_api.h             (existing — C API header)
    libAudioVideoGrabber2.so    (existing — compiled C library)
    constants.go    ← FrameType*, MediaType*, Codec*, Flag*, status code constants
    types.go        ← Frame, StreamInfo, Stats, Config  (Go mirrors of C structs)
    errors.go       ← statusError(), IsNotReady(), IsFatal()
    session.go      ← CGO: Init/Deinit/Open/Close/Stop/Resume/NextFrame/GetStreamInfo/GetStats
    demuxer.go      ← implements av.DemuxCloser + av.Pauser

pkg/
  pva/
    pva.go          ← PVAData, ObjectInfo types (no import of pkg/av — avoids circular dep)
    source.go       ← Source interface + NilSource
    merger.go       ← MetadataMerger: av.DemuxCloser decorator; injects *PVAData per packet

  av/                           (keep existing — interfaces unchanged)
    av.go                       CodecType, Stream, CodecData interfaces
    packet.go                   Packet.Metadata any, Packet.FrameID int64
    demuxer.go                  Demuxer, DemuxCloser, Pauser, TimeSeeker
    muxer.go                    Muxer, MuxCloser, CodecChanger
    factory.go                  DemuxerFactory, MuxerFactory, et al.
    streammanager.go            StreamManager interface
    streammanager3/             (keep existing — StreamManager impl)
    codec/h264parser/           (keep)
    codec/h265parser/           (keep)
    codec/aacparser/            (keep)

  format/
    fmp4/           (keep existing — Muxer + FragmentWriter + BuildInitSegment)
    mp4/            (keep existing)
    llhls/          (keep existing)
    webrtc/
      sender.go     ← NEW placeholder: av.MuxCloser stub for pion/webrtc
```

---

## Implementation Steps

### Step 1 — `pkg/pva/` (no deps; unblocks everything)

**`pva.go`**
- `ObjectInfo` struct: X, Y, W, H, T, C, I, E fields with bson/json tags
- `PVAData` struct: SiteID, ChannelID, timestamps, FrameID, counts, `Objects []*ObjectInfo`
  - Use `*ObjectInfo` pointer slice (nil-safe, avoids zero-value allocations)
  - Use `*PVAData` pointer throughout (nil = no analytics; avoids boxing cost)

**`source.go`**
- `Source interface { Fetch(frameID int64, wallClock time.Time) *PVAData }`
- `NilSource{}` implements `Source`, always returns nil

**`merger.go`**
- `MetadataMerger` wraps `av.DemuxCloser` + `Source`
- `GetCodecs` and `Close` pass through
- `ReadPacket` calls inner, sets `pkt.Metadata = source.Fetch(pkt.FrameID, pkt.WallClockTime)`

---

### Step 2 — `internal/avgrabber/` CGO bindings

**`constants.go`** — pure Go constants matching `avgrabber_api.h`:
```
FrameTypeParamSet = 0, FrameTypeKey = 1, FrameTypeDelta = 2
FrameTypeAudio = 16, FrameTypeUnknown = 255
MediaVideo = 0, MediaAudio = 1
CodecH264 = 2, CodecH265 = 8, CodecAAC = 6, CodecG711U = 3, ...
FlagNTPSynced = 0x01, FlagDiscontinuity = 0x02, FlagKeyframe = 0x04, FlagHasSEI = 0x10
StatusOK = 0, ErrNotReady = 10, ErrStopped = 18, ErrAuthFailed = 1101, ...
```

**`types.go`** — Go structs:
```go
Frame{ FrameType, MediaType, FrameSize, CodecType, Flags,
       WallClockMS, NTPMS, PTSTicks, DTSTicks, DurationTicks, Data []byte }
StreamInfo{ VideoCodec, AudioCodec, AudioChannels, FPS,
            Width, Height, VideoClockRate, AudioSampleRate, AudioSamplesPerFrame }
Stats{ VideoBitrateKbps, VideoFPSMilli, VideoFrameTotal, ... }
Config{ URL, Username, Password, Protocol, Multicast, Audio,
        ConnectTimeoutMS, FrameQueueDepth }
```

**`errors.go`** — `statusError(rc int) error`, `IsNotReady(err)`, `IsFatal(err)`

**`session.go`** — CGO wrapper:
```
#cgo LDFLAGS: -L${SRCDIR} -lAudioVideoGrabber2
#cgo CFLAGS:  -I${SRCDIR}
```
Functions: `Init()`, `Deinit()`, `Version()`, `Open(Config) (*Session, error)`,
`(*Session).Close()`, `Stop()`, `Resume()`, `NextFrame(timeoutMS) (*Frame, error)`,
`GetStreamInfo() (StreamInfo, error)`, `GetStats() (Stats, error)`

**`demuxer.go`** — implements `av.DemuxCloser` + `av.Pauser`:
- `GetCodecs(ctx)`:
  - Pull loop calling `NextFrame(200ms)` until `FrameTypeParamSet` arrives
  - Accumulate PARAM_SET payloads; on first KEY frame parse them:
    - H.264: `SplitAnnexB` → `h264parser.NewCodecDataFromSPSAndPPS(sps, pps)`
    - H.265: `SplitAnnexB` → `h265parser.NewCodecDataFromVPSSPSPPS(vps, sps, pps)`
    - AAC: buffer first audio frame; `aacparser` for `AudioSpecificConfig`
  - Return `[]av.Stream` with video idx=0, audio idx=1 (if present)
- `ReadPacket(ctx)`:
  - `NextFrame(50ms)` in loop, skip `ErrNotReady`
  - `FrameTypeParamSet` → accumulate into `pendingParamSet`; return next call
  - `FrameTypeKey` → `av.Packet{KeyFrame:true}` + set `NewCodecs` if `pendingParamSet` changed
  - `FrameTypeDelta` → `av.Packet{KeyFrame:false}`
  - `FrameTypeAudio` → `av.Packet` with audio stream index
  - Strip Annex-B start code from payload → raw NALU in `pkt.Data`
  - Timestamps: `pkt.DTS = time.Duration(frame.DTSTicks) * time.Second / videoClockRate`
  - `pkt.FrameID = frame.PTSTicks` (unique monotonic ID for PVA correlation)
  - `pkt.WallClockTime = time.UnixMilli(frame.WallClockMS)`
  - `pkt.IsDiscontinuity = frame.Flags & FlagDiscontinuity != 0`
- `Pause(ctx)` → `session.Stop()`
- `Resume(ctx)` → `session.Resume()`

---

### Step 3 — `pkg/format/webrtc/sender.go` (placeholder)

Implements `av.MuxCloser`. All methods compile and return `ErrNotImplemented`
except `WriteHeader` (stores `[]av.Stream`) and `Close` (no-op).
Documents the pion send loop from `internal/avgrabber/docs/go/06-muxer-webrtc.md`
in a large `// TODO` comment block for the future implementer.

---

### Step 4 — Wire in `cmd/` binary

```go
// DemuxerFactory: avgrabber + PVA merger
demuxerFactory := func(ctx context.Context, producerID string) (av.DemuxCloser, error) {
    cfg := avgrabber.Config{URL: producerID, Audio: true, Protocol: avgrabber.ProtoTCP}
    dmx, err := avgrabber.NewDemuxer(cfg)
    if err != nil {
        return nil, err
    }
    return pva.NewMetadataMerger(dmx, pva.NilSource{}), nil
}

// MuxerFactory examples
fmp4Factory := func(ctx context.Context, consumerID string) (av.MuxCloser, error) {
    // consumerID maps to an http.ResponseWriter or file
    return fmp4.NewMuxer(w), nil
}

sm := streammanager3.New(demuxerFactory, nil)
sm.Start(ctx)
sm.AddConsumer(ctx, "rtsp://camera/stream", "fmp4-client-1", fmp4Factory, nil, errCh)
```

---

## Key Design Decisions

| Decision | Choice | Reason |
|---|---|---|
| fMP4 library | Existing `pkg/format/fmp4` | Already production-quality; handles emsg, CodecTag, FragmentWriter, CodecChange |
| Muxer interface | `WriteHeader` + `WritePacket` + `WriteTrailer` | fMP4 init segment must precede all moof/mdat; required for WebRTC SDP; enables late-joining consumers |
| Codec change | `pkt.NewCodecs` + `CodecChanger` | Already implemented in all muxers; handles mid-stream resolution changes |
| Analytics injection | `MetadataMerger` decorator on demuxer | Transparent to StreamManager and all muxers; correlation by `FrameID` |
| `PVAData` location | `pkg/pva/` (own package) | Breaks circular dep between `pkg/av` (Packet) and analytics types |
| `Metadata` field type | `any` on `av.Packet` | Avoids import cycle; nil when absent (zero allocation) |
| Delivery policy | 1 consumer = blocking, 2+ = leaky | Single consumer: no drops; multi-consumer: one slow sink can't stall others |
| WebRTC | Placeholder only | pion + AAC transcoding complexity; separate phase |

---

## What Is Not Changing

| Component | Reason |
|---|---|
| `av.Muxer` / `av.Demuxer` interfaces | Correct and industry-standard |
| `pkg/av/streammanager3/` | Complete; handles all lifecycle and concurrency |
| `pkg/av/format/fmp4/` | Production-quality; FragmentWriter, emsg, CodecTag all present |
| `pkg/av/format/mp4/` | Kept |
| `pkg/av/format/llhls/` | Kept |
| `pkg/av/codec/h264parser/` | Used by avgrabber demuxer adapter |
| `pkg/av/codec/h265parser/` | Used by avgrabber demuxer adapter |
| `pkg/av/codec/aacparser/` | Used by avgrabber demuxer adapter |

`pkg/avf/` (AVF container) will be removed after migration is complete.
