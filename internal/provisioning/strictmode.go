package provisioning

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

// CheckStrictMode wraps 02_DATABASE_SCHEMA.md §4.1's Strict Mode probe —
// "any alt" — for callers that want a standalone verdict without paying
// for a full entitlement/world-state gather (e.g. an admin-facing
// diagnostic answering "why is this user denied on every platform").
// entitlement.GatherWorldState calls the exact same generated query
// (HasInvalidCharacterToken) directly as part of its own bundle, per its
// own doc comment — this is not the only call site, deliberately: both
// wrap 02_DATABASE_SCHEMA.md §4.1's `NOT EXISTS (...)` verbatim via the
// same sqlc-generated method rather than either one re-deriving it.
func CheckStrictMode(ctx context.Context, s *store.Store, userID uuid.UUID) (bool, error) {
	denied, err := s.HasInvalidCharacterToken(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return false, fmt.Errorf("provisioning: checking strict mode for user %s: %w", userID, err)
	}
	return denied, nil
}
