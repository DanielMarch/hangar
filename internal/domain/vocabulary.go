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
	// -- characters --
	{"characters.view", "characters", "View character sheets, skills and clones"},
	{"characters.manage_tokens", "characters", "Add or refresh a character's ESI token"},
	{"characters.revoke_tokens", "characters", "Revoke a character's ESI token"},

	// -- corporations --
	{"corporations.view", "corporations", "View corporation structures, wallets and members"},
	{"corporations.manage", "corporations", "Manage a tracked corporation's sync configuration"},

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

	// -- provisioning --
	{"provisioning.platforms.manage", "provisioning", "Configure Discord/TeamSpeak/Mumble platform connections"},
	{"provisioning.entitlements.manage", "provisioning", "Edit entitlement rules that grant platform groups"},
	{"provisioning.audit.view", "provisioning", "View the provisioning audit trail and exposure board"},

	// -- alerting --
	{"alerting.channels.manage", "alerting", "Configure alert delivery channels"},
	{"alerting.routing.manage", "alerting", "Edit alert routing rules"},
	{"alerting.unknown_types.acknowledge", "alerting", "Acknowledge unrecognised notification types"},

	// -- api tokens & webhooks --
	{"api_tokens.manage", "api", "Create or revoke third-party API tokens"},
	{"api_tokens.view_access_log", "api", "View third-party API token access logs"},
	{"webhooks.manage", "api", "Create or revoke outbound webhook endpoints"},
}
