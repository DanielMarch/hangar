-- app.corporation_member, member_tracking, title, member_title, role,
-- role_history, division, shareholder, facility, customs_office,
-- container_log, structure, starbase, starbase_detail, skyhook,
-- sovereignty_hub (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertCorporationMember :one
-- Membership itself never "changes" once present — only appears or
-- disappears — so DO NOTHING is the correct upsert shape (same reasoning as
-- character_implant above).
INSERT INTO app.corporation_member (corporation_id, character_id) VALUES ($1, $2)
ON CONFLICT (corporation_id, character_id) DO NOTHING
RETURNING *;

-- name: ListCorporationMembers :many
SELECT * FROM app.corporation_member WHERE corporation_id = $1 ORDER BY character_id;

-- name: DeleteCorporationMembersNotIn :exec
DELETE FROM app.corporation_member
 WHERE corporation_id = $1 AND NOT (character_id = ANY(sqlc.arg(keep_character_ids)::bigint[]));

-- name: UpsertCorporationMemberTracking :one
INSERT INTO app.corporation_member_tracking AS t (
    corporation_id, character_id, base_id, location_id, logoff_date, logon_date, ship_type_id, start_date
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (corporation_id, character_id) DO UPDATE
   SET base_id = EXCLUDED.base_id, location_id = EXCLUDED.location_id,
       logoff_date = EXCLUDED.logoff_date, logon_date = EXCLUDED.logon_date,
       ship_type_id = EXCLUDED.ship_type_id, start_date = EXCLUDED.start_date, updated_at = now()
 WHERE (t.base_id, t.location_id, t.logoff_date, t.logon_date, t.ship_type_id, t.start_date)
    IS DISTINCT FROM
       (EXCLUDED.base_id, EXCLUDED.location_id, EXCLUDED.logoff_date, EXCLUDED.logon_date,
        EXCLUDED.ship_type_id, EXCLUDED.start_date)
RETURNING *;

-- name: ListCorporationMemberTracking :many
SELECT * FROM app.corporation_member_tracking WHERE corporation_id = $1 ORDER BY character_id;

-- name: UpsertCorporationTitle :one
INSERT INTO app.corporation_title AS t (corporation_id, title_id, name) VALUES ($1,$2,$3)
ON CONFLICT (corporation_id, title_id) DO UPDATE
   SET name = EXCLUDED.name, updated_at = now()
 WHERE t.name IS DISTINCT FROM EXCLUDED.name
RETURNING *;

-- name: ListCorporationTitles :many
SELECT * FROM app.corporation_title WHERE corporation_id = $1 ORDER BY title_id;

-- name: ReplaceCorporationMemberTitle :one
INSERT INTO app.corporation_member_title (corporation_id, title_id, character_id) VALUES ($1,$2,$3)
ON CONFLICT (corporation_id, title_id, character_id) DO NOTHING
RETURNING *;

-- name: ListCorporationMemberTitles :many
SELECT * FROM app.corporation_member_title WHERE corporation_id = $1 AND character_id = $2;

-- name: ReplaceCorporationRole :one
INSERT INTO app.corporation_role (corporation_id, character_id, role, grantable, at_hq, at_base, at_other)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (corporation_id, character_id, role, grantable, at_hq, at_base, at_other) DO NOTHING
RETURNING *;

-- name: ListCorporationRoles :many
SELECT * FROM app.corporation_role WHERE corporation_id = $1 AND character_id = $2;

-- name: InsertCorporationRoleHistory :one
INSERT INTO app.corporation_role_history (
    corporation_id, record_id, character_id, changed_at, issuer_id, role_type, old_roles, new_roles
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (corporation_id, record_id) DO NOTHING
RETURNING *;

-- name: ListCorporationRoleHistory :many
SELECT * FROM app.corporation_role_history
 WHERE corporation_id = $1 ORDER BY changed_at DESC LIMIT sqlc.arg(page_size);

-- name: UpsertCorporationDivision :one
INSERT INTO app.corporation_division (corporation_id, division_kind, division, name)
VALUES ($1,$2,$3,$4)
ON CONFLICT (corporation_id, division_kind, division) DO UPDATE SET name = EXCLUDED.name
 WHERE app.corporation_division.name IS DISTINCT FROM EXCLUDED.name
RETURNING *;

-- name: ListCorporationDivisions :many
SELECT * FROM app.corporation_division WHERE corporation_id = $1 ORDER BY division_kind, division;

-- name: UpsertCorporationShareholder :one
INSERT INTO app.corporation_shareholder (corporation_id, shareholder_id, shareholder_type, share_count)
VALUES ($1,$2,$3,$4)
ON CONFLICT (corporation_id, shareholder_id, shareholder_type) DO UPDATE
   SET share_count = EXCLUDED.share_count
 WHERE app.corporation_shareholder.share_count IS DISTINCT FROM EXCLUDED.share_count
RETURNING *;

-- name: ListCorporationShareholders :many
SELECT * FROM app.corporation_shareholder WHERE corporation_id = $1 ORDER BY shareholder_id;

-- name: UpsertCorporationFacility :one
INSERT INTO app.corporation_facility (corporation_id, facility_id, system_id, type_id)
VALUES ($1,$2,$3,$4)
ON CONFLICT (corporation_id, facility_id) DO NOTHING
RETURNING *;

-- name: ListCorporationFacilities :many
SELECT * FROM app.corporation_facility WHERE corporation_id = $1 ORDER BY facility_id;

-- name: UpsertCorporationCustomsOffice :one
INSERT INTO app.corporation_customs_office AS t (
    corporation_id, office_id, system_id, reinforce_exit_start, reinforce_exit_end,
    allow_access_with_standings, allow_alliance_access, standing_level, alliance_tax_rate,
    corporation_tax_rate, excellent_standing_tax_rate, good_standing_tax_rate,
    neutral_standing_tax_rate, terrible_standing_tax_rate, bad_standing_tax_rate
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (corporation_id, office_id) DO UPDATE
   SET reinforce_exit_start = EXCLUDED.reinforce_exit_start, reinforce_exit_end = EXCLUDED.reinforce_exit_end,
       standing_level = EXCLUDED.standing_level, updated_at = now()
 WHERE (t.reinforce_exit_start, t.reinforce_exit_end, t.standing_level)
    IS DISTINCT FROM (EXCLUDED.reinforce_exit_start, EXCLUDED.reinforce_exit_end, EXCLUDED.standing_level)
RETURNING *;

-- name: ListCorporationCustomsOffices :many
SELECT * FROM app.corporation_customs_office WHERE corporation_id = $1 ORDER BY office_id;

-- name: InsertCorporationContainerLog :one
INSERT INTO app.corporation_container_log (
    corporation_id, logged_at, action, character_id, container_id, container_type_id,
    location_id, location_flag, new_config_bitmask, old_config_bitmask, password_type,
    quantity, type_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
RETURNING *;

-- name: ListCorporationContainerLog :many
SELECT * FROM app.corporation_container_log
 WHERE corporation_id = $1 ORDER BY logged_at DESC LIMIT sqlc.arg(page_size);

-- name: UpsertCorporationStructure :one
INSERT INTO app.corporation_structure AS t (
    corporation_id, structure_id, type_id, system_id, profile_id, fuel_expires, state,
    state_timer_start, state_timer_end, unanchors_at, reinforce_hour, next_reinforce_apply,
    next_reinforce_hour, services
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (corporation_id, structure_id) DO UPDATE
   SET fuel_expires = EXCLUDED.fuel_expires, state = EXCLUDED.state,
       state_timer_start = EXCLUDED.state_timer_start, state_timer_end = EXCLUDED.state_timer_end,
       services = EXCLUDED.services, updated_at = now()
 WHERE (t.fuel_expires, t.state, t.state_timer_start, t.state_timer_end, t.services)
    IS DISTINCT FROM
       (EXCLUDED.fuel_expires, EXCLUDED.state, EXCLUDED.state_timer_start, EXCLUDED.state_timer_end, EXCLUDED.services)
RETURNING *;

-- name: ListCorporationStructures :many
SELECT * FROM app.corporation_structure WHERE corporation_id = $1 ORDER BY structure_id;

-- name: UpsertCorporationStarbase :one
INSERT INTO app.corporation_starbase AS t (
    corporation_id, starbase_id, type_id, system_id, moon_id, onlined_since,
    reinforced_until, state, unanchor_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (corporation_id, starbase_id) DO UPDATE
   SET reinforced_until = EXCLUDED.reinforced_until, state = EXCLUDED.state,
       unanchor_at = EXCLUDED.unanchor_at, updated_at = now()
 WHERE (t.reinforced_until, t.state, t.unanchor_at)
    IS DISTINCT FROM (EXCLUDED.reinforced_until, EXCLUDED.state, EXCLUDED.unanchor_at)
RETURNING *;

-- name: ListCorporationStarbases :many
SELECT * FROM app.corporation_starbase WHERE corporation_id = $1 ORDER BY starbase_id;

-- name: UpsertStarbaseDetail :one
INSERT INTO app.starbase_detail AS t (
    corporation_id, starbase_id, system_id, state, fuel_bay_view, allow_alliance_members,
    allow_corporation_members, use_alliance_standings, attack_standing_threshold, fuels,
    reinforced_until
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (corporation_id, starbase_id) DO UPDATE
   SET state = EXCLUDED.state, fuels = EXCLUDED.fuels, reinforced_until = EXCLUDED.reinforced_until,
       updated_at = now()
 WHERE (t.state, t.fuels, t.reinforced_until) IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.fuels, EXCLUDED.reinforced_until)
RETURNING *;

-- name: GetStarbaseDetail :one
SELECT * FROM app.starbase_detail WHERE corporation_id = $1 AND starbase_id = $2;

-- name: UpsertCorporationSkyhook :one
INSERT INTO app.corporation_skyhook AS t (corporation_id, skyhook_id, type_id, system_id, planet_id, state, fuel_expires)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (corporation_id, skyhook_id) DO UPDATE
   SET state = EXCLUDED.state, fuel_expires = EXCLUDED.fuel_expires, updated_at = now()
 WHERE (t.state, t.fuel_expires) IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.fuel_expires)
RETURNING *;

-- name: ListCorporationSkyhooks :many
SELECT * FROM app.corporation_skyhook WHERE corporation_id = $1 ORDER BY skyhook_id;

-- name: UpsertCorporationSovereigntyHub :one
INSERT INTO app.corporation_sovereignty_hub AS t (corporation_id, hub_id, type_id, system_id, fuel_expires)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (corporation_id, hub_id) DO UPDATE
   SET fuel_expires = EXCLUDED.fuel_expires, updated_at = now()
 WHERE t.fuel_expires IS DISTINCT FROM EXCLUDED.fuel_expires
RETURNING *;

-- name: ListCorporationSovereigntyHubs :many
SELECT * FROM app.corporation_sovereignty_hub WHERE corporation_id = $1 ORDER BY hub_id;
