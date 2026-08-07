-- Project HANGAR — Phase 1b: industry & mining.
-- 02_DATABASE_SCHEMA.md §5.2 "Industry & mining (6)". `industry_job`,
-- `blueprint` and `mining_ledger` are owner-polymorphic (character and
-- corporation variants both exist in §6.2/§6.3); `mining_extraction`,
-- `mining_observer` and `mining_observer_record` are corporation-only
-- concepts (there is no character equivalent in the ESI contract).

-- +goose Up

-- #1
CREATE TABLE app.industry_job (
    owner_kind             text        NOT NULL,   -- 'character' | 'corporation'
    owner_id               bigint      NOT NULL,
    job_id                  bigint      NOT NULL,
    installer_id             bigint      NOT NULL,
    facility_id                bigint      NOT NULL,
    station_id                   bigint      NOT NULL,
    activity_id                    integer     NOT NULL,
    blueprint_id                     bigint      NOT NULL,
    blueprint_type_id                  integer     NOT NULL,
    blueprint_location_id                bigint      NOT NULL,
    output_location_id                     bigint      NOT NULL,
    runs                                     integer     NOT NULL,   -- NOT money
    cost                                     numeric(30,2),           -- money
    licensed_runs                             integer,                -- NOT money
    probability                                double precision,      -- NOT money
    product_type_id                             integer,
    status                                       text        NOT NULL, -- open vocabulary
    duration                                     integer     NOT NULL,
    start_date                                    timestamptz NOT NULL,
    end_date                                       timestamptz NOT NULL,
    pause_date                                      timestamptz,
    completed_date                                   timestamptz,
    completed_character_id                            bigint,
    successful_runs                                    integer,       -- NOT money
    updated_at                                          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, job_id)
);
CREATE INDEX ON app.industry_job (owner_kind, owner_id, status);
CREATE INDEX ON app.industry_job (installer_id);
CREATE INDEX ON app.industry_job (blueprint_id);

-- #2
CREATE TABLE app.blueprint (
    owner_kind        text    NOT NULL,
    owner_id          bigint  NOT NULL,
    item_id           bigint  NOT NULL,
    type_id           integer NOT NULL,
    location_id       bigint  NOT NULL,
    location_flag     text    NOT NULL,   -- open vocabulary
    quantity          bigint  NOT NULL,   -- NOT money; -1 original / -2 copy per ESI convention
    time_efficiency   smallint NOT NULL,
    material_efficiency smallint NOT NULL,
    runs              integer NOT NULL,   -- NOT money
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, item_id)
);

-- #3 — the character's personal mining ledger (/characters/{id}/mining).
CREATE TABLE app.mining_ledger (
    owner_kind  text    NOT NULL,
    owner_id    bigint  NOT NULL,
    date        date    NOT NULL,
    solar_system_id integer NOT NULL,
    type_id         integer NOT NULL,
    quantity        bigint  NOT NULL,   -- NOT money
    PRIMARY KEY (owner_kind, owner_id, date, solar_system_id, type_id)
);

-- #4 — corporation-only: /corporations/{id}/mining/extractions.
CREATE TABLE app.mining_extraction (
    corporation_id  bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    moon_id         bigint      NOT NULL,
    extraction_start_time timestamptz NOT NULL,
    chunk_arrival_time     timestamptz NOT NULL,
    natural_decay_time      timestamptz NOT NULL,
    PRIMARY KEY (corporation_id, moon_id, extraction_start_time)
);

-- #5 — corporation-only: /corporations/{id}/mining/observers.
CREATE TABLE app.mining_observer (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    observer_id    bigint      NOT NULL,
    observer_type  text        NOT NULL,   -- open vocabulary: 'structure'
    last_updated   timestamptz,
    PRIMARY KEY (corporation_id, observer_id)
);

-- #6 — corporation-only: /corporations/{id}/mining/observers/{observer_id}.
CREATE TABLE app.mining_observer_record (
    corporation_id bigint  NOT NULL,
    observer_id    bigint  NOT NULL,
    character_id   bigint  NOT NULL,
    type_id        integer NOT NULL,
    recorded_corporation_id bigint NOT NULL,
    quantity       bigint  NOT NULL,   -- NOT money
    last_updated   timestamptz NOT NULL,
    PRIMARY KEY (corporation_id, observer_id, character_id, type_id),
    FOREIGN KEY (corporation_id, observer_id) REFERENCES app.mining_observer (corporation_id, observer_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE app.mining_observer_record;
DROP TABLE app.mining_observer;
DROP TABLE app.mining_extraction;
DROP TABLE app.mining_ledger;
DROP TABLE app.blueprint;
DROP TABLE app.industry_job;
