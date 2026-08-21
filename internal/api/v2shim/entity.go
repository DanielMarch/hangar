package v2shim

// entity.go holds legacy's `EntityResource` — the `{entity_id, …}` object
// SeAT embedded wherever a row referenced a character, corporation or
// alliance.
//
// ── THE NAME IS ALWAYS "Unknown", DELIBERATELY ───────────────────────────
// Legacy resolved these names from its own `universe_names` table, and the
// recorded corpus was taken against an installation where that table is
// empty — so every entity in every recording carries legacy's
// `withDefault()` value, "Unknown". HANGAR usually KNOWS the name:
// app.character.name holds "Pilot One" for the very id the corporation.sheet
// recording calls "Unknown".
//
// The shim emits "Unknown" anyway, and that is the same rule
// testdata/legacy-api-v2/README.md already records for the eager-loaded SDE
// `type` object: "legacy's withDefault() emits a small, deterministic
// {…,"typeName":"Unknown",…} — which the shim reproduces exactly". The
// shim's contract is byte-identity with the recorded legacy response, not
// best-available data; a client that wants the resolved name moves to
// /api/v1, which has always had it. Substituting HANGAR's better answer here
// would break the one property this surface exists to provide.
//
// Found by measurement, not by reading: the first implementation DID resolve
// names, and the byte comparison against corporation.sheet failed with
// "Pilot One" where the recording says "Unknown".
//
// ── THE KEY ORDER IS NOT THE SAME EVERYWHERE ─────────────────────────────
// CorporationSheetResource emits {entity_id, category, name}; the wallet
// resources emit {entity_id, name, category}. Two SeAT resource classes
// built the same object with their keys in different orders, and JSON key
// order is part of the bytes. Only the order a SERVED route needs is
// provided — the second will arrive with the first wallet route that becomes
// servable — but the divergence is recorded here because a single "obvious"
// helper would silently break whichever route it did not match.

// legacyUnknownEntityName is legacy's withDefault() name. Named rather than
// inlined so a grep finds every place the shim deliberately declines to use
// a name it holds.
const legacyUnknownEntityName = "Unknown"

// entityCategoryFirst is the `{entity_id, category, name}` ordering used by
// CorporationSheetResource.
func entityCategoryFirst(id *int64, category string) *Obj {
	obj := NewObj(3)
	if id == nil {
		obj.Set("entity_id", nil)
	} else {
		obj.Set("entity_id", Int(*id))
	}
	return obj.Set("category", category).Set("name", legacyUnknownEntityName)
}

// legacyDefaultEntityCategory is the `category` every entity object on this
// surface carries, and it is a CONSTANT rather than resolved data.
//
// ── PHASE 20.10: SETTLED FROM SOURCE, NOT INFERRED ───────────────────────
// Phase 20.6 read "Unknown" off the recording and reproduced it. The
// `category` beside it looked like it might be real. It is not: every one of
// these relations is
//
//	hasOne(UniverseName::class, 'entity_id', <fk>)->withDefault([
//	    'name' => trans('web::seat.unknown'), 'category' => 'character',
//	])
//
// — read from eveseat/eveapi at the commit testdata/legacy-api-v2/README.md
// pins (MailHeader::sender, MailRecipient::entity, and the contract detail's
// issuer/assignee/acceptor). With `universe_names` empty, BOTH values come
// from that array, so a corporation recipient renders `"category":"character"`
// exactly as a character one does. The corpus agrees and shows why the
// recording alone could not have settled it: the contract acceptor is
// entity_id 0 — an entity that does not exist — and still reads "character".
const legacyDefaultEntityCategory = "character"

// entityNameFirst is the `{entity_id, name, category}` ordering — the OTHER
// key order this surface uses, promised in this file since Phase 20.6 and
// delivered by Phase 20.10's mail route.
//
// The order is not a style choice on SeAT's part and not arbitrary: Laravel's
// HasOneOrMany::getDefaultFor sets the FOREIGN KEY on the default model
// first, then applies the withDefault array in its literal order — so
// entity_id, then name, then category. CorporationSheetResource builds its
// entity objects by hand in a different order, which is why both spellings
// exist and why one shared helper would have been silently wrong for one of
// them.
// Every foreign key feeding one of these is NOT NULL in legacy, so a nil here
// takes the NOT NULL rule below and renders entity_id 0 — which is precisely
// what the contracts recording shows for an unaccepted contract's acceptor.
func entityNameFirst(id *int64) *Obj {
	return NewObj(3).
		Set("entity_id", legacyIntNotNull(id)).
		Set("name", legacyUnknownEntityName).
		Set("category", legacyDefaultEntityCategory)
}

// optInt renders a nullable bigint the way legacy did — the number, or JSON
// null. Written out rather than passing the *int64 straight to Set because
// Encode would otherwise have to know that a nil *int64 means null, and
// keeping that knowledge here leaves the encoder dealing only in concrete
// values.
// optString is optInt for a nullable text column: SQL NULL becomes JSON
// null, and a present value is emitted as-is. PHASE 23, for
// character.wallet-journal's `reason` and `context_id_type`, both of which
// are nullable in legacy and in HANGAR.
func optString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func optInt(v *int64) any {
	if v == nil {
		return nil
	}
	return Int(*v)
}

// zeroInt is optInt for a column that is NULLABLE in HANGAR and NOT NULL
// DEFAULT 0 in legacy — a case legacy's schema cannot represent, so its
// clients have never seen null from that field.
//
// PHASE 23 (N-3). One field uses it today, corporation_orders.issued_by,
// and it exists as a named helper rather than an inline conditional so the
// asymmetry is greppable: a reader asking "which fields does the shim
// flatten to zero, and why" gets a list instead of a search.
func zeroInt(v *int64) any {
	if v == nil {
		return Int(0)
	}
	return Int(*v)
}

// optInt32 is optInt for the columns sqlc types as *int32 — every `integer`
// column, as against `bigint`. Separate rather than generic because the two
// pointer types are what the generated models actually hand over and a
// conversion at every call site would bury the nil check.
func optInt32(v *int32) any {
	if v == nil {
		return nil
	}
	return Int(int64(*v))
}

// ── LEGACY'S NOT NULL COLUMNS, WHERE HANGAR'S ARE NULLABLE ───────────────
// A recurring shape on this surface, and one the corpus is systematically
// bad at pinning: legacy declares a column NOT NULL, HANGAR declares the same
// column nullable because ESI marks the field optional, and the fixture
// happens to supply a value — so the recording exercises only the branch the
// two systems agree on.
//
// FOUND ON LIVE DATA IN PHASE 20.10, not in a test. character.notifications
// passed its byte comparison and then emitted `"is_read":null` for all five
// of CEODude's real notifications, because ESI omits `is_read` and HANGAR
// stores the omission honestly. Legacy's column is
// `boolean('is_read')->default(false)` NOT NULL and its model casts to bool,
// so `null` is a value a legacy client has never once received.
//
// THE RULE, stated once here rather than decided per field: where legacy's
// column cannot represent the state HANGAR holds, the shim emits WHAT
// LEGACY'S SCHEMA WOULD HAVE HELD — its declared default, or MySQL's
// zero-value for the type where the column has no default. It is the same
// judgement translate_market.go makes for `state` (an ENUM with no default
// reads as "") and translate_industry.go makes for the corporation
// `station_id` (a column no sync writes reads as null).
//
// This is NOT the shim inventing data. It is the shim declining to invent a
// THIRD state — "unknown" — on a surface whose whole contract is that it
// looks like the thing it replaces. /api/v1 carries the honest null, and the
// migration guide points at it.

// legacyBoolNotNull renders a nullable HANGAR boolean as legacy's NOT NULL
// one: a missing value is the column's `default(false)`.
func legacyBoolNotNull(v *bool) bool { return v != nil && *v }

// legacyIntNotNull renders a nullable HANGAR bigint as legacy's NOT NULL one:
// a missing value is MySQL's zero for an integer column with no default.
func legacyIntNotNull(v *int64) Int {
	if v == nil {
		return 0
	}
	return Int(*v)
}

// legacyStringNotNull renders a nullable HANGAR text column as legacy's NOT
// NULL one: a missing value is the empty string.
func legacyStringNotNull(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// optInt16 is optInt for the columns sqlc types as *int16 — a `smallint`.
func optInt16(v *int16) any {
	if v == nil {
		return nil
	}
	return Int(int64(*v))
}

// legacyTypeObjectOrNullID is legacyTypeObject for a NULLABLE foreign key.
//
// UNMEASURED, and marked as such: every recording that carries a
// withDefault-closure type object carries a non-null id, so what legacy
// emitted for a null one is inference. Laravel resolves `belongsTo` on a null
// key to the default model and runs the closure over it, so the closure's
// `$type->typeID = $job->product_type_id` assigns null — hence `typeID: null`
// beside the same `"Unknown"`. The alternative reading, that the whole object
// would be null, is inconsistent with the relation having a default at all.
//
// Only app.industry_job.product_type_id reaches this; every other column
// feeding a type object is NOT NULL.
func legacyTypeObjectOrNullID(typeID *int32) *Obj {
	if typeID == nil {
		return NewObj(2).Set("typeID", nil).Set("typeName", legacyUnknownEntityName)
	}
	return legacyTypeObject(int64(*typeID))
}
