-- app.character_note, app.insurance_price, app.moon_report
-- (02_DATABASE_SCHEMA.md §5.2).

-- name: CreateCharacterNote :one
INSERT INTO app.character_note (character_id, author_user_id, body) VALUES ($1,$2,$3)
RETURNING *;

-- name: ListCharacterNotes :many
SELECT * FROM app.character_note WHERE character_id = $1 ORDER BY created_at DESC;

-- name: UpsertInsurancePrice :one
INSERT INTO app.insurance_price AS t (type_id, level, cost, payout) VALUES ($1,$2,$3,$4)
ON CONFLICT (type_id, level) DO UPDATE
   SET cost = EXCLUDED.cost, payout = EXCLUDED.payout, updated_at = now()
 WHERE (t.cost, t.payout) IS DISTINCT FROM (EXCLUDED.cost, EXCLUDED.payout)
RETURNING *;

-- name: ListInsurancePrices :many
SELECT * FROM app.insurance_price WHERE type_id = $1 ORDER BY level;

-- name: CreateMoonReport :one
INSERT INTO app.moon_report (submitted_by, moon_id, raw_text, parsed) VALUES ($1,$2,$3,$4)
RETURNING *;

-- name: GetMoonReport :one
SELECT * FROM app.moon_report WHERE report_id = $1;
