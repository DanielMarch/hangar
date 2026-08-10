package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/hangar-project/hangar/internal/api"
	v1 "github.com/hangar-project/hangar/internal/api/v1"
)

// newOpenAPICmd emits docs/openapi.json from the Huma router (Phase 15).
// It builds the full API — every internal/api/v1 route group registered,
// exactly as serving traffic would — with Deps.Store left nil throughout:
// route registration only ever closes over the store inside a handler
// body, and generating the spec never calls a handler, so this runs with
// no database connection at all (Principle 10: a generated-but-committed
// artefact must be reproducible in CI without live infrastructure).
func newOpenAPICmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Emit the OpenAPI 3.1 document generated from the Huma router",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mux := http.NewServeMux()
			api.Version = version
			hapi := api.NewAPI(mux, api.Deps{})
			v1.RegisterAll(hapi, api.Deps{})

			spec, err := api.MarshalSpec(hapi)
			if err != nil {
				return fmt.Errorf("openapi: marshaling spec: %w", err)
			}
			if err := os.WriteFile(out, append(spec, '\n'), 0o644); err != nil {
				return fmt.Errorf("openapi: writing %s: %w", out, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "docs/openapi.json", "output path")
	return cmd
}
