package v2shim

// translate_membertracking.go — legacy's `corporation_member_trackings` row.
//
// Unblocked by the same measurement as the rest of defect B55:
// ListCorporationMemberTracking is `SELECT * ... ORDER BY character_id` with
// no LIMIT, already called by /api/v1's corporation member-tracking route.
//
// ── `ship` IS AN EMPTY ARRAY, NOT AN EMPTY OBJECT ────────────────────────
// The recording emits `"ship":[]` for a row whose `ship_type_id` is 587.
// That is Laravel's `belongsTo(InvType::class)->withDefault()` called with NO
// arguments against the corpus's empty `invTypes`: it returns a fresh model
// carrying no attributes at all, whose `toArray()` is PHP's empty array, and
// `json_encode` renders an empty PHP array as `[]` rather than `{}`.
//
// So this differs from translate_market.go's `type` (a withDefault CLOSURE,
// which injects typeID and the name) and from translate_skills.go's `type`
// (no withDefault at all, hence `null`). Three eager-loads of the same SDE
// table, three different JSON values — `{…}`, `[]` and `null` — and nothing
// but the recording tells them apart. This is the field a shim written from
// the schema would have got wrong three times.
//
// `ship_type_id` itself is hidden by the model and does not appear, which is
// why HANGAR's column is read for nothing here: the id is not on the wire.
func corporationMemberTracking(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	// Ordered by character_id — legacy's (corporation_id, character_id) key.
	members, err := req.Deps.Store.ListCorporationMemberTracking(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing member tracking", err)
	}

	page := Window(members, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, member := range page {
		rows = append(rows, NewObj(7).
			Set("character_id", Int(member.CharacterID)).
			Set("start_date", legacyTimeOrNull(member.StartDate)).
			Set("base_id", optInt(member.BaseID)).
			Set("logon_date", legacyTimeOrNull(member.LogonDate)).
			Set("logoff_date", legacyTimeOrNull(member.LogoffDate)).
			Set("location_id", optInt(member.LocationID)).
			Set("ship", Arr{}))
	}
	return req.PageOf(rows, int64(len(members))), nil
}
