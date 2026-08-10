-- app.character_skill, character_skillqueue, character_attributes,
-- character_clone, character_implant, character_jump_fatigue,
-- character_loyalty_point, character_agent_research, character_title,
-- character_role, character_location (02_DATABASE_SCHEMA.md §5.2).
--
-- PHASE 7 NOTE: this file's queries were stubbed in Phase 1b, ahead of the
-- route handlers that would call them. Phase 7 fills the one real gap —
-- there was no query for GET /characters/{character_id} itself, the
-- character-sheet enrichment beyond Phase 5's minimal SSO-callback row
-- (SyncCharacterSheet, below) — and replaces UpsertCharacterLocation with
-- three narrower upserts, because /location, /online and /ship are three
-- separate ESI endpoints that never carry all eleven of that single
-- query's columns at once; the original single-call shape assumed one
-- source for the whole row, which isn't how the live spec is shaped.
-- Every other query here is unchanged from Phase 1b.

-- name: SyncCharacterSheet :one
-- Deliberately excludes user_id and owner_hash — those are Phase 5's SSO
-- identity fields, never touched by the sheet sync. character_title_id and
-- achievement_score are the 2026-08-04 pin's genuinely new columns
-- (db/migrations/00030_phase7_character_fixups.sql); `title` holds ESI's
-- corporation_title (a name, not an id — see internal/sync/handlers/
-- character_identity.go for why the roadmap's "renamed to
-- corporation_title_id" summary doesn't match the live spec).
UPDATE app.character AS t
   SET corporation_id     = $2,
       alliance_id        = $3,
       faction_id         = $4,
       security_status    = $5,
       birthday           = $6,
       gender             = $7,
       race_id            = $8,
       bloodline_id       = $9,
       description        = $10,
       title              = $11,
       character_title_id = $12,
       achievement_score  = $13,
       updated_at         = now()
 WHERE t.character_id = $1
   AND (t.corporation_id, t.alliance_id, t.faction_id, t.security_status, t.birthday,
        t.gender, t.race_id, t.bloodline_id, t.description, t.title, t.character_title_id, t.achievement_score)
      IS DISTINCT FROM
       ($2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: UpsertCharacterSkill :one
INSERT INTO app.character_skill AS t (character_id, skill_id, active_level, trained_level, skillpoints)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (character_id, skill_id) DO UPDATE
   SET active_level  = EXCLUDED.active_level,
       trained_level = EXCLUDED.trained_level,
       skillpoints   = EXCLUDED.skillpoints,
       updated_at    = now()
 WHERE (t.active_level, t.trained_level, t.skillpoints)
    IS DISTINCT FROM (EXCLUDED.active_level, EXCLUDED.trained_level, EXCLUDED.skillpoints)
RETURNING *;

-- name: ListCharacterSkills :many
SELECT * FROM app.character_skill WHERE character_id = $1 ORDER BY skill_id;

-- name: DeleteCharacterSkillsNotIn :exec
-- Phase 7 addition: skills is a full-state list (a skill can vanish via
-- skill extraction) but Phase 1b's stub had no prune query for it.
DELETE FROM app.character_skill
 WHERE character_id = $1 AND NOT (skill_id = ANY(sqlc.arg(keep_skill_ids)::bigint[]));

-- name: ReplaceCharacterSkillqueue :one
INSERT INTO app.character_skillqueue AS t (
    character_id, queue_position, skill_id, finished_level, training_start_sp,
    level_start_sp, level_end_sp, start_date, finish_date
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (character_id, queue_position) DO UPDATE
   SET skill_id = EXCLUDED.skill_id, finished_level = EXCLUDED.finished_level,
       training_start_sp = EXCLUDED.training_start_sp, level_start_sp = EXCLUDED.level_start_sp,
       level_end_sp = EXCLUDED.level_end_sp, start_date = EXCLUDED.start_date,
       finish_date = EXCLUDED.finish_date, updated_at = now()
 WHERE (t.skill_id, t.finished_level, t.training_start_sp, t.level_start_sp, t.level_end_sp,
        t.start_date, t.finish_date)
    IS DISTINCT FROM
       (EXCLUDED.skill_id, EXCLUDED.finished_level, EXCLUDED.training_start_sp,
        EXCLUDED.level_start_sp, EXCLUDED.level_end_sp, EXCLUDED.start_date, EXCLUDED.finish_date)
RETURNING *;

-- name: ListCharacterSkillqueue :many
SELECT * FROM app.character_skillqueue WHERE character_id = $1 ORDER BY queue_position;

-- name: DeleteCharacterSkillqueueBeyond :exec
-- The skill queue shrinks as CCP returns fewer entries; queue positions
-- beyond the freshly-synced length are stale and never re-appear, so a
-- soft delete would leave phantom future entries in the UI forever.
DELETE FROM app.character_skillqueue WHERE character_id = $1 AND queue_position >= $2;

-- name: UpsertCharacterAttributes :one
INSERT INTO app.character_attributes AS t (
    character_id, charisma, intelligence, memory, perception, willpower,
    bonus_remaps, last_remap_date, accrued_remap_cooldown_date
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (character_id) DO UPDATE
   SET charisma = EXCLUDED.charisma, intelligence = EXCLUDED.intelligence,
       memory = EXCLUDED.memory, perception = EXCLUDED.perception, willpower = EXCLUDED.willpower,
       bonus_remaps = EXCLUDED.bonus_remaps, last_remap_date = EXCLUDED.last_remap_date,
       accrued_remap_cooldown_date = EXCLUDED.accrued_remap_cooldown_date, updated_at = now()
 WHERE (t.charisma, t.intelligence, t.memory, t.perception, t.willpower, t.bonus_remaps,
        t.last_remap_date, t.accrued_remap_cooldown_date)
    IS DISTINCT FROM
       (EXCLUDED.charisma, EXCLUDED.intelligence, EXCLUDED.memory, EXCLUDED.perception,
        EXCLUDED.willpower, EXCLUDED.bonus_remaps, EXCLUDED.last_remap_date,
        EXCLUDED.accrued_remap_cooldown_date)
RETURNING *;

-- name: GetCharacterAttributes :one
SELECT * FROM app.character_attributes WHERE character_id = $1;

-- name: UpsertCharacterClone :one
INSERT INTO app.character_clone AS t (
    character_id, jump_clone_id, location_id, location_type, name, implants,
    is_home_clone, last_clone_jump_date, last_station_change_date
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (character_id, jump_clone_id) DO UPDATE
   SET location_id = EXCLUDED.location_id, location_type = EXCLUDED.location_type,
       name = EXCLUDED.name, implants = EXCLUDED.implants, is_home_clone = EXCLUDED.is_home_clone,
       last_clone_jump_date = EXCLUDED.last_clone_jump_date,
       last_station_change_date = EXCLUDED.last_station_change_date, updated_at = now()
 WHERE (t.location_id, t.location_type, t.name, t.implants, t.is_home_clone,
        t.last_clone_jump_date, t.last_station_change_date)
    IS DISTINCT FROM
       (EXCLUDED.location_id, EXCLUDED.location_type, EXCLUDED.name, EXCLUDED.implants,
        EXCLUDED.is_home_clone, EXCLUDED.last_clone_jump_date, EXCLUDED.last_station_change_date)
RETURNING *;

-- name: ListCharacterClones :many
SELECT * FROM app.character_clone WHERE character_id = $1 ORDER BY jump_clone_id;

-- name: DeleteCharacterClonesNotIn :exec
-- Phase 7 addition: clones is a full-state list like implants/skills, but
-- Phase 1b's stub had no prune query for it (ListCharacterClones/
-- UpsertCharacterClone only) — a jump clone can be destroyed, which never
-- shows up again in the response, so without this a destroyed clone would
-- linger forever.
DELETE FROM app.character_clone
 WHERE character_id = $1 AND NOT (jump_clone_id = ANY(sqlc.arg(keep_jump_clone_ids)::bigint[]));

-- name: ReplaceCharacterImplant :one
-- character_implant carries no mutable columns beyond its key, so there is
-- nothing for a DO UPDATE to guard with IS DISTINCT FROM — DO NOTHING is the
-- correct upsert shape here, same reasoning as the killmail child tables.
INSERT INTO app.character_implant (character_id, type_id) VALUES ($1, $2)
ON CONFLICT (character_id, type_id) DO NOTHING
RETURNING *;

-- name: ListCharacterImplants :many
SELECT * FROM app.character_implant WHERE character_id = $1 ORDER BY type_id;

-- name: DeleteCharacterImplantsNotIn :exec
-- PHASE 7 FIX: cast was ::int[] (int32) against a type_id column Phase 1b
-- had migrated as `integer` — the live spec declares type_id int64
-- (db/migrations/00030_phase7_character_fixups.sql widened the column to
-- bigint; this cast has to widen with it or every call fails to bind).
DELETE FROM app.character_implant
 WHERE character_id = $1 AND NOT (type_id = ANY(sqlc.arg(keep_type_ids)::bigint[]));

-- name: UpsertCharacterJumpFatigue :one
INSERT INTO app.character_jump_fatigue AS t (character_id, jump_fatigue_expire_date, last_jump_date, last_update_date)
VALUES ($1,$2,$3,$4)
ON CONFLICT (character_id) DO UPDATE
   SET jump_fatigue_expire_date = EXCLUDED.jump_fatigue_expire_date,
       last_jump_date = EXCLUDED.last_jump_date, last_update_date = EXCLUDED.last_update_date,
       updated_at = now()
 WHERE (t.jump_fatigue_expire_date, t.last_jump_date, t.last_update_date)
    IS DISTINCT FROM (EXCLUDED.jump_fatigue_expire_date, EXCLUDED.last_jump_date, EXCLUDED.last_update_date)
RETURNING *;

-- name: GetCharacterJumpFatigue :one
-- Phase 15 addition (internal/api/v1): GET /characters/{id}/fatigue needs a
-- read query — only the sync-side Upsert existed before this phase.
SELECT * FROM app.character_jump_fatigue WHERE character_id = $1;

-- name: ListCharacterAgentResearch :many
-- Phase 15 addition, same rationale: GET /characters/{id}/agents_research.
SELECT * FROM app.character_agent_research WHERE character_id = $1 ORDER BY agent_id;

-- name: UpsertCharacterLoyaltyPoint :one
INSERT INTO app.character_loyalty_point AS t (character_id, corporation_id, loyalty_points)
VALUES ($1,$2,$3)
ON CONFLICT (character_id, corporation_id) DO UPDATE
   SET loyalty_points = EXCLUDED.loyalty_points, updated_at = now()
 WHERE t.loyalty_points IS DISTINCT FROM EXCLUDED.loyalty_points
RETURNING *;

-- name: ListCharacterLoyaltyPoints :many
SELECT * FROM app.character_loyalty_point WHERE character_id = $1 ORDER BY corporation_id;

-- name: DeleteCharacterLoyaltyPointsNotIn :exec
-- Phase 7 addition: Phase 1b's stub had no prune query for loyalty points.
DELETE FROM app.character_loyalty_point
 WHERE character_id = $1 AND NOT (corporation_id = ANY(sqlc.arg(keep_corporation_ids)::bigint[]));

-- name: UpsertCharacterAgentResearch :one
INSERT INTO app.character_agent_research AS t (
    character_id, agent_id, skill_type_id, started_at, points_per_day, remainder_points
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (character_id, agent_id) DO UPDATE
   SET skill_type_id = EXCLUDED.skill_type_id, started_at = EXCLUDED.started_at,
       points_per_day = EXCLUDED.points_per_day, remainder_points = EXCLUDED.remainder_points,
       updated_at = now()
 WHERE (t.skill_type_id, t.started_at, t.points_per_day, t.remainder_points)
    IS DISTINCT FROM (EXCLUDED.skill_type_id, EXCLUDED.started_at, EXCLUDED.points_per_day, EXCLUDED.remainder_points)
RETURNING *;

-- name: DeleteCharacterAgentResearchNotIn :exec
-- Phase 7 addition: Phase 1b's stub had no prune query for agent research.
DELETE FROM app.character_agent_research
 WHERE character_id = $1 AND NOT (agent_id = ANY(sqlc.arg(keep_agent_ids)::bigint[]));

-- name: ReplaceCharacterTitle :one
INSERT INTO app.character_title AS t (character_id, title_id, name) VALUES ($1, $2, $3)
ON CONFLICT (character_id, title_id) DO UPDATE SET name = EXCLUDED.name
 WHERE t.name IS DISTINCT FROM EXCLUDED.name
RETURNING *;

-- name: ListCharacterTitles :many
SELECT * FROM app.character_title WHERE character_id = $1 ORDER BY title_id;

-- name: DeleteCharacterTitlesNotIn :exec
-- Phase 7 addition: Phase 1b's stub had no prune query for titles.
DELETE FROM app.character_title
 WHERE character_id = $1 AND NOT (title_id = ANY(sqlc.arg(keep_title_ids)::bigint[]));

-- name: ReplaceCharacterRole :one
-- GET /characters/{character_id}/roles has no "grantable" concept at all
-- — every row this query writes has grantable = false (the corp-level
-- roles endpoint, Phase 8, is what ever writes grantable = true, through
-- this same table).
INSERT INTO app.character_role (character_id, role, grantable, at_hq, at_base, at_other)
VALUES ($1,$2,false,$3,$4,$5)
ON CONFLICT (character_id, role, grantable, at_hq, at_base, at_other) DO NOTHING
RETURNING *;

-- name: ListCharacterRoles :many
SELECT * FROM app.character_role WHERE character_id = $1 ORDER BY role;

-- name: DeleteCharacterOwnedRoles :exec
-- Phase 7 addition: clears only the character-endpoint-owned subset
-- (grantable = false) before re-inserting the fresh set each sync; Phase
-- 8's grantable = true rows, from the corp-level endpoint, are untouched.
DELETE FROM app.character_role WHERE character_id = $1 AND NOT grantable;

-- character_location: one row, three ESI endpoints (location/online/ship),
-- each owning a disjoint column subset — see this file's header note for
-- why Phase 1b's single 11-column UpsertCharacterLocation doesn't fit.
-- Each upsert below leaves the OTHER endpoints' columns alone on the
-- UPDATE branch; the INSERT branch's placeholder for the other endpoints'
-- NOT NULL solar_system_id (0) is corrected the first time
-- UpsertCharacterLocationOnly runs, whatever order the three land in.

-- name: UpsertCharacterLocationOnly :one
INSERT INTO app.character_location AS t (character_id, solar_system_id, station_id, structure_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (character_id) DO UPDATE
   SET solar_system_id = EXCLUDED.solar_system_id,
       station_id      = EXCLUDED.station_id,
       structure_id    = EXCLUDED.structure_id,
       updated_at      = now()
 WHERE (t.solar_system_id, t.station_id, t.structure_id)
    IS DISTINCT FROM (EXCLUDED.solar_system_id, EXCLUDED.station_id, EXCLUDED.structure_id)
RETURNING *;

-- name: UpsertCharacterOnlineOnly :one
INSERT INTO app.character_location AS t (character_id, solar_system_id, is_online, last_login, last_logout, logins)
VALUES ($1, 0, $2, $3, $4, $5)
ON CONFLICT (character_id) DO UPDATE
   SET is_online   = EXCLUDED.is_online,
       last_login  = EXCLUDED.last_login,
       last_logout = EXCLUDED.last_logout,
       logins      = EXCLUDED.logins,
       updated_at  = now()
 WHERE (t.is_online, t.last_login, t.last_logout, t.logins)
    IS DISTINCT FROM (EXCLUDED.is_online, EXCLUDED.last_login, EXCLUDED.last_logout, EXCLUDED.logins)
RETURNING *;

-- name: UpsertCharacterShipOnly :one
INSERT INTO app.character_location AS t (character_id, solar_system_id, ship_item_id, ship_type_id, ship_name)
VALUES ($1, 0, $2, $3, $4)
ON CONFLICT (character_id) DO UPDATE
   SET ship_item_id = EXCLUDED.ship_item_id,
       ship_type_id = EXCLUDED.ship_type_id,
       ship_name    = EXCLUDED.ship_name,
       updated_at   = now()
 WHERE (t.ship_item_id, t.ship_type_id, t.ship_name)
    IS DISTINCT FROM (EXCLUDED.ship_item_id, EXCLUDED.ship_type_id, EXCLUDED.ship_name)
RETURNING *;

-- name: GetCharacterLocation :one
SELECT * FROM app.character_location WHERE character_id = $1;
