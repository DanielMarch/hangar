-- Project HANGAR — Phase 1b: sovereignty.
-- 02_DATABASE_SCHEMA.md §5.2 "Sovereignty (2)". Global concepts (§6.4:
-- /sovereignty/campaigns, /sovereignty/systems) — not owned by a character
-- or corporation, so no owner polymorphism.

-- +goose Up

-- #1
CREATE TABLE app.sovereignty_campaign (
    campaign_id       bigint      NOT NULL PRIMARY KEY,
    constellation_id  integer     NOT NULL,
    solar_system_id   integer     NOT NULL,
    structure_id      bigint,
    defender_id        bigint,
    event_type            text        NOT NULL,   -- open vocabulary: 'tcu_defense'|'ihub_defense'|'station_defense'|'station_freeport'
    start_time               timestamptz NOT NULL,
    attackers_score              double precision,   -- NOT money
    defender_score                  double precision,   -- NOT money
    updated_at                         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.sovereignty_campaign (solar_system_id);

-- #2
CREATE TABLE app.sovereignty_system (
    system_id        integer     NOT NULL PRIMARY KEY,
    alliance_id       bigint,
    corporation_id       bigint,
    faction_id               integer,
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app.sovereignty_system;
DROP TABLE app.sovereignty_campaign;
