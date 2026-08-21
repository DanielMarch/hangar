package v1

// admin_alerting.go is §4.4's CONFIGURATION surface — the alert catalogue,
// the channel registry, the routing rules that connect them, and the event
// board that shows what came out.
//
// ── PHASE 23 (N-4): WHY IT DID NOT EXIST ─────────────────────────────────
//
// Eight generated queries had no production caller — ListAlertTypes,
// GetAlertType, CountAlertTypesByDomain, ListAlertChannels,
// CreateAlertRoutingRule, ListRecentAlertEvents,
// ListAlertEventsForCoalesceKeySince and
// ListUnparseableCharacterNotifications — and two permissions have been in
// the closed vocabulary since Phase 10 with no endpoint behind either:
// `alerting.channels.manage` and `alerting.routing.manage`. A permission
// nothing checks is the same shape of evidence as a query nothing calls.
//
// N-9 is what made it urgent. Before this phase a stock installation
// delivered no alerts at all, so "an operator cannot configure where alerts
// go" was academic. Now that `serve` runs §4.4's producers and pump, an
// installation PRODUCES alerts and can still only decide their destination
// by writing SQL — which makes this the half that turns N-9 from a fix into
// a feature.
//
// ── WHAT IS DELIBERATELY NOT HERE ────────────────────────────────────────
//
// There is no DELETE for a channel and no edit for a routing rule. Both are
// real gaps and both are stated rather than quietly filled with a
// half-answer: a channel with deliveries against it cannot simply vanish
// (app.alert_delivery.channel_id is ON DELETE CASCADE, so deleting one
// would silently erase its delivery history, which is audit evidence), and
// `enabled` is the disable an operator actually wants. Rules are created
// and disabled, not edited, for the same reason the entitlement rule set is
// replaced rather than patched: an edited routing rule and a new one are
// indistinguishable afterwards, and routing is exactly where that matters.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// alertTargetKinds is app.alert_routing_rule.target_kind's CHECK constraint,
// restated here so a bad value is a 400 with the list in it rather than a
// 500 carrying a Postgres constraint name. The database remains the
// authority; this is the error message, not the rule.
var alertTargetKinds = []string{"user", "squad", "corporation", "alliance", "installation"}

// CreateChannelIn is POST /api/v1/admin/alerts/channels.
//
// Config is passed through verbatim to app.alert_channel.config and
// validated by channels.New at CONSTRUCTION — the same path the pump uses.
// Validating it here with a second parser is how the API and the dispatcher
// come to disagree about what a valid Slack config is.
type CreateChannelIn struct {
	Body struct {
		Kind   string          `json:"kind" doc:"smtp, slack_webhook or discord_webhook."`
		Name   string          `json:"name" doc:"Operator-facing name; unique."`
		Config json.RawMessage `json:"config" doc:"Channel-specific configuration, validated by the same constructor the pump uses."`
	}
}

// CreateRoutingRuleIn is POST /api/v1/admin/alerts/types/{type}/rules.
type CreateRoutingRuleIn struct {
	Type string `path:"type" doc:"app.alert_type.alert_type."`
	Body struct {
		TargetKind string `json:"target_kind" doc:"user, squad, corporation, alliance or installation."`
		TargetRef  string `json:"target_ref,omitempty" doc:"Id of the target entity; empty for 'installation'."`
		ChannelID  string `json:"channel_id" format:"uuid"`
		Mention    string `json:"mention,omitempty" doc:"Channel-native mention to lead the message with, e.g. @here."`
	}
}

// AlertTypeIn is a single alert-type path parameter.
type AlertTypeIn struct {
	Type string `path:"type"`
}

// AlertEventsIn is GET /api/v1/admin/alerts/events.
//
// The two lookups are mutually exclusive because they answer different
// questions. `type` is "what has this alert type produced lately"; the
// coalescing pair is "what went into THAT roll-up" — the question an
// operator asks holding a message that says "42 events" and wanting the
// forty-two.
type AlertEventsIn struct {
	Type        string `query:"type" doc:"Alert type to list recent events for."`
	CoalesceKey string `query:"coalesce_key" doc:"Coalescing key; lists the events that were rolled up under it."`
	SinceHours  int32  `query:"since_hours" default:"24" doc:"How far back the coalescing lookup reaches, in hours."`
	Limit       int32  `query:"limit" default:"50" doc:"Page size for the by-type lookup, 1-200."`
}

// SetChannelEnabledIn / SetRuleEnabledIn are the two disables. Neither
// deletes: see the file header.
type SetChannelEnabledIn struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

func registerAdminAlerting(hapi huma.API, deps api.Deps) {
	// ── THE CATALOGUE ────────────────────────────────────────────────────
	//
	// Read behind `alerting.routing.manage` rather than behind a new
	// view-only permission. The catalogue is only interesting to somebody
	// deciding where an alert should GO, the vocabulary is closed and
	// adding to it is a schema-seed change, and a permission invented to
	// avoid reusing one that fits is a permission nobody will ever grant
	// deliberately.
	get[EmptyIn, CollectionOut](hapi, deps, "alerting.routing.manage", "/api/v1/admin/alerts/types", "admin-alerts-types",
		"The alert catalogue: every type §4.4 can raise, with its domain, severity and coalescing window", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListAlertTypes(ctx)
			if err != nil {
				return nil, api.Internal("listing alert types", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	get[EmptyIn, CollectionOut](hapi, deps, "alerting.routing.manage", "/api/v1/admin/alerts/types/by-domain", "admin-alerts-types-by-domain",
		"Alert type counts per domain", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.CountAlertTypesByDomain(ctx)
			if err != nil {
				return nil, api.Internal("counting alert types by domain", err)
			}
			data := rowSliceOf(rows)
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	// One alert type with the rules that route it — deliberately ONE
	// response and not two round trips. "Where does this alert go?" is a
	// single question, and an operator who has to assemble the answer from
	// two screens is an operator who will assemble it wrongly once.
	get[AlertTypeIn, ItemOut](hapi, deps, "alerting.routing.manage", "/api/v1/admin/alerts/types/{type}", "admin-alerts-type",
		"One alert type and every routing rule that targets it", adminTag,
		func(ctx context.Context, in *AlertTypeIn) (*ItemOut, error) {
			alertType, err := deps.Store.GetAlertType(ctx, in.Type)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("alert type " + in.Type)
			} else if err != nil {
				return nil, api.Internal("reading alert type", err)
			}
			rules, err := deps.Store.ListAlertRoutingRulesForType(ctx, in.Type)
			if err != nil {
				return nil, api.Internal("listing routing rules", err)
			}
			data := rowOf(alertType)
			data["routing_rules"] = rowSliceOf(rules)
			// Stated explicitly rather than left to be inferred from an
			// empty array. An alert type with no rule produces events that
			// are recorded and never delivered — §4.4's "routed to nobody"
			// — and that is the single most likely misconfiguration on a
			// fresh installation, where ensureDefaultAlertChannels creates
			// channels and DELIBERATELY creates no rules.
			data["routed"] = len(rules) > 0
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	// ── CHANNELS ─────────────────────────────────────────────────────────
	get[EmptyIn, CollectionOut](hapi, deps, "alerting.channels.manage", "/api/v1/admin/alerts/channels", "admin-alerts-channels",
		"Configured alert delivery channels", adminTag,
		func(ctx context.Context, _ *EmptyIn) (*CollectionOut, error) {
			rows, err := deps.Store.ListAlertChannels(ctx)
			if err != nil {
				return nil, api.Internal("listing alert channels", err)
			}
			// app.alert_channel.config carries CREDENTIALS — a Slack
			// webhook URL is a bearer token for that channel, and the SMTP
			// config holds a password. Redacted here rather than at the
			// column, because the pump needs the real thing and only this
			// boundary is the one where it leaves the process.
			data := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				item := rowOf(row)
				item["config"] = redactChannelConfig(row.Config)
				data = append(data, item)
			}
			return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
		})

	mutate[CreateChannelIn, ItemOut](hapi, deps, http.MethodPost, "alerting.channels.manage",
		"/api/v1/admin/alerts/channels", "admin-alerts-create-channel", "Create an alert delivery channel", adminTag,
		func(ctx context.Context, in *CreateChannelIn) (*ItemOut, error) {
			if in.Body.Name == "" {
				return nil, huma.Error400BadRequest("name is required")
			}
			if !knownChannelKind(in.Body.Kind) {
				return nil, huma.Error422UnprocessableEntity("unknown channel kind " + in.Body.Kind +
					"; expected one of " + joinQuoted(channels.KnownKinds()))
			}
			// Constructed BEFORE it is stored, through the same
			// constructor the dispatcher uses. A channel row that cannot
			// be built is a row whose every delivery dead-letters on
			// "building channel" — discovered by an operator as an alert
			// that never arrived, which is the failure §4.4 exists to
			// prevent. Rejecting it here costs one round trip.
			if _, err := channels.New(in.Body.Kind, in.Body.Config); err != nil {
				return nil, huma.Error422UnprocessableEntity("channel configuration is not usable: " + err.Error())
			}
			row, err := deps.Store.CreateAlertChannel(ctx, in.Body.Kind, in.Body.Name, in.Body.Config)
			if err != nil {
				return nil, api.Internal("creating alert channel", err)
			}
			auditAdminAction(ctx, deps, "admin.alerting.channel_created", in.Body.Name)
			data := rowOf(row)
			data["config"] = redactedConfigMarker
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	mutate[SetChannelEnabledIn, EmptyOut](hapi, deps, http.MethodPatch, "alerting.channels.manage",
		"/api/v1/admin/alerts/channels/{id}", "admin-alerts-set-channel-enabled",
		"Enable or disable one alert channel", adminTag,
		func(ctx context.Context, in *SetChannelEnabledIn) (*EmptyOut, error) {
			id, err := parseUUID(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("channel id is not a uuid")
			}
			if err := deps.Store.SetAlertChannelEnabled(ctx, id, in.Body.Enabled); err != nil {
				return nil, api.Internal("setting channel enabled", err)
			}
			auditAdminAction(ctx, deps, "admin.alerting.channel_enabled_set", in.ID)
			return &EmptyOut{}, nil
		})

	// ── ROUTING ──────────────────────────────────────────────────────────
	mutate[CreateRoutingRuleIn, ItemOut](hapi, deps, http.MethodPost, "alerting.routing.manage",
		"/api/v1/admin/alerts/types/{type}/rules", "admin-alerts-create-rule",
		"Route one alert type to a channel for one audience", adminTag,
		func(ctx context.Context, in *CreateRoutingRuleIn) (*ItemOut, error) {
			if !contains(alertTargetKinds, in.Body.TargetKind) {
				return nil, huma.Error422UnprocessableEntity("unknown target kind " + in.Body.TargetKind +
					"; expected one of " + joinQuoted(alertTargetKinds))
			}
			// 'installation' is the whole-installation audience and takes
			// no ref; every other kind identifies an entity. Enforced here
			// because the column is merely nullable — the schema cannot
			// say "NULL exactly when kind is installation".
			if in.Body.TargetKind == "installation" && in.Body.TargetRef != "" {
				return nil, huma.Error422UnprocessableEntity("target_ref must be empty for target_kind 'installation'")
			}
			if in.Body.TargetKind != "installation" && in.Body.TargetRef == "" {
				return nil, huma.Error422UnprocessableEntity("target_ref is required for target_kind " + in.Body.TargetKind)
			}
			channelID, err := parseUUID(in.Body.ChannelID)
			if err != nil {
				return nil, huma.Error400BadRequest("channel_id is not a uuid")
			}
			// The alert type must exist. Without this the insert fails on
			// a foreign key and the operator is told about a constraint
			// rather than about a typo — and on a FRESH installation the
			// four THRESHOLD types genuinely do not exist yet (they are
			// completed by the first catalogue ingest), so "no such alert
			// type" is a real and temporary state worth naming.
			if _, err := deps.Store.GetAlertType(ctx, in.Type); errors.Is(err, pgx.ErrNoRows) {
				return nil, api.NotFound("alert type " + in.Type)
			} else if err != nil {
				return nil, api.Internal("reading alert type", err)
			}
			params := gen.CreateAlertRoutingRuleParams{
				AlertType: in.Type, TargetKind: in.Body.TargetKind, ChannelID: channelID,
			}
			if in.Body.TargetRef != "" {
				ref := in.Body.TargetRef
				params.TargetRef = &ref
			}
			if in.Body.Mention != "" {
				mention := in.Body.Mention
				params.Mention = &mention
			}
			row, err := deps.Store.CreateAlertRoutingRule(ctx, params)
			if err != nil {
				return nil, api.Internal("creating routing rule", err)
			}
			auditAdminAction(ctx, deps, "admin.alerting.routing_rule_created", in.Type)
			data := rowOf(row)
			return &ItemOut{Body: api.Item[map[string]any]{Data: &data, Sync: api.Sync{}}}, nil
		})

	// ── THE EVENT BOARD ──────────────────────────────────────────────────
	get[AlertEventsIn, CollectionOut](hapi, deps, "alerting.routing.manage", "/api/v1/admin/alerts/events", "admin-alerts-events",
		"Recent alert events, by type or by coalescing key", adminTag,
		func(ctx context.Context, in *AlertEventsIn) (*CollectionOut, error) {
			switch {
			case in.Type != "" && in.CoalesceKey != "":
				return nil, huma.Error400BadRequest("pass either type or coalesce_key, not both")
			case in.CoalesceKey != "":
				since := in.SinceHours
				if since <= 0 {
					since = 24
				}
				key := in.CoalesceKey
				rows, err := deps.Store.ListAlertEventsForCoalesceKeySince(ctx, &key, time.Now().Add(-time.Duration(since)*time.Hour))
				if err != nil {
					return nil, api.Internal("listing coalesced events", err)
				}
				data := rowSliceOf(rows)
				return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
			case in.Type != "":
				limit := in.Limit
				if limit < 1 || limit > 200 {
					limit = 50
				}
				rows, err := deps.Store.ListRecentAlertEvents(ctx, in.Type, limit)
				if err != nil {
					return nil, api.Internal("listing recent alert events", err)
				}
				data := rowSliceOf(rows)
				return &CollectionOut{Body: api.Collection[map[string]any]{Data: data, Page: api.EmptyPage(int32(len(data))), Sync: api.Sync{}}}, nil
			default:
				return nil, huma.Error400BadRequest("one of type or coalesce_key is required")
			}
		})
}

const redactedConfigMarker = "[redacted — channel configuration holds credentials]"

// redactChannelConfig replaces a channel's stored configuration with a
// marker plus the non-secret fields worth showing.
//
// A Slack or Discord webhook URL IS the credential — anyone holding one can
// post to the channel — and the SMTP config carries a password. Returning
// them from an authenticated admin endpoint would put them in browser
// history, in any proxy log on the path, and in the response cache of every
// tool an operator uses to debug this screen. The operator does not need
// them: they set them, and what they need afterwards is whether the row is
// the one they think it is.
func redactChannelConfig(config json.RawMessage) map[string]any {
	out := map[string]any{"redacted": redactedConfigMarker}
	var parsed map[string]any
	if err := json.Unmarshal(config, &parsed); err != nil {
		return out
	}
	// The SMTP host and destination list are not secrets, and they are the
	// fields an operator genuinely has to check against a row: "is this the
	// channel that mails ops@, or the one that mails everyone?"
	if to, ok := parsed["to"]; ok {
		out["to"] = to
	}
	if host, ok := parsed["host"]; ok {
		out["host"] = host
	}
	return out
}

func knownChannelKind(kind string) bool { return contains(channels.KnownKinds(), kind) }

func contains(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

func joinQuoted(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += `"` + v + `"`
	}
	return out
}
