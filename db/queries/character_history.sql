-- app.character_corporation_history, app.corporation_alliance_history,
-- app.character_intel_edge (02_DATABASE_SCHEMA.md §5.2 "History", "Intel").

-- name: InsertCharacterCorporationHistory :one
INSERT INTO app.character_corporation_history (character_id, record_id, corporation_id, is_deleted, start_date)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (character_id, record_id) DO NOTHING
RETURNING *;

-- name: ListCharacterCorporationHistory :many
SELECT * FROM app.character_corporation_history WHERE character_id = $1 ORDER BY start_date DESC;

-- name: InsertCorporationAllianceHistory :one
INSERT INTO app.corporation_alliance_history (corporation_id, record_id, alliance_id, is_deleted, start_date)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (corporation_id, record_id) DO NOTHING
RETURNING *;

-- name: ListCorporationAllianceHistory :many
SELECT * FROM app.corporation_alliance_history WHERE corporation_id = $1 ORDER BY start_date DESC;

-- name: UpsertCharacterIntelEdge :one
INSERT INTO app.character_intel_edge AS t (source_character_id, target_character_id, edge_kind, weight, last_observed_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (source_character_id, target_character_id, edge_kind) DO UPDATE
   SET weight = EXCLUDED.weight, last_observed_at = EXCLUDED.last_observed_at, updated_at = now()
 WHERE (t.weight, t.last_observed_at) IS DISTINCT FROM (EXCLUDED.weight, EXCLUDED.last_observed_at)
RETURNING *;

-- name: ListCharacterIntelEdges :many
-- Answers GET /characters/{id}/intel: every edge where the character is
-- either endpoint, source and target considered together.
SELECT * FROM app.character_intel_edge
 WHERE source_character_id = $1 OR target_character_id = $1
 ORDER BY weight DESC;
