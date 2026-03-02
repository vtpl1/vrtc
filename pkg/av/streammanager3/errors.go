package streammanager3

import "errors"

var (
	ErrProducerNotFound            = errors.New("producer not found")
	ErrProducerDemuxFactory        = errors.New("producer demux factory")
	ErrConsumerMuxFactory          = errors.New("consumer mux factory")
	ErrStreamManagerClosing        = errors.New("stream manager closing")
	ErrProducerClosing             = errors.New("producer closing")
	ErrProducerLastError           = errors.New("producer last error")
	ErrConsumerAlreadyExists       = errors.New("consumer already exists")
	ErrCodecsNotAvailable          = errors.New("codecs not available")
	ErrDroppingPacket              = errors.New("dropping packet")
	ErrStreamManagerNotStartedYet  = errors.New("stream manager not started yet")
	ErrProducerNotStartedYet       = errors.New("producer not started yet")
	ErrMuxerWritePacket            = errors.New("muxer write packet")
	ErrMuxerWriteHeader            = errors.New("muxer write header")
	ErrMuxerWriteCodecChange       = errors.New("muxer write codec change")
	ErrStreamManagerAlreadyStarted = errors.New("stream manager already started")
	ErrProducesAlreadyStarted      = errors.New("producer already started")
)
