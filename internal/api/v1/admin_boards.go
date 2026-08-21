package v1

// admin_boards.go is the rest of PHASE 23's N-4 build: four groups of
// generated queries that had no production caller, each of which was a real
// operator capability with no way to reach it.
//
//	subscription management      an operator could not disable, snooze or
//	                             opt a subscription out of caching except by
//	                             writing SQL, and nothing surfaced its runs
//	open-vocabulary board        Gate 6's whole point is that an unknown
//	                             external value is RECORDED rather than
//	                             rejected. Nothing read the record
//	webhook dead-letter board    §4.9's counterpart to the alerting board
//	                             next door, which has had an endpoint since
//	                             Phase 15
//	platform configuration       app.platform had NO production writer at
//	                             all, so §4.4's provisioning subsystem was
//	                             unconfigurable on every installation
//
// The last of those is the one worth naming as a defect rather than a gap.
// cmd/hangar/discord.go's own comment says it plainly — "there can be zero
// (an administrator hasn't created the platform record)" — and warns and
// registers no driver when there are none. Phases 11 through 13 built three
// provisioning drivers, an entitlement engine, an exposure board and a
// revocation SLO that Gate 2 measures, on top of a table that nothing in
// the product could put a row into. That is B-6's shape once more: a
// documented capability whose entry point does not exist, invisible to
// every test because each test inserts the row it needs.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/events"
)

// EntityScopeIn addresses one synchronised entity: a character,
// corporation or alliance.
type EntityScopeIn struct {
	Kind string `path:"entity_kind" doc:"character, corporation, alliance or global."`
	ID   int64  `path:"entity_id" doc:"EVE id of the entity; 0 for global."`
}

// SubscriptionPatchIn is PATCH /api/v1/admin/sync/subscriptions/{id}.
//
// Both fields are POINTERS so "leave this alone" and "set this to false"
// are different requests. A plain bool would make every PATCH that meant to
// change caching also silently disable the subscription, which is exactly
// the class of accident an operator screen must not be able to cause.
type SubscriptionPatchIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Enabled      *bool `json:"enabled,omitempty" doc:"Enable or disable this subscription entirely."`
		OptInNoCache *bool `json:"opt_in_no_cache,omitempty" doc:"Opt this subscription out of §5.4's conditional-request caching."`
	}
}

// SubscriptionRunsIn is GET /api/v1/admin/sync/subscriptions/{id}/runs.
type SubscriptionRunsIn struct {
	ID    string `path:"id" format:"uuid"`
	Limit int32  `query:"limit" default:"50" doc:"How many recent runs, 1-200."`
}

// VocabularyIn addresses one open vocabulary's board.
type VocabularyIn struct {
	Vocabulary string `path:"vocabulary" doc:"One of domain.OpenVocabularies(): ref_type, location_type, notification_type, scope, cache_mode, contract_status, required_role."`
}

// AcknowledgeVocabularyIn is the acknowledge half of the board.
type AcknowledgeVocabularyIn struct {
	Vocabulary string `path:"vocabulary"`
	Body       struct {
		Value string `json:"value" doc:"The observed value being acknowledged."`
	}
}

// CreatePlatformIn is POST /api/v1/admin/platforms.
type CreatePlatformIn struct {
	Body struct {
		Kind   string          `json:"kind" doc:"discord, teamspeak or mumble."`
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config" doc:"Platform-specific configuration; may be an empty object when the connection is configured by environment."`
	}
}

// CreatePlatformGroupIn is POST /api/v1/admin/platforms/{id}/groups — the
// remote group an entitlement rule can then target.
type CreatePlatformGroupIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		RemoteRef string `json:"remote_ref" doc:"The platform's own id for the group: a Discord role id, a TS3 server group id, a Mumble ACL group name."`
		Name      string `json:"name" doc:"Operator-facing name."`
	}
}

// SetRuleEnabledIn is PATCH .../rules/{rule_id}.
type SetRuleEnabledIn struct {
	ID     string `path:"id" format:"uuid"`
	RuleID string `path:"rule_id" format:"uuid"`
	Body   struct {
		Enabled bool `json:"enabled"`
	}
}

// EntitlementSourceIn is the reverse lookup: what does this squad, role or
// corporation grant?
type EntitlementSourceIn struct {
	Kind string `query:"source_kind" doc:"squad, role, corporation or alliance."`
	Ref  string `query:"source_ref" doc:"Id of the source entity."`
}

// GroupRulesIn lists the rules that grant one platform group.
type GroupRulesIn struct {
	ID      string `path:"id" format:"uuid"`
	GroupID string `path:"group_id" format:"uuid"`
}

func registerAdminBoards(hapi huma.API, deps api.Deps) {
	registerSubscriptionManagement(hapi, deps)
	registerVocabularyBoard(hapi, deps)
	registerWebhookBoard(hapi, deps)
	registerPlatformConfiguration(hapi, deps)
}

// ── SUBSCRIPTION MANAGEMENT ──────────────────────────────────────────────

func registerSubscriptionManagement(hapi huma.API, deps api.Deps) {
	get[EntityScopeIn, CollectionOut](hapi, deps, "admin.sync.view",
		"/api/v1/admin/sync/subscriptions/{entity_kind}/{entity_id}", "admin-sync-entity-subscriptions",
		"Every sync subscription for one entity, with its health", adminTag,
		func(ctx context.Context, in *EntityScopeIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListSyncSubscriptionsForEntity(ctx, in.Kind, in.ID)
			if err != nil {
				return nil, api.Internal("listing subscriptions", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[SubscriptionRunsIn, CollectionOut](hapi, deps, "admin.sync.view",
		"/api/v1/admin/sync/subscriptions/{id}/runs", "admin-sync-subscription-runs",
		"Recent sync runs for one subscription", adminTag,
		func(ctx context.Context, in *SubscriptionRunsIn) (*CollectionOut, error) {
			id, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("subscription id is not a uuid")
			}
			limit := in.Limit
			if limit < 1 || limit > 200 {
				limit = 50
			}
			rows, err := deps.Store.ListRecentSyncRuns(ctx, id, limit)
			if err != nil {
				return nil, api.Internal("listing sync runs", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	mutate[SubscriptionPatchIn, ItemOut](hapi, deps, http.MethodPatch, "admin.sync.manage",
		"/api/v1/admin/sync/subscriptions/{id}", "admin-sync-patch-subscription",
		"Enable/disable a subscription, or opt it out of conditional caching", adminTag,
		func(ctx context.Context, in *SubscriptionPatchIn) (*ItemOut, error) {
			id, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("subscription id is not a uuid")
			}
			if in.Body.Enabled == nil && in.Body.OptInNoCache == nil {
				return nil, huma.Error400BadRequest("nothing to change: pass enabled, opt_in_no_cache, or both")
			}
			// Existence checked first so a bad id is a 404 rather than a
			// successful no-op UPDATE. `SET ... WHERE subscription_id = $1`
			// affects zero rows for an id that does not exist and reports
			// no error, which would tell an operator their change landed.
			if _, err := deps.Store.GetSyncSubscription(ctx, id); errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("subscription " + in.ID)
			} else if err != nil {
				return nil, api.Internal("reading subscription", err)
			}
			if in.Body.Enabled != nil {
				if err := deps.Store.SetSyncSubscriptionEnabled(ctx, id, *in.Body.Enabled); err != nil {
					return nil, api.Internal("setting subscription enabled", err)
				}
				auditAdminAction(ctx, deps, "admin.sync.subscription_enabled_set", in.ID)
			}
			if in.Body.OptInNoCache != nil {
				if err := deps.Store.SetSyncNoCacheOptIn(ctx, id, *in.Body.OptInNoCache); err != nil {
					return nil, api.Internal("setting subscription cache opt-in", err)
				}
				auditAdminAction(ctx, deps, "admin.sync.subscription_no_cache_set", in.ID)
			}
			updated, err := deps.Store.GetSyncSubscription(ctx, id)
			if err != nil {
				return nil, api.Internal("re-reading subscription", err)
			}
			data := rowOf(updated)
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})
}

// ── THE OPEN-VOCABULARY BOARD ────────────────────────────────────────────
//
// Principle 14: every external value HANGAR does not recognise is recorded
// and never rejected. app.open_vocabulary is where they land — cache modes
// and required roles from the ESI spec ingest, notification types from the
// sync handlers — and Gate 6 §6.1's whole demonstration is that a spec
// carrying values nobody anticipated is absorbed rather than refused.
//
// Nothing read the table. An open vocabulary that is written and never read
// is a decision to ignore the thing you deliberately went to the trouble of
// not rejecting, and it fails the same way the unknown-types board would
// have without Phase 18's acknowledge endpoint: it grows, nobody looks, and
// the one value that mattered is on page nine.

func registerVocabularyBoard(hapi huma.API, deps api.Deps) {
	get[VocabularyIn, CollectionOut](hapi, deps, "admin.vocabularies.view",
		"/api/v1/admin/vocabularies/{vocabulary}", "admin-vocabulary-board",
		"Observed values of one open vocabulary that are pending acknowledgement", adminTag,
		func(ctx context.Context, in *VocabularyIn) (*CollectionOut, error) {
			if !domain.IsKnownVocabulary(in.Vocabulary) {
				return nil, huma.Error404NotFound("no such vocabulary: " + in.Vocabulary)
			}
			rows, err := deps.Store.ListUnacknowledgedOpenVocabulary(ctx, in.Vocabulary)
			if err != nil {
				return nil, api.Internal("listing open vocabulary values", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	// The badge number, as its own endpoint. A count(*) is not a page of
	// rows and the navigation needs it without fetching them — the same
	// separation alerting.DeadLetterCount has from DeadLetterBoard.
	get[EmptyIn, ItemOut](hapi, deps, "admin.vocabularies.view",
		"/api/v1/admin/vocabularies", "admin-vocabularies",
		"Every open vocabulary, with its count of values pending acknowledgement", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
			counts := map[string]any{}
			for _, vocabulary := range domain.OpenVocabularies() {
				n, err := deps.Store.CountUnacknowledgedOpenVocabulary(ctx, string(vocabulary))
				if err != nil {
					return nil, api.Internal("counting open vocabulary values", err)
				}
				counts[string(vocabulary)] = n
			}
			data := map[string]any{"pending": counts}
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	mutate[AcknowledgeVocabularyIn, EmptyOut](hapi, deps, http.MethodPost, "admin.vocabularies.acknowledge",
		"/api/v1/admin/vocabularies/{vocabulary}/acknowledge", "admin-vocabulary-acknowledge",
		"Acknowledge one observed value", adminTag,
		func(ctx context.Context, in *AcknowledgeVocabularyIn) (*EmptyOut, error) {
			if !domain.IsKnownVocabulary(in.Vocabulary) {
				return nil, huma.Error404NotFound("no such vocabulary: " + in.Vocabulary)
			}
			if in.Body.Value == "" {
				return nil, huma.Error400BadRequest("value is required")
			}
			// Who acknowledged is recorded, unlike the scope and
			// notification-type boards, because app.open_vocabulary has an
			// acknowledged_by column and leaving it NULL when a real user
			// is on the request is throwing away audit information the
			// schema asked for.
			var by uuid.NullUUID
			if userID, ok := userIDFromCtx(ctx); ok {
				by = uuid.NullUUID{UUID: userID, Valid: true}
			}
			if err := deps.Store.AcknowledgeOpenVocabularyValue(ctx, in.Vocabulary, in.Body.Value, by); err != nil {
				return nil, api.Internal("acknowledging open vocabulary value", err)
			}
			auditAdminAction(ctx, deps, "admin.vocabulary_value_acknowledged", in.Vocabulary+"="+in.Body.Value)
			return &EmptyOut{}, nil
		})
}

// ── THE WEBHOOK DEAD-LETTER BOARD ────────────────────────────────────────
//
// §4.9's outbox has had the same guarantee as §4.4's alert queue since
// Phase 19 — at-least-once, then dead-letter — and the alerting half has had
// an admin board since Phase 15. events.DeadLetterBoard has been written,
// tested and unreachable the whole time, which means a webhook subscriber
// that went permanently unreachable produced deliveries that were correctly
// dead-lettered and that nobody could see.

func registerWebhookBoard(hapi huma.API, deps api.Deps) {
	get[EmptyIn, CollectionOut](hapi, deps, "admin.webhooks.view",
		"/api/v1/admin/webhooks/dead-letter", "admin-webhooks-dead-letter",
		"Webhook deliveries that will never be retried", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := events.DeadLetterBoard(ctx, deps.Store, 100)
			if err != nil {
				return nil, api.Internal("reading the webhook dead-letter board", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, ItemOut](hapi, deps, "admin.webhooks.view",
		"/api/v1/admin/webhooks/outbox", "admin-webhooks-outbox",
		"Undispatched §4.9 outbox backlog", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*ItemOut, error) {
			pending, err := events.PendingCount(ctx, deps.Store)
			if err != nil {
				return nil, api.Internal("counting the webhook outbox", err)
			}
			data := map[string]any{"undispatched": pending}
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})
}

// ── PLATFORM AND ENTITLEMENT CONFIGURATION ───────────────────────────────

func registerPlatformConfiguration(hapi huma.API, deps api.Deps) {
	mutate[CreatePlatformIn, ItemOut](hapi, deps, http.MethodPost, "provisioning.platforms.manage",
		"/api/v1/admin/platforms", "admin-create-platform",
		"Create a provisioning platform connection", adminTag,
		func(ctx context.Context, in *CreatePlatformIn) (*ItemOut, error) {
			if in.Body.Name == "" {
				return nil, huma.Error400BadRequest("name is required")
			}
			if !contains(domain.PlatformKinds(), in.Body.Kind) {
				return nil, huma.Error422UnprocessableEntity("unknown platform kind " + in.Body.Kind +
					"; expected one of " + joinQuoted(domain.PlatformKinds()))
			}
			config := in.Body.Config
			if len(config) == 0 {
				// An empty object, not SQL NULL: app.platform.config is
				// NOT NULL, and the three drivers take their credentials
				// from the environment rather than from this column, so
				// "no platform-specific configuration" is the ordinary
				// case rather than an omission.
				config = json.RawMessage(`{}`)
			}
			row, err := deps.Store.CreatePlatform(ctx, in.Body.Kind, in.Body.Name, config)
			if err != nil {
				return nil, api.Internal("creating platform", err)
			}
			auditAdminAction(ctx, deps, "admin.provisioning.platform_created", in.Body.Kind+":"+in.Body.Name)
			data := rowOf(row)
			// The driver binds to platform rows at PROCESS START
			// (cmd/hangar/discord.go and its two siblings list platforms
			// once and register a driver per row), so a platform created
			// on a running installation has no driver until the next
			// restart. Said here rather than left to be discovered as
			// "provisioning does nothing for the platform I just made".
			data["driver_active_after_restart"] = true
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	mutate[CreatePlatformGroupIn, ItemOut](hapi, deps, http.MethodPost, "provisioning.platforms.manage",
		"/api/v1/admin/platforms/{id}/groups", "admin-create-platform-group",
		"Register a remote group as an entitlement target", adminTag,
		func(ctx context.Context, in *CreatePlatformGroupIn) (*ItemOut, error) {
			platformID, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("platform id is not a uuid")
			}
			if in.Body.RemoteRef == "" || in.Body.Name == "" {
				return nil, huma.Error400BadRequest("remote_ref and name are both required")
			}
			if _, err := deps.Store.GetPlatform(ctx, platformID); errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("platform " + in.ID)
			} else if err != nil {
				return nil, api.Internal("reading platform", err)
			}
			row, err := deps.Store.CreatePlatformGroup(ctx, platformID, in.Body.RemoteRef, in.Body.Name)
			if err != nil {
				return nil, api.Internal("creating platform group", err)
			}
			auditAdminAction(ctx, deps, "admin.provisioning.platform_group_created", in.Body.RemoteRef)
			data := rowOf(row)
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	get[GroupRulesIn, CollectionOut](hapi, deps, "provisioning.entitlements.manage",
		"/api/v1/admin/platforms/{id}/groups/{group_id}/rules", "admin-platform-group-rules",
		"Every enabled rule that grants one platform group", adminTag,
		func(ctx context.Context, in *GroupRulesIn) (*CollectionOut, error) {
			groupID, err := parseUUID(in.GroupID)
			if err != nil {
				return nil, huma.Error400BadRequest("group id is not a uuid")
			}
			rows, err := deps.Store.ListEntitlementRulesForGroup(ctx, groupID)
			if err != nil {
				return nil, api.Internal("listing rules for group", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	// The reverse lookup, and the one an operator needs before deleting
	// anything: "what does this squad currently grant?" Answering it from
	// the per-platform rule list means reading every platform and filtering
	// by hand, which is how a squad gets deleted and three Discord roles
	// silently stop being provisioned.
	get[EntitlementSourceIn, CollectionOut](hapi, deps, "provisioning.entitlements.manage",
		"/api/v1/admin/entitlements/by-source", "admin-entitlements-by-source",
		"Every enabled entitlement rule driven by one source (squad, role, corporation, alliance)", adminTag,
		func(ctx context.Context, in *EntitlementSourceIn) (*CollectionOut, error) {
			if in.Kind == "" || in.Ref == "" {
				return nil, huma.Error400BadRequest("source_kind and source_ref are both required")
			}
			rows, err := deps.Store.ListEntitlementRulesForSource(ctx, in.Kind, in.Ref)
			if err != nil {
				return nil, api.Internal("listing rules for source", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	mutate[SetRuleEnabledIn, EmptyOut](hapi, deps, http.MethodPatch, "provisioning.entitlements.manage",
		"/api/v1/admin/platforms/{id}/rules/{rule_id}", "admin-set-rule-enabled",
		"Enable or disable one entitlement rule without deleting it", adminTag,
		func(ctx context.Context, in *SetRuleEnabledIn) (*EmptyOut, error) {
			// Stamped ONCE, before any work, for the reason
			// deleteEntitlementRuleHandler records: §2.2's SLO is measured
			// from the originating event, not from each user's turn in the
			// loop.
			eventAt := time.Now()

			platformID, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("platform id is not a uuid")
			}
			ruleID, err := parseUUID(in.RuleID)
			if err != nil {
				return nil, huma.Error400BadRequest("rule id is not a uuid")
			}
			if deps.Pool == nil || deps.Urgent == nil {
				// Not a 501 — the endpoint IS implemented. A process built
				// without the revocation enqueuer cannot honour the <60s
				// SLO a disable is defined by, and flipping the flag
				// anyway would leave every affected user's groups live
				// until the next bulk reconcile.
				return nil, api.Internal("disabling entitlement rule", errNoUrgentEnqueuer)
			}
			// The rule must belong to the platform in the path — the same
			// check the delete makes, for the same reason: otherwise the
			// operator's audit trail describes something that did not
			// happen.
			//
			// Resolved through the rule's own group rather than through
			// ListEntitlementRulesForPlatform, which the delete uses.
			// That list has `AND er.enabled` in its predicate, so a
			// DISABLED rule is invisible to it — and a route whose whole
			// purpose includes re-enabling one cannot be built on a lookup
			// that cannot see it.
			rule, err := deps.Store.GetEntitlementRule(ctx, ruleID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("entitlement rule " + in.RuleID)
			} else if err != nil {
				return nil, api.Internal("reading entitlement rule", err)
			}
			group, err := deps.Store.GetPlatformGroup(ctx, rule.GroupID)
			if err != nil {
				return nil, api.Internal("resolving the rule's platform", err)
			}
			if group.PlatformID != platformID {
				return nil, api.NotFound("entitlement rule on this platform")
			}

			if err := SetEntitlementRuleEnabled(ctx, deps.Pool, deps.Urgent, ruleID, in.Body.Enabled, eventAt, "entitlement_rule_disabled"); err != nil {
				return nil, api.Internal("setting rule enabled", err)
			}
			auditAdminAction(ctx, deps, "admin.provisioning.rule_enabled_set", in.RuleID)
			return &EmptyOut{}, nil
		})
}
