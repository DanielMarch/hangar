-- Project HANGAR — Phase 1b: wallets.
-- 02_DATABASE_SCHEMA.md §5.2 "Wallets (3)" and §5.3 (wallet_journal
-- verbatim). wallet_journal and wallet_transaction are PARTITION BY RANGE
-- (date), monthly (§3.4): the partition key is inside every PK, a DEFAULT
-- partition exists from this migration, and a BRIN index on the time column
-- is created on the parent so it propagates to every partition, present and
-- future. `division` is 1-7 for corporations, always 1 for characters.

-- +goose Up

-- #1
CREATE TABLE app.wallet_balance (
    owner_kind  text          NOT NULL,   -- 'character' | 'corporation'
    owner_id    bigint        NOT NULL,
    division    smallint      NOT NULL DEFAULT 1,
    balance     numeric(30,2) NOT NULL,   -- money
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_kind, owner_id, division)
);

-- #2 — verbatim from 02_DATABASE_SCHEMA.md §5.3.
CREATE TABLE app.wallet_journal (
    owner_kind      text          NOT NULL,
    owner_id        bigint        NOT NULL,
    division        smallint      NOT NULL DEFAULT 1,   -- 1 for characters
    journal_id      bigint        NOT NULL,
    ref_type        text          NOT NULL,             -- OPEN vocabulary → app.open_vocabulary
    amount          numeric(30,2),                      -- Principle 9
    balance         numeric(30,2),
    tax             numeric(30,2),
    tax_receiver_id bigint,
    first_party_id  bigint,
    second_party_id bigint,
    context_id      bigint,
    context_id_type text,
    reason          text,
    description     text          NOT NULL,
    date            timestamptz   NOT NULL,
    PRIMARY KEY (owner_kind, owner_id, journal_id, date)  -- partition key in the PK
) PARTITION BY RANGE (date);

CREATE TABLE app.wallet_journal_default PARTITION OF app.wallet_journal DEFAULT;

CREATE INDEX ON app.wallet_journal USING brin (date);
CREATE INDEX ON app.wallet_journal (owner_kind, owner_id, date DESC);

-- #3 — same partitioning shape as #2.
CREATE TABLE app.wallet_transaction (
    owner_kind      text          NOT NULL,
    owner_id        bigint        NOT NULL,
    division        smallint      NOT NULL DEFAULT 1,
    transaction_id  bigint        NOT NULL,
    client_id       bigint,
    date            timestamptz   NOT NULL,
    is_buy          boolean       NOT NULL,
    is_personal     boolean,
    journal_ref_id  bigint,
    location_id     bigint        NOT NULL,
    quantity        bigint        NOT NULL,             -- NOT money
    type_id         integer       NOT NULL,
    unit_price      numeric(30,2) NOT NULL,              -- money
    PRIMARY KEY (owner_kind, owner_id, transaction_id, date)
) PARTITION BY RANGE (date);

CREATE TABLE app.wallet_transaction_default PARTITION OF app.wallet_transaction DEFAULT;

CREATE INDEX ON app.wallet_transaction USING brin (date);
CREATE INDEX ON app.wallet_transaction (owner_kind, owner_id, date DESC);

-- +goose Down
DROP TABLE app.wallet_transaction_default;
DROP TABLE app.wallet_transaction;
DROP TABLE app.wallet_journal_default;
DROP TABLE app.wallet_journal;
DROP TABLE app.wallet_balance;
