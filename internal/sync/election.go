package sync

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

// ErrNoEligibleActingCharacter is returned when no corporation member has a
// valid token honouring the route's required scopes and roles — §6.3's
// "no eligible character exists" case. The caller (a Worker) must not
// synthesise a character in this case; the subscription simply produces no
// data until a director's token becomes eligible.
var ErrNoEligibleActingCharacter = errors.New("sync: no eligible acting character for this (entity, route)")

// DBElector is the real ActingCharacterElector (01_ARCHITECTURE.md §6.3),
// reading candidate directors from app.corporation_member, their tokens
// from app.character_token/character_token_scope, their corporation roles
// from app.corporation_role, and their recent-403 history from
// app.sync_acting_character_history (Phase 8's addition — see
// 00031_phase8_acting_character_history.sql).
//
// Determinism (§6.3's whole point) comes from candidates() building one
// fully-ordered slice, sorted once by a single comparator that encodes
// every tiebreak level in the order the architecture doc specifies. The
// same DB state always produces the same ordering and therefore the same
// winner — no map iteration, no time-based jitter, nothing nondeterministic
// enters the comparison.
type DBElector struct {
	Store *store.Store
}

// candidate is one corporation member scored against one route.
type candidate struct {
	characterID    int64
	tokenValid     bool
	hasScopes      bool
	hasRoles       bool
	consecutive403 int32
}

// eligible reports whether this candidate actually satisfies every hard
// requirement — a valid token, every required scope, every required role.
// A candidate can rank first in the sort order below and still be
// ineligible (e.g. the entire corporation has no valid director token);
// Elect must check this before declaring a winner, not just take index 0.
func (c candidate) eligible() bool {
	return c.tokenValid && c.hasScopes && c.hasRoles
}

// Elect implements ActingCharacterElector. It only ever consults
// entityKind == EntityCorporation — alliance-scoped election, if HANGAR
// ever needs one, is a different candidate pool (alliance member
// corporations' directors) and not this phase's scope.
func (e DBElector) Elect(ctx context.Context, entityKind EntityKind, entityID int64, routeID uuid.UUID) (int64, error) {
	if entityKind != EntityCorporation {
		return 0, fmt.Errorf("sync: acting-character election is only defined for corporation-scoped subscriptions, got %q", entityKind)
	}

	members, err := e.Store.ListCorporationMembers(ctx, entityID)
	if err != nil {
		return 0, fmt.Errorf("sync: listing members of corporation %d: %w", entityID, err)
	}
	if len(members) == 0 {
		return 0, fmt.Errorf("%w: corporation %d has no tracked members", ErrNoEligibleActingCharacter, entityID)
	}

	requiredScopes, err := e.Store.ListEsiRouteScopes(ctx, routeID)
	if err != nil {
		return 0, fmt.Errorf("sync: listing required scopes for route %s: %w", routeID, err)
	}
	requiredRoles, err := e.Store.ListEsiRouteRoles(ctx, routeID)
	if err != nil {
		return 0, fmt.Errorf("sync: listing required roles for route %s: %w", routeID, err)
	}

	history, err := e.Store.ListActingCharacterHistory(ctx, string(entityKind), entityID, routeID)
	if err != nil {
		return 0, fmt.Errorf("sync: listing acting-character history for corporation %d route %s: %w", entityID, routeID, err)
	}
	failCount := make(map[int64]int32, len(history))
	for _, h := range history {
		failCount[h.CharacterID] = h.Consecutive403
	}

	candidates := make([]candidate, 0, len(members))
	for _, m := range members {
		c := candidate{characterID: m.CharacterID, consecutive403: failCount[m.CharacterID]}

		tok, err := e.Store.GetCharacterToken(ctx, m.CharacterID)
		if err == nil {
			c.tokenValid = tok.Valid
		} // no row / other error => tokenValid stays false, never fatal to the whole election

		if c.tokenValid {
			tokenScopes, err := e.Store.ListCharacterTokenScopes(ctx, m.CharacterID)
			if err != nil {
				return 0, fmt.Errorf("sync: listing token scopes for character %d: %w", m.CharacterID, err)
			}
			c.hasScopes = supersetOf(tokenScopes, requiredScopes)

			hasRoles, err := e.satisfiesRoles(ctx, entityID, m.CharacterID, requiredRoles)
			if err != nil {
				return 0, err
			}
			c.hasRoles = hasRoles
		}

		candidates = append(candidates, c)
	}

	// §6.3's ordering, applied as a single deterministic total order:
	// token valid -> has required scopes -> has required roles -> fewest
	// recent 403s -> lowest character_id. Each boolean sorts "true" (better)
	// before "false"; the two integer fields sort ascending.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.tokenValid != b.tokenValid {
			return a.tokenValid // true first
		}
		if a.hasScopes != b.hasScopes {
			return a.hasScopes
		}
		if a.hasRoles != b.hasRoles {
			return a.hasRoles
		}
		if a.consecutive403 != b.consecutive403 {
			return a.consecutive403 < b.consecutive403
		}
		return a.characterID < b.characterID
	})

	winner := candidates[0]
	if !winner.eligible() {
		return 0, fmt.Errorf("%w: corporation %d, route %s", ErrNoEligibleActingCharacter, entityID, routeID)
	}
	return winner.characterID, nil
}

// RoleDirector is EVE's highest corporation role. A Director holds every
// other corporation role implicitly — the in-game role editor cannot even
// express "Director without Accountant" — so ESI never enumerates the
// subordinate roles for one, and a literal set-membership test against the
// returned list wrongly finds them absent.
const RoleDirector = "Director"

// satisfiesRoles reports whether a character holds every role a route
// requires.
//
// ── DEFECTS B45 AND B46, BOTH FOUND BY A REAL CEO'S TOKEN ────────────────
//
// B45, THE SOURCE. This read app.corporation_role, which has NO PRODUCTION
// WRITER anywhere in the codebase — it is permanently empty, so hasRoles
// was false for every candidate on every route, and no election could ever
// succeed even with a populated candidate pool. The roles ESI actually
// returns arrive through GET /characters/{character_id}/roles, a
// CHARACTER-scoped route that does run, and land in app.character_role
// (216 rows for the first real character authorised against this build).
// That is the authoritative answer to "what corporation roles does this
// character hold", and it is what this now reads.
//
// B46, THE HIERARCHY. Membership in the returned list is not the whole
// test, because EVE's corporation roles are hierarchical and ESI reports
// them literally:
//
//   - the CEO holds every role, always, and cannot be stripped of any;
//   - a Director likewise holds every role implicitly.
//
// So a route requiring "Accountant" must accept a CEO or a Director whose
// role list does not contain the string "Accountant", because in the game
// they can perform the action. Treating the list literally locks the single
// most privileged character in a corporation out of the routes they are
// most likely to be the only one able to serve.
//
// The CEO check is by app.corporation.ceo_id rather than by role, because
// that is the only place the relationship is recorded. It is nil-tolerant:
// ceo_id is populated by a corporation-scoped route, so on a fresh
// installation it is still NULL at the moment of the first election, and a
// CEO who has been granted the Director role explicitly (as EVE does) is
// caught by the Director branch regardless.
func (e DBElector) satisfiesRoles(ctx context.Context, corporationID, characterID int64, requiredRoles []string) (bool, error) {
	if len(requiredRoles) == 0 {
		return true, nil
	}

	corp, err := e.Store.GetCorporation(ctx, corporationID)
	if err == nil && corp.CeoID != nil && *corp.CeoID == characterID {
		return true, nil
	} // no corporation row yet is not fatal — fall through to the role list

	roles, err := e.Store.ListCharacterRoles(ctx, characterID)
	if err != nil {
		return false, fmt.Errorf("sync: listing corporation roles for character %d: %w", characterID, err)
	}
	roleSet := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		roleSet[r.Role] = struct{}{}
		if r.Role == RoleDirector {
			return true, nil
		}
	}

	for _, req := range requiredRoles {
		if _, ok := roleSet[req]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// supersetOf reports whether have contains every element of want.
func supersetOf(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, s := range have {
		set[s] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}
