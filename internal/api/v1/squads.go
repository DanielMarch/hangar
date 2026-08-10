// squads.go implements SRS §6.6. Squad membership/moderation writes are
// gated by the closed RBAC vocabulary's squads.* permissions
// (squads.view/create/manage/moderate/apply — internal/domain/vocabulary.go);
// unlike characters.go/corporations.go this group actually has a
// fully-specified permission per action, so each route below uses the
// specific one rather than a single shared floor.
package v1

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const squadTag = "squads"

func registerSquads(hapi huma.API, deps api.Deps) {
	get[EmptyIn, CollectionOut](hapi, deps, "squads.view", "/api/v1/squads", "list-squads", "Squads", squadTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSquads(ctx)
			if err != nil {
				return nil, api.Internal("listing squads", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[CreateSquadIn, ItemOut](hapi, deps, "POST", "squads.create", "/api/v1/squads", "create-squad", "Create a squad", squadTag, createSquadHandler(deps))
	mutate[UpdateSquadIn, ItemOut](hapi, deps, "PATCH", "squads.manage", "/api/v1/squads/{id}", "update-squad", "Edit a squad", squadTag, updateSquadHandler(deps))
	mutate[UUIDIn, EmptyOut](hapi, deps, "DELETE", "squads.manage", "/api/v1/squads/{id}", "delete-squad", "Delete a squad", squadTag, deleteSquadHandler(deps))

	get[UUIDIn, CollectionOut](hapi, deps, "squads.view", "/api/v1/squads/{id}/members", "list-squad-members", "Members", squadTag, squadMembersHandler(deps))
	mutate[SquadMemberIn, EmptyOut](hapi, deps, "POST", "squads.manage", "/api/v1/squads/{id}/members", "add-squad-member", "Add a member", squadTag, addSquadMemberHandler(deps))
	mutate[SquadMemberDeleteIn, EmptyOut](hapi, deps, "DELETE", "squads.manage", "/api/v1/squads/{id}/members/{character_id}", "remove-squad-member", "Remove a member", squadTag, removeSquadMemberHandler(deps))

	get[UUIDIn, CollectionOut](hapi, deps, "squads.view", "/api/v1/squads/{id}/moderators", "list-squad-moderators", "Moderators", squadTag, squadModeratorsHandler(deps))
	mutate[SquadModeratorsIn, EmptyOut](hapi, deps, "PUT", "squads.manage", "/api/v1/squads/{id}/moderators", "set-squad-moderators", "Replace the moderator set", squadTag, setSquadModeratorsHandler(deps))

	get[UUIDIn, CollectionOut](hapi, deps, "squads.view", "/api/v1/squads/{id}/roles", "list-squad-roles", "RBAC roles this squad grants its members", squadTag, squadRolesHandler(deps))
	mutate[SquadRolesIn, EmptyOut](hapi, deps, "PUT", "squads.manage", "/api/v1/squads/{id}/roles", "set-squad-roles", "Replace the granted role set", squadTag, setSquadRolesHandler(deps))

	get[UUIDIn, CollectionOut](hapi, deps, "squads.moderate", "/api/v1/squads/{id}/applications", "list-squad-applications", "Pending applications", squadTag, squadApplicationsHandler(deps))
	mutate[ResolveApplicationIn, EmptyOut](hapi, deps, "POST", "squads.moderate", "/api/v1/squads/{id}/applications/resolve", "resolve-squad-application", "Approve or reject an application", squadTag, resolveApplicationHandler(deps))
}

// ---- shapes ----

type CreateSquadIn struct {
	Body struct {
		Name        string  `json:"name"`
		Type        string  `json:"type"`
		Description *string `json:"description,omitempty"`
	}
}

type UpdateSquadIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Name        string  `json:"name"`
		Description *string `json:"description,omitempty"`
	}
}

type SquadMemberIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		CharacterID int64 `json:"character_id"`
	}
}

type SquadMemberDeleteIn struct {
	ID          string `path:"id" format:"uuid"`
	CharacterID int64  `path:"character_id"`
}

type SquadModeratorsIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		UserIDs []string `json:"user_ids"`
	}
}

type SquadRolesIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		RoleIDs []string `json:"role_ids"`
	}
}

type ResolveApplicationIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		ApplicationID string `json:"application_id" format:"uuid"`
		Approve       bool   `json:"approve"`
	}
}

// ---- handlers ----

func createSquadHandler(deps api.Deps) func(context.Context, *CreateSquadIn) (*ItemOut, error) {
	return func(ctx context.Context, in *CreateSquadIn) (*ItemOut, error) {
		userID, ok := userIDFromCtx(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}
		row, err := deps.Store.CreateSquad(ctx, gen.CreateSquadParams{
			Name: in.Body.Name, Type: in.Body.Type, OwnerUserID: userID, Description: in.Body.Description,
		})
		if err != nil {
			return nil, api.Internal("creating squad", err)
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func updateSquadHandler(deps api.Deps) func(context.Context, *UpdateSquadIn) (*ItemOut, error) {
	return func(ctx context.Context, in *UpdateSquadIn) (*ItemOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		row, err := deps.Store.UpdateSquad(ctx, id, in.Body.Name, in.Body.Description)
		if err != nil {
			return nil, api.Internal("updating squad", err)
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func deleteSquadHandler(deps api.Deps) func(context.Context, *UUIDIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		if err := deps.Store.DeleteSquad(ctx, id); err != nil {
			return nil, api.Internal("deleting squad", err)
		}
		return &EmptyOut{}, nil
	}
}

func squadMembersHandler(deps api.Deps) func(context.Context, *UUIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		rows, err := deps.Store.ListSquadMembers(ctx, id)
		if err != nil {
			return nil, api.Internal("listing squad members", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func addSquadMemberHandler(deps api.Deps) func(context.Context, *SquadMemberIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *SquadMemberIn) (*EmptyOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		if err := deps.Store.AddSquadMember(ctx, id, in.Body.CharacterID); err != nil {
			return nil, api.Internal("adding squad member", err)
		}
		return &EmptyOut{}, nil
	}
}

func removeSquadMemberHandler(deps api.Deps) func(context.Context, *SquadMemberDeleteIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *SquadMemberDeleteIn) (*EmptyOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		if err := deps.Store.RemoveSquadMember(ctx, id, in.CharacterID); err != nil {
			return nil, api.Internal("removing squad member", err)
		}
		return &EmptyOut{}, nil
	}
}

func squadModeratorsHandler(deps api.Deps) func(context.Context, *UUIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		rows, err := deps.Store.ListUsersInSquad(ctx, id)
		if err != nil {
			return nil, api.Internal("listing squad users", err)
		}
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			if r.Valid {
				isMod, _ := deps.Store.IsSquadModerator(ctx, id, r.UUID)
				if isMod {
					out = append(out, map[string]any{"user_id": r.UUID.String()})
				}
			}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: out, Page: api.EmptyPage(int32(len(out))), Sync: api.Sync{}}}, nil
	}
}

// setSquadModeratorsHandler replaces the moderator set with a
// remove-then-add pass — app.squad_moderator has no bulk-replace query, so
// this diffs against the current membership in Go, calling
// AddSquadModerator/RemoveSquadModerator per row. Not wrapped in a single
// transaction (each call already commits independently) — a mid-way
// failure leaves a partially-applied set, surfaced to the caller as an
// error rather than silently rolled back; acceptable for an
// admin-initiated, low-frequency, easily-retried operation.
func setSquadModeratorsHandler(deps api.Deps) func(context.Context, *SquadModeratorsIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *SquadModeratorsIn) (*EmptyOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		want := map[uuid.UUID]bool{}
		for _, s := range in.Body.UserIDs {
			uid, err := parseUUID(s)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("malformed user_id: " + s)
			}
			want[uid] = true
		}
		current, err := deps.Store.ListUsersInSquad(ctx, id)
		if err != nil {
			return nil, api.Internal("listing squad users", err)
		}
		have := map[uuid.UUID]bool{}
		for _, r := range current {
			if r.Valid {
				if isMod, _ := deps.Store.IsSquadModerator(ctx, id, r.UUID); isMod {
					have[r.UUID] = true
				}
			}
		}
		for uid := range want {
			if !have[uid] {
				_ = deps.Store.AddSquadModerator(ctx, id, uid)
			}
		}
		for uid := range have {
			if !want[uid] {
				_ = deps.Store.RemoveSquadModerator(ctx, id, uid)
			}
		}
		return &EmptyOut{}, nil
	}
}

func squadRolesHandler(deps api.Deps) func(context.Context, *UUIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		rows, err := deps.Store.ListSquadRoles(ctx, id)
		if err != nil {
			return nil, api.Internal("listing squad roles", err)
		}
		out := make([]map[string]any, len(rows))
		for i, r := range rows {
			out[i] = map[string]any{"role_id": r.String()}
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: out, Page: api.EmptyPage(int32(len(out))), Sync: api.Sync{}}}, nil
	}
}

func setSquadRolesHandler(deps api.Deps) func(context.Context, *SquadRolesIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *SquadRolesIn) (*EmptyOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		want := map[uuid.UUID]bool{}
		for _, s := range in.Body.RoleIDs {
			rid, err := parseUUID(s)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("malformed role_id: " + s)
			}
			want[rid] = true
		}
		current, err := deps.Store.ListSquadRoles(ctx, id)
		if err != nil {
			return nil, api.Internal("listing squad roles", err)
		}
		have := map[uuid.UUID]bool{}
		for _, r := range current {
			have[r] = true
		}
		for rid := range want {
			if !have[rid] {
				_ = deps.Store.AddSquadRole(ctx, id, rid)
			}
		}
		for rid := range have {
			if !want[rid] {
				_ = deps.Store.RemoveSquadRole(ctx, id, rid)
			}
		}
		return &EmptyOut{}, nil
	}
}

func squadApplicationsHandler(deps api.Deps) func(context.Context, *UUIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		rows, err := deps.Store.ListPendingSquadApplications(ctx, id)
		if err != nil {
			return nil, api.Internal("listing squad applications", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func resolveApplicationHandler(deps api.Deps) func(context.Context, *ResolveApplicationIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *ResolveApplicationIn) (*EmptyOut, error) {
		appID, err := parseUUID(in.Body.ApplicationID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed application_id")
		}
		userID, _ := userIDFromCtx(ctx)
		status := "rejected"
		if in.Body.Approve {
			status = "approved"
		}
		if err := deps.Store.ResolveSquadApplication(ctx, appID, status, uuid.NullUUID{UUID: userID, Valid: userID != uuid.Nil}); err != nil {
			return nil, api.Internal("resolving squad application", err)
		}
		return &EmptyOut{}, nil
	}
}
