package videonetics

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/pkg/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Channel struct {
	SiteID     int64  `json:"site_id"`
	ChannelID  int64  `json:"channel_id"`
	AppID      int64  `json:"app_id"`
	LiveOrRec  int32  `json:"live_or_rec"`
	StreamType int32  `json:"stream_type"`
	StartTS    int64  `json:"start_ts"`
	SessionID  string `json:"session_id"`
}

var log zerolog.Logger

func NewClient(uri string, ctx *context.Context) *Conn {

	log = app.GetLogger("videonetics")
	host, channel, err := ParseVideoneticsUri(uri)
	if err != nil {
		return nil
	}
	return &Conn{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "videonetics",
			Medias:     getMedias(),
		},
		uri:     uri,
		ctx:     ctx,
		host:    host,
		channel: channel,
	}
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
