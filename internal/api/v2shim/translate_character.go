package v2shim

import (
	"sort"
	"time"
)

// LegacyTimeFormat is how legacy rendered every timestamp.
//
// It is Laravel's default Eloquent datetime cast (`Y-m-d H:i:s`) — no
// timezone offset, no `T` separator, not RFC 3339. HANGAR stores
// `timestamptz` and /api/v1 emits RFC 3339, so this conversion is
// mandatory and it is lossy in one direction worth naming: the offset is
// dropped, and legacy's values were always UTC because SeAT stores UTC. So
// the shim formats in UTC and a client parsing these keeps getting exactly
// what it got before.
const LegacyTimeFormat = "2006-01-02 15:04:05"

// legacyTime renders a timestamp the way legacy did.
func legacyTime(t time.Time) string { return t.UTC().Format(LegacyTimeFormat) }

// characterCorporationHistory — legacy's CorporationHistoryResource, which
// is `parent::toArray()` with `character_id` removed.
//
// So the field list is the character_corporation_histories TABLE's column
// order, minus the model's `$hidden` (id, created_at, updated_at) and minus
// character_id: start_date, corporation_id, is_deleted, record_id. That
// order is not a design; it is where 472 migrations left the columns, which
// is exactly why it had to be recorded rather than guessed.
func characterCorporationHistory(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	history, err := req.Deps.Store.ListCharacterCorporationHistory(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing corporation history", err)
	}

	// HANGAR's query orders by start_date DESC; legacy's unordered
	// paginate() returned MySQL's primary-key order, which for this table
	// is (character_id, record_id). The recorded corpus shows record_id 1
	// before record_id 2 even though record 1 is the NEWER row, so the
	// order is genuinely by record_id and not by date — sorted explicitly
	// here, because inheriting whichever order the store query happens to
	// use would be a byte difference nobody would think to look for.
	sort.SliceStable(history, func(i, j int) bool { return history[i].RecordID < history[j].RecordID })

	page := Window(history, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, entry := range page {
		rows = append(rows, NewObj(4).
			Set("start_date", legacyTime(entry.StartDate)).
			Set("corporation_id", Int(entry.CorporationID)).
			Set("is_deleted", entry.IsDeleted).
			Set("record_id", Int(entry.RecordID)))
	}
	return req.PageOf(rows, int64(len(history))), nil
}
