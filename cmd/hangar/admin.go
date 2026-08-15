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
	// Phase 20.5 (defect B22): internal/sde had no production caller at all.
	// The import is an OPERATOR COMMAND — see admin_import_sde.go's header for
	// why not a startup step and not a scheduled job — and sde-status is how
	// an operator finds out that a never-imported installation is rendering
	// ids rather than names.
	cmd.AddCommand(newAdminImportSDECmd())
	cmd.AddCommand(newAdminSDEStatusCmd())
	return cmd
}
