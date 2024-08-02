package debug

import "github.com/vtpl1/vrtc/internal/api"

func Init() {
	api.HandleFunc("api/stack", stackHandler)
}
