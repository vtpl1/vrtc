package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vtpl1/vrtc/internal/edge"
)

func main() {
	out := flag.String("out", "docs/openapi", "directory to write openapi.json and openapi.yaml")

	flag.Parse()

	if err := edge.ExportOpenAPI(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
