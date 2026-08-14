-- app.character, app.character_token, app.character_token_scope
-- (02_DATABASE_SCHEMA.md §4.1 #2-#4). Also carries the minimal
-- app.corporation/app.alliance upserts needed to satisfy character's FKs —
-- the full domain projection for those tables is Phase 1b/Phase 7-8's job.

-- name: UpsertCorporationStub :exec
-- Minimal upsert so app.character's FK has a row to point at before the
-- full corporation sync (Phase 8) exists. Phase 8 owns the complete upsert.
INSERT INTO app.corporation (corporation_id, name, ticker)
VALUES ($1, $2, $3)
ON CONFLICT (corporation_id) DO NOTHING;

-- name: UpsertAllianceStub :exec
INSERT INTO app.alliance (alliance_id, name)
VALUES ($1, $2)
ON CONFLICT (alliance_id) DO NOTHING;

-- name: UpsertCharacter :one
INSERT INTO app.character AS t (
    character_id, user_id, name, corporation_id, alliance_id, faction_id,
    security_status, birthday, gender, race_id, bloodline_id, ancestry_id,
    description, title, owner_hash
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
ON CONFLICT (character_id) DO UPDATE
   SET user_id          = EXCLUDED.user_id,
       name              = EXCLUDED.name,
       corporation_id    = EXCLUDED.corporation_id,
       alliance_id       = EXCLUDED.alliance_id,
       faction_id        = EXCLUDED.faction_id,
       security_status   = EXCLUDED.security_status,
       birthday          = EXCLUDED.birthday,
       gender            = EXCLUDED.gender,
       race_id           = EXCLUDED.race_id,
       bloodline_id      = EXCLUDED.bloodline_id,
       ancestry_id       = EXCLUDED.ancestry_id,
       description       = EXCLUDED.description,
       title             = EXCLUDED.title,
       owner_hash        = EXCLUDED.owner_hash,
       updated_at        = now()
 WHERE (t.name, t.corporation_id, t.alliance_id, t.security_status, t.owner_hash, t.title)
    IS DISTINCT FROM
       (EXCLUDED.name, EXCLUDED.corporation_id, EXCLUDED.alliance_id, EXCLUDED.security_status, EXCLUDED.owner_hash, EXCLUDED.title)
RETURNING *;

-- name: GetCharacter :one
SELECT * FROM app.character WHERE character_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteCharacter :exec
UPDATE app.character SET deleted_at = now(), updated_at = now() WHERE character_id = $1;

-- name: ListCharactersForUser :many
SELECT * FROM app.character WHERE user_id = $1 AND deleted_at IS NULL ORDER BY character_id;

-- ---- character_token ----

-- name: UpsertCharacterToken :exec
INSERT INTO app.character_token (
    character_id, key_version, wrapped_dek, nonce, ciphertext,
    access_expires_at, valid, invalid_reason, owner_hash, last_refreshed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, true, NULL, $7, now()
)
ON CONFLICT (character_id) DO UPDATE
   SET key_version       = EXCLUDED.key_version,
       wrapped_dek        = EXCLUDED.wrapped_dek,
       nonce              = EXCLUDED.nonce,
       ciphertext         = EXCLUDED.ciphertext,
       access_expires_at  = EXCLUDED.access_expires_at,
       valid              = true,
       invalid_reason     = NULL,
       owner_hash         = EXCLUDED.owner_hash,
       last_refreshed_at  = now(),
       updated_at         = now();

-- name: GetCharacterToken :one
SELECT * FROM app.character_token WHERE character_id = $1;

-- name: InvalidateCharacterToken :exec
UPDATE app.character_token
   SET valid = false, invalid_reason = $2, updated_at = now()
 WHERE character_id = $1;

-- name: ListInvalidCharacterTokens :many
-- Backed by the partial index on (valid) WHERE NOT valid.
SELECT * FROM app.character_token WHERE NOT valid;

-- ---- character_token_scope ----

-- name: ReplaceCharacterTokenScopes :exec
-- Flagged by sqlc's flag-delete rule for review: the scope set on a token is
-- authoritative from the SSO response and has no meaningful "removed but
-- remembered" state, unlike the ESI-synced projections §5.1 requires soft
-- deletes for. Callers wrap this and the following insert in one
-- transaction.
DELETE FROM app.character_token_scope WHERE character_id = $1;

-- name: AddCharacterTokenScope :exec
INSERT INTO app.character_token_scope (character_id, scope)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListCharacterTokenScopes :many
SELECT scope FROM app.character_token_scope WHERE character_id = $1 ORDER BY scope;

-- name: ListCharacterIDsWithValidTokens :many
-- Every character reconciliation should consider (defect B42). Soft-deleted
-- characters and invalidated tokens are excluded here rather than in Go, so
-- a caller cannot forget either filter.
SELECT c.character_id
  FROM app.character c
  JOIN app.character_token t ON t.character_id = c.character_id
 WHERE t.valid
   AND c.deleted_at IS NULL
 ORDER BY c.character_id;

-- name: UpsertCharacterStub :exec
-- Minimal character row so app.corporation_member's FK has something to
-- point at (defect B48).
--
-- GET /corporations/{id}/members returns bare character IDS — no names —
-- for every member of the corporation, and almost none of them have ever
-- logged into HANGAR, so app.character has no row for them. Before this,
-- SyncCorporationMembers upserted membership directly and the whole route
-- died on the first stranger with
--   insert or update on table "corporation_member" violates foreign key
--   constraint "corporation_member_character_id_fkey" (SQLSTATE 23503).
--
-- The name is deliberately left empty rather than invented: ESI did not
-- supply one on this route, and a placeholder like "Unknown" would be
-- indistinguishable from a real name downstream. The character sheet route
-- fills it in if and when that character is ever synced. Exactly the same
-- shape, and the same reasoning, as UpsertCorporationStub above.
INSERT INTO app.character (character_id, name, owner_hash)
VALUES ($1, '', '')
ON CONFLICT (character_id) DO NOTHING;
