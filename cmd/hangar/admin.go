package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative and diagnostic subcommands",
	}
	cmd.AddCommand(newAdminVerifyIdentifierTypesCmd())
	return cmd
}

// newAdminVerifyIdentifierTypesCmd is Principle 13's enforcement point
// (docs/01_ARCHITECTURE.md §17): every generated identifier column must match
// the ingested OpenAPI spec's declared type. It has no route catalogue to
// check against until Phase 2 ingests one, so it always errors for now;
// `make check-identifiers` already guards its invocation on the spec
// snapshot existing and skips this command entirely until then.
func newAdminVerifyIdentifierTypesCmd() *cobra.Command {
	var specPath string
	cmd := &cobra.Command{
		Use:   "verify-identifier-types",
		Short: "Verify every generated identifier column matches the ingested OpenAPI spec (Phase 2)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("admin verify-identifier-types: not implemented until Phase 2 (route catalogue ingest); spec=%q", specPath)
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "path to the captured OpenAPI spec snapshot")
	return cmd
}
