// Package debug handles stack traces over api
package debug

import "github.com/vtpl1/vrtc/internal/api"

// Init is the entrypoint
func Init() {
	api.HandleFunc("api/stack", stackHandler)
}
