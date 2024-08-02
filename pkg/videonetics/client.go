package videonetics

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc/pkg/core"
	"github.com/vtpl1/vrtc/utils"
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
	log = utils.GetLogger("videonetics")
	host, channel, err := ParseVideoneticsUri(uri)
	if err != nil {
		return nil
	}
	return &Conn{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "videonetics",
			// Medias:     getMedias(),
		},
		uri:     uri,
		ctx:     ctx,
		host:    host,
		channel: channel,
	}
}
