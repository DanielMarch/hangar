// admin_rules.go mounts the single-rule entitlement write surface —
// defect B32, and the last of 04_RELEASE_GATES.md §2.3's trigger matrix
// with no producer at all.
//
// ── WHY THESE TWO FUNCTIONS SAT UNMOUNTED SINCE PHASE 11 ─────────────────
// v1.CreateEntitlementRule and v1.DeleteEntitlementRule were written in
// Phase 11 as "documented placeholder seams" — already-parsed arguments in,
// already-shaped results out — and RegisterAll never mounted either. Phase
// 18 added PUT .../rules (a whole-set replace) and could not use
// DeleteEntitlementRule's bulk-urgent-revocation path, because that needs a
// *provisioning.Urgent and the API process had no River client; it recorded
// the limitation rather than half-doing it, and the single-rule endpoints
// stayed 404.
//
// Phase 20.3 supplies the missing piece — an insert-only River client in
// `serve` (cmd/hangar/revocation.go) — so both endpoints can be mounted
// WITH the revocation semantics the roadmap specifies, rather than mounted
// as writes that silently defer their consequences to the next nightly
// reconcile.
package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/provisioning/entitlement"
)

var (
	errNoTransactionalPool = errors.New("v1: no transactional pool configured on this API instance")
	// errNoUrgentEnqueuer is a process-assembly error, not a request error:
	// see deleteEntitlementRuleHandler for why the delete is refused rather
	// than performed without its revocations.
	errNoUrgentEnqueuer = errors.New("v1: no provision-urgent enqueuer configured on this API instance — " +
		"deleting an entitlement rule must revoke what it granted, and this process cannot enqueue that")
)

func registerAdminRules(hapi huma.API, deps api.Deps) {
	// Single-rule create, alongside Phase 18's whole-set PUT. Both exist
	// for the same reason the per-grant RBAC endpoints exist alongside the
	// grant-set replace (admin_roles.go): the replace is what a rule editor
	// saves after a preview, and this is what a script or a narrow
	// correction uses without read-modify-writing the whole set.
	//
	// It carries NO preview-token requirement, and that is deliberate.
	// Phase 18's gate exists because saving a whole rule set silently
	// DELETES every rule not in the payload — "a rule saved without preview
	// is how an accidental mass revocation happens". Adding one rule
	// deletes nothing and can only ever grant or deny by one rule's worth;
	// requiring a preview token here would train operators to mint tokens
	// reflexively, which is how the gate on the dangerous operation stops
	// being read.
	mutate[CreateRuleIn, ItemOut](hapi, deps, http.MethodPost,
		"provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/rules", "admin-create-platform-rule",
		"Add one entitlement rule to a platform", adminTag, createEntitlementRuleHandler(deps))

	// Single-rule delete, which IS entitlement-reducing for everyone the
	// rule matched — the roadmap's bulk urgent revocation.
	mutate[DeleteRuleIn, EmptyOut](hapi, deps, http.MethodDelete,
		"provisioning.entitlements.manage", "/api/v1/admin/platforms/{id}/rules/{rule_id}", "admin-delete-platform-rule",
		"Delete one entitlement rule and revoke what it granted", adminTag, deleteEntitlementRuleHandler(deps))
}

// ---- shapes ----

type CreateRuleIn struct {
	ID   string `path:"id" format:"uuid" doc:"Platform id."`
	Body struct {
		SourceKind string `json:"source_kind"`
		SourceRef  string `json:"source_ref"`
		GroupID    string `json:"group_id" format:"uuid"`
		Effect     string `json:"effect" enum:"grant,deny" doc:"deny always wins over grant (internal/provisioning/entitlement's precedence)."`
	}
}

type DeleteRuleIn struct {
	ID     string `path:"id" format:"uuid" doc:"Platform id."`
	RuleID string `path:"rule_id" format:"uuid"`
}

// ---- handlers ----

func createEntitlementRuleHandler(deps api.Deps) func(context.Context, *CreateRuleIn) (*ItemOut, error) {
	return func(ctx context.Context, in *CreateRuleIn) (*ItemOut, error) {
		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		groupID, err := parseUUID(in.Body.GroupID)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("malformed group_id")
		}
		if !validSourceKinds[in.Body.SourceKind] {
			return nil, huma.Error422UnprocessableEntity("unknown source_kind: " + in.Body.SourceKind)
		}
		if in.Body.Effect != entitlement.EffectGrant && in.Body.Effect != entitlement.EffectDeny {
			return nil, huma.Error422UnprocessableEntity("effect must be grant or deny, got: " + in.Body.Effect)
		}

		// The group must belong to the platform in the path. Without this a
		// rule created "on" platform A could target platform B's group, and
		// the platform's own rule list would never show it — the same check
		// ReplacePlatformRules makes, for the same reason.
		if err := requireGroupOnPlatform(ctx, deps, platformID, groupID); err != nil {
			return nil, err
		}

		row, err := CreateEntitlementRule(ctx, deps.Store, CreateEntitlementRuleInput{
			SourceKind: in.Body.SourceKind,
			SourceRef:  in.Body.SourceRef,
			GroupID:    groupID,
			Effect:     in.Body.Effect,
		})
		if err != nil {
			return nil, api.Internal("creating entitlement rule", err)
		}
		auditAdminAction(ctx, deps, "admin.provisioning.rule_created", row.RuleID.String())

		data := rowOf(row)
		return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
	}
}

// deleteEntitlementRuleHandler is DELETE
// /api/v1/admin/platforms/{id}/rules/{rule_id}.
//
// eventAt is stamped ONCE, here, before any work begins, and threaded into
// every per-user audit row DeleteEntitlementRule writes. 04_RELEASE_GATES.md
// §2.2 measures from "the timestamp of the originating entitlement-reducing
// event, not job start and not job claim" — and for a rule deletion that
// affects 5000 users, the originating event is the administrator's single
// click, not the moment each user's turn came round in the loop. Letting
// each iteration stamp its own time.Now() would make the last user's
// measured latency as short as the first's however long the loop took,
// which is the most flattering possible reading of exactly the case the
// SLO exists to bound.
func deleteEntitlementRuleHandler(deps api.Deps) func(context.Context, *DeleteRuleIn) (*EmptyOut, error) {
	return func(ctx context.Context, in *DeleteRuleIn) (*EmptyOut, error) {
		eventAt := time.Now()

		platformID, err := parseUUID(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed platform id")
		}
		ruleID, err := parseUUID(in.RuleID)
		if err != nil {
			return nil, huma.Error400BadRequest("malformed rule id")
		}
		if deps.Pool == nil {
			return nil, api.Internal("deleting entitlement rule", errNoTransactionalPool)
		}
		if deps.Urgent == nil {
			// Not a 501: the endpoint IS implemented. A process assembled
			// without the revocation enqueuer cannot honour the < 60s SLO
			// this operation is defined by, and performing the delete
			// anyway would silently leave every affected user's groups
			// live until the next bulk reconcile — the exact silent
			// deferral B32 exists to stop.
			return nil, api.Internal("deleting entitlement rule", errNoUrgentEnqueuer)
		}

		// The rule must belong to the platform in the path. Deleting one
		// platform's rule through another's URL would leave the operator's
		// audit trail describing something that did not happen.
		rules, err := deps.Store.ListEntitlementRulesForPlatform(ctx, platformID)
		if err != nil {
			return nil, api.Internal("listing entitlement rules", err)
		}
		found := false
		for _, r := range rules {
			if r.RuleID == ruleID {
				found = true
				break
			}
		}
		if !found {
			return nil, api.NotFound("entitlement rule on this platform")
		}

		if err := DeleteEntitlementRule(ctx, deps.Pool, deps.Urgent, ruleID, eventAt, "entitlement_rule_deleted"); err != nil {
			return nil, api.Internal("deleting entitlement rule", err)
		}
		auditAdminAction(ctx, deps, "admin.provisioning.rule_deleted", ruleID.String())
		return &EmptyOut{}, nil
	}
}

// requireGroupOnPlatform rejects a rule whose target group belongs to a
// different platform.
func requireGroupOnPlatform(ctx context.Context, deps api.Deps, platformID, groupID uuid.UUID) error {
	groups, err := deps.Store.ListPlatformGroups(ctx, platformID)
	if err != nil {
		return api.Internal("listing platform groups", err)
	}
	for _, g := range groups {
		if g.GroupID == groupID {
			return nil
		}
	}
	return huma.Error422UnprocessableEntity("group " + groupID.String() + " does not belong to platform " + platformID.String())
}
