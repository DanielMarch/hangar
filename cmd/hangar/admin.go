package main

import (
	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative and diagnostic subcommands",
	}
	cmd.AddCommand(newAdminVerifyIdentifierTypesCmd())
	cmd.AddCommand(newAdminBootstrapTokenCmd())
	cmd.AddCommand(newAdminIngestCatalogueCmd())
	// Phase 20.1.1 (defect B42): the operator half of subscription
	// reconciliation; `serve` runs the automatic half.
	cmd.AddCommand(newAdminSyncCmd())
	return cmd
}
