package avftomp4

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/vtpl1/vrtc/pkg/signal"
)

const AppName = "avf_to_mp4"

func Run(cfg Config) error {
	log.Info().Msgf("%+v", cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = ctx
	errChan := make(chan error)

	// go func(ctx context.Context, errChan chan<- error) {
	// 	var err error

	// 	defer func() {
	// 		errChan <- err
	// 	}()

	// 	log.Info().
	// 		Str("input", cfg.Input).
	// 		Str("output", cfg.Output).
	// 		Msg("Starting conversion")

	// 	c := api.Channel{}
	// 	streamID := c.StreamID()
	// 	o := streammanager3.NewPipeline(
	// 		ctx, streamID,
	// 		func(ctx context.Context, streamID string) (av.DemuxCloser, error) {
	// 			return avfreader.New(cfg.Input)
	// 		},
	// 		nil,
	// 	)

	// 	err = o.AddMuxer(ctx, streamID, "converter", func(_ context.Context, _, _ string) (av.MuxCloser, error) {
	// 		return mp4writer.New(cfg.Output)
	// 	}, nil, nil)
	// 	if err != nil {
	// 		log.Error().Err(err).Msg("AddMuxer error")
	// 	}

	// 	err = o.WaitStop()
	// 	if err != nil {
	// 		log.Error().Err(err).Msg("WaitStop error")
	// 	}
	// }(ctx, errChan)

	signal.WaitForTerminationRequest(errChan)
	cancel()

	return nil
}
