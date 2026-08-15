-- Project HANGAR — Phase 20.9, defect B56: the two numbers every skills
-- screen leads with, parsed from ESI on every sync and thrown away.
--
-- ── THE DEFECT ───────────────────────────────────────────────────────────
-- handlers.CharacterSkillsDTO has modelled `total_sp` and `unallocated_sp`
-- since Phase 1b, and test/capability's TestSyncCharacterSkills asserts both
-- against the recorded ESI response. SyncCharacterSkills then writes ONLY the
-- per-skill rows: it loops dto.Skills into app.character_skill, prunes, and
-- returns. dto.TotalSP and dto.UnallocatedSP are read into local memory and
-- discarded, every sync, on every installation.
--
-- Neither is derivable after the fact:
--
--   * total_sp is CLOSE to SUM(app.character_skill.skillpoints) but not
--     equal to it — ESI's total includes unallocated skill points, and a
--     character who has extracted skills or bought injectors differs from
--     the sum by exactly the amount that matters;
--   * unallocated_sp has no relationship to any per-skill row at all. It is
--     points that are, by definition, in no skill.
--
-- So a skills screen built on HANGAR could show every trained level and
-- could not show the headline figure, and nothing in the schema recorded
-- that as missing. This is the second half of the /api/v2 shim's
-- reasonCharacterSheetFields, and it is fixed here on its own merits rather
-- than for the shim's benefit — see the note in that constant on what it does
-- and does not unblock.
--
-- ── WHY A SEPARATE TABLE AND NOT TWO COLUMNS ON app.character ────────────
-- app.character is written by the character-SHEET sync
-- (db/queries/character_sheet.sql's SyncCharacterSheet, from
-- GET /characters/{character_id}). These two numbers come from a DIFFERENT
-- route, GET /characters/{character_id}/skills, on a different cache age and
-- a different subscription row. Putting them on app.character would mean two
-- syncs upserting one row, and the sheet upsert would have to be taught to
-- leave two columns alone — the kind of cross-route write that produces a
-- column silently reset to zero the next time the other route runs.
--
-- One table per route's full-state payload is the pattern the rest of the
-- character sheet already follows (app.character_attribute,
-- app.character_jump_fatigue), so this follows it.
--
-- ── unallocated_sp IS NULLABLE AND total_sp IS NOT ───────────────────────
-- The live spec (compatibility date 2026-08-04) marks `total_sp` REQUIRED on
-- CharactersCharacterIdSkillsGet and `unallocated_sp` optional — a character
-- with none may omit the field entirely rather than send 0. NULL therefore
-- means "ESI did not say", 0 means "ESI said none", and they are different
-- facts: the DTO's `omitempty` cannot tell them apart on the way out, but the
-- column can on the way in.

-- +goose Up

CREATE TABLE app.character_skill_summary (
    character_id    bigint PRIMARY KEY REFERENCES app.character(character_id),
    total_sp        bigint NOT NULL,
    unallocated_sp  bigint,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE app.character_skill_summary IS
    'The aggregate half of GET /characters/{character_id}/skills: total_sp and '
    'unallocated_sp, which are not derivable from app.character_skill. Written by '
    'the same sync that fills app.character_skill (Phase 20.9, B56).';

COMMENT ON COLUMN app.character_skill_summary.unallocated_sp IS
    'NULL means ESI omitted the field (it is optional in the spec); 0 means ESI '
    'reported none. Different facts, deliberately distinguishable.';

-- +goose Down

DROP TABLE app.character_skill_summary;
