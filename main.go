package main

import (
	"github.com/vtpl1/vrtc/internal/api"
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/pkg/shell"
)

func main() {
	// 1. Core modules: app, api/ws, streams
	app.Init() // init config and logs
	api.Init()

	// 7. Go

	shell.RunUntilSignal()
}
