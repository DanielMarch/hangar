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

	// ── DEFECT B55 (PHASE 20.9): reasonNoKeysetWindow WAS FALSE FOR NINE ──
	// From Phase 20.6 to 20.8 thirteen routes shared one blocker, whose text
	// read: "each of these needs a store query that can return the full
	// ordered set for a window ... not yet written". It was a claim about the
	// store, and nobody had checked it against the store.
	//
	// Nine of the thirteen already had exactly such a query, and had since
	// Phase 1b:
	//
	//	ListMarketOrdersByOwner        SELECT * … ORDER BY issued DESC
	//	ListIndustryJobsByOwner        SELECT * … ORDER BY start_date DESC
	//	ListCharacterSkills            SELECT * … ORDER BY skill_id
	//	ListCharacterSkillqueue        SELECT * … ORDER BY queue_position
	//	ListCharacterClones            SELECT * … ORDER BY jump_clone_id
	//	ListCorporationMemberTracking  SELECT * … ORDER BY character_id
	//	ListCorporationStructures      SELECT * … ORDER BY structure_id
	//
	// No LIMIT, no keyset, no OFFSET — the whole ordered relation — and every
	// one of them ALREADY CALLED IN PRODUCTION by the /api/v1 route serving
	// the same data. The blocker was not merely stale; it had never been true
	// for those routes, and it was load-bearing: it is the sentence a client
	// reads in the 501 body and the sentence the Gate 7 evidence counts.
	//
	// Found the way 20.8 found B52 — by asking whether a column's stated
	// values held up against something outside the artefact that stated them.
	// Eight of the nine are now SERVED and byte-verified against the corpus;
	// the ninth (corporation.structures) turned out to have a DIFFERENT and
	// real blocker, which is reasonStructureServices below.
	//
	// ── DEFECT B57 (PHASE 20.10): AND IT WAS STILL WRONG FOR FOUR MORE ────
	// 20.9 narrowed reasonNoKeysetWindow to six routes and said they were
	// "verified per route". They were — against the STORE. The question asked
	// was "is this relation really only a keyset page?", and for all six the
	// answer was yes. The question NOT asked was the one that decides whether
	// the reason is the real blocker: "and if the full-set query existed,
	// would the route then be servable?"
	//
	// For four of the six the answer is no, permanently:
	//
	//   character.assets, corporation.assets — `map_id` and `map_name` are
	//   REAL COLUMNS on legacy's asset tables (2018_01_05_195350:
	//   `bigInteger('map_id')->nullable()`, `string('map_name')->nullable()`),
	//   a persisted denormalisation of SeAT's own location resolution.
	//   app.asset has neither, and the recording proves legacy populates
	//   them: character.assets row TWO carries map_id 30000142 and map_name
	//   "Jita". Already documented as unreproducible in
	//   APPENDIX_C_MIGRATION.md §7 — while this file told clients to wait.
	//
	//   character.contracts, corporation.contracts — `contract_details.price`
	//   is a MySQL `double` (2018_01_07_134346), the same column type as
	//   character_wallet_journals.amount, and the recording carries the SAME
	//   divergent value: the fixture seeds 9007199254740993.01, which parses
	//   to the float64 9007199254740994, and the recording says
	//   9007199254741000 — three ulps away. That is reasonMySQLDoubleRounding
	//   exactly, settled in 20.7 and equally unfixable here.
	//
	// The cost of getting this wrong is not cosmetic. StatusPending's 501
	// body says "not yet"; four routes were telling integrators to wait for a
	// release that could never ship, and two of them contradicted a document
	// in this same repository that already said so.
	//
	// The lesson generalises past this file: a reason that is TRUE is not the
	// same as a reason that is THE BLOCKER, and only asking "what happens
	// after this is fixed" tells them apart.
	//
	// reasonNoKeysetWindow now names the TWO routes it was ever the whole
	// story for — and both are served as of this phase, so nothing carries it.
	// It is kept, unreferenced, because deleting it would delete the record.
	reasonNoKeysetWindow = "shimmable: keyed by EVE identifiers HANGAR stores unchanged. " +
		"The store exposes this relation ONLY as a keyset page (LIMIT + a cursor predicate) because " +
		"OFFSET is prohibited (SRS §6, enforced by sqlc's no-offset rule), and legacy paginated the " +
		"whole relation in PHP. Needs a full-set store query and corpus fixtures; not yet written."

	// reasonAssetMapColumns is what BOTH asset routes are actually blocked on.
	reasonAssetMapColumns = "NOT shimmable, and not for want of a store query (B57 — it carried " +
		"reasonNoKeysetWindow until Phase 20.10). Legacy's asset tables have `map_id` (bigint) and " +
		"`map_name` (string) as REAL PERSISTED COLUMNS holding SeAT's own location-resolution " +
		"denormalisation; app.asset has neither, and HANGAR resolves locations through " +
		"app.asset_location with a different shape and no stored name. The recording is decisive " +
		"rather than silent about it: character.assets row two carries map_id 30000142 and " +
		"map_name \"Jita\", so a constant null would be provably wrong. Recorded as unreproducible " +
		"in APPENDIX_C_MIGRATION.md §7 since Phase 19 — this route's reason simply did not say so."

	// reasonContractDoublePrice is what BOTH contract routes are actually
	// blocked on: the same measured conflict as character.wallet-journal.
	reasonContractDoublePrice = "NOT shimmable, and not for want of a store query (B57 — it carried " +
		"reasonNoKeysetWindow until Phase 20.10). `contract_details.price` is a MySQL DOUBLE, the " +
		"same column type as character_wallet_journals.amount, and the corpus carries the same " +
		"divergence settled in 20.7: the fixture seeds 9007199254740993.01, which parses to the " +
		"float64 9007199254740994, and the recording says 9007199254741000 — three ulps away. " +
		"HANGAR's app.contract.price is NUMERIC(30,2) and holds the value it was given, exactly. " +
		"No formatting rule makes an exact decimal emit a different double, so this is the same " +
		"unfixable corpus fact, not a second instance of a fixable bug. Everything else about the " +
		"route IS reproducible — bids, lines, the three entity objects and the nested location " +
		"objects all have HANGAR sources or recorded constants — which is precisely why the price " +
		"had to be checked rather than assumed."

	// ── reasonStructureServices IS RESOLVED (PHASE 20.10) AND UNREFERENCED ─
	// The re-recording it asked for was made. `services` elements are
	// {name, state} — the create migration's `corporation_id` column was
	// dropped by a later migration, so the model not hiding it is moot — and
	// that is exactly what app.corporation_structure.services holds. The
	// route is SERVED. Kept, unreferenced, because the reasoning below is why
	// the route was NOT served on evidence the corpus did not contain, and
	// that judgement was correct even though its prediction was not.
	//
	// It also bought something worth more than the route: the re-recording
	// inserts two structures in DESCENDING id order and two services in
	// non-alphabetical order, and legacy returns both in ASCENDING key order.
	// That MEASURES the primary-key ordering rule every other route in this
	// package had only inferred from the corporation-history recording.
	//
	// reasonStructureServices, as it stood:
	//
	// The store query exists (ListCorporationStructures, full ordered set) and
	// fourteen of the row's seventeen fields are reproducible: HANGAR holds
	// every column, and the two the recording shows as `reinforce_weekday` /
	// `next_reinforce_weekday` are constants because the LIVE ESI SPEC HAS
	// NEITHER PROPERTY — measured against the ingested catalogue at
	// compatibility date 2026-08-04 — so no current installation of either
	// system can hold a value for them.
	//
	// `services` is the one that stops it. In legacy it is a HasMany onto
	// `corporation_structure_services`, so each element is a full Eloquent
	// row with whatever column set and `$hidden` that table has; in HANGAR it
	// is `app.corporation_structure.services`, a jsonb array of ESI's
	// `{name, state}` objects. Those are different shapes, and the recording
	// CANNOT SAY HOW DIFFERENT: fixtures.php seeds no services at all, so the
	// recorded value is `[]` and the element shape is unpinned.
	//
	// Serving the route on that evidence would mean claiming byte-identity
	// from a recording that never exercised the field — and every structure
	// that matters has services online, so the untested branch is the common
	// case, not the corner. That is precisely the mistake StatusServed exists
	// to prevent, and the same standard that reclassified three wallet routes
	// in 20.6 after they were written and run.
	//
	// Closing it needs a NEW RECORDING with a populated
	// corporation_structure_services table, not more code.
	reasonStructureServices = "shimmable in shape and no longer blocked on the store (B55: " +
		"ListCorporationStructures returns the full ordered set, and the two `reinforce_weekday` " +
		"fields are constants because the live ESI spec has no such properties). Blocked on " +
		"`services`: legacy's is a HasMany onto corporation_structure_services and HANGAR's is a " +
		"jsonb array of ESI `{name, state}` objects, and fixtures.php seeds NO services — so the " +
		"recording holds `[]` and does not pin the element shape at all. Byte-identity cannot be " +
		"claimed from a field the corpus never exercised, and a structure with services online is " +
		"the common case. Needs a re-recording with services present, not more code."

	reasonIdentitySpace = "HANGAR's user and squad ids are uuids where legacy's were MySQL " +
		"auto-increment integers. `\"id\":1` and `\"id\":\"019ff31f-…\"` are different " +
		"identifier spaces; no translation invents the integer a legacy client stored."

	reasonGrantModel = "the RBAC grant model changed shape entirely; there is no role object " +
		"to translate onto."

	// reasonKillmailHash is KillmailsController's specific blocker, separate
	// from reasonNoKeysetWindow because building the store query would not
	// resolve it.
	// PHASE 20.7 (B48): one of this reason's two blockers is now GONE and the
	// other is unchanged, which is why the route is still not servable.
	//
	// The killmail SYNC was built this phase — a two-stage fan-out over the
	// recent-list routes into app.killmail/killmail_attacker/killmail_item
	// (internal/sync/worker/killmail_fanout.go) — so "the table is empty on
	// every installation" is no longer true.
	//
	// What remains is the surrogate, and it is not a matter of effort:
	// legacy's `attacker_hash` is a value SeAT COMPUTED FOR ITSELF to
	// deduplicate attacker rows. It is not in any ESI response, so HANGAR
	// cannot derive it from upstream data, and app.killmail_attacker
	// deliberately has no column for it (APPENDIX_C_MIGRATION.md §7). A
	// byte-identical response is therefore impossible for these three routes
	// no matter how complete the sync becomes — which is exactly the
	// distinction Gate 7 exists to record: SYNC CLOSED, SHIM NOT.
	reasonKillmailHash = "shimmable in shape, blocked on legacy's `attacker_hash`: a SeAT-internal " +
		"surrogate app.killmail_attacker has no column for and no ESI response carries " +
		"(APPENDIX_C_MIGRATION.md §7), so it cannot be derived. The sync half of this blocker was " +
		"CLOSED in Phase 20.7 (B48) — app.killmail now has a writer — but the surrogate is not " +
		"recoverable from upstream data at any level of effort."

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
	// ── SETTLED IN PHASE 20.7, AND THE 14-DIGIT READING WAS WRONG ────────
	// The measurement 20.6 asked for was taken: the recorder's own pinned
	// interpreter (php:8.2-cli, resolving to 8.2.33) run with
	// serialize_precision instrumented. It reports
	//
	//	serialize_precision = -1        (the default since PHP 7.1)
	//	precision           = 14
	//	json_encode(9007199254740993.01) = 9007199254740994
	//
	// so json_encode uses SHORTEST ROUND-TRIP, exactly as formatPHPDouble
	// already assumed. The 14-significant-digit hypothesis is refuted twice
	// over: forcing serialize_precision=14 produces "9.007199254741e+15",
	// EXPONENT form, which is not the corpus's "9007199254741000" either.
	//
	// What the corpus records is therefore not a rounded rendering of
	// ...993.01 at all — 9007199254741000.0 is an exactly representable
	// double three ulps away, and PHP prints it as those digits because that
	// IS its shortest round-trip form. The divergence is in what legacy's
	// MySQL DOUBLE column came to hold, upstream of any encoder.
	//
	// formatPHPDouble is consequently UNCHANGED and now pinned against the
	// transcript, including the exponent-form boundary
	// (encode_php_precision_test.go). The shim can reproduce money above
	// 2^53; what it cannot do is reproduce a value legacy stored as a
	// different double from the one it was given, which is a corpus fact and
	// not an encoder defect.
	//
	// Seeding HANGAR with 9007199254741000 to force a match was considered and
	// REJECTED: legacy was GIVEN ...993.01 and stored ...741000; HANGAR is
	// given ...993.01 and stores it exactly, because NUMERIC(30,2) is exact.
	// The two systems genuinely hold different numbers, and changing the
	// fixture would be making the test agree with itself.
	reasonMySQLDoubleRounding = "blocked on a MEASURED corpus conflict, not on missing code. The " +
		"recording's `amount` (9007199254741000) is a different float64 from the value the fixture " +
		"seeds (9007199254740993.01 parses to 9007199254740994, 3 ulps away). MEASURED in 20.7 against " +
		"PHP 8.2.33: serialize_precision is -1 (shortest round-trip), NOT 14-significant-digit rounding, " +
		"so the encoder is correct and unchanged — the two systems genuinely hold different doubles, " +
		"and no formatting rule can make HANGAR's exact NUMERIC(30,2) emit legacy's lossy DOUBLE."

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
	//
	// ── PHASE 20.9: ONE OF THE TWO BLOCKERS IS CLOSED, AND THE ROUTE IS ──
	// ── STILL NOT SERVABLE. BOTH HALVES OF THAT SENTENCE MATTER. ─────────
	// The second blocker was a REAL GAP in HANGAR independent of the shim,
	// and it is fixed on its own merits as defect B56: migration 00046 adds
	// app.character_skill_summary, SyncCharacterSkills writes total_sp and
	// unallocated_sp instead of discarding them, and
	// GET /api/v1/characters/{id}/skills/summary reads them back. A column
	// nothing writes and a column nothing reads are the same defect one step
	// apart, so the writer and the reader landed together.
	//
	// That does NOT make character.sheet servable. `user_id` is unchanged and
	// unchangeable: legacy's is a MySQL auto-increment integer and HANGAR's
	// app.character.user_id is a uuid, so the field has no honest value at
	// any level of effort. Two blockers minus one is one blocker, not zero,
	// and the note that "total_sp could be summed from app.character_skill"
	// was itself wrong — ESI's total INCLUDES unallocated points, so the sum
	// differs from the total by exactly the number that was missing.
	reasonCharacterSheetFields = "NOT shimmable, blocked on `user_id`: legacy's is a MySQL " +
		"auto-increment integer and HANGAR's app.character.user_id is a uuid, so the field has no " +
		"honest value — the same identifier-space break that makes UserController unshimmable, " +
		"appearing inside an otherwise reproducible route. This route's SECOND blocker is CLOSED as " +
		"of Phase 20.9 (B56): `skillpoints.total_sp` and `skillpoints.unallocated_sp` were parsed " +
		"from ESI and discarded on every sync, and app.character_skill_summary now holds both. " +
		"Closing it removed one blocker of two and changed nothing about this route's status."
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
			migrationCharacters+"/assets", reasonAssetMapColumns),
		pending("CharacterController", Prefix+"/character/contracts/{id}", "character.contracts",
			migrationCharacters+"/contracts", reasonContractDoublePrice),
		served("CharacterController", Prefix+"/character/industry/{id}", "character.industry",
			"characters.view", characterIndustry),
		served("CharacterController", Prefix+"/character/jump-clones/{id}", "character.jump-clones",
			"characters.view", characterJumpClones),
		served("CharacterController", Prefix+"/character/mail/{id}", "character.mail",
			"characters.view", characterMail),
		served("CharacterController", Prefix+"/character/market-orders/{id}", "character.market-orders",
			"characters.view", characterMarketOrders),
		served("CharacterController", Prefix+"/character/notifications/{id}", "character.notifications",
			"characters.view", characterNotifications),
		served("CharacterController", Prefix+"/character/skills/{id}", "character.skills",
			"characters.view", characterSkills),
		served("CharacterController", Prefix+"/character/skill-queue/{id}", "character.skill-queue",
			"characters.view", characterSkillQueue),
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
			migrationCorporations+"/assets", reasonAssetMapColumns),
		pending("CorporationController", Prefix+"/corporation/contracts/{id}", "corporation.contracts",
			migrationCorporations+"/contracts", reasonContractDoublePrice),
		served("CorporationController", Prefix+"/corporation/industry/{id}", "corporation.industry",
			"corporations.view", corporationIndustry),
		served("CorporationController", Prefix+"/corporation/market-orders/{id}", "corporation.market-orders",
			"corporations.view", corporationMarketOrders),
		served("CorporationController", Prefix+"/corporation/member-tracking/{id}", "corporation.member-tracking",
			"corporations.view", corporationMemberTracking),
		served("CorporationController", Prefix+"/corporation/structures/{id}", "corporation.structures",
			"corporations.view", corporationStructures),
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
