// admin.go implements SRS §6.8 — administration & observability. It wires
// Huma handlers directly on top of Phase 10's internal/rbac, Phase 11's
// admin_provisioning.go (this package, same directory), and Phase 14/14.1's
// internal/alerting dead-letter/unknown-types boards, per this phase's own
// instructions: "Call them; do not reimplement."
//
// Permission mapping gap, reported rather than silently reconciled: the
// closed RBAC vocabulary (internal/domain/vocabulary.go) has a specific
// permission for exactly three of this section's many read routes
// (admin.security_log.view, provisioning.audit.view,
// alerting.unknown_types.acknowledge — the last a write permission with no
// read counterpart). Every other §6.8 GET route below (sync health, ESI
// route/replica/ratelimit state, platforms, dead-letter, scopes) has no
// matching permission in that closed set. This file gates those with
// domain.SuperuserPermission rather than inventing an ad hoc name Phase 10
// never defined — a real gap between SRS §6.8's endpoint map and Phase 10's
// RBAC vocabulary, for a later phase to close by extending
// internal/domain/vocabulary.go (which has its own CI divergence check,
// TestPermissionSeedMatchesGoSet, guarding exactly this kind of drift).
package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const adminTag = "admin"

// permAdminRead is the fallback permission for §6.8 GET routes with no
// specific closed-vocabulary permission — see this file's doc comment.
const permAdminRead = rbac.SuperuserPermission

func registerAdmin(hapi huma.API, deps api.Deps) {
	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/sync/routes", "admin-sync-routes", "ESI route catalogue", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListEsiRoutes(ctx)
			if err != nil {
				return nil, api.Internal("listing esi routes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/sync/subscriptions", "admin-sync-subscriptions", "Sync subscriptions", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSchedulableEsiRoutes(ctx)
			if err != nil {
				return nil, api.Internal("listing schedulable routes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, ItemOut](hapi, deps, permAdminRead, "/api/v1/admin/sync/health", "admin-sync-health", "Aggregate sync health", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
			blocked, _ := deps.Store.ListBlockedEsiRoutes(ctx)
			all, _ := deps.Store.ListEsiRoutes(ctx)
			data := map[string]any{"total_routes": len(all), "blocked_routes": len(blocked)}
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/esi/catalogue/blocked", "admin-esi-catalogue-blocked", "Routes gated by the compatibility pin", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListBlockedEsiRoutes(ctx)
			if err != nil {
				return nil, api.Internal("listing blocked routes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[AdvancePinIn, ItemOut](hapi, deps, http.MethodPost, "admin.esi_pin.advance", "/api/v1/admin/esi/catalogue/pin", "admin-esi-pin-advance", "Advance the ESI compatibility date pin", adminTag, advancePinHandler(deps))

	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/esi/ratelimits", "admin-esi-ratelimits", "Rate limit ledger buckets", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListLedgerBuckets(ctx)
			if err != nil {
				return nil, api.Internal("listing ledger buckets", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, ItemOut](hapi, deps, permAdminRead, "/api/v1/admin/esi/errorlimit", "admin-esi-errorlimit", "Error budget state (not wired this phase)", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
			reason := "the live Governor2 error-budget row is read through internal/esi/ratelimit's in-process cache, not a Querier method — wiring an admin-facing snapshot is left for a later phase"
			item := api.UnavailableItem[map[string]any](reason)
			return &ItemOut{Body: item}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/esi/replicas", "admin-esi-replicas", "Live replica registry", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListLiveReplicas(ctx, 90*time.Second)
			if err != nil {
				return nil, api.Internal("listing live replicas", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/platforms", "admin-platforms", "Configured platforms (Discord/TeamSpeak/Mumble)", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListPlatforms(ctx)
			if err != nil {
				return nil, api.Internal("listing platforms", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[RulesPreviewIn, RulesPreviewOut](hapi, deps, http.MethodPost, "provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/rules/preview", "admin-platform-rules-preview", "Preview a hypothetical entitlement rule set", adminTag, rulesPreviewHandler(deps))
	mutate[UUIDIn, EmptyOut](hapi, deps, http.MethodPost, "provisioning.platforms.manage", "/api/v1/admin/platforms/{id}/lockdown", "admin-platform-lockdown", "Lock down a platform (not wired this phase)", adminTag,
		func(ctx context.Context, _ *UUIDIn) (*EmptyOut, error) {
			return nil, huma.Error501NotImplemented("platform lockdown is not wired this phase — internal/provisioning has no lockdown primitive yet")
		})

	get[UUIDIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/provisioning/exposures", "admin-provisioning-exposures", "Exposure board for one platform", adminTag, exposuresHandler(deps))
	get[EmptyIn, CollectionOut](hapi, deps, "provisioning.audit.view", "/api/v1/admin/provisioning/audit", "admin-provisioning-audit", "Recent provisioning audit", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListRecentProvisioningAudit(ctx, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing provisioning audit", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/alerts/dead-letter", "admin-alerts-dead-letter", "Dead-lettered alert deliveries", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := alerting.DeadLetterBoard(ctx, deps.Store, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing dead letter board", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[UUIDIn, EmptyOut](hapi, deps, http.MethodPost, permAdminRead, "/api/v1/admin/alerts/dead-letter/{id}/requeue", "admin-alerts-dead-letter-requeue", "Requeue one dead-lettered delivery", adminTag,
		func(ctx context.Context, in *UUIDIn) (*EmptyOut, error) {
			id, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("malformed id")
			}
			if err := alerting.Requeue(ctx, deps.Store, id); err != nil {
				return nil, api.Internal("requeuing delivery", err)
			}
			return &EmptyOut{}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/alerts/unknown-types", "admin-alerts-unknown-types", "Unrecognised notification types pending acknowledgement", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListUnacknowledgedNotificationTypes(ctx)
			if err != nil {
				return nil, api.Internal("listing unacknowledged notification types", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, permAdminRead, "/api/v1/admin/scopes/unknown", "admin-scopes-unknown", "Newly observed scope strings pending acknowledgement", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListUnacknowledgedEsiScopes(ctx)
			if err != nil {
				return nil, api.Internal("listing unacknowledged scopes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, "admin.roles.manage", "/api/v1/admin/scopes", "admin-scopes", "RBAC roles/grants", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListRoles(ctx)
			if err != nil {
				return nil, api.Internal("listing roles", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[UpdateScopesIn, EmptyOut](hapi, deps, http.MethodPut, "admin.roles.manage", "/api/v1/admin/scopes", "admin-update-scopes", "Edit a role's grants (not wired this phase)", adminTag,
		func(ctx context.Context, _ *UpdateScopesIn) (*EmptyOut, error) {
			return nil, huma.Error501NotImplemented("bulk role grant editing is not wired this phase — internal/rbac exposes AddRoleGrant/RemoveRoleGrant per-grant, no bulk-replace endpoint yet")
		})

	get[UsersPageIn, CollectionOut](hapi, deps, "admin.users.manage", "/api/v1/admin/users", "admin-users", "Users (keyset-paginated)", adminTag, adminUsersHandler(deps))
	mutate[UpdateUserIn, ItemOut](hapi, deps, http.MethodPatch, "admin.users.manage", "/api/v1/admin/users/{id}", "admin-update-user", "Edit a user (main character, active/admin flags)", adminTag, updateUserHandler(deps))

	get[SecurityLogIn, CollectionOut](hapi, deps, "admin.security_log.view", "/api/v1/admin/security-log", "admin-security-log", "Append-only security log", adminTag,
		func(ctx context.Context, in *SecurityLogIn) (*CollectionOut, error) {
			var uid uuid.NullUUID
			if in.UserID != "" {
				if id, err := parseUUID(in.UserID); err == nil {
					uid = uuid.NullUUID{UUID: id, Valid: true}
				}
			}
			rows, err := deps.Store.ListSecurityLogForUser(ctx, uid, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing security log", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
}

// ---- shapes ----

type AdvancePinIn struct {
	Body struct {
		NewPin string `json:"new_pin" doc:"YYYY-MM-DD"`
	}
}

type RulesPreviewIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Rules []struct {
			SourceKind string `json:"source_kind"`
			SourceRef  string `json:"source_ref"`
			GroupID    string `json:"group_id" format:"uuid"`
			Effect     string `json:"effect"`
		} `json:"rules"`
	}
}

type RulesPreviewOut struct {
	Body struct {
		Diffs []map[string]any `json:"diffs"`
	}
}

type UpdateScopesIn struct {
	Body struct {
		RoleID string   `json:"role_id" format:"uuid"`
		Grants []string `json:"grants"`
	}
}

type UsersPageIn struct {
	After  string `query:"after"`
	Before string `query:"before"`
	Limit  int32  `query:"limit" default:"50"`
}

type UpdateUserIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		MainCharacterID *int64 `json:"main_character_id,omitempty"`
		IsActive        *bool  `json:"is_active,omitempty"`
		IsAdmin         *bool  `json:"is_admin,omitempty"`
	}
}

type SecurityLogIn struct {
	UserID string `query:"user_id,omitempty" format:"uuid"`
}

// ---- handlers ----

func advancePinHandler(deps api.Deps) func(context.Context, *AdvancePinIn) (*ItemOut, error) {
	return func(ctx context.Context, in *AdvancePinIn) (*ItemOut, error) {
		newPin, err := catalogue.ParseDate(in.Body.NewPin)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed new_pin", err)
		}
		actor := "api"
		if userID, ok := userIDFromCtx(ctx); ok {
			actor = userID.String()
		}
		row, err := catalogue.AdvancePin(ctx, deps.Store, newPin, actor, nil)
		if err != nil {
			return nil, api.Internal("advancing pin", err)
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

func rulesPreviewHandler(deps api.Deps) func(context.Context, *RulesPreviewIn) (*RulesPreviewOut, error) {
	return func(ctx context.Context, in *RulesPreviewIn) (*RulesPreviewOut, error) {
		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		hypothetical := make([]RuleInput, len(in.Body.Rules))
		for i, r := range in.Body.Rules {
			gid, err := parseUUID(r.GroupID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("malformed group_id in rule " + itoa(int64(i)))
			}
			hypothetical[i] = RuleInput{SourceKind: r.SourceKind, SourceRef: r.SourceRef, GroupID: gid, Effect: r.Effect}
		}
		diffs, err := PreviewPlatformRules(ctx, deps.Store, platformID, hypothetical)
		if err != nil {
			return nil, api.Internal("previewing platform rules", err)
		}
		out := &RulesPreviewOut{}
		out.Body.Diffs = rowSliceOf(diffs)
		return out, nil
	}
}

func exposuresHandler(deps api.Deps) func(context.Context, *UUIDIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		board, err := GetExposureBoard(ctx, deps.Store, platformID)
		if err != nil {
			return nil, api.Internal("getting exposure board", err)
		}
		data := make([]map[string]any, 0, len(board.Mismatched)+len(board.Pending))
		for _, m := range board.Mismatched {
			row := rowOf(m)
			row["exposure_kind"] = "mismatched"
			data = append(data, row)
		}
		for _, p := range board.Pending {
			row := rowOf(p)
			row["exposure_kind"] = "pending"
			data = append(data, row)
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func adminUsersHandler(deps api.Deps) func(context.Context, *UsersPageIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UsersPageIn) (*CollectionOut, error) {
		page, err := api.ParsePageRequest(in.After, in.Before, &in.Limit)
		if err != nil {
			return nil, api.PageError(err)
		}
		after := uuid.Nil
		if page.Cursor != nil {
			if s, ok := page.Cursor["user_id"].(string); ok {
				if id, err := uuid.Parse(s); err == nil {
					after = id
				}
			}
		}
		rows, err := deps.Store.ListUsersPage(ctx, after, page.Limit)
		if err != nil {
			return nil, api.Internal("listing users", err)
		}
		data := rowSliceOf(rows)
		next := api.ZeroSentinel
		if len(rows) == int(page.Limit) {
			next = api.EncodeCursor(api.Keyset{"user_id": rows[len(rows)-1].UserID.String()})
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{
			Data: data, Page: api.PageInfo{NextCursor: next, PrevCursor: api.ZeroSentinel, Limit: page.Limit}, Sync: api.Sync{},
		}}, nil
	}
}

func updateUserHandler(deps api.Deps) func(context.Context, *UpdateUserIn) (*ItemOut, error) {
	return func(ctx context.Context, in *UpdateUserIn) (*ItemOut, error) {
		id, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed id")
		}
		if in.Body.MainCharacterID != nil {
			if err := deps.Store.SetUserMainCharacter(ctx, id, in.Body.MainCharacterID); err != nil {
				return nil, api.Internal("setting main character", err)
			}
		}
		row, err := deps.Store.GetUser(ctx, id)
		if err != nil {
			return nil, api.NotFound("user")
		}
		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

var _ = domain.SuperuserPermission
var _ = gen.AppUser{}
