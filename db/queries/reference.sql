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
--
-- SCOPE (Phase 20.8): this returns the member corporations HANGAR already
-- has rows for. The alliance member-corporation sync built this phase does
-- NOT widen that set, and deliberately: it RECONCILES the alliance_id of
-- corporations HANGAR already knows (see SetCorporationAllianceMembership
-- and ClearCorporationAllianceMembershipNotIn below) and inserts nothing.
-- Stubbing a row per member id would be a large alliance's several hundred
-- id-with-an-empty-name rows that nothing ever resolves — precisely the
-- defect that made app.alliance useless for the whole life of the project,
-- rebuilt one table across.
SELECT * FROM app.corporation WHERE alliance_id = $1 ORDER BY name;

-- ── CAPABILITY #37's MEMBER-CORPORATION RECONCILIATION (PHASE 20.8) ──────
-- GET /alliances/{alliance_id}/corporations returns every member corporation
-- id in the alliance, and app.corporation.alliance_id is the ONLY column
-- HANGAR has to put that fact in. These two statements are therefore the
-- whole writer for that route, and both are bounded to corporations that
-- already have a row: the route is authoritative about MEMBERSHIP, not a
-- licence to create entities.
--
-- The pair is genuinely useful rather than a formality: the corporation
-- sheet sync learns a corporation's alliance from ITS OWN route, so an
-- untracked-but-known corporation that joined or left this alliance is
-- corrected here at the alliance's cadence instead of waiting for a
-- corporation-scoped subscription that may not exist.

-- name: SetCorporationAllianceMembership :execrows
UPDATE app.corporation
   SET alliance_id = sqlc.arg(alliance_id)::bigint, updated_at = now()
 WHERE corporation_id = ANY(sqlc.arg(corporation_ids)::bigint[])
   AND alliance_id IS DISTINCT FROM sqlc.arg(alliance_id)::bigint;

-- name: ClearCorporationAllianceMembershipNotIn :execrows
-- The mirror: a corporation HANGAR records as being in this alliance that
-- the alliance's own member list does not contain has left it. Clearing to
-- NULL rather than guessing a new alliance is the only honest write — this
-- route says who is IN the alliance and nothing about where a leaver went.
UPDATE app.corporation
   SET alliance_id = NULL, updated_at = now()
 WHERE alliance_id = sqlc.arg(alliance_id)::bigint
   AND NOT (corporation_id = ANY(sqlc.arg(corporation_ids)::bigint[]));

-- name: ListAllianceMemberCharacters :many
-- The acting-character candidate pool for an ALLIANCE-scoped subscription
-- (internal/sync/election.go).
--
-- An alliance has no token, exactly as a corporation has none, so §6.3's
-- election applies — but the pool is a different shape: every tracked
-- character whose corporation is in the alliance, rather than the members of
-- one corporation. ESI's alliance contact routes require the scope and
-- membership in the alliance; they require no corporation ROLE, which is why
-- the alliance branch of the elector has no role test to satisfy.
SELECT c.character_id
  FROM app.character c
  JOIN app.corporation corp ON corp.corporation_id = c.corporation_id
 WHERE corp.alliance_id = sqlc.arg(alliance_id)::bigint
   AND c.deleted_at IS NULL
 ORDER BY c.character_id;

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

-- ── CAPABILITY #41's TWO ENUMERATIONS (PHASE 20.8) ───────────────────────
-- GET /universe/stations/{station_id} and /universe/structures/{structure_id}
-- are the two routes that fill app.location, and neither is enumerable:
-- there is no "list the stations" route and there could not be a useful one
-- (there are thousands, and CCP publishes them in the SDE). The id set has
-- to come from HANGAR's OWN rows — the location ids its syncs have already
-- landed — which is why UpsertLocation had no production caller for the
-- whole life of the project: the query below is the thing that was missing,
-- not the handler.
--
-- ── WHY ONLY THESE THREE TABLES ──────────────────────────────────────────
-- Many tables carry a location id: contracts (start/end), market orders,
-- industry jobs, member tracking. NONE of them says whether the id is a
-- station or a structure, and the two routes are different routes with
-- different scopes — so using those ids would mean GUESSING the kind from
-- the numeric range (60000000-64000000 for NPC stations, >= 1e12 for
-- structures). Principle 13 forbids exactly that kind of coercion, and a
-- wrong guess sends an authenticated structure request for a station id and
-- burns Governor 2's error budget on a guaranteed 404.
--
-- app.asset, app.character_clone and app.character_location are the three
-- places where ESI ITSELF discriminates: the first two carry CCP's own
-- `location_type` verbatim, and the third splits station_id and structure_id
-- into separate columns. app.corporation_structure joins the structure list
-- because that table's whole contents are, by the route that fills it,
-- structures.

-- name: ListUnresolvedStationIDs :many
-- Stations HANGAR has seen and has NOT yet resolved.
--
-- ── WHY "UNRESOLVED" AND NOT "ALL" ───────────────────────────────────────
-- An NPC station is static reference data — it is in the SDE, its name and
-- system never change, and CCP retired conquerable outposts years ago. So a
-- station resolved once never needs fetching again, and this query returning
-- the empty set in steady state is the correct cost for the fan-out: zero
-- requests. That is the same reasoning worker/killmail_fanout.go applies to
-- an immutable killmail, and it is deliberately NOT what the structure query
-- below does — a structure is player-owned and mutable.
SELECT DISTINCT seen.location_id::bigint AS station_id
  FROM (
        SELECT a.location_id
          FROM app.asset a
         WHERE a.location_type = 'station' AND a.deleted_at IS NULL
        UNION
        SELECT c.location_id
          FROM app.character_clone c
         WHERE c.location_type = 'station'
        UNION
        SELECT l.station_id
          FROM app.character_location l
         WHERE l.station_id IS NOT NULL
       ) AS seen (location_id)
 WHERE NOT EXISTS (
         SELECT 1 FROM app.location loc
          WHERE loc.location_type = 'station'
            AND loc.location_id = seen.location_id)
 ORDER BY station_id
 LIMIT sqlc.arg(max_ids);

-- name: ListCharacterStructureIDs :many
-- Every Upwell structure id THIS character's own rows reference, plus the
-- structures owned by the corporations it is a member of.
--
-- ── WHY THE FAN-OUT IS PER-CHARACTER AND THE STATION ONE IS GLOBAL ───────
-- /universe/structures/{structure_id} needs esi-universe.read_structures.v1
-- AND docking access, and docking access is granted PER CHARACTER by the
-- structure's ACL. Enumerating per character over the structures that
-- character's own assets, clones and location already sit in means every
-- call is one the character has demonstrably proven access to, so a 403 is
-- rare and genuinely informative when it happens. Enumerating globally would
-- ask one token about structures it has no relationship with and produce a
-- 403 per item, which Governor 2 counts.
--
-- ── WHY ALL OF THEM, EVERY PASS ──────────────────────────────────────────
-- Unlike a station, a structure is mutable: it is renamed, transferred,
-- unanchored. UpsertLocation's own IS DISTINCT FROM guard makes an unchanged
-- structure a zero-row write, and the route carries a one-hour cache age, so
-- re-reading the set costs one request per structure per hour and keeps the
-- name HANGAR displays true. Same judgement as the calendar-detail fan-out.
SELECT DISTINCT seen.location_id::bigint AS structure_id
  FROM (
        SELECT a.location_id
          FROM app.asset a
         WHERE a.owner_kind = 'character' AND a.owner_id = sqlc.arg(character_id)
           AND a.location_type = 'structure' AND a.deleted_at IS NULL
        UNION
        SELECT c.location_id
          FROM app.character_clone c
         WHERE c.character_id = sqlc.arg(character_id) AND c.location_type = 'structure'
        UNION
        SELECT l.structure_id
          FROM app.character_location l
         WHERE l.character_id = sqlc.arg(character_id) AND l.structure_id IS NOT NULL
        UNION
        SELECT cs.structure_id
          FROM app.corporation_structure cs
          JOIN app.corporation_member cm ON cm.corporation_id = cs.corporation_id
         WHERE cm.character_id = sqlc.arg(character_id)
       ) AS seen (location_id)
 ORDER BY structure_id
 LIMIT sqlc.arg(max_ids);

-- name: StartSdeImport :one
INSERT INTO app.sde_import (status) VALUES ('running') RETURNING *;

-- name: CompleteSdeImport :exec
UPDATE app.sde_import
   SET completed_at = now(), status = $2, checksum = $3, row_counts = $4, error = $5
 WHERE import_id = $1;

-- name: GetLatestSdeImport :one
SELECT * FROM app.sde_import ORDER BY started_at DESC LIMIT 1;

-- name: RecordSdeImportBuild :exec
-- PHASE 20.5 (B22). Stamps CCP's own build number onto a completed import,
-- under a reserved key inside the existing row_counts jsonb rather than in a
-- column of its own: the only consumer is `hangar admin import-sde
-- --if-changed`, which asks "is the live SDE already CCP's latest build",
-- and a migration for one comparison the operator drives would be a column
-- nothing else ever reads. The underscore prefix keeps it out of the table
-- namespace row_counts otherwise holds.
UPDATE app.sde_import
   SET row_counts = row_counts || jsonb_build_object('_ccp_build', sqlc.arg(build)::bigint)
 WHERE import_id = sqlc.arg(import_id);

-- name: SetCorporationMemberLimit :exec
-- PHASE 15.1 — `/corporations/{corporation_id}/members/limit` is its own
-- ESI route returning a bare integer, not a field of the corporation
-- sheet, so it needs a targeted write: UpsertCorporation would require
-- inventing values for every other column just to set this one.
UPDATE app.corporation
   SET member_limit = $2, updated_at = now()
 WHERE corporation_id = $1
   AND member_limit IS DISTINCT FROM $2;
