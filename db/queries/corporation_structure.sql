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

-- name: ListAllCorporationMemberTitles :many
-- Phase 15 addition (internal/api/v1): GET /corporations/{id}/members/titles
-- is corp-wide (SRS §6.3), unlike ListCorporationMemberTitles above which is
-- one member's row set — no query existed for the whole-corporation view.
SELECT * FROM app.corporation_member_title WHERE corporation_id = $1;

-- name: ReplaceCorporationRole :one
INSERT INTO app.corporation_role (corporation_id, character_id, role, grantable, at_hq, at_base, at_other)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (corporation_id, character_id, role, grantable, at_hq, at_base, at_other) DO NOTHING
RETURNING *;

-- name: ListCorporationRoles :many
SELECT * FROM app.corporation_role WHERE corporation_id = $1 AND character_id = $2;

-- name: ListAllCorporationRoles :many
-- Phase 15 addition, same rationale as ListAllCorporationMemberTitles
-- above: GET /corporations/{id}/roles is corp-wide.
SELECT * FROM app.corporation_role WHERE corporation_id = $1;

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

-- name: UpsertCorporationSkyhookStub :one
-- PHASE 8.1: GET .../structures/skyhooks (the LIST endpoint) gives only
-- id + planet_id — type_id/system_id are unresolvable pre-SDE (see
-- 00033_phase8_1_skyhook_reagent_fixup.sql) and state/reagents/is_active
-- only appear on the DETAIL call. This upsert seeds the row's identity;
-- UpsertCorporationSkyhookDetail (below) fills in the rest once the
-- worker fans out to the per-skyhook detail call.
INSERT INTO app.corporation_skyhook AS t (corporation_id, skyhook_id, planet_id)
VALUES ($1,$2,$3)
ON CONFLICT (corporation_id, skyhook_id) DO UPDATE SET planet_id = EXCLUDED.planet_id
 WHERE t.planet_id IS DISTINCT FROM EXCLUDED.planet_id
RETURNING *;

-- name: DeleteCorporationSkyhooksNotIn :exec
DELETE FROM app.corporation_skyhook
 WHERE corporation_id = $1 AND NOT (skyhook_id = ANY(sqlc.arg(keep_skyhook_ids)::bigint[]));

-- name: UpsertCorporationSkyhookDetail :one
INSERT INTO app.corporation_skyhook AS t (corporation_id, skyhook_id, state, is_active, reagents)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (corporation_id, skyhook_id) DO UPDATE
   SET state = EXCLUDED.state, is_active = EXCLUDED.is_active, reagents = EXCLUDED.reagents, updated_at = now()
 WHERE (t.state, t.is_active, t.reagents) IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.is_active, EXCLUDED.reagents)
RETURNING *;

-- name: ListCorporationSkyhooks :many
SELECT * FROM app.corporation_skyhook WHERE corporation_id = $1 ORDER BY skyhook_id;

-- name: UpsertCorporationSovereigntyHub :one
-- PHASE 8.1: type_id dropped from this list-endpoint upsert — it is
-- unresolvable pre-SDE (00033_phase8_1_skyhook_reagent_fixup.sql) and the
-- live list response never carries it; system_id has no such problem
-- (solar_system_id is direct on the list entry).
INSERT INTO app.corporation_sovereignty_hub AS t (corporation_id, hub_id, system_id)
VALUES ($1,$2,$3)
ON CONFLICT (corporation_id, hub_id) DO UPDATE SET system_id = EXCLUDED.system_id
 WHERE t.system_id IS DISTINCT FROM EXCLUDED.system_id
RETURNING *;

-- name: DeleteCorporationSovereigntyHubsNotIn :exec
DELETE FROM app.corporation_sovereignty_hub
 WHERE corporation_id = $1 AND NOT (hub_id = ANY(sqlc.arg(keep_hub_ids)::bigint[]));

-- name: UpsertCorporationSovereigntyHubDetail :one
INSERT INTO app.corporation_sovereignty_hub AS t (corporation_id, hub_id, system_id, reagents)
VALUES ($1,$2,$3,$4)
ON CONFLICT (corporation_id, hub_id) DO UPDATE
   SET reagents = EXCLUDED.reagents, updated_at = now()
 WHERE t.reagents IS DISTINCT FROM EXCLUDED.reagents
RETURNING *;

-- name: ListCorporationSovereigntyHubs :many
SELECT * FROM app.corporation_sovereignty_hub WHERE corporation_id = $1 ORDER BY hub_id;

-- Phase 9 backfill (closing 00033_phase8_1_skyhook_reagent_fixup.sql's
-- gap): app.corporation_skyhook.system_id/type_id and
-- app.corporation_sovereignty_hub.type_id were left nullable pre-SDE.
-- Now that `sde.planet`/`sde.type` exist and are populated (Phase 9's
-- atomic swap), these three UPDATEs resolve them wherever possible and
-- are safe to run unconditionally on every sync — a scalar subquery
-- (never a JOIN fan-out) so a name that happens to match more than one
-- sde.type row can't produce a nondeterministic multi-row UPDATE, and
-- `IS NULL` on the target column makes every one of them a no-op once
-- resolved once. Before any SDE import has ever run, `sde.type`/
-- `sde.planet` exist (00036's DDL) but are empty, so the subqueries
-- simply find nothing and these UPDATEs affect zero rows — never an
-- error, never a guess.

-- name: BackfillSkyhookSystemIDFromSDE :exec
-- planet_id -> sde.planet -> solar_system_id, per roadmap.
UPDATE app.corporation_skyhook sk
   SET system_id = (SELECT p.solar_system_id FROM sde.planet p WHERE p.planet_id = sk.planet_id)
 WHERE sk.corporation_id = $1
   AND sk.system_id IS NULL
   AND sk.planet_id IS NOT NULL
   AND EXISTS (SELECT 1 FROM sde.planet p WHERE p.planet_id = sk.planet_id);

-- name: BackfillSkyhookTypeIDFromSDE :exec
-- The skyhook structure type resolved by NAME against sde.type, never a
-- hardcoded numeric constant (Principle 13: no guessing at a
-- plausible-looking id with no verifiable source).
--
-- ── PHASE 23: THE NAME WAS 'Skyhook' AND CCP CALLS IT 'Orbital Skyhook' ──
--
-- Measured against build 3475087, the first SDE any installation has ever
-- imported. `sde.type` holds no row named 'Skyhook' at all; the anchorable
-- structure is 81080 'Orbital Skyhook', alongside 'Skyhook Reagent Silo',
-- 'Skyhook Cynosural Disruption' and 'Orbital Skyhook Wreck'.
--
-- So this backfill resolved nothing, and would have gone on resolving
-- nothing after an import — the failure the name-resolution was chosen to
-- avoid, arriving through the name instead of through the id. Principle 13
-- was followed correctly and the string was still a guess, because nothing
-- had ever compared it to the SDE. That is what running import-sde for the
-- first time bought.
--
-- Both spellings are accepted. 'Orbital Skyhook' is what build 3475087
-- ships and is tried first; 'Skyhook' stays as a fallback rather than being
-- replaced, because a bare name is CCP's to rename and a lookup that
-- survives one is worth four extra characters. ORDER BY makes the choice
-- deterministic rather than leaving it to whichever row the planner reaches
-- first.
UPDATE app.corporation_skyhook sk
   SET type_id = (
       SELECT t.type_id FROM sde.type t
        WHERE t.name IN ('Orbital Skyhook', 'Skyhook')
        ORDER BY (t.name = 'Orbital Skyhook') DESC, t.type_id
        LIMIT 1)
 WHERE sk.corporation_id = $1
   AND sk.type_id IS NULL
   AND EXISTS (SELECT 1 FROM sde.type t WHERE t.name IN ('Orbital Skyhook', 'Skyhook'));

-- name: BackfillSovereigntyHubTypeIDFromSDE :exec
-- Same name-resolved lookup as the skyhook type_id backfill above;
-- system_id needs no equivalent here (00033's header: the list endpoint
-- already gives solar_system_id directly).
UPDATE app.corporation_sovereignty_hub sh
   SET type_id = (SELECT t.type_id FROM sde.type t WHERE t.name = 'Sovereignty Hub' LIMIT 1)
 WHERE sh.corporation_id = $1
   AND sh.type_id IS NULL
   AND EXISTS (SELECT 1 FROM sde.type t WHERE t.name = 'Sovereignty Hub');
