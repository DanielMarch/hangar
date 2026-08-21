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
	//
	// PHASE 23 (N-1): TWELVE ROUTES HELD THIS AND NOBODY HAD DECIDED.
	//
	// "Not yet" is a promise, and a release cannot ship a compatibility
	// shim making twelve of them. Each of the twelve already carried a
	// reason DERIVED against legacy's source in Phase 22's audit — the
	// derivations were sound and the STATUS contradicted them, because a
	// route whose reason is "legacy puts a MySQL auto-increment on the
	// wire" is not waiting on work anybody intends to do.
	//
	// Eleven moved to StatusUnshimmable. A client reading "not shimmed
	// yet" waits for a release that is never coming, and telling the two
	// apart wrongly wastes somebody's quarter — which is the argument this
	// file already made for keeping the statuses separate, applied to the
	// routes rather than only to the type.
	//
	// The twelfth is character.sheet, and it was settled by a MEASUREMENT
	// rather than a judgement (N-2): fixtures.php now seeds one
	// refresh_tokens row, and the re-recorded corpus emits
	// "user_id": 1 where it used to emit null. That 1 is a MySQL
	// auto-increment against HANGAR's uuid, so it joined the other eleven.
	//
	// NO ROUTE HOLDS THIS STATUS. It is kept because a status that exists
	// only while something is unfinished is exactly what a future route
	// will need on the day it is half-built, and deleting it would make
	// "unshimmable" the only word available for "not done yet" —
	// which is how the twelve got mislabelled in the first place, from the
	// other direction.
	StatusPending RouteStatus = "pending"

	// StatusUnshimmable cannot be made byte-compatible, ever, because
	// something legacy puts ON THE WIRE has no honest source in HANGAR.
	// Answers 501 rather than 410: a future release could in principle
	// expose a compatibility mapping, so "Gone" would overstate it.
	//
	// PHASE 23 (N-1): WHAT THIS STATUS MEANS WAS TOO NARROW.
	//
	// It used to read "because HANGAR's identifier space differs from
	// legacy's", which described four of the routes holding it and none of
	// the eleven moved here this phase. Their reasons are not identifier
	// space:
	//
	//   reasonSurrogateID          a MySQL auto-increment PK on the wire
	//   reasonContractDoublePrice  a MySQL `double` money column
	//   reasonAssetMapColumns      persisted columns HANGAR does not have
	//   reasonKillmailHash         a legacy-computed attacker hash
	//
	// What they share with the identifier ones is the only thing that
	// matters here: the bytes come from legacy's own storage, and HANGAR's
	// storage is CORRECT rather than merely different — a decimal money
	// column cannot reproduce a double's rounding error, a uuid cannot
	// reproduce an auto-increment. Serving these would mean storing
	// legacy's mistakes.
	//
	// So the definition is now the shared property rather than the first
	// example anybody wrote down.
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
	//   9007199254741000 — three ulps away. That was reasonMySQLDoubleRounding
	//   exactly, and Phase 23 DISSOLVED it: see the note below on where the
	//   digits actually go. character.wallet-journal is served as a result;
	//   this route is not, because its own blocker is the surrogate id and
	//   that one is unchanged.
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
	// reasonNoKeysetWindow was ever the whole story for exactly TWO routes —
	// character.mail and character.notifications — and both are served as of
	// Phase 20.10, so the constant is DELETED rather than kept unreferenced.
	// Its text was:
	//
	//	shimmable: keyed by EVE identifiers HANGAR stores unchanged. The store
	//	exposes this relation ONLY as a keyset page (LIMIT + a cursor
	//	predicate) because OFFSET is prohibited (SRS §6), and legacy paginated
	//	the whole relation in PHP. Needs a full-set store query and corpus
	//	fixtures; not yet written.
	//
	// Deleted rather than parked: an unreferenced constant is not a record,
	// it is dead code that reads as live, and the linter is right to say so.
	// The record is this comment. Same reasoning that removed
	// BreakingControllers and its two siblings in 20.6.

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
	// reasonStructureServices is DELETED, not parked unreferenced — see the
	// note on reasonNoKeysetWindow above for why. As it stood:
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

	// ── PHASE 23 (N-1): reasonMySQLDoubleRounding IS GONE, AND WHY ──────────
	//
	// It blocked character.wallet-journal for five phases and it was WRONG,
	// in the specific way a reason can be wrong while every fact in it is
	// true. It is deleted rather than kept as an unused constant, and the
	// derivation is kept here because the correction is worth more than the
	// constant was.
	//
	// WHAT 20.6 MEASURED. The corpus seeds amount = 9007199254740993.01 and
	// records `"amount": 9007199254741000`. Those are different float64s —
	// ParseFloat gives 9007199254740994, three ulps away.
	//
	// WHAT 20.7 MEASURED. The recorder's own PHP 8.2.33 reports
	// serialize_precision = -1 and precision = 14, and
	// json_encode(9007199254740993.01) = 9007199254740994. So json_encode
	// uses shortest round-trip, exactly as formatPHPDouble already assumed,
	// and the 14-digit hypothesis was refuted: forcing
	// serialize_precision=14 gives "9.007199254741e+15", exponent form, which
	// is not the corpus value either.
	//
	// 20.7 concluded — correctly — that "the divergence is in what legacy's
	// MySQL DOUBLE column came to hold, upstream of any encoder", and stopped
	// there, reading that as unreproducible.
	//
	// WHAT 23 MEASURED, WHICH NOBODY HAD ASKED. What is actually IN the
	// column:
	//
	//	SELECT price FROM character_orders WHERE order_id = 8999
	//	= 9.007199254741e15
	//
	// Thirteen significant digits, in the table. MySQL 8.4 renders a double
	// to full shortest-round-trip on read (CAST(9007199254740993.01 AS
	// DOUBLE) = 9.007199254740994e15), so the loss is not the read either. It
	// is the WRITE: PDO binds a PHP float by STRINGIFYING it at the
	// `precision` ini — 14 — so MySQL received the text "9.007199254741E+15"
	// and stored the double nearest to that.
	//
	// Both earlier phases looked at the encoder because the divergence
	// appeared at encoding time. It was introduced three layers earlier, on a
	// path neither phase had reason to think about, and finding it took
	// asking the database what it held rather than asking PHP what it
	// printed.
	//
	// Reproducible, therefore. v2shim.phpPrecision applies the same
	// stringification to the exact decimal before the float64 conversion, and
	// character.wallet-journal is SERVED — the shim's seventeenth route and
	// its first wallet route. The exponent form never reaches the wire:
	// phpPrecision rounds THROUGH it and re-parses, and formatPHPDouble then
	// renders 9007199254741000 in shortest form, which is the corpus value.
	//
	// 20.7's closing paragraph still stands and is why the fixture was not
	// touched: "seeding HANGAR with 9007199254741000 to force a match was
	// considered and REJECTED — the two systems genuinely hold different
	// numbers, and changing the fixture would be making the test agree with
	// itself." The fix reproduces legacy's rounding at the BOUNDARY, where a
	// compatibility shim belongs, and HANGAR still stores the exact value.
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
	// ── PHASE 20.11 AUDIT: THE REASON WAS RIGHT, ITS EVIDENCE WAS NOT ─────
	// Re-derived against eveseat's source at the pinned commit. The blocker
	// holds, but the sentence "the field has no honest value" implied the
	// CORPUS demonstrates it, and the corpus demonstrates the opposite:
	//
	//	character.sheet.json records "user_id": null
	//
	// — not an integer. CharacterSheetResource emits `$this->user->id`, and
	// CharacterInfo::user() is a hasOneThrough over `refresh_tokens`, which
	// fixtures.php never seeds. So the recorded null is an artefact of the
	// fixture having no linked user, and a shim emitting a constant null
	// would be BYTE-IDENTICAL TO THE RECORDING while being wrong for every
	// real installation, where a character with a token has a linked user and
	// an integer id HANGAR cannot produce from a uuid.
	//
	// That is the corporation.structures/`services` trap exactly, and it is
	// why this route is still not served: the honest bar is byte-identity
	// with what legacy EMITS, not with what this particular fixture happened
	// to leave null. Recorded here rather than quietly served.
	//
	// ── PHASE 23 (N-2): SEEDED, RE-RECORDED, AND SETTLED ────────────────
	//
	// One refresh_tokens row for character 90000001 (user_id 1), and the
	// corpus re-recorded from it. The populated case is now pinned:
	//
	//	character.sheet.json records "user_id": 1
	//
	// It fell the way the reason predicted, and the reason is now EVIDENCED
	// rather than merely true. The 1 is legacy's `users.id`, a MySQL
	// auto-increment; HANGAR's app.user.user_id is a uuid. There is no
	// function from one to the other that two installations holding
	// identical data would agree on, which is the same blocker
	// users.index and users.show already carry.
	//
	// The old recording could not have shown this. A shim emitting a
	// constant null was byte-identical to it and wrong on every real
	// installation — the corporation.structures/`services` trap, and the
	// third time a Gate 7 reason had been true-but-mis-evidenced (B55, B57,
	// this). 90000002 deliberately keeps NO token, so the corpus now
	// records both the populated and the unpopulated case and the
	// difference between them is visible rather than inferred.
	//
	// So character.sheet is StatusUnshimmable, not StatusPending. It was
	// never waiting on work; it was waiting on a fixture row that would
	// tell anybody so.
	reasonCharacterSheetFields = "NOT shimmable, blocked on `user_id`: legacy's is a MySQL " +
		"auto-increment integer and HANGAR's app.character.user_id is a uuid, so no translation " +
		"produces the integer a legacy client stored — the same identifier-space break that makes " +
		"UserController unshimmable, appearing inside an otherwise reproducible route. NOTE (20.11 " +
		"audit): the RECORDING shows `\"user_id\": null`, because CharacterInfo::user() is a " +
		"hasOneThrough over `refresh_tokens` and fixtures.php seeds none — so a constant null would " +
		"match the corpus and be wrong on every populated installation. The corpus does not pin this " +
		"field; re-recording with a refresh token would. This route's SECOND blocker is CLOSED as " +
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
	// PHASE 23 (N-1): the pending() constructor is GONE, because no route
	// is pending any more. The STATUS stays — see its doc comment — but a
	// constructor for it would be an invitation, and the twelve routes that
	// held it were held there by nothing more than a convenient helper and
	// nobody deciding.
	// PHASE 23 (N-1). Eleven routes moved from pending() to this, and the
	// only thing that changed is the status: every reason below was already
	// derived against legacy's source, and every one says the bytes come
	// from legacy's own storage rather than from work HANGAR has not got
	// round to.
	unshimmable := func(controller, pattern, corpus, migration, reason string) LegacyRoute {
		return LegacyRoute{
			Controller: controller, Method: http.MethodGet, Pattern: pattern, Corpus: corpus,
			Status: StatusUnshimmable, Reason: reason, Migration: migration,
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
		// PHASE 23 (N-1): SERVED. See translate_wallet.go — the reason this
		// carried, reasonMySQLDoubleRounding, was measured and dissolved
		// rather than re-asserted, and `id` is ESI's journal-entry id
		// ($incrementing = false) rather than the auto-increment surrogate
		// that keeps the other three wallet routes unshimmable.
		served("CharacterController", Prefix+"/character/wallet-journal/{id}", "character.wallet-journal",
			"characters.view", characterWalletJournal),
		unshimmable("CharacterController", Prefix+"/character/wallet-transactions/{id}", "character.wallet-transactions",
			migrationCharacters+"/wallet/transactions", reasonSurrogateID),
		unshimmable("CharacterController", Prefix+"/character/sheet/{id}", "character.sheet",
			migrationCharacters, reasonCharacterSheetFields),
		unshimmable("CharacterController", Prefix+"/character/assets/{id}", "character.assets",
			migrationCharacters+"/assets", reasonAssetMapColumns),
		unshimmable("CharacterController", Prefix+"/character/contracts/{id}", "character.contracts",
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
		unshimmable("CharacterController", Prefix+"/character/killmails/{id}", "killmails.character",
			migrationKillmails, reasonKillmailHash),

		// ── CorporationController (11 routes) ────────────────────────────
		served("CorporationController", Prefix+"/corporation/contacts/{id}", "corporation.contacts",
			"corporations.view", listContacts("corporation")),
		served("CorporationController", Prefix+"/corporation/sheet/{id}", "corporation.sheet",
			"corporations.view", corporationSheet),
		unshimmable("CorporationController", Prefix+"/corporation/wallet-journal/{id}", "corporation.wallet-journal",
			migrationCorporations+"/wallets/{division}/journal", reasonSurrogateID),
		unshimmable("CorporationController", Prefix+"/corporation/wallet-transactions/{id}", "corporation.wallet-transactions",
			migrationCorporations+"/wallets/{division}/transactions", reasonSurrogateID),

		unshimmable("CorporationController", Prefix+"/corporation/assets/{id}", "corporation.assets",
			migrationCorporations+"/assets", reasonAssetMapColumns),
		unshimmable("CorporationController", Prefix+"/corporation/contracts/{id}", "corporation.contracts",
			migrationCorporations+"/contracts", reasonContractDoublePrice),
		served("CorporationController", Prefix+"/corporation/industry/{id}", "corporation.industry",
			"corporations.view", corporationIndustry),
		served("CorporationController", Prefix+"/corporation/market-orders/{id}", "corporation.market-orders",
			"corporations.view", corporationMarketOrders),
		served("CorporationController", Prefix+"/corporation/member-tracking/{id}", "corporation.member-tracking",
			"corporations.view", corporationMemberTracking),
		served("CorporationController", Prefix+"/corporation/structures/{id}", "corporation.structures",
			"corporations.view", corporationStructures),
		unshimmable("CorporationController", Prefix+"/corporation/killmails/{id}", "killmails.corporation",
			migrationKillmails, reasonKillmailHash),

		// ── KillmailsController (1 route) ────────────────────────────────
		unshimmable("KillmailsController", Prefix+"/killmails/{id}", "killmails.detail",
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
