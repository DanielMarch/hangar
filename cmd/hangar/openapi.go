package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newOpenAPICmd emits docs/openapi.json from the Huma router. The router
// doesn't exist until Phase 15, so this always errors for now; `make
// openapi` already treats a non-zero exit here as "not implemented yet" and
// leaves the Phase 0 stub at docs/openapi.json untouched (see Makefile).
func newOpenAPICmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Emit the OpenAPI 3.1 document generated from the Huma router (Phase 15)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("openapi: not implemented until Phase 15 (Huma router); out=%q", out)
		},
	}
	cmd.Flags().StringVar(&out, "out", "docs/openapi.json", "output path")
	return cmd
}
