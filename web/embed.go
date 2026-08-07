// Package web embeds the built SPA so the Go binary can serve it directly via
// embed.FS — no separate web root, matching the Dockerfile's single-image
// contract (SRS §9.1: "the image serves no separate web root").
//
// Before `pnpm run build` has ever run, web/dist holds only the tracked
// .gitkeep placeholder (see web/dist/.gitkeep and ../.gitignore), so
// `go build`/`go test` succeed on a fresh checkout without requiring Node.
// `make build`/`make web` and the Dockerfile's SPA stage always build the
// real assets first; Vite's emptyOutDir replaces this placeholder with them.
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
