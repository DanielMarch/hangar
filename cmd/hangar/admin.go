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
	return cmd
}
