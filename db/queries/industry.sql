-- app.industry_job, app.blueprint, app.mining_ledger, app.mining_extraction,
-- app.mining_observer, app.mining_observer_record (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertIndustryJob :one
INSERT INTO app.industry_job AS t (
    owner_kind, owner_id, job_id, installer_id, facility_id, station_id, activity_id,
    blueprint_id, blueprint_type_id, blueprint_location_id, output_location_id, runs,
    cost, licensed_runs, probability, product_type_id, status, duration, start_date,
    end_date, pause_date, completed_date, completed_character_id, successful_runs
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24
)
ON CONFLICT (owner_kind, owner_id, job_id) DO UPDATE
   SET status = EXCLUDED.status, pause_date = EXCLUDED.pause_date,
       completed_date = EXCLUDED.completed_date, completed_character_id = EXCLUDED.completed_character_id,
       successful_runs = EXCLUDED.successful_runs, updated_at = now()
 WHERE (t.status, t.pause_date, t.completed_date, t.completed_character_id, t.successful_runs)
    IS DISTINCT FROM
       (EXCLUDED.status, EXCLUDED.pause_date, EXCLUDED.completed_date,
        EXCLUDED.completed_character_id, EXCLUDED.successful_runs)
RETURNING *;

-- name: ListIndustryJobsByOwner :many
SELECT * FROM app.industry_job WHERE owner_kind = $1 AND owner_id = $2 ORDER BY start_date DESC;

-- name: UpsertBlueprint :one
INSERT INTO app.blueprint AS t (
    owner_kind, owner_id, item_id, type_id, location_id, location_flag, quantity,
    time_efficiency, material_efficiency, runs
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (owner_kind, owner_id, item_id) DO UPDATE
   SET location_id = EXCLUDED.location_id, location_flag = EXCLUDED.location_flag,
       quantity = EXCLUDED.quantity, time_efficiency = EXCLUDED.time_efficiency,
       material_efficiency = EXCLUDED.material_efficiency, runs = EXCLUDED.runs, updated_at = now()
 WHERE (t.location_id, t.location_flag, t.quantity, t.time_efficiency, t.material_efficiency, t.runs)
    IS DISTINCT FROM
       (EXCLUDED.location_id, EXCLUDED.location_flag, EXCLUDED.quantity,
        EXCLUDED.time_efficiency, EXCLUDED.material_efficiency, EXCLUDED.runs)
RETURNING *;

-- name: ListBlueprintsByOwner :many
SELECT * FROM app.blueprint WHERE owner_kind = $1 AND owner_id = $2 ORDER BY item_id;

-- name: UpsertMiningLedgerEntry :one
INSERT INTO app.mining_ledger AS t (owner_kind, owner_id, date, solar_system_id, type_id, quantity)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (owner_kind, owner_id, date, solar_system_id, type_id) DO UPDATE
   SET quantity = EXCLUDED.quantity
 WHERE t.quantity IS DISTINCT FROM EXCLUDED.quantity
RETURNING *;

-- name: ListMiningLedgerByOwner :many
SELECT * FROM app.mining_ledger WHERE owner_kind = $1 AND owner_id = $2 ORDER BY date DESC;

-- name: UpsertMiningExtraction :one
-- PHASE 8 FIX: structure_id added (00032_phase8_mining_extraction_structure_id.sql)
-- — the live spec declares it required and it was missing from the
-- Phase 1b column set entirely; see that migration's header.
INSERT INTO app.mining_extraction AS t (
    corporation_id, moon_id, extraction_start_time, chunk_arrival_time, natural_decay_time, structure_id
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (corporation_id, moon_id, extraction_start_time) DO UPDATE
   SET chunk_arrival_time = EXCLUDED.chunk_arrival_time, natural_decay_time = EXCLUDED.natural_decay_time,
       structure_id = EXCLUDED.structure_id
 WHERE (t.chunk_arrival_time, t.natural_decay_time, t.structure_id)
    IS DISTINCT FROM (EXCLUDED.chunk_arrival_time, EXCLUDED.natural_decay_time, EXCLUDED.structure_id)
RETURNING *;

-- name: ListMiningExtractionsByCorporation :many
SELECT * FROM app.mining_extraction WHERE corporation_id = $1 ORDER BY extraction_start_time DESC;

-- name: UpsertMiningObserver :one
INSERT INTO app.mining_observer AS t (corporation_id, observer_id, observer_type, last_updated)
VALUES ($1,$2,$3,$4)
ON CONFLICT (corporation_id, observer_id) DO UPDATE
   SET last_updated = EXCLUDED.last_updated
 WHERE t.last_updated IS DISTINCT FROM EXCLUDED.last_updated
RETURNING *;

-- name: ListMiningObserversByCorporation :many
SELECT * FROM app.mining_observer WHERE corporation_id = $1 ORDER BY observer_id;

-- name: UpsertMiningObserverRecord :one
INSERT INTO app.mining_observer_record AS t (
    corporation_id, observer_id, character_id, type_id, recorded_corporation_id, quantity, last_updated
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (corporation_id, observer_id, character_id, type_id) DO UPDATE
   SET quantity = EXCLUDED.quantity, last_updated = EXCLUDED.last_updated
 WHERE (t.quantity, t.last_updated) IS DISTINCT FROM (EXCLUDED.quantity, EXCLUDED.last_updated)
RETURNING *;

-- name: ListMiningObserverRecords :many
SELECT * FROM app.mining_observer_record WHERE corporation_id = $1 AND observer_id = $2 ORDER BY character_id;
