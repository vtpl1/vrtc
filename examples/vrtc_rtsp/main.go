package main

import (
	"github.com/vtpl1/vrtc/internal/app"
	"github.com/vtpl1/vrtc/pkg/shell"
)

func main() {
	app.AppName = "vrtc"
	app.Init()
	shell.RunUntilSignal()
}
