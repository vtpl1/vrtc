package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/vtpl1/vrtc/internal/api"
	"github.com/vtpl1/vrtc/internal/app"
)

func main() {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// 1. Core modules: app, api/ws, streams
	app.Init(&ctx) // init config and logs
	log := app.GetLogger("api")
	api.Init(&ctx)
	log.Info().Msg("Waiting for ctx done")
	// 7. Go
	<-ctx.Done()
	log.Info().Msg("ctx done")
	// shell.RunUntilSignal()
}
