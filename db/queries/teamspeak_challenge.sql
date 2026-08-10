-- app.teamspeak_challenge — Phase 13's single-use TS3 linking token
-- (01_ARCHITECTURE.md §9.4, db/migrations/00039).

-- name: IssueTeamspeakChallenge :one
INSERT INTO app.teamspeak_challenge (token, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: RedeemTeamspeakChallenge :one
-- The single-use guarantee: this UPDATE only ever affects a row that is
-- still unconsumed and unexpired, so a second redemption attempt with the
-- same token — concurrent or sequential — always affects zero rows.
-- Callers must check for pgx.ErrNoRows (sqlc's :one) and treat it as
-- "already consumed, expired, or never issued", never distinguishing
-- those three from the SQL result alone (no timing side-channel about
-- which reason applies).
UPDATE app.teamspeak_challenge
   SET consumed_at = now(),
       client_unique_identifier = $2
 WHERE token = $1
   AND consumed_at IS NULL
   AND expires_at > now()
RETURNING *;

-- name: GetTeamspeakChallenge :one
SELECT * FROM app.teamspeak_challenge WHERE token = $1;
