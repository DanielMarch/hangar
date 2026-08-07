package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that a locally running `hangar serve` is healthy (used as the Docker HEALTHCHECK)",
		// distroless has no shell and no curl (Dockerfile's runtime stage),
		// so the container's HEALTHCHECK runs this subcommand instead of a
		// shell one-liner.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHealthcheck()
		},
	}
}

func runHealthcheck() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/healthz", localizeAddr(cfg.HTTPAddr))
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: unexpected status %d from %s", resp.StatusCode, url)
	}
	return nil
}

// localizeAddr rewrites a listen address's wildcard host (0.0.0.0, ::, or
// empty) to 127.0.0.1 so the healthcheck — which always runs inside the same
// container as `serve` — dials a concrete loopback address.
func localizeAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || strings.HasPrefix(host, "[::") {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
