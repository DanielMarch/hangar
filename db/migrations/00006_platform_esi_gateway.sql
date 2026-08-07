-- Project HANGAR — Phase 1a: ESI gateway and sync metadata.
-- 02_DATABASE_SCHEMA.md §4.3 (#21-#32). app.esi_scope (#22) already exists
-- from 00002.
--
-- SRS v3.1 — B1/B9 correction: app.esi_ledger_bucket and app.esi_ledger_entry
-- are UNLOGGED (as is app.esi_cache_entry). esi_ledger_entry.consumed_at is
-- the RESPONSE timestamp, never the issue timestamp — reservations carry the
-- issue time in consumed_at plus expires_at and are re-stamped to the
-- response time on settle (defect B9). UNLOGGED tables are not physically
-- replicated by Postgres streaming replication; losing them on crash or on a
-- replica promotion costs one conservative re-reconciliation pass from the
-- next response's rate-limit headers, never a correctness failure — do NOT
-- make these tables LOGGED to "fix" that, per the roadmap's explicit
-- instruction.
--
-- `goose down` drops app.esi_ledger_entry before app.esi_ledger_bucket: the
-- FK ON DELETE CASCADE covers runtime deletes, not migration teardown order.

-- +goose Up

-- #21 — the route catalogue. Verbatim from 02_DATABASE_SCHEMA.md §4.3.
CREATE TABLE app.esi_route (
    route_id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    operation_id        text        NOT NULL UNIQUE,
    method              text        NOT NULL,
    -- VERBATIM from the spec. Never derived, never pluralised (§4.1.2).
    -- e.g. '/corporation/{corporation_id}/mining/extractions'  ← singular, deliberately
    upstream_path       text        NOT NULL,
    cache_age           interval,                       -- x-cache-age; NULL = not declared
    cache_mode          text,                           -- x-cache-mode; NULL ⇒ 'ttl-based'
    rate_limit_group    text,                           -- x-rate-limit.group
    rate_limit_max      integer,                        -- x-rate-limit.max-tokens
    rate_limit_window   interval,                       -- x-rate-limit.window-size
    pagination_style    text,                           -- x-pagination: 'cursor' | 'page' | NULL
    compatibility_date  date        NOT NULL,           -- x-compatibility-date
    blocked_by_pin      boolean     NOT NULL DEFAULT false,
    spec_fragment       jsonb       NOT NULL,           -- the raw operation object
    identifier_types    jsonb       NOT NULL,           -- {"corporation_id":"bigint","project_id":"uuid"}
    first_seen_at       timestamptz NOT NULL DEFAULT now(),
    retired_at          timestamptz,                    -- vanished from the spec; NEVER deleted
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (method, upstream_path, compatibility_date)
);
CREATE INDEX ON app.esi_route (blocked_by_pin) WHERE blocked_by_pin;
CREATE INDEX ON app.esi_route (rate_limit_group);

-- #23
CREATE TABLE app.esi_route_scope (
    route_id uuid NOT NULL REFERENCES app.esi_route(route_id) ON DELETE CASCADE,
    scope    text NOT NULL REFERENCES app.esi_scope(scope),
    PRIMARY KEY (route_id, scope)
);
CREATE INDEX ON app.esi_route_scope (scope);

-- #24 — from x-required-roles; role is an open vocabulary (CCP corp role names).
CREATE TABLE app.esi_route_role (
    route_id uuid NOT NULL REFERENCES app.esi_route(route_id) ON DELETE CASCADE,
    role     text NOT NULL,
    PRIMARY KEY (route_id, role)
);

-- #25 — every pin advance: old, new, actor, route diff.
CREATE TABLE app.esi_pin_history (
    pin_id      uuid        PRIMARY KEY DEFAULT uuidv7(),
    old_pin     date,
    new_pin     date        NOT NULL,
    actor       text        NOT NULL,             -- 'operator:<user_id>' | 'system:auto-advance'
    route_diff  jsonb       NOT NULL DEFAULT '{}',
    advanced_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.esi_pin_history (advanced_at DESC);

-- #26 — UNLOGGED L2 conditional cache. Never a source of truth; losing it on
-- crash costs one revalidation round.
CREATE UNLOGGED TABLE app.esi_cache_entry (
    cache_key     bytea       PRIMARY KEY,        -- sha256 over the §5.3 tuple
    etag          text,
    last_modified timestamptz,
    body          bytea       NOT NULL,
    status        smallint    NOT NULL,
    expires_at    timestamptz NOT NULL,
    stored_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.esi_cache_entry (expires_at);

-- #27 — installation-wide Governor 2 state, one row.
CREATE TABLE app.esi_error_budget (
    id           smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    window_start timestamptz NOT NULL DEFAULT now(),
    error_count  integer     NOT NULL DEFAULT 0,
    paused       boolean     NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- #28 — UNLOGGED. Governor 1 bucket config + reconciliation state; the row
-- lock that serialises acquire for exactly this (group, user_key).
CREATE UNLOGGED TABLE app.esi_ledger_bucket (
    rate_limit_group   text        NOT NULL,
    -- 'applicationID:characterID' on authenticated routes,
    -- 'sourceIP' or 'sourceIP:applicationID' on unauthenticated ones.
    user_key           text        NOT NULL,
    max_tokens         integer     NOT NULL,
    "window"           interval    NOT NULL,  -- quoted: `window` is a reserved word in Postgres
    -- Last authoritative server reading. The server always wins.
    server_remaining   integer,
    server_observed_at timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (rate_limit_group, user_key)
);

-- #29 — UNLOGGED. The (cost, consumed_at) entries themselves.
CREATE UNLOGGED TABLE app.esi_ledger_entry (
    entry_id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    rate_limit_group text        NOT NULL,
    user_key         text        NOT NULL,
    cost             smallint    NOT NULL,   -- 429 ⇒ 0; transport error ⇒ 5
    -- RESPONSE timestamp, not issue timestamp (SRS v3.1 §4.1.3, defect B9).
    -- Reservations carry the issue time here and are re-stamped on settle.
    consumed_at      timestamptz NOT NULL,
    state            text        NOT NULL,   -- 'reserved' | 'settled' | 'synthetic'
    expires_at       timestamptz,            -- reservations only: the request timeout
    FOREIGN KEY (rate_limit_group, user_key)
        REFERENCES app.esi_ledger_bucket (rate_limit_group, user_key) ON DELETE CASCADE
);
CREATE INDEX ON app.esi_ledger_entry (rate_limit_group, user_key, consumed_at);

-- #30 — heartbeat registry; selects `solo` vs `clustered` (§5.6). Written to
-- since Phase 0 by internal/telemetry/replica.go, which was inert until this
-- migration created the table it was guarding on.
CREATE TABLE app.esi_replica (
    replica_id     uuid        PRIMARY KEY,
    role           text        NOT NULL,      -- 'serve' | 'work' | 'schedule'
    version        text        NOT NULL,
    started_at     timestamptz NOT NULL,
    last_heartbeat timestamptz NOT NULL
);
CREATE INDEX ON app.esi_replica (last_heartbeat);

-- #31 — verbatim from 02_DATABASE_SCHEMA.md §4.3.
CREATE TABLE app.sync_subscription (
    subscription_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    entity_kind         text        NOT NULL,       -- 'character' | 'corporation' | 'alliance' | 'global'
    entity_id           bigint      NOT NULL,       -- 0 for global routes
    route_id             uuid       NOT NULL REFERENCES app.esi_route(route_id),
    enabled              boolean    NOT NULL DEFAULT true,
    acting_character_id bigint,                     -- elected director for corp routes (§6.3)
    next_due_at          timestamptz NOT NULL DEFAULT now(),
    last_success_at      timestamptz,
    last_status          smallint,
    etag                 text,
    last_modified        timestamptz,
    cursor_after         text,                       -- opaque; never parsed
    consecutive_304      integer     NOT NULL DEFAULT 0,   -- drives 1.5^n backoff
    consecutive_403      integer     NOT NULL DEFAULT 0,   -- drives entity breaker + re-election
    snoozed_until        timestamptz,                -- set from Retry-After on 429
    opt_in_no_cache      boolean     NOT NULL DEFAULT false,
    UNIQUE (entity_kind, entity_id, route_id)
);
-- SPEC DEFECT (02_DATABASE_SCHEMA.md §4.3): the illustrative index predicate
-- `WHERE enabled AND (snoozed_until IS NULL OR snoozed_until < now())` does
-- not build — `now()` is STABLE, not IMMUTABLE, and Postgres rejects any
-- non-immutable function in an index predicate (42P17). Reported rather than
-- silently reshaped: the intent (skip disabled/snoozed rows cheaply) is kept
-- by partialing on `enabled` alone, which IS immutable-safe; the planner
-- filters the small `snoozed_until` remainder from the already-narrow result
-- with a normal predicate, which is what the 5-second claim loop's query
-- does. See db/queries/sync_subscription.sql (ClaimDueSubscriptions).
CREATE INDEX ON app.sync_subscription (next_due_at) WHERE enabled;
CREATE INDEX ON app.sync_subscription (snoozed_until) WHERE snoozed_until IS NOT NULL;

-- #32 — per-attempt outcome; the `_sync` envelope source.
CREATE TABLE app.sync_run (
    run_id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    subscription_id uuid        NOT NULL REFERENCES app.sync_subscription(subscription_id) ON DELETE CASCADE,
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    status          smallint,
    outcome         text,              -- open vocabulary: '304'|'200'|'429'|'error'|...
    error           text,
    rows_affected   integer
);
CREATE INDEX ON app.sync_run (subscription_id, started_at DESC);

-- +goose Down
DROP TABLE app.sync_run;
DROP TABLE app.sync_subscription;
DROP TABLE app.esi_replica;
DROP TABLE app.esi_ledger_entry;   -- before esi_ledger_bucket — FK cascade covers runtime, not migration order
DROP TABLE app.esi_ledger_bucket;
DROP TABLE app.esi_error_budget;
DROP TABLE app.esi_cache_entry;
DROP TABLE app.esi_pin_history;
DROP TABLE app.esi_route_role;
DROP TABLE app.esi_route_scope;
DROP TABLE app.esi_route;
