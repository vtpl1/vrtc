package videonetics

import (
	"context"

	"github.com/rs/zerolog/log"
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

// var log zerolog.Logger

func NewClient(uri string, ctx *context.Context) (*Producer, error) {

	// log = app.GetLogger("grpc")
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	streamAddr := "dns:///172.16.1.146:20003"

	streamConn, err := grpc.NewClient(streamAddr, opts...)
	if err != nil {
		log.Info().Msg("[" + streamAddr + "] failed to dial: " + err.Error() + " for")
	}
	log.Info().Msg("[" + streamAddr + "] success to dial for ")

	return &Producer{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "grpc",
			Medias:     getMedias(),
		},
		uri:        uri,
		ctx:        ctx,
		streamConn: streamConn,
	}, err
}
