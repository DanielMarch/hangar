package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestGeneratedTypesAreClean — Phase 15 exit criterion: "openapi-typescript
// produces api.d.ts with no errors; make verify-generated diff is empty".
//
// The heavyweight half of this (actually invoking openapi-typescript and
// diffing git status) is `make types` + `make verify-generated` — a
// pnpm/node round trip that doesn't belong inside `go test`'s fast unit
// suite. This test is the fast, always-on half: docs/openapi.json parses
// as valid JSON (a schema openapi-typescript can't even read fails before
// it ever gets to "no errors"), is OpenAPI 3.1, and every path Phase 15
// registers is present — proving the committed artefact actually reflects
// the Huma router rather than a stale stub. The generated
// web/src/api/schema.d.ts is checked for existence and a sane structural
// shape (an exported `paths` interface, no literal "error" markers
// openapi-typescript would have emitted) as a second, independent check
// that the committed pair are the real generated output, not hand-edited.
func TestGeneratedTypesAreClean(t *testing.T) {
	root := findRepoRoot(t)

	specPath := root + "/docs/openapi.json"
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading %s: %v", specPath, err)
	}
	var spec struct {
		OpenAPI string                 `json:"openapi"`
		Paths   map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("docs/openapi.json is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf(`expected "openapi": "3.1.x", got %q`, spec.OpenAPI)
	}
	if len(spec.Paths) < 100 {
		t.Fatalf("expected the full §6.1-6.8 surface (100+ paths), got %d — docs/openapi.json looks stale", len(spec.Paths))
	}
	for _, want := range []string{
		"/api/v1/support/search",
		"/api/v1/characters/{id}",
		"/api/v1/corporations/{id}",
		"/api/v1/admin/alerts/dead-letter",
		"/api/v1/meta/esi-status",
	} {
		if _, ok := spec.Paths[want]; !ok {
			t.Errorf("expected %s in docs/openapi.json's paths, not found", want)
		}
	}

	schemaPath := root + "/web/src/api/schema.d.ts"
	schemaRaw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaPath, err)
	}
	schema := string(schemaRaw)
	if !strings.Contains(schema, "export interface paths") {
		t.Error("web/src/api/schema.d.ts does not look like openapi-typescript output (no `export interface paths`)")
	}
	if strings.Contains(strings.ToLower(schema), "openapi-typescript error") {
		t.Error("web/src/api/schema.d.ts contains an openapi-typescript error marker")
	}
	if !strings.Contains(schema, "/api/v1/support/search") {
		t.Error("web/src/api/schema.d.ts is missing a path present in docs/openapi.json — the two are out of sync")
	}
}

// TestNoUnimplementedEndpoints is Phase 15.1's exit criterion: "No
// endpoint in the final docs/openapi.json may respond 501."
//
// Phase 15 shipped six routes that were registered (so the OpenAPI surface
// matched SRS §6) but answered 501 because their backing schema, query or
// primitive did not exist. That is a defensible *interim* state and an
// indefensible *finished* one — it means the published contract advertises
// capability the server does not have. Phase 15.1 closed all six; this
// test is what stops a seventh appearing.
//
// If a future phase genuinely needs to register-before-implementing, this
// test failing is the intended conversation, not an obstacle to route
// around.
func TestNoUnimplementedEndpoints(t *testing.T) {
	root := findRepoRoot(t)
	raw, err := os.ReadFile(root + "/docs/openapi.json")
	if err != nil {
		t.Fatalf("reading docs/openapi.json: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string                     `json:"operationId"`
			Responses   map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("docs/openapi.json is not valid JSON: %v", err)
	}
	for path, item := range spec.Paths {
		for method, op := range item {
			if _, has := op.Responses["501"]; has {
				t.Errorf("%s %s (%s) declares a 501 response — every registered endpoint must be implemented (Phase 15.1 exit criterion)",
					strings.ToUpper(method), path, op.OperationID)
			}
		}
	}
}
