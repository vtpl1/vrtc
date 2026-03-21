package avf

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/mjpeg"
	"github.com/vtpl1/vrtc/pkg/av/codec/parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
	"github.com/vtpl1/vrtc/pkg/avf"
)

var (
	ErrProxyModeConflict         = errors.New("proxy mode conflict")
	ErrConfiguredAsPacketDemuxer = errors.New("configured as packet demuxer")
	ErrConfiguredAsFrameDemuxer  = errors.New("configured as frame demuxer")
	ErrConfiguredAsPacketMuxer   = errors.New("configured as packet muxer")
	ErrConfiguredAsFrameMuxer    = errors.New("configured as frame muxer")
	ErrEmptyHeader               = errors.New("empty header")
	ErrHeaderNotWritten          = errors.New("header not written")
	ErrProxyIsClosing            = errors.New("proxy is closing")
)

type ProxyMuxDemuxCloser struct {
	avfFrameDemuxer bool
	pktDemuxer      bool

	avfFrameMuxer bool
	pktMuxer      bool

	// detected codec state (frame muxer mode)
	videoCodec                    av.CodecData
	audioCodec                    av.CodecData
	disableAudio                  bool
	videoProbeCount               int
	audioProbeCount               int
	videoConnectHeader            []byte
	appendingToVideoConnectHeader bool
	videoProbeDone                bool
	audioProbeDone                bool

	muHeaders                 sync.Mutex
	headersWritten            bool
	headers                   []av.Stream
	headersErr                error
	headersAvailableCloseOnce sync.Once
	headersAvailable          chan struct{}

	packetsCloseOnce sync.Once
	packets          chan av.Packet

	closingCloseOnce sync.Once
	closing          chan struct{}

	// frame demuxer mode state
	readFrameHeaderSent bool
	readFramePending    []avf.Frame
}

// signalHeaders stores the header streams and unblocks any reader waiting on
// headersAvailable. It is the shared implementation for WriteHeader (pktMuxer
// mode) and writeHeaderFromCodecs (avfFrameMuxer mode).
func (m *ProxyMuxDemuxCloser) signalHeaders(ctx context.Context, streams []av.Stream) error {
	m.muHeaders.Lock()
	defer m.muHeaders.Unlock()

	m.headersWritten = true
	if len(streams) > 0 {
		m.headers = streams
	} else {
		m.headersErr = ErrEmptyHeader
	}

	select {
	case m.headersAvailable <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.closing:
		return ErrProxyIsClosing
	}
}

func (m *ProxyMuxDemuxCloser) writeHeaderFromCodecs(ctx context.Context) error {
	idx := uint16(0)
	streams := make([]av.Stream, 0)

	if m.videoCodec != nil {
		streams = append(streams, av.Stream{Idx: idx, Codec: m.videoCodec})
		idx++
	}

	if m.audioCodec != nil {
		streams = append(streams, av.Stream{Idx: idx, Codec: m.audioCodec})
	}

	return m.signalHeaders(ctx, streams)
}

// WriteFrame implements [avf.FrameMuxCloser].
func (m *ProxyMuxDemuxCloser) WriteFrame(ctx context.Context, frm avf.Frame) error {
	if m.pktMuxer {
		return ErrConfiguredAsPacketMuxer
	}

	m.avfFrameMuxer = true

	// ── Probe phase ──────────────────────────────────────────────────────────
	// Run codec detection before checking done-conditions so that a codec
	// detected in this frame immediately counts toward the done check.

	if !m.videoProbeDone {
		m.videoProbeCount++

		switch frm.FrameType {
		case avf.AUDIO_FRAME:
			if !m.disableAudio && m.audioCodec == nil {
				m.audioCodec = parseAudioCodec(frm.MediaType, frm.Data)
			}
		case avf.CONNECT_HEADER:
			m.videoConnectHeader = append(m.videoConnectHeader, frm.Data...)
			m.appendingToVideoConnectHeader = true
		case avf.H_FRAME, avf.I_FRAME, avf.P_FRAME, avf.UNKNOWN_FRAME:
			if m.appendingToVideoConnectHeader {
				m.appendingToVideoConnectHeader = false
				m.videoCodec = parseVideoCodec(frm.MediaType, m.videoConnectHeader)
			} else if frm.FrameType == avf.I_FRAME && m.videoCodec == nil && frm.MediaType == avf.MJPG {
				m.videoCodec = mjpeg.CodecData{}
			}
		}

		if m.videoProbeCount > videoProbeSize || m.videoCodec != nil {
			m.videoProbeDone = true
		}
	}

	if !m.audioProbeDone {
		if frm.FrameType == avf.AUDIO_FRAME && m.audioCodec == nil {
			m.audioCodec = parseAudioCodec(frm.MediaType, frm.Data)
		}

		m.audioProbeCount++

		if m.disableAudio || m.audioProbeCount > audioProbeSize || m.audioCodec != nil {
			m.audioProbeDone = true
		}

		// Once video is confirmed and no audio has been found yet, declare the
		// audio probe done immediately (no audio stream present).
		if m.videoProbeDone && m.audioCodec == nil {
			m.audioProbeDone = true
		}
	}

	// ── Forward phase ─────────────────────────────────────────────────────────
	if !m.videoProbeDone || !m.audioProbeDone {
		return nil
	}

	if !m.headersWritten {
		if err := m.writeHeaderFromCodecs(ctx); err != nil {
			return err
		}
	}

	// CONNECT_HEADER frames carry codec parameters, not media data — skip them.
	if frm.FrameType == avf.CONNECT_HEADER {
		return nil
	}

	pkt := avf.FrameToAVPacket(&frm)
	pkt.DTS = time.Duration(frm.TimeStamp) * time.Millisecond

	select {
	case m.packets <- *pkt:
	case <-m.closing:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// ReadFrame implements [avf.FrameDemuxCloser].
// It reads av.Packets written by the pktMuxer side and converts them to
// avf.Frames, emitting a CONNECT_HEADER before every keyframe.
func (m *ProxyMuxDemuxCloser) ReadFrame(ctx context.Context) (avf.Frame, error) {
	if m.pktDemuxer {
		return avf.Frame{}, ErrConfiguredAsPacketDemuxer
	}

	m.avfFrameDemuxer = true

	// Return any queued frames (e.g. the I_FRAME queued after a CONNECT_HEADER).
	if len(m.readFramePending) > 0 {
		frm := m.readFramePending[0]
		m.readFramePending = m.readFramePending[1:]

		return frm, nil
	}

	// On the first call, wait for headers and emit a CONNECT_HEADER.
	if !m.readFrameHeaderSent {
		select {
		case <-m.headersAvailable:
		case <-m.closing:
			return avf.Frame{}, io.EOF
		case <-ctx.Done():
			return avf.Frame{}, ctx.Err()
		}

		m.readFrameHeaderSent = true

		return m.buildConnectHeaderFrame(), nil
	}

	// Read the next packet and convert it to a frame.
	select {
	case pkt, ok := <-m.packets:
		if !ok {
			return avf.Frame{}, errors.Join(io.EOF, ErrProxyIsClosing)
		}

		if pkt.KeyFrame {
			// Emit CONNECT_HEADER now; queue the I_FRAME for the next call.
			m.readFramePending = append(m.readFramePending, pktToAVFFrame(pkt))

			return m.buildConnectHeaderFrame(), nil
		}

		return pktToAVFFrame(pkt), nil

	case <-m.closing:
		return avf.Frame{}, io.EOF
	case <-ctx.Done():
		return avf.Frame{}, ctx.Err()
	}
}

// buildConnectHeaderFrame constructs a CONNECT_HEADER avf.Frame from the
// first video stream in m.headers.
func (m *ProxyMuxDemuxCloser) buildConnectHeaderFrame() avf.Frame {
	m.muHeaders.Lock()
	headers := m.headers
	m.muHeaders.Unlock()

	for _, s := range headers {
		if !s.Codec.Type().IsVideo() {
			continue
		}

		mt := avf.MediaTypeFromCodec(s.Codec.Type())
		data := buildConnectHeaderPayload(s.Codec)

		return avf.Frame{
			BasicFrame: avf.BasicFrame{
				MediaType: mt,
				FrameType: avf.CONNECT_HEADER,
			},
			Data: data,
		}
	}

	return avf.Frame{BasicFrame: avf.BasicFrame{FrameType: avf.CONNECT_HEADER}}
}

// pktToAVFFrame converts an av.Packet to an avf.Frame for ReadFrame output.
func pktToAVFFrame(pkt av.Packet) avf.Frame {
	mt := avf.MediaTypeFromCodec(pkt.CodecType)

	var ft avf.FrameType

	switch pkt.CodecType {
	case av.H264, av.H265, av.MJPEG:
		if pkt.KeyFrame {
			ft = avf.I_FRAME
		} else {
			ft = avf.P_FRAME
		}
	default:
		ft = avf.AUDIO_FRAME
	}

	data := pkt.Data
	if pkt.CodecType == av.H264 || pkt.CodecType == av.H265 {
		data = prependStartCode(pkt.Data)
	}

	return avf.Frame{
		BasicFrame: avf.BasicFrame{
			MediaType: mt,
			FrameType: ft,
			TimeStamp: pkt.DTS.Milliseconds(),
		},
		FrameID: pkt.FrameID,
		Data:    data,
	}
}

// WriteHeader implements [av.MuxCloser].
func (m *ProxyMuxDemuxCloser) WriteHeader(ctx context.Context, streams []av.Stream) error {
	if m.avfFrameMuxer {
		return ErrConfiguredAsFrameMuxer
	}

	m.pktMuxer = true

	return m.signalHeaders(ctx, streams)
}

// WritePacket implements [av.MuxCloser].
func (m *ProxyMuxDemuxCloser) WritePacket(ctx context.Context, pkt av.Packet) error {
	if m.avfFrameMuxer {
		return ErrConfiguredAsFrameMuxer
	}

	m.pktMuxer = true
	if !m.headersWritten {
		return ErrHeaderNotWritten
	}

	select {
	case m.packets <- pkt:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.closing:
		return ErrProxyIsClosing
	}
}

// WriteTrailer implements [av.MuxCloser].
func (m *ProxyMuxDemuxCloser) WriteTrailer(_ context.Context, _ error) error {
	if m.avfFrameMuxer {
		return ErrConfiguredAsFrameMuxer
	}

	m.pktMuxer = true

	return nil
}

// Close implements [av.DemuxCloser].
func (m *ProxyMuxDemuxCloser) Close() error {
	m.headersAvailableCloseOnce.Do(func() {
		close(m.headersAvailable)
	})

	m.packetsCloseOnce.Do(func() {
		close(m.packets)
	})

	m.closingCloseOnce.Do(func() {
		close(m.closing)
	})

	return nil
}

// GetCodecs implements [av.DemuxCloser].
func (m *ProxyMuxDemuxCloser) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	if m.avfFrameDemuxer {
		return nil, ErrConfiguredAsFrameDemuxer
	}

	m.pktDemuxer = true
	select {
	case <-m.headersAvailable:
		m.muHeaders.Lock()
		defer m.muHeaders.Unlock()

		return m.headers, m.headersErr
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ReadPacket implements [av.DemuxCloser].
func (m *ProxyMuxDemuxCloser) ReadPacket(ctx context.Context) (av.Packet, error) {
	if m.avfFrameDemuxer {
		return av.Packet{}, ErrConfiguredAsFrameDemuxer
	}

	m.pktDemuxer = true

	select {
	case pkt, ok := <-m.packets:
		if !ok {
			return av.Packet{}, errors.Join(io.EOF, ErrProxyIsClosing)
		}

		return pkt, nil
	case <-ctx.Done():
		return av.Packet{}, ctx.Err()
	}
}

// NewProxyMuxDemuxCloser creates an in-memory proxy ProxyMuxDemuxCloser
// Supported combinations:
//   - Frame demuxer -> Packet muxer
//   - Packet demuxer -> Frame muxer
//   - Packet demuxer -> Packet muxer
func NewProxyMuxDemuxCloser(bufSize int) *ProxyMuxDemuxCloser {
	return &ProxyMuxDemuxCloser{
		headersAvailable: make(chan struct{}, 1),
		packets:          make(chan av.Packet, bufSize),
		closing:          make(chan struct{}),
	}
}

// parseVideoCodec builds codec data from a CONNECT_HEADER payload (Annex-B NALUs).
func parseVideoCodec(mediaType avf.MediaType, data []byte) av.CodecData {
	nalus, _ := parser.SplitNALUs(data)

	switch mediaType {
	case avf.H264:
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

	case avf.H265:
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

	case avf.MJPG,
		avf.MPEG,
		avf.G711U,
		avf.G711A,
		avf.L16,
		avf.AAC,
		avf.UNKNOWN,
		avf.G722,
		avf.G726,
		avf.OPUS,
		avf.MP2L2:
		return nil
	}

	return nil
}

// parseAudioCodec infers audio codec data from the MediaType and the first
// audio frame payload (used to detect AAC parameters via ADTS header parsing).
func parseAudioCodec(mediaType avf.MediaType, data []byte) av.CodecData {
	switch mediaType {
	case avf.G711U:
		return pcm.NewPCMMulawCodecData()

	case avf.G711A:
		return pcm.NewPCMAlawCodecData()

	case avf.OPUS:
		return codec.NewOpusCodecData(48000, av.ChStereo)

	case avf.AAC:
		// Attempt ADTS header parse for sample rate / channel config.
		if len(data) >= 7 {
			if cfg, _, _, _, err := aacparser.ParseADTSHeader(data); err == nil {
				if c, err := aacparser.NewCodecDataFromMPEG4AudioConfig(cfg); err == nil {
					return c
				}
			}
		}

		// Fall back to AAC-LC 8 kHz mono.
		fallback := aacparser.MPEG4AudioConfig{
			ObjectType:      2,  // AAC-LC
			SampleRateIndex: 11, // 8000 Hz
			ChannelConfig:   1,  // mono (front-center)
		}
		fallback.Complete()

		if c, err := aacparser.NewCodecDataFromMPEG4AudioConfig(fallback); err == nil {
			return c
		}

	case avf.MJPG,
		avf.MPEG,
		avf.H264,
		avf.L16,
		avf.UNKNOWN,
		avf.H265,
		avf.G722,
		avf.G726,
		avf.MP2L2:
		return nil
	}

	return nil
}
