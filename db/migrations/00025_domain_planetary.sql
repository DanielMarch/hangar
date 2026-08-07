-- Project HANGAR — Phase 1b: planetary interaction.
-- 02_DATABASE_SCHEMA.md §5.2 "Planetary interaction (2)" and the "detail
-- tables added for parity" (`app.planet_colony_detail` holds pins,
-- extractors and routes as JSONB rather than five more normalised tables —
-- PI layouts are read wholesale by the UI and never queried per-pin).
-- Character-scoped only.

-- +goose Up

-- #1
CREATE TABLE app.planet_colony (
    character_id     bigint      NOT NULL REFERENCES app.character(character_id),
    planet_id        bigint      NOT NULL,
    solar_system_id  integer     NOT NULL,
    planet_type      text        NOT NULL,   -- open vocabulary
    owner_id         bigint      NOT NULL,
    last_update      timestamptz NOT NULL,
    upgrade_level    integer     NOT NULL,   -- NOT money
    num_pins         integer     NOT NULL,   -- NOT money
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, planet_id)
);

-- #2 — pins, extractors, links and routes for GET
-- /characters/{id}/planets/{planet_id}, read wholesale by the UI.
CREATE TABLE app.planet_colony_detail (
    character_id bigint      NOT NULL,
    planet_id    bigint      NOT NULL,
    pins         jsonb       NOT NULL DEFAULT '[]',
    links        jsonb       NOT NULL DEFAULT '[]',
    routes       jsonb       NOT NULL DEFAULT '[]',
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, planet_id),
    FOREIGN KEY (character_id, planet_id) REFERENCES app.planet_colony (character_id, planet_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE app.planet_colony_detail;
DROP TABLE app.planet_colony;
