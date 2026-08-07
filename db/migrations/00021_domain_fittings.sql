-- Project HANGAR — Phase 1b: fittings.
-- 02_DATABASE_SCHEMA.md §5.2 "Fittings (2)". Character-scoped only.

-- +goose Up

-- #1
CREATE TABLE app.character_fitting (
    character_id bigint      NOT NULL REFERENCES app.character(character_id),
    fitting_id   bigint      NOT NULL,
    name         text        NOT NULL,
    description  text,
    ship_type_id integer     NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, fitting_id)
);

-- #2
CREATE TABLE app.character_fitting_item (
    character_id  bigint  NOT NULL,
    fitting_id    bigint  NOT NULL,
    record_id     bigint  NOT NULL,
    type_id       integer NOT NULL,
    flag          text    NOT NULL,   -- open vocabulary, e.g. 'HiSlot0'
    quantity      bigint  NOT NULL,   -- NOT money
    PRIMARY KEY (character_id, fitting_id, record_id),
    FOREIGN KEY (character_id, fitting_id) REFERENCES app.character_fitting (character_id, fitting_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE app.character_fitting_item;
DROP TABLE app.character_fitting;
