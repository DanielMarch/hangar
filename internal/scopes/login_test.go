package scopes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// embeddedSpec reads the same snapshot cmd/hangar falls back to. Read from
// disk rather than imported, so internal/scopes does not depend on
// internal/esi/catalogue for a test fixture.
func embeddedSpec(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "esi", "catalogue", "embedded", "openapi.snapshot.json"))
	if err != nil {
		t.Fatalf("reading embedded snapshot: %v", err)
	}
	return b
}

// TestFromSpecTakesReadScopesOnly is the whole point of the GET filter, and
// the reason defect B37's fix is not simply "ask for every scope on every
// path we touch".
//
// ESI declares write scopes on the SAME upstream_path as the reads:
// /characters/{id}/contacts carries read_contacts on GET and write_contacts
// on POST/PUT/DELETE; /characters/{id}/mail carries send_mail on POST.
// HANGAR issues no non-GET ESI call anywhere, so a path-level sweep would
// make every user grant three permissions the software cannot exercise —
// and users are right to refuse an application that asks for more than it
// needs.
func TestFromSpecTakesReadScopesOnly(t *testing.T) {
	spec := embeddedSpec(t)

	required, _, err := FromSpec(spec, []string{
		"/characters/{character_id}/contacts",
		"/characters/{character_id}/mail",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, scope := range required {
		if strings.Contains(scope, "write") ||
			strings.Contains(scope, "send_mail") ||
			strings.Contains(scope, "organize_mail") {
			t.Errorf("FromSpec returned the write scope %q — these paths carry write scopes on their "+
				"non-GET operations, and HANGAR makes no non-GET ESI call", scope)
		}
	}
	if !slices.Contains(required, "esi-characters.read_contacts.v1") {
		t.Errorf("expected the GET scope esi-characters.read_contacts.v1, got %v", required)
	}
	if !slices.Contains(required, "esi-mail.read_mail.v1") {
		t.Errorf("expected the GET scope esi-mail.read_mail.v1, got %v", required)
	}
}

// TestFromSpecReportsPathsAbsentFromTheSpec is defect B38's detector. Two
// sync-set paths had been pluralised into non-existence
// (/calendar/events for /calendar, /projects/{id}/contributions for
// /contributors). A path the spec does not contain contributes no scopes,
// so without this it fails by producing slightly less than it should —
// the quietest failure mode there is.
func TestFromSpecReportsPathsAbsentFromTheSpec(t *testing.T) {
	spec := embeddedSpec(t)

	_, missing, err := FromSpec(spec, []string{
		"/characters/{character_id}/calendar",        // real
		"/characters/{character_id}/calendar/events", // B38's pluralised ghost
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(missing, "/characters/{character_id}/calendar/events") {
		t.Errorf("a path absent from the spec must be reported, got missing=%v", missing)
	}
	if slices.Contains(missing, "/characters/{character_id}/calendar") {
		t.Errorf("the real path must not be reported missing, got missing=%v", missing)
	}
}

// TestFromSpecToleratesTrailingSlashes — app.esi_route.upstream_path is
// stored verbatim (Principle 5) and the spec is not consistent about
// trailing slashes, so matching must try both rather than normalise one
// into the other and quietly lose a route.
func TestFromSpecToleratesTrailingSlashes(t *testing.T) {
	spec := []byte(`{"paths":{"/widgets/":{"get":{"security":[{"OAuth2":["esi-widgets.read.v1"]}]}}}}`)

	for _, probe := range []string{"/widgets", "/widgets/"} {
		required, missing, err := FromSpec(spec, []string{probe})
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) != 0 {
			t.Errorf("%q reported missing", probe)
		}
		if len(required) != 1 || required[0] != "esi-widgets.read.v1" {
			t.Errorf("%q resolved to %v, want [esi-widgets.read.v1]", probe, required)
		}
	}
}

// TestFromSpecIsDeterministic — the scope set ends up in an authorization
// URL, and an unstable order would make two identical logins produce
// different URLs, which is needlessly hard to debug and defeats caching.
func TestFromSpecIsDeterministic(t *testing.T) {
	spec := embeddedSpec(t)
	paths := []string{
		"/characters/{character_id}/skills",
		"/characters/{character_id}/assets",
		"/characters/{character_id}/wallet/journal",
	}
	first, _, err := FromSpec(spec, paths)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, _, err := FromSpec(spec, paths)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(first, again) {
			t.Fatalf("FromSpec is not deterministic: %v then %v", first, again)
		}
	}
}

// TestFromSpecRejectsAMalformedSpec — the fallback path reads a file, and a
// truncated or corrupted snapshot must fail the login loudly rather than
// resolve to the empty set, which IS defect B37.
func TestFromSpecRejectsAMalformedSpec(t *testing.T) {
	if _, _, err := FromSpec([]byte("{not json"), []string{"/anything"}); err == nil {
		t.Error("a malformed spec must be an error, never an empty scope set")
	}
}

// TestUnknownPathYieldsNoScopesButIsReported guards the interaction of the
// two returns: a caller that ignores `missing` must not silently receive a
// shorter list it believes is complete.
func TestUnknownPathYieldsNoScopesButIsReported(t *testing.T) {
	spec := []byte(`{"paths":{"/known":{"get":{"security":[{"OAuth2":["a.v1"]}]}}}}`)
	required, missing, err := FromSpec(spec, []string{"/known", "/unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 1 || required[0] != "a.v1" {
		t.Errorf("required = %v, want [a.v1]", required)
	}
	if len(missing) != 1 || missing[0] != "/unknown" {
		t.Errorf("missing = %v, want [/unknown]", missing)
	}
}

// TestSpecWithNoSecurityIsNotAnError — public routes (corporation history,
// alliance names) legitimately declare no security, and HANGAR syncs some
// of them. Zero scopes from a real path is a valid answer; only a path that
// does not exist is a problem.
func TestSpecWithNoSecurityIsNotAnError(t *testing.T) {
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	spec := []byte(`{"paths":{"/public":{"get":{}}}}`)
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	required, missing, err := FromSpec(spec, []string{"/public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 0 || len(missing) != 0 {
		t.Errorf("a public route should yield no scopes and no missing entry, got required=%v missing=%v", required, missing)
	}
}
