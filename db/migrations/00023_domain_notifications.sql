-- Project HANGAR — Phase 1b: notifications.
-- 02_DATABASE_SCHEMA.md §5.2 "Notifications (2)". `character_notification`
-- is PARTITION BY RANGE (sent_at), monthly (§3.4). `type` is a CCP open
-- vocabulary (Principle 14) — hundreds of distinct notification types exist
-- and CCP adds more without notice.

-- +goose Up

-- #1
CREATE TABLE app.character_notification (
    character_id  bigint      NOT NULL,
    notification_id bigint      NOT NULL,
    sent_at         timestamptz NOT NULL,
    sender_id          bigint,
    sender_type           text,        -- open vocabulary
    type                     text        NOT NULL,   -- open vocabulary → app.open_vocabulary
    text                       text,        -- raw YAML body; unparseable payloads land here verbatim
    is_read                      boolean,
    PRIMARY KEY (character_id, notification_id, sent_at)   -- partition key in the PK
) PARTITION BY RANGE (sent_at);

CREATE TABLE app.character_notification_default PARTITION OF app.character_notification DEFAULT;

CREATE INDEX ON app.character_notification USING brin (sent_at);
CREATE INDEX ON app.character_notification (character_id, sent_at DESC);

-- #2 — /characters/{id}/notifications/contacts (standing-change contact
-- notifications; a distinct ESI shape from #1).
CREATE TABLE app.notification_contact (
    character_id   bigint      NOT NULL REFERENCES app.character(character_id),
    notification_id bigint      NOT NULL,
    send_date         timestamptz NOT NULL,
    sender_character_id bigint      NOT NULL,
    sender_name            text,
    message                   text,
    standing_level               double precision,   -- NOT money
    PRIMARY KEY (character_id, notification_id)
);
CREATE INDEX ON app.notification_contact (character_id, send_date DESC);

-- +goose Down
DROP TABLE app.notification_contact;
DROP TABLE app.character_notification_default;
DROP TABLE app.character_notification;
