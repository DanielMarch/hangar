# 02 — Database Schema Design Blueprint

**Target:** PostgreSQL 18
**Migrations:** Goose v3, embedded, versioned, plain SQL, forward-only in production
**Access layer:** pgx v5 + sqlc (`sqlc.yaml` at repo root)
**Derived from:** [`00_SRS_v3.1.md`](00_SRS_v3.1.md) §5, §6, Principles 9 / 13 / 14 / 16

> **Scope note.** This document is a *design blueprint*. It contains illustrative DDL so
> that column types, keys and constraints are unambiguous. It is **not** the migration set.
> Phase 1 translates this into numbered Goose files under `db/migrations/`.

---

## 1. Schemas

| Schema | Owner | Contents | Migrated by |
| :-- | :-- | :-- | :-- |
| `app` | HANGAR | everything application-owned | Goose |
| `river` | River | job queue internals (`river_job`, `river_leader`, `river_queue`, …) | River's Go migrator |
| `sde` | HANGAR | Static Data Export definitions | Goose (DDL) + `hangar admin sde import` (data) |
| `sde_next` | HANGAR | transient build target for the SDE atomic swap | created and dropped by the importer |

`hangar migrate up` runs River's migrator first, then Goose. Goose migrations must never
reference `river.*`; project anything needed into `app.sync_run`.

---

## 2. Table count and the Phase 1a / 1b split

SRS v3.0 scoped Phase 1 as "~48 core tables". Projecting the §6 endpoint contract — 106
upstream ESI routes, the six sub-resource endpoints added by P2, the UUID-keyed project tables
and the eight detail tables of §5.2 — yields roughly **129 tables** even with maximal
owner-polymorphism (§3.1 below). The stated 48 covered the platform tier only, and Phase 1 was
not schedulable as written.

**Corrected in SRS v3.1 §5.2:** the schema is ≈129 tables in two tiers, and Phase 1 is split.

| Sub-phase | Tier | Tables | Contents |
| :-- | :-- | :-- | :-- |
| **1a** | Platform | **51** (§4) | identity and access, RBAC and squads, ESI gateway and sync metadata, cluster-shared rate governor state, provisioning, alerting, events, shared reference and open vocabularies |
| **1b** | Domain projections | **≈ 78** (§5) | the ESI datasets behind §6, owner-polymorphic wherever a concept exists for both characters and corporations |

Both sub-phases must land before Phase 2 — the route catalogue writes into 1a and the Phase 7–9
handlers write into 1b. The split exists for review tractability, not for sequencing: reviewing
129 tables of DDL in one pass is how a wrong identifier type gets waved through.

The Tier-1 count rose from 48 to 51 with the F-1 correction, which adds `app.esi_ledger_bucket`,
`app.esi_ledger_entry` and `app.esi_replica` (§4.3).

---

## 3. Design rules

### 3.1 Money is exact — Principle 9

Every money column is `NUMERIC(30,2)`. `NUMERIC(30,2)` holds ±10²⁸ with two decimals, which
covers total ISK in existence with wide margin.

* Go side: `shopspring/decimal.Decimal` via the `sqlc.yaml` override. `pgtype.Numeric` is
  acceptable at the driver boundary but must not escape `internal/store`.
* JSON side: serialised as a **string**, e.g. `"1234567890.12"`.
* `float64` is prohibited on any money path. `make check-money` walks every struct reachable
  from `internal/domain` and `internal/api/dto` by reflection and fails on a `float64` field
  whose name matches the money vocabulary. This is the Phase 1 exit criterion.

Money columns: every `*_isk`, `amount`, `balance`, `price`, `total`, `tax`, `reward`,
`collateral`, `buyout`, `volume_remain`-adjacent value columns, ledger sums.
`quantity`, `volume` (m³) and `runs` are **not** money and stay `bigint` / `numeric`.

### 3.2 Identifiers are typed by the spec — Principle 13

Column type is derived from the OpenAPI schema of the corresponding field. Never assumed.

| OpenAPI schema | Postgres | Go |
| :-- | :-- | :-- |
| `type: integer, format: int64` | `bigint` | `int64` |
| `type: integer, format: int32` | `integer` | `int32` |
| `type: string, format: uuid` | `uuid` | `uuid.UUID` |
| `type: string` (opaque vocabulary) | `text` | `string` |
| `type: string, format: date-time` | `timestamptz` | `time.Time` |
| `type: number` on a money path | `numeric(30,2)` | `decimal.Decimal` |

**Coercion between `bigint` and `uuid` is prohibited in both directions.** Storing a UUID as
`text` "to be flexible" is also prohibited — it defeats indexing and permits malformed values.

**Generation-time check.** `hangar admin verify-identifier-types` reads the ingested spec
(or `internal/esi/catalogue/embedded/openapi.snapshot.json` offline — one path, also used by the
Makefile's `check-identifiers` target), walks every column in `information_schema`
whose name matches `%_id` or is a declared identifier in the mapping registry, and asserts
the Postgres type matches the spec's declared type. A mismatch **fails the build**. This runs
in `make ci`, so a CCP schema change that flips an identifier type surfaces as a red build
rather than a runtime cast panic.

Routes already using UUID identifiers: corporation projects (v1.0), and — post-v1.0 —
freelance jobs, mercenary tactical operations, military campaigns. CCP has stated UUID
identifiers will continue to appear, so the mapping registry must be data, not a switch
statement.

Internal surrogate keys that HANGAR itself mints (outbox rows, alert events, sessions) use
`uuid` generated by PostgreSQL 18's built-in **`uuidv7()`** — time-ordered, so it does not
fragment the index the way `uuidv4()` does. Externally-supplied UUIDs are stored as given and
never regenerated.

### 3.3 Vocabularies are open — Principle 14

External enumerations (`ref_type`, `location_type`, notification `type`, scope strings,
`x-cache-mode` values, contract `status`, …) are `text`. **No PostgreSQL `ENUM` for external
data** — adding a value to an enum is a migration, and CCP does not schedule migrations for us.

Every observed value is recorded in `app.open_vocabulary` with `first_seen_at` and a nullable
`acknowledged_at`. Unacknowledged values populate the administrator boards
(`/admin/scopes/unknown`, `/admin/alerts/unknown-types`). An unrecognised value is **ingested
and surfaced, never rejected**.

`app.permission` is the deliberate exception: it is HANGAR's *own* closed set, seeded from Go.

### 3.4 Time-series partitioning

`PARTITION BY RANGE (<time column>)`, monthly, on: `app.wallet_journal`,
`app.wallet_transaction`, `app.character_notification`, `app.killmail`, `app.market_history`.

* Partition key must be part of every primary key and unique constraint.
* A River periodic job (`partition-maintenance`, daily) creates the next **three** months of
  partitions ahead. Three, not one — a job outage must not cause an insert failure.
* A `DEFAULT` partition exists on each parent so an out-of-range row is captured rather than
  rejected; a non-empty default partition raises an operational alert.
* Retention is by `DETACH PARTITION` (DDL), never `DELETE` — §5.1 bans destructive DML.

### 3.5 Updates only on real change

```sql
INSERT INTO app.character_skill AS t (character_id, skill_id, active_level, trained_level, skillpoints, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (character_id, skill_id) DO UPDATE
   SET active_level  = EXCLUDED.active_level,
       trained_level = EXCLUDED.trained_level,
       skillpoints   = EXCLUDED.skillpoints,
       updated_at    = now()
 WHERE (t.active_level, t.trained_level, t.skillpoints)
    IS DISTINCT FROM (EXCLUDED.active_level, EXCLUDED.trained_level, EXCLUDED.skillpoints);
```

The `WHERE` on the `DO UPDATE` is the entire mechanism. `IS DISTINCT FROM` (not `<>`) so
NULL→NULL counts as unchanged. Phase 7's exit criterion asserts a second identical sync
produces zero `updated_at` changes.

**PostgreSQL 18 note.** PG18 allows `RETURNING` to reference both `OLD` and `NEW`, e.g.
`RETURNING old.trained_level, new.trained_level`. This lets a single statement upsert *and*
emit exactly the changed rows for the domain-event outbox, replacing the read-compare-write
round trip. Verify against the PG18 release notes before relying on it; the `IS DISTINCT FROM`
form above is the portable fallback and is what Phase 1 should ship.

### 3.6 Mutation policy

Destructive DML (`DELETE`, `TRUNCATE`) is **banned in Goose migrations**. A migration lint in
`make ci` greps for both. Application-level deletes are permitted only where a soft delete is
genuinely impossible; the sqlc `flag-delete` rule surfaces each one for review. Asset
reconciliation uses a soft delete (`deleted_at`) so an item that reappears is restored rather
than re-inserted with a new surrogate identity.

### 3.7 Indexing conventions

* Every foreign key gets an index — Postgres does not create one.
* Owner-polymorphic tables lead with `(owner_kind, owner_id, …)`. PG18's **B-tree skip scan**
  makes a query that omits the leading `owner_kind` usable, but do not design for it; write
  queries that supply the prefix.
* Partial indexes for hot predicates: `WHERE deleted_at IS NULL`, `WHERE blocked_by_pin`,
  `WHERE acknowledged_at IS NULL`, `WHERE state = 'pending'`.
* `BRIN` on the time column of every partitioned table — cheap and effective for append-only
  time-series.
* `updated_at` on tables read by the `_sync` envelope is indexed so freshness lookups do not
  scan.

---

## 4. Tier 1 — the 51 core platform tables

### 4.1 Identity and access (10)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 1 | `app.user` | `user_id uuid` (`uuidv7()`) | one per SeAT-equivalent account; `main_character_id bigint` |
| 2 | `app.character` | `character_id bigint` | ESI-typed identifier; corp/alliance affiliation, `owner_hash text` |
| 3 | `app.character_token` | `character_id bigint` | envelope ciphertext; `valid bool`, `invalid_reason text` |
| 4 | `app.character_token_scope` | `(character_id, scope)` | `scope text` → `app.esi_scope` |
| 5 | `app.session` | `session_id uuid` | server-side; `pkce_verifier`, `state`, `expires_at` |
| 6 | `app.api_token` | `token_id uuid` | third-party tokens; hashed secret, scoped permission array |
| 7 | `app.api_token_access_log` | `log_id uuid` | route, status, ip, at |
| 8 | `app.share_link` | `link_id uuid` | user-generated shareable views |
| 9 | `app.security_log` | `log_id uuid` | append-only; every search, every admin action, every lockdown |
| 10 | `app.setting` | `key text` | runtime settings incl. the **compatibility pin** and the cached JWKS |

```sql
CREATE TABLE app.character_token (
    character_id       bigint      PRIMARY KEY REFERENCES app.character(character_id),
    key_version        int         NOT NULL,
    wrapped_dek        bytea       NOT NULL,
    nonce              bytea       NOT NULL,
    ciphertext         bytea       NOT NULL,   -- AES-256-GCM(refresh_token)
    -- AAD = character_id ‖ key_version ‖ 'refresh_token'
    access_expires_at  timestamptz,
    valid              boolean     NOT NULL DEFAULT true,
    invalid_reason     text,                   -- open vocabulary, never an ENUM
    owner_hash         text        NOT NULL,   -- change ⇒ character transferred ⇒ revoke
    last_refreshed_at  timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.character_token (valid) WHERE NOT valid;   -- Strict Mode NOT EXISTS probe
```

Strict Mode's per-character check is
`NOT EXISTS (SELECT 1 FROM app.character c JOIN app.character_token t USING (character_id) WHERE c.user_id = $1 AND NOT t.valid)`.
The partial index above is what makes it a millisecond query at 5000 users.

### 4.2 RBAC and squads (10)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 11 | `app.role` | `role_id uuid` | |
| 12 | `app.permission` | `permission text` | **closed Go set**, seeded; CI fails on divergence |
| 13 | `app.role_grant` | `grant_id uuid` | `(role_id, permission, effect)` where `effect ∈ {allow, deny}` |
| 14 | `app.user_role` | `(user_id, role_id)` | |
| 15 | `app.effective_permission` | `(user_id, permission)` | materialised projection; refreshed on grant change |
| 16 | `app.squad` | `squad_id uuid` | `type` (open/managed/hidden), `owner_user_id` |
| 17 | `app.squad_member` | `(squad_id, character_id)` | |
| 18 | `app.squad_moderator` | `(squad_id, user_id)` | |
| 19 | `app.squad_role` | `(squad_id, role_id)` | squad membership grants an RBAC role |
| 20 | `app.squad_application` | `application_id uuid` | `status`, `resolved_by`, `resolved_at` |

`effect` is a **HANGAR-internal** two-value set, so a `CHECK (effect IN ('allow','deny'))`
is acceptable here; Principle 14 constrains *external* vocabularies only.

Deny precedence in SQL:

```sql
SELECT NOT bool_or(g.effect = 'deny') AND bool_or(g.effect = 'allow') AS permitted
  FROM app.user_role ur
  JOIN app.role_grant g USING (role_id)
 WHERE ur.user_id = $1 AND g.permission = $2;
```

A deny anywhere wins regardless of allow count — the Phase 10 truth table enumerates all
combinations.

### 4.3 ESI gateway and sync metadata (12)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 21 | `app.esi_route` | `route_id uuid` | the catalogue; see DDL below |
| 22 | `app.esi_scope` | `scope text` | **opaque**; `first_seen_at`, `acknowledged_at` |
| 23 | `app.esi_route_scope` | `(route_id, scope)` | from the operation's `security` block |
| 24 | `app.esi_route_role` | `(route_id, role)` | from `x-required-roles`; `role text`, open vocabulary |
| 25 | `app.esi_pin_history` | `pin_id uuid` | every pin advance: old, new, actor, route diff (JSONB) |
| 26 | `app.esi_cache_entry` | `cache_key bytea` | **UNLOGGED** L2 conditional cache |
| 27 | `app.esi_error_budget` | singleton `id smallint = 1` | installation-wide Governor 2 state |
| 28 | `app.esi_ledger_bucket` | `(rate_limit_group, user_key)` | **UNLOGGED** — Governor 1 bucket config + reconciliation state; the row lock that serialises acquire |
| 29 | `app.esi_ledger_entry` | `entry_id uuid` | **UNLOGGED** — the `(cost, consumed_at)` entries |
| 30 | `app.esi_replica` | `replica_id uuid` | heartbeat registry; selects `solo` vs `clustered` |
| 31 | `app.sync_subscription` | `subscription_id uuid` | `(entity_kind, entity_id, route_id)` unique |
| 32 | `app.sync_run` | `run_id uuid` | per-attempt outcome; the `_sync` envelope source |

#### The cluster-shared consumption ledger (SRS v3.1 §4.1.3, Principle 16)

ESI enforces the `(group, userID)` budget installation-wide, so HANGAR accounts for it
installation-wide. Per-replica accounting is prohibited — see `01_ARCHITECTURE.md` §5.6 for why
header reconciliation cannot substitute.

```sql
CREATE UNLOGGED TABLE app.esi_ledger_bucket (
    rate_limit_group   text        NOT NULL,
    -- 'applicationID:characterID' on authenticated routes,
    -- 'sourceIP' or 'sourceIP:applicationID' on unauthenticated ones.
    user_key           text        NOT NULL,
    max_tokens         integer     NOT NULL,
    -- The floating window's length. NOT a HANGAR constant: it is the spec's
    -- own x-rate-limit.window-size, "15m" on every one of the 225 routes
    -- ingested from live ESI, corroborated by X-Ratelimit-Limit's own
    -- "<max>/15m" suffix. Verified in Phase 20.4.1 rather than assumed. The
    -- LENGTH is right and the SHAPE differs: HANGAR releases each request's
    -- cost one window after that request, while ESI was measured behaving as
    -- a sliding-window counter over fixed 15-minute wall-clock windows, so
    -- its tail is longer. 01_ARCHITECTURE.md §5.5 has the readings and why
    -- HANGAR's shape is deliberately not changed to match.
    window             interval    NOT NULL,
    -- ── THE RECONCILER'S OWN ARITHMETIC (§5.5, Phases 20.4 and 20.4.1) ──
    -- Three readings, written under this row's own FOR UPDATE lock in the
    -- transaction that performs the correction, in this order:
    --
    --   local_remaining_at_reading     what HANGAR held, BEFORE
    --   server_remaining               what the server said
    --   local_remaining_after_reading  what HANGAR held, once converged
    --
    -- and two metrics, both measured against least(server_remaining,
    -- max_tokens) because §5.5 never converges above the ceiling:
    --
    --   esi_ledger_prediction_error = |at_reading    − that|  recorded
    --   esi_ledger_divergence       = |after_reading − that|  Gate 1.3, bound 0
    --
    -- All three are nullable and NULL means no reading, which is not a
    -- reading of zero. Storing only the live sum against a snapshot server
    -- reading is the defect migration 00042 fixed; reporting only the
    -- pre-correction pair is the one 00043 fixed.
    server_remaining              integer,
    local_remaining_at_reading    integer,
    local_remaining_after_reading integer,
    server_observed_at timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (rate_limit_group, user_key)
);

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
```

Acquire is one short transaction, and the bucket row is the only lock taken:

```sql
-- 1. serialise this bucket and nothing else
SELECT max_tokens, window FROM app.esi_ledger_bucket
 WHERE rate_limit_group = $1 AND user_key = $2 FOR UPDATE;

-- 2. evict, then measure
DELETE FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2
   AND (consumed_at <= now() - $3::interval
        OR (state = 'reserved' AND expires_at < now()));

SELECT coalesce(sum(cost), 0) FROM app.esi_ledger_entry
 WHERE rate_limit_group = $1 AND user_key = $2;

-- 3. reserve the worst case, or return the retry time
INSERT INTO app.esi_ledger_entry (rate_limit_group, user_key, cost, consumed_at, state, expires_at)
VALUES ($1, $2, 5, now(), 'reserved', now() + $4::interval);
```

On authenticated routes `user_key` is one character, so different characters never contend. The
transaction costs ~0.3 ms against a network call already costing 50–500 ms.

```sql
CREATE TABLE app.esi_replica (
    replica_id     uuid        PRIMARY KEY,
    role           text        NOT NULL,      -- 'serve' | 'work' | 'schedule'
    version        text        NOT NULL,
    started_at     timestamptz NOT NULL,
    last_heartbeat timestamptz NOT NULL
);
CREATE INDEX ON app.esi_replica (last_heartbeat);
```

Heartbeat every 10 s; live if under 30 s old. **Exactly one live replica ⇒ `solo` mode** (pure
in-process ledger, no database round-trip). **Two or more ⇒ `clustered`.** The mode is derived,
never configured: an operator-settable divisor is exactly the knob that gets set wrong at 03:00,
which is the defect this design removes.

Both ledger tables are UNLOGGED because losing them on crash costs a conservative
re-reconciliation from the next response's headers, not a correctness failure. Mode transitions
flush in both directions before admitting further requests (Phase 4 exit criterion).

```sql
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
```

`identifier_types` is what `hangar admin verify-identifier-types` reads. It is populated at
ingest from the operation's parameter and response schemas, so Gate 6's synthetic UUID path
identifier is typed with **zero code changes**.

```sql
CREATE UNLOGGED TABLE app.esi_cache_entry (
    cache_key      bytea       PRIMARY KEY,        -- sha256 over the §5.3 tuple
    etag           text,
    last_modified  timestamptz,
    body           bytea       NOT NULL,
    status         smallint    NOT NULL,
    expires_at     timestamptz NOT NULL,
    stored_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.esi_cache_entry (expires_at);
```

UNLOGGED is deliberate: losing this table on crash costs one revalidation round, and skipping
WAL for a high-churn cache is a large write saving. It is never a source of truth.

```sql
CREATE TABLE app.sync_subscription (
    subscription_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    entity_kind         text        NOT NULL,       -- 'character' | 'corporation' | 'alliance' | 'global'
    entity_id           bigint      NOT NULL,       -- 0 for global routes
    route_id            uuid        NOT NULL REFERENCES app.esi_route(route_id),
    enabled             boolean     NOT NULL DEFAULT true,
    acting_character_id bigint,                     -- elected director for corp routes (§6.3)
    next_due_at         timestamptz NOT NULL DEFAULT now(),
    last_success_at     timestamptz,
    last_status         smallint,
    etag                text,
    last_modified       timestamptz,
    cursor_after        text,                       -- opaque; never parsed
    consecutive_304     integer     NOT NULL DEFAULT 0,   -- drives 1.5^n backoff
    consecutive_403     integer     NOT NULL DEFAULT 0,   -- drives entity breaker + re-election
    snoozed_until       timestamptz,                -- set from Retry-After on 429
    opt_in_no_cache     boolean     NOT NULL DEFAULT false,
    UNIQUE (entity_kind, entity_id, route_id)
);
CREATE INDEX ON app.sync_subscription (next_due_at)
   WHERE enabled AND (snoozed_until IS NULL OR snoozed_until < now());
```

That partial index is the planner's claim path; without it the 5-second claim loop scans.

### 4.4 Access provisioning (5)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 33 | `app.platform` | `platform_id uuid` | discord / teamspeak / mumble instance + config |
| 34 | `app.platform_group` | `group_id uuid` | remote group identity: Discord role id, TS3 server group, Mumble ACL group |
| 35 | `app.entitlement_rule` | `rule_id uuid` | `(source_kind, source_ref, group_id, effect)`; seven sources + deny |
| 36 | `app.provisioning_state` | `(platform_id, user_id)` | linked remote identity, challenge token, desired vs actual groups |
| 37 | `app.provisioning_audit` | `audit_id uuid` | **Gate 2 evidence** |

```sql
CREATE TABLE app.provisioning_audit (
    audit_id                   uuid        PRIMARY KEY DEFAULT uuidv7(),
    platform_id                uuid        NOT NULL REFERENCES app.platform(platform_id),
    user_id                    uuid        NOT NULL,
    action                     text        NOT NULL,   -- 'grant' | 'revoke' | 'link' | 'unlink'
    reason                     text        NOT NULL,   -- open vocabulary
    groups_added               text[]      NOT NULL DEFAULT '{}',
    groups_removed             text[]      NOT NULL DEFAULT '{}',
    -- Gate 2 measures p99 over (platform_call_completed_at - event_at).
    -- event_at is the ORIGINATING event, not job start: queue wait is the part
    -- that fails under load and must be inside the measurement.
    event_at                   timestamptz NOT NULL,
    platform_call_completed_at timestamptz,
    outcome                    text,
    error                      text
);
CREATE INDEX ON app.provisioning_audit (event_at DESC);
CREATE INDEX ON app.provisioning_audit (platform_call_completed_at)
   WHERE platform_call_completed_at IS NULL;      -- the exposure board
```

`provisioning_state.desired_groups` vs `actual_groups` is what the exposure board diffs, and
`platform_call_completed_at IS NULL` with an ageing `event_at` **is** a pending revocation.

### 4.5 Alerting (6)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 38 | `app.alert_type` | `alert_type text` | 54 seeded rows across 8 domains; `source_route_id` for threshold alerts |
| 39 | `app.alert_channel` | `channel_id uuid` | SMTP / Slack webhook / Discord webhook config |
| 40 | `app.alert_routing_rule` | `rule_id uuid` | `(alert_type, target_kind, target_ref, channel_id, mention)` |
| 41 | `app.alert_event` | `event_id uuid` | `dedupe_hash`, `coalesce_key`, `payload jsonb` |
| 42 | `app.alert_delivery` | `delivery_id uuid` | outbox + attempts + dead-letter |
| 43 | `app.notification_unknown_type` | `type text` | the unknown-types board |

```sql
CREATE TABLE app.alert_type (
    alert_type      text    PRIMARY KEY,        -- CCP notification type or hangar.* event
    domain          text    NOT NULL,           -- structures|characters|platform|wars|
                                                -- corporations|sovereignty|contracts|alliances
    category        text    NOT NULL,           -- 'esi_notification'|'domain_event'|'threshold'
    -- BUILD-TIME RULE: category='threshold' ⇒ source_route_id NOT NULL and that route
    -- must be present in the sync set. Enforced by TestThresholdAlertSourceRoutesScheduled.
    source_route_id uuid    REFERENCES app.esi_route(route_id),
    default_enabled boolean NOT NULL DEFAULT true,
    CONSTRAINT threshold_declares_source
        CHECK (category <> 'threshold' OR source_route_id IS NOT NULL)
);
```

Seeded domain counts, asserted at build time: Structures **23** (5 Skyhook), Characters 7,
platform 7, Wars 6, Corporations 5, Sovereignty 4, Contracts 1, Alliances 1 = **54**.

> **Corrected in Phase 14.1.** This line previously read "Structures 22" while still totalling
> 54 — the same arithmetic defect §4.4 carried, stated here in a form where the sum is written
> out. Measured against the upstream: Structures is 23. See docs/BASELINE.md §4a.

`alert_event.payload` is `jsonb` and is where an unparseable CCP notification YAML lands
verbatim so the generic renderer can produce key/value output. The queue never halts.

### 4.6 Events and webhooks (3)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 44 | `app.webhook_endpoint` | `endpoint_id uuid` | url, HMAC secret (envelope-encrypted), event filter |
| 45 | `app.outbox_event` | `event_id uuid` (`uuidv7()`) | written in the mutating transaction |
| 46 | `app.webhook_delivery` | `delivery_id uuid` | attempts, response status, next retry |

`outbox_event` is `uuidv7()`-keyed so the dispatcher reads in causal order with an index-only
scan. The Phase 19 test asserts the data mutation and the outbox insert are in one
transaction by rolling the transaction back and proving neither survives.

### 4.7 Shared reference and open vocabularies (5)

| # | Table | Key | Notes |
| :-- | :-- | :-- | :-- |
| 47 | `app.corporation` | `corporation_id bigint` | includes the 2026-07-21 palette fields |
| 48 | `app.alliance` | `alliance_id bigint` | |
| 49 | `app.location` | `(location_type, location_id)` | resolved stations / structures / systems |
| 50 | `app.open_vocabulary` | `(vocabulary, value)` | **the Principle 14 mechanism** |
| 51 | `app.sde_import` | `import_id uuid` | atomic-swap bookkeeping, checksum, row counts |

```sql
CREATE TABLE app.open_vocabulary (
    vocabulary      text        NOT NULL,   -- 'ref_type'|'location_type'|'notification_type'|
                                            -- 'scope'|'cache_mode'|'contract_status'|'required_role'
    value           text        NOT NULL,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    occurrences     bigint      NOT NULL DEFAULT 1,
    acknowledged_at timestamptz,
    acknowledged_by uuid,
    PRIMARY KEY (vocabulary, value)
);
CREATE INDEX ON app.open_vocabulary (vocabulary) WHERE acknowledged_at IS NULL;
```

One table, one admin board pattern, every open vocabulary. Gate 6's novel scope grammar and
unrecognised `x-cache-mode` value both land here with **zero code changes** — which is exactly
what Gate 6 tests.

`app.esi_scope` is kept as its own table (#22) despite this, because scopes carry route
relationships (`esi_route_scope`) and token relationships (`character_token_scope`) that a
generic vocabulary row cannot express.

**Tier 1 total: 10 + 10 + 12 + 5 + 6 + 3 + 5 = 51.**

---

## 5. Tier 2 — ESI domain projections (≈ 72)

### 5.1 Owner polymorphism — the mechanism that bounds the count

Legacy SeAT duplicates most tables per owner (`character_assets`, `corporation_assets`, …).
HANGAR uses a discriminated owner on shared concepts:

```
owner_kind text NOT NULL   -- 'character' | 'corporation'
owner_id   bigint NOT NULL
```

Leading every index with `(owner_kind, owner_id, …)`. This halves the table count on the
eleven concepts that exist for both owners (assets, wallets, contracts, industry, blueprints,
killmails, market orders, contacts, standings, medals, mining) and makes the
`/characters/{id}/…` and `/corporations/{id}/…` handlers one implementation.

`app.asset` is the SRS's own example (§5.2): compound PK `(owner_kind, owner_id, item_id)` so
an asset transfer between owners is an insert plus a soft delete rather than a PK collision.

### 5.2 Table map

| Group | Tables | Phase |
| :-- | :-- | :-- |
| **Corporation structure** (16) | `corporation_member`, `corporation_member_tracking`, `corporation_title`, `corporation_member_title`, `corporation_role`, `corporation_role_history`, `corporation_division`, `corporation_shareholder`, `corporation_facility`, `corporation_customs_office`, `corporation_container_log`, `corporation_structure`, `corporation_starbase`, `starbase_detail`, `corporation_skyhook`ᴿ, `corporation_sovereignty_hub`ᴿ | 8 |
| **Corporation projects** (3, UUID-keyed) | `corporation_project`, `corporation_project_contributor`, `corporation_project_contribution` | 9 |
| **History** (2) | `character_corporation_history`, `corporation_alliance_history` | 7/8 |
| **Assets** (2) | `asset`, `asset_location` | 9 |
| **Wallets** (3) | `wallet_balance`, `wallet_journal`ᴾ, `wallet_transaction`ᴾ | 8 |
| **Contracts** (3) | `contract`, `contract_item`, `contract_bid` | 9 |
| **Industry & mining** (6) | `industry_job`, `blueprint`, `mining_ledger`, `mining_extraction`, `mining_observer`, `mining_observer_record` | 8/9 |
| **Market** (4) | `market_order`, `market_order_history`, `market_history`ᴾ, `market_price` | 9 |
| **Killmails** (3) | `killmail`ᴾ, `killmail_attacker`, `killmail_item` | 9 |
| **Social** (5) | `contact`, `contact_label`, `standing`, `medal`, `medal_issued` | 7/8 |
| **Character sheet** (11) | `character_skill`, `character_skillqueue`, `character_attributes`, `character_clone`, `character_implant`, `character_jump_fatigue`, `character_loyalty_point`, `character_agent_research`, `character_title`, `character_role`, `character_location` | 7 |
| **Fittings** (2) | `character_fitting`, `character_fitting_item` | 7 |
| **Mail** (5) | `mail_header`, `mail_body`, `mail_recipient`, `mail_label`, `mail_list` | 9 |
| **Notifications** (2) | `character_notification`ᴾ, `notification_contact` | 9 |
| **Calendar** (3) | `calendar_event`, `calendar_event_detail`, `calendar_event_attendee` | 9 |
| **Planetary interaction** (2) | `planet_colony`, `planet_colony_detail` | 9 |
| **Sovereignty** (2) | `sovereignty_campaign`, `sovereignty_system` | 8 |
| **Tools** (3) | `character_note`, `insurance_price`, `moon_report` | 9 |
| **Intel** (1) | `character_intel_edge` — derived interaction graph over mail, contacts, killmails, standings | 9 |

ᴾ = `PARTITION BY RANGE`, monthly.

ᴿ = **Phase 8.1 fixup** (`00033_phase8_1_skyhook_reagent_fixup.sql`): the live spec models both
structures as reagent-powered, not fuel-powered — `fuel_expires` was dropped in favour of a
`reagents jsonb` column mirroring `starbase_detail.fuels`'s shape, and `type_id`
(both tables) plus `corporation_skyhook.system_id` were relaxed to nullable, since none of the
three is obtainable from ESI pre-SDE (Phase 9/25) and Principle 13 forbids guessing at a
plausible-looking constant with no verifiable source.

**Tier 2 total: 78.** Combined with Tier 1: **129 tables**, matching SRS v3.1 §5.2 — plus Phase
8's own platform-tier addition, `app.sync_acting_character_history`
(`00031_phase8_acting_character_history.sql`, §6.3's per-candidate 403 history — see that
migration's header), for **135 tables** as actually migrated. `db/migrations_integration_test.go`
and `db/migrations_domain_integration_test.go` assert the corrected total.

Every table in this map traces to at least one §6 endpoint and at least one Appendix A
capability. `docs/03_IMPLEMENTATION_ROADMAP.md` carries the phase mapping; a table with no
phase, or an endpoint with no table, is a specification defect under SRS §11.

### 5.3 Representative layouts

**Assets — designed for a single-query recursive tree (§5.2, Phase 1 exit).**

```sql
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
```

```sql
-- Single-query tree for GET /api/v1/{owner}/{id}/assets/tree/{location_id}
WITH RECURSIVE tree AS (
    SELECT a.*, 1 AS depth, ARRAY[a.item_id] AS path
      FROM app.asset a
     WHERE a.owner_kind = $1 AND a.owner_id = $2
       AND a.location_id = $3 AND a.deleted_at IS NULL
    UNION ALL
    SELECT c.*, t.depth + 1, t.path || c.item_id
      FROM app.asset c
      JOIN tree t ON c.location_id = t.item_id
     WHERE c.owner_kind = $1 AND c.owner_id = $2 AND c.deleted_at IS NULL
       AND t.depth < $4                       -- bound; containers can cycle after a bad sync
       AND NOT c.item_id = ANY(t.path)        -- cycle guard
)
SELECT * FROM tree;
```

Both bounds are required. Depth 5 in under 2 seconds is the Phase 17 target, and a cycle
introduced by a torn sync must degrade to a truncated tree, not an unbounded query.

**Wallet journal — partitioned, exact money.**

```sql
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

CREATE INDEX ON app.wallet_journal USING brin (date);
CREATE INDEX ON app.wallet_journal (owner_kind, owner_id, date DESC);
```

`ref_type` is `text`, never an enum — CCP adds ref types without notice, and rejecting one
would drop a journal row, which is a money-loss bug.

**Corporation projects — UUID-keyed, no coercion (Principle 13, Gate 6).**

```sql
CREATE TABLE app.corporation_project (
    project_id      uuid        PRIMARY KEY,           -- FROM CCP. type: string, format: uuid.
                                                       -- NOT generated here; NOT stored as text.
    corporation_id  bigint      NOT NULL REFERENCES app.corporation(corporation_id),
    name            text        NOT NULL,
    state           text        NOT NULL,              -- open vocabulary
    contribution_type text,
    target_progress numeric(30,2),
    current_progress numeric(30,2),
    reward_isk      numeric(30,2),                     -- money
    expires_at      timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.corporation_project_contribution (
    project_id   uuid    NOT NULL REFERENCES app.corporation_project(project_id),
    character_id bigint  NOT NULL,                     -- int64 identifier, alongside a uuid one
    amount       numeric(30,2) NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, character_id)
);
```

This table is the Phase 9 fixture proving a `uuid` PK joins against a `bigint` FK in the same
row without coercion, and the Gate 6 fixture for identifier typing.

**Starbase detail — the fuel-low alert's data source.**

```sql
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
    PRIMARY KEY (corporation_id, starbase_id)
);
```

---

## 6. `sde` schema and the atomic swap

SDE definitions arrive as streamed JSONL. Import must never leave the schema half-updated
while the API is serving.

1. `CREATE SCHEMA sde_next` and build every table into it, streaming with `COPY`.
2. Verify: row counts against the manifest, checksum, and a smoke query per table.
3. In one transaction: `ALTER SCHEMA sde RENAME TO sde_old; ALTER SCHEMA sde_next RENAME TO sde;`
4. `DROP SCHEMA sde_old CASCADE` **outside** the transaction, after a grace period.
5. Record the outcome in `app.sde_import`.

Steps 3 and 4 are separate because the rename takes an `ACCESS EXCLUSIVE` lock and must be
held for microseconds, not for the duration of a drop.

Core `sde` tables: `type`, `group`, `category`, `region`, `constellation`, `solar_system`,
`station`, `station_operation`, `market_group`, `dogma_attribute`, `dogma_effect`,
`type_dogma_attribute`, `type_material`, `blueprint`, `blueprint_activity`, `icon`, `graphic`,
`faction`, `npc_corporation`, `race`, `bloodline`, `ancestry`, `skin`, `moon`, `planet`.

**Edge case.** Post-v1.0 military campaigns "join definitions from SDE". The `sde` schema must
therefore be joinable from `app` — same database, not a separate service. That constrains SDE
to Postgres and rules out shipping it as a file-backed lookup.

---

## 7. PostgreSQL 18 features this design relies on

| Feature | Used for | Confidence |
| :-- | :-- | :-- |
| `uuidv7()` built-in | time-ordered internal surrogate keys without an extension | High — verify at Phase 1 |
| `RETURNING old.* / new.*` | single-statement upsert + changed-row event emission (§3.5) | Medium — portable fallback shipped |
| B-tree skip scan | tolerates queries omitting the leading `owner_kind` | Optimisation only; do not design for it |
| Async I/O (`io_method`) | read throughput on partitioned time-series scans | Config-level; set in `docker-compose.yml` |
| `NOT NULL NOT VALID` | adding NOT NULL to a large table without a full rewrite | Phase 1b onward |
| Temporal `WITHOUT OVERLAPS` constraints | non-overlapping provisioning-state history | Optional; evaluate at Phase 11 |

Nothing in the *correctness* of this schema depends on a PG18-only feature except `uuidv7()`,
which has a trivial fallback (`gen_random_uuid()` plus a `created_at` index). Confirm each
against the PG18 release notes during Phase 1 rather than assuming.

---

## 8. Phase 1 exit checklist

| Criterion | Mechanism | Sub-phase |
| :-- | :-- | :-- |
| Clean `goose up` and `goose down` on an empty PG18 | Testcontainers, both directions, twice | 1a + 1b |
| `sqlc generate` produces no diff | `make verify-generated` | 1a + 1b |
| **Zero `float64` on money paths** | reflection walk over `internal/domain` + `internal/api/dto` | 1b |
| **Every identifier column matches the spec's declared type, including `uuid`** | `hangar admin verify-identifier-types` against the ingested spec | 1b |
| No destructive DML in any migration | migration lint | 1a + 1b |
| All 51 Tier-1 tables present | schema-diff test against §4 | 1a |
| Ledger tables are UNLOGGED and the bucket FK cascades | schema introspection test | 1a |
| Replica registry drives mode selection | insert two live heartbeats ⇒ `clustered`; expire one ⇒ `solo` | 1a |
| Tier-2 map complete | schema-diff test against §5.2 | 1b |
| Recursive asset-tree CTE proven | fixture with 5 nesting levels + an injected cycle | 1b |
| Partition maintenance creates three months ahead | fast-forward clock test | 1b |

---

## 9. Post-Phase-1 schema and query additions

§4 and §5 describe the schema as Phase 1a/1b delivered it. Later phases
have added to it. This section is the running record of what changed
after Phase 1, so the document keeps describing what actually exists
rather than what was first designed.

### 9.1 Columns added after Phase 1

| Migration | Table | Column | Phase | Why |
| :-- | :-- | :-- | :-- | :-- |
| `00040_phase15_1_defect_closure.sql` | `app.corporation` | `member_limit integer` | 15.1 | SRS §6.3 exposes `GET /corporations/{id}/members/limit`, and the ingested snapshot confirms the upstream route. Phase 1a modelled `member_count` (current occupancy) but never `member_limit` (permitted maximum) — different facts; the "approaching the ceiling" reading needs both. Phase 15 could not register the route at all and reported the gap. |
| `00040_phase15_1_defect_closure.sql` | `app.platform` | `locked_down boolean NOT NULL DEFAULT false`, `locked_down_at`, `locked_down_by`, `lockdown_reason` | 15.1 | SRS §6.8's `POST /admin/platforms/{id}/lockdown`. Deliberately **not** a reuse of `app.platform.enabled`: `enabled` is the ordinary "is this platform in use" switch, and flipping it to handle an incident would afterwards be indistinguishable from a platform that was never configured. `locked_down` records who froze provisioning, when, and why. |

### 9.2 Queries added after Phase 1

Every query below is real, committed and load-bearing. Phase 15 added
twelve of them outside its declared file list while implementing SRS §6
for real; Phase 15.1 added the rest closing Phase 15's 501s. Recording
them here is Principle 10's documentation counterpart — the schema
document must describe the queries that exist.

**Phase 15** (`internal/api/v1` needed a read path that no sync-side query
provided):

| Query | File | Serves |
| :-- | :-- | :-- |
| `ListApiTokensForUser` | `user.sql` | `GET /api/v1/api-tokens` — every prior `api_token` query targeted one known token. |
| `ListShareLinksForUser` | `user.sql` | `GET /api/v1/me/share-links` |
| `GetCharacterJumpFatigue` | `character_sheet.sql` | `GET /characters/{id}/fatigue` — only the sync-side `Upsert` existed. |
| `ListCharacterAgentResearch` | `character_sheet.sql` | `GET /characters/{id}/agents_research` |
| `ListAllCorporationMemberTitles` | `corporation_structure.sql` | `GET /corporations/{id}/members/titles` is corp-wide; the existing query was one member's rows. |
| `ListAllCorporationRoles` | `corporation_structure.sql` | `GET /corporations/{id}/roles`, same corp-wide vs per-member distinction. |
| `ListAlliances` | `reference.sql` | `GET /api/v1/alliances` |
| `ListCorporationsByAlliance` | `reference.sql` | `GET /api/v1/alliances/{id}/corporations` |
| `SearchCharactersByName` / `SearchCorporationsByName` / `SearchAlliancesByName` | `reference.sql` | `POST /api/v1/support/search`. Searches HANGAR's own synced rows only — CCP prohibits using ESI for entity discovery (§4.7). Always parameter-bound, never string-concatenated. |
| `UpdateSquad` / `DeleteSquad` | `squad.sql` | `PATCH` / `DELETE /api/v1/squads/{id}` — no mutation beyond `CreateSquad` existed. |

**Phase 15.1** (closing Phase 15's 501 responses):

| Query | File | Serves |
| :-- | :-- | :-- |
| `ListMarketOrdersByRegion` | `market.sql` | `GET /markets/{region_id}/orders`. **Scope:** the orders HANGAR has synced for tracked owners in that region — not the complete public order book, which would mean ingesting hundreds of thousands of rows across ~100 regions and which nothing in the SRS asks for. `app.market_order` was always built for this read: Phase 1b gave it a `region_id` column *and* a dedicated index on it, an index no owner-scoped query can use. |
| `ListMarketTypesByRegion` | `market.sql` | `GET /markets/{region_id}/types`, same scope note. |
| `SetCorporationMemberLimit` | `reference.sql` | The `/members/limit` sync — a bare-integer upstream route, so it needs a targeted write rather than the full corporation upsert. |
| `AggregateWalletJournalByRefType` | `wallet.sql` | `GET /corporations/{id}/ledger/{bounties,pi}`. One parameterised aggregate serves both; `ref_type` is an open vocabulary (Principle 14) so the set is passed as data, not baked into SQL. `total_amount` is `NUMERIC` ⇒ `decimal.Decimal` ⇒ a JSON string (Principle 9). |
| `ListSdeTypeNames` | `tools.sql` | EFT fitting export. Returns only rows that exist, so an installation with no SDE import degrades to id placeholders per line rather than failing the export. |
| `SetPlatformLockdown` | `provisioning.sql` | `POST /admin/platforms/{id}/lockdown` |
| `DeleteRoleGrants` | `rbac.sql` | The delete half of `PUT /admin/scopes`' atomic grant replace (paired with `AddRoleGrant` inside one transaction by `internal/rbac.ReplaceRoleGrants`). |

### 9.3 Corrected query semantics

| Query | Phase | Correction |
| :-- | :-- | :-- |
| `CompleteSessionLogin` | 15.1 | Now also sets `expires_at`. It previously left the authenticated session on the 10-minute pre-auth `sso.StateTTL` deadline that `BeginLogin` wrote, and `GetSession` filters `expires_at > now()` — so **every user would have been force-logged-out ten minutes after clicking "log in"**. `config.CryptoConfig.SessionTTL` (default 720h) had existed since Phase 5 with no consumer; this is it. Undetected until Phase 15.1 because Phase 15 left `/auth/callback` answering 501, so no session was ever completed. |
| `UpsertCorporation` | 15.1 | `member_limit` added to both the column list and the `IS DISTINCT FROM` change guard. Omitting it from the guard would have pinned the value forever: a corporation that trained Corporation Management would keep its stale limit because no *other* column changed and the `DO UPDATE` would be skipped. |
