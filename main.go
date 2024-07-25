package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/vtpl1/vrtc/internal/api"
	"github.com/vtpl1/vrtc/internal/api/ws"
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/internal/grpc"
	"github.com/vtpl1/vrtc/internal/hls"
	"github.com/vtpl1/vrtc/internal/mp4"
	"github.com/vtpl1/vrtc/internal/ngrok"
	"github.com/vtpl1/vrtc/internal/rtsp"
	"github.com/vtpl1/vrtc/internal/streams"
	"github.com/vtpl1/vrtc/internal/webrtc"
)

func main() {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	api.InternalTerminationRequest = make(chan int)

	// 1. Core modules: app, api/ws, streams
	app.Init() // init config and logs
	log := app.GetLogger("api")
	api.Init(&ctx)
	ws.Init() // init WS API endpoint

	streams.Init()

	// 2. Main sources and servers

	rtsp.Init()   // rtsp source, RTSP server
	webrtc.Init() // webrtc source, WebRTC server
	grpc.Init(&ctx)

	// 3. Main API

	mp4.Init() // MP4 API
	hls.Init() // HLS API
	// mjpeg.Init() // MJPEG API

	// 6. Helper modules
	ngrok.Init() // ngrok module

	// 7. Go
	log.Info().Msg("Waiting for ctx done")
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
