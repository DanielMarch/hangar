package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

// Effect mirrors app.role_grant.effect — HANGAR's own closed two-value
// set (02_DATABASE_SCHEMA.md §4.2's deliberate Principle 14 exception),
// never an open vocabulary.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Grant is one raw (permission, effect) tuple reached via either grant
// path — what db/queries/rbac.sql's ListUserGrants returns, already
// unioning direct user_role grants with squad-derived ones. Multiple
// Grants for the same permission are normal (a user can hold several
// roles, each granting or denying the same permission) and are exactly
// what deny precedence resolves.
type Grant struct {
	Permission string
	Effect     Effect
}

// Resolve is the pure deny-first resolution over an already-fetched grant
// set — no I/O, so it is exactly what TestDenyPrecedesAllowTruthTable
// exercises directly (every combination of grants in, permitted bool out)
// and what TestMaterializedMatchesRecomputed's "from scratch" half calls
// after an independent ListUserGrants fetch, with no dependency on
// whatever is currently sitting in app.effective_permission.
//
// Deny precedence, absolute: a deny on `permission` from ANY grant beats
// any number of allows, from any path. Superuser
// (rbac.SuperuserPermission) is resolved through this SAME mechanism, not
// a bypass branch: holding it (allowed, not denied) implicitly allows
// every OTHER permission that isn't itself explicitly denied — but a deny
// on the permission actually being checked always wins regardless, and a
// deny on the superuser grant itself simply removes it as a fallback.
// This is what makes superuser deniable in a real sense (02_DATABASE_
// SCHEMA.md §4.2 / roadmap: "Superuser is a permission, not a bypass
// branch in code. A bypass branch cannot be denied.").
func Resolve(grants []Grant, permission string) bool {
	var permissionDenied, permissionAllowed bool
	var superuserDenied, superuserAllowed bool
	for _, g := range grants {
		switch {
		case g.Permission == permission && g.Effect == EffectDeny:
			permissionDenied = true
		case g.Permission == permission && g.Effect == EffectAllow:
			permissionAllowed = true
		case g.Permission == SuperuserPermission && g.Effect == EffectDeny:
			superuserDenied = true
		case g.Permission == SuperuserPermission && g.Effect == EffectAllow:
			superuserAllowed = true
		}
	}
	if permissionDenied {
		return false
	}
	if permissionAllowed {
		return true
	}
	return superuserAllowed && !superuserDenied
}

// ResolveAll resolves every permission in `all` against one grant set —
// materialize.go's per-user bulk path. O(len(grants) + len(all)), a
// single pass building the same allow/deny sets Resolve computes inline,
// so materializing HANGAR's ~27-permission closed set for one user never
// re-scans the grant slice per permission.
func ResolveAll(grants []Grant, all []string) map[string]bool {
	denied := make(map[string]bool, len(grants))
	allowed := make(map[string]bool, len(grants))
	for _, g := range grants {
		switch g.Effect {
		case EffectDeny:
			denied[g.Permission] = true
		case EffectAllow:
			allowed[g.Permission] = true
		}
	}
	superuserOK := allowed[SuperuserPermission] && !denied[SuperuserPermission]

	out := make(map[string]bool, len(all))
	for _, p := range all {
		if denied[p] {
			out[p] = false
			continue
		}
		out[p] = allowed[p] || superuserOK
	}
	return out
}

// FetchGrants loads a user's raw grant set from the database (both
// paths — see db/queries/rbac.sql's ListUserGrants doc comment).
func FetchGrants(ctx context.Context, s *store.Store, userID uuid.UUID) ([]Grant, error) {
	rows, err := s.ListUserGrants(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: fetching grants for user %s: %w", userID, err)
	}
	grants := make([]Grant, len(rows))
	for i, r := range rows {
		grants[i] = Grant{Permission: r.Permission, Effect: Effect(r.Effect)}
	}
	return grants, nil
}

// ResolveLive fetches a user's grants and resolves one permission —
// "the query it's built on" plus the pure Resolve above; used where a
// live, from-scratch answer is wanted without touching
// app.effective_permission (the materialize.go recomputation cross-check,
// and any caller that would rather pay one query than trust a
// potentially-stale materialised row).
func ResolveLive(ctx context.Context, s *store.Store, userID uuid.UUID, permission string) (bool, error) {
	grants, err := FetchGrants(ctx, s, userID)
	if err != nil {
		return false, err
	}
	return Resolve(grants, permission), nil
}

// ResolveAllLive is ResolveLive's bulk counterpart, over every permission
// in HANGAR's closed set — what materialize.go calls per affected user.
func ResolveAllLive(ctx context.Context, s *store.Store, userID uuid.UUID) (map[string]bool, error) {
	grants, err := FetchGrants(ctx, s, userID)
	if err != nil {
		return nil, err
	}
	return ResolveAll(grants, AllPermissions()), nil
}
