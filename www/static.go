package www

import (
	"embed"
	"io/fs"
)

//go:embed dist_orig
var embed_ui embed.FS

func GetStaticFS() fs.FS {
	//embedRoot, err := fs.Sub(embed_ui, "ui")
	embedRoot, err := fs.Sub(embed_ui, "dist_orig")
	if err != nil {
		// slog.Error("Unable to get root for web ui", slog.String("error", err.Error()))
		// os.Exit(1)
		panic("Unable to get root for web ui")
	}
	return embedRoot
	// return http.FileServer(http.FS(embedRoot))
}
