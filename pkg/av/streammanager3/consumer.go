package streammanager3

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
)

type Consumer struct {
	id           string
	muxerFactory av.MuxerFactory
	muxerRemover av.MuxerRemover
	errCh        chan<- error

	cancel         context.CancelFunc
	wg             sync.WaitGroup
	alreadyClosing atomic.Bool
	inactive       atomic.Bool
	writeOnce      sync.Once

	mu               sync.RWMutex
	headers          []av.Stream
	headersErr       error
	headersAvailable chan []av.Stream
	queue            chan av.Packet
}

func NewConsumer(
	consumerID string,
	muxerFactory av.MuxerFactory,
	muxerRemover av.MuxerRemover,
	errCh chan<- error,
) *Consumer {
	m := &Consumer{
		id:               consumerID,
		muxerFactory:     muxerFactory,
		muxerRemover:     muxerRemover,
		errCh:            errCh,
		headersAvailable: make(chan []av.Stream, 1),
		queue:            make(chan av.Packet, 50),
	}

	return m
}

func (m *Consumer) Start(ctx context.Context) error {
	sctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	if m.alreadyClosing.Load() {
		// Close was called before Start; discard the context and do not
		// increment the WaitGroup — Close may have already returned from Wait.
		m.mu.Unlock()
		cancel()

		return nil
	}

	m.cancel = cancel
	m.mu.Unlock()
	m.wg.Go(func() {
		defer cancel()
		defer func() {
			if m.muxerRemover != nil {
				ctxDetached := context.WithoutCancel(sctx)

				ctxTimeout, cancel := context.WithTimeout(ctxDetached, 5*time.Second)
				defer cancel()

				_ = m.muxerRemover(ctxTimeout, m.id)
			}
		}()
		defer m.inactive.Store(true)

		select {
		case <-sctx.Done():
			m.setLastError(sctx.Err())

			return
		case _, ok := <-m.headersAvailable:
			if !ok {
				return
			}

			muxer, err := m.muxerFactory(sctx, m.id)
			if err != nil {
				m.setLastError(errors.Join(ErrConsumerMuxFactory, err))

				return
			}

			defer func() {
				ctxDetached := context.WithoutCancel(sctx)

				ctxTimeout, cancel := context.WithTimeout(ctxDetached, 5*time.Second)
				defer cancel()

				_ = muxer.WriteTrailer(ctxTimeout, nil)
				_ = muxer.Close()
			}()

			m.mu.RLock()
			streams := m.headers
			m.mu.RUnlock()

			if err := muxer.WriteHeader(sctx, streams); err != nil {
				m.setLastError(errors.Join(ErrMuxerWriteHeader, err))

				return
			}

			for {
				select {
				case <-sctx.Done():
					return
				case pkt, ok := <-m.queue:
					if !ok {
						return
					}

					if pkt.NewCodecs != nil {
						if cc, ok := muxer.(av.CodecChanger); ok {
							if err := cc.WriteCodecChange(sctx, pkt.NewCodecs); err != nil {
								m.setLastError(errors.Join(ErrMuxerWriteCodecChange, err))

								return
							}
						}
					}

					if err := muxer.WritePacket(sctx, pkt); err != nil {
						m.setLastError(errors.Join(ErrMuxerWritePacket, err))

						return
					}
				}
			}
		}
	})

	return nil
}

func (m *Consumer) Close() error {
	if !m.alreadyClosing.CompareAndSwap(false, true) {
		return nil
	}

	m.inactive.Store(true)
	// Lock (not RLock) to synchronise with Start: either Start's wg.Add(1)
	// completes before we reach Wait, or Start sees alreadyClosing=true under
	// this same lock and does not call Add at all.
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	m.wg.Wait()

	return nil
}

func (m *Consumer) WriteHeader(ctx context.Context, streams []av.Stream) error {
	m.writeOnce.Do(func() {
		defer close(m.headersAvailable)

		if len(streams) == 0 {
			m.setLastError(ErrCodecsNotAvailable)

			return
		}

		_ = m.WriteCodecChange(ctx, streams)
		select {
		case <-ctx.Done():
		case m.headersAvailable <- streams:
		}
	})

	return m.LastError()
}

func (m *Consumer) WriteCodecChange(_ context.Context, changed []av.Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.headers = changed

	return nil
}

func (m *Consumer) WritePacket(ctx context.Context, pkt av.Packet) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.queue <- pkt:
	}

	return nil
}

func (m *Consumer) WritePacketLeaky(ctx context.Context, pkt av.Packet) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.queue <- pkt:
	default:
		return ErrDroppingPacket
	}

	return nil
}

func (m *Consumer) WriteTrailer(_ context.Context, _ error) error {
	return nil
}

func (m *Consumer) setLastError(err error) {
	if err == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.headersErr = err
	if m.errCh == nil {
		return
	}

	select {
	case m.errCh <- err:
	default:
	}

	m.inactive.Store(true)
}

func (m *Consumer) LastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.headersErr
}

func (m *Consumer) Inactive() bool {
	return m.inactive.Load()
}
