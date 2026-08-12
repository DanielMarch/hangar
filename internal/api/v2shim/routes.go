package v2shim

import "net/http"

// Routes is the shim's route table — the single place that says which
// legacy read routes are served, which corpus recording each is measured
// against, and which RBAC permission guards it.
//
// ── COVERAGE, STATED HONESTLY ────────────────────────────────────────────
// Legacy's /api/v2 has 33 GET routes across nine controllers. They fall
// into three groups, and the split is not arbitrary — it follows HANGAR's
// identity model:
//
//  1. SERVED HERE. Routes keyed by an EVE identifier (character,
//     corporation, alliance id) whose response shape comes from an explicit
//     SeAT Resource class. HANGAR stores those ids unchanged and the field
//     lists line up, so byte-compatibility is achievable and achieved.
//
//  2. NOT TRANSLATABLE — IDENTITY. Every route under /users and /squads.
//     Legacy's `users.id` and `squads.id` are MySQL auto-increment
//     integers; HANGAR's app.user.user_id and app.squad.squad_id are uuids
//     (02_DATABASE_SCHEMA.md §4.1/§4.5). `"id":1` and
//     `"id":"019ff31f-..."` are not a formatting difference, they are
//     different identifier spaces, and no translation invents the integer
//     a legacy client stored. SRS Appendix C marks only RoleController and
//     RoleLookupController as breaking; User and Squad are equally
//     un-shimmable in their identity fields, and that is reported as a
//     specification inconsistency rather than papered over.
//
//  3. NOT TRANSLATABLE — GRANT MODEL. RoleController and
//     RoleLookupController, which SRS Appendix C already documents as
//     breaking with no shim. See translate_breaking.go.
//
// Group 1's remaining routes are mechanical repetitions of the patterns in
// translate_contacts.go and translate_character.go — a store query, an
// ordered Obj in the recorded field order, and req.PageOf. What is NOT
// mechanical, and is why they are not all here yet, is that several of
// them need a keyset-window store query that does not exist (the
// `no-offset` rule, see Window's comment) and HANGAR fixture data
// corresponding to the recorded corpus.
func Routes() []Route {
	return []Route{
		// ── AllianceController ───────────────────────────────────────────
		{
			Controller: "AllianceController",
			Method:     http.MethodGet,
			Pattern:    Prefix + "/alliance/contacts/{id}",
			Corpus:     "alliance.contacts",
			Permission: "alliances.view",
			Appends:    true,
			Handle:     listContacts("alliance"),
		},

		// ── CharacterController ──────────────────────────────────────────
		{
			Controller: "CharacterController",
			Method:     http.MethodGet,
			Pattern:    Prefix + "/character/contacts/{id}",
			Corpus:     "character.contacts",
			Permission: "characters.view",
			Appends:    true,
			Handle:     listContacts("character"),
		},
		{
			Controller: "CharacterController",
			Method:     http.MethodGet,
			Pattern:    Prefix + "/character/corporation-history/{id}",
			Corpus:     "character.corporation-history",
			Permission: "characters.view",
			Appends:    true,
			Handle:     characterCorporationHistory,
		},

		// ── CorporationController ────────────────────────────────────────
		{
			Controller: "CorporationController",
			Method:     http.MethodGet,
			Pattern:    Prefix + "/corporation/contacts/{id}",
			Corpus:     "corporation.contacts",
			Permission: "corporations.view",
			Appends:    true,
			Handle:     listContacts("corporation"),
		},
	}
}

// BreakingControllers are the legacy controllers that answer 410 Gone with
// a migration pointer instead of being shimmed.
func BreakingControllers() map[string]string {
	return map[string]string{
		"RoleController":       "/api/v2/roles",
		"RoleLookupController": "/api/v2/roles/query/permissions",
	}
}

// UnshimmableControllers are the ones whose responses cannot be made
// byte-compatible because HANGAR's identifiers are uuids where legacy's
// were integers. They answer the "not shimmed" 501 rather than a 410: the
// grant-model break is permanent and conceptual, whereas this one is an
// identifier-space mismatch a future release could address by exposing a
// stable legacy-id mapping, so saying "Gone" would overstate it.
func UnshimmableControllers() map[string]string {
	return map[string]string{
		"UserController":  "/api/v2/users",
		"SquadController": "/api/v2/squads",
	}
}

// PendingControllers are shimmable but NOT YET SHIMMED.
//
// This is unfinished work, recorded as unfinished rather than dressed up as
// a design decision. KillmailsController's three read routes are keyed by
// EVE identifiers and HANGAR holds every field they need except one —
// legacy's `attacker_hash`, a SeAT-internal surrogate for which
// app.killmail_attacker has `record_id` instead. Resolving that is a
// decision about what to emit for a field HANGAR does not have, the same
// question `map_id`/`map_name` and `SquadResource.logo` raise, and it is
// answered in docs/APPENDIX_C_MIGRATION.md's "fields the shim cannot
// reproduce" table rather than guessed at here.
//
// Until then these paths answer the read-only 501 with a message that says
// "not yet", which is the true statement. They are listed separately from
// UnshimmableControllers precisely so nobody reads a temporary gap as a
// permanent one — and so the coverage assertion in
// TestShimByteCompatibleForAllNineControllers has to name them.
func PendingControllers() map[string]string {
	return map[string]string{
		"KillmailsController": "/api/v2/killmails/15001",
	}
}
