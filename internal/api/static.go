package api

import (
	"net/http"

	"github.com/vtpl1/vrtc/www"
)

func initStatic(staticDir string) error {
	var root http.FileSystem
	if staticDir == "" {
		staticFs, err := www.GetStaticFS()
		if err != nil {
			return err
		}
		root = http.FS(staticFs)
	} else {
		log.Info().Str("dir", staticDir).Msg("[api] serve static")
		root = http.Dir(staticDir)
	}

	base := len(basePath)
	fileServer := http.FileServer(root)

	HandleFunc("", func(w http.ResponseWriter, r *http.Request) {
		if base > 0 {
			r.URL.Path = r.URL.Path[base:]
		}
		fileServer.ServeHTTP(w, r)
	})
	return nil
}
