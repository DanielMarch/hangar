-- app.character_notification, app.notification_contact
-- (02_DATABASE_SCHEMA.md §5.2). character_notification is partitioned by
-- sent_at; the partition key rides along in the ON CONFLICT target.

-- name: UpsertCharacterNotification :one
INSERT INTO app.character_notification AS t (
    character_id, notification_id, sent_at, sender_id, sender_type, type, text, is_read
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (character_id, notification_id, sent_at) DO UPDATE
   SET is_read = EXCLUDED.is_read
 WHERE t.is_read IS DISTINCT FROM EXCLUDED.is_read
RETURNING *;

-- name: ListCharacterNotificationsPage :many
SELECT * FROM app.character_notification
 WHERE character_id = $1 AND sent_at < sqlc.arg(before_sent_at)
 ORDER BY sent_at DESC
 LIMIT sqlc.arg(page_size);

-- name: UpsertNotificationContact :one
INSERT INTO app.notification_contact AS t (
    character_id, notification_id, send_date, sender_character_id, sender_name, message, standing_level
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (character_id, notification_id) DO NOTHING
RETURNING *;

-- name: ListNotificationContacts :many
SELECT * FROM app.notification_contact WHERE character_id = $1 ORDER BY send_date DESC;
