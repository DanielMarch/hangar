-- app.corporation_project, app.corporation_project_contributor,
-- app.corporation_project_contribution (02_DATABASE_SCHEMA.md §5.3). The
-- Principle 13 / Gate 6 uuid-alongside-bigint fixture: project_id is
-- CCP-supplied, never generated, never coerced to/from bigint or text.

-- name: UpsertCorporationProject :one
INSERT INTO app.corporation_project AS t (
    project_id, corporation_id, name, state, contribution_type, target_progress,
    current_progress, reward_isk, expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (project_id) DO UPDATE
   SET state = EXCLUDED.state, current_progress = EXCLUDED.current_progress,
       updated_at = now()
 WHERE (t.state, t.current_progress) IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.current_progress)
RETURNING *;

-- name: GetCorporationProject :one
SELECT * FROM app.corporation_project WHERE project_id = $1;

-- name: UpdateCorporationProjectDetail :exec
-- PHASE 20.7 (B48/B50). The fields only the project DETAIL route carries.
--
-- This exists because UpsertCorporationProject's ON CONFLICT clause updates
-- ONLY state and current_progress — deliberately, so that the frequently
-- running list sync cannot blank what the detail sync wrote. The corollary
-- is that the detail sync cannot write through that upsert either, because
-- for an existing project it would take the same conflict path. So the two
-- routes write disjoint column sets through two statements, and neither can
-- erase the other's.
--
-- contribution_type is derived from the detail response's `configuration`,
-- whose oneOf key names the kind of contribution the project wants
-- ('mine_material', 'manufacture_item', 'destroy_npc', ...); expires_at is
-- `details.expires`. Neither appears on the list route at all.
UPDATE app.corporation_project
   SET contribution_type = $2, expires_at = $3, updated_at = now()
 WHERE project_id = $1
   AND (contribution_type, expires_at) IS DISTINCT FROM ($2, $3);

-- name: ListCorporationProjects :many
SELECT * FROM app.corporation_project WHERE corporation_id = $1 ORDER BY expires_at NULLS LAST;

-- name: UpsertCorporationProjectContributor :one
INSERT INTO app.corporation_project_contributor AS t (project_id, character_id, joined_at)
VALUES ($1,$2,$3)
ON CONFLICT (project_id, character_id) DO UPDATE
   SET joined_at = EXCLUDED.joined_at, updated_at = now()
 WHERE t.joined_at IS DISTINCT FROM EXCLUDED.joined_at
RETURNING *;

-- name: ListCorporationProjectContributors :many
SELECT * FROM app.corporation_project_contributor WHERE project_id = $1 ORDER BY character_id;

-- name: UpsertCorporationProjectContribution :one
INSERT INTO app.corporation_project_contribution AS t (project_id, character_id, amount)
VALUES ($1,$2,$3)
ON CONFLICT (project_id, character_id) DO UPDATE
   SET amount = EXCLUDED.amount, updated_at = now()
 WHERE t.amount IS DISTINCT FROM EXCLUDED.amount
RETURNING *;

-- name: GetCorporationProjectContribution :one
-- Backs GET /corporations/{id}/projects/{project_id}/contribution/{character_id} —
-- the uuid PK joining a bigint FK in one row without coercion (Gate 6).
SELECT * FROM app.corporation_project_contribution WHERE project_id = $1 AND character_id = $2;

-- name: ListCorporationProjectContributions :many
SELECT * FROM app.corporation_project_contribution WHERE project_id = $1 ORDER BY amount DESC;
