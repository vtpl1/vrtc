package avgrabber

/*
#cgo CFLAGS:  -I${SRCDIR}
#cgo LDFLAGS: -L${SRCDIR} -lAudioVideoGrabber2 -Wl,-rpath,${SRCDIR}

#include "avgrabber_api.h"
#include <stdlib.h>
*/
import "C"
import "unsafe"

// Init initialises library-wide resources. Call once per process before Open.
func Init() { C.avgrabber_init() }

// Deinit releases library-wide resources. Call after all sessions are closed.
func Deinit() { C.avgrabber_deinit() }

// Version returns the library version triple.
func Version() (major, minor, patch int) {
	var ma, mi, pa C.int32_t
	C.avgrabber_version(&ma, &mi, &pa)

	return int(ma), int(mi), int(pa)
}

// Session wraps an opaque AVGrabberSession pointer.
type Session struct {
	ptr *C.AVGrabberSession
}

// Open opens an RTSP session. Streaming begins in the background immediately.
// Call Close when done; all frames must be released before Close.
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
	ccfg.frame_queue_depth = C.int32_t(cfg.FrameQueueDepth)

	var ptr *C.AVGrabberSession

	if err := statusError(int(C.avgrabber_open(&ptr, &ccfg))); err != nil {
		return nil, err
	}

	return &Session{ptr: ptr}, nil
}

// Close stops streaming and destroys the session.
func (s *Session) Close() error {
	if s.ptr == nil {
		return nil
	}

	err := statusError(int(C.avgrabber_close(s.ptr)))
	s.ptr = nil

	return err
}

// Stop disconnects without destroying the session. Call Resume to reconnect.
func (s *Session) Stop() error {
	err := statusError(int(C.avgrabber_stop(s.ptr)))

	return err
}

// Resume reconnects a stopped session. No-op if already running.
func (s *Session) Resume() error {
	err := statusError(int(C.avgrabber_resume(s.ptr)))

	return err
}

// NextFrame retrieves the next frame, blocking up to timeoutMS milliseconds.
//
//   - timeoutMS > 0  block up to N ms; return ErrNotReady on timeout
//   - timeoutMS = 0  non-blocking
//   - timeoutMS < 0  block indefinitely
//
// Returns (nil, ErrNotReady) on timeout — caller should loop.
// Returns (nil, err) where IsFatal(err) is true to signal session end.
func (s *Session) NextFrame(timeoutMS int32) (*Frame, error) {
	var cframe *C.AVGrabberFrame

	if err := statusError(
		int(C.avgrabber_next_frame(s.ptr, C.int32_t(timeoutMS), &cframe)),
	); err != nil {
		return nil, err
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

	if rc := int(C.avgrabber_stream_info(s.ptr, &ci)); rc != StatusOK {
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

	if rc := int(C.avgrabber_stats(s.ptr, &cs)); rc != StatusOK {
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
