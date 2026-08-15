-- app.character_notification, app.notification_contact
-- (02_DATABASE_SCHEMA.md §5.2). character_notification is partitioned by
-- sent_at; the partition key rides along in the ON CONFLICT target.

-- name: UpsertCharacterNotification :one
-- payload/parse_failed (00035): CCP notification YAML is not always valid
-- YAML (roadmap edge case). `payload` holds the parsed structure when it
-- is, or a `{"raw": text}` fallback wrapper when it is not; `parse_failed`
-- flags the latter so the generic renderer and the unknown-types board can
-- find them without re-parsing. A parse failure never blocks this insert.
INSERT INTO app.character_notification AS t (
    character_id, notification_id, sent_at, sender_id, sender_type, type, text, is_read, payload, parse_failed
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (character_id, notification_id, sent_at) DO UPDATE
   SET is_read = EXCLUDED.is_read
 WHERE t.is_read IS DISTINCT FROM EXCLUDED.is_read
RETURNING *;

-- name: ListCharacterNotificationsPage :many
-- ── DEFECT B46 (PHASE 20.6) ──────────────────────────────────────────────
-- This one never returned 500, which is why it outlived the three that did:
-- a single timestamptz argument types correctly, so it compiled and ran. It
-- was broken in the other direction. ESI delivers notifications in batches
-- that share a `timestamp` to the second, so `sent_at <` with no tiebreak
-- cannot address a page boundary that falls inside such a batch: the next
-- page either re-serves the tied rows or steps over them, and which one
-- happens depends on data the cursor does not carry.
--
-- ORDER BY sent_at DESC alone is also not a total order, so the rows within
-- a tie could come back in any order Postgres liked between two calls —
-- pagination over a non-deterministic sort is not pagination.
--
-- The keyset is now (sent_at, notification_id), matching the other four
-- pages in this family. The casts are load-bearing (see wallet.sql).
SELECT * FROM app.character_notification
 WHERE character_id = $1
   AND (sent_at, notification_id) < (sqlc.arg(before_sent_at)::timestamptz, sqlc.arg(before_notification_id)::bigint)
 ORDER BY sent_at DESC, notification_id DESC
 LIMIT sqlc.arg(page_size);

-- name: ListUnparseableCharacterNotifications :many
-- The unknown-types board's YAML-parse-failure view (Principle 14 applied
-- to a whole payload shape, not just one field — see 00035's header).
SELECT * FROM app.character_notification
 WHERE character_id = $1 AND parse_failed
 ORDER BY sent_at DESC;

-- name: UpsertNotificationContact :one
INSERT INTO app.notification_contact AS t (
    character_id, notification_id, send_date, sender_character_id, sender_name, message, standing_level
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (character_id, notification_id) DO NOTHING
RETURNING *;

-- name: ListNotificationContacts :many
SELECT * FROM app.notification_contact WHERE character_id = $1 ORDER BY send_date DESC;
