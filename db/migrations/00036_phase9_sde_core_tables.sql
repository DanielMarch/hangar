-- Project HANGAR — Phase 9: `sde` schema core tables.
-- 02_DATABASE_SCHEMA.md §6: "Core `sde` tables: type, group, category,
-- region, constellation, solar_system, station, station_operation,
-- market_group, dogma_attribute, dogma_effect, type_dogma_attribute,
-- type_material, blueprint, blueprint_activity, icon, graphic, faction,
-- npc_corporation, race, bloodline, ancestry, skin, moon, planet."
--
-- DDL lives here (Goose), matching §1's "Goose (DDL) + `hangar admin sde
-- import` (data)" split. `internal/sde/import.go` builds `sde_next` by
-- cloning each of these tables' structure with
-- `CREATE TABLE sde_next.<t> (LIKE sde.<t> INCLUDING ALL)` immediately
-- before streaming COPY into it, so the two are never allowed to drift —
-- there is exactly one place the column list is written down.
--
-- SDE JSONL rows vary enormously in shape between table (a `type` row and a
-- `dogma_effect` row share almost nothing), and CCP adds/removes fields
-- between releases without notice — the same open-ended-external-data
-- situation Principle 14 addresses for ESI vocabularies. Rather than
-- hand-modeling every nested field of all 24 tables (a moving target with
-- no stable OpenAPI contract the way ESI has one), each table keeps a
-- small set of columns actually joined against elsewhere in this schema
-- (the FKs §6's "join definitions from SDE" edge case depends on) plus a
-- `data jsonb NOT NULL` column holding the complete decoded JSONL row.
-- `name` is pulled out as plain `text` (the manifest's English name) since
-- every consumer needs it and localized-name jsonb would make every list
-- view pay a JSONB extraction. This is a deliberate scope reduction from
-- "one column per SDE field" and is called out in the Phase 9 report
-- rather than silently modeled as if it were exhaustive.

-- +goose Up

CREATE TABLE sde.category (
    category_id   integer     PRIMARY KEY,
    name          text        NOT NULL,
    published     boolean     NOT NULL DEFAULT true,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.group_ (
    group_id      integer     PRIMARY KEY,
    category_id   integer     NOT NULL,
    name          text        NOT NULL,
    published     boolean     NOT NULL DEFAULT true,
    data          jsonb       NOT NULL
);
CREATE INDEX ON sde.group_ (category_id);

CREATE TABLE sde.market_group (
    market_group_id integer   PRIMARY KEY,
    parent_group_id  integer,
    name             text     NOT NULL,
    data             jsonb    NOT NULL
);

CREATE TABLE sde.type (
    type_id       integer     PRIMARY KEY,
    group_id      integer     NOT NULL,
    market_group_id integer,
    name          text        NOT NULL,
    volume        double precision,
    mass          double precision,
    published     boolean     NOT NULL DEFAULT true,
    data          jsonb       NOT NULL
);
CREATE INDEX ON sde.type (group_id);
CREATE INDEX ON sde.type (market_group_id);

CREATE TABLE sde.region (
    region_id     integer     PRIMARY KEY,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.constellation (
    constellation_id integer  PRIMARY KEY,
    region_id         integer NOT NULL,
    name              text    NOT NULL,
    data              jsonb   NOT NULL
);
CREATE INDEX ON sde.constellation (region_id);

CREATE TABLE sde.solar_system (
    solar_system_id  integer  PRIMARY KEY,
    constellation_id integer  NOT NULL,
    region_id        integer  NOT NULL,
    name             text     NOT NULL,
    security_status  double precision,
    data             jsonb    NOT NULL
);
CREATE INDEX ON sde.solar_system (constellation_id);
CREATE INDEX ON sde.solar_system (region_id);

CREATE TABLE sde.station_operation (
    operation_id  integer     PRIMARY KEY,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.station (
    station_id       bigint    PRIMARY KEY,
    solar_system_id  integer   NOT NULL,
    operation_id     integer,
    name             text      NOT NULL,
    data             jsonb     NOT NULL
);
CREATE INDEX ON sde.station (solar_system_id);

CREATE TABLE sde.planet (
    planet_id        bigint    PRIMARY KEY,
    solar_system_id  integer   NOT NULL,
    type_id          integer,
    name             text      NOT NULL,
    data             jsonb     NOT NULL
);
CREATE INDEX ON sde.planet (solar_system_id);

CREATE TABLE sde.moon (
    moon_id          bigint    PRIMARY KEY,
    planet_id        bigint    NOT NULL,
    solar_system_id  integer   NOT NULL,
    name             text      NOT NULL,
    data             jsonb     NOT NULL
);
CREATE INDEX ON sde.moon (planet_id);
CREATE INDEX ON sde.moon (solar_system_id);

CREATE TABLE sde.dogma_attribute (
    attribute_id  integer     PRIMARY KEY,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.dogma_effect (
    effect_id     integer     PRIMARY KEY,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.type_dogma_attribute (
    type_id       integer     NOT NULL,
    attribute_id  integer     NOT NULL,
    value         double precision NOT NULL,
    PRIMARY KEY (type_id, attribute_id)
);

CREATE TABLE sde.type_material (
    type_id          integer  NOT NULL,
    material_type_id integer  NOT NULL,
    quantity         bigint   NOT NULL,
    PRIMARY KEY (type_id, material_type_id)
);

CREATE TABLE sde.blueprint (
    blueprint_type_id integer PRIMARY KEY,
    max_production_limit integer,
    data                  jsonb NOT NULL
);

CREATE TABLE sde.blueprint_activity (
    blueprint_type_id integer NOT NULL,
    activity           text    NOT NULL,   -- 'manufacturing'|'copying'|'invention'|'research_material'|'research_time'|'reaction'
    time               integer,
    data               jsonb   NOT NULL,
    PRIMARY KEY (blueprint_type_id, activity),
    FOREIGN KEY (blueprint_type_id) REFERENCES sde.blueprint (blueprint_type_id)
);

CREATE TABLE sde.icon (
    icon_id       integer     PRIMARY KEY,
    file_name     text,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.graphic (
    graphic_id    integer     PRIMARY KEY,
    file_name     text,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.faction (
    faction_id    integer     PRIMARY KEY,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.npc_corporation (
    corporation_id bigint     PRIMARY KEY,
    faction_id     integer,
    name           text       NOT NULL,
    data           jsonb      NOT NULL
);
CREATE INDEX ON sde.npc_corporation (faction_id);

CREATE TABLE sde.race (
    race_id       integer     PRIMARY KEY,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

CREATE TABLE sde.bloodline (
    bloodline_id  integer     PRIMARY KEY,
    race_id       integer,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);
CREATE INDEX ON sde.bloodline (race_id);

CREATE TABLE sde.ancestry (
    ancestry_id   integer     PRIMARY KEY,
    bloodline_id  integer,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);
CREATE INDEX ON sde.ancestry (bloodline_id);

CREATE TABLE sde.skin (
    skin_id       integer     PRIMARY KEY,
    type_id       integer,
    name          text        NOT NULL,
    data          jsonb       NOT NULL
);

-- +goose Down
DROP TABLE sde.skin;
DROP TABLE sde.ancestry;
DROP TABLE sde.bloodline;
DROP TABLE sde.race;
DROP TABLE sde.npc_corporation;
DROP TABLE sde.faction;
DROP TABLE sde.graphic;
DROP TABLE sde.icon;
DROP TABLE sde.blueprint_activity;
DROP TABLE sde.blueprint;
DROP TABLE sde.type_material;
DROP TABLE sde.type_dogma_attribute;
DROP TABLE sde.dogma_effect;
DROP TABLE sde.dogma_attribute;
DROP TABLE sde.moon;
DROP TABLE sde.planet;
DROP TABLE sde.station;
DROP TABLE sde.station_operation;
DROP TABLE sde.solar_system;
DROP TABLE sde.constellation;
DROP TABLE sde.region;
DROP TABLE sde.type;
DROP TABLE sde.market_group;
DROP TABLE sde.group_;
DROP TABLE sde.category;
