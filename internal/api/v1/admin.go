// admin.go implements SRS §6.8 — administration & observability. It wires
// Huma handlers directly on top of Phase 10's internal/rbac, Phase 11's
// admin_provisioning.go (this package, same directory), and Phase 14/14.1's
// internal/alerting dead-letter/unknown-types boards, per Phase 15's own
// instructions: "Call them; do not reimplement."
//
// PERMISSION MAPPING — closed in Phase 15.1. Phase 15 found that the RBAC
// vocabulary (internal/domain/vocabulary.go) named a permission for only
// three of this section's routes, and gated every other §6.8 read on
// domain.SuperuserPermission as a documented stopgap rather than inventing
// names mid-phase. That made the whole observability surface
// all-or-nothing: an operator who should only see sync health had to be
// handed the one permission that bypasses every other check in the system.
//
// Phase 15.1 added the missing read permissions (admin.sync.view,
// admin.esi.view, admin.platforms.view, admin.scopes.view,
// provisioning.exposures.view, alerting.unknown_types.view,
// alerting.deadletter.view, alerting.deadletter.requeue) and every route
// below now names the specific permission it needs. No route in this file
// is gated on superuser any more; superuser still *implies* all of them
// via internal/rbac.Resolve's fallback, which is the correct relationship.
package v1

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/api"
	apimw "github.com/hangar-project/hangar/internal/api/middleware"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store/gen"
)

const adminTag = "admin"

func registerAdmin(hapi huma.API, deps api.Deps) {
	get[EmptyIn, CollectionOut](hapi, deps, "admin.sync.view", "/api/v1/admin/sync/routes", "admin-sync-routes", "ESI route catalogue", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListEsiRoutes(ctx)
			if err != nil {
				return nil, api.Internal("listing esi routes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, "admin.sync.view", "/api/v1/admin/sync/subscriptions", "admin-sync-subscriptions", "Sync subscriptions", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSchedulableEsiRoutes(ctx)
			if err != nil {
				return nil, api.Internal("listing schedulable routes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, ItemOut](hapi, deps, "admin.sync.view", "/api/v1/admin/sync/health", "admin-sync-health", "Aggregate sync health", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
			blocked, _ := deps.Store.ListBlockedEsiRoutes(ctx)
			all, _ := deps.Store.ListEsiRoutes(ctx)
			schedulable, _ := deps.Store.ListSchedulableEsiRoutes(ctx)
			data := map[string]any{
				"total_routes":       len(all),
				"blocked_routes":     len(blocked),
				"schedulable_routes": len(schedulable),
			}
			// PHASE 18. The pin and its ceiling belong on the health card:
			// the Sync Health screen and the pin-advance flow both need
			// them, and neither was reachable without a second endpoint that
			// SRS §6.8 does not define. Reported as null rather than omitted
			// when unresolvable, so "no pin" is distinguishable from "this
			// build does not report one".
			if pin, err := catalogue.GetPin(ctx, deps.Store); err == nil {
				data["compatibility_pin"] = catalogue.FormatDate(pin)
			} else {
				data["compatibility_pin"] = nil
			}
			if dMax, source, err := catalogue.GetDMax(ctx, deps.Store, time.Now()); err == nil {
				data["d_max"] = catalogue.FormatDate(dMax)
				data["d_max_source"] = source
			} else {
				data["d_max"] = nil
				data["d_max_source"] = nil
			}
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, "admin.esi.view", "/api/v1/admin/esi/catalogue/blocked", "admin-esi-catalogue-blocked", "Routes gated by the compatibility pin", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListBlockedEsiRoutes(ctx)
			if err != nil {
				return nil, api.Internal("listing blocked routes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	// PREVIEW FIRST, and registered first so the OpenAPI document reads in
	// the order the operation is meant to be used ([v3.1 — B13], SRS §6.8).
	// The preview is non-mutating: it computes the route diff for a
	// candidate date and touches neither app.setting nor
	// app.esi_pin_history. It is what makes Principle 12 honest — an
	// administrator sees the diff BEFORE the pin moves, which one mutating
	// endpoint cannot provide.
	//
	// It is gated on the same permission as the advance rather than on the
	// broader admin.esi.view: a preview enumerates exactly which routes an
	// advance would turn on, which is the pin operator's business.
	mutate[PreviewPinIn, PinPreviewOut](hapi, deps, http.MethodPost, "admin.esi_pin.advance", "/api/v1/admin/esi/catalogue/pin/preview", "admin-esi-pin-preview", "Preview the route diff for a candidate compatibility date (non-mutating)", adminTag, previewPinHandler(deps))
	mutate[AdvancePinIn, ItemOut](hapi, deps, http.MethodPost, "admin.esi_pin.advance", "/api/v1/admin/esi/catalogue/pin", "admin-esi-pin-advance", "Advance the ESI compatibility date pin", adminTag, advancePinHandler(deps))
	get[EmptyIn, CollectionOut](hapi, deps, "admin.esi.view", "/api/v1/admin/esi/catalogue/pin/history", "admin-esi-pin-history", "Compatibility pin advance history, with the recorded route diff", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListEsiPinHistory(ctx, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing pin history", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, "admin.esi.view", "/api/v1/admin/esi/ratelimits", "admin-esi-ratelimits", "Rate limit ledger buckets, with per-bucket ledger divergence", adminTag, ledgerDivergenceHandler(deps))
	// PHASE 15.1: really wired. Phase 15 answered "unavailable" here on the
	// belief that the error budget was only reachable through
	// internal/esi/ratelimit's in-process cache. It is not — Governor2's
	// cache is a one-second read-through over app.esi_error_budget, and
	// db/queries/esi_error_budget.sql has exposed GetErrorBudget since
	// Phase 4. Reading the row directly is also the *correct* choice for an
	// admin view: the installation-wide budget is shared across replicas,
	// so the authoritative answer is the table, not whatever this replica
	// happens to have cached.
	get[EmptyIn, ItemOut](hapi, deps, "admin.esi.view", "/api/v1/admin/esi/errorlimit", "admin-esi-errorlimit", "Installation-wide ESI error budget", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
			row, err := deps.Store.GetErrorBudget(ctx)
			if err != nil {
				// PHASE 18 CLOSE-OUT. This was a 404, which is wrong twice
				// over. app.esi_error_budget is a SINGLETON created by
				// Governor 2's Init — and Init runs when the ESI gateway is
				// assembled, which happens in the worker process, not in
				// `serve`. So on a freshly installed system this route
				// 404'd, and the rate-limit dashboard's error-budget panel
				// crashed into its ErrorBoundary showing "Something went
				// wrong" — for a system that is simply new.
				//
				// "Not initialised yet" is UNAVAILABLE WITH AN EXPLANATION,
				// exactly as §6 requires and exactly as /meta/server-status
				// already does before its first sync. It is not a missing
				// resource, and it is certainly not "0 errors", which would
				// be a false reading of a budget that is not being tracked.
				return &ItemOut{Body: api.UnavailableItem[map[string]any](
					"The ESI error budget has not been initialised on this installation yet — " +
						"Governor 2 creates it when the gateway first runs, which happens in the worker process",
				)}, nil
			}
			data := rowOf(row)
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})
	get[EmptyIn, CollectionOut](hapi, deps, "admin.esi.view", "/api/v1/admin/esi/replicas", "admin-esi-replicas", "Live replica registry", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListLiveReplicas(ctx, 90*time.Second)
			if err != nil {
				return nil, api.Internal("listing live replicas", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, "admin.platforms.view", "/api/v1/admin/platforms", "admin-platforms", "Configured platforms (Discord/TeamSpeak/Mumble)", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListPlatforms(ctx)
			if err != nil {
				return nil, api.Internal("listing platforms", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	// PHASE 18 — the rule editor's read side. The editor needs both the
	// platform's groups (to offer as rule targets) and its current rule
	// set (to edit); neither was reachable over the API.
	get[UUIDIn, CollectionOut](hapi, deps, "provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/groups", "admin-platform-groups", "Groups one platform offers as entitlement targets", adminTag,
		func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
			platformID, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("malformed platform id")
			}
			rows, err := deps.Store.ListPlatformGroups(ctx, platformID)
			if err != nil {
				return nil, api.Internal("listing platform groups", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	get[UUIDIn, CollectionOut](hapi, deps, "provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/rules", "admin-platform-rules", "Current entitlement rule set for one platform", adminTag,
		func(ctx context.Context, in *UUIDIn) (*CollectionOut, error) {
			platformID, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("malformed platform id")
			}
			rows, err := deps.Store.ListEntitlementRulesForPlatform(ctx, platformID)
			if err != nil {
				return nil, api.Internal("listing entitlement rules", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[RulesPreviewIn, RulesPreviewOut](hapi, deps, http.MethodPost, "provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/rules/preview", "admin-platform-rules-preview", "Preview a hypothetical entitlement rule set", adminTag, rulesPreviewHandler(deps))
	mutate[ReplaceRulesIn, RulesSavedOut](hapi, deps, http.MethodPut, "provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/rules", "admin-platform-rules-replace", "Replace one platform's entitlement rule set (requires a matching preview token)", adminTag, replacePlatformRulesHandler(deps))
	mutate[LockdownIn, ItemOut](hapi, deps, http.MethodPost, "provisioning.platforms.manage", "/api/v1/admin/platforms/{id}/lockdown", "admin-platform-lockdown", "Freeze or unfreeze outbound provisioning for one platform", adminTag, lockdownHandler(deps))

	// PHASE 18 DEFECT CLOSURE. This was registered with UUIDIn, whose `id`
	// is a PATH parameter — but SRS §6.8's path for this route carries no
	// `{id}` segment, so Huma emitted a required path parameter that the
	// path itself can never supply. in.ID was therefore always empty and
	// exposuresHandler's parseUUID always failed: the exposure board
	// answered 400 to every request that had ever been made to it. It is
	// the subject of one of this phase's exit criteria
	// (TestExposureBoardShowsExactAges), which is how it surfaced.
	//
	// The platform moves to a QUERY parameter rather than the path being
	// changed, because §6.8's path is the contract and it has no segment.
	get[ExposuresIn, CollectionOut](hapi, deps, "provisioning.exposures.view", "/api/v1/admin/provisioning/exposures", "admin-provisioning-exposures", "Exposure board for one platform", adminTag, exposuresHandler(deps))
	get[EmptyIn, CollectionOut](hapi, deps, "provisioning.audit.view", "/api/v1/admin/provisioning/audit", "admin-provisioning-audit", "Recent provisioning audit", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListRecentProvisioningAudit(ctx, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing provisioning audit", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, "alerting.deadletter.view", "/api/v1/admin/alerts/dead-letter", "admin-alerts-dead-letter", "Dead-lettered alert deliveries", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := alerting.DeadLetterBoard(ctx, deps.Store, api.MaxLimit)
			if err != nil {
				return nil, api.Internal("listing dead letter board", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	mutate[UUIDIn, EmptyOut](hapi, deps, http.MethodPost, "alerting.deadletter.requeue", "/api/v1/admin/alerts/dead-letter/{id}/requeue", "admin-alerts-dead-letter-requeue", "Requeue one dead-lettered delivery", adminTag,
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
	get[EmptyIn, CollectionOut](hapi, deps, "alerting.unknown_types.view", "/api/v1/admin/alerts/unknown-types", "admin-alerts-unknown-types", "Unrecognised notification types pending acknowledgement", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListUnacknowledgedNotificationTypes(ctx)
			if err != nil {
				return nil, api.Internal("listing unacknowledged notification types", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	// PHASE 18. The acknowledge half of the two unknown boards. Both
	// AcknowledgeNotificationType and AcknowledgeEsiScope have existed as
	// generated queries since Phases 14 and 2 respectively, and both
	// permissions were in the closed vocabulary, but NO endpoint ever
	// called either: the boards were readable and unclearable, so they grow
	// without bound and get ignored — the exact failure the roadmap's edge
	// case names. SRS §6.8's endpoint list does not enumerate these two
	// routes; reported at the close of this phase rather than reconciled
	// silently.
	mutate[AcknowledgeTypeIn, EmptyOut](hapi, deps, http.MethodPost, "alerting.unknown_types.acknowledge", "/api/v1/admin/alerts/unknown-types/{type}/acknowledge", "admin-alerts-unknown-types-acknowledge", "Acknowledge one unrecognised notification type", adminTag,
		func(ctx context.Context, in *AcknowledgeTypeIn) (*EmptyOut, error) {
			if in.Type == "" {
				return nil, huma.Error400BadRequest("empty type")
			}
			if err := deps.Store.AcknowledgeNotificationType(ctx, in.Type); err != nil {
				return nil, api.Internal("acknowledging notification type", err)
			}
			auditAdminAction(ctx, deps, "admin.alerting.unknown_type_acknowledged", in.Type)
			return &EmptyOut{}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, "admin.scopes.view", "/api/v1/admin/scopes/unknown", "admin-scopes-unknown", "Newly observed scope strings pending acknowledgement", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListUnacknowledgedEsiScopes(ctx)
			if err != nil {
				return nil, api.Internal("listing unacknowledged scopes", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})
	// PHASE 18 — see the note on the unknown-types acknowledge above. A
	// scope string is opaque and may contain slashes and dots
	// (Principle 14), so it travels in the BODY, not the path: an ESI scope
	// like `esi-characters.read_titles.v1` in a path segment is at the
	// mercy of every proxy's own idea of path normalisation.
	mutate[AcknowledgeScopeIn, EmptyOut](hapi, deps, http.MethodPost, "admin.scopes.acknowledge", "/api/v1/admin/scopes/unknown/acknowledge", "admin-scopes-unknown-acknowledge", "Acknowledge one newly observed scope string", adminTag,
		func(ctx context.Context, in *AcknowledgeScopeIn) (*EmptyOut, error) {
			if in.Body.Scope == "" {
				return nil, huma.Error400BadRequest("empty scope")
			}
			if err := deps.Store.AcknowledgeEsiScope(ctx, in.Body.Scope); err != nil {
				return nil, api.Internal("acknowledging scope", err)
			}
			auditAdminAction(ctx, deps, "admin.scopes.acknowledged", in.Body.Scope)
			return &EmptyOut{}, nil
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
	mutate[UpdateScopesIn, CollectionOut](hapi, deps, http.MethodPut, "admin.roles.manage", "/api/v1/admin/scopes", "admin-update-scopes", "Replace one role's grant set atomically", adminTag, replaceRoleGrantsHandler(deps))

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

// PreviewPinIn is POST /api/v1/admin/esi/catalogue/pin/preview's body. It
// is a POST despite being non-mutating for the same reason the advance is:
// the candidate date is a request body, and SRS §6.8 names the verb.
type PreviewPinIn struct {
	Body struct {
		NewPin string `json:"new_pin" doc:"Candidate compatibility date, YYYY-MM-DD. Nothing is changed by previewing it."`
	}
}

// PinPreviewOut carries the full both-directions route diff plus the D_max
// bound the candidate was measured against, so a client can render both
// "here is what changes" and "this date is beyond what ESI has published"
// from one call.
type PinPreviewOut struct {
	Body struct {
		Data catalogue.PinPreview `json:"data"`
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
		// PreviewToken is what PUT .../rules requires back. See
		// admin_provisioning.go's RuleSetPreviewToken: a save can only
		// succeed for a rule set that was previewed, and editing any rule
		// after previewing invalidates it.
		PreviewToken string `json:"preview_token"`
	}
}

// ReplaceRulesIn is PUT /api/v1/admin/platforms/{id}/rules — the COMPLETE
// desired rule set (a replace, not a merge: anything absent is removed),
// plus the token proving this exact set was previewed.
type ReplaceRulesIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Rules []struct {
			SourceKind string `json:"source_kind"`
			SourceRef  string `json:"source_ref"`
			GroupID    string `json:"group_id" format:"uuid"`
			Effect     string `json:"effect" enum:"grant,deny"`
		} `json:"rules"`
		PreviewToken string `json:"preview_token" doc:"The token POST .../rules/preview returned for this exact rule set. Required: a rule set that has not been previewed cannot be saved."`
	}
}

type RulesSavedOut struct {
	Body struct {
		Rules []map[string]any `json:"rules"`
	}
}

type UpdateScopesIn struct {
	Body struct {
		RoleID string `json:"role_id" format:"uuid"`
		// Grants is the COMPLETE desired grant set for the role — this is
		// a replace, not a merge: anything absent is revoked.
		Grants []RoleGrantIn `json:"grants"`
	}
}

type RoleGrantIn struct {
	Permission string `json:"permission"`
	Effect     string `json:"effect" enum:"allow,deny" doc:"deny always wins over allow (internal/rbac's absolute deny precedence)."`
}

type LockdownIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		LockedDown bool   `json:"locked_down"`
		Reason     string `json:"reason,omitempty" doc:"Why the platform is being frozen. Recorded for the audit trail; ignored when unlocking."`
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

// ExposuresIn selects the platform whose exposure board to return. Query,
// not path — see the registration comment.
type ExposuresIn struct {
	PlatformID string `query:"platform_id" format:"uuid" doc:"The platform whose exposure board to return."`
}

// AcknowledgeTypeIn acknowledges one unrecognised notification type. The
// type name is a HANGAR-side identifier from ESI's notification `type`
// field — alphanumeric in every observed case — so a path parameter is
// safe here in a way it is not for a scope string.
type AcknowledgeTypeIn struct {
	Type string `path:"type" doc:"The unrecognised notification type, verbatim."`
}

// AcknowledgeScopeIn acknowledges one newly observed ESI scope string.
// Body, not path — see the registration comment.
type AcknowledgeScopeIn struct {
	Body struct {
		Scope string `json:"scope" doc:"The scope string, verbatim and unparsed (Principle 14)."`
	}
}

// ---- handlers ----

// previewPinHandler is POST /api/v1/admin/esi/catalogue/pin/preview — the
// non-mutating half of the pin operation ([v3.1 — B13]). It reads the
// current pin, D_max and the route set, and writes nothing.
func previewPinHandler(deps api.Deps) func(context.Context, *PreviewPinIn) (*PinPreviewOut, error) {
	return func(ctx context.Context, in *PreviewPinIn) (*PinPreviewOut, error) {
		candidate, err := catalogue.ParseDate(in.Body.NewPin)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed new_pin", err)
		}
		preview, err := catalogue.PreviewPin(ctx, deps.Store, candidate, time.Now())
		if err != nil {
			return nil, api.Internal("previewing pin advance", err)
		}
		out := &PinPreviewOut{}
		out.Body.Data = preview
		return out, nil
	}
}

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
		oldPin, _ := catalogue.GetPin(ctx, deps.Store)
		row, diff, err := catalogue.AdvancePin(ctx, deps.Store, newPin, actor, time.Now())
		if err != nil {
			// A candidate newer than D_max is the administrator asking for
			// something ESI has not published — a 422 on their input, not a
			// 500 on ours. This is the server half of
			// TestPinAdvanceRefusesDateNewerThanDMax; the client half cannot
			// stand alone, since any direct API call bypasses it.
			var oor *catalogue.OutOfRangeError
			if errors.As(err, &oor) {
				return nil, huma.Error422UnprocessableEntity(oor.Error())
			}
			return nil, api.Internal("advancing pin", err)
		}
		// Advancing the pin changes which routes the whole installation may
		// call — audited like every other administrative action.
		var actorID uuid.UUID
		if userID, ok := userIDFromCtx(ctx); ok {
			actorID = userID
		}
		target := catalogue.FormatDate(newPin)
		_ = apimw.Audit(ctx, deps.Store, actorID, "admin.esi.pin_advanced", &target, "", map[string]any{"new_pin": target})
		raisePinAdvancedAlert(ctx, deps, oldPin, newPin, diff)

		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// raisePinAdvancedAlert emits §4.4's `hangar.platform.esi_pin_advanced`
// domain event — Phase 20.4, and the DOMAIN-EVENT third of defect B25.
//
// ── WHY THIS PARTICULAR EVENT ────────────────────────────────────────────
// The alert catalogue has carried seven `platform` domain events since
// Phase 1a and nothing raised any of them. This one is the most consequential
// of the seven and the only one whose trigger is an administrator's
// deliberate act: advancing the compatibility pin changes which ESI routes
// the WHOLE INSTALLATION may call, in both directions. §4.4 lists it as a
// default-enabled alert precisely because the people who need to know are
// not necessarily the person who clicked the button.
//
// ── WHY SemanticFields IS THE RIGHT FINGERPRINT HERE ─────────────────────
// A CCP notification has notification_id and a threshold has a subject, so
// both have a natural identity. A domain event has neither, and hashing
// the payload's SERIALISATION would be unstable across process restarts
// (Go randomises map iteration order) — the failure mode dedupe.go's
// header calls "the worst possible", because it looks fine in a unit test.
// SemanticFields names the fields that ARE the identity: the pin moved
// from one date to another, and that pair re-arms the alert by itself,
// since the next advance necessarily has a different `to`.
//
// A failure to raise the alert does NOT fail the request. The pin has
// already moved and the audit row is already written; refusing the
// administrator's action after the fact because a notification could not
// be queued would be the tail wagging the dog. It is logged.
func raisePinAdvancedAlert(ctx context.Context, deps api.Deps, oldPin, newPin time.Time, diff catalogue.RouteDiff) {
	if deps.Alerts == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"from":             catalogue.FormatDate(oldPin),
		"to":               catalogue.FormatDate(newPin),
		"routes_unblocked": len(diff.NewlyUnblocked),
		"routes_blocked":   len(diff.NewlyBlocked),
		"routes_unchanged": diff.Unchanged,
	})
	if err != nil {
		return
	}
	const alertType = "hangar.platform.esi_pin_advanced"
	if _, err := deps.Alerts.Emit(ctx, alerting.EmitRequest{
		AlertType:  alertType,
		Payload:    payload,
		OccurredAt: time.Now(),
		Fingerprint: func(target alerting.Target) alerting.Fingerprint {
			fields := alerting.SemanticFields(payload, "from", "to")
			fields["target_kind"] = target.Kind
			fields["target_ref"] = target.Ref
			return alerting.Fingerprint{AlertType: alertType, Fields: fields}
		},
	}); err != nil {
		slog.WarnContext(ctx, "alerting: the ESI compatibility pin advanced but its alert could not be raised — "+
			"the pin HAS moved; only the notification was lost",
			"old_pin", catalogue.FormatDate(oldPin), "new_pin", catalogue.FormatDate(newPin), "error", err)
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
		out.Body.PreviewToken = RuleSetPreviewToken(platformID, hypothetical)
		return out, nil
	}
}

// replacePlatformRulesHandler is PUT /api/v1/admin/platforms/{id}/rules.
// The preview-token check below is the SERVER half of
// TestRuleEditorRequiresPreviewConfirmation: a rule set that was not
// previewed — or was edited after being previewed — cannot be saved, by
// any client, including one that never renders the editor at all.
func replacePlatformRulesHandler(deps api.Deps) func(context.Context, *ReplaceRulesIn) (*RulesSavedOut, error) {
	return func(ctx context.Context, in *ReplaceRulesIn) (*RulesSavedOut, error) {
		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		rules := make([]RuleInput, len(in.Body.Rules))
		for i, r := range in.Body.Rules {
			gid, err := parseUUID(r.GroupID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("malformed group_id in rule " + itoa(int64(i)))
			}
			if !validSourceKinds[r.SourceKind] {
				return nil, huma.Error422UnprocessableEntity("unknown source_kind: " + r.SourceKind)
			}
			if r.Effect != entitlement.EffectGrant && r.Effect != entitlement.EffectDeny {
				return nil, huma.Error422UnprocessableEntity("effect must be grant or deny, got: " + r.Effect)
			}
			rules[i] = RuleInput{SourceKind: r.SourceKind, SourceRef: r.SourceRef, GroupID: gid, Effect: r.Effect}
		}

		if in.Body.PreviewToken == "" {
			return nil, huma.Error422UnprocessableEntity(
				"preview_token is required: preview this rule set before saving it — an unpreviewed rule set is how an accidental mass revocation happens")
		}
		want := RuleSetPreviewToken(platformID, rules)
		if subtle.ConstantTimeCompare([]byte(in.Body.PreviewToken), []byte(want)) != 1 {
			return nil, huma.Error422UnprocessableEntity(
				"preview_token does not match these rules: the rule set changed since it was previewed — preview it again before saving")
		}

		if deps.Pool == nil {
			return nil, api.Internal("replacing platform rules", huma.Error500InternalServerError("no transactional pool configured"))
		}
		saved, err := ReplacePlatformRules(ctx, deps.Pool, platformID, rules)
		if err != nil {
			return nil, api.Internal("replacing platform rules", err)
		}
		auditAdminAction(ctx, deps, "admin.provisioning.rules_replaced", platformID.String())

		out := &RulesSavedOut{}
		out.Body.Rules = rowSliceOf(saved)
		return out, nil
	}
}

// validSourceKinds mirrors internal/provisioning/entitlement's closed
// Go-side set. Closed because these are HANGAR's OWN rule sources, not an
// external vocabulary — Principle 14 does not apply.
var validSourceKinds = map[string]bool{
	entitlement.SourceUser:        true,
	entitlement.SourceRole:        true,
	entitlement.SourceCorporation: true,
	entitlement.SourceAlliance:    true,
	entitlement.SourceCorpTitle:   true,
	entitlement.SourceSquad:       true,
	entitlement.SourcePublic:      true,
}

// auditAdminAction records one administrative action against the
// append-only security log. Best-effort by the same convention
// lockdownHandler established: failing to audit must not fail the action
// the operator asked for, and the error is not actionable by the caller.
func auditAdminAction(ctx context.Context, deps api.Deps, action, target string) {
	var actor uuid.UUID
	if userID, ok := userIDFromCtx(ctx); ok {
		actor = userID
	}
	_ = apimw.Audit(ctx, deps.Store, actor, action, &target, "", nil)
}

// ledgerDivergenceHandler is GET /api/v1/admin/esi/ratelimits. It answers
// with one row per Governor 1 ledger bucket, each carrying the local and
// server readings AND the divergence between them — the roadmap's Phase 18
// edge case: "surface esi_ledger_divergence prominently: sustained
// divergence is the early warning for a Gate 1 failure" (Gate 1.3's bar is
// max(divergence) <= 1 per group).
//
// `divergence` is null, not zero, until the server has been heard from for
// that bucket. Zero divergence is a healthy reading; no reading is not a
// reading, and collapsing them would hide a bucket whose headers have
// stopped arriving behind a wall of reassuring zeroes.
//
// ── PHASE 20.4: THE SUBTRACTION'S OPERANDS ───────────────────────────────
// `divergence` is (local_remaining_at_reading − server_remaining): the pair
// the reconciler wrote together under the bucket lock. It is NOT computed
// from `local_remaining`, which this row also carries and which is summed
// live at request time — subtracting a live count from a stored snapshot
// measures how much has been consumed since the last reconcile, which on
// this installation read 40-55 on healthy buckets against a Gate 1.3
// tolerance of 1 (migration 00042 has the readings).
//
// Both numbers stay on the row on purpose. `local_remaining` answers "how
// much headroom is there right now", `divergence` answers "how far apart
// were we and CCP the last time both were known", and an operator watching
// a rate-limit dashboard needs both. The board is also the ONE reader that
// deliberately does not apply the telemetry collector's freshness rule: a
// stale reading is shown with `server_observed_at` beside it, because
// "nothing has been heard from this bucket in 90 seconds" is a thing an
// operator must be able to see and a gauge cannot say.
func ledgerDivergenceHandler(deps api.Deps) func(context.Context, *EmptyIn) (*CollectionOut, error) {
	return func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
		rows, err := deps.Store.ListLedgerDivergence(ctx)
		if err != nil {
			return nil, api.Internal("listing ledger divergence", err)
		}
		data := rowSliceOf(rows)
		for i, r := range rows {
			if r.ServerRemaining == nil || r.LocalRemainingAtReading == nil {
				data[i]["divergence"] = nil
				continue
			}
			d := int64(*r.LocalRemainingAtReading) - int64(*r.ServerRemaining)
			if d < 0 {
				d = -d
			}
			data[i]["divergence"] = d
		}
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

func exposuresHandler(deps api.Deps) func(context.Context, *ExposuresIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *ExposuresIn) (*CollectionOut, error) {
		platformID, err := parseUUID(in.PlatformID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed or missing platform_id")
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
		// PHASE 20.2. UpdateUserIn has declared `is_active` and `is_admin`
		// since Phase 15 and this handler SILENTLY DROPPED BOTH — the
		// OpenAPI document advertised them, the SPA sent them, the endpoint
		// answered 200, and nothing changed. Deactivating a compromised
		// account is not a field to lose quietly.
		if in.Body.IsActive != nil {
			if err := deps.Store.SetUserActive(ctx, id, *in.Body.IsActive); err != nil {
				return nil, api.Internal("setting user active", err)
			}
			auditAdminAction(ctx, deps, "admin.user.active_changed", id.String())
		}
		if in.Body.IsAdmin != nil {
			if err := deps.Store.SetUserAdmin(ctx, id, *in.Body.IsAdmin); err != nil {
				return nil, api.Internal("setting user admin", err)
			}
			auditAdminAction(ctx, deps, "admin.user.admin_changed", id.String())
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

// replaceRoleGrantsHandler is PUT /api/v1/admin/scopes — a full replace of
// one role's grant set, delegated to internal/rbac.ReplaceRoleGrants so
// the delete/insert/rematerialise all happen in one transaction.
func replaceRoleGrantsHandler(deps api.Deps) func(context.Context, *UpdateScopesIn) (*CollectionOut, error) {
	return func(ctx context.Context, in *UpdateScopesIn) (*CollectionOut, error) {
		roleID, err := parseUUID(in.Body.RoleID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed role_id")
		}
		// Validate every permission against the closed vocabulary BEFORE
		// touching the database: an unknown permission name is a client
		// error (422), not a foreign-key error surfaced as a 500, and
		// rejecting the whole request keeps the replace all-or-nothing.
		known := make(map[string]bool, len(domain.Permissions))
		for _, p := range domain.Permissions {
			known[p.Name] = true
		}
		grants := make([]gen.AppRoleGrant, 0, len(in.Body.Grants))
		for _, g := range in.Body.Grants {
			if !known[g.Permission] {
				return nil, huma.Error422UnprocessableEntity("unknown permission: " + g.Permission)
			}
			if g.Effect != "allow" && g.Effect != "deny" {
				return nil, huma.Error422UnprocessableEntity("effect must be allow or deny, got: " + g.Effect)
			}
			grants = append(grants, gen.AppRoleGrant{RoleID: roleID, Permission: g.Permission, Effect: g.Effect})
		}
		if deps.Pool == nil {
			return nil, api.Internal("replacing role grants", huma.Error500InternalServerError("no transactional pool configured"))
		}
		if err := rbac.ReplaceRoleGrants(ctx, deps.Pool, roleID, grants); err != nil {
			return nil, api.Internal("replacing role grants", err)
		}
		rows, err := deps.Store.ListRoleGrants(ctx, roleID)
		if err != nil {
			return nil, api.Internal("listing role grants", err)
		}
		data := rowSliceOf(rows)
		return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
	}
}

// lockdownHandler is POST /api/v1/admin/platforms/{id}/lockdown — the
// incident freeze added in Phase 15.1 (see
// 00040_phase15_1_defect_closure.sql for why this is a column of its own
// rather than a reuse of app.platform.enabled).
//
// ── PHASE 20.4: GATE 2 TRIGGER ROW 8, SETTLED ────────────────────────────
// §2.3's matrix marks this row "must enqueue urgent" without saying what.
// Phase 20.3 recorded the question rather than guessing on a security
// control. The settlement — locking enqueues nothing, unlocking enqueues a
// full platform reconcile — is argued in full on
// provisioning.Urgent.EnqueuePlatformReconcile, which is where the next
// reader will be standing when they ask why.
//
// The other half of the settlement is not here at all, and it is the
// larger half: the freeze is now ENFORCED, in
// provisioning.applyToDriver, because it previously was not. Until 20.4
// nothing anywhere read app.platform.locked_down — this handler wrote it,
// the UI displayed it, the audit trail recorded it, and both worker paths
// carried on calling the platform's driver regardless.
func lockdownHandler(deps api.Deps) func(context.Context, *LockdownIn) (*ItemOut, error) {
	return func(ctx context.Context, in *LockdownIn) (*ItemOut, error) {
		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		var actor uuid.NullUUID
		if userID, ok := userIDFromCtx(ctx); ok {
			actor = uuid.NullUUID{UUID: userID, Valid: true}
		}
		var reason *string
		if in.Body.Reason != "" {
			reason = &in.Body.Reason
		}
		row, err := deps.Store.SetPlatformLockdown(ctx, gen.SetPlatformLockdownParams{
			PlatformID: platformID, LockedDown: in.Body.LockedDown, Actor: actor, Reason: reason,
		})
		if err != nil {
			return nil, api.NotFound("platform")
		}
		// Freezing or thawing outbound provisioning is a security-relevant
		// administrative action — audited like every other one.
		outcome := "unlocked"
		if in.Body.LockedDown {
			outcome = "locked_down"
		}
		target := platformID.String()
		_ = apimw.Audit(ctx, deps.Store, actor.UUID, "admin.platform."+outcome, &target, "", map[string]any{"reason": in.Body.Reason})

		// UNLOCKING is the transition that owes work. Everything that
		// changed while the platform was frozen was recorded
		// skipped_locked_down and left on the exposure board; this is what
		// closes it, without waiting for the nightly pass.
		//
		// A failure to enqueue does NOT fail the request, and that is a
		// deliberate asymmetry with B32's rule deletion (which refuses
		// outright when deps.Urgent is nil). Unfreezing is the SAFE
		// direction: the operator has ended an incident, and refusing to
		// record that because a queue insert failed would leave the
		// platform frozen — a worse outcome than a reconcile that starts
		// on its own schedule instead. It is logged as a real problem
		// rather than swallowed.
		if !in.Body.LockedDown && deps.Urgent != nil {
			if err := deps.Urgent.EnqueuePlatformReconcile(ctx, platformID); err != nil {
				// slog.Default: cmd/hangar's newLogger installs the
				// configured handler as the default at boot, and Deps
				// carries no logger of its own.
				slog.WarnContext(ctx,
					"provisioning: platform unfrozen but its catch-up reconcile could not be enqueued — "+
						"entitlement changes made during the freeze stay on the exposure board until the next bulk pass",
					"platform_id", target, "error", err)
			}
		}

		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}
