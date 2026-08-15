-- app.user, and the identity-adjacent tables that don't warrant their own
-- query file yet: app.session, app.api_token, app.api_token_access_log,
-- app.share_link, app.security_log, app.setting (02_DATABASE_SCHEMA.md §4.1
-- #5-#10). app.character / app.character_token live in character_token.sql.

-- name: CreateUser :one
INSERT INTO app.user (display_name)
VALUES ($1)
RETURNING *;

-- name: GetUser :one
SELECT * FROM app.user WHERE user_id = $1;

-- name: GetUserByMainCharacterID :one
SELECT * FROM app.user WHERE main_character_id = $1;

-- name: SetUserMainCharacter :exec
UPDATE app.user SET main_character_id = $2, updated_at = now() WHERE user_id = $1;

-- name: TouchUserLastLogin :exec
UPDATE app.user SET last_login_at = now(), updated_at = now() WHERE user_id = $1;

-- name: SetUserActive :exec
UPDATE app.user SET is_active = $2, updated_at = now() WHERE user_id = $1;

-- name: SetUserAdmin :exec
UPDATE app.user SET is_admin = $2, updated_at = now() WHERE user_id = $1;

-- name: ListUsersPage :many
-- Keyset pagination — OFFSET is prohibited (sqlc no-offset rule).
SELECT * FROM app.user
 WHERE user_id > sqlc.arg(after_user_id)
 ORDER BY user_id
 LIMIT sqlc.arg(page_size);

-- name: HasInvalidCharacterToken :one
-- Strict Mode's per-user probe (02_DATABASE_SCHEMA.md §4.1): true if any of
-- this user's characters holds an invalid token. The partial index on
-- app.character_token(valid) WHERE NOT valid is what keeps this a
-- millisecond query at scale.
SELECT EXISTS (
    SELECT 1 FROM app.character c
    JOIN app.character_token t USING (character_id)
   WHERE c.user_id = $1 AND NOT t.valid
) AS has_invalid_token;

-- ---- sessions ----

-- name: CreateSession :one
INSERT INTO app.session (user_id, pkce_verifier, state, ip_address, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSession :one
SELECT * FROM app.session WHERE session_id = $1 AND expires_at > now();

-- name: CompleteSessionLogin :exec
-- Attaches the resolved user to a pre-auth session and clears
-- pkce_verifier/state in the same statement: `state` is single-use
-- (01_ARCHITECTURE.md §7.1), so a session can never be replayed through
-- the callback path a second time even if the cookie is stolen after the
-- fact — GetSession still finds the row (for the browser's ongoing
-- session), but there is no verifier/state left to consume.
--
-- PHASE 15.1 FIX — expires_at is now promoted here. BeginLogin creates the
-- pre-auth row with expires_at = now + sso.StateTTL (10 minutes), which is
-- the correct lifetime for an *unconsumed* PKCE state but catastrophically
-- wrong for the authenticated session that replaces it: this statement did
-- not touch expires_at, and GetSession filters `expires_at > now()`, so
-- every user was silently force-logged-out ten minutes after clicking
-- "log in". It was invisible until Phase 15.1 wired the login flow for
-- real (Phase 15 left /auth/callback answering 501, so no session was ever
-- completed). config.CryptoConfig.SessionTTL (default 720h) had been
-- declared since Phase 5 and read by nothing — this is the consumer it was
-- always meant to have.
UPDATE app.session
   SET user_id = $2, pkce_verifier = NULL, state = NULL, expires_at = $3
 WHERE session_id = $1;

-- name: DeleteSession :exec
-- Explicit logout. Flagged by sqlc's flag-delete rule for review: a
-- terminated session carries no data worth retaining.
DELETE FROM app.session WHERE session_id = $1;

-- name: DeleteExpiredSessions :execrows
-- Flagged by sqlc's flag-delete rule for review: a session past its
-- expires_at carries no data worth a soft delete, unlike the ESI-synced
-- projections §5.1 requires soft deletes for.
--
-- PHASE 21 (B-2). This is the RETENTION path for app.session, and until
-- this phase it had no production caller at all. The row holds ip_address,
-- user_agent and pkce_verifier, so an installation that never ran this
-- retained personal data for every login it had ever served — measured at
-- the pre-v1.0 audit, 19 of 22 rows were expired and unreachable.
--
-- It was never an authentication hole: GetSession above filters
-- `expires_at > now()`, so an expired row cannot authenticate. The defect
-- was retention, and the fix is internal/housekeeping.Sweeper, which runs
-- this on a timer from `serve`.
--
-- :execrows rather than :exec because the sweeper logs what it deleted. A
-- housekeeping loop that cannot say what it removed is indistinguishable
-- from one that is not running — which is the defect this closes.
DELETE FROM app.session WHERE expires_at <= now();

-- ---- third-party API tokens ----

-- name: CreateApiToken :one
INSERT INTO app.api_token (user_id, name, hashed_secret, permissions, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetApiTokenByHash :one
SELECT * FROM app.api_token
 WHERE hashed_secret = $1 AND revoked_at IS NULL
   AND (expires_at IS NULL OR expires_at > now());

-- name: RevokeApiToken :exec
UPDATE app.api_token SET revoked_at = now() WHERE token_id = $1;

-- name: TouchApiTokenLastUsed :exec
UPDATE app.api_token SET last_used_at = now() WHERE token_id = $1;

-- name: RecordApiTokenAccess :exec
INSERT INTO app.api_token_access_log (token_id, route, status, ip_address)
VALUES ($1, $2, $3, $4);

-- name: ListApiTokenAccessLog :many
SELECT * FROM app.api_token_access_log
 WHERE token_id = $1
 ORDER BY at DESC
 LIMIT sqlc.arg(page_size);

-- name: ListApiTokensForUser :many
-- Phase 15 (internal/api/v1) addition: GET /api/v1/api-tokens needs a
-- caller-scoped list, which did not exist before this phase — every
-- other api_token query targets one already-known token_id/hash. Ordered
-- by created_at DESC so the newest token (the one a user just minted) is
-- first without needing a client-supplied cursor; the set per user is
-- small enough that keyset pagination isn't warranted here (same
-- reasoning as the SRS §6.2/§6.3 "naturally bounded per-owner" routes).
SELECT * FROM app.api_token
 WHERE user_id = $1
 ORDER BY created_at DESC;

-- ---- share links ----

-- name: CreateShareLink :one
INSERT INTO app.share_link (user_id, view, params, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetShareLink :one
SELECT * FROM app.share_link
 WHERE link_id = $1 AND revoked_at IS NULL
   AND (expires_at IS NULL OR expires_at > now());

-- name: RevokeShareLink :exec
UPDATE app.share_link SET revoked_at = now() WHERE link_id = $1;

-- name: ListShareLinksForUser :many
-- Phase 15 addition, same rationale as ListApiTokensForUser above: GET
-- /api/v1/me/share-links needs a caller-scoped list.
SELECT * FROM app.share_link
 WHERE user_id = $1
 ORDER BY created_at DESC;

-- ---- security log (append-only) ----

-- name: RecordSecurityLogEntry :exec
INSERT INTO app.security_log (user_id, action, target, ip_address, detail)
VALUES ($1, $2, $3, $4, $5);

-- name: ListSecurityLogForUser :many
SELECT * FROM app.security_log
 WHERE user_id = $1
 ORDER BY at DESC
 LIMIT sqlc.arg(page_size);

-- ---- settings ----

-- name: GetSetting :one
SELECT * FROM app.setting WHERE key = $1;

-- name: UpsertSetting :exec
INSERT INTO app.setting (key, value, updated_by)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE
   SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()
 WHERE app.setting.value IS DISTINCT FROM EXCLUDED.value;
