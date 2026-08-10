// Package rbac implements Phase 10: SQL-backed grant resolution with
// absolute deny precedence, materialisation of app.effective_permission,
// and the mutation wrappers that keep the two consistent.
//
// 02_DATABASE_SCHEMA.md §4.2 defines exactly two paths by which a role
// reaches a user — direct (app.user_role) and squad-derived
// (app.squad_member -> app.character.user_id -> app.squad_role) — not the
// seven-source split described in 01_ARCHITECTURE.md §9.1, which is a
// different model (app.entitlement_rule) belonging to Phase 11's
// provisioning entitlement engine. Every "grant source" reference in this
// package means one of those two paths; nothing here reads
// app.entitlement_rule.
package rbac

import "github.com/hangar-project/hangar/internal/domain"

// AllPermissions returns every permission name in HANGAR's closed set
// (internal/domain.Permissions is the source of truth; this package never
// duplicates it). materialize.go uses this to know the full set of rows
// to write per affected user.
func AllPermissions() []string {
	names := make([]string, len(domain.Permissions))
	for i, p := range domain.Permissions {
		names[i] = p.Name
	}
	return names
}

// SuperuserPermission re-exports domain.SuperuserPermission so callers of
// this package never need to import internal/domain themselves just to
// pass the superuser sentinel through to CheckPermission.
const SuperuserPermission = domain.SuperuserPermission
