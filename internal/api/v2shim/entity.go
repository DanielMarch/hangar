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

// optInt renders a nullable bigint the way legacy did — the number, or JSON
// null. Written out rather than passing the *int64 straight to Set because
// Encode would otherwise have to know that a nil *int64 means null, and
// keeping that knowledge here leaves the encoder dealing only in concrete
// values.
func optInt(v *int64) any {
	if v == nil {
		return nil
	}
	return Int(*v)
}
