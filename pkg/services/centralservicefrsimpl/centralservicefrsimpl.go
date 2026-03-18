package centralservicefrsimpl

import (
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	centralservicefrs "github.com/vtpl1/vrtc/gen/central_service_frs"
	"github.com/vtpl1/vrtc/gen/data_models"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/avf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

type CentralServicefFrsImpl struct {
	centralservicefrs.UnimplementedCentralServiceServer

	closed    chan struct{}
	closeOnce sync.Once

	mu      sync.RWMutex
	sources map[string]*Source
	filters map[string]map[string]avf.Filter
}

type Source struct {
	id               string
	mu               sync.RWMutex
	consumersChanged chan struct{}
}

func New() (*CentralServicefFrsImpl, error) {
	m := &CentralServicefFrsImpl{
		closed: make(chan struct{}),
	}

	return m, nil
}

func (m *CentralServicefFrsImpl) ReadEngines(
	req *centralservicefrs.ReadEnginesRequest,
	stream grpc.ServerStreamingServer[centralservicefrs.ReadEnginesResponse],
) error {
	peerAddr := "unavailable"
	if p, ok := peer.FromContext(stream.Context()); ok {
		peerAddr = p.Addr.String()
	}

	log := log.With().
		Str("peer-addr", peerAddr).
		Str("identifier", req.GetNodeId()).
		Str("rpc", "rpc(ReadEngines)").Logger()

	log.Info().Msg("request")

	var nodeID string

	defer func() {
		if len(nodeID) == 0 {
			return
		}

		m.mu.Lock()

		s, ok := m.sources[nodeID]
		if ok {
			delete(m.sources, s.id)
		}
		m.mu.Unlock()

		log.Info().Msg("request finished")
	}()

	sendUpdate := func() error {
		if err := stream.Send(
			&centralservicefrs.ReadEnginesResponse{
				Err: &data_models.Error{
					Code:    0,
					Message: "Success",
				},
			},
		); err != nil {
			log.Error().Err(err).Msg("send error")

			return err
		}

		return nil
	}

	sendError := func(err error) error {
		if err := stream.Send(
			&centralservicefrs.ReadEnginesResponse{
				Err: &data_models.Error{
					Code:    1,
					Message: err.Error(),
				},
			},
		); err != nil {
			log.Error().Err(err).Msg("send error")

			return err
		}

		return nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.closed:
			_ = sendError(grpc.ErrServerStopped)

			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			ticker.Reset(30 * time.Second)

			return sendUpdate()
		}
	}
}

func (m *CentralServicefFrsImpl) Close() error {
	m.closeOnce.Do(func() {
		close(m.closed)
	})

	return nil
}

func (m *CentralServicefFrsImpl) GetDemuxCloser(
	sourceID, producerID string,
) (av.DemuxCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if f, ok := m.filters[sourceID]; ok {
		if f, ok := f[producerID]; ok {
			return f, nil
		}
	}

	return nil, errors.New("fff")
}

func (m *CentralServicefFrsImpl) RemoveDemuxCloser(sourceID, producerID string) error {
	return nil
}

func (m *CentralServicefFrsImpl) GetAVFMuxCloser(
	sourceID, producerID string,
) (avf.FrameMuxCloser, error) {
	return nil, nil
}

func (m *CentralServicefFrsImpl) RemoveAVFMuxCloser(sourceID, producerID string) error {
	return nil
}
