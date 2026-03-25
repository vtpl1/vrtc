package streammanager3

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
)

// consumerSeq is a process-wide monotonic counter used to generate unique
// consumer IDs when the caller does not supply one.
var consumerSeq atomic.Uint64

type StreamManager struct {
	demuxerFactory av.DemuxerFactory
	demuxerRemover av.DemuxerRemover

	cancel         context.CancelFunc
	wg             sync.WaitGroup
	mu             sync.RWMutex
	alreadyClosing atomic.Bool
	started        atomic.Bool
	producers      map[string]*Producer

	producersToStart chan *Producer
}

type consumerHandle struct {
	manager    *StreamManager
	producerID string
	consumerID string
	closed     atomic.Bool
}

func New(
	demuxerFactory av.DemuxerFactory,
	demuxerRemover av.DemuxerRemover,
) *StreamManager {
	m := &StreamManager{
		demuxerFactory:   demuxerFactory,
		demuxerRemover:   demuxerRemover,
		producers:        make(map[string]*Producer),
		producersToStart: make(chan *Producer, 10),
	}

	return m
}

func (h *consumerHandle) ID() string {
	return h.consumerID
}

func (h *consumerHandle) Close(ctx context.Context) error {
	if !h.closed.CompareAndSwap(false, true) {
		return nil
	}

	err := h.manager.removeConsumer(ctx, h.producerID, h.consumerID)
	if errors.Is(err, ErrProducerNotFound) {
		return nil
	}

	return err
}

func (m *StreamManager) Consume(
	ctx context.Context,
	producerID string,
	opts av.ConsumeOptions,
) (av.ConsumerHandle, error) {
	if opts.ConsumerID == "" {
		opts.ConsumerID = fmt.Sprintf("consumer-%d", consumerSeq.Add(1))
	}

	if m.alreadyClosing.Load() {
		return nil, ErrStreamManagerClosing
	}

	if !m.started.Load() {
		return nil, ErrStreamManagerNotStartedYet
	}

	for {
		m.mu.Lock()

		p, existed := m.producers[producerID]
		if !existed {
			p = NewProducer(producerID, m.demuxerFactory, m.demuxerRemover)
			m.producers[producerID] = p
		}
		m.mu.Unlock()

		if !existed {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case m.producersToStart <- p:
			}
		}

		if err := p.LastError(); err != nil {
			return nil, fmt.Errorf(
				"producerID: %s:\n%w",
				producerID,
				errors.Join(ErrProducerLastError, err),
			)
		}

		if err := p.AddConsumer(
			ctx,
			opts.ConsumerID,
			opts.MuxerFactory,
			opts.MuxerRemover,
			opts.ErrChan,
		); err != nil {
			if errors.Is(err, ErrProducerClosing) || errors.Is(err, ErrProducerNotStartedYet) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}

				continue
			}

			return nil, fmt.Errorf("%s: %w", producerID, err)
		}

		return &consumerHandle{
			manager:    m,
			producerID: producerID,
			consumerID: opts.ConsumerID,
		}, nil
	}
}

func (m *StreamManager) removeConsumer(
	ctx context.Context,
	producerID string,
	consumerID string,
) error {
	m.mu.RLock()
	p, ok := m.producers[producerID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%s: %w", producerID, ErrProducerNotFound)
	}

	return p.RemoveConsumer(ctx, consumerID)
}

func (m *StreamManager) GetActiveProducersCount(_ context.Context) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.producers)
}

func (m *StreamManager) PauseProducer(ctx context.Context, producerID string) error {
	m.mu.RLock()
	p, ok := m.producers[producerID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%s: %w", producerID, ErrProducerNotFound)
	}

	return p.Pause(ctx)
}

func (m *StreamManager) ResumeProducer(ctx context.Context, producerID string) error {
	m.mu.RLock()
	p, ok := m.producers[producerID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%s: %w", producerID, ErrProducerNotFound)
	}

	return p.Resume(ctx)
}

func (m *StreamManager) Start(ctx context.Context) error {
	if !m.started.CompareAndSwap(false, true) {
		return ErrStreamManagerAlreadyStarted
	}

	sctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	m.wg.Go(func() {
		defer cancel()
		defer func() {
			m.mu.RLock()

			inactive := make(map[string]*Producer, len(m.producers))
			maps.Copy(inactive, m.producers)

			m.mu.RUnlock()

			for _, p := range inactive {
				_ = p.Close()
			}

			m.mu.Lock()
			for producerID := range m.producers {
				delete(m.producers, producerID)
			}
			m.mu.Unlock()
		}()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.mu.RLock()

				inactive := make(map[string]*Producer, len(m.producers))
				for producerID, p := range m.producers {
					if p.ConsumerCount() == 0 {
						inactive[producerID] = p
					}
				}

				m.mu.RUnlock()

				for _, p := range inactive {
					_ = p.Close()
				}

				m.mu.Lock()
				for producerID := range inactive {
					delete(m.producers, producerID)
				}
				m.mu.Unlock()
			case <-sctx.Done():
				return
			case p, ok := <-m.producersToStart:
				if ok {
					err := p.Start(sctx)
					if err != nil {
						m.mu.Lock()
						delete(m.producers, p.id)
						m.mu.Unlock()
					}
				}
			}
		}
	})

	return nil
}

func (m *StreamManager) SignalStop() bool {
	if !m.alreadyClosing.CompareAndSwap(false, true) {
		return false
	}

	m.mu.RLock()
	cancel := m.cancel
	m.mu.RUnlock()

	if cancel != nil {
		cancel()
	}

	return true
}

func (m *StreamManager) WaitStop() error {
	m.wg.Wait()

	return nil
}

func (m *StreamManager) Stop() error {
	if !m.SignalStop() {
		return nil
	}

	return m.WaitStop()
}
