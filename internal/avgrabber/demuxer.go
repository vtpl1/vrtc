//go:build cgo

package avgrabber

import (
	"context"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec"
	"github.com/vtpl1/vrtc/pkg/av/codec/aacparser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/pcm"
)

const (
	videoStreamIdx uint16 = 0
	audioStreamIdx uint16 = 1

	// frameTimeoutMS is the per-call NextFrame timeout.
	// Short enough to respect context cancellation promptly.
	frameTimeoutMS = 50
)

// Demuxer implements av.DemuxCloser and av.Pauser over an avgrabber Session.
//
// Lifecycle:
//  1. GetCodecs blocks until the first PARAM_SET + KEY pair arrives, then
//     returns []av.Stream so the muxer can write its header.
//  2. ReadPacket delivers KEY, DELTA, and AUDIO packets. A new PARAM_SET
//     causes pkt.NewCodecs to be set on the following KEY packet.
//  3. Pause / Resume delegate to Session.Stop / Resume.
//  4. Close tears down the session.
type Demuxer struct {
	session *Session

	// video codec state
	videoCodec av.CodecData
	videoClk   uint32 // VideoClockRate (typically 90000)

	// audio codec state
	audioCodec av.CodecData
	audioClk   uint32 // AudioSampleRate
	audioReady bool

	// pending H.264 param-sets
	pendingSPSH264 []byte
	pendingPPSH264 []byte

	// pending H.265 param-sets
	pendingVPSH265 []byte
	pendingSPSH265 []byte
	pendingPPSH265 []byte

	// codecDirty is true when a new PARAM_SET arrived since the last KEY.
	// The next KEY packet will carry NewCodecs.
	codecDirty bool
}

// NewDemuxer opens an RTSP session and returns a Demuxer.
// Call Close when done.
func NewDemuxer(cfg Config) (*Demuxer, error) {
	s, err := Open(cfg)
	if err != nil {
		return nil, err
	}

	return &Demuxer{session: s}, nil
}

// GetCodecs blocks until the first PARAM_SET arrives, parses the codec
// parameters, and returns the initial stream list. Called once by the Producer.
func (d *Demuxer) GetCodecs(ctx context.Context) ([]av.Stream, error) {
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		f, err := d.session.NextFrame(frameTimeoutMS)
		if err != nil {
			if IsNotReady(err) {
				continue
			}

			return nil, err
		}

		if f.MediaType == MediaVideo && f.FrameType == FrameTypeParamSet {
			if err := d.applyParamSet(f); err != nil {
				return nil, err
			}

			// Try to learn audio codec from StreamInfo (non-AAC codecs).
			if !d.audioReady {
				d.tryAudioFromStreamInfo()
			}

			if streams := d.buildStreams(); streams != nil {
				return streams, nil
			}
		}

		// Buffer first audio frame for AAC ASC extraction.
		if f.MediaType == MediaAudio && !d.audioReady {
			d.tryAudioFromFrame(f)
		}
	}
}

// ReadPacket returns the next av.Packet, looping over ErrNotReady internally.
func (d *Demuxer) ReadPacket(ctx context.Context) (av.Packet, error) {
	for {
		if ctx.Err() != nil {
			return av.Packet{}, ctx.Err()
		}

		f, err := d.session.NextFrame(frameTimeoutMS)
		if err != nil {
			if IsNotReady(err) {
				continue
			}

			return av.Packet{}, err
		}

		pkt, skip, err := d.frameToPacket(f)
		if err != nil {
			return av.Packet{}, err
		}

		if skip {
			continue
		}

		return pkt, nil
	}
}

// Pause suspends frame delivery by stopping the underlying RTSP session.
// Implements av.Pauser.
func (d *Demuxer) Pause(_ context.Context) error {
	return d.session.Stop()
}

// Resume restarts frame delivery after Pause.
// Implements av.Pauser.
func (d *Demuxer) Resume(_ context.Context) error {
	return d.session.Resume()
}

// IsPaused always returns false; pause state is not tracked locally.
// Implements av.Pauser.
func (d *Demuxer) IsPaused() bool { return false }

// Close tears down the RTSP session.
func (d *Demuxer) Close() error {
	return d.session.Close()
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (d *Demuxer) frameToPacket(f *Frame) (pkt av.Packet, skip bool, err error) {
	switch f.MediaType {
	case MediaVideo:
		return d.videoFrameToPacket(f)
	case MediaAudio:
		return d.audioFrameToPacket(f)
	default:
		return av.Packet{}, true, nil
	}
}

func (d *Demuxer) videoFrameToPacket(f *Frame) (av.Packet, bool, error) {
	switch f.FrameType {
	case FrameTypeParamSet:
		if err := d.applyParamSet(f); err != nil {
			return av.Packet{}, false, err
		}

		return av.Packet{}, true, nil

	case FrameTypeKey:
		pkt := d.basePacket(f, videoStreamIdx)
		pkt.KeyFrame = true
		pkt.Data = stripStartCode(f.Data)

		if d.codecDirty {
			pkt.NewCodecs = d.buildStreams()
			d.codecDirty = false
		}

		return pkt, false, nil

	case FrameTypeDelta:
		pkt := d.basePacket(f, videoStreamIdx)
		pkt.Data = stripStartCode(f.Data)

		return pkt, false, nil

	default:
		return av.Packet{}, true, nil
	}
}

func (d *Demuxer) audioFrameToPacket(f *Frame) (av.Packet, bool, error) {
	if !d.audioReady {
		d.tryAudioFromFrame(f)

		if !d.audioReady {
			return av.Packet{}, true, nil
		}
	}

	pkt := d.basePacket(f, audioStreamIdx)

	if f.CodecType == CodecAAC {
		// Strip ADTS header; send raw AAC payload.
		_, hdrLen, _, _, err := aacparser.ParseADTSHeader(f.Data)
		if err != nil {
			return av.Packet{}, true, nil //nolint:nilerr // malformed ADTS: skip frame silently
		}

		pkt.Data = f.Data[hdrLen:]
	} else {
		pkt.Data = f.Data
	}

	if d.audioClk > 0 && f.DurationTicks > 0 {
		pkt.Duration = time.Duration(f.DurationTicks) * time.Second / time.Duration(d.audioClk)
	}

	return pkt, false, nil
}

// basePacket builds the shared fields of an av.Packet from a Frame.
func (d *Demuxer) basePacket(f *Frame, idx uint16) av.Packet {
	clk := d.videoClk
	if idx == audioStreamIdx {
		clk = d.audioClk
	}

	var dts time.Duration

	var ptsOffset time.Duration

	if clk > 0 {
		dts = time.Duration(f.DTSTicks) * time.Second / time.Duration(clk)

		if f.PTSTicks != f.DTSTicks {
			ptsOffset = time.Duration(f.PTSTicks-f.DTSTicks) * time.Second / time.Duration(clk)
		}
	}

	return av.Packet{
		Idx:             idx,
		CodecType:       avCodecType(f),
		IsDiscontinuity: f.IsDiscontinuity(),
		DTS:             dts,
		PTSOffset:       ptsOffset,
		WallClockTime:   time.UnixMilli(f.WallClockMS),
		FrameID:         f.PTSTicks, // monotonic unique id — used for PVA correlation
	}
}

// applyParamSet parses an Annex-B PARAM_SET frame into pending SPS/PPS/VPS state.
func (d *Demuxer) applyParamSet(f *Frame) error {
	switch f.CodecType {
	case CodecH264:
		return d.applyH264ParamSet(f.Data)
	case CodecH265:
		return d.applyH265ParamSet(f.Data)
	}

	return nil
}

func (d *Demuxer) applyH264ParamSet(data []byte) error {
	var sps, pps []byte

	for _, nal := range splitAnnexB(data) {
		if len(nal) == 0 {
			continue
		}

		switch nal[0] & 0x1F {
		case 7:
			sps = nal
		case 8:
			pps = nal
		}
	}

	if sps == nil || pps == nil {
		return nil // wait for complete set
	}

	cd, err := h264parser.NewCodecDataFromSPSAndPPS(sps, pps)
	if err != nil {
		return err
	}

	d.videoCodec = cd
	d.videoClk = cd.TimeScale()
	d.pendingSPSH264 = sps
	d.pendingPPSH264 = pps
	d.codecDirty = true

	return nil
}

func (d *Demuxer) applyH265ParamSet(data []byte) error {
	var vps, sps, pps []byte

	for _, nal := range splitAnnexB(data) {
		if len(nal) == 0 {
			continue
		}

		nalType := (nal[0] & 0x7E) >> 1

		switch nalType {
		case 32:
			vps = nal
		case 33:
			sps = nal
		case 34:
			pps = nal
		}
	}

	if vps == nil || sps == nil || pps == nil {
		return nil
	}

	cd, err := h265parser.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	if err != nil {
		return err
	}

	d.videoCodec = cd
	d.videoClk = cd.TimeScale()
	d.pendingVPSH265 = vps
	d.pendingSPSH265 = sps
	d.pendingPPSH265 = pps
	d.codecDirty = true

	return nil
}

// tryAudioFromStreamInfo builds codec data for PCM-family codecs using StreamInfo.
func (d *Demuxer) tryAudioFromStreamInfo() {
	info, err := d.session.GetStreamInfo()
	if err != nil || info.AudioSampleRate == 0 {
		return
	}

	d.audioClk = info.AudioSampleRate

	ch := av.ChMono
	if info.AudioChannels >= 2 {
		ch = av.ChFrontLeft | av.ChFrontRight
	}

	switch info.AudioCodec {
	case CodecG711U:
		d.audioCodec = pcm.PCMMulawCodecData{
			Typ:        av.PCM_MULAW,
			SmplFormat: av.S16,
			SmplRate:   int(info.AudioSampleRate),
			ChLayout:   ch,
		}
		d.audioReady = true
	case CodecG711A:
		d.audioCodec = pcm.PCMAlawCodecData{
			Typ:        av.PCM_ALAW,
			SmplFormat: av.S16,
			SmplRate:   int(info.AudioSampleRate),
			ChLayout:   ch,
		}
		d.audioReady = true
	case CodecOpus:
		d.audioCodec = codec.NewOpusCodecData(int(info.AudioSampleRate), ch)
		d.audioReady = true
	case CodecG722, CodecG726, CodecL16:
		d.audioCodec = pcm.PCMMulawCodecData{
			Typ:        av.PCM,
			SmplFormat: av.S16,
			SmplRate:   int(info.AudioSampleRate),
			ChLayout:   ch,
		}
		d.audioReady = true
	}
}

// tryAudioFromFrame extracts codec data from the first audio frame.
func (d *Demuxer) tryAudioFromFrame(f *Frame) {
	ch := av.ChMono

	switch f.CodecType {
	case CodecAAC:
		config, _, _, _, err := aacparser.ParseADTSHeader(f.Data)
		if err != nil {
			return
		}

		cd, err := aacparser.NewCodecDataFromMPEG4AudioConfig(config)
		if err != nil {
			return
		}

		d.audioCodec = cd
		d.audioClk = uint32(config.SampleRate)
		d.audioReady = true

	case CodecG711U:
		d.audioCodec = pcm.NewPCMMulawCodecData()
		d.audioClk = 8000
		d.audioReady = true

	case CodecG711A:
		d.audioCodec = pcm.NewPCMAlawCodecData()
		d.audioClk = 8000
		d.audioReady = true

	case CodecOpus:
		// Try StreamInfo for accurate sample rate; default to 48000.
		sr := uint32(48000)
		if info, err := d.session.GetStreamInfo(); err == nil && info.AudioSampleRate > 0 {
			sr = info.AudioSampleRate
		}

		d.audioCodec = codec.NewOpusCodecData(int(sr), ch)
		d.audioClk = sr
		d.audioReady = true

	case CodecG722, CodecG726, CodecL16:
		sr := uint32(8000)
		if info, err := d.session.GetStreamInfo(); err == nil && info.AudioSampleRate > 0 {
			sr = info.AudioSampleRate
		}

		d.audioCodec = pcm.PCMMulawCodecData{
			Typ:        av.PCM,
			SmplFormat: av.S16,
			SmplRate:   int(sr),
			ChLayout:   ch,
		}
		d.audioClk = sr
		d.audioReady = true
	}
}

// buildStreams returns the current []av.Stream, or nil if video codec is unknown.
func (d *Demuxer) buildStreams() []av.Stream {
	if d.videoCodec == nil {
		return nil
	}

	streams := []av.Stream{{Idx: videoStreamIdx, Codec: d.videoCodec}}

	if d.audioReady && d.audioCodec != nil {
		streams = append(streams, av.Stream{Idx: audioStreamIdx, Codec: d.audioCodec})
	}

	return streams
}

// avCodecType maps avgrabber codec constants to av.CodecType.
func avCodecType(f *Frame) av.CodecType {
	if f.MediaType == MediaAudio {
		switch f.CodecType {
		case CodecAAC:
			return av.AAC
		case CodecG711U:
			return av.PCM_MULAW
		case CodecG711A:
			return av.PCM_ALAW
		case CodecOpus:
			return av.OPUS
		default:
			return av.PCM
		}
	}

	switch f.CodecType {
	case CodecH264:
		return av.H264
	case CodecH265:
		return av.H265
	case CodecMJPEG:
		return av.MJPEG
	default:
		return av.UNKNOWN
	}
}

// splitAnnexB splits an Annex-B byte stream into raw NAL units (start codes removed).
// Handles both 3-byte (00 00 01) and 4-byte (00 00 00 01) start codes.
func splitAnnexB(data []byte) [][]byte {
	var nals [][]byte

	start, i := 0, 0

	for i < len(data) {
		scLen := annexBStartCodeLen(data, i)
		if scLen == 0 {
			i++

			continue
		}

		if i > start {
			nals = append(nals, data[start:i])
		}

		start = i + scLen
		i = start
	}

	if start < len(data) {
		nals = append(nals, data[start:])
	}

	return nals
}

// annexBStartCodeLen returns the length of the Annex-B start code at data[i],
// or 0 if there is no start code at that position.
func annexBStartCodeLen(data []byte, i int) int {
	if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
		return 4
	}

	if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
		return 3
	}

	return 0
}

// stripStartCode removes the leading Annex-B start code from a single-NALU payload.
func stripStartCode(data []byte) []byte {
	if len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		return data[4:]
	}

	if len(data) >= 3 && data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return data[3:]
	}

	return data
}
