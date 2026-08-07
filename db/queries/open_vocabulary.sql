-- name: RecordOpenVocabularyValue :exec
-- The one Principle-14 write path: every observed external value, of every
-- vocabulary, lands here. Never rejected, whatever it is.
INSERT INTO app.open_vocabulary (vocabulary, value)
VALUES ($1, $2)
ON CONFLICT (vocabulary, value) DO UPDATE
   SET last_seen_at = now(),
       occurrences  = app.open_vocabulary.occurrences + 1;

-- name: ListUnacknowledgedOpenVocabulary :many
SELECT * FROM app.open_vocabulary
 WHERE vocabulary = $1 AND acknowledged_at IS NULL
 ORDER BY first_seen_at;

-- name: AcknowledgeOpenVocabularyValue :exec
UPDATE app.open_vocabulary
   SET acknowledged_at = now(), acknowledged_by = $3
 WHERE vocabulary = $1 AND value = $2;

-- name: CountUnacknowledgedOpenVocabulary :one
SELECT count(*) FROM app.open_vocabulary
 WHERE vocabulary = $1 AND acknowledged_at IS NULL;
