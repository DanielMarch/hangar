-- app.esi_error_budget — Governor 2's installation-wide budget, one row
-- (02_DATABASE_SCHEMA.md §4.3 #27, SRS v3.1 §4.1.3). Read through a
-- one-second in-process cache by the caller; every non-2XX/3XX response
-- writes here.

-- name: GetErrorBudget :one
SELECT * FROM app.esi_error_budget WHERE id = 1;

-- name: InitErrorBudget :exec
INSERT INTO app.esi_error_budget (id, window_start, error_count, paused)
VALUES (1, now(), 0, false)
ON CONFLICT (id) DO NOTHING;

-- name: IncrementErrorBudget :one
UPDATE app.esi_error_budget
   SET error_count = error_count + 1, updated_at = now()
 WHERE id = 1
RETURNING *;

-- name: ResetErrorBudgetWindow :exec
UPDATE app.esi_error_budget
   SET window_start = now(), error_count = 0, updated_at = now()
 WHERE id = 1;

-- name: SetErrorBudgetPaused :exec
UPDATE app.esi_error_budget SET paused = $1, updated_at = now() WHERE id = 1;
