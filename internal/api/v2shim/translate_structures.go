package v2shim

import (
	"encoding/json"
	"sort"
)

// translate_structures.go — legacy's corporation.structures route.
//
// ── THIS ROUTE WAS CLASSIFIED UNSERVABLE IN 20.9 AND THE CLASSIFICATION ──
// ── WAS WRONG. THE PROCESS THAT PRODUCED IT WAS NOT. ─────────────────────
// 20.9 found that `services` is a HasMany in legacy and a jsonb array here,
// that fixtures.php seeded NO services, and that the recording therefore held
// `[]` and never pinned the element shape. It refused to claim byte-identity
// from a field the corpus had not exercised, and said the fix was a
// re-recording rather than more code. That was the right call on the evidence
// available, and the answer it was waiting for is now in.
//
// Phase 20.10 re-recorded with services seeded. The element is:
//
//	{"name":"Clone Bay","state":"offline"}
//
// — two keys, which is exactly what app.corporation_structure.services holds,
// because ESI's service object is {name, state} and HANGAR stores it verbatim.
// The prediction that it might be wider was wrong for an instructive reason:
// `Schema::create('corporation_structure_services')` declares a
// `corporation_id` column and CorporationStructureService does not hide it, so
// reading the create migration says the element has three keys. A LATER
// migration drops the column. The schema had to be read out of the migrated
// MySQL database, not off the migration that created the table — the same
// lesson as B55 and B57 in a third costume: an artefact describing a thing is
// not the thing.
//
// ── AND THE RE-RECORDING SETTLED ROW ORDER, WHICH IS WORTH MORE ──────────
// The two structures are inserted ...004 first, ...003 second, so insertion
// order and primary-key order DISAGREE. The recording returns ...003 first.
// That measures what every other route in this package has only inferred
// since 20.9: legacy's unordered `->paginate()` returns InnoDB's
// clustered-index scan, which is primary-key order. It holds a second time
// INSIDE the row — the two services are inserted "Market Hub" then
// "Clone Bay" and come back "Clone Bay" then "Market Hub", which is the
// (structure_id, name) composite key in order.
//
// So `services` is sorted by name here, and it is sorted rather than trusted
// to arrive that way: HANGAR's jsonb array preserves ESI's order, which is
// not legacy's.
//
// ── THE TWO `reinforce_weekday` FIELDS ARE CONSTANT null ─────────────────
// Unchanged from 20.9 and still measured: the live ESI spec has NEITHER
// `reinforce_weekday` nor `next_reinforce_weekday` (compatibility date
// 2026-08-04), so no current installation of either system can hold a value
// for them. HANGAR has no column; legacy has a column nothing writes.

// corporationStructures — legacy's `corporation_structures` row.
//
// Field order is the physical column order minus the model's `$hidden`
// (corporation_id, type_id, system_id, created_at, updated_at), with the four
// eager-loaded relations appended: info, type, services, solar_system.
func corporationStructures(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	// ListCorporationStructures already orders by structure_id, which the
	// re-recording confirms is legacy's order. No re-sort needed.
	structures, err := req.Deps.Store.ListCorporationStructures(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing corporation structures", err)
	}

	page := Window(structures, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, structure := range page {
		services, err := legacyStructureServices(structure.Services)
		if err != nil {
			return nil, internalError("rendering structure services", err)
		}

		id := structure.StructureID
		rows = append(rows, NewObj(16).
			Set("structure_id", Int(structure.StructureID)).
			Set("profile_id", optInt32(structure.ProfileID)).
			Set("fuel_expires", legacyTimeOrNull(structure.FuelExpires)).
			Set("state_timer_start", legacyTimeOrNull(structure.StateTimerStart)).
			Set("state_timer_end", legacyTimeOrNull(structure.StateTimerEnd)).
			Set("unanchors_at", legacyTimeOrNull(structure.UnanchorsAt)).
			Set("state", structure.State).
			Set("reinforce_weekday", nil).
			Set("reinforce_hour", optInt16(structure.ReinforceHour)).
			Set("next_reinforce_weekday", nil).
			Set("next_reinforce_hour", optInt16(structure.NextReinforceHour)).
			Set("next_reinforce_apply", legacyTimeOrNull(structure.NextReinforceApply)).
			// `info` is a withDefault CLOSURE against an empty
			// universe_structures — the foreign key plus the default name.
			// A fourth distinct shape for an eager-loaded relation on this
			// surface; see translate_membertracking.go for the other three.
			Set("info", NewObj(2).
				Set("structure_id", Int(id)).
				Set("name", legacyUnknownEntityName)).
			Set("type", nil).
			Set("services", services).
			Set("solar_system", nil))
	}
	return req.PageOf(rows, int64(len(structures))), nil
}

// legacyStructureService is one element of app.corporation_structure.services
// as ESI delivers it and HANGAR stores it.
type legacyStructureService struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// legacyStructureServices renders the jsonb array as legacy's HasMany.
//
// Sorted by name, which is legacy's (structure_id, name) clustered order and
// NOT the order ESI sent — the re-recording measured the difference by
// inserting the two services in the opposite order to the one they come back
// in. Sorting an array whose stored order is meaningful would normally be
// wrong; here the stored order is ESI's and the target order is MySQL's, so
// reproducing the target means sorting.
func legacyStructureServices(raw json.RawMessage) (Arr, error) {
	// Never nil: a structure with no services renders `[]`, which is what the
	// column defaults to and what the pre-20.10 recording held.
	out := Arr{}
	if len(raw) == 0 {
		return out, nil
	}
	var services []legacyStructureService
	if err := json.Unmarshal(raw, &services); err != nil {
		return nil, err
	}
	sort.SliceStable(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	for _, service := range services {
		out = append(out, NewObj(2).Set("name", service.Name).Set("state", service.State))
	}
	return out, nil
}
