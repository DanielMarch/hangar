// Package entitlement is HANGAR's access-provisioning entitlement engine
// (01_ARCHITECTURE.md §9.1): a pure function over a user's "world state"
// and the live (or, for preview.go, a hypothetical) app.entitlement_rule
// set. Evaluate itself performs no I/O, touches no clock, and uses no
// randomness — sources.go is the ONLY file in this package allowed to
// touch the database, and strictmode.go's precondition is folded into
// WorldState before Evaluate ever runs, not threaded through as a special
// case inside it.
package entitlement

import (
	"strconv"

	"github.com/google/uuid"
)

// Source kinds, mirroring app.entitlement_rule.source_kind's CHECK
// constraint after db/migrations/00037_phase11_entitlement_source_fixup.sql
// (see that migration's comment for why 00007's original seven —
// 'permission'/'manual' in place of 'corp_title'/'public' — were wrong).
// The seven grant sources, verbatim from 01_ARCHITECTURE.md §9.1 /
// 00_SRS_v3.1.md: "user, role, corporation, alliance, corp title, squad,
// public."
const (
	SourceUser        = "user"
	SourceRole        = "role"
	SourceCorporation = "corporation"
	SourceAlliance    = "alliance"
	SourceCorpTitle   = "corp_title"
	SourceSquad       = "squad"
	SourcePublic      = "public"
)

// Effects, mirroring app.entitlement_rule.effect's CHECK constraint.
const (
	EffectGrant = "grant"
	EffectDeny  = "deny"
)

// Rule is one (source, group, effect) tuple — the Go-side shape of an
// app.entitlement_rule row, deliberately independent of internal/store/gen
// so this package has zero database dependency (sources.go/preview.go
// convert gen.AppEntitlementRule to this at the I/O boundary).
type Rule struct {
	GroupID    uuid.UUID
	SourceKind string
	SourceRef  string
	Effect     string
}

// WorldState is everything about one user that a Rule's source can match,
// gathered by sources.go. StrictModeDenied being true overrides every rule
// unconditionally — Strict Mode is a precondition, not an eighth source
// (01_ARCHITECTURE.md §9.1): "if the per-character NOT EXISTS query finds
// any invalid ESI token on any of the user's characters, platform access
// is denied wholesale."
type WorldState struct {
	UserID uuid.UUID

	// CharacterIDs is every character belonging to this user — not
	// consulted by Evaluate directly, but kept here for callers/tests that
	// want to see what sources.go gathered.
	CharacterIDs []int64

	// RoleIDs is every RBAC role held DIRECTLY (app.user_role) — never
	// roles reached only via a squad's app.squad_role grant. See
	// evaluate.go's package doc and READ FIRST #3: a `role` entitlement
	// rule and Phase 10's squad-grants-a-role RBAC path are independent
	// mechanisms. A role held only through squad membership does NOT
	// satisfy a `role` entitlement rule — only a `squad` rule naming that
	// squad does.
	RoleIDs []uuid.UUID

	// SquadIDs is every squad this user's characters are direct members
	// of (app.squad_member), independent of whether that squad happens to
	// grant an RBAC role.
	SquadIDs []uuid.UUID

	// CorporationIDs / AllianceIDs are the distinct corp/alliance ids
	// across all of this user's characters.
	CorporationIDs []int64
	AllianceIDs    []int64

	// CorpTitles is "corporationID:titleID" for every title held by any
	// of this user's characters — title_id alone is only unique within
	// one corporation (app.corporation_title's PRIMARY KEY is
	// (corporation_id, title_id)), so the pair is what a corp_title rule's
	// source_ref must encode and what this must match against.
	CorpTitles []string

	// StrictModeDenied is strictmode.go's verdict: true if ANY of this
	// user's characters holds an invalid ESI token ("any alt" —
	// 01_ARCHITECTURE.md §9.1).
	StrictModeDenied bool
}

func contains64(haystack []int64, needle int64) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func containsUUID(haystack []uuid.UUID, needle uuid.UUID) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// sourceMatches reports whether rule's (source_kind, source_ref) matches
// world — everything Evaluate needs to know about one rule before
// grant/deny precedence comes into it. Malformed source_ref values (an id
// that fails to parse) simply never match — an entitlement rule is
// authored through admin_provisioning.go, which is responsible for
// validating source_ref shape at write time; a rule that somehow reached
// this function malformed anyway is treated as "matches nobody" rather
// than panicking or erroring evaluation for every other rule.
func sourceMatches(rule Rule, world WorldState) bool {
	switch rule.SourceKind {
	case SourcePublic:
		return true
	case SourceUser:
		return rule.SourceRef == world.UserID.String()
	case SourceRole:
		id, err := uuid.Parse(rule.SourceRef)
		if err != nil {
			return false
		}
		return containsUUID(world.RoleIDs, id)
	case SourceSquad:
		id, err := uuid.Parse(rule.SourceRef)
		if err != nil {
			return false
		}
		return containsUUID(world.SquadIDs, id)
	case SourceCorporation:
		id, err := strconv.ParseInt(rule.SourceRef, 10, 64)
		if err != nil {
			return false
		}
		return contains64(world.CorporationIDs, id)
	case SourceAlliance:
		id, err := strconv.ParseInt(rule.SourceRef, 10, 64)
		if err != nil {
			return false
		}
		return contains64(world.AllianceIDs, id)
	case SourceCorpTitle:
		return containsString(world.CorpTitles, rule.SourceRef)
	default:
		return false
	}
}

// Evaluate is the pure entitlement resolution: deny-beats-grant per group,
// exactly Phase 10's resolve.go shape (internal/rbac.Resolve) applied to
// app.entitlement_rule's independent source set instead of app.role_grant.
// The returned map is set-shaped — a key is present with value true iff
// the group is granted; a group matched only by deny rules (or matched by
// nothing) is simply absent, never present with value false, so callers
// can compare group sets with a plain map lookup or `len`/range.
func Evaluate(world WorldState, rules []Rule) map[uuid.UUID]bool {
	granted := make(map[uuid.UUID]bool)
	if world.StrictModeDenied {
		return granted // wholesale denial — no rule is even considered
	}

	denied := make(map[uuid.UUID]bool)
	for _, rule := range rules {
		if !sourceMatches(rule, world) {
			continue
		}
		switch rule.Effect {
		case EffectDeny:
			denied[rule.GroupID] = true
		case EffectGrant:
			granted[rule.GroupID] = true
		}
	}
	for groupID := range denied {
		delete(granted, groupID)
	}
	return granted
}
