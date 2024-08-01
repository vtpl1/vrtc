// Package www exposes static file hosting
package www

import (
	"embed"
	"io/fs"
)

//go:embed dist_orig
var embedUI embed.FS

// GetStaticFS returns static UI files
func GetStaticFS() (fs.FS, error) {
	embedRoot, err := fs.Sub(embedUI, "dist_orig")
	if err != nil {
		return nil, err
	}
	return embedRoot, nil
}
