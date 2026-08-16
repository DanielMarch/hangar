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

-- name: ResumeDiscordInvalidBudgetIfWindowElapsed :one
-- PHASE 22, defect B-5 ONE DRIVER OVER. This budget mirrors §5.7's
-- mechanism, and it mirrored the deadlock with it: the un-pause branch
-- lives in RecordInvalid, RecordInvalid runs only on a counted RESPONSE,
-- and a paused driver sends nothing — so Discord provisioning stopped
-- permanently after any burst of 401/403/429, exactly as ESI sync did.
-- Found while fixing Governor 2, by call-graph rather than by measurement:
-- unlike the ESI budget this one has never been observed to trip in a gate
-- run, because no gate drives a real Discord guild.
--
-- The pause here has NO separate resume threshold — the fixed window's own
-- rollover is what un-pauses (see internal/provisioning/drivers/discord's
-- InvalidBudget doc), so the elapsed test is the whole condition and there
-- is no hysteresis gap to preserve. invalid_count is zeroed alongside for
-- the same reason the ESI query zeroes error_count: a counter describing a
-- window that no longer applies is a lie the next reader has no way to
-- detect.
UPDATE app.discord_invalid_budget
   SET window_start  = CASE WHEN now() - window_start >= sqlc.arg(invalid_window)::interval
                            THEN now() ELSE window_start END,
       invalid_count = CASE WHEN now() - window_start >= sqlc.arg(invalid_window)::interval
                            THEN 0 ELSE invalid_count END,
       paused        = CASE WHEN paused
                             AND now() - window_start >= sqlc.arg(invalid_window)::interval
                            THEN false ELSE paused END,
       updated_at    = now()
 WHERE id = 1
RETURNING *;
