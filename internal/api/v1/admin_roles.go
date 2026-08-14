// admin_roles.go is the RBAC role-management surface — SRS §6.8's
// "roles/grants" administration, and the missing half of defect B26.
//
// ── WHY THIS FILE DID NOT EXIST UNTIL PHASE 20.2 ─────────────────────────
// internal/rbac shipped in Phase 10 with CreateRole, DeleteRole,
// AssignUserRole, RevokeUserRole, AddRoleGrant and RemoveRoleGrant fully
// written, fully unit-tested, transactionally consistent with their
// materialisation, and — every one of them — with NO PRODUCTION CALLER.
// Phase 15.1 added PUT /api/v1/admin/scopes, which replaces one existing
// role's grant set wholesale, and that was the entire mutation surface: an
// operator could edit a role that already existed, and could not create
// one, delete one, or give it to anybody.
//
// The consequence is defect B40, and it is not a cosmetic one. A freshly
// authenticated SSO user holds ZERO permissions: /api/v1/me answers 200
// (it needs only a session) and every other endpoint answers 403, because
// RequirePermission reads app.effective_permission and nothing had ever
// written a row into it for that user. There was no route — none — by
// which anyone could grant themselves or anyone else a permission. The
// installation could sync, and nobody could look at what it synced.
//
// The routes below are the surface. cmd/hangar's first-login bootstrap
// (see internal/rbac/bootstrap.go) is what makes the FIRST administrator
// exist so that these routes can be reached at all.
package v1

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/rbac"
)

func registerAdminRoles(hapi huma.API, deps api.Deps) {
	// The closed permission vocabulary itself, so an administrator editing
	// grants picks from what exists rather than typing a name that will be
	// rejected at save time (replaceRoleGrantsHandler validates against the
	// same list).
	get[EmptyIn, CollectionOut](hapi, deps, "admin.roles.manage", "/api/v1/admin/permissions", "admin-permissions", "The closed RBAC permission vocabulary", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListPermissions(ctx)
			if err != nil {
				return nil, api.Internal("listing permissions", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	mutate[CreateRoleIn, ItemOut](hapi, deps, "POST", "admin.roles.manage", "/api/v1/admin/roles", "admin-create-role", "Create an RBAC role", adminTag, createRoleHandler(deps))
	get[UUIDIn, ItemOut](hapi, deps, "admin.roles.manage", "/api/v1/admin/roles/{id}", "admin-get-role", "One RBAC role", adminTag,
		func(ctx context.Context, in *UUIDIn) (*ItemOut, error) {
			roleID, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("malformed role id")
			}
			row, err := deps.Store.GetRole(ctx, roleID)
			if err != nil {
				return nil, api.NotFound("role")
			}
			data := rowOf(row)
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})
	mutate[UUIDIn, EmptyOut](hapi, deps, "DELETE", "admin.roles.manage", "/api/v1/admin/roles/{id}", "admin-delete-role", "Delete a non-system RBAC role", adminTag, deleteRoleHandler(deps))

	// Per-grant add/remove, alongside Phase 15.1's whole-set replace on
	// /api/v1/admin/scopes. Both exist deliberately: the replace is what a
	// rule editor saves, and these two are what a script or a narrow
	// correction uses without having to read-modify-write the whole set.
	mutate[AddGrantIn, CollectionOut](hapi, deps, "POST", "admin.roles.manage", "/api/v1/admin/roles/{id}/grants", "admin-add-role-grant", "Add one permission grant to a role", adminTag, addRoleGrantHandler(deps))
	mutate[UUIDIn, EmptyOut](hapi, deps, "DELETE", "admin.roles.manage", "/api/v1/admin/roles/grants/{id}", "admin-remove-role-grant", "Remove one permission grant by grant id", adminTag, removeRoleGrantHandler(deps))

	// Role ASSIGNMENT is gated on admin.users.manage, not admin.roles.manage
	// — "define what a role can do" and "decide who holds it" are different
	// powers, and the vocabulary already names both.
	get[UUIDIn, CollectionOut](hapi, deps, "admin.users.manage", "/api/v1/admin/users/{id}/roles", "admin-user-roles", "Roles held directly by one user", adminTag,
		func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
			userID, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("malformed user id")
			}
			rows, err := deps.Store.ListUserRoles(ctx, userID)
			if err != nil {
				return nil, api.Internal("listing user roles", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[AssignRoleIn, EmptyOut](hapi, deps, "POST", "admin.users.manage", "/api/v1/admin/users/{id}/roles", "admin-assign-user-role", "Grant a role to a user", adminTag, assignUserRoleHandler(deps))
	mutate[RevokeRoleIn, EmptyOut](hapi, deps, "DELETE", "admin.users.manage", "/api/v1/admin/users/{id}/roles/{role_id}", "admin-revoke-user-role", "Revoke a role from a user", adminTag, revokeUserRoleHandler(deps))
}

// ---- shapes ----

type CreateRoleIn struct {
	Body struct {
		Name        string  `json:"name"`
		Description *string `json:"description,omitempty"`
	}
}

type AddGrantIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Permission string `json:"permission"`
		Effect     string `json:"effect" enum:"allow,deny" doc:"deny always wins over allow (internal/rbac's absolute deny precedence)."`
	}
}

type AssignRoleIn struct {
	ID   string `path:"id" format:"uuid" doc:"User id."`
	Body struct {
		RoleID string `json:"role_id" format:"uuid"`
	}
}

type RevokeRoleIn struct {
	ID     string `path:"id" format:"uuid" doc:"User id."`
	RoleID string `path:"role_id" format:"uuid"`
}

// ---- handlers ----

// createRoleHandler is POST /api/v1/admin/roles. is_system is NEVER
// settable over the API: the flag is what protects the seeded `admin` and
// `member` roles from deletion (db/queries/rbac.sql's DeleteRole guards on
// `AND NOT is_system`), so letting a caller set it would let them mint an
// undeletable role, and letting them clear it would let them delete the
// role their own access depends on.
func createRoleHandler(deps api.Deps) func(context.Context, *CreateRoleIn) (*ItemOut, error) {
	return func(ctx context.Context, in *CreateRoleIn) (*ItemOut, error) {
		if in.Body.Name == "" {
			return nil, huma.Error422UnprocessableEntity("name is required")
		}
		row, err := rbac.CreateRole(ctx, deps.Store, in.Body.Name, in.Body.Description, false)
		if err != nil {
			return nil, api.Internal("creating role", err)
		}
		auditAdminAction(ctx, deps, "admin.rbac.role_created", row.RoleID.String())
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func deleteRoleHandler(deps api.Deps) func(context.Context, *UUIDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
		roleID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed role id")
		}
		if deps.Pool == nil {
			return nil, api.Internal("deleting role", huma.Error500InternalServerError("no transactional pool configured"))
		}
		// A system role is a documented no-op at the SQL level rather than
		// an error, which would leave the caller believing the delete
		// happened. Checked here so the API can say what actually occurred.
		role, err := deps.Store.GetRole(ctx, roleID)
		if err != nil {
			return nil, api.NotFound("role")
		}
		if role.IsSystem {
			return nil, huma.Error422UnprocessableEntity("role " + role.Name + " is a system role and cannot be deleted")
		}
		if err := rbac.DeleteRole(ctx, deps.Pool, roleID); err != nil {
			return nil, api.Internal("deleting role", err)
		}
		auditAdminAction(ctx, deps, "admin.rbac.role_deleted", roleID.String())
		return &EmptyOut{}, nil
	}
}

func addRoleGrantHandler(deps api.Deps) func(context.Context, *AddGrantIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *AddGrantIn) (*CollectionOut, error) {
		roleID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed role id")
		}
		if !knownPermission(in.Body.Permission) {
			return nil, huma.Error422UnprocessableEntity("unknown permission: " + in.Body.Permission)
		}
		effect := rbac.Effect(in.Body.Effect)
		if effect != rbac.EffectAllow && effect != rbac.EffectDeny {
			return nil, huma.Error422UnprocessableEntity("effect must be allow or deny, got: " + in.Body.Effect)
		}
		if deps.Pool == nil {
			return nil, api.Internal("adding role grant", huma.Error500InternalServerError("no transactional pool configured"))
		}
		if err := rbac.AddRoleGrant(ctx, deps.Pool, roleID, in.Body.Permission, effect); err != nil {
			return nil, api.Internal("adding role grant", err)
		}
		auditAdminAction(ctx, deps, "admin.rbac.grant_added", roleID.String()+" "+in.Body.Permission+"="+in.Body.Effect)

		rows, err := deps.Store.ListRoleGrants(ctx, roleID)
		if err != nil {
			return nil, api.Internal("listing role grants", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func removeRoleGrantHandler(deps api.Deps) func(context.Context, *UUIDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
		grantID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed grant id")
		}
		if deps.Pool == nil {
			return nil, api.Internal("removing role grant", huma.Error500InternalServerError("no transactional pool configured"))
		}
		if err := rbac.RemoveRoleGrant(ctx, deps.Pool, grantID); err != nil {
			return nil, api.Internal("removing role grant", err)
		}
		auditAdminAction(ctx, deps, "admin.rbac.grant_removed", grantID.String())
		return &EmptyOut{}, nil
	}
}

func assignUserRoleHandler(deps api.Deps) func(context.Context, *AssignRoleIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *AssignRoleIn) (*EmptyOut, error) {
		userID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed user id")
		}
		roleID, err := parseUUID(in.Body.RoleID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed role_id")
		}
		if deps.Pool == nil {
			return nil, api.Internal("assigning role", huma.Error500InternalServerError("no transactional pool configured"))
		}
		var grantedBy uuid.NullUUID
		if actor, ok := userIDFromCtx(ctx); ok {
			grantedBy = uuid.NullUUID{UUID: actor, Valid: true}
		}
		if err := rbac.AssignUserRole(ctx, deps.Pool, userID, roleID, grantedBy); err != nil {
			return nil, api.Internal("assigning role", err)
		}
		auditAdminAction(ctx, deps, "admin.rbac.role_assigned", userID.String()+" "+roleID.String())
		return &EmptyOut{}, nil
	}
}

func revokeUserRoleHandler(deps api.Deps) func(context.Context, *RevokeRoleIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *RevokeRoleIn) (*EmptyOut, error) {
		userID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed user id")
		}
		roleID, err := parseUUID(in.RoleID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed role_id")
		}
		if deps.Pool == nil {
			return nil, api.Internal("revoking role", huma.Error500InternalServerError("no transactional pool configured"))
		}
		if err := rbac.RevokeUserRole(ctx, deps.Pool, userID, roleID); err != nil {
			return nil, api.Internal("revoking role", err)
		}
		auditAdminAction(ctx, deps, "admin.rbac.role_revoked", userID.String()+" "+roleID.String())
		return &EmptyOut{}, nil
	}
}

// knownPermission checks a permission name against internal/domain's closed
// set — the same validation replaceRoleGrantsHandler applies, so the two
// mutation paths cannot accept different vocabularies.
func knownPermission(name string) bool {
	for _, p := range domain.Permissions {
		if p.Name == name {
			return true
		}
	}
	return false
}
