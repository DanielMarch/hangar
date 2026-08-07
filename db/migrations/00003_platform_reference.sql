-- Project HANGAR — Phase 1a: shared reference data.
-- 02_DATABASE_SCHEMA.md §4.7 (#47 corporation, #48 alliance, #49 location,
-- #51 sde_import). app.open_vocabulary (#50) lives in 00002.
--
-- app.corporation and app.alliance have a mutual reference (a corporation's
-- alliance_id and an alliance's executor_corporation_id). Both tables are
-- created without that one FK each, then both FKs are added once both
-- tables exist — the same pattern used for app.user / app.character in
-- 00004.

-- +goose Up

-- #48
CREATE TABLE app.alliance (
    alliance_id             bigint        PRIMARY KEY,   -- ESI-typed int64 identifier
    name                    text          NOT NULL,
    ticker                  text,
    creator_id              bigint,
    creator_corporation_id  bigint,
    executor_corporation_id bigint,       -- FK added below, after app.corporation exists
    date_founded            timestamptz,
    faction_id              integer,
    updated_at              timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX ON app.alliance (executor_corporation_id);

-- #47
CREATE TABLE app.corporation (
    corporation_id  bigint          PRIMARY KEY,  -- ESI-typed int64 identifier
    name            text            NOT NULL,
    ticker          text            NOT NULL,
    member_count    integer,
    ceo_id          bigint,
    alliance_id     bigint          REFERENCES app.alliance(alliance_id),
    description     text,
    tax_rate        double precision,             -- fraction of income, NOT money (Principle 9)
    date_founded    timestamptz,
    creator_id      bigint,
    url             text,
    faction_id      integer,
    home_station_id bigint,
    shares          bigint,
    war_eligible    boolean,
    -- CCP's 2026-07-21 corporation palette/branding addition (colours, badge
    -- layout). Not yet individually typed by the route catalogue (Phase 2);
    -- captured verbatim per Principle 14 rather than dropped or rejected.
    palette         jsonb,
    updated_at      timestamptz     NOT NULL DEFAULT now()
);
CREATE INDEX ON app.corporation (alliance_id);
CREATE INDEX ON app.corporation (ceo_id);

ALTER TABLE app.alliance
    ADD CONSTRAINT fk_alliance_executor_corporation
    FOREIGN KEY (executor_corporation_id) REFERENCES app.corporation(corporation_id);

-- #49 — resolved stations / structures / solar systems, keyed by CCP's own
-- location_type + location_id pair (open vocabulary; see app.asset in
-- Phase 1b for the consumer).
CREATE TABLE app.location (
    location_type text        NOT NULL,   -- open vocabulary: 'station'|'structure'|'solar_system'|...
    location_id   bigint      NOT NULL,
    name          text,
    system_id     integer,
    owner_id      bigint,                 -- corporation_id for player-owned structures
    type_id       integer,
    resolved_at   timestamptz,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (location_type, location_id)
);
CREATE INDEX ON app.location (system_id);

-- #51 — atomic-swap bookkeeping for the sde/sde_next rename (02_… §6).
CREATE TABLE app.sde_import (
    import_id    uuid        PRIMARY KEY DEFAULT uuidv7(),
    started_at   timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    status       text        NOT NULL DEFAULT 'running', -- open vocabulary: running|verified|swapped|failed
    checksum     text,
    row_counts   jsonb       NOT NULL DEFAULT '{}',
    error        text
);
CREATE INDEX ON app.sde_import (started_at DESC);

-- +goose Down
DROP TABLE app.sde_import;
DROP TABLE app.location;
ALTER TABLE app.alliance DROP CONSTRAINT fk_alliance_executor_corporation;
DROP TABLE app.corporation;
DROP TABLE app.alliance;
