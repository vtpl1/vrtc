package videonetics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vtpl1/vrtc/pkg/core"
	pb "github.com/vtpl1/vrtc/pkg/videonetics/service"
)

func getMedias() []*core.Media {
	medias := []*core.Media{
		{
			Kind:      core.KindVideo,
			Direction: core.DirectionRecvonly,
			Codecs: []*core.Codec{
				{Name: core.CodecH264},
			},
		},
	}
	return medias
}

// GetMedias implements core.Producer.
// Subtle: this method shadows the method (Connection).GetMedias of Conn.Connection.
func (c *Conn) GetMedias() []*core.Media {
	log.Info().Msgf("GRPC Medias: %v", c.Medias)
	return c.Medias
}

// GetTrack implements core.Producer.
// Subtle: this method shadows the method (Connection).GetTrack of Conn.Connection.
func (c *Conn) GetTrack(media *core.Media, codec *core.Codec) (*core.Receiver, error) {
	core.Assert(media.Direction == core.DirectionRecvonly)

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
	c.Fire("Videonetics reconnect")

	// close current session
	_ = c.Close()

	// start new session
	if err := c.Dial(); err != nil {
		return err
	}

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
	log.Info().Msg("Start called")
	for {
		ok := false
		if !ok {
			return
		}
	}
}

// Stop implements core.Producer.
// Subtle: this method shadows the method (Connection).Stop of Conn.Connection.
func (c *Conn) Stop() error {
	log.Info().Msg("Stop called")
	c.conn.Close()
	return nil
}

func (c *Conn) ReadFramePVA() {
	ctx, cancel := context.WithCancel(*c.ctx)
	defer cancel()
	var channel = Channel{
		SiteID:     1,
		ChannelID:  1,
		AppID:      0,
		LiveOrRec:  1,
		StreamType: 0,
		StartTS:    0,
		SessionID:  "",
	}
	serviceClient := pb.NewStreamServiceClient(c.conn)
	stream, err := serviceClient.ReadFramePVA(ctx, &pb.ReadFramePVARequest{Channel: &pb.Channel{
		SiteId:     channel.SiteID,
		ChannelId:  channel.ChannelID,
		AppId:      channel.AppID,
		LiveOrRec:  channel.LiveOrRec,
		StreamType: channel.StreamType,
		StartTs:    channel.StartTS,
		SessionId:  channel.SessionID,
	}})
	if err != nil {
		log.Info().Msg("Failed to FrameRead: " + err.Error() + ", ")
		serviceClient = nil
		stream = nil
		return
	}
	for {
		response, err := stream.Recv()
		if err != nil || response == nil {
			log.Info().Msg("Failed to FrameRead: " + err.Error() + ", ")
			time.Sleep(1000 * time.Millisecond)
			serviceClient = nil
			stream = nil
			return
		}
		channel = Channel{
			SiteID:     response.GetFramePva().GetChannel().GetSiteId(),
			ChannelID:  response.GetFramePva().GetChannel().GetChannelId(),
			AppID:      response.GetFramePva().GetChannel().GetAppId(),
			LiveOrRec:  response.GetFramePva().GetChannel().GetLiveOrRec(),
			StreamType: response.GetFramePva().GetChannel().GetStreamType(),
			StartTS:    response.GetFramePva().GetChannel().GetStartTs(),
			SessionID:  response.GetFramePva().GetChannel().GetSessionId(),
		}
		fmt.Printf("Here: %d", response.GetFramePva().GetFrame().GetFrameId())
	}

}
