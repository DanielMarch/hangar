package v2shim

import (
	"time"
)

// translate_skills.go — the three character routes that read a full-state
// list HANGAR already stores whole: skills, the skill queue and jump clones.
//
// All three were blocked on `reasonNoKeysetWindow` and none of them ever
// needed a new query (defect B55): ListCharacterSkills, ListCharacterSkillqueue
// and ListCharacterClones each `SELECT *` with an ORDER BY and no LIMIT, and
// each is already called by /api/v1.
//
// ── TWO TIME FORMATS IN ONE PACKAGE, AND THE CORPUS IS WHY ───────────────
// Every other recorded route renders timestamps as `Y-m-d H:i:s`. The skill
// queue renders them as `2026-09-01T00:00:00.000000Z`. That is not an
// inconsistency in the recording — it is Eloquent: a datetime column that is
// NOT in the model's `$casts` reaches json_encode as the raw MySQL string,
// and one that IS cast becomes a Carbon, whose default `serializeDate` is
// ISO-8601 with six fractional digits. SeAT casts start_date/finish_date on
// CharacterSkillQueue and does not cast them anywhere else in this corpus.
//
// A shim that picked one format would have been byte-identical on eight
// routes and silently wrong on the ninth, which is the whole argument for
// recording responses instead of reading models.

// legacyCarbonTimeFormat is Laravel's default `Model::serializeDate` —
// `Carbon::toJSON()`, ISO-8601 with microseconds and a literal Z. Used only
// where the recording shows it.
const legacyCarbonTimeFormat = "2006-01-02T15:04:05.000000Z"

func legacyCarbonTime(t time.Time) string { return t.UTC().Format(legacyCarbonTimeFormat) }

func legacyCarbonTimeOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return legacyCarbonTime(*t)
}

func legacyTimeOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return legacyTime(*t)
}

// characterSkills — legacy's `character_skills` row.
//
// Note what is NOT in it: `skill_id`. The model hides the foreign key and
// replaces it with the eager-loaded `type` relation, and that relation has
// no `withDefault` — so against the corpus's empty SDE it is `null`, and the
// response says how many skillpoints are in a skill without saying which
// skill. That is legacy's behaviour on an installation with no SDE imported,
// it is what the recording holds, and reproducing it is the contract.
// Contrast translate_market.go's `type`, which DOES have a withDefault and
// therefore carries its typeID. Two relations to the same table, two
// shapes; only the corpus distinguishes them.
func characterSkills(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	// ListCharacterSkills already orders by skill_id, which is legacy's
	// primary-key order for (character_id, skill_id) — the same clustered-
	// index argument characterCorporationHistory measured. No re-sort needed.
	skills, err := req.Deps.Store.ListCharacterSkills(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing skills", err)
	}

	page := Window(skills, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, skill := range page {
		rows = append(rows, NewObj(4).
			Set("skillpoints_in_skill", Int(skill.Skillpoints)).
			Set("trained_skill_level", Int(int64(skill.TrainedLevel))).
			Set("active_skill_level", Int(int64(skill.ActiveLevel))).
			Set("type", nil))
	}
	return req.PageOf(rows, int64(len(skills))), nil
}

// characterSkillQueue — legacy's `character_skill_queues` row.
//
// The field order is the physical column order and it is not the order
// anybody would guess: finish_date precedes start_date, and level_end_sp
// precedes level_start_sp. Both inversions are in the recording.
func characterSkillQueue(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	// Ordered by queue_position, which is both the natural order and
	// legacy's primary-key order for (character_id, queue_position).
	queue, err := req.Deps.Store.ListCharacterSkillqueue(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing skill queue", err)
	}

	page := Window(queue, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, entry := range page {
		rows = append(rows, NewObj(8).
			Set("finish_date", legacyCarbonTimeOrNull(entry.FinishDate)).
			Set("start_date", legacyCarbonTimeOrNull(entry.StartDate)).
			Set("finished_level", Int(int64(entry.FinishedLevel))).
			Set("queue_position", Int(int64(entry.QueuePosition))).
			Set("training_start_sp", optInt(entry.TrainingStartSp)).
			Set("level_end_sp", optInt(entry.LevelEndSp)).
			Set("level_start_sp", optInt(entry.LevelStartSp)).
			Set("type", nil))
	}
	return req.PageOf(rows, int64(len(queue))), nil
}

// characterJumpClones — legacy's `character_jump_clones` row.
//
// `implants` is the one relation-shaped field on this route that is NOT an
// SDE join and therefore NOT a documented gap: legacy stored ESI's array
// verbatim in a JSON column and HANGAR stores it as `bigint[]`. The live
// spec types `jump_clones[].implants` as an array of integers (measured
// against the ingested catalogue), so both systems hold the same list of
// numbers and the recording's `[]` is an empty one, not an unrenderable one.
func characterJumpClones(req *Request) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	// Ordered by jump_clone_id — legacy's (character_id, jump_clone_id) key.
	clones, err := req.Deps.Store.ListCharacterClones(ctx, req.IDs[0])
	if err != nil {
		return nil, internalError("listing jump clones", err)
	}

	page := Window(clones, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, clone := range page {
		// Never nil: an empty implant list is `[]`, and a nil Arr would
		// encode as `null` — the distinction Arr exists to preserve.
		implants := make(Arr, 0, len(clone.Implants))
		for _, implant := range clone.Implants {
			implants = append(implants, Int(implant))
		}

		rows = append(rows, NewObj(5).
			Set("jump_clone_id", Int(clone.JumpCloneID)).
			Set("name", clone.Name).
			Set("location_id", Int(clone.LocationID)).
			Set("location_type", clone.LocationType).
			Set("implants", implants))
	}
	return req.PageOf(rows, int64(len(clones))), nil
}
