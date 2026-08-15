package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// CharacterSkillsDTO is GET /characters/{character_id}/skills
// (CharactersSkills). Every numeric field here — including skill_id — is
// declared int64 by the spec, not the int32 Phase 1b originally migrated
// (see 00030_phase7_character_fixups.sql).
//
// ── UnallocatedSP IS A POINTER (PHASE 20.9, B56) ─────────────────────────
// It was `int64` with `omitempty`, which cannot tell "ESI omitted the field"
// from "ESI said zero" — the spec marks total_sp REQUIRED and
// unallocated_sp OPTIONAL, so both cases occur. That did not matter while
// the value was discarded; it matters now that it reaches a nullable column.
type CharacterSkillsDTO struct {
	Skills        []CharacterSkillEntry `json:"skills"`
	TotalSP       int64                 `json:"total_sp"`
	UnallocatedSP *int64                `json:"unallocated_sp,omitempty"`
}

type CharacterSkillEntry struct {
	ActiveSkillLevel   int64 `json:"active_skill_level"`
	SkillID            int64 `json:"skill_id"`
	SkillpointsInSkill int64 `json:"skillpoints_in_skill"`
	TrainedSkillLevel  int64 `json:"trained_skill_level"`
}

func ParseCharacterSkills(body []byte) (CharacterSkillsDTO, error) {
	var dto CharacterSkillsDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return CharacterSkillsDTO{}, fmt.Errorf("handlers: parsing character skills: %w", err)
	}
	return dto, nil
}

// SyncCharacterSkills upserts every skill ESI reports and prunes any row
// for this character not in that set (a skill can vanish entirely via
// skill extraction — full-state list, no soft delete).
//
// ── DEFECT B56 (PHASE 20.9): THE AGGREGATE HALF WAS DISCARDED ────────────
// The response carries three things — the per-skill array, total_sp and
// unallocated_sp — and until this phase only the array was written. The two
// numbers were parsed into the DTO, asserted by test/capability's
// TestSyncCharacterSkills against the recorded response, and then dropped on
// the floor by this function on every sync since Phase 1b.
//
// Neither survives the drop: total_sp INCLUDES unallocated points so it is
// not SUM(skillpoints) over the rows written below, and unallocated_sp is by
// definition in no skill at all. app.character_skill_summary exists to hold
// them; see migration 00046 for why it is a separate table.
func SyncCharacterSkills(ctx context.Context, s *store.Store, characterID int64, dto CharacterSkillsDTO) (SyncResult, error) {
	ids := make([]int64, len(dto.Skills))
	for i, sk := range dto.Skills {
		ids[i] = sk.SkillID
		if _, err := s.UpsertCharacterSkill(ctx, gen.UpsertCharacterSkillParams{
			CharacterID:  characterID,
			SkillID:      sk.SkillID,
			ActiveLevel:  int16(sk.ActiveSkillLevel),
			TrainedLevel: int16(sk.TrainedSkillLevel),
			Skillpoints:  sk.SkillpointsInSkill,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting skill %d for character %d: %w", sk.SkillID, characterID, err)
		}
	}
	if err := s.DeleteCharacterSkillsNotIn(ctx, characterID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale skills for character %d: %w", characterID, err)
	}

	if _, err := s.UpsertCharacterSkillSummary(ctx, characterID, dto.TotalSP, dto.UnallocatedSP); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting skill summary for character %d: %w", characterID, err)
	}

	// RowsAffected stays the SKILL count. The summary is one row per
	// character that exists whether the character has skills or not, so
	// counting it would make an empty skill list report 1 and break the
	// "zero rows is a question" reading that has found three defects.
	return SyncResult{RowsAffected: int32(len(dto.Skills))}, nil
}

// CharacterSkillQueueEntryDTO is one element of GET
// /characters/{character_id}/skillqueue (array response —
// CharactersSkillqueueSkill). finish_date/start_date are absent for a
// paused queue entry (roadmap edge case).
type CharacterSkillQueueEntryDTO struct {
	FinishDate      time.Time `json:"finish_date,omitempty"`
	FinishedLevel   int64     `json:"finished_level"`
	LevelEndSP      *int64    `json:"level_end_sp,omitempty"`
	LevelStartSP    *int64    `json:"level_start_sp,omitempty"`
	QueuePosition   int64     `json:"queue_position"`
	SkillID         int64     `json:"skill_id"` // $ref: TypeID, int64
	StartDate       time.Time `json:"start_date,omitempty"`
	TrainingStartSP *int64    `json:"training_start_sp,omitempty"`
}

func ParseCharacterSkillQueue(body []byte) ([]CharacterSkillQueueEntryDTO, error) {
	var dto []CharacterSkillQueueEntryDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing character skillqueue: %w", err)
	}
	return dto, nil
}

// SyncCharacterSkillQueue upserts each entry at its ESI-reported
// queue_position (per-position IS DISTINCT FROM guard — Phase 1b's own
// design, db/queries/character_sheet.sql's ReplaceCharacterSkillqueue) and
// prunes positions beyond the freshly-synced length — the queue only ever
// shrinks from the end, so DeleteCharacterSkillqueueBeyond(len(entries)) is
// exact, no ID set to compute.
func SyncCharacterSkillQueue(ctx context.Context, s *store.Store, characterID int64, entries []CharacterSkillQueueEntryDTO) (SyncResult, error) {
	for _, e := range entries {
		if _, err := s.ReplaceCharacterSkillqueue(ctx, gen.ReplaceCharacterSkillqueueParams{
			CharacterID:     characterID,
			QueuePosition:   int32(e.QueuePosition),
			SkillID:         e.SkillID,
			FinishedLevel:   int16(e.FinishedLevel),
			TrainingStartSp: e.TrainingStartSP,
			LevelStartSp:    e.LevelStartSP,
			LevelEndSp:      e.LevelEndSP,
			StartDate:       nilIfZero(e.StartDate),
			FinishDate:      nilIfZero(e.FinishDate),
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting skillqueue entry %d for character %d: %w", e.QueuePosition, characterID, err)
		}
	}
	if err := s.DeleteCharacterSkillqueueBeyond(ctx, characterID, int32(len(entries))); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning skillqueue beyond position %d for character %d: %w", len(entries), characterID, err)
	}
	return SyncResult{RowsAffected: int32(len(entries))}, nil
}
