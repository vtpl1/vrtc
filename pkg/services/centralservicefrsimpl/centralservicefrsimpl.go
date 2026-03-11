package centralservicefrsimpl

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	centralservicefrs "github.com/vtpl1/vrtc/gen/central_service_frs"
	"github.com/vtpl1/vrtc/gen/data_models"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

type CentralServicefFrsImpl struct {
	centralservicefrs.UnimplementedCentralServiceServer

	closed    chan struct{}
	closeOnce sync.Once
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
	defer log.Info().Msg("request finished")

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
