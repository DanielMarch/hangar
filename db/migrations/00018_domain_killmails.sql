-- Project HANGAR — Phase 1b: killmails.
-- 02_DATABASE_SCHEMA.md §5.2 "Killmails (3)". `killmail` is owner-polymorphic
-- (both /characters/{id}/killmails and /corporations/{id}/killmails project
-- into the same table) and PARTITION BY RANGE (killmail_time), monthly.

-- +goose Up

-- #1
CREATE TABLE app.killmail (
    owner_kind        text        NOT NULL,   -- 'character' | 'corporation'
    owner_id          bigint      NOT NULL,
    killmail_id        bigint      NOT NULL,
    killmail_hash        text        NOT NULL,
    killmail_time           timestamptz NOT NULL,
    solar_system_id           integer     NOT NULL,
    moon_id                     bigint,
    war_id                        bigint,
    victim_character_id            bigint,
    victim_corporation_id            bigint,
    victim_alliance_id                 bigint,
    victim_faction_id                   integer,
    victim_ship_type_id                   integer     NOT NULL,
    victim_damage_taken                     bigint      NOT NULL,   -- NOT money
    victim_x                                  double precision,
    victim_y                                    double precision,
    victim_z                                      double precision,
    PRIMARY KEY (owner_kind, owner_id, killmail_id, killmail_time)  -- partition key in the PK
) PARTITION BY RANGE (killmail_time);

CREATE TABLE app.killmail_default PARTITION OF app.killmail DEFAULT;

CREATE INDEX ON app.killmail USING brin (killmail_time);
CREATE INDEX ON app.killmail (owner_kind, owner_id, killmail_time DESC);
-- A UNIQUE index on a partitioned table must include every partitioning
-- column (PG18 enforces this at CREATE time) — killmail_time rides along
-- here for that reason, not because the hash is only unique within a month.
CREATE UNIQUE INDEX ON app.killmail (killmail_id, killmail_hash, killmail_time);

-- #2
CREATE TABLE app.killmail_attacker (
    owner_kind         text    NOT NULL,
    owner_id           bigint  NOT NULL,
    killmail_id        bigint  NOT NULL,
    killmail_time       timestamptz NOT NULL,
    record_id             bigint  NOT NULL,
    character_id             bigint,
    corporation_id              bigint,
    alliance_id                   bigint,
    faction_id                     integer,
    damage_done                      bigint  NOT NULL,   -- NOT money
    final_blow                        boolean NOT NULL,
    security_status                     double precision,
    ship_type_id                          integer,
    weapon_type_id                          integer,
    PRIMARY KEY (owner_kind, owner_id, killmail_id, killmail_time, record_id),
    FOREIGN KEY (owner_kind, owner_id, killmail_id, killmail_time)
        REFERENCES app.killmail (owner_kind, owner_id, killmail_id, killmail_time) ON DELETE CASCADE
);

-- #3
CREATE TABLE app.killmail_item (
    owner_kind         text    NOT NULL,
    owner_id           bigint  NOT NULL,
    killmail_id        bigint  NOT NULL,
    killmail_time       timestamptz NOT NULL,
    record_id             bigint  NOT NULL,
    parent_record_id        bigint,
    type_id                   integer NOT NULL,
    flag                        integer NOT NULL,
    quantity_dropped              bigint,   -- NOT money
    quantity_destroyed              bigint,   -- NOT money
    singleton                         integer,
    PRIMARY KEY (owner_kind, owner_id, killmail_id, killmail_time, record_id),
    FOREIGN KEY (owner_kind, owner_id, killmail_id, killmail_time)
        REFERENCES app.killmail (owner_kind, owner_id, killmail_id, killmail_time) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE app.killmail_item;
DROP TABLE app.killmail_attacker;
DROP TABLE app.killmail_default;
DROP TABLE app.killmail;
