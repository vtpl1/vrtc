package videonetics

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/internal/streams"
	"github.com/vtpl1/vrtc/pkg/core"
	"github.com/vtpl1/vrtc/pkg/videonetics"
)

func Init(ctx_ *context.Context) {
	var cfg struct {
		Mod struct {
			StreamAddr   string `yaml:"stream_addr"`
			MetadataAddr string `yaml:"metadata_addr"`
		} `yaml:"videonetics"`
	}
	// default config
	// cfg.Mod.StreamAddr = "dns:///172.16.2.143:2003"
	app.LoadConfig(&cfg)
	app.Info["videonetics"] = cfg.Mod

	log = app.GetLogger("videonetics")
	ctx = ctx_
	// videonetics client
	streams.HandleFunc("videonetics", videoneticsHandler)
}

var log zerolog.Logger
var ctx *context.Context

func videoneticsHandler(rawURL string) (core.Producer, error) {
	log.Info().Msgf("[videonetics] videoneticsHandler %s", rawURL)
	conn, err := videonetics.NewClient(rawURL, ctx)
	return conn, err
}
