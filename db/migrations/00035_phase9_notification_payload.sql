-- Project HANGAR — Phase 9: notification YAML parsing (Principle 14).
--
-- CCP's notification "text" body is YAML, and it is not always VALID YAML —
-- some payloads carry unquoted values that trip a strict parser. That is an
-- expected path, not an exception (roadmap edge case). `text` (added in
-- 00023) already keeps the raw body verbatim regardless of parse outcome;
-- this migration adds the structured side of the same row:
--
--   payload      — the YAML re-expressed as JSONB when it parses, or a
--                   `{"raw": "<verbatim text>"}` wrapper when it does not.
--                   Either way this is what internal/alerting/render/generic.go
--                   walks to produce a generic key/value rendering — Phase 9
--                   is the first domain-wide consumer of the open-vocabulary
--                   pattern (02_DATABASE_SCHEMA.md §3.3) applied to a whole
--                   payload shape, not just one field.
--   parse_failed — true when the YAML did not parse. Never a hard failure:
--                   the row is still written, `type` still feeds
--                   app.open_vocabulary, and the sync queue moves on to the
--                   next notification.

-- +goose Up
ALTER TABLE app.character_notification ADD COLUMN payload jsonb;
ALTER TABLE app.character_notification ADD COLUMN parse_failed boolean NOT NULL DEFAULT false;
CREATE INDEX character_notification_parse_failed_idx ON app.character_notification (character_id, parse_failed) WHERE parse_failed;

-- +goose Down
DROP INDEX app.character_notification_parse_failed_idx;
ALTER TABLE app.character_notification DROP COLUMN parse_failed;
ALTER TABLE app.character_notification DROP COLUMN payload;
