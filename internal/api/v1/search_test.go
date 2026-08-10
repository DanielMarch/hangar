package v1

import (
	"context"
	"errors"
	"testing"

	"github.com/hangar-project/hangar/internal/api"
)

// TestSearchRequiresActingCharacter — Phase 15 exit criterion: "character-less
// session gets the specific error; every search is audited". This proves
// the unauthenticated branch (no session at all) without a live database:
// resolveActingCharacter's first check is userIDFromCtx, which short-circuits
// before ever touching deps.Store — deps.Store stays nil throughout, the
// same "never dereference outside a real request" contract every other
// handler in this package relies on for cmd/hangar/openapi.go's spec-only
// build path.
//
// The authenticated-but-no-main-character branch (a real user row with
// MainCharacterID == nil) additionally needs deps.Store.GetUser, i.e. a
// live database — that path belongs in an _integration_test.go file
// alongside this package's other DB-backed suites
// (authorize_integration_test.go, public_mumble_auth_integration_test.go),
// not duplicated here without one.
func TestSearchRequiresActingCharacter(t *testing.T) {
	ctx := context.Background() // no middleware.WithUserID applied — an unauthenticated request

	_, _, err := resolveActingCharacter(ctx, api.Deps{Store: nil})
	if err == nil {
		t.Fatal("expected an error for a character-less/unauthenticated session")
	}
	if !errors.Is(err, api.ErrActingCharacterRequired) {
		t.Fatalf("expected the specific ErrActingCharacterRequired, got %v", err)
	}
}
