-- app.mail_header, app.mail_body, app.mail_recipient, app.mail_label,
-- app.mail_list (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertMailHeader :one
INSERT INTO app.mail_header AS t (character_id, mail_id, from_id, subject, sent_at, is_read, labels)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (character_id, mail_id) DO UPDATE
   SET is_read = EXCLUDED.is_read, labels = EXCLUDED.labels, updated_at = now()
 WHERE (t.is_read, t.labels) IS DISTINCT FROM (EXCLUDED.is_read, EXCLUDED.labels)
RETURNING *;

-- name: ListMailHeadersPage :many
SELECT * FROM app.mail_header
 WHERE character_id = $1 AND (sent_at, mail_id) < (sqlc.arg(before_sent_at), sqlc.arg(before_mail_id))
 ORDER BY sent_at DESC, mail_id DESC
 LIMIT sqlc.arg(page_size);

-- name: UpsertMailBody :one
INSERT INTO app.mail_body AS t (character_id, mail_id, body) VALUES ($1,$2,$3)
ON CONFLICT (character_id, mail_id) DO UPDATE SET body = EXCLUDED.body, updated_at = now()
 WHERE t.body IS DISTINCT FROM EXCLUDED.body
RETURNING *;

-- name: GetMailBody :one
SELECT * FROM app.mail_body WHERE character_id = $1 AND mail_id = $2;

-- name: InsertMailRecipient :one
INSERT INTO app.mail_recipient (character_id, mail_id, recipient_id, recipient_type) VALUES ($1,$2,$3,$4)
ON CONFLICT (character_id, mail_id, recipient_id) DO NOTHING
RETURNING *;

-- name: ListMailRecipients :many
SELECT * FROM app.mail_recipient WHERE character_id = $1 AND mail_id = $2;

-- name: UpsertMailLabel :one
INSERT INTO app.mail_label AS t (character_id, label_id, name, color, unread_count) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (character_id, label_id) DO UPDATE
   SET name = EXCLUDED.name, color = EXCLUDED.color, unread_count = EXCLUDED.unread_count
 WHERE (t.name, t.color, t.unread_count) IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.color, EXCLUDED.unread_count)
RETURNING *;

-- name: ListMailLabels :many
SELECT * FROM app.mail_label WHERE character_id = $1 ORDER BY label_id;

-- name: UpsertMailList :one
INSERT INTO app.mail_list AS t (character_id, list_id, name) VALUES ($1,$2,$3)
ON CONFLICT (character_id, list_id) DO UPDATE SET name = EXCLUDED.name
 WHERE t.name IS DISTINCT FROM EXCLUDED.name
RETURNING *;

-- name: ListMailLists :many
SELECT * FROM app.mail_list WHERE character_id = $1 ORDER BY list_id;
