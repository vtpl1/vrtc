package videonetics

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/pion/rtp"
	"github.com/vtpl1/vrtc/pkg/core"
	"github.com/vtpl1/vrtc/pkg/h264"
	"github.com/vtpl1/vrtc/pkg/h265"
	pb "github.com/vtpl1/vrtc/pkg/videonetics/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// func getMedias() []*core.Media {
// 	medias := []*core.Media{
// 		{
// 			Kind:      core.KindVideo,
// 			Direction: core.DirectionRecvonly,
// 			Codecs: []*core.Codec{
// 				{
// 					// Name: core.CodecH265,
// 					Name:      core.CodecH264,
// 					ClockRate: 90000,
// 					FmtpLine:  "fmtp:96 packetization-mode=1;profile-level-id=42C032;sprop-parameter-sets=Z0LAMtkAKAC1pqAgICgAAAMACAAAAwCgeMGSQA==,aMuDyyA="},
// 				// FmtpLine: "fmtp:96 sprop-vps=QAEMAf//AUAAAAMAgAAAAwAAAwB4EwJA;sprop-sps=QgEBAUAAAAMAgAAAAwAAAwB4oAPAgBEHy55O5EoPKrm4CAgIIAUmXAAzf5gB;sprop-pps=RAHANzwEbJA="},
// 			},
// 		},
// 	}
// 	return medias
// }

// GetMedias implements core.Producer.
// Subtle: this method shadows the method (Connection).GetMedias of Conn.Connection.
func (c *Conn) GetMedias() []*core.Media {
	log.Info().Msgf("[videonetics] Medias: %v", c.Medias)
	return c.Medias
}

// GetTrack implements core.Producer.
// Subtle: this method shadows the method (Connection).GetTrack of Conn.Connection.
func (c *Conn) GetTrack(media *core.Media, codec *core.Codec) (*core.Receiver, error) {
	core.Assert(media.Direction == core.DirectionRecvonly)
	log.Info().Msgf("[videonetics] GetTrack start")
	defer func() {
		log.Info().Msgf("[videonetics] GetTrack end")
	}()
	for _, track := range c.Receivers {
		if track.Codec == codec {
			return track, nil
		}
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state == StatePlay {
		if err := c.Reconnect(); err != nil {
			return nil, err
		}
	}

	track := core.NewReceiver(media, codec)
	var i int = 0
	track.ID = byte(i)
	c.Receivers = append(c.Receivers, track)

	return track, nil
}

func (c *Conn) Reconnect() error {
	log.Info().Msgf("[videonetics] Reconnect start")
	defer func() {
		log.Info().Msgf("[videonetics] Reconnect end")
	}()
	c.Fire("Videonetics reconnect")

	// close current session
	_ = c.Close()

	// start new session
	if err := c.Dial(); err != nil {
		return err
	}
	if err := c.Describe(); err != nil {
		return err
	}
	return nil
}

func (c *Conn) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Conn) Dial() (err error) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	conn, err := grpc.NewClient(c.host, opts...)
	if err != nil {
		log.Err(err).Msgf("[%v] failed to dial for %v", c.host, c.channel)
		return
	}
	log.Info().Msgf("[%v] success to dial for %v", c.host, c.channel)
	c.stateMu.Lock()
	c.state = StateConn
	c.stateMu.Unlock()
	c.conn = conn
	return
}

func binSize(val int) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(val))
	return buf
}

// Get the fmtpline
func (c *Conn) Describe() (err error) {
	log.Info().Msgf("[videonetics] Describe start")
	defer func() {
		log.Info().Msgf("[videonetics] Describe end")
	}()
	serviceClient := pb.NewStreamServiceClient(c.conn)
	stream, err := serviceClient.ReadFramePVA(*c.ctx, &pb.ReadFramePVARequest{Channel: &pb.Channel{
		SiteId:     c.channel.SiteID,
		ChannelId:  c.channel.ChannelID,
		AppId:      c.channel.AppID,
		LiveOrRec:  c.channel.LiveOrRec,
		StreamType: c.channel.StreamType,
		StartTs:    c.channel.StartTS,
		SessionId:  c.channel.SessionID,
	}})
	if err != nil {
		log.Info().Msg("Failed to FrameRead 1: " + err.Error() + ", ")
		serviceClient = nil
		stream = nil
		return
	}
	c.stream = stream
	totalFrameReceived := 0
	var sps []byte = nil
	var pps []byte = nil
	var vps []byte = nil
dd:
	for {
		response, err := c.stream.Recv()
		if err != nil || response == nil {
			log.Info().Msg("Failed to FrameRead 3: " + err.Error() + ", ")
			c.stream = nil
			return err
		}
		if totalFrameReceived > 10 {
			return errors.New("sps pps vps not received yet")
		}
		totalFrameReceived++
		mediaType := response.GetFramePva().GetFrame().GetMediaType()
		buffer := response.GetFramePva().GetFrame().GetBuffer()
		fmt.Printf("buff %v\n", buffer)
		switch mediaType {
		case 2:
			switch h264.NALUType(buffer) {
			case h264.NALUTypeSPS:
				sps = buffer[4:]
			case h264.NALUTypePPS:
				pps = buffer[4:]
			}
			if sps != nil && pps != nil {
				// sps_pps := append(sps, pps...)
				// fmt.Printf("sps: %v pps: %v sps_pps: %v len %v", sps, pps, sps_pps, len(sps_pps))
				avccbuffer := append(binSize(len(sps)), sps...)
				avccbuffer = append(avccbuffer, binSize(len(pps))...)
				avccbuffer = append(avccbuffer, pps...)

				codec := h264.AVCCToCodec(avccbuffer)
				codec.PayloadType = 0
				c.Medias = append(c.Medias, &core.Media{
					Kind:      core.KindVideo,
					Direction: core.DirectionRecvonly,
					Codecs: []*core.Codec{
						codec,
					}})
				log.Info().Msgf("[videonetics] Codec H264 %v %v", codec, codec.FmtpLine)
				break dd
			}
		case 8:
			switch h265.NALUType(buffer) {
			case h265.NALUTypeSPS:
				sps = buffer[4:]
			case h265.NALUTypePPS:
				pps = buffer[4:]
			case h265.NALUTypeVPS:
				vps = buffer[4:]
			}
			if sps != nil && pps != nil && vps != nil {
				avccbuffer := append(binSize(len(sps)), sps...)
				avccbuffer = append(avccbuffer, binSize(len(pps))...)
				avccbuffer = append(avccbuffer, pps...)
				avccbuffer = append(avccbuffer, binSize(len(vps))...)
				avccbuffer = append(avccbuffer, vps...)

				codec := h265.AVCCToCodec(avccbuffer)
				codec.PayloadType = 0

				c.Medias = append(c.Medias, &core.Media{
					Kind:      core.KindVideo,
					Direction: core.DirectionRecvonly,
					Codecs: []*core.Codec{
						codec,
					}})
				log.Info().Msgf("[videonetics] Codec H265 %v %v", codec, codec.FmtpLine)
				break dd
			}
		}

		// c.Medias = append(c.Medias, )
		if totalFrameReceived > 2 {
			break
		}
	}

	return
}

// Start implements core.Producer.
func (c *Conn) Start() (err error) {
	log.Info().Msgf("[videonetics] Start start")
	defer func() {
		log.Info().Msgf("[videonetics] Start end")
	}()

	ok := false
	c.stateMu.Lock()
	switch c.state {
	case StateNone:
		err = nil
	case StateConn:
		c.state = StatePlay
		ok = true
	}
	c.stateMu.Unlock()

	if !ok {
		return
	}
	log.Info().Msgf("[videonetics] Handle Start %v", err)
	err = c.Handle()
	log.Info().Msgf("[videonetics] Handle Return %v", err)
	return
}

// Stop implements core.Producer.
// Subtle: this method shadows the method (Connection).Stop of Conn.Connection.
func (c *Conn) Stop() (err error) {
	log.Info().Msgf("[videonetics] Stop start")
	defer func() {
		log.Info().Msgf("[videonetics] Stop end")
	}()
	for _, receiver := range c.Receivers {
		receiver.Close()
	}
	for _, sender := range c.Senders {
		sender.Close()
	}

	c.stateMu.Lock()
	if c.state != StateNone {
		c.state = StateNone
		err = c.Close()
	}
	c.stateMu.Unlock()

	return
}

func (c *Conn) ReadFramePVA() (err error) {

	var sequenceNumber uint16 = 1
	var timestamp uint32 = 0
	for {
		response, err := c.stream.Recv()
		if err != nil || response == nil {
			log.Info().Msg("Failed to FrameRead 3: " + err.Error() + ", ")
			c.stream = nil
			return err
		}
		// channel := Channel{
		// 	SiteID:     response.GetFramePva().GetChannel().GetSiteId(),
		// 	ChannelID:  response.GetFramePva().GetChannel().GetChannelId(),
		// 	AppID:      response.GetFramePva().GetChannel().GetAppId(),
		// 	LiveOrRec:  response.GetFramePva().GetChannel().GetLiveOrRec(),
		// 	StreamType: response.GetFramePva().GetChannel().GetStreamType(),
		// 	StartTS:    response.GetFramePva().GetChannel().GetStartTs(),
		// 	SessionID:  response.GetFramePva().GetChannel().GetSessionId(),
		// }
		// fmt.Printf("Here: %d %d\n\n", response.GetFramePva().GetFrame().GetFrameId(), channel.StartTS)
		size := len(response.GetFramePva().GetFrame().Buffer[4:])
		c.Recv += int(size)
		// const ClockRate = 90000
		sequenceNumber++
		timestamp += 80

		packet := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    96,
				Marker:         true,
				SSRC:           20,
				SequenceNumber: sequenceNumber,
				Timestamp:      timestamp * 90,
			},
		}
		packet.Payload = response.GetFramePva().GetFrame().Buffer[4:]

		// if err = packet.Unmarshal(response.GetFramePva().GetFrame().Buffer); err != nil {
		// 	log.Err(err).Msgf("error")
		// 	return
		// }
		var channelID byte = 0
		for _, receiver := range c.Receivers {
			// log.Info().Msgf("Receiver ID: %v  channelID %v", receiver.ID, channelID)
			if receiver.ID == channelID {
				// log.Info().Msgf("packet %v", packet)
				receiver.WriteRTP(packet)
				break
			}
		}
	}

}
