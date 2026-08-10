package entitlement

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

// GatherWorldState is the ONLY function in this package that touches the
// database (or, transitively, the clock — HasInvalidCharacterToken's
// NOT EXISTS probe reads current row state, nothing time-based itself).
// Everything else in this package (Evaluate) is a pure function over its
// result, which is what makes preview.go trivially correct: same
// WorldState, same Evaluate, different rule set.
//
// Strict Mode's own query is folded in here (docs/02_DATABASE_SCHEMA.md
// §4.1's `NOT EXISTS (... WHERE c.user_id = $1 AND NOT t.valid)`, used
// verbatim via the existing HasInvalidCharacterToken query — never
// re-derived) because it is explicitly one of the things a user's world
// state comprises, per 01_ARCHITECTURE.md §9.1: Strict Mode is a
// precondition, not an eighth source, but it is still a fact ABOUT the
// user that has to be gathered before Evaluate can run.
// internal/provisioning/strictmode.go wraps the same query for callers
// that want a strict-mode verdict on its own, without a full world-state
// gather.
func GatherWorldState(ctx context.Context, s *store.Store, userID uuid.UUID) (WorldState, error) {
	world := WorldState{UserID: userID}

	characters, err := s.ListCharactersForUser(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return WorldState{}, fmt.Errorf("entitlement: listing characters for user %s: %w", userID, err)
	}

	seenCorp := make(map[int64]bool)
	seenAlliance := make(map[int64]bool)
	seenTitle := make(map[string]bool)
	for _, c := range characters {
		world.CharacterIDs = append(world.CharacterIDs, c.CharacterID)

		if c.CorporationID != nil {
			corpID := *c.CorporationID
			if !seenCorp[corpID] {
				seenCorp[corpID] = true
				world.CorporationIDs = append(world.CorporationIDs, corpID)
			}

			titles, err := s.ListCorporationMemberTitles(ctx, corpID, c.CharacterID)
			if err != nil {
				return WorldState{}, fmt.Errorf("entitlement: listing corp titles for character %d: %w", c.CharacterID, err)
			}
			for _, t := range titles {
				// "corporationID:titleID" — title_id alone is only unique
				// within one corporation (evaluate.go's WorldState doc).
				key := strconv.FormatInt(corpID, 10) + ":" + strconv.FormatInt(t.TitleID, 10)
				if !seenTitle[key] {
					seenTitle[key] = true
					world.CorpTitles = append(world.CorpTitles, key)
				}
			}
		}

		if c.AllianceID != nil {
			allianceID := *c.AllianceID
			if !seenAlliance[allianceID] {
				seenAlliance[allianceID] = true
				world.AllianceIDs = append(world.AllianceIDs, allianceID)
			}
		}
	}

	roles, err := s.ListUserRoles(ctx, userID)
	if err != nil {
		return WorldState{}, fmt.Errorf("entitlement: listing direct roles for user %s: %w", userID, err)
	}
	for _, r := range roles {
		world.RoleIDs = append(world.RoleIDs, r.RoleID)
	}

	squadIDs, err := s.ListSquadsForUser(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return WorldState{}, fmt.Errorf("entitlement: listing direct squads for user %s: %w", userID, err)
	}
	world.SquadIDs = squadIDs

	strictModeDenied, err := s.HasInvalidCharacterToken(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return WorldState{}, fmt.Errorf("entitlement: checking strict mode for user %s: %w", userID, err)
	}
	world.StrictModeDenied = strictModeDenied

	return world, nil
}
