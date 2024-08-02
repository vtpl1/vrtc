// Package main is the entrypoint
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vtpl1/vrtc/internal/api"
	"github.com/vtpl1/vrtc/internal/api/ws"
	"github.com/vtpl1/vrtc/internal/debug"
	"github.com/vtpl1/vrtc/internal/hls"
	"github.com/vtpl1/vrtc/internal/mp4"
	"github.com/vtpl1/vrtc/internal/ngrok"
	"github.com/vtpl1/vrtc/internal/rtsp"
	"github.com/vtpl1/vrtc/internal/streams"
	"github.com/vtpl1/vrtc/internal/videonetics"
	"github.com/vtpl1/vrtc/internal/webrtc"
	"github.com/vtpl1/vrtc/utils"
)

func main() {
	utils.Version = "1.9.4"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	utils.InternalTerminationRequest = make(chan int)

	// 1. Core modules: app, api/ws, streams

	err := utils.Init() // init config and logs
	if err != nil {
		fmt.Println("Error: ", err)
		return
	}
	log := utils.GetLogger("api")

	api.Init() // init API before all others
	ws.Init()  // init WS API endpoint

	streams.Init() // streams module

	// 2. Main sources and servers

	rtsp.Init()   // rtsp source, RTSP server
	webrtc.Init() // webrtc source, WebRTC server

	// 3. Main API

	mp4.Init() // MP4 API
	hls.Init() // HLS API
	// mjpeg.Init() // MJPEG API

	// 4. Other sources and servers

	videonetics.Init(&ctx)

	// 5. Other sources

	// 6. Helper modules

	ngrok.Init() // ngrok module

	debug.Init() // debug API

	// 7. Go
	log.Info().Msg("Waiting for ctx done")
	doShutdown := false
	for !doShutdown {
		select {
		case <-ctx.Done():
			doShutdown = true
		case <-utils.InternalTerminationRequest:
			stop()
			doShutdown = true
		}
	}

	log.Info().Msg("ctx done")
}
