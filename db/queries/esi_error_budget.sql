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

-- name: RecordErrorAgainstBudget :one
-- 01_ARCHITECTURE.md §5.7: 100 non-2XX/3XX responses per FIXED 60-second
-- window, installation-wide. A single atomic UPDATE (not a
-- read-then-branch-then-write from Go) so two replicas recording an error
-- at the same instant can never both observe a stale window_start and
-- double-reset it: whichever row version a transaction sees, the CASE
-- either rolls the window over or increments it, and Postgres's per-row
-- MVCC serialises the two outcomes.
UPDATE app.esi_error_budget
   SET window_start = CASE WHEN now() - window_start >= sqlc.arg(error_window)::interval
                            THEN now() ELSE window_start END,
       error_count   = CASE WHEN now() - window_start >= sqlc.arg(error_window)::interval
                            THEN 1 ELSE error_count + 1 END,
       updated_at    = now()
 WHERE id = 1
RETURNING *;
