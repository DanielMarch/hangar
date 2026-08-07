-- Project HANGAR — Phase 1b: calendar.
-- 02_DATABASE_SCHEMA.md §5.2 "Calendar (3)" and the §5.2 "detail tables
-- added for parity" (`app.calendar_event_detail`). Character-scoped only.

-- +goose Up

-- #1
CREATE TABLE app.calendar_event (
    character_id bigint      NOT NULL REFERENCES app.character(character_id),
    event_id     bigint      NOT NULL,
    title        text        NOT NULL,
    event_date   timestamptz NOT NULL,
    event_response text,        -- open vocabulary: 'accepted'|'declined'|'tentative'|'not_responded'
    importance     integer,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, event_id)
);
CREATE INDEX ON app.calendar_event (character_id, event_date DESC);

-- #2
CREATE TABLE app.calendar_event_detail (
    character_id bigint NOT NULL,
    event_id     bigint NOT NULL,
    text         text,
    owner_id     bigint,
    owner_name   text,
    owner_type   text,   -- open vocabulary: 'character'|'corporation'|'alliance'|'eve_server'
    duration     integer,
    PRIMARY KEY (character_id, event_id),
    FOREIGN KEY (character_id, event_id) REFERENCES app.calendar_event (character_id, event_id) ON DELETE CASCADE
);

-- #3
CREATE TABLE app.calendar_event_attendee (
    character_id bigint NOT NULL,
    event_id     bigint NOT NULL,
    attendee_character_id bigint NOT NULL,
    response                 text,   -- open vocabulary
    PRIMARY KEY (character_id, event_id, attendee_character_id),
    FOREIGN KEY (character_id, event_id) REFERENCES app.calendar_event (character_id, event_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE app.calendar_event_attendee;
DROP TABLE app.calendar_event_detail;
DROP TABLE app.calendar_event;
