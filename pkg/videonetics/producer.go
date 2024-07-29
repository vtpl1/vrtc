package videonetics

import (
	"errors"

	"github.com/pion/rtp"
	"github.com/vtpl1/vrtc/pkg/core"
	pb "github.com/vtpl1/vrtc/pkg/videonetics/service"
)

func getMedias() []*core.Media {
	medias := []*core.Media{
		{
			Kind:      core.KindVideo,
			Direction: core.DirectionRecvonly,
			Codecs: []*core.Codec{
				{
					Name: core.CodecH265,
					//Name: core.CodecH264,
					ClockRate: 90000,
					// FmtpLine:  "fmtp:96 packetization-mode=1;profile-level-id=42C032;sprop-parameter-sets=Z0LAMtkAKAC1pqAgICgAAAMACAAAAwCgeMGSQA==,aMuDyyA="},
					FmtpLine: "fmtp:96 sprop-vps=QAEMAf//AUAAAAMAgAAAAwAAAwB4EwJA;sprop-sps=QgEBAUAAAAMAgAAAAwAAAwB4oAPAgBEHy55O5EoPKrm4CAgIIAUmXAAzf5gB;sprop-pps=RAHANzwEbJA="},
			},
		},
	}
	return medias
}

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
	// if err := c.Describe(); err != nil {
	// 	return err
	// }
	return nil
}

func (c *Conn) Close() error {
	if c.conn == nil {
		return errors.New("connection is not established")
	}
	return c.conn.Close()
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
func (c *Conn) Describe() (err error) {
	return nil
}
func (c *Conn) Describe1() (err error) {
	log.Info().Msgf("[videonetics] Describe start")
	defer func() {
		log.Info().Msgf("[videonetics] Describe end")
	}()
	var channel = Channel{
		SiteID:     1,
		ChannelID:  3,
		AppID:      0,
		LiveOrRec:  1,
		StreamType: 0,
		StartTS:    0,
		SessionID:  "",
	}
	serviceClient := pb.NewStreamServiceClient(c.conn)
	stream, err := serviceClient.ReadFramePVA(*c.ctx, &pb.ReadFramePVARequest{Channel: &pb.Channel{
		SiteId:     channel.SiteID,
		ChannelId:  channel.ChannelID,
		AppId:      channel.AppID,
		LiveOrRec:  channel.LiveOrRec,
		StreamType: channel.StreamType,
		StartTs:    channel.StartTS,
		SessionId:  channel.SessionID,
	}})
	if err != nil {
		log.Info().Msg("Failed to FrameRead 1: " + err.Error() + ", ")
		serviceClient = nil
		stream = nil
		return
	}
	count := 0
	for {
		response, err := stream.Recv()
		if err != nil || response == nil {
			log.Info().Msg("Failed to FrameRead 2: " + err.Error() + ", ")
			return err
		}
		if count < 2 {
			count++
		} else {
			err = nil
			break
		}

	}
	log.Info().Msg("Here in Describe")
	// c.stream = stream
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

func (c *Conn) ReadFramePVA() {
	var channel = Channel{
		SiteID:     1,
		ChannelID:  3,
		AppID:      0,
		LiveOrRec:  1,
		StreamType: 0,
		StartTS:    0,
		SessionID:  "",
	}
	serviceClient := pb.NewStreamServiceClient(c.conn)
	stream, err := serviceClient.ReadFramePVA(*c.ctx, &pb.ReadFramePVARequest{Channel: &pb.Channel{
		SiteId:     channel.SiteID,
		ChannelId:  channel.ChannelID,
		AppId:      channel.AppID,
		LiveOrRec:  channel.LiveOrRec,
		StreamType: channel.StreamType,
		StartTs:    channel.StartTS,
		SessionId:  channel.SessionID,
	}})
	if err != nil {
		log.Info().Msg("Failed to FrameRead 1: " + err.Error() + ", ")
		serviceClient = nil
		stream = nil
		return
	}
	var sequenceNumber uint16 = 1
	var timestamp uint32 = 0
	for {
		response, err := stream.Recv()
		if err != nil || response == nil {
			log.Info().Msg("Failed to FrameRead 3: " + err.Error() + ", ")
			stream = nil
			return
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
			log.Info().Msgf("Receiver ID: %v  channelID %v", receiver.ID, channelID)
			if receiver.ID == channelID {
				packet.Timestamp = timestamp * 90
				log.Info().Msgf("packet %v", packet)
				receiver.WriteRTP(packet)
				break
			}
		}
	}

}
