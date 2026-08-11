-- app.corporation, app.alliance, app.location, app.sde_import
-- (02_DATABASE_SCHEMA.md §4.7 #47-#49, #51). Not one of the roadmap's named
-- Phase 1a query files, but corporation/alliance/location are shared
-- reference data that Tier-1 FKs (app.character) already depend on, and
-- sde_import bookkeeping has nowhere else to live — the full corporation/
-- alliance sync upsert (all ESI-sourced columns) is Phase 7/8's job;
-- these cover the reference-data lifecycle Phase 1a itself needs.

-- name: UpsertCorporation :one
INSERT INTO app.corporation AS t (
    corporation_id, name, ticker, member_count, ceo_id, alliance_id,
    description, tax_rate, date_founded, creator_id, url, faction_id,
    home_station_id, shares, war_eligible, palette, member_limit
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (corporation_id) DO UPDATE
   SET name              = EXCLUDED.name,
       ticker             = EXCLUDED.ticker,
       member_count       = EXCLUDED.member_count,
       member_limit       = EXCLUDED.member_limit,
       ceo_id             = EXCLUDED.ceo_id,
       alliance_id        = EXCLUDED.alliance_id,
       description        = EXCLUDED.description,
       tax_rate           = EXCLUDED.tax_rate,
       date_founded       = EXCLUDED.date_founded,
       creator_id         = EXCLUDED.creator_id,
       url                = EXCLUDED.url,
       faction_id         = EXCLUDED.faction_id,
       home_station_id    = EXCLUDED.home_station_id,
       shares             = EXCLUDED.shares,
       war_eligible       = EXCLUDED.war_eligible,
       palette            = EXCLUDED.palette,
       updated_at         = now()
-- member_limit joins the change guard (Phase 15.1): without it a
-- corporation that trained Corporation Management would keep the stale
-- limit forever, because no OTHER column changed and the DO UPDATE would
-- be skipped entirely.
 WHERE (t.name, t.ticker, t.member_count, t.member_limit, t.ceo_id, t.alliance_id, t.tax_rate, t.shares, t.war_eligible)
    IS DISTINCT FROM
       (EXCLUDED.name, EXCLUDED.ticker, EXCLUDED.member_count, EXCLUDED.member_limit, EXCLUDED.ceo_id,
        EXCLUDED.alliance_id, EXCLUDED.tax_rate, EXCLUDED.shares, EXCLUDED.war_eligible)
RETURNING *;

-- name: GetCorporation :one
SELECT * FROM app.corporation WHERE corporation_id = $1;

-- name: UpsertAlliance :one
INSERT INTO app.alliance AS t (
    alliance_id, name, ticker, creator_id, creator_corporation_id,
    executor_corporation_id, date_founded, faction_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (alliance_id) DO UPDATE
   SET name                     = EXCLUDED.name,
       ticker                    = EXCLUDED.ticker,
       creator_id                = EXCLUDED.creator_id,
       creator_corporation_id    = EXCLUDED.creator_corporation_id,
       executor_corporation_id   = EXCLUDED.executor_corporation_id,
       date_founded              = EXCLUDED.date_founded,
       faction_id                = EXCLUDED.faction_id,
       updated_at                = now()
 WHERE (t.name, t.ticker, t.executor_corporation_id)
    IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.ticker, EXCLUDED.executor_corporation_id)
RETURNING *;

-- name: GetAlliance :one
SELECT * FROM app.alliance WHERE alliance_id = $1;

-- name: ListAlliances :many
-- Phase 15 addition (internal/api/v1): GET /api/v1/alliances needs a list
-- query — every alliance query before this phase targeted one already-known
-- alliance_id.
SELECT * FROM app.alliance ORDER BY name;

-- name: ListCorporationsByAlliance :many
-- Phase 15 addition: GET /api/v1/alliances/{id}/corporations.
SELECT * FROM app.corporation WHERE alliance_id = $1 ORDER BY name;

-- name: SearchCharactersByName :many
-- Phase 15 addition: POST /api/v1/support/search's backing query — SRS
-- §6.7/§4.7: "CCP prohibits using ESI for entity discovery", so this
-- searches HANGAR's own already-synced app.character/app.corporation/
-- app.alliance rows only, never calls out to ESI. $1 is always a bind
-- parameter (never string-concatenated) — internal/api/filters' adversarial
-- input rejection runs before this query is ever reached, and parameter
-- binding is the second, independent layer of defense against injection.
SELECT * FROM app.character WHERE name ILIKE '%' || sqlc.arg(query)::text || '%' AND deleted_at IS NULL ORDER BY name LIMIT sqlc.arg(page_size);

-- name: SearchCorporationsByName :many
SELECT * FROM app.corporation WHERE name ILIKE '%' || sqlc.arg(query)::text || '%' ORDER BY name LIMIT sqlc.arg(page_size);

-- name: SearchAlliancesByName :many
SELECT * FROM app.alliance WHERE name ILIKE '%' || sqlc.arg(query)::text || '%' ORDER BY name LIMIT sqlc.arg(page_size);

-- name: UpsertLocation :one
INSERT INTO app.location AS t (location_type, location_id, name, system_id, owner_id, type_id, resolved_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (location_type, location_id) DO UPDATE
   SET name        = EXCLUDED.name,
       system_id    = EXCLUDED.system_id,
       owner_id     = EXCLUDED.owner_id,
       type_id      = EXCLUDED.type_id,
       resolved_at  = now(),
       updated_at   = now()
 WHERE (t.name, t.system_id, t.owner_id, t.type_id)
    IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.system_id, EXCLUDED.owner_id, EXCLUDED.type_id)
RETURNING *;

-- name: GetLocation :one
SELECT * FROM app.location WHERE location_type = $1 AND location_id = $2;

-- name: StartSdeImport :one
INSERT INTO app.sde_import (status) VALUES ('running') RETURNING *;

-- name: CompleteSdeImport :exec
UPDATE app.sde_import
   SET completed_at = now(), status = $2, checksum = $3, row_counts = $4, error = $5
 WHERE import_id = $1;

-- name: GetLatestSdeImport :one
SELECT * FROM app.sde_import ORDER BY started_at DESC LIMIT 1;

-- name: SetCorporationMemberLimit :exec
-- PHASE 15.1 — `/corporations/{corporation_id}/members/limit` is its own
-- ESI route returning a bare integer, not a field of the corporation
-- sheet, so it needs a targeted write: UpsertCorporation would require
-- inventing values for every other column just to set this one.
UPDATE app.corporation
   SET member_limit = $2, updated_at = now()
 WHERE corporation_id = $1
   AND member_limit IS DISTINCT FROM $2;
