-- app.calendar_event, app.calendar_event_detail, app.calendar_event_attendee
-- (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertCalendarEvent :one
INSERT INTO app.calendar_event AS t (character_id, event_id, title, event_date, event_response, importance)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (character_id, event_id) DO UPDATE
   SET event_response = EXCLUDED.event_response, updated_at = now()
 WHERE t.event_response IS DISTINCT FROM EXCLUDED.event_response
RETURNING *;

-- name: ListCalendarEvents :many
SELECT * FROM app.calendar_event WHERE character_id = $1 ORDER BY event_date DESC;

-- name: UpsertCalendarEventDetail :one
INSERT INTO app.calendar_event_detail AS t (character_id, event_id, text, owner_id, owner_name, owner_type, duration)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (character_id, event_id) DO UPDATE
   SET text = EXCLUDED.text, duration = EXCLUDED.duration
 WHERE (t.text, t.duration) IS DISTINCT FROM (EXCLUDED.text, EXCLUDED.duration)
RETURNING *;

-- name: GetCalendarEventDetail :one
SELECT * FROM app.calendar_event_detail WHERE character_id = $1 AND event_id = $2;

-- name: UpsertCalendarEventAttendee :one
INSERT INTO app.calendar_event_attendee AS t (character_id, event_id, attendee_character_id, response)
VALUES ($1,$2,$3,$4)
ON CONFLICT (character_id, event_id, attendee_character_id) DO UPDATE
   SET response = EXCLUDED.response
 WHERE t.response IS DISTINCT FROM EXCLUDED.response
RETURNING *;

-- name: ListCalendarEventAttendees :many
SELECT * FROM app.calendar_event_attendee WHERE character_id = $1 AND event_id = $2;
