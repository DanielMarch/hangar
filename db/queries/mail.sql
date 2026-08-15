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
-- ── DEFECT B46 (PHASE 20.6) ──────────────────────────────────────────────
-- Identical to the two wallet keyset queries: an uncast row comparison made
-- sqlc generate `BeforeMailID time.Time`, the call site passed the same
-- time.Time to both parameters, and GET /api/v1/characters/{id}/mail
-- returned the same 22P02 on every call. B46 was reported as "the wallet
-- screen"; the mail screen was broken by the same line of SQL and was found
-- by probing the running installation rather than by reading the report.
-- The casts are load-bearing — see the note on ListWalletJournalPage.
SELECT * FROM app.mail_header
 WHERE character_id = $1 AND (sent_at, mail_id) < (sqlc.arg(before_sent_at)::timestamptz, sqlc.arg(before_mail_id)::bigint)
 ORDER BY sent_at DESC, mail_id DESC
 LIMIT sqlc.arg(page_size);

-- name: ListMailHeadersByCharacter :many
-- PHASE 20.10 — the /api/v2 shim's character.mail route.
--
-- The keyset page above cannot serve it: legacy loaded the whole relation and
-- paginated in PHP, and OFFSET is prohibited here (SRS §6), so the shim reads
-- the ordered set and slices in Go (v2shim.Window). This is the query B55's
-- nine routes turned out to ALREADY have and these four genuinely did not.
--
-- ORDER BY mail_id, not by sent_at: legacy's CharacterController::getMail
-- calls `->paginate()` with NO orderBy (verified against eveseat/api at the
-- commit testdata/legacy-api-v2/README.md pins), so MySQL returns the
-- clustered-index scan. `mail_headers` has no declared primary key after
-- 2019_10_30_131410 dropped character_id, so InnoDB clusters on the first
-- UNIQUE NOT NULL index, which is the one 2018_01_05_112025 added on mail_id.
SELECT * FROM app.mail_header WHERE character_id = $1 ORDER BY mail_id;

-- name: UpsertMailBody :one
INSERT INTO app.mail_body AS t (character_id, mail_id, body) VALUES ($1,$2,$3)
ON CONFLICT (character_id, mail_id) DO UPDATE SET body = EXCLUDED.body, updated_at = now()
 WHERE t.body IS DISTINCT FROM EXCLUDED.body
RETURNING *;

-- name: GetMailBody :one
SELECT * FROM app.mail_body WHERE character_id = $1 AND mail_id = $2;

-- name: ListMailHeadersWithoutBody :many
-- Drives the per-mail body fanout (roadmap: "Mail bodies are one ESI
-- request per mail"; HANGAR must route each through the catalogue, never
-- build the URL by hand — see worker.doMailBodyFanout).
SELECT h.* FROM app.mail_header h
  LEFT JOIN app.mail_body b ON b.character_id = h.character_id AND b.mail_id = h.mail_id
 WHERE h.character_id = $1 AND b.mail_id IS NULL
 ORDER BY h.mail_id;

-- name: InsertMailRecipient :one
INSERT INTO app.mail_recipient (character_id, mail_id, recipient_id, recipient_type) VALUES ($1,$2,$3,$4)
ON CONFLICT (character_id, mail_id, recipient_id) DO NOTHING
RETURNING *;

-- name: ListMailRecipients :many
-- ORDER BY added in Phase 20.10. It had none, so the row order was whatever
-- Postgres chose and could differ between two calls for the same mail — not
-- a defect anything had noticed, but the /api/v2 shim renders these into a
-- JSON array whose order IS the bytes, and an unordered read cannot be
-- byte-compared. recipient_id matches both HANGAR's primary key and the
-- (mail_id, recipient_id) unique index legacy clusters this table on.
SELECT * FROM app.mail_recipient WHERE character_id = $1 AND mail_id = $2 ORDER BY recipient_id;

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
