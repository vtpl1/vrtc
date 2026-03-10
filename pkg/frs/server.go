// Package frs implements an av.DemuxCloser backed by a gRPC server that receives
// FramePVA frames via StreamService.WriteFramePva and exposes them as an AV stream.
// It also implements CentralService.ReadEngines for engine discovery.
package frs

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	centralservicefrs "github.com/vtpl1/vrtc/gen/central_service_frs"
	data_models "github.com/vtpl1/vrtc/gen/data_models"
	streamservicefrs "github.com/vtpl1/vrtc/gen/stream_service_frs"
	"github.com/vtpl1/vrtc/pkg/av"
	avcodec "github.com/vtpl1/vrtc/pkg/av/codec"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/mjpeg"
	"github.com/vtpl1/vrtc/pkg/av/codec/parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"google.golang.org/grpc"
)

// Frame media type values matching data_models.Frame.media_type and the AVF format.
const (
	mediaTypeMJPG  = int32(0)
	mediaTypeH264  = int32(2)
	mediaTypeG711U = int32(3)
	mediaTypeG711A = int32(4)
	mediaTypeAAC   = int32(6)
	mediaTypeH265  = int32(8)
	mediaTypeOPUS  = int32(11)
)

// Frame type values matching data_models.Frame.frame_type and the AVF format.
const (
	frameTypeHFrame        = int32(0)
	frameTypeIFrame        = int32(1)
	frameTypePFrame        = int32(2)
	frameTypeConnectHeader = int32(3)
	frameTypeAudio         = int32(16)
)

const (
	frameChanSize = 128
	videoProbSize = 200 * 50
	audioProbSize = 4 * 50
)

// ErrNoCodecFound is returned by GetCodecs when no decodable stream is found.
var ErrNoCodecFound = errors.New("frs: no decodable codec found in stream")

// Server is a gRPC server implementing StreamService and CentralService,
// and also satisfies av.DemuxCloser to expose incoming FramePVA frames as an AV stream.
type Server struct {
	streamservicefrs.UnimplementedStreamServiceServer
	centralservicefrs.UnimplementedCentralServiceServer

	// incoming FramePVA from WriteFramePva RPC
	frameCh chan *data_models.FramePVA

	// codec state (populated by GetCodecs)
	videoCodec av.CodecData
	audioCodec av.CodecData
	videoIdx   uint16
	audioIdx   uint16
	streams    []av.Stream
	probed     bool

	// frames buffered during GetCodecs probe (replayed by ReadPacket)
	rawBuf []*data_models.FramePVA
	rawPos int

	// CONNECT_HEADER accumulation state (used in pvaToPacket for mid-stream changes)
	connectHeader   []byte
	appendingHeader bool

	// pending mid-stream codec change (attached to next emitted packet)
	pendingCodecChange []av.Stream

	// timestamps for duration calculation (milliseconds)
	lastVideoTS int64
	lastAudioTS int64

	// connected engine IDs (for ReadEngines)
	engMu     sync.RWMutex
	engineIDs map[string]struct{}
	engChange chan struct{} // closed and replaced on each engine list change

	grpcServer *grpc.Server
	closed     chan struct{}
	closeOnce  sync.Once
}

// New creates a Server that listens on addr, registers both StreamService and
// CentralService on the gRPC server, and starts serving in a goroutine.
func New(addr string, opts ...grpc.ServerOption) (*Server, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	s := &Server{
		frameCh:   make(chan *data_models.FramePVA, frameChanSize),
		engineIDs: make(map[string]struct{}),
		engChange: make(chan struct{}),
		closed:    make(chan struct{}),
	}

	s.grpcServer = grpc.NewServer(opts...)
	streamservicefrs.RegisterStreamServiceServer(s.grpcServer, s)
	centralservicefrs.RegisterCentralServiceServer(s.grpcServer, s)

	go s.grpcServer.Serve(lis) //nolint:errcheck

	return s, nil
}

// ── StreamServiceServer ────────────────────────────────────────────────────────

// WriteFramePva implements StreamServiceServer.
// It receives a client-streamed sequence of WriteFramePvaRequest messages and
// forwards each FramePVA to the internal frame channel for GetCodecs/ReadPacket.
func (s *Server) WriteFramePva(stream grpc.ClientStreamingServer[streamservicefrs.WriteFramePvaRequest, streamservicefrs.WriteFramePvaResponse]) error {
	var nodeID, engineID string

	for {
		req, err := stream.Recv()
		if err == io.EOF {
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
			s.addEngine(engineID)
		}

		if pva := req.GetFramePva(); pva != nil {
			select {
			case s.frameCh <- pva:
			case <-s.closed:
				return nil
			}
		}
	}

	if engineID != "" {
		s.removeEngine(engineID)
	}

	return stream.SendAndClose(&streamservicefrs.WriteFramePvaResponse{
		NodeId:   nodeID,
		EngineId: engineID,
	})
}

// ── CentralServiceServer ───────────────────────────────────────────────────────

// ReadEngines implements CentralServiceServer.
// It immediately streams the current set of connected engine IDs, then keeps
// streaming updates as engines connect or disconnect, until the client disconnects
// or the server closes.
func (s *Server) ReadEngines(_ *centralservicefrs.ReadEnginesRequest, stream grpc.ServerStreamingServer[centralservicefrs.ReadEnginesResponse]) error {
	for {
		s.engMu.RLock()
		ids := make([]string, 0, len(s.engineIDs))
		for id := range s.engineIDs {
			ids = append(ids, id)
		}
		notify := s.engChange
		s.engMu.RUnlock()

		if err := stream.Send(&centralservicefrs.ReadEnginesResponse{
			EngineIds: ids,
		}); err != nil {
			return err
		}

		select {
		case <-notify:
			// engine list changed — re-send updated list
		case <-s.closed:
			return nil
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// ── av.DemuxCloser ─────────────────────────────────────────────────────────────

// GetCodecs probes incoming FramePVA frames to detect stream codecs and returns
// the initial stream list. Must be called exactly once before ReadPacket.
func (s *Server) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	var connectHeader []byte
	appendingHeader := false

	for len(s.rawBuf) < videoProbSize {
		if s.videoCodec != nil {
			break
		}

		pva, err := s.recvPVA(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		s.rawBuf = append(s.rawBuf, pva)

		frm := pva.GetFrame()
		if frm == nil {
			continue
		}

		switch frm.GetFrameType() {
		case frameTypeConnectHeader:
			connectHeader = append(connectHeader, frm.GetBuffer()...)
			appendingHeader = true
		case frameTypeAudio:
			if s.audioCodec == nil {
				s.audioCodec = parseAudioCodec(frm.GetMediaType(), frm.GetBuffer())
			}
		default:
			if appendingHeader {
				appendingHeader = false
				s.videoCodec = parseVideoCodec(frm.GetMediaType(), connectHeader)
				connectHeader = nil
			} else if frm.GetFrameType() == frameTypeIFrame && frm.GetMediaType() == mediaTypeMJPG {
				s.videoCodec = mjpeg.CodecData{}
			}
		}
	}

	// Short audio probe if audio was not found during video probe.
	for i := 0; s.audioCodec == nil && i < audioProbSize; i++ {
		pva, err := s.recvPVA(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		s.rawBuf = append(s.rawBuf, pva)

		frm := pva.GetFrame()
		if frm != nil && frm.GetFrameType() == frameTypeAudio {
			s.audioCodec = parseAudioCodec(frm.GetMediaType(), frm.GetBuffer())
		}
	}

	if s.videoCodec == nil && s.audioCodec == nil {
		return nil, ErrNoCodecFound
	}

	idx := uint16(0)
	if s.videoCodec != nil {
		s.videoIdx = idx
		s.streams = append(s.streams, av.Stream{Idx: idx, Codec: s.videoCodec})
		idx++
	}
	if s.audioCodec != nil {
		s.audioIdx = idx
		s.streams = append(s.streams, av.Stream{Idx: idx, Codec: s.audioCodec})
	}

	s.probed = true
	return s.streams, nil
}

// ReadPacket returns the next av.Packet. Returns io.EOF when the stream ends.
// A returned Packet with non-nil NewCodecs signals a mid-stream codec change.
func (s *Server) ReadPacket(ctx context.Context) (av.Packet, error) {
	for {
		if ctx.Err() != nil {
			return av.Packet{}, ctx.Err()
		}

		var pva *data_models.FramePVA
		if s.rawPos < len(s.rawBuf) {
			pva = s.rawBuf[s.rawPos]
			s.rawBuf[s.rawPos] = nil // release reference for GC
			s.rawPos++
		} else {
			var err error
			pva, err = s.recvPVA(ctx)
			if err != nil {
				return av.Packet{}, err
			}
		}

		pkt, skip := s.pvaToPacket(pva)
		if skip {
			continue
		}

		if len(s.pendingCodecChange) > 0 {
			pkt.NewCodecs = s.pendingCodecChange
			s.pendingCodecChange = nil
		}

		return pkt, nil
	}
}

// Close shuts down the gRPC server gracefully.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.grpcServer.GracefulStop()
	})
	return nil
}

// ── internal ───────────────────────────────────────────────────────────────────

func (s *Server) recvPVA(ctx context.Context) (*data_models.FramePVA, error) {
	select {
	case pva, ok := <-s.frameCh:
		if !ok {
			return nil, io.EOF
		}
		return pva, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		return nil, io.EOF
	}
}

// pvaToPacket converts a FramePVA into an av.Packet.
// skip=true means the frame should not be emitted (CONNECT_HEADER or unknown type).
func (s *Server) pvaToPacket(pva *data_models.FramePVA) (av.Packet, bool) {
	frm := pva.GetFrame()
	if frm == nil {
		return av.Packet{}, true
	}

	switch frm.GetFrameType() {
	case frameTypeConnectHeader:
		// Accumulate codec parameter bytes; parse when the next video frame arrives.
		if s.probed {
			s.connectHeader = append(s.connectHeader, frm.GetBuffer()...)
			s.appendingHeader = true
		}
		return av.Packet{}, true

	case frameTypeHFrame, frameTypeIFrame, frameTypePFrame:
		if s.appendingHeader {
			s.appendingHeader = false
			if newCodec := parseVideoCodec(frm.GetMediaType(), s.connectHeader); newCodec != nil {
				s.videoCodec = newCodec
				s.rebuildStreams()
				s.pendingCodecChange = s.streams
			}
			s.connectHeader = nil
		}

		if s.videoCodec == nil {
			return av.Packet{}, true
		}

		ts := frm.GetTimestamp()
		if s.lastVideoTS != 0 && ts <= s.lastVideoTS {
			ts = s.lastVideoTS + 1
		}
		var dur time.Duration
		if s.lastVideoTS != 0 {
			dur = time.Duration(ts-s.lastVideoTS) * time.Millisecond
		}
		s.lastVideoTS = ts

		return av.Packet{
			Idx:       s.videoIdx,
			KeyFrame:  frm.GetFrameType() == frameTypeIFrame,
			DTS:       time.Duration(ts) * time.Millisecond,
			Duration:  dur,
			CodecType: s.videoCodec.Type(),
			FrameID:   frm.GetFrameId(),
			Data:      stripVideoPrefix(frm.GetBuffer()),
			Extra:     pva.GetPva(),
		}, false

	case frameTypeAudio:
		if s.audioCodec == nil {
			if c := parseAudioCodec(frm.GetMediaType(), frm.GetBuffer()); c != nil {
				s.audioCodec = c
				s.rebuildStreams()
				s.pendingCodecChange = s.streams
			} else {
				return av.Packet{}, true
			}
		}

		data := frm.GetBuffer()
		// Strip ADTS header from AAC frames when present.
		if frm.GetMediaType() == mediaTypeAAC && len(data) >= 7 &&
			data[0] == 0xFF && data[1]&0xF6 == 0xF0 {
			if _, hdrLen, _, _, err := aacparser.ParseADTSHeader(data); err == nil && hdrLen < len(data) {
				data = data[hdrLen:]
			}
		}

		ts := frm.GetTimestamp()
		var dur time.Duration
		if s.lastAudioTS != 0 && ts > s.lastAudioTS {
			dur = time.Duration(ts-s.lastAudioTS) * time.Millisecond
		}
		s.lastAudioTS = ts

		return av.Packet{
			Idx:       s.audioIdx,
			DTS:       time.Duration(ts) * time.Millisecond,
			Duration:  dur,
			CodecType: s.audioCodec.Type(),
			FrameID:   frm.GetFrameId(),
			Data:      data,
		}, false
	}

	return av.Packet{}, true
}

func (s *Server) rebuildStreams() {
	s.streams = s.streams[:0]
	if s.videoCodec != nil {
		s.streams = append(s.streams, av.Stream{Idx: s.videoIdx, Codec: s.videoCodec})
	}
	if s.audioCodec != nil {
		s.streams = append(s.streams, av.Stream{Idx: s.audioIdx, Codec: s.audioCodec})
	}
}

func (s *Server) addEngine(id string) {
	s.engMu.Lock()
	s.engineIDs[id] = struct{}{}
	old := s.engChange
	s.engChange = make(chan struct{})
	s.engMu.Unlock()
	close(old)
}

func (s *Server) removeEngine(id string) {
	s.engMu.Lock()
	delete(s.engineIDs, id)
	old := s.engChange
	s.engChange = make(chan struct{})
	s.engMu.Unlock()
	close(old)
}

// ── codec helpers ──────────────────────────────────────────────────────────────

// parseVideoCodec builds codec data from Annex-B NALU bytes (from a CONNECT_HEADER frame).
func parseVideoCodec(mediaType int32, data []byte) av.CodecData {
	nalus, _ := parser.SplitNALUs(data)

	switch mediaType {
	case mediaTypeH264:
		var sps, pps []byte
		for _, nalu := range nalus {
			if len(nalu) == 0 {
				continue
			}
			if h264parser.IsSPSNALU(nalu) && sps == nil {
				sps = nalu
			} else if h264parser.IsPPSNALU(nalu) && pps == nil {
				pps = nalu
			}
		}
		if sps != nil && pps != nil {
			if c, err := h264parser.NewCodecDataFromSPSAndPPS(sps, pps); err == nil {
				return c
			}
		}

	case mediaTypeH265:
		var vps, sps, pps []byte
		for _, nalu := range nalus {
			if len(nalu) == 0 {
				continue
			}
			switch {
			case h265parser.IsVPSNALU(nalu) && vps == nil:
				vps = nalu
			case h265parser.IsSPSNALU(nalu) && sps == nil:
				sps = nalu
			case h265parser.IsPPSNALU(nalu) && pps == nil:
				pps = nalu
			}
		}
		if vps != nil && sps != nil && pps != nil {
			if c, err := h265parser.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps); err == nil {
				return c
			}
		}

	case mediaTypeMJPG:
		return mjpeg.CodecData{}
	}

	return nil
}

// parseAudioCodec infers audio codec data from the media type and first frame payload.
func parseAudioCodec(mediaType int32, data []byte) av.CodecData {
	switch mediaType {
	case mediaTypeG711U:
		return pcm.NewPCMMulawCodecData()

	case mediaTypeG711A:
		return pcm.NewPCMAlawCodecData()

	case mediaTypeOPUS:
		return avcodec.NewOpusCodecData(48000, av.ChStereo)

	case mediaTypeAAC:
		if len(data) >= 7 {
			if cfg, _, _, _, err := aacparser.ParseADTSHeader(data); err == nil {
				if c, err := aacparser.NewCodecDataFromMPEG4AudioConfig(cfg); err == nil {
					return c
				}
			}
		}
		// Fallback: AAC-LC 8 kHz mono.
		fallback := aacparser.MPEG4AudioConfig{
			ObjectType:      2,  // AAC-LC
			SampleRateIndex: 11, // 8000 Hz
			ChannelConfig:   1,  // mono
		}
		fallback.Complete()
		if c, err := aacparser.NewCodecDataFromMPEG4AudioConfig(fallback); err == nil {
			return c
		}
	}

	return nil
}

// stripVideoPrefix removes the 4-byte length/start-code prefix from video frame payloads.
func stripVideoPrefix(data []byte) []byte {
	if len(data) <= 4 {
		return data
	}
	return data[4:]
}
