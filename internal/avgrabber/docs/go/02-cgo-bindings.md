# cgo Bindings

A complete Go wrapper over `avgrabber_api.h`. Copy these types and helpers
into your package as a starting point — they are designed to be extended,
not used verbatim.

## Constants

```go
// Frame type (AVGrabberFrameHeader.FrameType)
const (
    FrameTypeParamSet = 0   // AVGRABBER_FRAME_PARAM_SET — SPS+PPS / VPS+SPS+PPS
    FrameTypeKey      = 1   // AVGRABBER_FRAME_KEY       — IDR / random-access keyframe
    FrameTypeDelta    = 2   // AVGRABBER_FRAME_DELTA     — non-key video frame
    FrameTypeAudio    = 16  // AVGRABBER_FRAME_AUDIO     — raw audio payload
    FrameTypeUnknown  = 255 // AVGRABBER_FRAME_UNKNOWN   — unclassified
)

// Media type (AVGrabberFrameHeader.MediaType)
const (
    MediaVideo = 0 // AVGRABBER_MEDIA_VIDEO
    MediaAudio = 1 // AVGRABBER_MEDIA_AUDIO
)

// Codec type (AVGrabberFrameHeader.CodecType)
const (
    CodecMJPEG   = 0
    CodecMPEG    = 1
    CodecH264    = 2
    CodecG711U   = 3  // G.711 µ-law, 8 kHz mono
    CodecG711A   = 4  // G.711 A-law, 8 kHz mono
    CodecL16     = 5  // Linear PCM 16-bit
    CodecAAC     = 6  // AAC, ADTS-framed
    CodecUnknown = 7
    CodecH265    = 8
    CodecG722    = 9  // G.722 ADPCM, 16 kHz encoded bandwidth
    CodecG726    = 10 // G.726 ADPCM, 8 kHz
    CodecOpus    = 11 // Opus, 48 kHz RTP clock
)

// Transport protocol (Config.Protocol)
const (
    ProtoUDP = 0
    ProtoTCP = 1
)

// Flag bits (Frame.Flags bitmask)
const (
    FlagNTPSynced     = 0x01 // ntp_ms is RTCP-anchored (true camera wall-clock)
    FlagDiscontinuity = 0x02 // clock reset or gap > 1 s preceded this frame
    FlagKeyframe      = 0x04 // random-access video frame
    FlagHasSEI        = 0x10 // SEI NAL units present in this access unit
)

// Status codes
const (
    OK              = 0
    ErrNullPointer  = 3
    ErrNotReady     = 10   // normal timeout — keep looping
    ErrStopped      = 18
    ErrInvalidArg   = 37
    ErrAuthFailed   = 1101
)
```

## Core types

```go
// Frame is a Go copy of one AVGrabberFrameHeader plus its payload.
// Data is a fresh slice allocated by C.GoBytes; you own it.
type Frame struct {
    FrameType     int32
    MediaType     int32
    FrameSize     int32
    CodecType     uint8
    Flags         uint8
    WallClockMS   int64
    NTPMS         int64  // valid only when Flags&FlagNTPSynced != 0
    PTSTicks      int64  // presentation timestamp in stream clock ticks (64-bit, monotonic)
    DTSTicks      int64  // decode timestamp (== PTSTicks for most IP cameras)
    DurationTicks uint32 // audio: samples per frame; video: 0 (compute from consecutive PTSTicks)
    Data          []byte // exactly FrameSize bytes
}

// Flag helpers — check individual flag bits.
func (f *Frame) IsKeyframe()      bool { return f.Flags&FlagKeyframe != 0 }
func (f *Frame) IsNTPSynced()     bool { return f.Flags&FlagNTPSynced != 0 }
func (f *Frame) IsDiscontinuity() bool { return f.Flags&FlagDiscontinuity != 0 }
func (f *Frame) HasSEI()          bool { return f.Flags&FlagHasSEI != 0 }

// StreamInfo mirrors AVGrabberStreamInfo (available after first PARAM_SET).
type StreamInfo struct {
    VideoCodec           uint8
    AudioCodec           uint8
    AudioChannels        uint8  // 1 = mono, 2 = stereo
    FPS                  uint8  // from SPS VUI; 0 if not signalled
    Width                uint16 // pixels; 0 until first SPS
    Height               uint16
    VideoClockRate       uint32 // RTP clock for video, typically 90000 Hz
    AudioSampleRate      uint32 // RTP clock for audio, e.g. 8000 / 48000 Hz
    AudioSamplesPerFrame uint32 // AAC=1024, G.711≈160, Opus=960; use as duration_ticks
}

// Stats mirrors AVGrabberStats (available immediately after Open).
type Stats struct {
    VideoBitrateKbps uint32
    VideoFPSMilli    uint32 // fps × 1000; divide by 1000 for real fps
    VideoFrameTotal  uint64
    VideoBytesTotal  uint64
    AudioBitrateKbps uint32
    AudioFrameTotal  uint64
    AudioBytesTotal  uint64
    ElapsedMS        uint64
    Discontinuities  uint32
}

// Config is the session configuration passed to Open.
// Fields not set default to safe zero values.
type Config struct {
    URL              string
    Username         string
    Password         string
    Protocol         int32  // ProtoTCP or ProtoUDP
    Multicast        bool
    Audio            bool
    ConnectTimeoutMS int32  // 0 = library default (5000 ms)
    FrameQueueDepth  int32  // 0 = library default (60 frames)
}
```

## Session lifecycle

```go
/*
#cgo CFLAGS: -I../../AudioVideoGrabber3/include
#cgo LDFLAGS: -L../../build/Release -lAudioVideoGrabber2
#include "avgrabber_api.h"
#include <stdlib.h>
*/
import "C"
import (
    "errors"
    "fmt"
    "unsafe"
)

// Init initialises library-wide resources. Call once per process.
func Init() { C.avgrabber_init() }

// Deinit releases library-wide resources. Call after all sessions are closed.
func Deinit() { C.avgrabber_deinit() }

// Version returns the library version triple.
func Version() (major, minor, patch int) {
    var ma, mi, pa C.int32_t
    C.avgrabber_version(&ma, &mi, &pa)
    return int(ma), int(mi), int(pa)
}

// Session wraps an opaque AVGrabberSession*.
type Session struct {
    ptr *C.AVGrabberSession
}

// Open opens an RTSP session. Returns immediately; streaming begins in background.
// Call Close when done. All outstanding frames must be released before Close.
func Open(cfg Config) (*Session, error) {
    var ccfg C.AVGrabberConfig

    cURL := C.CString(cfg.URL)
    defer C.free(unsafe.Pointer(cURL))
    ccfg.url = cURL

    cUser := C.CString(cfg.Username)
    defer C.free(unsafe.Pointer(cUser))
    ccfg.username = cUser

    cPass := C.CString(cfg.Password)
    defer C.free(unsafe.Pointer(cPass))
    ccfg.password = cPass

    ccfg.protocol = C.int32_t(cfg.Protocol)
    if cfg.Audio {
        ccfg.audio = 1
    }
    if cfg.Multicast {
        ccfg.multicast = 1
    }
    ccfg.connect_timeout_ms = C.int32_t(cfg.ConnectTimeoutMS)
    ccfg.frame_queue_depth  = C.int32_t(cfg.FrameQueueDepth)

    var ptr *C.AVGrabberSession
    rc := C.avgrabber_open(&ptr, &ccfg)
    if rc != C.AVGRABBER_OK {
        return nil, statusError(int(rc))
    }
    return &Session{ptr: ptr}, nil
}

// Close stops streaming and destroys the session.
func (s *Session) Close() error {
    rc := C.avgrabber_close(s.ptr)
    s.ptr = nil
    if rc != C.AVGRABBER_OK {
        return statusError(int(rc))
    }
    return nil
}

// Stop disconnects without destroying the session. Resume to reconnect.
func (s *Session) Stop() error {
    rc := C.avgrabber_stop(s.ptr)
    if rc != C.AVGRABBER_OK {
        return statusError(int(rc))
    }
    return nil
}

// Resume reconnects a stopped session. No-op if already running.
func (s *Session) Resume() error {
    rc := C.avgrabber_resume(s.ptr)
    if rc != C.AVGRABBER_OK {
        return statusError(int(rc))
    }
    return nil
}
```

## Frame retrieval — pull model

```go
// NextFrame retrieves the next frame, blocking up to timeoutMS milliseconds.
//
//   timeoutMS > 0  block up to N ms; return ErrNotReady if no frame
//   timeoutMS = 0  non-blocking
//   timeoutMS < 0  block indefinitely
//
// Returns (nil, ErrNotReady) on timeout — caller should loop.
// Returns (nil, ErrStopped) or (nil, ErrAuthFailed) to signal session end.
func (s *Session) NextFrame(timeoutMS int32) (*Frame, error) {
    var cframe *C.AVGrabberFrame
    rc := int(C.avgrabber_next_frame(s.ptr, C.int32_t(timeoutMS), &cframe))
    if rc != OK {
        return nil, statusError(rc)
    }
    defer C.avgrabber_frame_release(cframe)

    h := C.avgrabber_frame_header(cframe)
    d := C.avgrabber_frame_data(cframe)
    sz := int(h.frame_size)

    return &Frame{
        FrameType:     int32(h.frame_type),
        MediaType:     int32(h.media_type),
        FrameSize:     int32(sz),
        CodecType:     uint8(h.codec_type),
        Flags:         uint8(h.flags),
        WallClockMS:   int64(h.wall_clock_ms),
        NTPMS:         int64(h.ntp_ms),
        PTSTicks:      int64(h.pts_ticks),
        DTSTicks:      int64(h.dts_ticks),
        DurationTicks: uint32(h.duration_ticks),
        Data:          C.GoBytes(unsafe.Pointer(d), C.int(sz)),
    }, nil
}

// GetStreamInfo returns negotiated codec and resolution parameters.
// Returns ErrNotReady until the first PARAM_SET frame has been received.
func (s *Session) GetStreamInfo() (StreamInfo, error) {
    var ci C.AVGrabberStreamInfo
    rc := int(C.avgrabber_stream_info(s.ptr, &ci))
    if rc != OK {
        return StreamInfo{}, statusError(rc)
    }
    return StreamInfo{
        VideoCodec:           uint8(ci.video_codec),
        AudioCodec:           uint8(ci.audio_codec),
        AudioChannels:        uint8(ci.audio_channels),
        FPS:                  uint8(ci.fps),
        Width:                uint16(ci.width),
        Height:               uint16(ci.height),
        VideoClockRate:       uint32(ci.video_clock_rate),
        AudioSampleRate:      uint32(ci.audio_sample_rate),
        AudioSamplesPerFrame: uint32(ci.audio_samples_per_frame),
    }, nil
}

// GetStats returns runtime statistics. Available immediately after Open.
func (s *Session) GetStats() (Stats, error) {
    var cs C.AVGrabberStats
    rc := int(C.avgrabber_stats(s.ptr, &cs))
    if rc != OK {
        return Stats{}, statusError(rc)
    }
    return Stats{
        VideoBitrateKbps: uint32(cs.video_bitrate_kbps),
        VideoFPSMilli:    uint32(cs.video_fps_milli),
        VideoFrameTotal:  uint64(cs.video_frames_total),
        VideoBytesTotal:  uint64(cs.video_bytes_total),
        AudioBitrateKbps: uint32(cs.audio_bitrate_kbps),
        AudioFrameTotal:  uint64(cs.audio_frames_total),
        AudioBytesTotal:  uint64(cs.audio_bytes_total),
        ElapsedMS:        uint64(cs.elapsed_ms),
        Discontinuities:  uint32(cs.discontinuities),
    }, nil
}
```

## Push model (callback)

cgo cannot pass a Go function pointer directly to C. The standard pattern
uses `//export` and a package-level channel.

```go
// pushCh delivers frame headers from the C callback thread to Go goroutines.
// Buffer size should be large enough to absorb bursts without blocking the
// library's delivery thread.
var pushCh = make(chan Frame, 512)

// goFrameCallback is exported so C can call it.
// It runs on the library's internal delivery thread.
// Do NOT call avgrabber_stop() or avgrabber_close() from here.
//
//export goFrameCallback
func goFrameCallback(
    _ *C.AVGrabberSession,
    h *C.AVGrabberFrameHeader,
    d *C.uint8_t,
    _ unsafe.Pointer,
) {
    sz := int(h.frame_size)
    f := Frame{
        FrameType:     int32(h.frame_type),
        MediaType:     int32(h.media_type),
        FrameSize:     int32(sz),
        CodecType:     uint8(h.codec_type),
        Flags:         uint8(h.flags),
        WallClockMS:   int64(h.wall_clock_ms),
        NTPMS:         int64(h.ntp_ms),
        PTSTicks:      int64(h.pts_ticks),
        DTSTicks:      int64(h.dts_ticks),
        DurationTicks: uint32(h.duration_ticks),
        Data:          C.GoBytes(unsafe.Pointer(d), C.int(sz)),
    }
    // Non-blocking send: drop frame if consumer is lagging.
    select {
    case pushCh <- f:
    default:
    }
}

// SetCallback registers the exported Go callback on a session.
func (s *Session) SetCallback() error {
    rc := C.avgrabber_set_callback(
        s.ptr,
        (C.AVGrabberFrameCallback)(C.goFrameCallback),
        nil,
    )
    if rc != C.AVGRABBER_OK {
        return statusError(int(rc))
    }
    return nil
}
```

The C forward-declaration must also appear in the cgo comment block:

```c
extern void goFrameCallback(AVGrabberSession*, const AVGrabberFrameHeader*,
                             const uint8_t*, void*);
```

## Error helper

```go
var errMessages = map[int]string{
    OK:             "ok",
    ErrNullPointer: "null pointer",
    ErrNotReady:    "not ready",
    ErrStopped:     "stopped",
    ErrInvalidArg:  "invalid argument",
    ErrAuthFailed:  "authentication failed",
}

func statusError(rc int) error {
    if msg, ok := errMessages[rc]; ok {
        return errors.New(msg)
    }
    return fmt.Errorf("avgrabber error %d", rc)
}

// IsNotReady returns true for the normal timeout condition.
func IsNotReady(err error) bool { return err != nil && err.Error() == "not ready" }

// IsFatal returns true when the session will not recover automatically.
func IsFatal(err error) bool {
    if err == nil {
        return false
    }
    s := err.Error()
    return s == "stopped" || s == "authentication failed"
}
```

## Typical pull loop

```go
func capture(s *Session) error {
    for {
        f, err := s.NextFrame(200)
        if err != nil {
            if IsNotReady(err) {
                continue
            }
            return err // stopped or auth failed
        }
        if err := handleFrame(f); err != nil {
            return err
        }
    }
}
```
