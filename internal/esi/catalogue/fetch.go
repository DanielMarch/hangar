package catalogue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// EsiBaseURL is ESI's production host. The full gateway (Phase 3: caching,
// retries, circuit breakers) does not exist yet — Boot needs exactly two
// unauthenticated meta calls to discover the catalogue, so it talks to ESI
// directly through a plain *http.Client rather than waiting on Phase 3.
const EsiBaseURL = "https://esi.evetech.net"

// CompatibilityDatesPath is the discovery endpoint (01_ARCHITECTURE.md
// §5.1, step 1). It takes no X-Compatibility-Date header — it is the one
// call that predates having a date to send.
const CompatibilityDatesPath = "/meta/compatibility-dates"

// OpenAPIPath is the spec endpoint (step 2). It is fetched at D_max, never
// at the app pin — pinning discovery blinds the catalogue permanently.
const OpenAPIPath = "/meta/openapi.json"

// fetchCompatibilityDates performs step 1 of the boot sequence: GET
// {baseURL}/meta/compatibility-dates, no X-Compatibility-Date header.
// baseURL is a parameter (rather than always EsiBaseURL) purely so
// TestSpecFetchedAtDMaxNotAppPin and TestOfflineBootUsesEmbeddedSnapshot
// can point it at an httptest server instead of the real ESI host.
func fetchCompatibilityDates(ctx context.Context, client *http.Client, baseURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+CompatibilityDatesPath, nil)
	if err != nil {
		return nil, fmt.Errorf("catalogue: building compatibility-dates request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalogue: fetching compatibility-dates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("catalogue: compatibility-dates returned %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		CompatibilityDates []string `json:"compatibility_dates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("catalogue: decoding compatibility-dates response: %w", err)
	}
	if len(payload.CompatibilityDates) == 0 {
		return nil, fmt.Errorf("catalogue: compatibility-dates response listed zero dates")
	}
	return payload.CompatibilityDates, nil
}

// fetchOpenAPISpec performs step 2 of the boot sequence: GET
// {baseURL}/meta/openapi.json with X-Compatibility-Date: dMax. The caller
// MUST pass D_max here, never the app pin (01_ARCHITECTURE.md §5.1) — this
// function takes the header value as an explicit parameter rather than
// reading a package-level "current pin" precisely so that mistake cannot
// compile.
func fetchOpenAPISpec(ctx context.Context, client *http.Client, baseURL, dMax string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+OpenAPIPath, nil)
	if err != nil {
		return nil, fmt.Errorf("catalogue: building openapi.json request: %w", err)
	}
	req.Header.Set("X-Compatibility-Date", dMax)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalogue: fetching openapi.json: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("catalogue: openapi.json returned %d: %s", resp.StatusCode, body)
	}
	specBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("catalogue: reading openapi.json body: %w", err)
	}
	if len(specBytes) == 0 {
		return nil, fmt.Errorf("catalogue: openapi.json returned an empty body")
	}
	return specBytes, nil
}
