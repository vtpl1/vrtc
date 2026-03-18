package avf

const (
	// frameHeaderSize: magic(4)+refFrameOff(8)+MediaType(4)+FrameType(4)+Timestamp(8)+frameSize(4).
	frameHeaderSize = 32
	// frameTrailerSize: currentFrameOff(8).
	frameTrailerSize = 8
	// maxFrameSize is the maximum allowed payload (3 MB).
	maxFrameSize = 3 * 1024 * 1024
	// videoProbeSize is the maximum number of frames to scan for a video codec, during this scan also scan for audio codec.
	videoProbeSize = 200 * 50
	// audioProbeSize is the maximum number of frames to scan for a audio codec, if the audio codec is not found during the video codec scan cycle.
	audioProbeSize = 4 * 50
	// readBufferSize is the internal bufio.Reader buffer size (2 MB).
	readBufferSize = 2 * 1024 * 1024
)
