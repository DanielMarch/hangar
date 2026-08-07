-- Project HANGAR — Phase 1b: corporation structure.
-- 02_DATABASE_SCHEMA.md §5.2 "Corporation structure (16)".
-- All 16 tables here are inherently corporation-scoped concepts (no
-- character equivalent exists in the ESI contract), so none use owner
-- polymorphism — that mechanism is reserved for the eleven concepts that
-- genuinely exist for both owners (§5.1).

-- +goose Up

-- #1
CREATE TABLE app.corporation_member (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    character_id   bigint      NOT NULL REFERENCES app.character(character_id),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, character_id)
);
CREATE INDEX ON app.corporation_member (character_id);

-- #2
CREATE TABLE app.corporation_member_tracking (
    corporation_id    bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    character_id      bigint      NOT NULL REFERENCES app.character(character_id),
    base_id           bigint,
    location_id       bigint,
    logoff_date       timestamptz,
    logon_date        timestamptz,
    ship_type_id      integer,
    start_date        timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, character_id)
);

-- #3
CREATE TABLE app.corporation_title (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    title_id       bigint      NOT NULL,
    name           text        NOT NULL,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, title_id)
);

-- #4
CREATE TABLE app.corporation_member_title (
    corporation_id bigint NOT NULL,
    title_id       bigint NOT NULL,
    character_id   bigint NOT NULL REFERENCES app.character(character_id),
    PRIMARY KEY (corporation_id, title_id, character_id),
    FOREIGN KEY (corporation_id, title_id) REFERENCES app.corporation_title(corporation_id, title_id) ON DELETE CASCADE
);
CREATE INDEX ON app.corporation_member_title (character_id);

-- #5 — role text is open vocabulary (x-required-roles / character roles).
CREATE TABLE app.corporation_role (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    character_id   bigint      NOT NULL REFERENCES app.character(character_id),
    role           text        NOT NULL,
    grantable      boolean     NOT NULL DEFAULT false,
    at_hq          boolean     NOT NULL DEFAULT false,
    at_base        boolean     NOT NULL DEFAULT false,
    at_other       boolean     NOT NULL DEFAULT false,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, character_id, role, grantable, at_hq, at_base, at_other)
);
CREATE INDEX ON app.corporation_role (character_id);

-- #6
CREATE TABLE app.corporation_role_history (
    corporation_id  bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    record_id       bigint      NOT NULL,
    character_id    bigint      NOT NULL,
    changed_at      timestamptz NOT NULL,
    issuer_id       bigint      NOT NULL,
    role_type       text        NOT NULL,   -- open vocabulary: 'grantable_roles'|'roles'|...
    old_roles       text[]      NOT NULL DEFAULT '{}',
    new_roles       text[]      NOT NULL DEFAULT '{}',
    PRIMARY KEY (corporation_id, record_id)
);
CREATE INDEX ON app.corporation_role_history (corporation_id, character_id, changed_at DESC);

-- #7
CREATE TABLE app.corporation_division (
    corporation_id bigint  NOT NULL REFERENCES app.corporation(corporation_id),
    division_kind  text    NOT NULL,   -- 'wallet' | 'hangar'
    division       smallint NOT NULL,  -- 1-7
    name           text,
    PRIMARY KEY (corporation_id, division_kind, division)
);

-- #8
CREATE TABLE app.corporation_shareholder (
    corporation_id     bigint NOT NULL REFERENCES app.corporation(corporation_id),
    shareholder_id     bigint NOT NULL,
    shareholder_type   text   NOT NULL,   -- open vocabulary: 'character' | 'corporation'
    share_count        bigint NOT NULL,   -- NOT money
    PRIMARY KEY (corporation_id, shareholder_id, shareholder_type)
);

-- #9
CREATE TABLE app.corporation_facility (
    corporation_id bigint NOT NULL REFERENCES app.corporation(corporation_id),
    facility_id    bigint NOT NULL,
    system_id      integer,
    type_id        integer,
    PRIMARY KEY (corporation_id, facility_id)
);

-- #10
CREATE TABLE app.corporation_customs_office (
    corporation_id      bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    office_id           bigint      NOT NULL,
    system_id           integer,
    reinforce_exit_start smallint,
    reinforce_exit_end   smallint,
    allow_access_with_standings boolean,
    allow_alliance_access       boolean,
    standing_level              text,   -- open vocabulary
    alliance_tax_rate            double precision,  -- a rate, not money
    corporation_tax_rate         double precision,
    excellent_standing_tax_rate  double precision,
    good_standing_tax_rate       double precision,
    neutral_standing_tax_rate    double precision,
    terrible_standing_tax_rate   double precision,
    bad_standing_tax_rate        double precision,
    updated_at                   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, office_id)
);

-- #11
CREATE TABLE app.corporation_container_log (
    corporation_id  bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    log_id          bigserial,
    logged_at        timestamptz NOT NULL,
    action           text        NOT NULL,   -- open vocabulary
    character_id     bigint      NOT NULL,
    container_id     bigint      NOT NULL,
    container_type_id integer,
    location_id       bigint,
    location_flag     text,
    new_config_bitmask integer,
    old_config_bitmask  integer,
    password_type       text,
    quantity             bigint,   -- NOT money
    type_id               integer,
    PRIMARY KEY (corporation_id, log_id)
);
CREATE INDEX ON app.corporation_container_log (corporation_id, logged_at DESC);

-- #12
CREATE TABLE app.corporation_structure (
    corporation_id       bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    structure_id         bigint      NOT NULL,
    type_id              integer     NOT NULL,
    system_id             integer     NOT NULL,
    profile_id             integer,
    fuel_expires             timestamptz,
    state                     text,       -- open vocabulary
    state_timer_start          timestamptz,
    state_timer_end             timestamptz,
    unanchors_at                  timestamptz,
    reinforce_hour                 smallint,
    next_reinforce_apply             timestamptz,
    next_reinforce_hour               smallint,
    services                            jsonb NOT NULL DEFAULT '[]',
    updated_at                          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, structure_id)
);

-- #13
CREATE TABLE app.corporation_starbase (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    starbase_id    bigint      NOT NULL,
    type_id        integer     NOT NULL,
    system_id      integer     NOT NULL,
    moon_id        bigint,
    onlined_since  timestamptz,
    reinforced_until timestamptz,
    state             text,   -- open vocabulary
    unanchor_at        timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, starbase_id)
);

-- #14 — verbatim from 02_DATABASE_SCHEMA.md §5.3 ("Starbase detail").
CREATE TABLE app.starbase_detail (
    corporation_id  bigint      NOT NULL,
    starbase_id     bigint      NOT NULL,
    system_id       integer     NOT NULL,
    state           text,                              -- open vocabulary
    fuel_bay_view   text,                              -- role names, open vocabulary
    allow_alliance_members boolean,
    allow_corporation_members boolean,
    use_alliance_standings boolean,
    attack_standing_threshold double precision,
    -- The fuel bay. app.alert_type('corporation.starbase.fuel_low').source_route_id
    -- points at /corporations/{id}/starbases/{starbase_id}, and the build-time check
    -- proves that route is in the sync set.
    fuels           jsonb       NOT NULL DEFAULT '[]', -- [{type_id, quantity}]
    reinforced_until timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, starbase_id),
    FOREIGN KEY (corporation_id, starbase_id) REFERENCES app.corporation_starbase(corporation_id, starbase_id)
);

-- #15
CREATE TABLE app.corporation_skyhook (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    skyhook_id     bigint      NOT NULL,
    type_id        integer     NOT NULL,
    system_id      integer     NOT NULL,
    planet_id      bigint,
    state          text,       -- open vocabulary
    fuel_expires   timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, skyhook_id)
);

-- #16
CREATE TABLE app.corporation_sovereignty_hub (
    corporation_id bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    hub_id         bigint      NOT NULL,
    type_id        integer     NOT NULL,
    system_id      integer     NOT NULL,
    fuel_expires   timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (corporation_id, hub_id)
);

-- +goose Down
DROP TABLE app.corporation_sovereignty_hub;
DROP TABLE app.corporation_skyhook;
DROP TABLE app.starbase_detail;
DROP TABLE app.corporation_starbase;
DROP TABLE app.corporation_structure;
DROP TABLE app.corporation_container_log;
DROP TABLE app.corporation_customs_office;
DROP TABLE app.corporation_facility;
DROP TABLE app.corporation_shareholder;
DROP TABLE app.corporation_division;
DROP TABLE app.corporation_role_history;
DROP TABLE app.corporation_role;
DROP TABLE app.corporation_member_title;
DROP TABLE app.corporation_title;
DROP TABLE app.corporation_member_tracking;
DROP TABLE app.corporation_member;
