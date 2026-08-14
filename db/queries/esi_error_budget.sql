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

-- ── PHASE 20.2 (defect B29): TWO QUERIES DELETED, NOT WIRED ──────────────
--
-- IncrementErrorBudget (:one, error_count = error_count + 1) and
-- ResetErrorBudgetWindow (:exec, window_start = now(), error_count = 0)
-- lived here with no production caller and were listed under B29 in
-- test/reachability/generated_allowlist.txt.
--
-- They are DELETED rather than given callers, because giving them callers
-- would reintroduce the exact race RecordErrorAgainstBudget below was
-- written to remove. Together they are a read-then-branch-then-write
-- sequence: Go reads window_start, decides whether the window has rolled
-- over, and then either resets or increments. Two replicas recording an
-- error in the same instant can both observe the same stale window_start
-- and both reset, discarding one window's worth of accounting from a
-- budget whose entire purpose is to be installation-wide and exact.
--
-- RecordErrorAgainstBudget does both branches in ONE atomic UPDATE, and it
-- has had a production caller since Phase 4 (internal/esi/ratelimit's
-- Governor2.RecordError, reached from internal/esi.Client.Do). Nothing was
-- missing; these two were the superseded halves of it, left behind.
--
-- FOR THE RECORD, because the two look alike from the outside: the budget
-- reading error_count = 0 after 1371 real sync runs on the development
-- installation is NOT evidence of a missing caller. Every one of those runs
-- returned 200 or 304, and §5.7 counts only non-2XX/3XX responses. A zero
-- there was the correct reading of a healthy installation.
