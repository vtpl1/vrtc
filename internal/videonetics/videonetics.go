package videonetics

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc/internal/streams"
	"github.com/vtpl1/vrtc/pkg/core"
	"github.com/vtpl1/vrtc/pkg/videonetics"
	"github.com/vtpl1/vrtc/utils"
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
	utils.LoadConfig(&cfg)
	utils.Info["videonetics"] = cfg.Mod

	log = utils.GetLogger("videonetics")
	ctx = ctx_
	// videonetics client
	streams.HandleFunc("videonetics", videoneticsHandler)
}

var (
	log zerolog.Logger
	ctx *context.Context
)

func videoneticsHandler(rawURL string) (core.Producer, error) {
	log.Info().Msgf("[videonetics] videoneticsHandler %s", rawURL)
	conn := videonetics.NewClient(rawURL, ctx)
	if err := conn.Reconnect(); err != nil {
		return nil, err
	}
	return conn, nil
}
