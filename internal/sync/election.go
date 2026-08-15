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

// Elect implements ActingCharacterElector for the two entity kinds that
// have no token of their own.
//
// ── PHASE 20.8: THE ALLIANCE BRANCH ──────────────────────────────────────
// This used to reject everything but EntityCorporation, with the note that
// alliance election "is a different candidate pool ... and not this phase's
// scope". Capability #37 is that scope. The pool is every tracked character
// whose corporation is in the alliance — a character's alliance is its
// corporation's, so app.corporation.alliance_id is the whole join — and the
// SCORING below is shared verbatim, because §6.3's ordering (valid token,
// then scopes, then roles, then fewest recent 403s, then lowest id) is about
// the candidate, not about what it is acting for.
//
// Roles are the one asymmetry, and it resolves itself: ESI's alliance routes
// declare no corporation role at all (app.esi_route_role is empty for all
// four), so requiredRoles is empty and satisfiesRoles returns true without
// consulting a corporation. No alliance-specific role logic exists because
// EVE has no alliance-level role that gates these routes.
func (e DBElector) Elect(ctx context.Context, entityKind EntityKind, entityID int64, routeID uuid.UUID) (int64, error) {
	var memberIDs []int64
	switch entityKind {
	case EntityCorporation:
		members, err := e.Store.ListCorporationMembers(ctx, entityID)
		if err != nil {
			return 0, fmt.Errorf("sync: listing members of corporation %d: %w", entityID, err)
		}
		memberIDs = make([]int64, len(members))
		for i, m := range members {
			memberIDs[i] = m.CharacterID
		}
		if len(memberIDs) == 0 {
			return 0, fmt.Errorf("%w: corporation %d has no tracked members", ErrNoEligibleActingCharacter, entityID)
		}
	case EntityAlliance:
		ids, err := e.Store.ListAllianceMemberCharacters(ctx, entityID)
		if err != nil {
			return 0, fmt.Errorf("sync: listing tracked characters in alliance %d: %w", entityID, err)
		}
		memberIDs = ids
		if len(memberIDs) == 0 {
			return 0, fmt.Errorf("%w: alliance %d has no tracked characters", ErrNoEligibleActingCharacter, entityID)
		}
	default:
		return 0, fmt.Errorf("sync: acting-character election is only defined for corporation- and alliance-scoped subscriptions, got %q", entityKind)
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
		return 0, fmt.Errorf("sync: listing acting-character history for %s %d route %s: %w", entityKind, entityID, routeID, err)
	}
	failCount := make(map[int64]int32, len(history))
	for _, h := range history {
		failCount[h.CharacterID] = h.Consecutive403
	}

	candidates := make([]candidate, 0, len(memberIDs))
	for _, characterID := range memberIDs {
		c := candidate{characterID: characterID, consecutive403: failCount[characterID]}

		tok, err := e.Store.GetCharacterToken(ctx, characterID)
		if err == nil {
			c.tokenValid = tok.Valid
		} // no row / other error => tokenValid stays false, never fatal to the whole election

		if c.tokenValid {
			tokenScopes, err := e.Store.ListCharacterTokenScopes(ctx, characterID)
			if err != nil {
				return 0, fmt.Errorf("sync: listing token scopes for character %d: %w", characterID, err)
			}
			c.hasScopes = supersetOf(tokenScopes, requiredScopes)

			// satisfiesRoles takes a CORPORATION id for its CEO check. For an
			// alliance subscription entityID is an alliance id, and passing it
			// would look up a corporation that does not exist — harmless
			// (GetCorporation's error is tolerated) but meaningless. It is
			// only ever reached with a non-empty requiredRoles, which no
			// alliance route has, so the corporation id is the right and only
			// thing to pass here.
			hasRoles, err := e.satisfiesRoles(ctx, entityID, characterID, requiredRoles)
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
		return 0, fmt.Errorf("%w: %s %d, route %s", ErrNoEligibleActingCharacter, entityKind, entityID, routeID)
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
