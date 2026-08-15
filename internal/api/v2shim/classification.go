package v2shim

import "net/http"

// classification.go is Gate 7's answer, per ROUTE rather than per controller.
//
// ── WHY PER-ROUTE, AND WHY THE ROUTER READS IT ───────────────────────────
// Until Phase 20.6 this package had three functions — BreakingControllers,
// UnshimmableControllers, PendingControllers — and the router consulted NONE
// of them. Register() hard-coded the two `/api/v2/roles*` prefixes and let
// everything else fall through to one generic 501. So the lists were
// documentation that happened to compile, and all three sat on the
// reachability allowlist as unreachable symbols, which is exactly what they
// were.
//
// Worse, the coverage test iterated CONTROLLERS. CharacterController counted
// as "covered" on the strength of two routes out of fifteen, so the headline
// "4 of 33 routes" was true and the test that was supposed to police it
// could not see the other 29.
//
// Both are fixed here. Classification() names all 33 legacy read routes with
// a status and a reason; Register() derives every response from it; and
// TestEveryLegacyReadRouteIsClassified iterates ROUTES, so a route can no
// longer hide behind a sibling.
//
// ── THE FOUR STATUSES ────────────────────────────────────────────────────
// The distinction that matters is between a break that is PERMANENT
// (StatusBreaking, StatusUnshimmable) and one that is UNFINISHED
// (StatusPending). A client reading "410 Gone" rewrites its integration; a
// client reading "501, not yet" waits. Telling them apart wrongly wastes
// somebody's quarter, which is why they are separate statuses with separate
// bodies rather than one "not available" answer.

// RouteStatus is what the shim does with one legacy read route.
type RouteStatus string

const (
	// StatusServed is shimmed AND byte-identical to its recording in
	// testdata/legacy-api-v2. A route that returns plausible JSON but does
	// not match those bytes is NOT served — it is Pending, because a client
	// diffing responses would find it broken.
	StatusServed RouteStatus = "served"

	// StatusPending is shimmable but not yet shimmed. Answers 501 with a
	// message that says "not yet". This is unfinished work recorded as
	// unfinished; it is not a design decision.
	StatusPending RouteStatus = "pending"

	// StatusUnshimmable cannot be made byte-compatible, ever, because
	// HANGAR's identifier space differs from legacy's. Answers 501 rather
	// than 410: the identifier mismatch could in principle be addressed by a
	// future release exposing a stable legacy-id mapping, so "Gone" would
	// overstate it.
	StatusUnshimmable RouteStatus = "unshimmable"

	// StatusBreaking is a route whose underlying model changed so the
	// concept no longer exists. Answers 410 Gone with a migration pointer.
	StatusBreaking RouteStatus = "breaking"
)

// LegacyRoute is one of legacy's 34 /api/v2 read routes (32 recorded
// patterns plus the two unrecorded breaking role routes — measured from
// MANIFEST.json, not the "33" this package asserted for five phases).
type LegacyRoute struct {
	Controller string
	Method     string
	// Pattern is the net/http ServeMux pattern this route answers on.
	Pattern string
	// Corpus is the basename in testdata/legacy-api-v2/responses this route
	// is measured against, or "" where the recording carries no such route
	// (the two role controllers were never recorded — they are breaking).
	Corpus string
	Status RouteStatus
	// Reason is why this route has the status it has. Required for every
	// status except StatusServed, where the recording is the reason.
	Reason string
	// Migration is the /api/v1 equivalent, put in the 410/501 body.
	Migration string
	// Permission is the RBAC permission the equivalent /api/v1 route
	// requires. Only meaningful for StatusServed.
	Permission string
	// Appends mirrors the legacy controller's ->appends() behaviour.
	Appends bool
	// Handle produces the response body. Non-nil exactly when StatusServed.
	Handle func(*Request) (any, error)
}

const (
	migrationCharacters   = "/api/v1/characters/{id}"
	migrationCorporations = "/api/v1/corporations/{id}"
	migrationKillmails    = "/api/v1/{characters,corporations}/{id}/killmails"

	// reasonNoKeysetWindow is the shared blocker for the list routes that
	// remain. Legacy loaded the whole relation and paginated in PHP; HANGAR's
	// store exposes keyset pages (OFFSET is prohibited — SRS §6, enforced by
	// sqlc's no-offset rule), so each of these needs a store query that can
	// return the full ordered set for a window, and a fixture matching the
	// recorded corpus. Mechanical, but not free, and not guessable.
	reasonNoKeysetWindow = "shimmable: keyed by EVE identifiers HANGAR stores unchanged. " +
		"Needs a full-set store query and corpus fixtures; not yet written."

	reasonIdentitySpace = "HANGAR's user and squad ids are uuids where legacy's were MySQL " +
		"auto-increment integers. `\"id\":1` and `\"id\":\"019ff31f-…\"` are different " +
		"identifier spaces; no translation invents the integer a legacy client stored."

	reasonGrantModel = "the RBAC grant model changed shape entirely; there is no role object " +
		"to translate onto."

	// reasonKillmailHash is KillmailsController's specific blocker, separate
	// from reasonNoKeysetWindow because building the store query would not
	// resolve it.
	reasonKillmailHash = "shimmable in shape, blocked on two things: legacy's `attacker_hash` is a " +
		"SeAT-internal surrogate app.killmail_attacker has no column for (APPENDIX_C_MIGRATION.md §7), " +
		"and app.killmail has NO WRITER at all — the killmail sync handler was never built " +
		"(defect B47's not-built class), so the table is empty on every installation."

	// reasonMySQLDoubleRounding is a MEASURED conflict between the corpus and
	// this package's own double formatter, found in Phase 20.6 by writing the
	// route and running it against the recording rather than by reading either.
	//
	// The corpus seeds amount = 9007199254740993.01 and records
	// `"amount": 9007199254741000`. Those are two DIFFERENT float64 values:
	//
	//	strconv.ParseFloat("9007199254740993.01", 64) == 9007199254740994
	//	9007199254741000                              != 9007199254740994   (3 ulps apart)
	//
	// So legacy's at-rest value is not the IEEE-754 value nearest the number
	// it was given — 9007199254741000 is that number rounded to FOURTEEN
	// significant digits. encode.go's formatPHPDouble implements the shortest
	// round-tripping form on the stated premise that "PHP's default
	// serialize_precision is -1", and given the float 9007199254741000 it
	// emits the right bytes (encode_test.go pins exactly that). The conflict
	// is upstream of the encoder: either the recorder's PHP rendered at
	// precision 14, or MySQL's DOUBLE column did not hold what strtod would.
	//
	// Every OTHER double in the corpus (5.55, 0.1, 27289.0) is short enough
	// that shortest-round-trip and 14 significant digits are indistinguishable
	// — so the one value that can tell the two rules apart lives in the one
	// route nobody had implemented, and the ambiguity has never been
	// exercised. That is the finding, and it is not resolvable from inside Go:
	// settling it means re-running testdata/legacy-api-v2/recorder against PHP
	// 8.2.33 with serialize_precision instrumented.
	//
	// Seeding HANGAR with 9007199254741000 to force a match was considered and
	// REJECTED: legacy was GIVEN ...993.01 and stored ...741000; HANGAR is
	// given ...993.01 and stores it exactly, because NUMERIC(30,2) is exact.
	// The two systems genuinely hold different numbers, and changing the
	// fixture would be making the test agree with itself.
	reasonMySQLDoubleRounding = "blocked on a MEASURED corpus conflict, not on missing code. The " +
		"recording's `amount` (9007199254741000) is a different float64 from the value the fixture " +
		"seeds (9007199254740993.01 parses to 9007199254740994) — legacy's loss is MySQL's/PHP's " +
		"14-significant-digit rounding, not IEEE-754 nearest, so Money's float64 round-trip cannot " +
		"reproduce it. Resolving it needs the PHP recorder re-run with serialize_precision measured."

	// reasonSurrogateID is the blocker three of the four wallet routes hit
	// and the fourth does not, which is why byte-verification is the test
	// and "it returns plausible JSON" is not.
	//
	// character.wallet-journal's `id` is 10001 — the ESI journal id, which
	// HANGAR stores. But:
	//
	//   * character.wallet-transactions leads with `"id": 1` AND carries
	//     `"transaction_id": 11001` separately;
	//   * corporation.wallet-journal leads with `"internal_id": 1`;
	//   * corporation.wallet-transactions leads with `"id": 1`.
	//
	// Those leading values are SeAT's own MySQL auto-increment primary key,
	// the same class of SeAT-internal surrogate as `attacker_hash`
	// (APPENDIX_C_MIGRATION.md §7). HANGAR's app.wallet_transaction is keyed
	// on (owner_kind, owner_id, transaction_id, date) and has no such
	// column, and inventing a counter would produce a value that differs
	// between two installations holding identical data.
	//
	// These three were WRITTEN, run against the corpus, and reclassified on
	// the evidence — the handler is deleted rather than left serving bytes
	// that do not match.
	reasonSurrogateID = "the recorded response leads with SeAT's own MySQL auto-increment key " +
		"(`id` / `internal_id`), a SeAT-internal surrogate HANGAR has no column for — " +
		"app.wallet_transaction is keyed on (owner_kind, owner_id, transaction_id, date). " +
		"Inventing a counter would differ between two installations holding identical data. " +
		"Same class as `attacker_hash` in APPENDIX_C_MIGRATION.md §7."

	// reasonCharacterSheetFields is why the one obvious single-resource
	// route is NOT the one implemented.
	reasonCharacterSheetFields = "NOT shimmable today, for two independent reasons. (1) `user_id` is a " +
		"legacy MySQL integer; HANGAR's app.character.user_id is a uuid, so the field has no honest " +
		"value — the same identifier-space break that makes UserController unshimmable, appearing " +
		"inside an otherwise reproducible route. (2) `skillpoints.total_sp` and " +
		"`skillpoints.unallocated_sp` are PARSED FROM ESI AND DISCARDED: " +
		"handlers.CharacterSkillsDTO reads both fields on every skills sync and " +
		"SyncCharacterSkills writes only the per-skill rows, so no column holds either. " +
		"total_sp could be summed from app.character_skill; unallocated_sp cannot be derived at all."
)

// Classification is every legacy /api/v2 read route and what the shim does
// with it. This is the authority Register() reads.
func Classification() []LegacyRoute {
	served := func(controller, pattern, corpus, permission string, handle func(*Request) (any, error)) LegacyRoute {
		return LegacyRoute{
			Controller: controller, Method: http.MethodGet, Pattern: pattern, Corpus: corpus,
			Status: StatusServed, Permission: permission, Appends: true, Handle: handle,
		}
	}
	pending := func(controller, pattern, corpus, migration, reason string) LegacyRoute {
		return LegacyRoute{
			Controller: controller, Method: http.MethodGet, Pattern: pattern, Corpus: corpus,
			Status: StatusPending, Reason: reason, Migration: migration,
		}
	}

	return []LegacyRoute{
		// ── AllianceController (1 route) ─────────────────────────────────
		served("AllianceController", Prefix+"/alliance/contacts/{id}", "alliance.contacts",
			"alliances.view", listContacts("alliance")),

		// ── CharacterController (15 routes) ──────────────────────────────
		served("CharacterController", Prefix+"/character/contacts/{id}", "character.contacts",
			"characters.view", listContacts("character")),
		served("CharacterController", Prefix+"/character/corporation-history/{id}", "character.corporation-history",
			"characters.view", characterCorporationHistory),
		pending("CharacterController", Prefix+"/character/wallet-journal/{id}", "character.wallet-journal",
			migrationCharacters+"/wallet/journal", reasonMySQLDoubleRounding),
		pending("CharacterController", Prefix+"/character/wallet-transactions/{id}", "character.wallet-transactions",
			migrationCharacters+"/wallet/transactions", reasonSurrogateID),
		pending("CharacterController", Prefix+"/character/sheet/{id}", "character.sheet",
			migrationCharacters, reasonCharacterSheetFields),
		pending("CharacterController", Prefix+"/character/assets/{id}", "character.assets",
			migrationCharacters+"/assets", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/contracts/{id}", "character.contracts",
			migrationCharacters+"/contracts", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/industry/{id}", "character.industry",
			migrationCharacters+"/industry", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/jump-clones/{id}", "character.jump-clones",
			migrationCharacters+"/clones", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/mail/{id}", "character.mail",
			migrationCharacters+"/mail", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/market-orders/{id}", "character.market-orders",
			migrationCharacters+"/orders", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/notifications/{id}", "character.notifications",
			migrationCharacters+"/notifications", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/skills/{id}", "character.skills",
			migrationCharacters+"/skills", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/skill-queue/{id}", "character.skill-queue",
			migrationCharacters+"/skillqueue", reasonNoKeysetWindow),
		pending("CharacterController", Prefix+"/character/killmails/{id}", "killmails.character",
			migrationKillmails, reasonKillmailHash),

		// ── CorporationController (11 routes) ────────────────────────────
		served("CorporationController", Prefix+"/corporation/contacts/{id}", "corporation.contacts",
			"corporations.view", listContacts("corporation")),
		served("CorporationController", Prefix+"/corporation/sheet/{id}", "corporation.sheet",
			"corporations.view", corporationSheet),
		pending("CorporationController", Prefix+"/corporation/wallet-journal/{id}", "corporation.wallet-journal",
			migrationCorporations+"/wallets/{division}/journal", reasonSurrogateID),
		pending("CorporationController", Prefix+"/corporation/wallet-transactions/{id}", "corporation.wallet-transactions",
			migrationCorporations+"/wallets/{division}/transactions", reasonSurrogateID),

		pending("CorporationController", Prefix+"/corporation/assets/{id}", "corporation.assets",
			migrationCorporations+"/assets", reasonNoKeysetWindow),
		pending("CorporationController", Prefix+"/corporation/contracts/{id}", "corporation.contracts",
			migrationCorporations+"/contracts", reasonNoKeysetWindow),
		pending("CorporationController", Prefix+"/corporation/industry/{id}", "corporation.industry",
			migrationCorporations+"/industry", reasonNoKeysetWindow),
		pending("CorporationController", Prefix+"/corporation/market-orders/{id}", "corporation.market-orders",
			migrationCorporations+"/orders", reasonNoKeysetWindow),
		pending("CorporationController", Prefix+"/corporation/member-tracking/{id}", "corporation.member-tracking",
			migrationCorporations+"/membertracking", reasonNoKeysetWindow),
		pending("CorporationController", Prefix+"/corporation/structures/{id}", "corporation.structures",
			migrationCorporations+"/structures", reasonNoKeysetWindow),
		pending("CorporationController", Prefix+"/corporation/killmails/{id}", "killmails.corporation",
			migrationKillmails, reasonKillmailHash),

		// ── KillmailsController (1 route) ────────────────────────────────
		pending("KillmailsController", Prefix+"/killmails/{id}", "killmails.detail",
			migrationKillmails, reasonKillmailHash),

		// ── UserController (2 routes) — identifier space ─────────────────
		{
			Controller: "UserController", Method: http.MethodGet, Pattern: Prefix + "/users",
			Corpus: "users.index", Status: StatusUnshimmable, Reason: reasonIdentitySpace,
			Migration: "/api/v1/admin/users",
		},
		{
			Controller: "UserController", Method: http.MethodGet, Pattern: Prefix + "/users/{id}",
			Corpus: "users.show", Status: StatusUnshimmable, Reason: reasonIdentitySpace,
			Migration: "/api/v1/admin/users/{id}",
		},

		// ── SquadController (2 routes) — identifier space, plus a raster ──
		{
			Controller: "SquadController", Method: http.MethodGet, Pattern: Prefix + "/squads",
			Corpus: "squads.index", Status: StatusUnshimmable,
			Reason: reasonIdentitySpace + " SquadResource.logo compounds it: it always returned a " +
				"rendered PNG data-URL, generating a placeholder avatar when none was stored, and no " +
				"translation layer reproduces a raster image.",
			Migration: "/api/v1/squads",
		},
		{
			Controller: "SquadController", Method: http.MethodGet, Pattern: Prefix + "/squads/{id}",
			Corpus: "squads.show", Status: StatusUnshimmable, Reason: reasonIdentitySpace,
			Migration: "/api/v1/squads/{id}",
		},

		// ── RoleController / RoleLookupController — grant model ──────────
		// Registered as PREFIXES by Register(), so every path beneath them
		// answers 410 including ones this shim never heard of: a client
		// calling /api/v2/roles/query/anything must be told the grant model
		// changed, not handed a 404 that reads as "wrong URL, try again".
		{
			Controller: "RoleController", Method: http.MethodGet, Pattern: Prefix + "/roles",
			Status: StatusBreaking, Reason: reasonGrantModel,
			Migration: "/api/v1/admin/roles, /api/v1/admin/scopes",
		},
		{
			Controller: "RoleLookupController", Method: http.MethodGet, Pattern: Prefix + "/roles/query/permissions",
			Status: StatusBreaking, Reason: reasonGrantModel,
			Migration: "/api/v1/admin/users/{id}, /api/v1/me",
		},
	}
}

// ByStatus groups Classification by status — the shape the Gate 7 evidence
// and the coverage test both count.
func ByStatus() map[RouteStatus][]LegacyRoute {
	out := map[RouteStatus][]LegacyRoute{}
	for _, route := range Classification() {
		out[route.Status] = append(out[route.Status], route)
	}
	return out
}

// ── THE THREE CONTROLLER-LEVEL FUNCTIONS ARE GONE (PHASE 20.6) ──────────
// BreakingControllers, UnshimmableControllers and PendingControllers used to
// live here, returning controller -> one representative path. They were the
// symptom this file fixes rather than a thing to preserve: a controller-level
// verdict cannot describe CharacterController, which now spans served,
// pending-for-three-different-reasons routes at once, and the router
// consulted none of them.
//
// Classification() replaces all three at the granularity the question
// actually has, and ByStatus() gives the grouped view the evidence and the
// coverage test want. Deleting them removed three reachability-allowlist
// entries by making them unnecessary, which is the honest way for that file
// to shrink.
