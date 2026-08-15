package main

// ── DEFECT B52 (PHASE 20.8): THE OTHER COLUMN NOBODY CHECKED ─────────────
//
// B51 was traceability.csv's `verification_test` column citing tests that do
// not exist — 42 of 45 rows. Writing those tests meant reading every row of
// the matrix carefully, and doing that surfaced the same defect one column
// over: `hangar_endpoints` names the /api/v1 endpoints that deliver each
// capability, every value was hand-written, and nothing ever confirmed the
// endpoint EXISTS either.
//
// Measured at the start of Phase 20.8 against docs/openapi.json: of 22
// distinct endpoints named, SEVEN matched no registered path, not even as a
// prefix.
//
//	/api/v1/admin/audit    the audit log is /api/v1/admin/security-log
//	/api/v1/admin/pin      the pin is /api/v1/admin/esi/catalogue/pin
//	/api/v1/admin/routes   the route board is /api/v1/admin/sync/routes
//	/api/v1/admin/tokens   API tokens are user-scoped: /api/v1/api-tokens
//	/api/v1/admin/webhooks webhooks are user-scoped: /api/v1/me/webhooks
//	/api/v1/market/prices  singular; the endpoint is /api/v1/markets/prices
//	/api/v1/meta/locales   DOES NOT EXIST AT ALL, in any spelling
//
// The last one is the interesting one and is why this is a defect rather
// than a typo sweep. Capability #58 is Localisation, and the matrix claimed
// it was delivered by an endpoint that has never been registered. HANGAR's
// locale is an installation-wide boot setting (HANGAR_LOCALE, validated in
// internal/config and resolved to an ESI Accept-Language in internal/i18n),
// not a served resource — so the row's real answer is "no endpoint", and
// saying so is a different claim from naming one. /api/v1/market/prices is
// the B38 shape again: a path pluralised wrongly by hand, never compared to
// the artefact that would have said so.
//
// The fix is the one B51 got: measure, and fail the gate on the count.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// registeredEndpoints reads the paths out of the committed OpenAPI
// document.
//
// docs/openapi.json is the right authority rather than the Huma router
// itself: it is what `make verify-generated` proves is in step with the
// router, it is what web/src/api/schema.d.ts is generated from, and it is
// the contract published to clients. An endpoint absent from it is absent
// from the product's contract whatever the Go code does.
func registeredEndpoints(repoRoot string) (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "docs", "openapi.json"))
	if err != nil {
		return nil, fmt.Errorf("reading docs/openapi.json: %w", err)
	}
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("parsing docs/openapi.json: %w", err)
	}
	if len(spec.Paths) == 0 {
		return nil, fmt.Errorf("docs/openapi.json declares no paths — the check would pass vacuously")
	}
	out := make(map[string]bool, len(spec.Paths))
	for p := range spec.Paths {
		out[p] = true
	}
	return out, nil
}

// checkCapabilityEndpoints reports every capability row naming a HANGAR
// endpoint that is not registered.
//
// A citation satisfies the check two ways, and the second is deliberate
// rather than lenient. It may be a REGISTERED PATH verbatim, or a proper
// PREFIX of one at a path-segment boundary — "/api/v1/admin/sync" names the
// admin sync board, which is served as /sync/routes, /sync/subscriptions and
// /sync/health, and forcing the matrix to list all three would make it a
// worse document without making it a truer one. What the prefix rule does
// NOT admit is a name that matches nothing: "/api/v1/admin/audit" is not a
// prefix of "/api/v1/admin/security-log", and that is exactly the case this
// exists to catch.
//
// A row with NO endpoints is not reported. Several capabilities genuinely
// have none — Localisation is an installation setting, not a resource — and
// an empty list is an honest claim, the same way a parenthetical in the
// verification_test column is.
func checkCapabilityEndpoints(repoRoot string, rows []CapabilityRow) ([]string, error) {
	registered, err := registeredEndpoints(repoRoot)
	if err != nil {
		return nil, err
	}

	var missing []string
	for _, row := range rows {
		for _, endpoint := range row.HangarEndpoints {
			endpoint = strings.TrimSpace(endpoint)
			if endpoint == "" || registered[endpoint] {
				continue
			}
			prefixed := false
			for path := range registered {
				if strings.HasPrefix(path, endpoint+"/") {
					prefixed = true
					break
				}
			}
			if !prefixed {
				missing = append(missing, fmt.Sprintf(
					"capability %d names endpoint %s, which is registered in docs/openapi.json neither verbatim nor as a path prefix", row.ID, endpoint))
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}
