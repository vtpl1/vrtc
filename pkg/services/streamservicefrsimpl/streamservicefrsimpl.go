package streamservicefrsimpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	streamservicefrs "github.com/vtpl1/vrtc/gen/stream_service_frs"
	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

type StreamServicefFrsImpl struct {
	streamservicefrs.UnimplementedStreamServiceServer

	muxerFactory av.AVFFrameMuxerFactory
	muxerRemover av.AVFFrameMuxerRemover

	closed    chan struct{}
	closeOnce sync.Once
}

func New(
	muxerFactory av.AVFFrameMuxerFactory,
	muxerRemover av.AVFFrameMuxerRemover,
) (*StreamServicefFrsImpl, error) {
	m := &StreamServicefFrsImpl{
		muxerFactory: muxerFactory,
		muxerRemover: muxerRemover,
		closed:       make(chan struct{}),
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

	ctx := stream.Context()

	var (
		nodeID, engineID, producerID string
		muxCloser                    av.AVFFrameMuxCloser
	)

	defer func() {
		if utils.IsNilInterface(muxCloser) {
			if err := muxCloser.Close(); err != nil {
				log.Error().Err(err).Msg("muxCloser error")
			}
		}

		if m.muxerRemover != nil {
			ctxDetached := context.WithoutCancel(ctx)

			ctxTimeOut, cancel := context.WithTimeout(ctxDetached, 5*time.Second)
			defer cancel()

			_ = m.muxerRemover(ctxTimeOut, nodeID, producerID)
		}

		log.Info().Msg("request finished")
	}()

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

		if utils.IsNilInterface(muxCloser) {
			producerID = nodeID + "_" + engineID

			mux, err := m.muxerFactory(ctx, nodeID, producerID)
			if err != nil {
				break
			}

			muxCloser = mux
		}

		framePva := req.GetFramePva()
		if framePva == nil {
			return errors.New("GetFramePva error") //nolint:err113
		}

		select {
		case <-m.closed:
			return nil
		default:
			frame := framePva.GetFrame()
			pva := framePva.GetPva()
			pavaData, _ := json.Marshal(pva)
			muxCloser.WriteFrame(ctx,
				av.AVFFrame{
					MediaType: uint32(frame.GetMediaType()),
					FrameType: uint32(frame.GetFrameType()),
					Timestamp: frame.GetTimestamp(),
					FrameID:   frame.GetFrameId(),
					Data:      frame.GetBuffer(),
					ExtraData: pavaData,
				},
			)

			fmt.Println( //nolint:forbidigo
				framePva.GetFrame(),
				framePva.GetPva(),
				framePva.GetStatus(),
			)

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
