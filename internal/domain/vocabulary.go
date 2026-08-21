// Package domain holds HANGAR's own entities, invariants and closed
// vocabularies — the things Principle 13/14 say are *not* driven by an
// external spec. Tier-2 (domain-projection) entities land in Phase 1b;
// Phase 1a contributes the pieces every later phase needs immediately:
// money typing, identifier typing, and the two vocabularies HANGAR itself
// owns (see money.go, ids.go).
package domain

// OpenVocabulary names the finite set of `vocabulary` values valid in
// app.open_vocabulary (02_DATABASE_SCHEMA.md §3.3, Principle 14). Adding a
// new *category* of open vocabulary is a code change here; the *values*
// within each category are never closed — that is the entire point of the
// table, and no switch statement or CHECK constraint may reject one.
type OpenVocabulary string

const (
	VocabRefType          OpenVocabulary = "ref_type"
	VocabLocationType     OpenVocabulary = "location_type"
	VocabNotificationType OpenVocabulary = "notification_type"
	VocabScope            OpenVocabulary = "scope"
	VocabCacheMode        OpenVocabulary = "cache_mode"
	VocabContractStatus   OpenVocabulary = "contract_status"
	VocabRequiredRole     OpenVocabulary = "required_role"
)

// OpenVocabularies is every category above, in stable order.
//
// PHASE 23 (N-4). It exists because the admin board that reads
// app.open_vocabulary has to enumerate the categories to count them, and a
// second list of the same seven strings written in the API layer is a
// second list that can drift from this one. The CATEGORIES are closed —
// adding one is a code change, as the type comment says — while the values
// inside each never are, which is what makes enumerating the categories
// legitimate and enumerating the values a Principle 14 violation.
func OpenVocabularies() []OpenVocabulary {
	return []OpenVocabulary{
		VocabRefType, VocabLocationType, VocabNotificationType, VocabScope,
		VocabCacheMode, VocabContractStatus, VocabRequiredRole,
	}
}

// IsKnownVocabulary reports whether a string names one of the categories.
// A request for a category that does not exist is a 404, not an empty
// board: an empty board says "nothing pending", and telling an operator
// that about a misspelled vocabulary is a lie they would act on.
func IsKnownVocabulary(name string) bool {
	for _, vocabulary := range OpenVocabularies() {
		if string(vocabulary) == name {
			return true
		}
	}
	return false
}

// PlatformKinds is app.platform.kind's CHECK constraint — HANGAR's three
// supported provisioning drivers (02_DATABASE_SCHEMA.md §4.4 #33).
//
// A CLOSED vocabulary, and legitimately so under Principle 14: it names
// HANGAR's own drivers, not anything an external system reports. A fourth
// value would need a driver implementation, a migration and an entry here,
// in that order.
func PlatformKinds() []string { return []string{"discord", "teamspeak", "mumble"} }

// SuperuserPermission is the one permission internal/rbac.Resolve treats
// specially: holding it (without an explicit deny on it) implicitly
// allows every other permission that is not itself explicitly denied.
// Named here, not in internal/rbac, because domain is the owner of the
// closed permission vocabulary — internal/rbac only consumes it.
const SuperuserPermission = "superuser"

// Permission is one row of app.permission — HANGAR's own closed RBAC
// vocabulary (the deliberate exception carved out by SRS v3.1 defect B11:
// Principle 14 scopes openness to *external* vocabularies only).
type Permission struct {
	Name        string
	Category    string
	Description string
}

// Permissions is the single source of truth for app.permission. It is never
// edited by hand in SQL: `go generate ./internal/domain/...` (see
// tools/gen-permission-seed) renders it into db/seed/permissions.sql, and
// TestPermissionSeedMatchesGoSet (internal/domain/vocabulary_test.go) fails
// CI the moment the two diverge — that is the "CI divergence check" the
// roadmap requires for this table.
//
//go:generate go run ../../tools/gen-permission-seed
var Permissions = []Permission{
	// -- superuser --
	// Phase 10 spec gap, reported rather than silently papered over: the
	// roadmap's RBAC design notes require "Superuser is a permission, not
	// a bypass branch in code. A bypass branch cannot be denied" — but no
	// such permission existed in this closed set before Phase 10. Added
	// here so it is deniable like anything else (internal/rbac.Resolve
	// treats it as an implicit fallback allow for every OTHER permission,
	// but never overrides an explicit deny on the specific permission
	// being checked, and is itself denied/revoked exactly like any other
	// permission — see internal/rbac/resolve.go's doc comment).
	{SuperuserPermission, "admin", "Bypass every permission check unless the specific permission is itself denied"},

	// -- characters --
	{"characters.view", "characters", "View character sheets, skills and clones"},
	{"characters.manage_tokens", "characters", "Add or refresh a character's ESI token"},
	{"characters.revoke_tokens", "characters", "Revoke a character's ESI token"},

	// -- corporations --
	{"corporations.view", "corporations", "View corporation structures, wallets and members"},
	{"corporations.manage", "corporations", "Manage a tracked corporation's sync configuration"},

	// -- alliances / sovereignty / markets / tools --
	//
	// PHASE 15.1 ADDITION. SRS §6.4 (alliances & sovereignty), §6.5
	// (markets) and §6.7 (utilities & support) each define a group of
	// endpoints, but this closed set had no permission covering any of
	// them — Phase 15 shipped those routes gated on "a resolved session
	// and nothing more" as an explicit stopgap and reported the gap rather
	// than inventing names mid-phase. These are those names.
	//
	// Deliberately NOT added, and still correctly session-only: the
	// /api/v1/me* family (self-scoped by definition — a permission
	// governing "may this user read their own account" is meaningless) and
	// /api/v1/meta/{esi-status,server-status} (installation health the SPA
	// shell renders for every signed-in user; gating it would break the
	// dashboard for ordinary members, which is the opposite of the intent).
	{"alliances.view", "alliances", "View alliance sheets, member corporations and contacts"},
	{"sovereignty.view", "sovereignty", "View sovereignty campaigns and system ownership"},
	{"markets.view", "markets", "View market orders, price history and regional market data"},
	{"tools.view", "tools", "Use reference lookups: universe locations, insurance prices and standings"},

	// -- squads --
	{"squads.view", "squads", "View squad rosters and applications"},
	{"squads.create", "squads", "Create a new squad"},
	{"squads.manage", "squads", "Edit squad settings, roles and membership"},
	{"squads.moderate", "squads", "Approve or reject squad applications"},
	{"squads.apply", "squads", "Apply to join a squad"},

	// -- admin --
	{"admin.settings.manage", "admin", "Change installation-wide runtime settings"},
	{"admin.users.manage", "admin", "Manage user accounts and role assignment"},
	{"admin.roles.manage", "admin", "Create or edit RBAC roles and grants"},
	{"admin.security_log.view", "admin", "View the append-only security log"},
	{"admin.esi_routes.manage", "admin", "Edit route catalogue overrides (blocked_by_pin, TTL)"},
	{"admin.esi_pin.advance", "admin", "Advance the ESI compatibility date pin"},
	// PHASE 15.1 ADDITION — the read side of §6.8. Before this, only three
	// of §6.8's routes had a permission at all (settings/users/roles
	// manage, security_log.view, esi_pin.advance) and Phase 15 gated every
	// §6.8 *read* on `superuser` as a stopgap, which made the entire
	// observability surface all-or-nothing: an operator who should see
	// sync health had to be handed a permission that bypasses every other
	// check in the system. These split it apart.
	{"admin.sync.view", "admin", "View the sync route catalogue, subscriptions and aggregate health"},
	{"admin.esi.view", "admin", "View ESI gateway state: blocked routes, rate limits, error budget and replicas"},
	{"admin.platforms.view", "admin", "View configured provisioning platforms"},
	{"admin.scopes.view", "admin", "View newly observed ESI scope strings pending acknowledgement"},
	// PHASE 18 ADDITION. The scope board's counterpart to
	// alerting.unknown_types.acknowledge, which Phase 14 defined but whose
	// scope-side twin nobody ever did — so the unknown-scope board could be
	// read and never cleared. Acknowledging is a separate authority from
	// viewing for the same reason it is on the alerting side: it is an
	// assertion that a human has looked at a novel scope grammar and
	// decided what it means.
	{"admin.scopes.acknowledge", "admin", "Acknowledge a newly observed ESI scope string"},

	// ── PHASE 23 (N-4): FOUR PERMISSIONS FOR FOUR SURFACES THAT NOW EXIST ──
	//
	// Every one of these guards an endpoint added in the same commit. A
	// permission added ahead of its endpoint is the mirror of what N-4 is
	// about — `alerting.channels.manage` and `alerting.routing.manage` have
	// sat in this list since Phase 10 with nothing behind either, which is
	// exactly as much evidence of a working feature as a query with no
	// caller.
	//
	// admin.sync.manage is deliberately SEPARATE from admin.sync.view. The
	// view permission is already granted widely enough to read a route
	// catalogue; disabling a subscription stops an entity synchronising,
	// and an operator who can look at the sync board should not thereby be
	// able to switch it off.
	{"admin.sync.manage", "admin", "Enable, disable or change caching on individual sync subscriptions"},
	{"admin.vocabularies.view", "admin", "View observed open-vocabulary values pending acknowledgement"},
	// Acknowledging is separated from viewing for the same reason it is on
	// the scope and notification-type boards: it is an assertion that a
	// human has looked at a value HANGAR did not recognise and decided what
	// it means.
	{"admin.vocabularies.acknowledge", "admin", "Acknowledge an observed open-vocabulary value"},
	// Distinct from `webhooks.manage`, which is USER-scoped — it lets a
	// user create and revoke their own endpoints. This is the
	// installation-wide dead-letter board and outbox backlog, which show
	// every subscriber's deliveries.
	{"admin.webhooks.view", "admin", "View the webhook dead-letter board and outbox backlog"},

	// -- provisioning --
	{"provisioning.platforms.manage", "provisioning", "Configure Discord/TeamSpeak/Mumble platform connections"},
	{"provisioning.entitlements.manage", "provisioning", "Edit entitlement rules that grant platform groups"},
	{"provisioning.audit.view", "provisioning", "View the provisioning audit trail"},
	// PHASE 15.1: split out of provisioning.audit.view, whose description
	// claimed "and exposure board" but which no exposure-board route ever
	// used — GET /admin/provisioning/exposures was gated on `superuser`.
	// The two are different reads: the audit trail is a history of calls
	// made, the exposure board is the live set of users whose actual
	// platform groups disagree with their entitlements.
	{"provisioning.exposures.view", "provisioning", "View the provisioning exposure board (live desired-vs-actual group mismatches)"},

	// -- alerting --
	{"alerting.channels.manage", "alerting", "Configure alert delivery channels"},
	{"alerting.routing.manage", "alerting", "Edit alert routing rules"},
	{"alerting.unknown_types.acknowledge", "alerting", "Acknowledge unrecognised notification types"},
	// PHASE 15.1 ADDITION. Phase 14 defined only the *acknowledge* half of
	// the unknown-types workflow and nothing at all for the dead-letter
	// board, so Phase 15's §6.8 handlers for both were gated on
	// `superuser`. Viewing and acting are separated here: requeuing a
	// dead-lettered delivery re-sends a real message to a real platform,
	// which is not the same authority as reading the board.
	{"alerting.unknown_types.view", "alerting", "View unrecognised notification types pending acknowledgement"},
	{"alerting.deadletter.view", "alerting", "View the alert delivery dead-letter board"},
	{"alerting.deadletter.requeue", "alerting", "Requeue a dead-lettered alert delivery"},

	// -- api tokens & webhooks --
	{"api_tokens.manage", "api", "Create or revoke third-party API tokens"},
	{"api_tokens.view_access_log", "api", "View third-party API token access logs"},
	{"webhooks.manage", "api", "Create or revoke outbound webhook endpoints"},
}
