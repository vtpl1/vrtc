package avgrabber

// Frame is a Go copy of one AVGrabberFrameHeader plus its payload.
// Data is a fresh []byte allocated by C.GoBytes; the caller owns it.
type Frame struct {
	FrameType     int32
	MediaType     int32
	FrameSize     int32
	CodecType     uint8
	Flags         uint8
	WallClockMS   int64  // server wall clock, ms since Unix epoch; always valid
	NTPMS         int64  // camera NTP ms; valid only when Flags&FlagNTPSynced != 0
	PTSTicks      int64  // presentation timestamp in stream clock ticks (64-bit, monotonic)
	DTSTicks      int64  // decode timestamp; == PTSTicks for IP cameras without B-frames
	DurationTicks uint32 // audio: samples per frame; video: 0 (compute from consecutive PTSTicks)
	Data          []byte // exactly FrameSize bytes of Annex-B payload (video) or raw audio
}

// IsKeyframe reports whether FlagKeyframe is set.
func (f *Frame) IsKeyframe() bool { return f.Flags&FlagKeyframe != 0 }

// IsNTPSynced reports whether NTPMS is anchored to a camera RTCP SR.
func (f *Frame) IsNTPSynced() bool { return f.Flags&FlagNTPSynced != 0 }

// IsDiscontinuity reports whether a clock reset or gap preceded this frame.
func (f *Frame) IsDiscontinuity() bool { return f.Flags&FlagDiscontinuity != 0 }

// HasSEI reports whether SEI NAL units are present in this access unit.
func (f *Frame) HasSEI() bool { return f.Flags&FlagHasSEI != 0 }

// StreamInfo mirrors AVGrabberStreamInfo.
// Available after the first PARAM_SET frame has been received.
type StreamInfo struct {
	VideoCodec           uint8
	AudioCodec           uint8
	AudioChannels        uint8  // 1 = mono, 2 = stereo
	FPS                  uint8  // from SPS VUI; 0 if not signalled
	Width                uint16 // pixels; 0 until first SPS
	Height               uint16
	VideoClockRate       uint32 // RTP clock for video, typically 90000 Hz
	AudioSampleRate      uint32 // RTP clock for audio, e.g. 8000 / 48000 Hz
	AudioSamplesPerFrame uint32 // AAC=1024, G.711≈160, Opus=960
}

// Stats mirrors AVGrabberStats.
type Stats struct {
	VideoBitrateKbps uint32
	VideoFPSMilli    uint32 // fps × 1000
	VideoFrameTotal  uint64
	VideoBytesTotal  uint64
	AudioBitrateKbps uint32
	AudioFrameTotal  uint64
	AudioBytesTotal  uint64
	ElapsedMS        uint64
	Discontinuities  uint32
}

// Config is passed to Open to open an RTSP session.
type Config struct {
	URL              string
	Username         string
	Password         string
	Protocol         int32 // ProtoTCP or ProtoUDP
	Multicast        bool
	Audio            bool
	ConnectTimeoutMS int32 // 0 = library default (5000 ms)
	FrameQueueDepth  int32 // 0 = library default (60 frames)
}
