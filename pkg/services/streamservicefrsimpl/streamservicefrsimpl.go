package streamservicefrsimpl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	streamservicefrs "github.com/vtpl1/vrtc/gen/stream_service_frs"
	"github.com/vtpl1/vrtc/pkg/avf"
	"github.com/vtpl1/vrtc/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

var errServerSendStatus = errors.New("server send status")

type StreamServicefFrsImpl struct {
	streamservicefrs.UnimplementedStreamServiceServer

	muxerFactory avf.FrameMuxerFactory
	muxerRemover avf.AVFFrameMuxerRemover

	closed    chan struct{}
	closeOnce sync.Once
}

func New(
	muxerFactory avf.FrameMuxerFactory,
	muxerRemover avf.AVFFrameMuxerRemover,
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
		muxCloser                    avf.FrameMuxCloser
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

		select {
		case <-m.closed:
			return nil
		default:
			framePva := req.GetFramePva()
			if framePva == nil {
				return errors.New("GetFramePva error") //nolint:err113
			}

			frame := framePva.GetFrame()
			pva := framePva.GetPva()

			status1 := framePva.GetStatus()
			if status1.GetState() < 0 {
				err := fmt.Errorf(
					"%w %d, %s",
					errServerSendStatus,
					status1.GetState(),
					status1.GetStateMessage(),
				)

				return err
			}
			// fmt.Printf("recv: [id:%v, codec:%v, nalU:%v, ts:%v, size:%v, pvaObjs:%v]\n",
			// 	frame.GetFrameId(), frame.GetMediaType(), frame.GetFrameType(), frame.GetTimestamp(), frame.GetBufferSize(), len(pva.GetObjectList()))

			if frame.GetFrameType() == 0 {
				continue
			}

			var objList []avf.ObjectInfo
			for _, v := range pva.GetObjectList() {
				objList = append(objList, avf.ObjectInfo{
					X: v.GetX(),
					Y: v.GetY(),
					W: v.GetW(),
					H: v.GetH(),
					T: v.GetT(),
					C: v.GetC(),
					I: v.GetI(),
				})
			}

			pvaData := avf.PVAData{
				FrameID:          pva.GetFrameId(),
				StartTimestamp:   pva.GetTimeStamp(),
				EndTimestamp:     pva.GetTimeStampEnd(),
				EncodedTimestamp: pva.GetTimeStampEncoded(),
				VehicleCount:     pva.GetVehicleCount(),
				PeopleCount:      pva.GetPeopleCount(),
				RefWidth:         pva.GetRefWidth(),
				RefHeight:        pva.GetRefHeight(),
				ObjectList:       objList,
			}

			frameInfo := avf.Frame{
				BasicFrame: avf.BasicFrame{
					MediaType: avf.MediaType(frame.GetMediaType()),
					FrameType: avf.FrameType(frame.GetFrameType()),
					TimeStamp: frame.GetTimestamp(),
				},
				Bitrate:         frame.GetBitrate(),
				Fps:             frame.GetFps(),
				MotionAvailable: int8(frame.GetMotionAvailable()),
				FrameID:         frame.GetFrameId(),
				Data:            frame.GetBuffer(),
				Pvadata:         pvaData,
			}

			muxCloser.WriteFrame(ctx,
				frameInfo,
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
