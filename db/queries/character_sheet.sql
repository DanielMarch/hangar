-- app.character_skill, character_skillqueue, character_attributes,
-- character_clone, character_implant, character_jump_fatigue,
-- character_loyalty_point, character_agent_research, character_title,
-- character_role, character_location (02_DATABASE_SCHEMA.md §5.2).

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
DELETE FROM app.character_implant
 WHERE character_id = $1 AND NOT (type_id = ANY(sqlc.arg(keep_type_ids)::int[]));

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

-- name: UpsertCharacterLoyaltyPoint :one
INSERT INTO app.character_loyalty_point AS t (character_id, corporation_id, loyalty_points)
VALUES ($1,$2,$3)
ON CONFLICT (character_id, corporation_id) DO UPDATE
   SET loyalty_points = EXCLUDED.loyalty_points, updated_at = now()
 WHERE t.loyalty_points IS DISTINCT FROM EXCLUDED.loyalty_points
RETURNING *;

-- name: ListCharacterLoyaltyPoints :many
SELECT * FROM app.character_loyalty_point WHERE character_id = $1 ORDER BY corporation_id;

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

-- name: ReplaceCharacterTitle :one
INSERT INTO app.character_title AS t (character_id, title_id, name) VALUES ($1, $2, $3)
ON CONFLICT (character_id, title_id) DO UPDATE SET name = EXCLUDED.name
 WHERE t.name IS DISTINCT FROM EXCLUDED.name
RETURNING *;

-- name: ListCharacterTitles :many
SELECT * FROM app.character_title WHERE character_id = $1 ORDER BY title_id;

-- name: ReplaceCharacterRole :one
INSERT INTO app.character_role (character_id, role, grantable, at_hq, at_base, at_other)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (character_id, role, grantable, at_hq, at_base, at_other) DO NOTHING
RETURNING *;

-- name: ListCharacterRoles :many
SELECT * FROM app.character_role WHERE character_id = $1 ORDER BY role;

-- name: UpsertCharacterLocation :one
INSERT INTO app.character_location AS t (
    character_id, solar_system_id, station_id, structure_id, is_online, last_login,
    last_logout, logins, ship_item_id, ship_type_id, ship_name
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (character_id) DO UPDATE
   SET solar_system_id = EXCLUDED.solar_system_id, station_id = EXCLUDED.station_id,
       structure_id = EXCLUDED.structure_id, is_online = EXCLUDED.is_online,
       last_login = EXCLUDED.last_login, last_logout = EXCLUDED.last_logout,
       logins = EXCLUDED.logins, ship_item_id = EXCLUDED.ship_item_id,
       ship_type_id = EXCLUDED.ship_type_id, ship_name = EXCLUDED.ship_name, updated_at = now()
 WHERE (t.solar_system_id, t.station_id, t.structure_id, t.is_online, t.last_login, t.last_logout,
        t.logins, t.ship_item_id, t.ship_type_id, t.ship_name)
    IS DISTINCT FROM
       (EXCLUDED.solar_system_id, EXCLUDED.station_id, EXCLUDED.structure_id, EXCLUDED.is_online,
        EXCLUDED.last_login, EXCLUDED.last_logout, EXCLUDED.logins, EXCLUDED.ship_item_id,
        EXCLUDED.ship_type_id, EXCLUDED.ship_name)
RETURNING *;

-- name: GetCharacterLocation :one
SELECT * FROM app.character_location WHERE character_id = $1;
