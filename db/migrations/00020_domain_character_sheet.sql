-- Project HANGAR — Phase 1b: character sheet.
-- 02_DATABASE_SCHEMA.md §5.2 "Character sheet (11)". Every table here is
-- inherently single-owner (a character's own skills, clone, attributes,
-- ...); none uses owner polymorphism.

-- +goose Up

-- #1
CREATE TABLE app.character_skill (
    character_id  bigint  NOT NULL REFERENCES app.character(character_id),
    skill_id      integer NOT NULL,
    active_level  smallint NOT NULL,
    trained_level smallint NOT NULL,
    skillpoints   bigint  NOT NULL,   -- NOT money
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, skill_id)
);

-- #2
CREATE TABLE app.character_skillqueue (
    character_id     bigint  NOT NULL REFERENCES app.character(character_id),
    queue_position    integer NOT NULL,
    skill_id           integer NOT NULL,
    finished_level        smallint NOT NULL,
    training_start_sp       bigint,
    level_start_sp             bigint,
    level_end_sp                  bigint,
    start_date                      timestamptz,
    finish_date                       timestamptz,
    updated_at                          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, queue_position)
);

-- #3
CREATE TABLE app.character_attributes (
    character_id   bigint      NOT NULL PRIMARY KEY REFERENCES app.character(character_id),
    charisma       integer     NOT NULL,
    intelligence   integer     NOT NULL,
    memory         integer     NOT NULL,
    perception     integer     NOT NULL,
    willpower      integer     NOT NULL,
    bonus_remaps   integer,
    last_remap_date timestamptz,
    accrued_remap_cooldown_date timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- #4
CREATE TABLE app.character_clone (
    character_id     bigint      NOT NULL,
    jump_clone_id     bigint      NOT NULL,
    location_id         bigint      NOT NULL,
    location_type          text        NOT NULL,   -- open vocabulary
    name                      text,
    implants                     integer[]   NOT NULL DEFAULT '{}',
    is_home_clone                  boolean     NOT NULL DEFAULT false,
    last_clone_jump_date              timestamptz,
    last_station_change_date            timestamptz,
    updated_at                            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, jump_clone_id),
    FOREIGN KEY (character_id) REFERENCES app.character(character_id)
);

-- #5
CREATE TABLE app.character_implant (
    character_id bigint  NOT NULL REFERENCES app.character(character_id),
    type_id      integer NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, type_id)
);

-- #6 — /characters/{id}/fatigue.
CREATE TABLE app.character_jump_fatigue (
    character_id            bigint NOT NULL PRIMARY KEY REFERENCES app.character(character_id),
    jump_fatigue_expire_date timestamptz,
    last_jump_date            timestamptz,
    last_update_date             timestamptz,
    updated_at                     timestamptz NOT NULL DEFAULT now()
);

-- #7
CREATE TABLE app.character_loyalty_point (
    character_id  bigint NOT NULL REFERENCES app.character(character_id),
    corporation_id bigint NOT NULL,
    loyalty_points bigint NOT NULL,   -- NOT money
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, corporation_id)
);

-- #8
CREATE TABLE app.character_agent_research (
    character_id      bigint NOT NULL REFERENCES app.character(character_id),
    agent_id            bigint NOT NULL,
    skill_type_id          integer NOT NULL,
    started_at                timestamptz NOT NULL,
    points_per_day               double precision NOT NULL,   -- NOT money
    remainder_points                double precision NOT NULL,   -- NOT money
    updated_at                         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, agent_id)
);

-- #9
CREATE TABLE app.character_title (
    character_id bigint NOT NULL REFERENCES app.character(character_id),
    title_id     bigint NOT NULL,
    name         text   NOT NULL,
    PRIMARY KEY (character_id, title_id)
);

-- #10
CREATE TABLE app.character_role (
    character_id bigint NOT NULL REFERENCES app.character(character_id),
    role         text   NOT NULL,   -- open vocabulary
    grantable    boolean NOT NULL DEFAULT false,
    at_hq        boolean NOT NULL DEFAULT false,
    at_base      boolean NOT NULL DEFAULT false,
    at_other     boolean NOT NULL DEFAULT false,
    PRIMARY KEY (character_id, role, grantable, at_hq, at_base, at_other)
);

-- #11 — /characters/{id}/location, /online, /ship collapsed into one
-- current-state row per character.
CREATE TABLE app.character_location (
    character_id     bigint  NOT NULL PRIMARY KEY REFERENCES app.character(character_id),
    solar_system_id  integer NOT NULL,
    station_id       bigint,
    structure_id     bigint,
    is_online        boolean,
    last_login       timestamptz,
    last_logout      timestamptz,
    logins           bigint,   -- NOT money
    ship_item_id     bigint,
    ship_type_id     integer,
    ship_name        text,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app.character_location;
DROP TABLE app.character_role;
DROP TABLE app.character_title;
DROP TABLE app.character_agent_research;
DROP TABLE app.character_loyalty_point;
DROP TABLE app.character_jump_fatigue;
DROP TABLE app.character_implant;
DROP TABLE app.character_clone;
DROP TABLE app.character_attributes;
DROP TABLE app.character_skillqueue;
DROP TABLE app.character_skill;
