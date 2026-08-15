-- app.killmail, app.killmail_attacker, app.killmail_item
-- (02_DATABASE_SCHEMA.md §5.2). killmail is partitioned by killmail_time;
-- the partition key rides along in every ON CONFLICT target.

-- name: UpsertKillmail :one
INSERT INTO app.killmail AS t (
    owner_kind, owner_id, killmail_id, killmail_hash, killmail_time, solar_system_id,
    moon_id, war_id, victim_character_id, victim_corporation_id, victim_alliance_id,
    victim_faction_id, victim_ship_type_id, victim_damage_taken, victim_x, victim_y, victim_z
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (owner_kind, owner_id, killmail_id, killmail_time) DO UPDATE
   SET killmail_hash = EXCLUDED.killmail_hash
 WHERE t.killmail_hash IS DISTINCT FROM EXCLUDED.killmail_hash
RETURNING *;

-- name: GetKillmail :one
SELECT * FROM app.killmail WHERE owner_kind = $1 AND owner_id = $2 AND killmail_id = $3;

-- name: ListKillmailsByOwner :many
SELECT * FROM app.killmail
 WHERE owner_kind = $1 AND owner_id = $2
 ORDER BY killmail_time DESC
 LIMIT sqlc.arg(page_size);

-- name: ListKnownKillmailIDs :many
-- PHASE 20.7 (B48). The killmail ids this owner already has a stored detail
-- for, so the two-stage sync can skip re-fetching them.
--
-- This is what makes the fan-out bounded. A killmail is IMMUTABLE — CCP
-- never revises one — so a killmail already stored never needs fetching
-- again, and in steady state the recent list returns almost entirely ids
-- that are already here and the pass makes ZERO detail calls. Without this
-- the sync would re-fetch every killmail in the recent window on every
-- pass, forever, for data that cannot have changed.
SELECT killmail_id FROM app.killmail
 WHERE owner_kind = $1 AND owner_id = $2;

-- name: UpsertKillmailAttacker :one
INSERT INTO app.killmail_attacker AS t (
    owner_kind, owner_id, killmail_id, killmail_time, record_id, character_id,
    corporation_id, alliance_id, faction_id, damage_done, final_blow, security_status,
    ship_type_id, weapon_type_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (owner_kind, owner_id, killmail_id, killmail_time, record_id) DO NOTHING
RETURNING *;

-- name: ListKillmailAttackers :many
SELECT * FROM app.killmail_attacker
 WHERE owner_kind = $1 AND owner_id = $2 AND killmail_id = $3 ORDER BY record_id;

-- name: UpsertKillmailItem :one
INSERT INTO app.killmail_item AS t (
    owner_kind, owner_id, killmail_id, killmail_time, record_id, parent_record_id,
    type_id, flag, quantity_dropped, quantity_destroyed, singleton
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (owner_kind, owner_id, killmail_id, killmail_time, record_id) DO NOTHING
RETURNING *;

-- name: ListKillmailItems :many
SELECT * FROM app.killmail_item
 WHERE owner_kind = $1 AND owner_id = $2 AND killmail_id = $3 ORDER BY record_id;
