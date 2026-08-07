-- app.contact, app.contact_label, app.standing, app.medal, app.medal_issued
-- (02_DATABASE_SCHEMA.md §5.2). contact/contact_label/standing are
-- owner-polymorphic across all three owner kinds (character, corporation,
-- alliance — SRS v3.1 §6.2-§6.4).

-- name: UpsertContact :one
INSERT INTO app.contact AS t (owner_kind, owner_id, contact_id, contact_type, standing, is_blocked, is_watched, label_ids)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (owner_kind, owner_id, contact_id) DO UPDATE
   SET standing = EXCLUDED.standing, is_blocked = EXCLUDED.is_blocked,
       is_watched = EXCLUDED.is_watched, label_ids = EXCLUDED.label_ids, updated_at = now()
 WHERE (t.standing, t.is_blocked, t.is_watched, t.label_ids)
    IS DISTINCT FROM (EXCLUDED.standing, EXCLUDED.is_blocked, EXCLUDED.is_watched, EXCLUDED.label_ids)
RETURNING *;

-- name: ListContacts :many
SELECT * FROM app.contact WHERE owner_kind = $1 AND owner_id = $2 ORDER BY contact_id;

-- name: DeleteContactsNotIn :exec
DELETE FROM app.contact
 WHERE owner_kind = $1 AND owner_id = $2 AND NOT (contact_id = ANY(sqlc.arg(keep_contact_ids)::bigint[]));

-- name: UpsertContactLabel :one
INSERT INTO app.contact_label AS t (owner_kind, owner_id, label_id, name) VALUES ($1,$2,$3,$4)
ON CONFLICT (owner_kind, owner_id, label_id) DO UPDATE SET name = EXCLUDED.name
 WHERE t.name IS DISTINCT FROM EXCLUDED.name
RETURNING *;

-- name: ListContactLabels :many
SELECT * FROM app.contact_label WHERE owner_kind = $1 AND owner_id = $2 ORDER BY label_id;

-- name: UpsertStanding :one
INSERT INTO app.standing AS t (owner_kind, owner_id, from_id, from_type, standing) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (owner_kind, owner_id, from_id) DO UPDATE
   SET standing = EXCLUDED.standing, updated_at = now()
 WHERE t.standing IS DISTINCT FROM EXCLUDED.standing
RETURNING *;

-- name: ListStandings :many
SELECT * FROM app.standing WHERE owner_kind = $1 AND owner_id = $2 ORDER BY from_id;

-- name: UpsertMedal :one
INSERT INTO app.medal AS t (corporation_id, medal_id, title, description, created_at, creator_id)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (corporation_id, medal_id) DO UPDATE
   SET title = EXCLUDED.title, description = EXCLUDED.description
 WHERE (t.title, t.description) IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.description)
RETURNING *;

-- name: ListCorporationMedals :many
SELECT * FROM app.medal WHERE corporation_id = $1 ORDER BY medal_id;

-- name: InsertMedalIssued :one
INSERT INTO app.medal_issued (corporation_id, medal_id, character_id, reason, status, issuer_id, issued_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (corporation_id, medal_id, character_id, issued_at) DO NOTHING
RETURNING *;

-- name: ListMedalsIssuedByCorporation :many
SELECT * FROM app.medal_issued WHERE corporation_id = $1 ORDER BY issued_at DESC;

-- name: ListMedalsIssuedToCharacter :many
-- Answers GET /characters/{id}/medals.
SELECT * FROM app.medal_issued WHERE character_id = $1 ORDER BY issued_at DESC;
