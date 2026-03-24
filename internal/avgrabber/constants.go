package avgrabber

// Frame type values — AVGrabberFrameHeader.frame_type.
const (
	FrameTypeParamSet = 0   // PARAM_SET: SPS+PPS or VPS+SPS+PPS in Annex-B
	FrameTypeKey      = 1   // KEY: IDR / random-access keyframe in Annex-B
	FrameTypeDelta    = 2   // DELTA: non-key video frame in Annex-B
	FrameTypeAudio    = 16  // AUDIO: raw encoded audio payload
	FrameTypeUnknown  = 255 // UNKNOWN: unclassified; skip silently
)

// Media type values — AVGrabberFrameHeader.media_type.
const (
	MediaVideo = 0 // AVGRABBER_MEDIA_VIDEO
	MediaAudio = 1 // AVGRABBER_MEDIA_AUDIO
)

// Codec type values — AVGrabberFrameHeader.codec_type.
const (
	CodecMJPEG   = 0
	CodecMPEG    = 1
	CodecH264    = 2
	CodecG711U   = 3 // G.711 µ-law, 8 kHz mono
	CodecG711A   = 4 // G.711 A-law, 8 kHz mono
	CodecL16     = 5 // Linear PCM 16-bit
	CodecAAC     = 6 // AAC, ADTS-framed
	CodecUnknown = 7
	CodecH265    = 8
	CodecG722    = 9  // G.722 ADPCM, 16 kHz audio bandwidth
	CodecG726    = 10 // G.726 ADPCM, 8 kHz
	CodecOpus    = 11 // Opus, 48 kHz RTP clock
)

// Transport protocol values — AVGrabberConfig.protocol.
const (
	ProtoUDP = 0
	ProtoTCP = 1
)

// Flag bit values — AVGrabberFrameHeader.flags bitmask.
const (
	FlagNTPSynced     = 0x01 // ntp_ms is RTCP-anchored camera wall-clock
	FlagDiscontinuity = 0x02 // clock reset or gap > 1 s preceded this frame
	FlagKeyframe      = 0x04 // random-access video frame
	FlagHasSEI        = 0x10 // SEI NAL units present in this access unit
)

// Status codes returned by the C library.
const (
	StatusOK       = 0
	ErrNullPointer = 3
	ErrNotReady    = 10 // normal timeout — caller should loop
	ErrStopped     = 18
	ErrInvalidArg  = 37
	ErrAuthFailed  = 1101
)
