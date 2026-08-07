-- Project HANGAR — Phase 1a: events and webhooks.
-- 02_DATABASE_SCHEMA.md §4.6 (#44-#46).

-- +goose Up

-- #44 — HMAC secret is envelope-encrypted at rest, same scheme as
-- app.character_token (wrapped DEK + nonce + ciphertext, AAD = endpoint_id ‖
-- key_version ‖ 'webhook_secret').
CREATE TABLE app.webhook_endpoint (
    endpoint_id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    owner_user_id           uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    url                     text        NOT NULL,
    hmac_key_version        int         NOT NULL,
    hmac_wrapped_dek        bytea       NOT NULL,
    hmac_nonce              bytea       NOT NULL,
    hmac_ciphertext         bytea       NOT NULL,
    event_filter            text[]      NOT NULL DEFAULT '{}',
    enabled                 boolean     NOT NULL DEFAULT true,
    created_at              timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.webhook_endpoint (owner_user_id);

-- #45 — uuidv7()-keyed so the dispatcher reads in causal order with an
-- index-only scan. Written in the same transaction as the data mutation it
-- announces (Phase 19 exit criterion).
CREATE TABLE app.outbox_event (
    event_id      uuid        PRIMARY KEY DEFAULT uuidv7(),
    aggregate     text        NOT NULL,     -- e.g. 'character' | 'corporation' | 'wallet_journal'
    aggregate_id  text        NOT NULL,
    event_type    text        NOT NULL,     -- HANGAR domain event vocabulary, defined incrementally by phase
    payload       jsonb       NOT NULL,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    dispatched_at timestamptz
);
CREATE INDEX ON app.outbox_event (event_id) WHERE dispatched_at IS NULL;

-- #46 — attempts, response status, next retry.
CREATE TABLE app.webhook_delivery (
    delivery_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    endpoint_id     uuid        NOT NULL REFERENCES app.webhook_endpoint(endpoint_id) ON DELETE CASCADE,
    event_id        uuid        NOT NULL REFERENCES app.outbox_event(event_id) ON DELETE CASCADE,
    attempt         integer     NOT NULL DEFAULT 0,
    response_status smallint,
    next_retry_at   timestamptz,
    delivered_at    timestamptz,
    error           text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.webhook_delivery (endpoint_id);
CREATE INDEX ON app.webhook_delivery (next_retry_at) WHERE delivered_at IS NULL;

-- +goose Down
DROP TABLE app.webhook_delivery;
DROP TABLE app.outbox_event;
DROP TABLE app.webhook_endpoint;
