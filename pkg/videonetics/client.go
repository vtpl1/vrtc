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

func NewClient(uri string, ctx *context.Context) (*Conn, error) {
	log = app.GetLogger("videonetics")
	return &Conn{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "videonetics",
			Medias:     getMedias(),
		},
		uri: uri,
		ctx: ctx,
	}, nil
}

func (c *Conn) Dial() (err error) {
	// log = app.GetLogger("grpc")
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	streamAddr := "dns:///172.16.1.146:20003"

	conn, err := grpc.NewClient(streamAddr, opts...)
	if err != nil {
		log.Info().Msg("[" + streamAddr + "] failed to dial: " + err.Error() + " for")
		return
	}
	log.Info().Msg("[" + streamAddr + "] success to dial for ")
	c.conn = conn
	return
}
