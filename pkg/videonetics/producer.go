package videonetics

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc/pkg/core"
	pb "github.com/vtpl1/vrtc/pkg/videonetics/service"
	"google.golang.org/grpc"
)

type Producer struct {
	core.Connection
	// core.Listener

	uri        string
	ctx        *context.Context
	streamConn *grpc.ClientConn
}

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
// Subtle: this method shadows the method (Connection).GetMedias of Producer.Connection.
func (c *Producer) GetMedias() []*core.Media {
	log.Info().Msgf("GRPC Medias: %v", c.Medias)
	return c.Medias
}

// GetTrack implements core.Producer.
// Subtle: this method shadows the method (Connection).GetTrack of Producer.Connection.
func (c *Producer) GetTrack(media *core.Media, codec *core.Codec) (*core.Receiver, error) {
	core.Assert(media.Direction == core.DirectionRecvonly)

	for _, track := range c.Receivers {
		if track.Codec == codec {
			return track, nil
		}
	}

	track := core.NewReceiver(media, codec)
	var i int = 0
	track.ID = byte(i)
	c.Receivers = append(c.Receivers, track)

	return track, nil
}

// Start implements core.Producer.
func (c *Producer) Start() error {
	log.Info().Msg("Start called")
	go c.ReadFramePVA()
	return nil
}

// Stop implements core.Producer.
// Subtle: this method shadows the method (Connection).Stop of Producer.Connection.
func (c *Producer) Stop() error {
	log.Info().Msg("Stop called")
	c.streamConn.Close()
	return nil
}

func (c *Producer) ReadFramePVA() {
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
	serviceClient := pb.NewStreamServiceClient(c.streamConn)
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
