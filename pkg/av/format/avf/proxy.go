package avf

import (
	"context"
	"errors"
	"io"
	"sync"

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
	ErrNoVideoCodecFound         = errors.New("no video codec found during probe")
)

type ProxyMuxDemuxCloser struct {
	avfFrameDemuxer bool
	pktDemuxer      bool

	avfFrameMuxer bool
	pktMuxer      bool

	// ── Frame→Packet mode probe state ────────────────────────────────────────
	videoCodec                    av.CodecData
	audioCodec                    av.CodecData
	disableAudio                  bool
	videoProbeCount               int
	audioProbeCount               int
	videoConnectHeader            []byte // accumulates CONNECT_HEADER data during probe
	appendingToVideoConnectHeader bool
	videoProbeDone                bool
	audioProbeDone                bool

	// ── Frame→Packet mode forward-phase state ─────────────────────────────────
	videoStreamIdx         uint16      // assigned after probe (always 0)
	audioStreamIdx         uint16      // assigned after probe (1 when audio stream present)
	postProbeConnectHeader []byte      // accumulates CONNECT_HEADER data post-probe
	accumulatingPostProbe  bool        // true while gathering post-probe CONNECT_HEADERs
	pendingNewCodecs       []av.Stream // attached to next packet after codec change

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

// signalHeaderError stores err as the header error and unblocks GetCodecs.
// Returns nil on success or ctx/closing error if the proxy is shutting down.
func (m *ProxyMuxDemuxCloser) signalHeaderError(ctx context.Context, err error) error {
	m.muHeaders.Lock()
	defer m.muHeaders.Unlock()

	m.headersWritten = true
	m.headersErr = err

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
	if m.videoCodec == nil {
		if err := m.signalHeaderError(ctx, ErrNoVideoCodecFound); err != nil {
			return err
		}

		return ErrNoVideoCodecFound
	}

	idx := uint16(0)
	streams := make([]av.Stream, 0, 2)

	m.videoStreamIdx = idx
	streams = append(streams, av.Stream{Idx: idx, Codec: m.videoCodec})
	idx++

	if m.audioCodec != nil {
		m.audioStreamIdx = idx
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

	if !m.videoProbeDone {
		m.videoProbeCount++

		switch frm.FrameType {
		case avf.CONNECT_HEADER:
			m.videoConnectHeader = append(m.videoConnectHeader, frm.Data...)
			m.appendingToVideoConnectHeader = true
		case avf.NON_REF_FRAME, avf.I_FRAME, avf.P_FRAME:
			if m.appendingToVideoConnectHeader {
				m.appendingToVideoConnectHeader = false
				m.videoCodec = parseVideoCodec(frm.MediaType, m.videoConnectHeader)
			} else if frm.FrameType == avf.I_FRAME && m.videoCodec == nil && frm.MediaType == avf.MJPG {
				m.videoCodec = mjpeg.CodecData{}
			}
		case avf.UNKNOWN_FRAME:
			// Skip silently — does NOT terminate CONNECT_HEADER accumulation (R1).
		}

		if m.videoProbeCount > videoProbeSize || m.videoCodec != nil {
			m.videoProbeDone = true
		}
	}

	if !m.audioProbeDone {
		// Audio detection happens only in this block (not in the video probe block)
		// to avoid calling parseAudioCodec twice on the same frame.
		if frm.FrameType == avf.AUDIO_FRAME && !m.disableAudio && m.audioCodec == nil {
			m.audioCodec = parseAudioCodec(frm.MediaType, frm.Data)
		}

		m.audioProbeCount++

		if m.disableAudio || m.audioProbeCount > audioProbeSize || m.audioCodec != nil {
			m.audioProbeDone = true
		}

		// Once video is confirmed and no audio found, audio probe is done immediately.
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

	// In error state (e.g. no video codec found), reject further writes.
	if m.headersErr != nil {
		return m.headersErr
	}

	// Post-probe CONNECT_HEADER: accumulate for mid-stream codec change.
	if frm.FrameType == avf.CONNECT_HEADER {
		m.postProbeConnectHeader = append(m.postProbeConnectHeader, frm.Data...)
		m.accumulatingPostProbe = true

		return nil
	}

	// UNKNOWN_FRAME: skip silently; does NOT terminate post-probe accumulation.
	if frm.FrameType == avf.UNKNOWN_FRAME {
		return nil
	}

	// Video data frame after post-probe accumulation: parse updated codec.
	if m.accumulatingPostProbe && frm.FrameType != avf.AUDIO_FRAME {
		m.accumulatingPostProbe = false

		if newCodec := parseVideoCodec(frm.MediaType, m.postProbeConnectHeader); newCodec != nil {
			m.videoCodec = newCodec
			m.pendingNewCodecs = []av.Stream{{Idx: m.videoStreamIdx, Codec: m.videoCodec}}
		}

		m.postProbeConnectHeader = nil
	}

	// Resolve stream index and codec for this frame.
	var (
		idx      uint16
		pktCodec av.CodecData
	)

	if frm.FrameType == avf.AUDIO_FRAME {
		if m.audioCodec == nil {
			return nil // no audio stream; drop
		}

		idx = m.audioStreamIdx
		pktCodec = m.audioCodec
	} else {
		idx = m.videoStreamIdx
		pktCodec = m.videoCodec
	}

	// Split multi-NALU Annex-B access units into individual single-NALU frames
	// before converting. For audio frames SplitFrame is a no-op.
	split := avf.SplitFrame(frm)

	first := true

	for i := range split {
		sf := split[i]

		pkt, ok := avf.FrameToPacket(&sf, idx, pktCodec)
		if !ok {
			continue
		}

		// Attach NewCodecs to the first packet produced after a codec change.
		if first && len(m.pendingNewCodecs) > 0 {
			pkt.NewCodecs = m.pendingNewCodecs
			m.pendingNewCodecs = nil
		}

		first = false

		select {
		case m.packets <- pkt:
		case <-m.closing:
			return io.EOF
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// ReadFrame implements [avf.FrameDemuxCloser].
// It reads av.Packets written by the pktMuxer side and converts them to
// avf.Frames, emitting per-NALU CONNECT_HEADERs before every keyframe.
func (m *ProxyMuxDemuxCloser) ReadFrame(ctx context.Context) (avf.Frame, error) {
	if m.pktDemuxer {
		return avf.Frame{}, ErrConfiguredAsPacketDemuxer
	}

	m.avfFrameDemuxer = true

	// Return any queued frames (pending CONNECT_HEADERs or I_FRAME).
	if len(m.readFramePending) > 0 {
		frm := m.readFramePending[0]
		m.readFramePending = m.readFramePending[1:]

		return frm, nil
	}

	// On the first call, wait for headers and emit the initial CONNECT_HEADERs.
	if !m.readFrameHeaderSent {
		select {
		case <-m.headersAvailable:
		case <-m.closing:
			return avf.Frame{}, io.EOF
		case <-ctx.Done():
			return avf.Frame{}, ctx.Err()
		}

		m.readFrameHeaderSent = true

		// headersAvailable may have been closed by Close() before headers were
		// written (e.g. proxy closed without a WriteHeader call). In that case
		// headersWritten is false and there is nothing to emit.
		m.muHeaders.Lock()
		headersWritten := m.headersWritten
		headers := m.headers
		m.muHeaders.Unlock()

		if !headersWritten {
			return avf.Frame{}, io.EOF
		}

		for _, s := range headers {
			if !s.Codec.Type().IsVideo() {
				continue
			}

			frames := avf.BuildConnectHeaderFrames(s.Codec, 0)
			if len(frames) > 0 {
				m.readFramePending = append(m.readFramePending, frames[1:]...)

				return frames[0], nil
			}
		}

		// No video stream or no parameter sets — return empty CONNECT_HEADER.
		return avf.Frame{BasicFrame: avf.BasicFrame{FrameType: avf.CONNECT_HEADER}}, nil
	}

	// Read the next packet and convert it to avf.Frame(s).
	select {
	case pkt, ok := <-m.packets:
		if !ok {
			return avf.Frame{}, errors.Join(io.EOF, ErrProxyIsClosing)
		}

		// Look up the codec for this packet's stream index.
		var codec av.CodecData

		m.muHeaders.Lock()
		for _, s := range m.headers {
			if s.Idx == pkt.Idx {
				codec = s.Codec

				break
			}
		}
		m.muHeaders.Unlock()

		frames := avf.PacketToFrames(pkt, codec)
		if len(frames) == 0 {
			// Cannot convert (unsupported codec) — return a sentinel.
			return avf.Frame{BasicFrame: avf.BasicFrame{FrameType: avf.UNKNOWN_FRAME}}, nil
		}

		if len(frames) > 1 {
			m.readFramePending = append(m.readFramePending, frames[1:]...)
		}

		return frames[0], nil

	case <-m.closing:
		return avf.Frame{}, io.EOF
	case <-ctx.Done():
		return avf.Frame{}, ctx.Err()
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

		// headersAvailable may have been closed by Close() before any headers
		// were written. In that case headersWritten is false and we return EOF.
		if !m.headersWritten {
			return nil, io.EOF
		}

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
