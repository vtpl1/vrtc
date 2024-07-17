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
	api.InternalTerminationRequest = make(chan int)
	// 1. Core modules: app, api/ws, streams
	app.Init(&ctx) // init config and logs
	log := app.GetLogger("api")
	api.Init(&ctx)
	log.Info().Msg("Waiting for ctx done")

	// 7. Go
	doShutdown := false
	for !doShutdown {
		select {
		case <-ctx.Done():
			doShutdown = true
		case <-api.InternalTerminationRequest:
			stop()
			doShutdown = true
		}
	}

	log.Info().Msg("ctx done")
	// shell.RunUntilSignal()
}
