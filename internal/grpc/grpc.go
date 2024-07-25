package grpc

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/internal/streams"
	"github.com/vtpl1/vrtc/pkg/core"
	"github.com/vtpl1/vrtc/pkg/grpc"
)

func Init(ctx_ *context.Context) {
	var cfg struct {
		Mod struct {
			StreamAddr   string `yaml:"stream_addr"`
			MetadataAddr string `yaml:"metadata_addr"`
		} `yaml:"grpc"`
	}
	// default config
	// cfg.Mod.StreamAddr = "dns:///172.16.2.143:2003"
	app.LoadConfig(&cfg)
	app.Info["grpc"] = cfg.Mod

	log = app.GetLogger("grpc")
	ctx = ctx_
	// grpc client
	streams.HandleFunc("grpc", grpcHandler)
}

var log zerolog.Logger
var ctx *context.Context

func grpcHandler(rawURL string) (core.Producer, error) {
	log.Info().Msgf("[grpc] grpcHandler %s", rawURL)
	conn := grpc.NewClient(rawURL)
	if err := conn.Dial(); err != nil {
		return nil, err
	}
	return conn, nil
}
