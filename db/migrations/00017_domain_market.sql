-- Project HANGAR — Phase 1b: market.
-- 02_DATABASE_SCHEMA.md §5.2 "Market (4)". `market_order` and
-- `market_order_history` are owner-polymorphic (character and corporation
-- orders/history both exist). `market_history` and `market_price` are
-- global (region/type, not owned by a character or corporation) — the
-- former is PARTITION BY RANGE (date), monthly (§3.4).

-- +goose Up

-- #1
CREATE TABLE app.market_order (
    owner_kind      text          NOT NULL,   -- 'character' | 'corporation'
    owner_id        bigint        NOT NULL,
    order_id        bigint        NOT NULL,
    type_id         integer       NOT NULL,
    region_id       integer       NOT NULL,
    location_id     bigint        NOT NULL,
    range           text          NOT NULL,   -- open vocabulary: '1'..'40'|'station'|'region'|'solarsystem'
    is_buy_order    boolean       NOT NULL DEFAULT false,
    is_corporation  boolean       NOT NULL DEFAULT false,
    escrow          numeric(30,2),            -- money
    price           numeric(30,2) NOT NULL,   -- money
    volume_total    bigint        NOT NULL,   -- NOT money
    volume_remain   bigint        NOT NULL,   -- NOT money
    min_volume      bigint,                   -- NOT money
    duration        integer       NOT NULL,
    issued          timestamptz   NOT NULL,
    wallet_division smallint,
    updated_at      timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, order_id)
);
CREATE INDEX ON app.market_order (owner_kind, owner_id, type_id);
CREATE INDEX ON app.market_order (region_id);

-- #2
CREATE TABLE app.market_order_history (
    owner_kind      text          NOT NULL,
    owner_id        bigint        NOT NULL,
    order_id        bigint        NOT NULL,
    type_id         integer       NOT NULL,
    region_id       integer       NOT NULL,
    location_id     bigint        NOT NULL,
    range           text          NOT NULL,
    is_buy_order    boolean       NOT NULL DEFAULT false,
    is_corporation  boolean       NOT NULL DEFAULT false,
    escrow          numeric(30,2),
    price           numeric(30,2) NOT NULL,
    volume_total    bigint        NOT NULL,
    volume_remain   bigint        NOT NULL,
    min_volume      bigint,
    duration        integer       NOT NULL,
    issued          timestamptz   NOT NULL,
    state           text          NOT NULL,   -- open vocabulary: 'cancelled'|'expired'
    wallet_division smallint,
    updated_at      timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, order_id)
);

-- #3 — verbatim shape of §3.4/§5.3's partitioning pattern applied to daily
-- region/type price history.
CREATE TABLE app.market_history (
    region_id    integer       NOT NULL,
    type_id      integer       NOT NULL,
    date         date          NOT NULL,
    average      numeric(30,2),   -- money
    highest      numeric(30,2),   -- money
    lowest       numeric(30,2),   -- money
    order_count  bigint        NOT NULL,   -- NOT money
    volume       bigint        NOT NULL,   -- NOT money
    PRIMARY KEY (region_id, type_id, date)   -- partition key in the PK
) PARTITION BY RANGE (date);

CREATE TABLE app.market_history_default PARTITION OF app.market_history DEFAULT;

CREATE INDEX ON app.market_history USING brin (date);

-- #4 — global adjusted/average prices per type (/markets/prices).
CREATE TABLE app.market_price (
    type_id        integer       NOT NULL PRIMARY KEY,
    adjusted_price numeric(30,2),   -- money
    average_price  numeric(30,2),   -- money
    updated_at     timestamptz   NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app.market_price;
DROP TABLE app.market_history_default;
DROP TABLE app.market_history;
DROP TABLE app.market_order_history;
DROP TABLE app.market_order;
