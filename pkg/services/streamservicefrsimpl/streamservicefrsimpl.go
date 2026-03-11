package streamservicefrsimpl

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/rs/zerolog/log"
	streamservicefrs "github.com/vtpl1/vrtc/gen/stream_service_frs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

type StreamServicefFrsImpl struct {
	streamservicefrs.UnimplementedStreamServiceServer

	closed    chan struct{}
	closeOnce sync.Once
}

func New() (*StreamServicefFrsImpl, error) {
	m := &StreamServicefFrsImpl{
		closed: make(chan struct{}),
	}

	return m, nil
}

func (m *StreamServicefFrsImpl) WriteFramePva(
	stream grpc.ClientStreamingServer[streamservicefrs.WriteFramePvaRequest, streamservicefrs.WriteFramePvaResponse],
) error {
	peerAddr := "unavailable"
	if p, ok := peer.FromContext(stream.Context()); ok {
		peerAddr = p.Addr.String()
	}

	log := log.With().
		Str("peer-addr", peerAddr).
		Str("rpc", "rpc(WriteFramePva)").Logger()

	log.Info().Msg("request")
	defer log.Info().Msg("request finished")

	var nodeID, engineID string

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		if nodeID == "" {
			nodeID = req.GetNodeId()
		}

		if engineID == "" && req.GetEngineId() != "" {
			engineID = req.GetEngineId()
		}

		framePva := req.GetFramePva()
		if framePva == nil {
			return errors.New("GetFramePva error") //nolint:err113
		}

		select {
		case <-m.closed:
			return nil
		default:
			fmt.Println(
				framePva.GetFrame(),
				framePva.GetPva(),
				framePva.GetStatus(),
			) //nolint:forbidigo

			continue
		}
	}

	return nil
}

func (m *StreamServicefFrsImpl) Close() error {
	m.closeOnce.Do(func() {
		close(m.closed)
	})

	return nil
}
