package api

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// MarshalSpec returns huma's generated OpenAPI 3.1 document as JSON.
// cmd/hangar/openapi.go calls this once every internal/api/v1 route group
// has registered against the huma.API instance NewAPI built — no live
// *store.Store is required anywhere in that path (registration only closes
// over it inside handler bodies), so `hangar openapi --out docs/openapi.json`
// runs with no database connection at all (Principle 10: a
// generated-but-committed artefact must be reproducible in CI without live
// infrastructure).
func MarshalSpec(a huma.API) ([]byte, error) {
	spec, err := a.OpenAPI().MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("api: marshaling openapi spec: %w", err)
	}
	return spec, nil
}
