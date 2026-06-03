// Package web embeds the built TypeScript UI assets into the binary so Fabrika
// ships as a single self-contained executable (SPECS.md §3). Run `make web`
// (esbuild) to populate web/dist before `go build`.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the built UI rooted at the dist directory (so paths look like
// "index.html", "app.js"). If the UI hasn't been built yet, the returned FS will
// only contain the placeholder; the server still starts and serves the API.
func Assets() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
