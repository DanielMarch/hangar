-- Project HANGAR — Phase 1b: history.
-- 02_DATABASE_SCHEMA.md §5.2 "History (2)". Both are inherently
-- single-owner concepts (a character's own corp history; a corp's own
-- alliance history), so neither uses owner polymorphism.

-- +goose Up

-- #1
CREATE TABLE app.character_corporation_history (
    character_id   bigint      NOT NULL REFERENCES app.character(character_id),
    record_id      bigint      NOT NULL,
    corporation_id bigint      NOT NULL,
    is_deleted     boolean     NOT NULL DEFAULT false,
    start_date     timestamptz NOT NULL,
    PRIMARY KEY (character_id, record_id)
);
CREATE INDEX ON app.character_corporation_history (corporation_id);

-- #2
CREATE TABLE app.corporation_alliance_history (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    record_id      bigint      NOT NULL,
    alliance_id    bigint,
    is_deleted     boolean     NOT NULL DEFAULT false,
    start_date     timestamptz NOT NULL,
    PRIMARY KEY (corporation_id, record_id)
);
CREATE INDEX ON app.corporation_alliance_history (alliance_id);

-- +goose Down
DROP TABLE app.corporation_alliance_history;
DROP TABLE app.character_corporation_history;
