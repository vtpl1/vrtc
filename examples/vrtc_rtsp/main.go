package main

import (
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/pkg/shell"
)

func main() {
	// app.Version = "1.9.4"
	app.AppName = "vrtc"
	app.Init()
	shell.RunUntilSignal()
}
