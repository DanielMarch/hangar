// Command hangar is the single binary for Project HANGAR: an HTTP API server,
// a background job worker, a sync scheduler, and administrative tooling,
// selected by subcommand (SRS §3.2). See root.go for the command tree.
package main

import (
	"fmt"
	"os"
)

// version, commit and buildDate are injected via -ldflags at build time
// (Makefile's LDFLAGS). "dev"/"unknown" are the values seen in a plain
// `go build` or `go run` outside the release pipeline.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
