-- Project HANGAR — Phase 1b: assets.
-- 02_DATABASE_SCHEMA.md §5.2 "Assets (2)" and §5.3 (Phase 1 exit: the
-- single-query recursive tree fixture).

-- +goose Up

-- #1 — verbatim from 02_DATABASE_SCHEMA.md §5.3.
CREATE TABLE app.asset (
    owner_kind        text     NOT NULL,           -- 'character' | 'corporation'
    owner_id          bigint   NOT NULL,
    item_id           bigint   NOT NULL,
    type_id           integer  NOT NULL,
    location_id       bigint   NOT NULL,
    location_type     text     NOT NULL,           -- open vocabulary
    location_flag     text     NOT NULL,           -- open vocabulary
    quantity          bigint   NOT NULL,           -- NOT money
    is_singleton      boolean  NOT NULL,
    is_blueprint_copy boolean,
    name              text,                        -- from .../assets/names
    x                 double precision,            -- position: geometry, not money
    y                 double precision,
    z                 double precision,
    deleted_at        timestamptz,                 -- soft delete: reconciliation, never DELETE
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, item_id)
);
CREATE INDEX ON app.asset (owner_kind, owner_id, location_id) WHERE deleted_at IS NULL;
CREATE INDEX ON app.asset (owner_kind, owner_id, type_id)     WHERE deleted_at IS NULL;

-- #2 — materialised top-level root location per asset, so "assets grouped
-- by station/structure" views (§8.3) do not re-walk the recursive tree on
-- every page render. Recomputed by the sync engine whenever an asset's
-- location_id changes; never authoritative on its own.
CREATE TABLE app.asset_location (
    owner_kind      text     NOT NULL,
    owner_id        bigint   NOT NULL,
    item_id         bigint   NOT NULL,
    root_location_id   bigint   NOT NULL,
    root_location_type text     NOT NULL,   -- open vocabulary: 'station'|'structure'|'solar_system'
    system_id           integer,
    updated_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, item_id),
    FOREIGN KEY (owner_kind, owner_id, item_id) REFERENCES app.asset (owner_kind, owner_id, item_id) ON DELETE CASCADE
);
CREATE INDEX ON app.asset_location (owner_kind, owner_id, root_location_id);

-- +goose Down
DROP TABLE app.asset_location;
DROP TABLE app.asset;
