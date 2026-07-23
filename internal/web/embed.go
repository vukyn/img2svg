// Package web embeds the built React UI into the binary at compile time, so the
// server is a single self-contained artifact (no need to ship internal/web/dist
// alongside the binary or run from a specific working directory).
//
// The assets are produced by `make build-web` into internal/web/dist. That step
// MUST run before `go build` — go:embed reads the files at compile time. A
// committed .gitkeep keeps the directory (and the embed pattern) valid on a fresh
// checkout before any build; the real bundle then fills it in.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the built UI rooted at the dist directory, so lookups are
// "index.html", "assets/…". Panics if the embed root is malformed (a programmer
// error in the embed directive, not a runtime condition).
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
