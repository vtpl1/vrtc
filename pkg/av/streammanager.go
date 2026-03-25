package av

import (
	"context"
	"time"

	"github.com/vtpl1/vrtc/pkg/lifecycle"
)

// ProducerStats is a point-in-time snapshot of a single producer's metrics.
type ProducerStats struct {
	ID             string    `json:"id"`
	ConsumerCount  int       `json:"consumerCount"`
	PacketsRead    uint64    `json:"packetsRead"`
	BytesRead      uint64    `json:"bytesRead"`
	KeyFrames      uint64    `json:"keyFrames"`
	DroppedPackets uint64    `json:"droppedPackets"`
	StartedAt      time.Time `json:"startedAt"`
	LastPacketAt   time.Time `json:"lastPacketAt"`
	LastError      string    `json:"lastError,omitempty"`
}

// ConsumeOptions configures a consumer attachment to a producer.
type ConsumeOptions struct {
	ConsumerID   string
	MuxerFactory MuxerFactory
	MuxerRemover MuxerRemover
	ErrChan      chan<- error
}

// ConsumerHandle represents an attached consumer.
//
// Close detaches the consumer from its producer and closes the underlying
// muxer. Close is safe to call multiple times.
type ConsumerHandle interface {
	ID() string
	Close(ctx context.Context) error
}

// StreamManager coordinates a set of producers (demuxers) and consumers (muxers).
// A single StreamManager may host multiple producers; each producer can serve
// multiple consumers simultaneously.
//
// # Lifecycle
//
// A StreamManager must be started before consumers can be attached:
//
//	sm := streammanager3.New(demuxerFactory, demuxerRemover)
//	if err := sm.Start(ctx); err != nil { /* handle */ }
//	defer sm.Stop()
//
//	handle, err := sm.Consume(ctx, "camera-1", av.ConsumeOptions{
//		ConsumerID:   "recorder-a",
//		MuxerFactory: muxFactory,
//		MuxerRemover: muxRemover,
//		ErrChan:      errCh,
//	})
//	if err != nil { /* handle */ }
//	defer handle.Close(ctx)
//
// # Producer management
//
// Producers are created on-demand: the first Consume call for a given
// producerID opens a demuxer via the DemuxerFactory supplied to the
// implementation constructor. A producer remains alive as long as at least
// one consumer is attached; idle producers (zero consumers) are reclaimed
// automatically by a background ticker (within ~1 s).
//
// # Delivery policy
//
// The delivery mode depends on the active consumer count per producer:
//   - 1 consumer  → blocking write: back-pressure propagates to ReadPacket;
//     no packets are dropped as long as the consumer keeps up.
//   - 2+ consumers → leaky write: a slow consumer drops frames rather than
//     stalling the pipeline for the others.
//
// # Concurrency
//
// All methods are safe to call concurrently from multiple goroutines.
type StreamManager interface {
	// GetProducersStats returns a point-in-time snapshot of all active producers.
	GetProducersStats(ctx context.Context) []ProducerStats

	// GetActiveProducersCount returns the number of producers currently managed
	// by this StreamManager. A producer is considered active from the moment its
	// first consumer is registered until all its consumers have been removed and
	// the background cleanup ticker has reclaimed it (within ~1 s).
	GetActiveProducersCount(ctx context.Context) int

	// Consume attaches a new consumer to the named producer and returns a handle
	// that can later detach it. If no producer with producerID exists, one is
	// created automatically using the DemuxerFactory supplied to the constructor.
	//
	// Consume blocks until the producer's initial codec headers are available
	// (i.e. GetCodecs has returned), then delivers a WriteHeader to the muxer.
	// It retries transparently if the producer is still starting or is
	// transiently closing.
	//
	// Errors:
	//   - ErrStreamManagerNotStartedYet  if Start has not been called.
	//   - ErrStreamManagerClosing        if Stop or SignalStop has been called.
	//   - ErrConsumerAlreadyExists       if opts.ConsumerID is already registered
	//     on producerID.
	//   - ErrProducerLastError (wrapped) if the producer's demuxer previously
	//     failed.
	//   - ctx.Err()                      if the context is cancelled while waiting.
	//
	// opts.ErrChan, if non-nil, receives asynchronous write errors from the
	// consumer's muxer (e.g. ErrMuxerWritePacket). The channel should be buffered
	// to avoid blocking the consumer's write loop.
	Consume(ctx context.Context, producerID string, opts ConsumeOptions) (ConsumerHandle, error)

	// PauseProducer requests the named producer's demuxer to suspend packet
	// delivery. The request is forwarded only if the underlying DemuxCloser
	// implements av.Pauser; otherwise PauseProducer is a no-op.
	//
	// Errors:
	//   - ErrProducerNotFound      if no producer with producerID exists.
	//   - ErrProducerClosing       if the producer is shutting down.
	//   - ErrProducerNotStartedYet if the producer goroutine has not yet begun.
	PauseProducer(ctx context.Context, producerID string) error

	// ResumeProducer requests the named producer's demuxer to resume packet
	// delivery after a previous PauseProducer call. Like PauseProducer, it is
	// a no-op when the demuxer does not implement av.Pauser.
	//
	// Errors:
	//   - ErrProducerNotFound      if no producer with producerID exists.
	//   - ErrProducerClosing       if the producer is shutting down.
	//   - ErrProducerNotStartedYet if the producer goroutine has not yet begun.
	ResumeProducer(ctx context.Context, producerID string) error

	// StartStopper embeds the full Start / Stop lifecycle.
	//
	// Start launches the background goroutine that manages producer creation,
	// idle cleanup, and context propagation. It must be called exactly once
	// before Consume; subsequent calls return ErrStreamManagerAlreadyStarted.
	//
	// Stop signals shutdown (cancels the internal context) and blocks until all
	// producers and their consumers have exited cleanly. Calling Stop multiple
	// times is safe; all calls after the first return nil immediately.
	lifecycle.StartStopper
}
