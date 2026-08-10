-- app.discord_invalid_budget — Phase 12's installation-wide Discord
-- invalid-request (401/403/429) counter, one row, mirroring
-- db/queries/esi_error_budget.sql's shape exactly (01_ARCHITECTURE.md
-- §9.3 shares §5.7's mechanism — see db/migrations/00038's comment for
-- why "shares the mechanism" won out over the surrounding "rolling"
-- prose). Read through a one-second in-process cache by the caller.

-- name: GetDiscordInvalidBudget :one
SELECT * FROM app.discord_invalid_budget WHERE id = 1;

-- name: InitDiscordInvalidBudget :exec
INSERT INTO app.discord_invalid_budget (id, window_start, invalid_count, paused)
VALUES (1, now(), 0, false)
ON CONFLICT (id) DO NOTHING;

-- name: SetDiscordInvalidBudgetPaused :exec
UPDATE app.discord_invalid_budget SET paused = $1, updated_at = now() WHERE id = 1;

-- name: RecordInvalidAgainstDiscordBudget :one
-- Same atomic single-UPDATE window-rollover pattern as
-- RecordErrorAgainstBudget: one round trip, no read-then-branch-then-write
-- race between replicas.
UPDATE app.discord_invalid_budget
   SET window_start  = CASE WHEN now() - window_start >= sqlc.arg(invalid_window)::interval
                            THEN now() ELSE window_start END,
       invalid_count = CASE WHEN now() - window_start >= sqlc.arg(invalid_window)::interval
                            THEN 1 ELSE invalid_count + 1 END,
       updated_at    = now()
 WHERE id = 1
RETURNING *;
