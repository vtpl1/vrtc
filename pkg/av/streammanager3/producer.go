package streammanager3

import (
	"context"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
)

type Producer struct {
	id             string
	demuxerFactory av.DemuxerFactory
	demuxerRemover av.DemuxerRemover

	cancel           context.CancelFunc
	wg               sync.WaitGroup
	mu               sync.RWMutex
	alreadyClosing   atomic.Bool
	started          atomic.Bool
	consumers        map[string]*Consumer
	consumersToStart chan *Consumer

	demuxer          av.DemuxCloser
	headers          []av.Stream
	headersErr       error
	headersAvailable chan struct{}
}

func NewProducer(
	producerID string,
	demuxerFactory av.DemuxerFactory,
	demuxerRemover av.DemuxerRemover,
) *Producer {
	m := &Producer{
		id:               producerID,
		demuxerFactory:   demuxerFactory,
		demuxerRemover:   demuxerRemover,
		consumersToStart: make(chan *Consumer),
		headersAvailable: make(chan struct{}),
		consumers:        make(map[string]*Consumer),
	}

	return m
}

func (m *Producer) Start(ctx context.Context) error {
	if !m.started.CompareAndSwap(false, true) {
		return ErrProducesAlreadyStarted
	}

	sctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.cancel = cancel
	m.wg.Add(1)
	m.mu.Unlock()

	go func(ctx context.Context, cancel context.CancelFunc) {
		defer m.wg.Done()
		defer cancel()

		demuxer, err := m.demuxerFactory(ctx, m.id)
		if err != nil {
			m.setLastCodecError(errors.Join(ErrProducerDemuxFactory, err))

			return
		}

		m.mu.Lock()
		m.demuxer = demuxer

		m.mu.Unlock()
		defer m.demuxer.Close()
		defer func(ctx context.Context) {
			if m.demuxerRemover != nil {
				ctxDetached := context.WithoutCancel(ctx)

				ctxTimeout, cancel := context.WithTimeout(ctxDetached, 5*time.Second)
				defer cancel()

				_ = m.demuxerRemover(ctxTimeout, m.id)
			}
		}(ctx)
		defer func() {
			m.mu.RLock()

			inactive := make(map[string]*Consumer, len(m.consumers))
			maps.Copy(inactive, m.consumers)

			m.mu.RUnlock()

			for _, c := range inactive {
				_ = c.Close()
			}

			m.mu.Lock()
			for consumerID := range m.consumers {
				delete(m.consumers, consumerID)
			}
			m.mu.Unlock()
		}()

		streams, err := m.demuxer.GetCodecs(ctx)
		if err != nil {
			m.setLastCodecError(err)

			return
		}

		m.mu.Lock()

		m.headers = streams
		select {
		case <-m.headersAvailable:
			// already closed
		default:
			close(m.headersAvailable)
		}
		m.mu.Unlock()

		m.wg.Go(func() {
			m.readWriteLoop(ctx)
		})

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case c, ok := <-m.consumersToStart:
				if !ok {
					continue
				}

				m.mu.RLock()
				c1, exists := m.consumers[c.id] // cross check if the consumer still exists
				m.mu.RUnlock()

				if !exists {
					continue
				}

				_ = c1.Start(ctx)
			case <-ticker.C:
				m.mu.RLock()

				inactive := make(map[string]*Consumer, len(m.consumers))
				for consumerID, c := range m.consumers {
					if !c.Inactive() {
						continue
					}

					inactive[consumerID] = c
				}

				m.mu.RUnlock()

				for _, c := range inactive {
					_ = c.Close()
				}

				m.mu.Lock()
				for consumerID := range inactive {
					delete(m.consumers, consumerID)
				}
				m.mu.Unlock()

			case <-ctx.Done():
				return
			}
		}
	}(sctx, cancel)

	return nil
}

func (m *Producer) Close() error {
	if !m.alreadyClosing.CompareAndSwap(false, true) {
		return nil
	}

	if m.cancel != nil {
		m.cancel()
	}

	m.wg.Wait()

	return nil
}

func (m *Producer) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.headersAvailable:
		return m.headers, m.headersErr
	}
}

func (m *Producer) ReadPacket(ctx context.Context) (av.Packet, error) {
	return m.demuxer.ReadPacket(ctx)
}

func (m *Producer) ConsumerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.consumers)
}

func (m *Producer) readWriteLoop(ctx context.Context) {
	fpsLimitTicker := time.NewTicker(time.Second / time.Duration(maxFps))
	defer fpsLimitTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-fpsLimitTicker.C:
			pkt, err := m.ReadPacket(ctx)
			if err != nil {
				if m.cancel != nil {
					m.cancel()
				}

				return
			}

			if pkt.NewCodecs != nil {
				m.mu.Lock()
				m.headers = pkt.NewCodecs
				m.mu.Unlock()
			}

			m.mu.RLock()

			active := make(map[string]*Consumer, len(m.consumers))
			for consumerID, c := range m.consumers {
				if c.LastError() != nil {
					continue
				}

				if c.Inactive() {
					continue
				}

				active[consumerID] = c
			}

			m.mu.RUnlock()
			// Delivery policy:
			//   1 consumer  → blocking write (WritePacket).
			//     Back-pressure propagates up to ReadPacket, so no packets are
			//     dropped as long as the single consumer can keep up.
			//   2+ consumers → leaky write (WritePacketLeaky).
			//     A slow or stalled consumer does not block the others; it simply
			//     misses frames that do not fit in its queue.
			if len(active) == 1 {
				for _, c := range active {
					_ = c.WritePacket(ctx, pkt)
				}

				continue
			}

			for _, c := range active {
				_ = c.WritePacketLeaky(ctx, pkt)
			}
		}
	}
}

func (m *Producer) setLastCodecError(err error) {
	if err == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.headersErr = err
	select {
	case <-m.headersAvailable:
		// already closed
	default:
		close(m.headersAvailable)
	}
}

func (m *Producer) LastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.headersErr
}

func (m *Producer) AddConsumer(
	ctx context.Context,
	consumerID string,
	muxerFactory av.MuxerFactory,
	muxerRemover av.MuxerRemover,
	errChan chan<- error,
) error {
	if m.alreadyClosing.Load() {
		return ErrProducerClosing
	}

	if !m.started.Load() {
		return ErrProducerNotStartedYet
	}

	if err := m.LastError(); err != nil {
		return err
	}

	m.mu.Lock()

	_, existed := m.consumers[consumerID]
	if existed {
		m.mu.Unlock()

		return ErrConsumerAlreadyExists
	}

	c := NewConsumer(consumerID, muxerFactory, muxerRemover, errChan)
	m.consumers[consumerID] = c
	m.mu.Unlock()

	streams, err := m.GetCodecs(ctx)
	if err != nil {
		m.mu.Lock()
		delete(m.consumers, consumerID)
		m.mu.Unlock()
		c.setLastError(errors.Join(ErrCodecsNotAvailable, err))

		return err
	}

	select {
	case <-ctx.Done():
		c.inactive.Store(true)

		return ctx.Err()
	case m.consumersToStart <- c:
	}

	return c.WriteHeader(ctx, streams)
}

func (m *Producer) RemoveConsumer(_ context.Context, consumerID string) error {
	m.mu.RLock()
	consumer, exists := m.consumers[consumerID]
	m.mu.RUnlock()

	if exists {
		_ = consumer.Close()
	}

	return nil
}

func (m *Producer) Pause(ctx context.Context) error {
	if m.alreadyClosing.Load() {
		return ErrProducerClosing
	}

	if !m.started.Load() {
		return ErrProducerNotStartedYet
	}

	m.mu.RLock()
	dmx := m.demuxer
	m.mu.RUnlock()

	if pauser, ok := dmx.(av.Pauser); ok {
		return pauser.Pause(ctx)
	}

	return nil
}

func (m *Producer) Resume(ctx context.Context) error {
	if m.alreadyClosing.Load() {
		return ErrProducerClosing
	}

	if !m.started.Load() {
		return ErrProducerNotStartedYet
	}

	m.mu.RLock()
	dmx := m.demuxer
	m.mu.RUnlock()

	if pauser, ok := dmx.(av.Pauser); ok {
		return pauser.Resume(ctx)
	}

	return nil
}
