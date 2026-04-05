package edgeview

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:ui/dist
var uiFS embed.FS

// uiAvailable reports whether the embedded UI dist contains at least an
// index.html (i.e. the frontend was built before `go build`).
func uiAvailable() bool {
	f, err := uiFS.Open("ui/dist/index.html")
	if err != nil {
		return false
	}

	f.Close()

	return true
}

// isUIPath returns true for paths that should bypass auth because they
// are either embedded UI assets or SPA fallback routes (i.e. anything
// that is NOT an /api/, /health, /docs, or /openapi path).
func isUIPath(path string) bool {
	if !uiAvailable() {
		return false
	}

	return !strings.HasPrefix(path, "/api/") &&
		!strings.HasPrefix(path, "/health") &&
		!strings.HasPrefix(path, "/docs") &&
		!strings.HasPrefix(path, "/openapi")
}

// spaHandler returns an http.Handler that serves the embedded SPA.
// Static assets are served directly; all other paths fall back to
// index.html so that client-side routing works.
func spaHandler() http.Handler {
	sub, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		// Should never happen — the path is compile-time constant.
		panic("edgeview: ui/dist sub-filesystem: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file first.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		if _, err := fs.Stat(sub, path); err == nil {
			fileServer.ServeHTTP(w, r)

			return
		}

		// File not found → serve index.html (SPA fallback).
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
