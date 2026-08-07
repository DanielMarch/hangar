-- Project HANGAR — Phase 1b: contracts.
-- 02_DATABASE_SCHEMA.md §5.2 "Contracts (3)". `contract` is owner-polymorphic
-- (both /characters/{id}/contracts and /corporations/{id}/contracts exist);
-- `type`, `status`, `availability` are CCP vocabularies and stay `text`
-- (Principle 14) rather than the legacy `enum` columns.

-- +goose Up

-- #1
CREATE TABLE app.contract (
    owner_kind             text          NOT NULL,   -- 'character' | 'corporation'
    owner_id               bigint        NOT NULL,
    contract_id            bigint        NOT NULL,
    issuer_id               bigint        NOT NULL,
    issuer_corporation_id    bigint        NOT NULL,
    assignee_id               bigint,
    acceptor_id                 bigint,
    start_location_id            bigint,
    end_location_id                bigint,
    type                              text          NOT NULL,   -- open vocabulary
    status                            text          NOT NULL,   -- open vocabulary
    title                             text,
    for_corporation                   boolean       NOT NULL DEFAULT false,
    availability                      text          NOT NULL,   -- open vocabulary
    date_issued                       timestamptz   NOT NULL,
    date_expired                      timestamptz   NOT NULL,
    date_accepted                     timestamptz,
    days_to_complete                  integer,
    date_completed                    timestamptz,
    price                             numeric(30,2),             -- money
    reward                            numeric(30,2),             -- money
    collateral                        numeric(30,2),             -- money
    buyout                            numeric(30,2),             -- money
    volume                            double precision,           -- m3, NOT money
    updated_at                        timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, contract_id)
);
CREATE INDEX ON app.contract (owner_kind, owner_id, status);
CREATE INDEX ON app.contract (owner_kind, owner_id, date_issued DESC);
CREATE INDEX ON app.contract (assignee_id);
CREATE INDEX ON app.contract (acceptor_id);

-- #2
CREATE TABLE app.contract_item (
    owner_kind    text    NOT NULL,
    owner_id      bigint  NOT NULL,
    contract_id   bigint  NOT NULL,
    record_id     bigint  NOT NULL,
    type_id       integer NOT NULL,
    quantity      bigint  NOT NULL,   -- NOT money
    raw_quantity  bigint,
    is_singleton  boolean NOT NULL,
    is_included   boolean NOT NULL,
    is_blueprint_copy boolean,
    material_efficiency smallint,
    time_efficiency      smallint,
    runs                    integer,   -- NOT money
    PRIMARY KEY (owner_kind, owner_id, contract_id, record_id),
    FOREIGN KEY (owner_kind, owner_id, contract_id) REFERENCES app.contract (owner_kind, owner_id, contract_id) ON DELETE CASCADE
);

-- #3
CREATE TABLE app.contract_bid (
    owner_kind  text        NOT NULL,
    owner_id    bigint      NOT NULL,
    contract_id bigint      NOT NULL,
    bid_id      bigint      NOT NULL,
    bidder_id   bigint      NOT NULL,
    date_bid    timestamptz NOT NULL,
    amount      numeric(30,2) NOT NULL,   -- money
    PRIMARY KEY (owner_kind, owner_id, contract_id, bid_id),
    FOREIGN KEY (owner_kind, owner_id, contract_id) REFERENCES app.contract (owner_kind, owner_id, contract_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE app.contract_bid;
DROP TABLE app.contract_item;
DROP TABLE app.contract;
