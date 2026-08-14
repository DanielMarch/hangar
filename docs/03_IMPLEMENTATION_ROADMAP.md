# 03 — The 21-Phase Execution Roadmap

**Derived from:** [`00_SRS_v3.1.md`](00_SRS_v3.1.md) §7 — **not** v3.0, which contains seven
corrected defects. Phases 1, 4, 13, 16 and 20 changed materially between the two.

**Target executor:** Sonnet 5, medium effort, one phase per session.
**Rule:** a phase is not complete until every exit criterion has a *passing named test*. No
phase may begin before its predecessor's exit criteria are green. This is SRS §7's
"isolated integration tests before the next begins", made literal.

---

## How to use this document

Each phase has a fixed structure:

* **Objective** — one sentence.
* **Depends on** — phases that must be green.
* **Legacy reference** — the SeAT repository and path to reverse-engineer business logic
  from. Paths are given at directory granularity; **confirm them against HEAD** before
  relying on a specific filename (see Phase 0, task 6).
* **Files** — exact paths to create or modify.
* **Design notes** — decisions already made in `01_ARCHITECTURE.md` that apply here.
* **Edge cases** — the things that will otherwise be discovered in production.
* **Exit criteria** — named tests. Copy the names verbatim.
* **Prompt seed** — a ready-to-paste task brief for the implementing agent.

### Legacy repositories

| Repo | URL | Use for |
| :-- | :-- | :-- |
| `eveapi` | https://github.com/eveseat/eveapi | Sync job semantics, ESI endpoint bindings, models |
| `web` | https://github.com/eveseat/web | The 72 UI controllers, Blade views, DataTables, locale files |
| `notifications` | https://github.com/eveseat/notifications | The 54 concrete alert types and their payload shapes |
| `services` | https://github.com/eveseat/services | Job scheduling, repositories, console commands |
| `api` | https://github.com/eveseat/api | `/api/v2` controllers and resources — the shim's source of truth |

### Upstream documentation

| Subject | URL |
| :-- | :-- |
| EVE SSO (OAuth 2.0 PKCE, JWKS) | https://developers.eveonline.com/docs/services/sso/ |
| Static Data Export | https://developers.eveonline.com/docs/services/static-data/ |
| Discord API | https://discord.com/developers/docs/intro |
| TeamSpeak 3 server / WebQuery | https://community.teamspeak.com/c/teamspeak-3-server |
| Mumble + gRPC | https://www.mumble.info/documentation/ · https://grpc.io/docs/guides/ |
| Slack incoming webhooks | https://api.slack.com/messaging/webhooks |

### Standing instructions for every phase

1. Read `01_ARCHITECTURE.md` §17 (cross-cutting invariants) before writing code.
2. Money is `decimal.Decimal` in Go and `string` in JSON. Never `float64`.
3. Identifier types come from the spec. Never assume `bigint`.
4. External vocabularies are `text`. Never a regex, never a closed set, never an ENUM.
5. `X-Compatibility-Date` on every ESI request: the **app pin** for data, **`D_max`** for the
   spec fetch.
6. `upstream_path` verbatim. Never pluralise, never derive.
7. Failure is scoped: one entity, one route. Never halt the installation.
8. Add the test **before** or **with** the code, never after the phase is declared done.

---

## Phase 0 — Repository & Toolchain Bootstrap

**Objective.** A buildable skeleton: Go backend, SPA shell, redacting logger, Docker images, CI.

**Depends on.** Nothing. The scaffold in this repository is the starting state.

**First action: `git init`.** `C:\HANGAR` is not yet a git repository. Initialise it, make the
existing scaffold the initial commit, and create a working branch before changing anything —
Principle 10's generated-artefact diffing and Gate 6's clean-`git status` proof both require
version control from the start.

**Legacy reference.** None. This phase is greenfield.

**Files.**

```
cmd/hangar/main.go, root.go, serve.go, work.go, schedule.go, migrate.go, admin.go,
             openapi.go, healthcheck.go
internal/config/config.go, secret.go, validate.go
internal/telemetry/slog.go, redact.go, otel.go, metrics.go, replica.go
web/vite.config.ts, tsconfig.json, eslint.config.js, index.html, pnpm-lock.yaml
web/src/main.tsx, web/src/styles/index.css
.github/workflows/ci.yml, release.yml
deploy/install.sh, deploy/install.bat
docs/openapi.json                     ← minimal valid OpenAPI 3.1 stub; see below
docs/BASELINE.md                      ← task 6 output
```

**Design notes.**

* `config.Secret` is a `string` wrapper whose `String()`, `MarshalJSON`, `LogValue()` and
  `Format()` all return `[REDACTED]`. Every credential field in `internal/config` uses it.
* The redaction handler wraps `slog.JSONHandler` and walks attribute values recursively —
  through `slog.Group`, `map[string]any`, slices and nested structs.
* `web/src/styles/index.css` is the **single** sanctioned stylesheet (SRS v3.1 §8.1).
  `make check-css` fails on a second one.
* **Commit `docs/openapi.json` as a minimal valid OpenAPI 3.1 stub.** It is a
  generated-but-committed artefact (Principle 10) and the Dockerfile's SPA stage copies it. With
  no stub, no container image can build until Phase 15. Phase 15 replaces the contents; the file
  must exist from Phase 0.
* **`make ci` is progressive.** Each invariant check skips when its input is absent and starts
  enforcing the moment the phase that introduces it lands. `make ci-strict` (`STRICT=1`) turns
  every skip into a failure — wire CI to use plain `ci` now and switch to `ci-strict` at Phase 15
  and on every release tag. Do not "fix" a skipping check by deleting it.
* `internal/telemetry/replica.go` is the heartbeat loop into `app.esi_replica`. It lands here
  rather than in Phase 4 because every process role must heartbeat from the first release, and
  retrofitting it into all three commands later is more disruptive than writing it now. It is
  inert until Phase 1a creates the table — guard on the table's existence.
* The `Dockerfile`, `Makefile`, `sqlc.yaml`, `docker-compose.yml`, `go.mod` and
  `web/package.json` in this repository are the target shapes. Run `go mod tidy` and
  `pnpm install` to resolve real versions, then commit `go.sum` and `pnpm-lock.yaml`. Only the
  pins marked contractual in `go.mod` are fixed; everything else is an indicative floor.

**Task 6 — measure the baseline (Principle 15).** Clone the five legacy repositories at HEAD
and emit `docs/BASELINE.md` with counts and the command used for each:

| Dimension | Expected | Method |
| :-- | :-- | :-- |
| ESI call sites | 107 | job classes declaring `$endpoint`, plus the inline mail-body call in `Jobs/Mail/` |
| Distinct ESI routes | 106 | dedupe the above; `/corporations/{id}/assets/locations` is bound twice |
| UI controller classes | 72 | `web/src/Http/Controllers/**/*Controller.php` |
| Concrete alert types | 54 | `notifications/src/Notifications/**`, excluding abstracts and traits |
| UI locales | 9 | `af de en fr ja ko ro ru zh-CN` |
| ESI scopes | 70 | distinct scope strings in the 2026-05-19 spec |
| `/api/v2` controllers | 9 | `api/src/Http/Controllers/Api/v2/` |

Gate 4 compares against *this file*, not against the SRS's assertions. If a measured count
disagrees with the SRS, that is a specification defect to be raised — record the measurement,
do not silently adopt either number.

**Edge cases.**

* CI must build `windows/amd64` too (§9.2). Path separators and `embed.FS` (which always uses
  forward slashes) diverge — catch it in CI, not at release.
* `docker compose up` must be healthy from a blank environment with only
  `POSTGRES_PASSWORD` and the two SSO values set. Everything else defaults.
* The static binary must have `CGO_ENABLED=0`. A single accidental cgo dependency silently
  produces a dynamically linked binary that fails on distroless.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `make ci` | passes end to end, with skips only for phases not yet started |
| `TestRedactHandlerRecursive` | secrets at nesting depth ≥ 3, inside maps and slices, do not appear in output |
| `TestConfigFailsFastOnMissingSecrets` | absent `HANGAR_MASTER_KEY` aborts boot with a named error, never a generated key |
| `TestStaticBinaryHasNoDynamicLinks` | `go build` output is static on all three targets |
| `make check-no-ice` | no ZeroC Ice or cgo dependency in `go.mod`/`go.sum` |
| compose smoke | `docker compose up -d` reaches healthy in < 90 s from empty |
| CI | image builds (the `openapi.json` stub is present) and is pushed to the public registry on tag |

**Prompt seed.**
> Implement HANGAR Phase 0 per `docs/03_IMPLEMENTATION_ROADMAP.md`. Run `git init` first and
> commit the existing scaffold. Then create the Cobra command tree, Viper config with a `Secret`
> type that redacts in every serialisation path, a recursive slog redaction handler, the replica
> heartbeat loop, the Vite/React shell with exactly one stylesheet, and GitHub Actions for CI and
> release. Commit a minimal valid OpenAPI 3.1 stub at
> `docs/openapi.json` — the Docker build copies it and no image can build without it. Run
> `go mod tidy` and `pnpm install`, then commit `go.sum` and `pnpm-lock.yaml`; only the pins
> marked contractual in `go.mod` are fixed. Do not implement any domain logic. Exit when
> `make ci` passes and `docker compose up -d` is healthy from a blank environment. Also produce
> `docs/BASELINE.md` by measuring the five legacy repositories at HEAD.

---

## Phase 1 — Database Schema & Migrations *(split into 1a and 1b)*

The complete schema is ≈129 tables. SRS v3.0 scoped this as "~48 core tables", which covered only
the platform tier and made the phase unschedulable. **SRS v3.1 §5.2 splits it.** Both sub-phases
must land before Phase 2 — the route catalogue writes into 1a and the Phase 7–9 handlers write
into 1b. The split is for review tractability: reviewing 129 tables of DDL in one pass is how a
wrong identifier type gets waved through.

**Depends on.** Phase 0.

**Legacy reference.** `eveapi/src/Models/**` and `eveapi/src/database/migrations/**` for field
coverage. **Do not copy the shape** — legacy duplicates tables per owner where HANGAR uses owner
polymorphism (`02_DATABASE_SCHEMA.md` §5.1). Use legacy only to confirm no field is missing.

---

### Phase 1a — Platform tier (51 tables)

**Objective.** Identity and access, RBAC and squads, ESI gateway and sync metadata, the
cluster-shared rate governor state, provisioning, alerting, events, shared reference and open
vocabularies.

**Files.**

```
db/migrations/00001_schemas.sql … 000NN_platform_*.sql
db/queries/{user,character_token,rbac,squad,esi_route,esi_scope,esi_ledger,
            esi_error_budget,esi_replica,sync_subscription,provisioning,
            alert,outbox,open_vocabulary}.sql
db/seed/permissions.sql, alert_types.sql, roles.sql
internal/store/gen/**                    (generated, committed)
internal/domain/money.go, ids.go, vocabulary.go
```

**Design notes.** Follow `02_DATABASE_SCHEMA.md` §4 exactly — all 51 tables in the seven groups
given there. The three tables added by the ledger correction need particular care:

* `app.esi_ledger_bucket` and `app.esi_ledger_entry` are **UNLOGGED**, with the entry table
  cascading from the bucket. The bucket row is the lock that serialises acquire for that
  `(group, user_key)` and nothing else.
* `app.esi_ledger_entry.consumed_at` is the **response** timestamp (SRS v3.1 §4.1.3, defect B9).
  Reservations carry the issue time plus an `expires_at`, and are re-stamped on settle.
* `app.esi_replica` is the heartbeat registry that selects `solo` vs `clustered`. Phase 4 consumes
  it; Phase 1a creates it and proves the liveness predicate.

**Edge cases.**

* `goose down` must drop `esi_ledger_entry` before `esi_ledger_bucket` — the FK cascade covers
  runtime deletes, not migration ordering.
* `app.permission` is HANGAR's own closed set (SRS v3.1 Principle 14 scopes openness to *external*
  vocabularies). Seed it from Go and fail CI on divergence.
* `uuidv7()` — confirm it exists in PG18; if not, fall back to `gen_random_uuid()` plus a
  `created_at` index.
* UNLOGGED tables are not physically replicated. If an administrator adds streaming replication
  later, the ledger starts empty on the replica — which is safe, because reconciliation is
  conservative. Document it; do not make the tables LOGGED to "fix" it.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestGooseUpDownIdempotent` | clean up, clean down, clean up again on empty PG18 |
| `make verify-generated` | `sqlc generate` produces no diff |
| `TestAllPlatformTablesPresent` | all 51 tables of `02_…` §4 exist with the specified keys |
| `TestLedgerTablesUnloggedAndCascade` | both ledger tables are UNLOGGED; entry cascades from bucket |
| `TestReplicaLivenessPredicate` | two fresh heartbeats ⇒ 2 live; one aged past 30 s ⇒ 1 live |
| `TestPermissionSeedMatchesGoSet` | divergence between the Go set and the seeded rows fails CI |
| `TestNoDestructiveDMLInMigrations` | no `DELETE`/`TRUNCATE` in `db/migrations/` |

**Prompt seed.**
> Implement HANGAR Phase 1a. Write the Goose migrations for the 51 platform-tier tables in
> `docs/02_DATABASE_SCHEMA.md` §4, the sqlc query sources and the generated store package.
> `app.esi_ledger_bucket`, `app.esi_ledger_entry` and `app.esi_cache_entry` are UNLOGGED, and
> `consumed_at` on ledger entries is the response timestamp, not the issue timestamp. Create
> `app.esi_replica` with the 10 s heartbeat / 30 s liveness contract. `app.permission` is a
> HANGAR-owned closed set seeded from Go with a CI divergence check; all *external* vocabularies
> are `text` and never ENUM. No DELETE or TRUNCATE in any migration. Exit on the seven named tests.

---

### Phase 1b — Domain projection tier (≈78 tables)

**Objective.** The ESI datasets behind §6, owner-polymorphic wherever a concept exists for both
characters and corporations.

**Files.**

```
db/migrations/001NN_domain_*.sql
db/queries/{asset,wallet,contract,industry,market,killmail,character_*,
            corporation_*,mail,calendar,planet,sovereignty,project}.sql
internal/store/*.go                      (repository facades)
internal/domain/**                       (entities, invariants)
cmd/hangar/admin_verify_identifiers.go
```

**Design notes.** Follow `02_DATABASE_SCHEMA.md` §5. Owner polymorphism (`owner_kind`,
`owner_id`) is a requirement, not a preference: it is what keeps the tier at 78 tables and makes
the character and corporation handlers one implementation. Partition `wallet_journal`,
`wallet_transaction`, `character_notification`, `killmail` and `market_history` monthly, with the
partition key inside every PK.

**Edge cases.**

* `goose down` on a partitioned table must drop partitions before the parent.
* The `DEFAULT` partition must exist from the creating migration, or a backfill outside the
  created range fails.
* `IS DISTINCT FROM`, not `<>`, in every `DO UPDATE … WHERE` — otherwise NULL→NULL registers as a
  change and `updated_at` churns forever.
* sqlc must not emit `float64` anywhere. If it does, the override is missing.
* Standings and positions are `double precision`, **not** money. Quantities and runs are `bigint`.
  Only actual ISK is `NUMERIC(30,2)`.
* `app.corporation_project.project_id` is `uuid` supplied by CCP — not generated here, not stored
  as `text`.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestAllDomainTablesPresent` | schema-diff against `02_…` §5.2 |
| `TestNoFloatOnMoneyPaths` | reflection over `internal/domain` + `internal/api/dto` finds zero `float64` on money fields |
| `TestIdentifierTypesMatchSpec` | every identifier column matches the spec's declared type, **including `uuid` columns** |
| `TestAssetTreeRecursiveCTE` | 5-level nesting resolves; an injected cycle terminates at the depth bound |
| `TestPartitionMaintenanceCreatesThreeMonths` | fast-forwarded clock creates ahead, never behind |
| `TestUpsertGuardUsesIsDistinctFrom` | re-applying identical data changes no `updated_at` |

**Prompt seed.**
> Implement HANGAR Phase 1b. Write the Goose migrations for the ~78 domain projection tables in
> `docs/02_DATABASE_SCHEMA.md` §5, using owner-polymorphic `(owner_kind, owner_id)` keys wherever
> a concept exists for both characters and corporations. All money is `NUMERIC(30,2)` →
> `decimal.Decimal`; standings, positions, quantities and runs are not money. Identifier column
> types come from the OpenAPI spec — `uuid` where the spec says `format: uuid`, never coerced to
> bigint or text. Partition the five time-series tables monthly with a DEFAULT partition. Ship
> `hangar admin verify-identifier-types`. Exit on the six named tests.

---

## Phase 2 — Route Catalogue (OpenAPI Ingest)

**Objective.** Map ESI's `openapi.json` into `app.esi_route`.

**Depends on.** Phase 1a **and** Phase 1b. The catalogue writes into 1a, but the Phase 2
identifier-type check compares against columns that only exist after 1b.

**Legacy reference.** `eveapi/src/Jobs/**` — each job's `$endpoint`, `$version` and `$scope`
properties are the legacy equivalent of a catalogue row. Use them to enumerate the 106 routes
HANGAR must end up subscribing to. **Note the legacy defect not to reproduce:** ad-hoc
per-call pinning (`setCompatibilityDate('2025-08-09')` appears four times in `Jobs/Mail/`)
and ESI calls that bypass the endpoint registry entirely.

**Files.**

```
internal/esi/catalogue/boot.go, ingest.go, compatdate.go, pin.go, snapshot.go
internal/esi/catalogue/embedded/openapi.snapshot.json   (+ the D_max it was captured at)
db/queries/esi_route.sql
testdata/esi/openapi.minimal.json, openapi.synthetic-drift.json
```

**Design notes.** The boot sequence in `01_ARCHITECTURE.md` §5.1 is strictly ordered and the
two dates must never be conflated:

1. `GET /meta/compatibility-dates` → `D_max` = newest.
2. `GET https://esi.evetech.net/meta/openapi.json` with `X-Compatibility-Date: D_max`.
   **The app pin must not be used here.** Pinning discovery blinds the catalogue permanently.
3. Ingest every operation, persisting all six vendor extensions verbatim plus
   `upstream_path`, the declared scopes and `identifier_types`.
4. Routes whose `x-compatibility-date` is newer than the app pin → `blocked_by_pin = true`,
   excluded from scheduling, surfaced with a diff.
5. The pin is **never** advanced automatically.

Use `libopenapi` — the spec is OpenAPI 3.1 and `kin-openapi` mishandles `type: [x, "null"]`.

**Edge cases.**

* **11:00 UTC rollover.** Date arithmetic uses `now().UTC().Add(-11h).Truncate(24h)`. One
  function, table-tested; never inlined.
* **Future dates are rejected upstream.** Clamp `D_max` to the rollover-adjusted today.
* **Offline boot.** Fall back to the embedded snapshot, mark the catalogue
  `stale_snapshot`, and surface that state. A stale snapshot must never look like a live ingest.
* **Singular paths.** `/corporation/{corporation_id}/mining/extractions` and
  `/corporation/{corporation_id}/mining/observers[/{observer_id}]` are singular. This is the
  named test fixture. If any code path derives a path from a resource name, it is wrong.
* **Retired routes.** A route that vanishes from the spec is marked `retired_at`, never
  deleted — subscriptions reference it.
* **Unrecognised `x-cache-mode`.** Store the raw value in `app.open_vocabulary` and treat
  scheduling as `ttl-based`. Do not reject the route (Gate 6).
* **Non-zero floor.** An ingest that maps zero operations is a failure, not an empty success —
  a truncated download must not silently wipe the catalogue.

**Additional deliverable — the Gate 6 fixture (SRS v3.1 §7 Phase 2).** Author and commit
`test/drift/gate6_synthetic_spec.json` at the end of this phase, not at Phase 20. A gate fixture
written in response to a failure does not test what it claims to. See `04_RELEASE_GATES.md` §6.1
for its four required injected conditions.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestIngestMapsAllOperations` | operation count > 0 floor; every operation has a row |
| `TestSpecFetchedAtDMaxNotAppPin` | the spec request carries `D_max`; asserts it is **not** the pin |
| `TestRoutesNewerThanPinAreBlocked` | `blocked_by_pin = true` and excluded from the scheduling query |
| `TestCompatibilityDateRolloverAt1100UTC` | table-driven across the boundary in both directions |
| `TestOfflineBootUsesEmbeddedSnapshot` | network failure → snapshot loaded, `stale_snapshot` set |
| `TestUpstreamPathStoredVerbatim` | fixture `/corporation/{corporation_id}/mining/extractions` round-trips **singular** |
| `TestUnknownCacheModeDefaultsToTtlBased` | route ingested, value recorded, scheduling is `ttl-based` |

**Prompt seed.**
> Implement HANGAR Phase 2. Build the ESI route catalogue: resolve `D_max` from
> `/meta/compatibility-dates`, fetch `openapi.json` **at `D_max` and never at the app pin**,
> and ingest every operation into `app.esi_route` with all six vendor extensions,
> `upstream_path` verbatim, declared scopes and identifier types. Mark routes newer than the
> app pin `blocked_by_pin` and exclude them from scheduling. Never advance the pin
> automatically. Use `libopenapi` (the spec is 3.1). Exit on the seven named tests, including
> the singular-path fixture `/corporation/{corporation_id}/mining/extractions`.

---

## Phase 3 — ESI Gateway I (HTTP core & conditional cache)

**Objective.** Conditional requests via ETag / `Last-Modified`, and the two-tier cache.

**Depends on.** Phase 2.

**Legacy reference.** `eveapi/src/Services/Esi/**` and the `eveseat/eseye` client for the
legacy caching and header handling. HANGAR's cache key is materially different — it includes
the compatibility date and the resolved ESI language, which legacy does not.

**Files.**

```
internal/esi/transport/roundtripper.go, useragent.go, compatdate.go, retry.go
internal/esi/cache/l1.go, l2.go, key.go, conditional.go
internal/esi/pagination/cursor.go, page.go, torn.go
internal/i18n/resolve.go, locales.json          (needed early: the cache key depends on it)
db/queries/esi_cache.sql
```

**Design notes.** Cache key = `sha256(method ‖ normalized_path ‖ sorted_query ‖
compatibility_date ‖ tenant ‖ resolved_esi_language ‖ token_subject)`. Normalisation touches
the **request envelope only** — never a path segment.

**Edge cases.**

* `af` and `en` must produce the **same** cache key, because both resolve to ESI `en`. This is
  a named exit criterion, and it is the reason `internal/i18n` lands in Phase 3 rather than 16.
* `zh-CN` → `zh`: the region subtag is stripped.
* `x-cache-mode: no-cache` → no L1 write, no L2 write, **and no conditional headers sent**.
  All three, not just the first.
* Torn page sets: `Last-Modified` must match across every page. A mismatch discards the whole
  payload and retries. Partially committing a wallet journal loses transactions.
* A 304 must not clear a stored validator, and must not reset adaptive backoff.
* `X-Pages` absent on a page-paginated route → treat as one page, do not loop to infinity.
* Redis L2, when configured, must degrade to a miss on error — never to a request failure.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestConditionalRequestYields304` | stored ETag produces `If-None-Match`; 304 serves from cache |
| `TestTornPaginationDiscardsPayload` | mismatched `Last-Modified` across pages → nothing committed, retry scheduled |
| `TestNoCacheRouteWritesNothingAndSendsNoValidators` | asserts all three behaviours |
| `TestCacheKeyUsesResolvedEsiLanguage` | **`af` and `en` share one cache entry**; `zh-CN` maps to `zh` |
| `TestNormalizationNeverRewritesPathSegments` | singular mining paths survive normalisation unchanged |
| `TestRedisL2ErrorDegradesToMiss` | Redis failure produces a cache miss, not a request error |

**Prompt seed.**
> Implement HANGAR Phase 3. Build the ESI HTTP transport chain (user agent, unconditional
> `X-Compatibility-Date` from the app pin, retry) and the two-tier conditional cache
> (ristretto L1, Postgres UNLOGGED L2, optional Redis). The cache key must include the
> **resolved ESI language**, not the UI locale — so implement `internal/i18n` resolution now.
> Implement cursor and page pagination with torn-set detection. Exit on the six named tests,
> including `TestCacheKeyUsesResolvedEsiLanguage` proving `af` and `en` share an entry.

---

## Phase 4 — ESI Gateway II (Rate limiting & resilience)

**Objective.** The **cluster-shared** floating-window consumption ledger and the error-limit
engine.

**Depends on.** Phase 3, plus `app.esi_ledger_bucket` / `app.esi_ledger_entry` / `app.esi_replica`
from Phase 1a.

**Legacy reference.** None usable. Legacy SeAT's rate handling is one of the structural
inadequacies HANGAR exists to fix (SRS §1.1). Do **not** port it.

**Files.**

```
internal/esi/ratelimit/ledger.go, bucket.go, shard.go, reserve.go     (solo path)
internal/esi/ratelimit/shared.go, mode.go, flush.go                   (clustered path)
internal/esi/ratelimit/reconcile.go, errorlimit.go, parse.go
internal/esi/breaker/breaker.go
internal/telemetry/replica.go                                          (heartbeat loop)
internal/esi/ratelimit/ledger_bench_test.go
db/queries/esi_ledger.sql, esi_error_budget.sql, esi_replica.sql
```

**Design notes.** Read `01_ARCHITECTURE.md` §5.5–§5.7 in full first. The critical points:

* State is a **cost-weighted expiry ledger** of `(cost, consumed_at)` entries, not a bucket with a
  refill rate. **A continuous-refill token bucket is prohibited.**
* `available = max_tokens − Σ live costs − Σ live reservations`. Entries evicted on read.
* **Costs, evaluated in this order:** 429 = **0** (this overrides the 4XX rule); 2XX = 2;
  3XX = 1; other 4XX = 5; 5XX = 0; transport error, timeout or no response = 5.
* **Predictive reservation:** reserve the worst case (5) before issuing; settle to the observed
  cost after. Reserving optimistically lets a run of 4XX overdraw the window.
* `consumed_at` is the **response** timestamp; reservations are stamped at issue and expire at the
  request timeout. Using the response timestamp releases cost no earlier than the server does, so
  any error is in the safe direction.
* `X-Ratelimit-Limit` parses as `<max-tokens>/<window>`, suffix `m` or `h`.
* **The server always wins.** Reconcile every response: if the server reports less, inject a
  synthetic cost expiring a full window from now; if more, evict oldest entries until they agree,
  never exceeding `max_tokens`.

**The ledger is cluster-shared (SRS v3.1 §4.1.3, Principle 16).** ESI enforces the budget
installation-wide, so HANGAR accounts for it installation-wide. Two modes, **selected
automatically from the replica registry and never configured**:

| Mode | When | Behaviour |
| :-- | :-- | :-- |
| `solo` | exactly one live replica | in-process min-heap ledger; no DB round-trip. Bounded heap over a preallocated slice sized `max_tokens + 8`, sharded FNV-1a into `NumCPU()*4` shards. A heap, not a deque — responses settle out of order. |
| `clustered` | two or more live replicas | shared Postgres ledger; acquire is one short transaction taking `FOR UPDATE` on the bucket row only |

Heartbeat into `app.esi_replica` every 10 s; live = heartbeat under 30 s old. **Do not add a
configurable divisor or mode flag.** That was the rejected mitigation: a knob that can be set
wrongly reintroduces exactly the defect this design removes.

Governor 2 (error limit) is installation-wide and cluster-shared through a single Postgres row
with no solo fast path — a row touched only on error responses costs nothing worth optimising.
Pause at 20 remaining, resume at 60 (hysteresis, or the installation oscillates). Any observed 420
is a critical alert.

**Edge cases.**

* **429 with no rate-limit headers.** CCP's in-monolith limiters produce these and they are still
  live. Charge nothing; honour `Retry-After` if present, otherwise snooze for `ttl_floor`;
  increment `esi_429_headerless_total`. Do **not** infer `remaining = 0` (that stalls the
  installation) and do not ignore the 429.
* **Routes with no `x-rate-limit`.** Rate limiting is not active everywhere. Absence means
  Governor 2 alone applies — not "unlimited", and not "default bucket".
* **420 on a Governor 1 route.** The error limit applies globally, including to routes under
  Governor 1. A 420 is never a per-route condition.
* **Mode transitions.** `solo` → `clustered` must flush the in-process ledger into the shared
  table *before* admitting any further request; `clustered` → `solo` must read the shared table
  into memory before engaging the fast path. Neither may lose an entry or double-count one.
* **A replica that dies without deregistering** ages out of the registry after 30 s. During that
  window the survivors stay in `clustered` mode, which is the safe direction.
* Clock jumps: use a monotonic clock for in-process window arithmetic, injected for testability.
  The shared path uses `now()` on the database, which gives all replicas one clock.
* A reservation whose request never returns must expire at the request timeout and be charged the
  worst case, not silently reclaimed. In `clustered` mode the expiry sweep is part of the eviction
  step of the next acquire, so a crashed replica's reservations do not pin the bucket.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestLedgerFidelityAgainstFloatingWindow` | a simulated 15-minute window proves tokens return **exactly one window after each individual request**, and that a continuous-refill model diverges. **The test must fail if a refill model is substituted.** |
| `TestPredictiveReservationSurvives4XXRun` | a run of 4XX responses never overdraws the window |
| `Test429SnoozesExactlyRetryAfter` | the affected subscription snoozes for exactly `Retry-After`; siblings continue; cost charged is 0 |
| `Test429ExemptionOverrides4XXCost` | a 429 charges 0, not 5 |
| `TestTransportErrorChargesWorstCase` | a timeout charges 5 |
| `TestReconcilerHandlesHeaderless429` | no panic, no stall, no charge, metric incremented |
| `TestServerHeadersAlwaysWin` | divergence in both directions converges to the server value |
| `TestErrorLimitProactivePause` | pause fires at the configured remaining threshold **before** a 420 is observed; resume uses the higher threshold; 420 triggers a global pause and a critical alert |
| `TestModeSelectedFromReplicaRegistry` | one live replica ⇒ `solo`; a second heartbeat ⇒ `clustered`; expiry ⇒ back to `solo` |
| `TestModeTransitionLosesNoEntries` | both transition directions preserve the exact live-cost sum |
| `TestClusteredLedgerNeverExceedsMaxTokens` | **three concurrent replicas sharing one bucket never admit a request that takes aggregate consumption above `max_tokens`** |
| `TestClusteredReservationSurvivesReplicaCrash` | a killed replica's reservations expire and are reclaimed by the next acquire |
| `BenchmarkLedgerSolo1MOperations` | **< 2 seconds** for 1,000,000 acquire/settle pairs on the in-process path |
| `BenchmarkLedgerClusteredThroughput` | **≥ 2,000 acquire/settle pairs per second per replica at p99 < 10 ms** against a real PG18 |

**Prompt seed.**
> Implement HANGAR Phase 4: `internal/esi/ratelimit/`. Model ESI's floating window exactly — a
> cost-weighted expiry ledger of `(cost, consumed_at)` entries where each request's cost returns
> one window after *that request*. A continuous-refill token bucket is PROHIBITED. Reserve the
> worst-case cost (5) before issuing and settle to the observed cost; `consumed_at` is the
> **response** timestamp. Costs in precedence order: 429=0 (overrides the 4XX rule), 2XX=2, 3XX=1,
> other 4XX=5, 5XX=0, transport error=5.
>
> **The ledger is cluster-shared.** ESI enforces the budget installation-wide, so account for it
> installation-wide: a shared Postgres ledger (`app.esi_ledger_bucket` + `app.esi_ledger_entry`,
> both UNLOGGED) where acquire takes `FOR UPDATE` on the bucket row only. Select `solo`
> (in-process, no DB round-trip) versus `clustered` **automatically** from the `app.esi_replica`
> heartbeat registry — 10 s heartbeat, 30 s liveness. Do NOT add a configurable divisor or mode
> flag; an operator-settable knob reintroduces the defect this design exists to remove. Flush in
> both directions on mode transition.
>
> Reconcile against `X-Ratelimit-Remaining` on every response — the server always wins. Handle
> 429s with no rate-limit headers. Implement the installation-wide error-limit governor sharing
> one Postgres row, pausing at 20 remaining and resuming at 60. Exit on the fourteen named tests,
> including three replicas never exceeding `max_tokens` in aggregate, and a fidelity test that
> **fails if a refill model is substituted**.

---

## Phase 5 — EVE SSO & Token Lifecycle

**Objective.** PKCE SSO, offline JWT validation, envelope encryption, opaque scope catalogue.

**Depends on.** Phase 4.

**Legacy reference.** `eveapi/src/Models/RefreshToken.php` and `web/src/Http/Controllers/Auth/`
for the login flow shape. **Legacy calls `/verify`; HANGAR must not.** That endpoint began
redirecting 2026-03-24 and the redirect was removed 2026-04-28.

**Upstream.** https://developers.eveonline.com/docs/services/sso/

**Files.**

```
internal/sso/oauth.go, pkce.go, callback.go, refresh.go, lifecycle.go
internal/sso/jwks/cache.go, verify.go, claims.go
internal/crypto/envelope.go, keyring.go
internal/scopes/catalogue.go, require.go, reauthorize.go
cmd/hangar/admin_bootstrap_token.go
db/queries/character_token.sql, esi_scope.sql
```

**Design notes.**

* Authorization Code + PKCE S256, plus HTTP Basic client authentication (confidential client).
* `code_verifier` and `state` held server-side keyed by an `HttpOnly`, `SameSite=Lax` cookie.
  `state` is single-use with a 10-minute TTL.
* Validation: `alg = RS256` (reject `none` and HS*), `kid` resolvable in the cached JWKS,
  `iss` ∈ configured set, `aud` contains `EVE Online` **and** the client id, `exp`/`nbf`/`iat`
  within 60 s skew, `sub` matches `CHARACTER:EVE:<digits>`.
* JWKS: fetch at boot, refresh every 12 h, refetch on unknown `kid` **throttled to once per
  5 minutes**, persisted to `app.setting` so a cold offline boot still validates.
* Envelope: per-token DEK → AES-256-GCM → DEK wrapped with the master key.
  **AAD = `character_id ‖ key_version ‖ 'refresh_token'`.**
* Scopes are `text` primary keys, **opaque**. No parsing, no regex, no version matching.

**Edge cases.**

* **`scp` is a string when exactly one scope was granted, and an array otherwise.** A
  single-scope token is common (a Mumble-only linkage). Unmarshal into a type accepting both.
* **`owner` hash change** ⇒ the character was transferred to another EVE account ⇒ invalidate
  every stored token for that character immediately. This is an entitlement-reducing event and
  feeds `provision-urgent` in Phase 11.
* **Refresh rotation.** EVE SSO returns a new refresh token on every refresh and kills the old
  one. Two concurrent refreshes invalidate each other. Take
  `pg_advisory_xact_lock(hashtext('esi_refresh:'||character_id))`, re-read inside the lock,
  refresh, persist in the same transaction. Not an optimisation — a correctness requirement.
* **`invalid_grant`** ⇒ mark invalid, fire revocation, do not retry.
* **Two scope grammars are live simultaneously.** `esi-<group>.<action>.v1` (70 scopes as of
  2026-05-19) and `esi.<domain>.<subject>:<action>` (from 2026-08-04). Any code assuming the
  first rejects the second.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestAuthorizationCodePKCEEndToEnd` | full exchange against a stub SSO |
| `TestJWTValidatedOfflineNoNetwork` | validation succeeds with **all network denied**; asserts zero outbound calls |
| `TestJWKSUnknownKidRefetchThrottled` | at most one refetch per 5 minutes under a burst of unknown kids |
| `TestScpClaimAcceptsStringAndArray` | single-scope string form and multi-scope array form both parse |
| `TestOwnerHashChangeInvalidatesTokens` | changed `owner` invalidates every token for the character |
| `TestConcurrentRefreshSerialisedByAdvisoryLock` | 50 concurrent refreshes produce exactly one rotation |
| `TestEnvelopeAADRejectsMismatchedCharacter` | ciphertext moved to another character fails to decrypt |
| `TestScopeCatalogueIngestsBothGrammars` | both live grammars ingest; **an adversarial third grammar also ingests** and no regex rejects it |
| `TestBootstrapTokenIssues` | CLI issues a working admin token |

**Prompt seed.**
> Implement HANGAR Phase 5. Build EVE SSO Authorization Code + PKCE S256 with confidential
> client auth, **offline-only** JWT validation against a cached JWKS (there is no `/verify`
> endpoint — do not create a code path that could call one), AES-256-GCM envelope encryption
> with AAD bound to `character_id ‖ key_version ‖ 'refresh_token'`, and the opaque scope
> catalogue. Handle the `scp` claim being a string for single-scope tokens. Serialise refresh
> rotation with a Postgres advisory lock. Never parse, validate or pattern-check a scope
> string. Exit on the nine named tests, including an adversarial test proving an invented
> scope grammar is ingested rather than rejected.

---

## Phase 6 — Sync Engine (Planner & Subscriptions)

**Objective.** The job generator that evaluates `next_due_at`.

**Depends on.** Phase 5.

**Legacy reference.** `services/src/Console/Commands/**` and `eveapi/src/Jobs/**` for what gets
scheduled and how often. **Do not port the model:** legacy uses flat per-character sweeps;
HANGAR schedules granular `(entity, route)` subscriptions, which is what makes Principle 3
achievable.

**Files.**

```
internal/sync/subscription.go, duetime.go, cachemode.go, backoff.go
internal/sync/planner/loop.go, claim.go, leader.go, enqueue.go
internal/sync/normalize/*.go
db/queries/sync_subscription.sql, sync_run.sql
```

**Design notes.** Leader election via `pg_try_advisory_lock` on a dedicated connection. Claim
due work every 5 s with `SELECT … FOR UPDATE SKIP LOCKED`, enqueue to River **in the same
transaction**. River's unique-job option keyed on `(route_id, entity_kind, entity_id)` is the
second line of defence.

Cache-mode policy — all four cases, per `01_ARCHITECTURE.md` §6.2. Absent is the default and
`ttl-based`, and is also the fallback for any unrecognised future value.

**Edge cases.**

* `x-cache-age: 0` resolves to `ttl_floor`, **never 0**. Combined with `event-based` it means
  CCP declares no TTL contract.
* 1.5ⁿ backoff accumulates on consecutive **304s** and resets on any **200** — not on a 304.
* Full jitter on every computed `next_due_at`, or 5000 characters synchronise into a herd.
* `no-cache` routes are scheduled at `ttl_floor` **only for subscriptions that explicitly opt in**.
* `blocked_by_pin` routes are excluded from claiming entirely.
* A snoozed subscription (from a 429) must be excluded by the claim predicate, not filtered
  after claiming — otherwise the planner burns claims on work it cannot run.
* Losing the advisory lock mid-loop must abort the in-flight claim, not complete it.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestClaimIsAtomicUnderConcurrency` | `FOR UPDATE SKIP LOCKED` with N planners yields zero double-claims |
| `TestCacheModePolicyTableDriven` | **all four cases** including absent → `ttl-based` |
| `TestZeroCacheAgeResolvesToTtlFloor` | `x-cache-age: 0` never schedules at 0 |
| `TestAdaptiveBackoffOn304ResetOn200` | 1.5ⁿ growth capped at `backoff_cap`; reset only on 200 |
| `TestBlockedByPinExcludedFromScheduling` | blocked routes never enqueue |
| `TestPlannerSoakNoDuplicateJobs` | **30-minute soak creates zero duplicate jobs** |

**Prompt seed.**
> Implement HANGAR Phase 6: the sync planner. Leader-elect via Postgres advisory locks, claim
> due `app.sync_subscription` rows every 5 s with `FOR UPDATE SKIP LOCKED`, and transactionally
> enqueue to River. Implement all four `x-cache-mode` cases — absent defaults to `ttl-based`
> and is also the fallback for unrecognised values. `x-cache-age: 0` resolves to `ttl_floor`,
> never 0. Apply 1.5ⁿ backoff on consecutive 304s, reset on 200 only. Full jitter everywhere.
> Exclude `blocked_by_pin` and snoozed subscriptions in the claim predicate. Exit on the six
> named tests including a 30-minute zero-duplicate soak.

---

## Phase 7 — Route Handlers I (Character core)

**Objective.** Character endpoints with idempotent bulk upserts.

**Depends on.** Phase 6.

**Legacy reference.** `eveapi/src/Jobs/Character/**`, `Skills/**`, `Clones/**`, `Contacts/**`,
`Assets/Character/**`. For each job, note the `$endpoint` and the model it writes.

**Scope.** Skills, skillqueue, attributes, clones, implants, contacts, contact labels,
standings, titles, roles, medals, loyalty points, agent research, fatigue, corporation history,
location/online/ship.

**Files.**

```
internal/sync/handlers/character_*.go
internal/store/character_*.go
db/queries/character_*.sql
testdata/esi/character/*.json          (recorded responses — golden files)
```

**Design notes.** DTOs must reflect the **2026-08-04 pin**: on `GET /characters/{character_id}`,
`title_id` is renamed `corporation_title_id`, and `character_title_id` plus `achievement_score`
were added 2026-06-09. A DTO carrying the old `title_id` is a pin violation.

Every upsert uses the `IS DISTINCT FROM` guard from `02_DATABASE_SCHEMA.md` §3.5.

**Edge cases.**

* `/characters/{id}/online` returns `last_login`/`last_logout` as null for a character who has
  never logged in since the field existed.
* `/characters/{id}/ship` can 404 for a docked character on some paths — a 404 here is *data*,
  not an error, and must not trip the circuit breaker.
* Skill queue entries can have null `finish_date` (paused queue).
* Standings and contacts both carry a `standing` float in ESI — this is a *standing*, not
  money; `double precision` is correct and `NUMERIC(30,2)` is wrong.
* Corporation history has null `end_date` on the current entry.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestGoldenFileParsesAll<Domain>` | every recorded ESI response parses into the DTO with no field loss |
| `TestSecondSyncProducesZeroUpdatedAtChanges` | re-syncing unchanged data changes no `updated_at` |
| `TestCharacterDTOMatchesPin20260804` | `corporation_title_id`, `character_title_id`, `achievement_score` present; no `title_id` |
| `TestDataLevel404DoesNotTripBreaker` | a 404 on `/ship` is recorded as data, not as a failure |

**Prompt seed.**
> Implement HANGAR Phase 7: character core route handlers. Skills, skillqueue, attributes,
> clones, implants, contacts and labels, standings, titles, roles, medals, loyalty points,
> agent research, fatigue, corporation history, location/online/ship. Every write is an
> idempotent bulk upsert with an `IS DISTINCT FROM` guard so `updated_at` changes only on real
> change. DTOs must match the 2026-08-04 pin (`corporation_title_id`, `character_title_id`,
> `achievement_score`). Reference `eveseat/eveapi` `src/Jobs/Character/**` for field coverage
> only — do not copy the flat-sweep scheduling model. Exit on the four named tests.

---

## Phase 8 — Route Handlers II (Corporation & wallets)

**Objective.** Corporation endpoints and the partitioned exact-money ledgers.

**Depends on.** Phase 7.

**Legacy reference.** `eveapi/src/Jobs/Corporation/**`, `Wallet/**`, `Industry/**`,
`Killmails/**`. Ledger aggregation logic lives in `web/src/Http/Controllers/Corporation/`.

**Scope.** Members, member tracking, member limits, **member titles**, roles, role history,
divisions, facilities, wallets, wallet journal, wallet transactions, shareholders, standings,
medals and issued medals, container logs, customs offices, **starbases and starbase detail**,
**structures, skyhooks and sovereignty hubs**, ledgers (bounties, PI, mining), mining
extractions and observers, alliance history.

**Files.**

```
internal/sync/handlers/corporation_*.go, wallet_*.go, structure_*.go, mining_*.go
internal/store/corporation_*.go, wallet_*.go
db/queries/corporation_*.sql, wallet_*.sql
testdata/esi/corporation/*.json
```

**Design notes.** Acting-character election (`01_ARCHITECTURE.md` §6.3) is load-bearing here:
every corp route needs a director token honouring `x-required-roles`, with deterministic
ordering and automatic fallback on 403.

**Starbase detail is a Phase 14 prerequisite.** `app.starbase_detail.fuels` is the source for
`corporation.starbase.fuel_low`; the Phase 14 build-time check fails without it.

**Edge cases.**

* **Singular upstream paths.** `/corporation/{corporation_id}/mining/extractions`,
  `/mining/observers`, `/mining/observers/{observer_id}`. Read them from `upstream_path`; never
  construct them.
* Wallet journal is **page-paginated** with `X-Pages`, and the `Last-Modified`-must-match rule
  applies. A torn journal loses money.
* Division 1 is the master wallet; characters have no divisions and use `division = 1`.
* `ref_type` is an open vocabulary. An unseen value is recorded in `app.open_vocabulary` and
  the row is stored — never dropped.
* Member tracking requires the `Director` role; a corp with no director token has no data,
  which must render as *unavailable*, not as an empty member list.
* Container logs are only retained upstream for a limited window; a gap is normal, not a bug.
* Structure `fuel_expires` can be absent for an unfuelled structure — absent ≠ expired.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestWalletPagePaginationAndTornDetection` | full page walk; a mismatched `Last-Modified` discards and retries |
| `TestExactMoneyRoundTrip` | `NUMERIC(30,2)` → `decimal.Decimal` → JSON string with no precision loss at 10²⁰ |
| `TestStarbaseDetailPopulatesFuelBay` | `app.starbase_detail.fuels` populated from a recorded fixture |
| `TestSkyhookAndSovereigntyHubRoundTrip` | detail endpoints round-trip |
| `TestSingularMiningPathsUsedVerbatim` | the request URL is singular, sourced from `upstream_path` |
| `TestActingCharacterFallbackOn403` | a 403 re-elects deterministically and does not disable the subscription |

**Prompt seed.**
> Implement HANGAR Phase 8: corporation route handlers and partitioned exact-money ledgers.
> Members/tracking/limits/titles, roles and history, divisions, facilities, wallets and
> journals, shareholders, standings, medals, container logs, customs offices, **starbases and
> starbase detail**, **structures, skyhooks and sovereignty hubs**, ledgers, mining extractions
> and observers. Money is `NUMERIC(30,2)` end to end and a JSON string on the wire. Use
> `upstream_path` verbatim — the mining routes are singular. Implement acting-character
> election with deterministic ordering and 403 fallback. Starbase detail must populate the fuel
> bay that Phase 14's fuel-low alert depends on. Exit on the six named tests.

---

## Phase 8.1 — Skyhook & sovereignty-hub reagent fixup

**Objective.** Correct a Phase 8 schema defect before it becomes load-bearing for Phase 14's
alert catalogue: skyhooks and sovereignty hubs are reagent-powered structures, not fuel-powered
ones, and Phase 8 modeled both with the same `fuel_expires` column
`corporation_structure`/`corporation_starbase` legitimately use.

**Depends on.** Phase 8.

**Trigger.** A post-Phase-8 review of the worker wiring found two compounding issues in the same
area: (1) neither `GET .../structures/skyhooks/{skyhook_id}` nor
`.../sovereignty-hubs/{sovereignty_hub_id}` returns anything resembling a fuel-expiry timestamp
anywhere in the live embedded spec — both use a **reagent bay** instead
(`reagents: [{type_id, secured_stock, unsecured_stock, last_cycle}]`); `fuel_expires` would have
sat permanently `NULL`, which is worse than merely unpopulated because it implies a fuel-low
alert capability that could never fire. (2) Phase 8 never wired the skyhook/sovereignty-hub
**list** routes into `CorporationWorker` at all, and left the starbase/skyhook/sovereignty-hub
**detail** and mining-observer-record routes — each needing a dynamic per-item path/query
parameter — unwired ("scoped out... handlers exist, dispatch doesn't"). Since Phase 14's
build-time check depends on `app.starbase_detail.fuels` actually being populated at runtime, and
no later phase revisits corporation structures, this could not be deferred to Phase 9.

**Scope.**

* `app.corporation_skyhook`/`app.corporation_sovereignty_hub`: drop `fuel_expires`, add
  `reagents jsonb NOT NULL DEFAULT '[]'` (mirroring `starbase_detail.fuels`'s existing shape);
  add `corporation_skyhook.is_active boolean`.
* Relax `corporation_skyhook.type_id`/`system_id` and `corporation_sovereignty_hub.type_id` to
  nullable — none of the three is obtainable from ESI pre-SDE (a skyhook/sovereignty-hub has
  exactly one real type in EVE, but neither list nor detail ever echoes it; skyhook `system_id`
  needs the `planet_id -> planet -> system` SDE join). Left `NULL` rather than guessed —
  Principle 13 forbids a silent hardcoded constant with no verifiable source.
  `corporation_sovereignty_hub.system_id` has no such problem (the list gives `solar_system_id`
  directly) and stays `NOT NULL`.
* Wire the four previously-unwired fan-outs into `CorporationWorker`: starbase detail (the named
  Phase 14 prerequisite), skyhook list + detail, sovereignty-hub list + detail, and
  mining-observer-records detail. Each detail fan-out reads the already-synced parent list from
  the database and makes one call per known id — a 403 on the first item aborts the remaining
  calls (role failures are homogeneous across a corporation's own items), a 404 on any one item
  is data (the item vanished between list and detail calls), never a failure.

**Files.**

```
db/migrations/00033_phase8_1_skyhook_reagent_fixup.sql
db/queries/corporation_structure.sql                    (skyhook/sovereignty-hub queries rewritten)
internal/sync/handlers/corporation_deployables.go        (reagent DTOs, list+detail Sync functions)
internal/sync/worker/corporation.go                      (fanoutDetail + four fan-out methods)
testdata/esi/corporation/skyhook_detail.json              (updated)
testdata/esi/corporation/sovereignty_hub_detail.json       (new)
```

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestStarbaseDetailPopulatesFuelBay` | `app.starbase_detail.fuels` populates from a recorded fixture |
| `TestSkyhookAndSovereigntyHubRoundTrip` | both detail endpoints round-trip through a real database, including a second-sync idempotency check |
| `TestCorporationDTOsMatchLiveSpec` (extended) | skyhook/sovereignty-hub detail DTOs carry `reagents`/`reagent_bay`, never `fuel_expires` |
| `TestSingularMiningPathsUsedVerbatim` (extended) | the mining-observer-records detail fan-out also uses the singular `/corporation/...` form |

**Prompt seed.**
> Implement HANGAR Phase 8.1: correct `app.corporation_skyhook`/`app.corporation_sovereignty_hub`
> to model a reagent bay instead of a fuel expiry (neither structure type has a fuel-expiry
> concept in the live spec), relax the three genuinely SDE-blocked identifier columns to
> nullable rather than guessing at them, and wire the starbase/skyhook/sovereignty-hub detail
> and mining-observer-record fan-outs `CorporationWorker` left unwired. Exit on the four named
> tests.

---

## Phase 9 — Route Handlers III (Assets, contracts, market, notifications, SDE)

**Objective.** The remaining complex datasets plus SDE streaming.

**Depends on.** Phase 8.

**Legacy reference.** `eveapi/src/Jobs/Assets/**`, `Contracts/**`, `Mail/**`,
`PlanetaryInteraction/**`, `Calendar/**`, `Market/**`, `Killmails/**`. Notification YAML
handling: `notifications/src/**` plus `eveapi/src/Models/Character/CharacterNotification.php`.

**Upstream.** https://developers.eveonline.com/docs/services/static-data/

**Scope.** Asset sync with soft delete and the recursive tree, **contract items and bids**,
**mail bodies**, **PI colony detail**, **calendar event detail**, **corporation project detail
and per-character contribution**, market orders/history/prices, notification YAML parsing,
SDE JSONL streaming.

**Files.**

```
internal/sync/handlers/asset_*.go, contract_*.go, mail_*.go, planet_*.go,
                       calendar_*.go, project_*.go, market_*.go, notification_*.go
internal/sde/stream.go, import.go, swap.go, manifest.go
internal/alerting/render/generic.go        (the fallback renderer lands here, used in 14)
testdata/esi/**, testdata/notifications/*.yaml
```

**Design notes.** `app.corporation_project.project_id` is `uuid` **from CCP** — not generated
here, not stored as text. It joins against `bigint character_id` in
`corporation_project_contribution` with no coercion. This is the Principle 13 proof case.

SDE atomic swap: build into `sde_next`, verify, rename in one short transaction, drop the old
schema outside it (`02_DATABASE_SCHEMA.md` §6).

**Edge cases.**

* **Asset reconciliation.** Items missing from a full sync are **soft-deleted**, not deleted.
  An item that reappears is restored rather than re-inserted.
* **Asset location cycles.** A torn sync can produce a container graph with a cycle. Both the
  depth bound and the `NOT item_id = ANY(path)` guard are required.
* **Unparseable notification YAML.** Store as JSONB, render generically, list on the
  unknown-types board. **The queue must never halt.** This is Principle 14 in its most
  operationally important form.
* CCP notification YAML is *not* always valid YAML — some payloads contain unquoted values that
  trip strict parsers. A parse failure is an expected path, not an exception.
* **Mail bodies** are one request per mail. Legacy invokes this inline, bypassing the endpoint
  registry — HANGAR must route it through the catalogue like every other call.
* Contract items for a *courier* contract are empty by design; empty ≠ failed.
* Market history is bulk and region-scoped; it is the largest partitioned table.
* SDE download is large and compressed. Stream it; never buffer it entirely in memory.
* A failed SDE import must leave the live `sde` schema untouched.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestAssetReconciliationSoftDeletesMissing` | missing items soft-deleted; reappearing items restored |
| `TestUnparseableNotificationYAMLImportsAsJSONB` | a malformed fixture imports, renders generically, and **does not halt the queue** |
| `TestSDEAtomicSwap` | live schema untouched on failure; swap is atomic on success |
| `TestContractItemsMailBodiesColonyDetailRoundTrip` | each round-trips from a recorded fixture |
| `TestUUIDKeyedProjectInsertAndJoin` | `uuid` PK inserts and joins against `bigint` **without coercion** |
| `TestMailBodyRoutedThroughCatalogue` | the mail-body call uses a catalogue route, not an inline URL |

**Prompt seed.**
> Implement HANGAR Phase 9. Asset sync with soft delete and the bounded, cycle-guarded
> recursive tree; contract items and bids; mail bodies (routed through the route catalogue, not
> inline); PI colony detail; calendar event detail; corporation project detail and per-character
> contribution with **`uuid` project ids that are never coerced**; market orders, history and
> prices; notification YAML parsing where an unparseable payload imports as JSONB and renders
> generically without halting the queue; and streaming SDE JSONL import with an atomic schema
> swap. Exit on the six named tests.

---

## Phase 10 — RBAC & Authorization

**Objective.** The SQL-backed grant model and effective-permission resolution.

**Depends on.** Phase 9.

**Legacy reference.** `web/src/Models/Acl/**` and `web/src/Http/Middleware/Authorize.php`.
Note that legacy's `RoleController` grant model is **not** translatable to HANGAR's — Appendix C
documents it as breaking with no shim.

**Files.**

```
internal/rbac/resolve.go, grant.go, materialize.go, permissions.go
internal/api/middleware/authorize.go
db/queries/rbac.sql
db/seed/permissions.sql
```

**Design notes.** `app.permission` is HANGAR's own closed Go set (the deliberate exception to
Principle 14) and is seeded from Go with a CI divergence check. Deny beats allow absolutely, at
every level and from every source. `app.effective_permission` is materialised and refreshed on
grant change; a correctness test asserts it agrees with a from-scratch recomputation.

**Edge cases.**

* A deny on a role the user holds beats an allow from *any* other source, including a direct
  user grant and a squad grant.
* A user with zero roles gets zero permissions — not "default allow".
* Superuser is a permission, not a bypass branch in code. A bypass branch cannot be denied.
* Materialisation must be transactionally consistent with the grant change that triggered it.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestDenyPrecedesAllowTruthTable` | exhaustive over all seven grant sources × {allow, deny, absent} |
| `TestMaterializedMatchesRecomputed` | materialised table agrees with a from-scratch resolution for 1000 random users |
| `TestNoRolesMeansNoPermissions` | default is deny |
| `BenchmarkResolve5000Users` | **< 2 ms** per resolution at 5000 users |

**Prompt seed.**
> Implement HANGAR Phase 10: RBAC. SQL-backed grants across seven sources with **absolute deny
> precedence**, a materialised `app.effective_permission` refreshed transactionally on grant
> change, and the authorization middleware. `app.permission` is a closed Go set seeded into the
> database with a CI divergence check. Superuser is a permission, not a code bypass. Exit on
> the four named tests: an exhaustive deny-precedence truth table and a < 2 ms resolution
> benchmark at 5000 users.

---

## Phase 11 — Access Provisioning Core (Entitlements & revocation)

**Objective.** The entitlement engine, reconciliation, and the < 60 s revocation SLO.

**Depends on.** Phase 10.

**Legacy reference.** The abandoned third-party connector plugins (`seat-connector` and its
drivers). HANGAR elevates this to a core subsystem precisely because the plugins were
abandoned — treat legacy as a **requirements source, not a design source**.

**Files.**

```
internal/provisioning/entitlement/evaluate.go, sources.go, preview.go
internal/provisioning/reconcile.go, urgent.go, strictmode.go, driver.go
internal/api/v1/admin_provisioning.go
db/queries/provisioning.sql
```

**Design notes.** The entitlement engine is a **pure function** — no I/O, no clock, no
randomness. That is what makes the dry-run preview trivially correct: it is the same function
against a hypothetical rule set.

Revocation: every entitlement-reducing event enqueues to `provision-urgent` **in the mutating
transaction**. `provision-urgent` never shares a worker pool with `provision-bulk`.

Measure p99 over `platform_call_completed_at − event_at` from `app.provisioning_audit` —
`event_at` is the **originating event**, not job start, because queue wait is the part that
fails under load.

**Edge cases.**

* **Strict Mode is "any alt".** One invalid token on any of a user's characters denies platform
  access for the whole user. The named test fails a user whose *alt* is invalid while their
  main is fine.
* A user linked to a platform but with zero entitlements must be **removed**, not left alone.
* A revocation for a platform that is down must retry and remain visible on the exposure board
  with its true age — never be marked complete.
* An `owner` hash change (Phase 5) is an entitlement-reducing event.
* Deleting an entitlement rule reduces entitlements for everyone it matched — that is a bulk
  urgent revocation, not a background reconcile.
* Preview must return **exact** gains and losses, per user, not counts.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestStrictModeFailsUserWhenAnyAltInvalid` | main valid + alt invalid ⇒ denied |
| `TestRevocationEnqueuedInSameTransaction` | rolling back the mutation also rolls back the job |
| `TestRevocationP99Under60sAtLoad` | **5000 identities across 3 platforms, p99 < 60 s** measured from `event_at` |
| `TestDryRunPreviewExactGainsAndLosses` | preview lists the exact users and groups affected |
| `TestUrgentQueueNotStarvedByBulk` | a full bulk reconcile in flight does not delay an urgent revocation |

**Prompt seed.**
> Implement HANGAR Phase 11: the access provisioning core. A **pure** entitlement engine over
> seven grant sources with absolute deny precedence; Strict Mode as a mandatory precondition
> where any invalid alt token denies the whole user; and synchronous revocation — every
> entitlement-reducing event enqueues to a dedicated `provision-urgent` queue **inside the
> mutating transaction**, with p99 event-to-platform-call under 60 s. Record `event_at` and
> `platform_call_completed_at` in `app.provisioning_audit` for Gate 2 evidence. Exit on the
> five named tests.

---

## Phase 12 — Discord Driver

**Objective.** A Discord driver that survives Discord's rate limits and Cloudflare.

**Depends on.** Phase 11.

**Legacy reference.** The abandoned `seat-discord-connector`. Requirements source only.

**Upstream.** https://discord.com/developers/docs/intro

**Files.**

```
internal/provisioning/drivers/discord/client.go, buckets.go, budget.go,
                                      cloudflare.go, hierarchy.go, driver.go
```

**Design notes.** Hand-rolled HTTP client, not `discordgo` — the bucket accounting,
invalid-request budget and 1015 detection all need raw headers.

Key on the returned `X-RateLimit-Bucket`, **not** on the URL. Enforce the 50/s global ceiling.
Track the 10,000-per-10-minutes invalid-request budget (401/403/429): warn at 50 %, **pause at
80 %**. API version from config against an allowlist, validated at boot.

**Edge cases.**

* **Cloudflare 1015 arrives outside Discord's framing**: either an HTML body containing
  `error code: 1015`, or JSON `{"code": 40333}`, on a 429 *or* a 403. Sniff content-type **and**
  body prefix — a JSON decode of the HTML form fails and must not be reported as a transport error.
* **Role hierarchy.** Refuse, proactively, any assignment of a role at or above the bot's
  highest role position, and any operation against the guild owner. Attempting and failing burns
  invalid-request budget — the very resource being protected. Cache bot member and role
  positions for 60 s, invalidating on 403.
* `X-RateLimit-Global` and `X-RateLimit-Scope: global` mean the whole client pauses, not one bucket.
* A member who has left the guild returns 404 on role operations — that is *reconciled state*,
  not an error.
* `X-RateLimit-Reset-After` is a float in seconds; do not truncate it to an integer.
* Shared-resource 429s (`scope: shared`) must not be charged against the invalid budget the
  same way — check `X-RateLimit-Scope` before accounting.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestAPIVersionAllowlistEnforced` | a version outside the allowlist fails at config validation, not at first request |
| `TestPauseAt80PercentInvalidBudget` | processing halts at 80 % of the 10k/10min budget |
| `TestCloudflare1015ParsedFromHTMLAndJSON` | both framings detected; neither reported as a transport error |
| `TestRoleHierarchyGuardBlocksAboveBot` | assignment above the bot's position is refused **without issuing a request** |
| `TestBucketKeyedOnHeaderNotURL` | two URLs sharing a bucket share a limiter |

**Prompt seed.**
> Implement HANGAR Phase 12: the Discord provisioning driver. Hand-rolled HTTP client (not
> discordgo). Track per-route buckets keyed on `X-RateLimit-Bucket`, enforce the 50/s global
> ceiling, and track the 10,000-per-10-minutes Cloudflare invalid-request budget with a warn at
> 50 % and a **pause at 80 %**. Detect undocumented Cloudflare 1015 bans delivered as HTML
> (`error code: 1015`) or as `{"code": 40333}` without standard 4XX framing. Proactively refuse
> role assignments at or above the bot's hierarchy position **without issuing the request**. API
> version from config against an allowlist validated at boot. Exit on the five named tests.

---

## Phase 13 — TeamSpeak & Mumble Drivers

**Objective.** TS3 WebQuery and Mumble gRPC provisioning.

**Depends on.** Phase 12.

**Upstream.** https://community.teamspeak.com/c/teamspeak-3-server ·
https://www.mumble.info/documentation/ · https://grpc.io/docs/guides/

**Files.**

```
internal/provisioning/drivers/teamspeak/webquery.go, escape.go, challenge.go, driver.go
internal/provisioning/drivers/mumble/grpc.go, acl.go, authenticator.go, driver.go
internal/api/v1/public_mumble_auth.go
```

**Design notes.**

* **TeamSpeak.** WebQuery over HTTP with `x-api-key`. Identity is `client_unique_identifier`
  (base64 UID), bound through a **single-use challenge token** recorded with `consumed_at`.
* **Mumble.** gRPC MurmurRPC for ACL group add/remove; optional external-authenticator mode via
  the bidirectional `AuthenticatorStream` for absolute connection denial.
* `POST /api/v1/public/mumble/auth` is signed with a shared secret and is the only
  unauthenticated write route in the API. It must be rate-limited and audited.

**Edge cases.**

* **TS3 query escaping** (`\s` space, `\p` pipe, `\/` slash, `\\` backslash) applies to values
  even over WebQuery. Escape outbound and unescape inbound — a corporation name with a space
  otherwise silently truncates the command.
* TS3 returns errors as `error id=... msg=...` **inside a 200 response**. HTTP status is not the
  error signal; parse the body.
* A challenge token redeemed twice must fail the second time.
* Mumble ACL group membership is per-channel; HANGAR manages the root channel unless configured
  otherwise, and a group removal on the wrong channel silently does nothing.
* External-authenticator mode must **fail closed on HANGAR being unreachable** only if the
  administrator opts in — failing closed by default locks everyone out during a HANGAR restart.
  Make this an explicit, documented configuration choice.
* **ZeroC Ice is out of scope for this phase** (SRS v3.1 §4.3, §9.3, defect B4). v3.0 required an
  in-binary Ice fallback; that is unsatisfiable alongside §9.2's static binaries, because no
  maintained Go Ice binding exists and linking Ice requires CGO. gRPC is the only in-binary
  driver. Ice connectivity ships separately as `hangar-mumble-ice-bridge`, a companion container
  exposing the same gRPC contract, addressed via `HANGAR_MUMBLE_ICE_BRIDGE_ADDR`. **Do not add an
  Ice dependency to `go.mod`.** No release gate depends on the bridge.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestChallengeTokenSingleUse` | verified then immediately consumed; second redemption fails |
| `TestTS3EscapingRoundTrip` | values containing spaces, pipes and slashes survive both directions |
| `TestTS3ErrorInsideHTTP200Detected` | `error id=` in a 200 body is treated as a failure |
| `TestMumbleGRPCAddsAndRemovesACLGroups` | against a stub MurmurRPC server |
| `TestExternalAuthenticatorDeniesConnection` | denial mode refuses the connection outright |

**Prompt seed.**
> Implement HANGAR Phase 13: the TeamSpeak and Mumble drivers. TeamSpeak over TS3 WebQuery with
> `x-api-key`, mapping `client_unique_identifier` via single-use challenge tokens, with correct
> TS3 query escaping and detection of `error id=` responses returned inside HTTP 200. Mumble
> over gRPC MurmurRPC for ACL group management, plus optional external-authenticator mode for
> absolute connection denial and the signed `POST /api/v1/public/mumble/auth` endpoint. gRPC is
> the ONLY in-binary Mumble driver — ZeroC Ice ships as a separate out-of-process bridge on the
> same gRPC contract, so do not add an Ice dependency to `go.mod`. Fail-closed behaviour in
> external-authenticator mode is an explicit administrator opt-in. Exit on the five named tests.

---

## Phase 14 — Alerting & Notifications

**Objective.** The delivery pipeline with coalescing and dead-lettering.

**Depends on.** Phase 13 (drivers) and Phase 8 (starbase detail).

**Legacy reference.** https://github.com/eveseat/notifications —
`src/Notifications/**` is the authoritative source for all **54** concrete types and their
payload fields. `notifications.integrations.php` lists the three delivery integrations.
Exclude abstract classes and traits from the count.

**Upstream.** https://api.slack.com/messaging/webhooks

**Files.**

```
internal/alerting/catalogue/seed.go, domains.go, thresholds.go
internal/alerting/interpret.go, route.go, coalesce.go, dedupe.go, deadletter.go
internal/alerting/channels/smtp.go, slack.go, discord.go
internal/alerting/render/*.go
db/seed/alert_types.sql
testdata/notifications/*.yaml
```

**Design notes.** 54 types across eight domains: Structures **23** (incl. 5 Skyhook),
Characters 7, platform 7, **Wars 6**, Corporations 5, Sovereignty 4, Contracts 1, Alliances 1.
The seed count and per-domain counts are asserted at build time.

> **Corrected in Phase 14.1.** This line originally read "Structures 22", and its eight numbers
> summed to 53 against the stated total of 54. Phase 14 reported the inconsistency rather than
> reconciling it silently; Phase 14.1 measured the upstream and established that the total was
> right and the Structures figure was understated by one. See Phase 14.1's section below and
> docs/BASELINE.md §4a.

**Wars have no dedicated table** — the six war alerts are notification-derived and §6 exposes no
wars endpoint. Do not invent one.

Every **threshold** alert declares `source_route_id`, and a threshold alert whose source route
is not in the sync set is a **build-time error**. Structure fuel → `/corporations/{id}/structures`;
starbase fuel → `/corporations/{id}/starbases/{starbase_id}` (the *detail* route).

**Edge cases.**

* **`char-notification` bucket is 15 tokens / 15 minutes** — extremely tight. Poll at 600 s with
  jitter and hold a **permanent 5-token reserve** so an interactive refresh or a retry can never
  exhaust it.
* An unrecognised CCP type delivers via the **generic key/value fallback renderer** and lands on
  the unknown-types board. It never dead-letters and never halts the queue.
* Coalescing key is `(routing target, alert type)`. 40 events inside the window render as one
  message.
* Dedupe hash must be stable across restarts — hash the payload's semantic fields, not a
  serialisation that includes a timestamp or map ordering.
* Slack and Discord webhook payloads have different size limits; a coalesced roll-up of 40 events
  can exceed both. Truncate with an explicit "and N more" rather than failing delivery.
* An SMTP failure must retry with backoff and eventually dead-letter — never block the queue.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestAlertCatalogueSeeds54AcrossEightDomains` | exact per-domain counts including Wars 6 and Skyhooks 5 |
| `TestThresholdAlertSourceRoutesScheduled` | **build-time**: every threshold alert's source route is in the sync set |
| `TestUnrecognisedTypeUsesGenericRenderer` | delivers, does not dead-letter, appears on the unknown-types board |
| `TestFortyEventsCoalesceToOneMessage` | one message, correct roll-up, within channel size limits |
| `TestCharNotificationPollingHoldsFiveTokenReserve` | 600 s polling never drops the bucket below 5 |
| `TestDeadLetterAfterMaxAttempts` | exhausted deliveries land on the admin-visible dead-letter queue |

**Prompt seed.** *(Left verbatim as the historical record of the instruction given. Its
"Structures 22" is the defect Phase 14 reported and Phase 14.1 corrected to 23 — the seed is not
edited, because what it asked for is exactly what made the inconsistency findable.)*
> Implement HANGAR Phase 14: alerting. Seed **54** concrete alert types across eight domains
> (Structures 22 including 5 Skyhook, Characters 7, platform 7, Wars 6, Corporations 5,
> Sovereignty 4, Contracts 1, Alliances 1) sourced from `eveseat/notifications`
> `src/Notifications/**`. Add a build-time check that every threshold alert declares a source
> route present in the sync set. Build the SMTP, Slack and Discord webhook channels with
> transactional outbox, hash dedupe, coalescing roll-ups and an admin-visible dead-letter queue.
> An unrecognised CCP notification type must render generically and never halt the queue. Poll
> `char-notification` at 600 s holding a permanent 5-token reserve against its 15/15min bucket.
> Exit on the six named tests.

---

## Phase 14.1 — Alert catalogue reconciliation & Phase 14 defect closure

**Objective.** Close every open item Phase 14 reported, now that the upstream it could not reach
is reachable.

**Depends on.** Phase 14.

**Trigger.** Phase 14 shipped with six reported findings, two of which it could not resolve from
inside its own build environment and deliberately did not guess at. Access to
`eveseat/notifications` became available afterwards, which settles the largest of them outright
and turns the alert catalogue from a defensible reading into a measured one.

**Legacy reference.** https://github.com/eveseat/notifications at commit
`844f7de7746b8c5161a0ad61cc7690af61eaf092` — the same commit docs/BASELINE.md §"Repositories
measured" already pinned. `src/Notifications/**` and `src/Config/notifications.alerts.php`.

**Scope.**

* **The 53-vs-54 defect, resolved by measurement.** SRS §4.4's eight per-domain counts summed to
  53 against a stated total of 54. Phase 14 reported this, established from docs/BASELINE.md §4
  that the *total* was the independently measured figure, and shipped 53 with the per-domain
  counts exact rather than invent a 54th type into a domain it could not identify. Phase 14.1
  re-ran BASELINE §4's own pipeline — same command, same pinned commit — grouped by category:
  **Structures is 23, not §4.4's 22.** The other seven counts are confirmed, Skyhook is confirmed
  to be 5, and the total of 54 is confirmed twice over (a second, independent artefact,
  `notifications.alerts.php`, holds 55 alert keys of which one is marked not visible). §4.4 and
  docs/BASELINE.md §4a are corrected; the catalogue now seeds 54.
* **Catalogue membership rebuilt from the measurement.** Phase 14 flagged every domain assignment
  as unverified judgement. Membership is now the upstream's wherever the upstream entry is a CCP
  notification type, with four documented substitutions where it is not (the platform domain is
  HANGAR's own events, upstream's two observer-computed entries become HANGAR thresholds,
  upstream's `Killmail`/`NewMailMessage` become HANGAR domain events, and CCP's
  `StructureFuelAlert`/`TowerResourceAlertMsg` are displaced by the two fuel-low thresholds §4.4
  mandates). The measurement is committed to `testdata/upstream/` and read back in CI, so this is
  reproducible provenance rather than a claim. Several Phase 14 guesses were wrong and are
  corrected — notably the entire Characters and Wars sets, and CCP's own `Moonmining` casing.
* **`internal/telemetry` — errors no longer vanish from logs.** The redacting handler rebuilt
  every `slog.KindAny` value by reflection, and Go's errors are structs with only unexported
  fields, which reflection cannot copy. Every `logger.Error(..., "error", err)` call **in the
  entire product** therefore rendered as `error=""`. Found in Phase 14 when a deliberately
  unreachable webhook logged an empty reason while writing the correct text to the database.
  Fixed at the handler; the workaround Phase 14 added in `internal/alerting` is reverted.
* **`TestPlannerSoakNoDuplicateJobs` de-flaked.** Its 5 ms timing slack sat below the drift
  between the database clock (which enforces the lease) and Go-side callback timing (which
  measured it) under full-suite load — the flake seen at the end of Phases 11, 13 and 14. The
  slack is now 20 ms, chosen to sit strictly between "lease honoured" (~40 ms) and "lease ignored"
  (~15 ms, the claim interval), so no detection power is lost and the lease itself is untouched.
* **Per-user email routing recorded as a known limitation.** `app.user` has no email column and
  EVE SSO never supplies one, so a `target_kind = 'user'` rule has no address to resolve to.
  Documented in §4.4 with the reason and what closing it would require (a column *and* an
  address-verification flow); no schema change made.

**Files.**

```
internal/alerting/catalogue/seed.go, domains.go       (membership + counts from the measurement)
internal/alerting/catalogue/catalogue_test.go         (upstream provenance test)
internal/alerting/alerting_integration_test.go        (displaced-type delivery test)
internal/telemetry/redact.go, redact_test.go          (error-message preservation)
internal/sync/planner/claim_integration_test.go       (timing slack)
db/seed/alert_types.sql                               (54 rows)
testdata/upstream/eveseat_notifications_alerts.txt    (the measurement, committed)
docs/00_SRS_v3.1.md §4.4, docs/BASELINE.md §4a
```

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestAlertCatalogueSeeds54AcrossEightDomains` | now asserts **54**, and per-domain counts including Structures 23 |
| `TestCatalogueMatchesMeasuredUpstream` | counts and CCP-type membership match the committed measurement; divergences are exactly the documented ones |
| `TestCatalogueTypesExistInLiveSpecEnum` | every seeded CCP type is in the live spec's own enum (unchanged, still passing) |
| `TestDisplacedUpstreamTypeStillDeliversViaOpenVocabulary` | a displaced CCP type still registers, boards and delivers |
| `TestRedactHandlerPreservesErrorMessages` | `error=""` cannot return, in both the JSON and text handlers |
| `TestPlannerSoakNoDuplicateJobs` | passes under full-suite load without weakening the lease invariant |

**Prompt seed.**
> Resolve the findings Phase 14 reported. Measure `eveseat/notifications` at the commit
> docs/BASELINE.md pins, using BASELINE §4's own pipeline grouped per category, and rebuild the
> alert catalogue from the result — correcting SRS §4.4's Structures count and committing the
> measurement as test data so the provenance is reproducible. Fix the redacting slog handler that
> blanks every error message in the product. De-flake the planner soak test without weakening its
> invariant. Record per-user email routing as a known limitation with its reason.

---

## Phase 15 — HTTP API Layer

**Objective.** Serve the `/api/v1` surface and generate `openapi.json`.

**Depends on.** Phase 14.

**Legacy reference.** `web/src/Http/Controllers/**` for the 72 controllers' data shapes —
Gate 4 maps each to a HANGAR endpoint. `api/src/Http/Resources/**` shows the legacy v2 response
shapes that Phase 19's shim must reproduce.

**Files.**

```
internal/api/router.go, openapi.go, envelope.go, cursor.go, errors.go
internal/api/v1/*.go                      (grouped by SRS §6.1–6.8)
internal/api/dto/*.go
internal/api/filters/spec.go, validate.go
internal/api/middleware/*.go
cmd/hangar/openapi.go
docs/openapi.json                          (generated, committed)
web/src/api/schema.d.ts                    (generated, committed)
```

**Design notes.** One API (Principle 6) — the SPA gets no private endpoint. Every collection
response carries the `_sync` envelope. Money is a JSON **string**. Internal cursors are opaque
base64 over a keyset tuple; `limit` ∈ [10, 100] default 50; `OFFSET` is prohibited.

Search (`POST /api/v1/support/search`) requires a **resolved acting character**, restricts
results to entities the caller can already see under RBAC, applies a per-user rate limit, and
writes every query to `app.security_log`. CCP prohibits using ESI for entity discovery — this is
policy, not preference, and the UI must not expose an unrestricted lookup.

Two status endpoints, never conflated: `/meta/esi-status` (ESI service health, drives gateway
decisions) and `/meta/server-status` (Tranquility players/VIP/version, drives the dashboard).

**Edge cases.**

* **Cursor validation.** `after` and `before` are mutually exclusive — supplying both is a client
  error. The `'0'` sentinel means start-of-set with `after` and end-of-set with `before`.
  `limit` bounds enforced in both directions.
* `blocked_by_pin` data renders as **unavailable with an explanation**, never as an empty list.
  Empty and unavailable are different states.
* Adversarial filters: unknown fields, SQL fragments, type-confused values must produce 422 —
  never 500, and never a successful query.
* An unauthenticated or character-less session hitting `/support/search` gets a specific error
  explaining the acting-character requirement, not a generic 403.
* Huma's generated OpenAPI must round-trip through `openapi-typescript` cleanly; a schema Huma
  can emit but `openapi-typescript` cannot consume fails CI.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestGeneratedTypesAreClean` | `openapi-typescript` produces `api.d.ts` with no errors; `make verify-generated` diff is empty |
| `TestAdversarialFiltersRejected` | non-whitelisted filters produce 422 |
| `TestZeroOffsetInGeneratedSQL` | no `OFFSET` anywhere in sqlc output |
| `TestCursorRejectsAfterAndBefore` | both supplied ⇒ client error |
| `TestCursorLimitBounds` | < 10 and > 100 rejected; default 50 |
| `TestCursorZeroSentinelBothDirections` | `'0'` = start-of-set with `after`, end-of-set with `before` |
| `TestSearchRequiresActingCharacter` | character-less session gets the specific error; every search is audited |
| `TestBlockedByPinRendersUnavailableNotEmpty` | `_sync.blocked_by_pin` set, `data` explicitly unavailable |

**Prompt seed.**
> Implement HANGAR Phase 15: the `/api/v1` HTTP layer on Huma v2.39.1. Implement every endpoint
> in SRS §6.1–6.8 with the `_sync` freshness envelope, money as JSON strings, opaque base64
> keyset cursors (`limit` 10–100 default 50, no `OFFSET`), whitelisted filter specifications,
> and generation of `docs/openapi.json` → `web/src/api/schema.d.ts`. `POST /support/search`
> requires a resolved acting character, is RBAC-restricted, rate-limited and fully audited.
> `/meta/esi-status` and `/meta/server-status` are distinct endpoints. Exit on the eight named
> tests, including cursor validation rejecting simultaneous `after` and `before` and handling
> the `'0'` sentinel in both directions.

**Status: CLOSED CLEAN** (Phase 15 implementation + Phase 15.1 defect closure). Phase 15
delivered all 156 operations and its eight exit tests, but left six routes registered-and-501
and a set of reported gaps; Phase 15.1 closed every one of them. There are no outstanding
Phase 15 items — see Phase 15.1 below.

---

## Phase 15.1 — HTTP API defect closure & documentation reconciliation

**Objective.** Resolve every open item from Phase 15's closing report so Phase 16 starts against
a codebase and document set with no known-open items. Same pattern as Phase 14.1 for Phase 14.

**Depends on.** Phase 15.

**What it closed.**

| Item | Resolution |
| :-- | :-- |
| `/auth/login` + `/auth/callback` answered 501 (`serve.go` passed a nil `*sso.Flow`) | `cmd/hangar/sso.go` assembles the real Flow: a `jwks.SettingStore` adapter over `*store.Store`, JWKS cache seeded from `app.setting` then refreshed, offline verifier, keyring. A cold cache (no persisted keys *and* no reachable JWKS) is fatal; a reachable-but-failing refresh over a warm cache is not — §7.1's offline-boot contract. No `jwks.Clock` implementation was needed: `NewCache` already defaults a nil clock to its own system clock, so adding one would have been a type with no behavioural effect. |
| Six routes registered but answering 501 | All six implemented — see the table below. `TestNoUnimplementedEndpoints` now fails the build if a seventh appears. |
| RBAC vocabulary had no permission for §6.4/§6.5/§6.7 or §6.8's read side | Twelve permissions added, seed regenerated, every stopgap re-gated. `/api/v1/me*` and `/api/v1/meta/*` remain session-only as a documented design decision. |
| `/meta/server-status` had no sync source | Global sync of ESI `GET /status/` into `app.setting`; renders *unavailable with an explanation* before the first successful run, never zeroes. |
| 12 Phase 15 query additions undocumented | Recorded in `02_DATABASE_SCHEMA.md` §9.2, alongside Phase 15.1's own seven. |
| `make ci-strict` blocked by a Windows file lock | Root-caused (see below) — an environmental self-inflicted concurrency issue, not a repository defect. `make ci-strict` now runs directly. |

**The six 501s and how each was closed.**

| Route | Phase 15's stated reason | Phase 15.1 |
| :-- | :-- | :-- |
| `/markets/{region_id}/orders`, `/types` | "no backing table" | Wrong: `app.market_order` has a `region_id` column *and* a dedicated index on it that no owner-scoped query can use. Implemented as an owner-scoped-but-region-filtered read; SRS §6.5 now states the scope explicitly. |
| `/corporations/{id}/members/limit` | no `member_limit` column | Column added (`00040`), sync handler added, route registered. |
| `/admin/esi/errorlimit` | "only reachable through an in-process cache" | Wrong: `GetErrorBudget` has existed since Phase 4, and the table is the *correct* source for an admin view because the budget is installation-wide across replicas. |
| `PUT /admin/scopes` | only per-grant Add/Remove existed | Right to refuse to fake it. `internal/rbac.ReplaceRoleGrants` now does delete + insert + rematerialise in one transaction, so concurrent editors serialise instead of interleaving. |
| `POST /admin/platforms/{id}/lockdown` | "no lockdown primitive" | `app.platform.locked_down` + who/when/why. Deliberately not a reuse of `enabled`. |
| `/characters/{id}/fittings/{id}/eft` | rendered `[type_id]`, no SDE lookup | `ListSdeTypeNames`; degrades per-line to the id placeholder when no SDE has been imported. |

**Defects found while doing the above** — all three were invisible to the existing tests, and
all three are the kind that only surface when a path is exercised end to end for the first time:

1. **Every user would have been force-logged-out ten minutes after login.** `BeginLogin` writes
   `expires_at = now + StateTTL` (10 min, correct for an unconsumed PKCE state);
   `CompleteSessionLogin` did not touch it; `GetSession` filters on it.
   `config.CryptoConfig.SessionTTL` (720h) had existed since Phase 5 **with no consumer**.
   Fixed, with `TestSessionTTLPromotedOnLogin` covering it.
2. **First-time login crashed on a foreign key.** `resolveUser` called `SetUserMainCharacter`
   *before* `UpsertCharacter`, pointing `app.user.main_character_id` at a character row that did
   not exist yet. `app.user` and `app.character` reference each other and neither FK is
   `DEFERRABLE`, so the write order is not a matter of taste. Survived from Phase 5 because
   `internal/sso`'s unit tests use an in-memory fake with no FK enforcement and no test had ever
   run the flow against real Postgres.
3. **Character reauthorization could never succeed.** The handler returned a redirect URL whose
   `state`/PKCE verifier lived on a session row the browser was never given a cookie for. It also
   did not check that the character belonged to the caller — an open redirect minting a login
   session for an arbitrary character id.

**Why `make ci-strict` would not run — two separate hazards.** Phase 15 attributed this to one
cause; it was two, and only the first was correctly identified.

*Hazard 1 — the sqlc "user-mapped section" error.* Phase 15 worked around
`The requested operation cannot be performed on a file with a user-mapped section open` on
`internal/store/gen/*.go` by regenerating into an isolated temp directory. It does not reproduce:
34 controlled attempts (15 consecutive `sqlc generate`, 3 after `go build`, 3 after `go vet`, 1
after `golangci-lint`, 2 concurrent, 10 concurrent `go build` + `sqlc`) all passed. The cause was
a **long-running background `make verify-generated` left running while foreground `sqlc generate`
calls overlapped it** — two processes rewriting the same files, which Windows surfaces as a
mapped-section conflict (Defender real-time scanning is on, with no exclusion for the repository,
which is what turns the overlap into that specific error). Not a `gopls`, build-cache or
repository defect. Operational rule: never run `make generate`/`verify-generated` in the
background while editing or generating.

*Hazard 2 — `verify-generated` hanging on `git diff`.* Discovered only when hazard 1 stopped
masking it: with `sqlc` succeeding, the recipe proceeded to
`git diff --exit-code -- <paths>`, **and that hung indefinitely** — twice, ~13+ minutes, `make`
at near-zero CPU with a live `git.exe` child, blocking the entire gate. It is the same command
that hung an earlier Phase 15 background invocation which never completed, so this had been
happening unrecognised.

It does **not** reproduce standalone: no `core.pager`, no `core.fsmonitor`, no stale
`index.lock`, a 14 MiB repository and a ~500-line diff, and both `> /dev/null` and `| tail`
variants return promptly. **The mechanism is therefore unproven, and the fix is not a
diagnosis** — it removes the failure surface instead: `--quiet` so git writes no output at all
(the check only ever wanted the exit code) and `--no-pager` so no pager can be spawned under any
tty-detection outcome, with a bounded `--name-only` list printed on failure instead of a
potentially enormous diff. Recorded honestly in the Makefile: if it recurs, add a timeout rather
than restore the old form — a gate that can hang forever is worse than one that fails.

**Two further pre-existing defects, found only because `make ci-strict` finally ran end to end.**
Both are latent Phase 0 issues, both would have failed in CI, and both independently corroborate
Phase 14.1's finding that `make ci` had never actually gone green.

1. **`TestConfigFailsFastOnEachRequiredSecret` could not pass in CI.** Its `setEnv` helper only
   *set* the variables present in its map, so `delete(env, "HANGAR_DB_URL")` removed the key from
   the map but left any **ambient** `HANGAR_DB_URL` untouched — the test asserted "loading fails
   when this secret is missing" while the secret was still present in the process environment.
   It therefore passed only in a shell exporting none of the five required variables, and failed
   in the one environment that matters: CI exports `HANGAR_DB_URL` for the `make ci-strict` step.
   Reproduced on Phase 15's own commit (`46b803b`) to confirm it predates Phase 15.1. Fixed by
   having `setEnv` explicitly blank every required variable the map omits (via `t.Setenv(k, "")`,
   which stays hermetic because `t.Setenv` restores on cleanup, and which `internal/config`
   already treats as missing).

2. **`check-identifiers` had no configuration in CI.** Every `hangar` subcommand validates the
   *whole* configuration at boot and aborts on any missing secret — deliberate fail-fast
   behaviour and a Phase 0 exit criterion — but the workflow exported `HANGAR_DB_URL` alone. Under
   `STRICT=1`, where a skipped check is a hard failure, this gate could never have passed. Fixed
   by supplying throwaway, structurally-valid placeholder configuration at the job level in both
   `ci.yml` and `release.yml` rather than by weakening the validation.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestSSOLoginRoundTripToAuthenticatedMe` | `/auth/login` → callback → session cookie → authenticated `/api/v1/me`, against real Postgres |
| `TestSSOCallbackReplayDoesNotLogTheUserOut` | back-button callback replay redirects a logged-in user instead of 401ing them out |
| `TestSSOCallbackWithoutSessionIsRejected` | replay tolerance does not weaken the unauthenticated case |
| `TestSessionTTLPromotedOnLogin` | authenticated session promoted to `SessionTTL`, not left on `StateTTL` |
| `TestNoUnimplementedEndpoints` | zero 501 responses anywhere in `docs/openapi.json` |
| `TestPermissionSeedMatchesGoSet` | still green with the twelve added permissions |
| `make ci-strict` | passes directly, no workaround |

---

## Phase 16 — Frontend I (Shell, auth, dashboard, localisation)

**Objective.** The SPA shell, authentication flow, dashboard and the 9-locale i18n layer.

**Depends on.** Phase 15.

**Legacy reference.** `web/src/resources/views/layouts/**` for the information architecture and
`web/src/lang/**` for the 9 locale files — those translation strings are directly reusable.

**Files.**

```
web/src/routes/__root.tsx, _authed.tsx, _authed/index.tsx, login.tsx, callback.tsx
web/src/components/layout/AppShell.tsx, Sidebar.tsx, Header.tsx, Breadcrumbs.tsx
web/src/components/ui/**                  (shadcn, vendored)
web/src/components/SyncBadge.tsx, IskValue.tsx, ErrorBoundary.tsx, Skeleton.tsx
web/src/stores/ui.ts, session.ts
web/src/api/client.ts, queries/*.ts
web/src/i18n/index.ts                      (consumes internal/i18n/locales.json)
web/src/styles/index.css                   (THE single stylesheet)
web/eslint.config.js                       (money + hardcoded-string rules)
```

**Design notes.** Dark-mode-first; neutral → `zinc`, destructive → `red`, informational/active →
`cyan`. `sans` = Inter with a system fallback. **Every ISK value and identifier uses `font-mono`
or `tabular-nums`.** Persistent collapsible left sidebar, top header with contextual actions,
breadcrumbs and session controls. Breadcrumbs are **derived from router state**, never
hand-written per page.

Locale resolution reads `internal/i18n/locales.json` — one source of truth shared by Go and Vite
(F-7). No hand-maintained TypeScript copy.

**Edge cases.**

* **The 250 KB gzipped budget is tight.** React 19 + Router + Query + Zustand is ~130–160 KB
  before application code. Route-level code splitting from the first commit, measured on the
  **entry chunk**.
* **Exactly one `.css` file may exist under `web/src/`** (SRS v3.1 §8.1, defect B3). v3.0 said
  "no custom `.css` files", which does not build under Tailwind 4's CSS-first configuration.
  `web/src/styles/index.css` is the sole stylesheet and its contents are restricted to the
  Tailwind import, the `@theme` token block, the dark-mode variant and the shadcn base layer.
  No component-, module- or page-level stylesheet. `make check-css` enforces it.
* `af`/`ro` have no ESI equivalent and fall back to `en`; `zh-CN` → `zh`. An unmapped UI locale
  is a **build failure**, on both the Go and TypeScript sides.
* **The locale table has exactly one definition**, `internal/i18n/locales.json`, embedded in Go
  and imported by Vite (SRS v3.1 §4.6, defect B7). Do not hand-write a TypeScript copy: the two
  would drift, and the drift surfaces only as an ESI cache-key rejection in production.
* The ESLint `no-literal-string` rule produces heavy noise on `className`, `data-testid` and
  `aria-*`. Configure the allowlist deliberately, then run `--max-warnings=0`.
* Server state must never enter Zustand.
* The SSO callback route must consume the `state` cookie exactly once and handle the
  back-button replay without erroring the user out.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `size-limit` | **entry chunk < 250 KB gzipped** |
| `TestESLintBlocksNumberOnISK` | `Number()`/`parseFloat` on an ISK-named identifier fails lint |
| `TestESLintBlocksHardcodedEnglish` | a bare English string literal in `.tsx` fails lint |
| `TestLocaleResolutionExhaustive` | **all 9 UI locales** resolve to a valid ESI `Accept-Language`; `af`/`ro` → `en`; `zh-CN` → `zh`; **an unmapped locale fails the build** |
| `TestBreadcrumbsDerivedFromRouter` | a nested route renders the full chain with no per-page definition |
| `make check-css` | exactly one stylesheet under `web/src/` |

**Prompt seed.**
> Implement HANGAR Phase 16: the SPA shell. TanStack Router v1 nested routes with a persistent
> collapsible sidebar, header, and breadcrumbs **derived from router state**; the SSO login and
> callback flow; the dashboard; TanStack Query v5 typed from `web/src/api/schema.d.ts`; Zustand
> v5 for client-only state (never server data); shadcn/ui vendored into
> `web/src/components/ui/`. Dark-mode-first zinc/red/cyan, all ISK and identifiers in
> `font-mono`/`tabular-nums`. Exactly one stylesheet. i18n for 9 locales reading
> `internal/i18n/locales.json` as the single source of truth. ESLint must block `Number()` on
> ISK strings and hardcoded English in `.tsx`. Exit on the six named criteria including the
> 250 KB gzipped entry-chunk budget.

---

## Phase 17 — Frontend II (Character & corporation views)

**Objective.** The data-heavy character and corporation screens.

**Depends on.** Phase 16.

**Legacy reference.** `web/src/Http/Controllers/Character/**`,
`web/src/Http/Controllers/Corporation/**` and `web/src/Http/DataTables/**` for column sets,
filters and the ledger aggregations. This is the primary Gate 4 parity surface: **72 controllers
must all have a HANGAR equivalent.**

**Files.**

```
web/src/components/data-table/DataTable.tsx, columns.ts, virtualization.ts, filters.tsx
web/src/features/character/**              (sheet, skills, assets, wallet, contracts,
                                            mail, PI, calendar, industry, killmails, intel)
web/src/features/corporation/**            (members, wallets, ledgers, structures,
                                            starbases, skyhooks, projects, mining)
web/src/features/squads/**
web/src/routes/_authed/characters/**, corporations/**, squads/**
```

**Design notes.** One generic, column-driven `DataTable` reused everywhere —
`@tanstack/react-table` v8 + `@tanstack/react-virtual` v3. Seventy bespoke tables is the legacy
failure mode being replaced. `<Suspense>` with **shape-matched** skeletons; every distinct data
module inside its own error boundary rendering a local retry.

**Edge cases.**

* 100k-row wallet virtualisation at 60 fps requires fixed row heights; a variable-height row
  destroys scroll performance. Design the columns for it.
* The asset tree must render to depth 5 in < 2 s. Fetch the whole subtree in one request (the
  recursive CTE) rather than one request per level.
* Complex tables use `overflow-x-auto` **without breaking the app shell** — the shell must not
  gain a horizontal scrollbar.
* `blocked_by_pin` data renders as unavailable with an administrator-facing explanation.
* The mail reader must render bodies safely — CCP mail contains user-authored HTML; sanitise it.
* Contract items drawer: a courier contract legitimately has no items; render "no items" rather
  than a loading state that never resolves.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestVirtualizedWalletScrolls100kAt60fps` | measured, not asserted by inspection |
| `TestAssetTreeDepth5Under2s` | one request, bounded depth |
| `TestSyncBadgeReflectsEnvelope` | fresh / stale / blocked-by-pin states all distinct |
| `TestContractDetailRendersItemsAndBids` | including the empty-items courier case |
| `TestMailReaderRendersAndSanitisesBodies` | script content in a mail body is neutralised |
| `TestErrorBoundaryIsolatesModule` | a failing wallet panel does not take down the character route |

**Prompt seed.**
> Implement HANGAR Phase 17: character and corporation views. Build one generic virtualized
> `DataTable` (TanStack Table v8 + Virtual v3, fixed row heights) and use it everywhere. Ship the
> character screens (sheet, skills, assets tree, wallet, contracts with an items drawer, mail
> with a body reader, PI colony view, calendar, industry, killmails, intel), the corporation
> screens (members, wallets and ledgers, structures, starbases, skyhooks, projects, mining), and
> squad administration. Shape-matched Suspense skeletons, a per-module error boundary with local
> retry, and a `SyncBadge` on every data surface. Exit on the six named tests including 100k-row
> scrolling at 60 fps and asset-tree depth 5 under 2 s.

---

## Phase 18 — Frontend III (Admin & observability)

**Status: CLOSED CLEAN.** All eight exit criteria pass. B12 and B13 are closed; closing them
surfaced eight further defects (B14–B21, SRS §0 "Findings raised during implementation [added at
the Phase 18 close-out]"), **all of which are also fixed in this phase**.

Two of those eight were initially deferred to a later phase and then closed here after review,
because both were functional gaps rather than scheduling questions:

* **B20 — `catalogue.Boot` had no caller in the running system.** Latent since Phase 2. Because
  `app.sync_subscription` foreign-keys into `app.esi_route`, an empty catalogue meant nothing in
  the ESI sync layer could run *or even be configured*, and Phase 18's own catalogue screens were
  empty on every real deployment. Now wired two ways: `hangar admin ingest-catalogue`, and a
  non-fatal background ingest at `serve` startup. The pin is never advanced as a side effect.
* **B21 — `hangar admin bootstrap-token` minted a token no middleware accepted.** Latent since
  Phase 15. The only way into a fresh installation before anyone has completed SSO did not work.
  Bearer-token authentication now exists, with the token's own `permissions` array applied as a
  **cap** — resolving a scoped token to its owner without that cap would have been a privilege
  escalation.

One item remains open by design and belongs to Phase 20: §16's Prometheus metric set
(`esi_ledger_divergence` and the rest) does not exist. No Phase 18 criterion depends on it — the
rate-limit dashboard computes divergence from the ledger tables directly.

The other standing item, retrofitting e2e coverage over Phases 16–17, was explicitly scoped **out**
of this phase and remains a scheduling question, not a defect.

**Objective.** The administrator surfaces, **and the backend work they depend on**.

**Depends on.** Phase 17.

**Legacy reference.** `web/src/Http/Controllers/Configuration/**` and
`web/src/Http/Controllers/Tools/**`. Several HANGAR boards have **no legacy equivalent** —
blocked-by-pin, unknown scopes, the exposure board — because they exist to surface the SRS's new
invariants.

**⚠ This is NOT a frontend-only phase. [added at the Phase 17 close-out]** Two of the five exit
criteria below cannot be satisfied against the API as built. The Phase 17 close-out audit found
three backend gaps that must be closed *first*; they are recorded as SRS defects **B12** and
**B13** (§0) and are scoped into this phase:

1. **No pin-advance preview endpoint exists.** Only the mutating
   `POST /api/v1/admin/esi/catalogue/pin` is registered (`internal/api/v1/admin.go:79`). The
   criterion requires the diff be shown *before* the pin moves, which one mutating call cannot do.
   SRS §6.8 now specifies `POST /api/v1/admin/esi/catalogue/pin/preview`.
2. **The route diff is never computed.** `advancePinHandler` passes `nil` to
   `catalogue.AdvancePin`, which substitutes `{}` (`internal/esi/catalogue/pin.go:77-79`). Every
   recorded `route_diff` to date is empty. Nothing diffs the route set across two pin dates.
3. **No server-side `D_max` validation.** `AdvancePin` accepts any date. The criterion demands
   rejection *client- and server-side*; only the client half was ever specifiable.

Plus a fourth, broader defect this phase must fix because its own screens are the first to trip
on it:

4. **`jsonb` columns reach the wire hex-encoded (B12).** `internal/api/dto/row.go:83` hex-encodes
   any `[]byte`; Go's `json.RawMessage` **is** `[]byte`. 42 generated model fields are affected —
   including `esi_pin_history.route_diff`, which this phase must *render*. Fixing the converter
   also repairs starbase `fuels`, skyhook/sov-hub `reagents`, structure `services` and the
   planetary colony `pins`/`links`/`routes` that Phase 17 had to render as opaque strings.

**Files.**

```
internal/api/dto/row.go                     (B12: emit json.RawMessage as nested JSON, not hex)
internal/api/v1/admin.go                    (B13: register the pin/preview endpoint)
internal/esi/catalogue/pin.go               (B13: compute the diff; enforce D_max server-side)
internal/esi/catalogue/diff.go              (B13: route-set diff across two compatibility dates)

web/src/features/admin/sync/**             (Sync Health, Route Catalogue viewer)
web/src/features/admin/esi/**              (blocked-by-pin board, pin-advance preview,
                                            rate-limit and error-limit dashboards,
                                            replica registry + ledger mode)
web/src/features/admin/scopes/**           (unknown-scope board)
web/src/features/admin/provisioning/**     (platforms, rule editor + preview, exposure board,
                                            lockdown)
web/src/features/admin/alerts/**           (dead-letter viewer, unknown-types board)
web/src/features/admin/users/**, security/**
web/e2e/**                                  (Playwright smoke suite — see "Verification gap")
```

**Design notes.** These screens are where the architecture becomes operable. The pin-advance
preview is the mechanism that keeps Principle 12 honest: an administrator must see the route diff
before the pin can move — which is exactly why B13's separate preview endpoint is a correctness
requirement and not an API-shape preference.

**Verification gap carried in from Phase 17.** `@playwright/test` and a `pnpm run e2e` script have
been dependencies since Phase 0 with **no suite behind them**, and no phase owned building one.
That is why Phase 17 had to verify its 60fps criterion by proxy (bounded DOM window) rather than
by measurement, and why 21 of its 24 feature components have no direct test. Phase 18's two
*confirmation-flow* criteria are precisely the kind jsdom verifies weakly, so this phase wires up
a **small, bounded** Playwright suite covering the pin-advance and rule-editor confirm flows
against a seeded database. It is not a mandate to retrofit e2e coverage over Phases 16–17; that
remains an open item for scheduling.

> **CLOSED.** `web/playwright.config.ts` + `web/e2e/**` (two specs, four tests) run the real
> binary — serving the real SPA out of `embed.FS` — against a real, seeded, throwaway Postgres.
> No API stub and no Vite dev server anywhere in the suite. `make e2e` is guarded on
> `HANGAR_DB_URL` the same way `check-identifiers` is, and is wired into `make ci`.
>
> Authentication is **seeded, not stubbed**: `app.session` is a plain uuid-keyed table and
> `ResolveSession` reads that uuid straight out of the cookie, so inserting a session row and
> handing the browser the matching cookie is a real login to every layer under test. Only the SSO
> round trip is skipped, and that is not what the suite verifies.
>
> The suite was checked to be non-vacuous: with the server-side `preview_token` check disabled,
> `rule-editor.spec.ts`'s server test fails (422 → 200) while its browser test still passes —
> which is the separation the two halves are supposed to have.
>
> **It found two real defects that every jsdom test had passed over**: `pgtype.Date` reaching the
> wire as a struct (B14) and a stale-preview hint in the rule editor that could never render
> because the edit handler cleared the very state the hint keyed on. Both are fixed.

**Edge cases.**

* The entitlement rule editor must **mandate preview confirmation before saving**. A rule saved
  without preview is how an accidental mass revocation happens.
* The exposure board lists pending revocations with their **exact ages** computed from
  `event_at`, not from job start.
* The pin-advance flow must refuse to advance to a date newer than `D_max`, and must show the
  full route diff including newly *blocked* routes, not only newly unblocked ones. **Both halves
  need backend work first (B13) — see the warning above.** "Rejected client-side" alone is not the
  criterion and never was: a UI-only bound check is bypassed by any direct API call.
* A diff of "no routes changed" is a **legitimate, informative answer**, not an empty state. An
  administrator advancing across a quiet week should see "nothing changes", explicitly — the same
  empty-vs-unavailable distinction §6 draws for collections. Rendering a blank panel there invites
  advancing the pin believing the preview simply failed to load.
* `route_diff` is `jsonb` and, until B12 is fixed, arrives hex-encoded. Do **not** work around
  this in the client by hex-decoding — fix the converter. A client that decodes a HANGAR response
  field is the defect, not the workaround.
* The unknown-scope and unknown-types boards need an "acknowledge" action that writes
  `acknowledged_at` — otherwise they grow unbounded and get ignored.
* The rate-limit dashboard should surface `esi_ledger_divergence` prominently: sustained
  divergence is the early warning for a Gate 1 failure.
* The replica board (`GET /api/v1/admin/esi/replicas`) shows live replicas and the resulting
  ledger mode. It is read-only by design — the mode is derived, never set. Do not add a mode
  override control; an operator-settable mode reintroduces defect B1.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestRuleEditorRequiresPreviewConfirmation` | saving without preview is impossible |
| `TestExposureBoardShowsExactAges` | ages computed from `event_at` |
| `TestPinAdvanceShowsRouteDiffBeforeChange` | the diff is displayed and confirmed before the pin moves |
| `TestPinAdvanceRefusesDateNewerThanDMax` | out-of-range dates rejected client- **and server-side** |
| `TestUnknownBoardsAcknowledge` | acknowledging writes `acknowledged_at` and clears the board |
| `TestJSONBFieldsEmitNestedJSON` | **(B12)** a `json.RawMessage` model field serialises as a JSON object/array, not a hex string; binary columns still hex-encode |
| `TestPinPreviewIsNonMutating` | **(B13)** calling preview leaves the stored pin and `esi_pin_history` untouched |
| `TestPinAdvanceRecordsComputedDiff` | **(B13)** the persisted `route_diff` is the real diff, never `{}`; newly blocked and newly unblocked routes both appear |

**Prompt seed.**
> Implement HANGAR Phase 18: the admin and observability surfaces **and the backend work they
> depend on**. Close SRS defects B12 and B13 FIRST — they are prerequisites, not cleanup:
> (a) make `internal/api/dto/row.go` emit `json.RawMessage` as nested JSON instead of hex (it
> currently hits the `[]byte` path by accident, affecting 42 model fields including the
> `route_diff` this phase must render); (b) add the non-mutating
> `POST /api/v1/admin/esi/catalogue/pin/preview`, actually compute the route-set diff across two
> compatibility dates, and enforce `D_max` server-side in `catalogue.AdvancePin`, which today
> accepts any date and records `{}` as the diff. Then build the frontend: Sync Health, the Route
> Catalogue viewer, the blocked-by-pin board with a pin-advance flow that **shows the full route
> diff and requires confirmation before the pin can move**, the unknown-scope board, rate-limit
> and error-limit dashboards (surface ledger divergence prominently), access-provisioning
> platforms with a rule editor that **mandates preview confirmation before saving**, the exposure
> board listing pending revocations with exact ages from `event_at`, the alert dead-letter viewer
> and unknown-types board with acknowledge actions, user administration and the security log.
> Reuse Phase 16/17's AppShell, DataTable/CollectionTable, ErrorBoundary and SyncBadge rather than
> rebuilding them. The replica board is read-only by design — do not add a mode override control;
> that reintroduces B1. Exit on all eight named tests.

---

## Phase 19 — Events, Webhooks & Third-Party Migration

**Objective.** The transactional outbox, signed webhooks, and the `/api/v2` sunset shim.

**Depends on.** Phase 18.

**Legacy reference.** https://github.com/eveseat/api —
`src/Http/Controllers/Api/v2/**` (nine controllers: Alliance, Character, Corporation, Killmails,
Role, RoleLookup, Squad, User, and the base `ApiController`) and `src/Http/Resources/**` for the
exact response shapes. `routes/api.php` enumerates the routes.

**Files.**

```
internal/events/outbox.go, dispatch.go, sign.go, retry.go
internal/api/v2shim/router.go, translate_*.go, headers.go
deploy/verify-webhook-signature.sh          (reference verification script)
testdata/legacy-api-v2/*.json               (recorded legacy responses — Gate 7 corpus)
docs/APPENDIX_C_MIGRATION.md
```

**Design notes.** Data mutation and outbox insert share **one transaction**. Signature is
HMAC-SHA256 over `timestamp ‖ '.' ‖ body`, sent as `X-Hangar-Signature: t=<unix>,v1=<hex>`, with
a replay window.

The shim is **read-only**. Every shim response carries `Deprecation: true` and an RFC 8594
`Sunset` header. `RoleController` and `RoleLookupController` are documented as **breaking with no
shim** — the grant model is not translatable and pretending otherwise is worse than a clean break.

**Edge cases.**

* **Byte-compatibility** (Gate 7) means field order and JSON formatting too, not just values.
  Record legacy responses as the corpus and compare bytes.
* Legacy v2 responses have **no `_sync` envelope**. The shim must strip it, not pass it through.
* Legacy pagination is Laravel-style (`current_page`, `last_page`, `per_page`, `total`) and
  HANGAR is keyset-based. The shim must synthesise the legacy envelope; `total` is the expensive
  field — decide whether to compute it or return a documented approximation, and record the choice.
* Legacy money fields are JSON **numbers**; HANGAR emits strings. The shim must convert back,
  which reintroduces float imprecision — this is a documented, deliberate property of the shim
  and must be called out in the migration guide.
* A webhook endpoint that is permanently down must not retain jobs forever — cap attempts and
  disable the endpoint with an admin notification.
* Signature verification must be constant-time.

**Exit criteria.**

| Test | Assertion |
| :-- | :-- |
| `TestOutboxAtomicWithMutation` | rolling back the mutation rolls back the outbox row |
| `TestWebhookSignatureVerifiesWithReferenceScript` | `deploy/verify-webhook-signature.sh` validates a live payload |
| `TestShimByteCompatibleForAllNineControllers` | **byte-identical** to recorded legacy responses for every migrated read route |
| `TestShimEmitsDeprecationAndSunset` | both headers on every shim response |
| `TestShimStripsSyncEnvelope` | no `_sync` key in any shim response |
| `TestReshapedRoutesReturn410WithMigrationPointer` | `RoleController`/`RoleLookupController` paths return a documented breaking-change response |
| `TestShimAuthenticatesLikeV1AndCannotExceedTokenScope` | **(Phase 19's own addition)** an unauthenticatable shim is a shim nobody can migrate to — and one that authenticates *differently* is worse than one that cannot authenticate at all. The shim resolves the same `app.api_token` credential `/api/v1` does (accepting it in legacy's `X-Token` header as a transport alias, `/api/v2` only), and a token whose own `permissions` array omits the permission gets a 403 even when its owner holds it. A shim route must never be a way around Phase 18's B21 cap. |

**Prompt seed.**
> Implement HANGAR Phase 19. Build the transactional outbox — data mutation and outbox insert in
> one transaction — with HMAC-SHA256-signed webhook dispatch and a reference verification script.
> Then build the `/api/v2` read-only sunset shim translating legacy request and response shapes
> onto `/api/v1` handlers for all nine legacy controllers from `eveseat/api`
> `src/Http/Controllers/Api/v2/`. Responses must be **byte-compatible** with recorded legacy
> responses, carry `Deprecation: true` and an RFC 8594 `Sunset` header, strip the `_sync`
> envelope, and synthesise Laravel-style pagination. `RoleController` and `RoleLookupController`
> are breaking with no shim. Exit on the six named tests.

---

## Phase 20 — Load Testing & Release

**Objective.** Pass all **seven** release gates.

**Depends on.** Phase 19.

**Files.**

```
deploy/helm/**
deploy/dashboards/*.json
test/load/gate1_esi.go, gate2_revocation.go, gate3_alerts.go
test/parity/gate4_matrix.go
test/drift/gate6_synthetic_spec.json
test/shim/gate7_corpus/**
docs/RELEASE_NOTES.md, docs/BASELINE.md (refreshed)
.github/workflows/release.yml
```

`04_RELEASE_GATES.md` is the full verification matrix. Summary:

| Gate | Requirement |
| :-- | :-- |
| 1 | 4-hour, 5000-character run: zero rate-limit breaches across both governors; ledger vs `X-Ratelimit-Remaining` divergence ≤ 1 request. **Run at N=1 and N=3 replicas; both results recorded.** |
| 2 | 5000 identities × 3 platforms, revocation p99 < 60 s from the originating event, with the bulk queue saturated |
| 3 | 4-hour alert load test drops zero alerts; the accounting identity holds exactly |
| 4 | All 58 capabilities verified against the **measured** baseline in `docs/BASELINE.md` |
| 5 | Blank environment → running installation in 3 commands, no compilation, Redis absent |
| 6 | A synthetic spec with (a) a post-pin route, (b) a UUID path identifier, (c) a novel scope grammar, (d) an unrecognised `x-cache-mode` ingests with **zero code changes** |
| 7 | `/api/v2` shim byte-compatible against recorded legacy responses |

**Prompt seed.**
> Implement HANGAR Phase 20: load testing and release. Build the harnesses for all seven release
> gates in `docs/04_RELEASE_GATES.md`, the Helm chart, the Grafana dashboards and the release
> workflow producing static binaries for linux/amd64, linux/arm64 and windows/amd64 plus the
> published container image. **Gate 1 is executed at N=1 and at N=3 replicas** and both results
> recorded — a pass at one replica count is evidence about that count only, even though the
> cluster-shared ledger should pass both. Gate 6's synthetic spec was committed in Phase 2 and
> must ingest with **zero code changes**; if any source change is needed, that is a Gate 6
> failure, not a fix.

---

## Phase 20 is split into 20.1 – 20.8 **[decided at the Phase 20 open]**

**Trigger.** Phase 20's first action was the production-caller audit its own brief demanded before
Gate 4 could be signed off (*"does any non-test file outside this package construct this type?"*,
asked of every subsystem rather than the handful Phase 19 happened to touch). Run mechanically —
`go list -deps ./cmd/hangar` for unreachable packages, `x/tools/cmd/deadcode` for unreachable
functions — it returned **124 functions unreachable from `main`** and thirteen new defects,
B24–B36, recorded in SRS §0 and in [`PRODUCTION_CALLER_AUDIT.md`](PRODUCTION_CALLER_AUDIT.md).

That finding makes single-phase execution impossible, for a reason that is structural rather than
one of effort. **Release-gate rule 0.4 says a gate that requires a code change to pass is a failed
gate, not a fixed one.** Gates 1, 2 and 3 each measure a subsystem that is currently unreachable
from `main`. Wiring those subsystems up and then running their gates in the same phase produces
exactly the tautology rule 0.4 exists to forbid — the same defect as re-recording the Gate 7
corpus to fit the shim, one level up.

**The split is the resolution.** Each wiring defect is closed in a sub-phase that owns it, ships
its own exit criteria and its own tests, and is verified against real data. Only Phase 20.8 runs
the gates, several sub-phases downstream of every change it measures. A gate then reports on a
build that already worked, which is the thing a gate is supposed to be evidence of.

| Sub-phase | Owns | Closes | Gate it unblocks |
| :-- | :-- | :-- | :-- |
| **20.1** | Specification reconciliation, the metric surface, the reachability guard | B36 + the two measured spec corrections | prerequisite for 1, 2, 3, 7 |
| **20.2** | ESI gateway correctness wiring | ~~B23~~, ~~B28~~, ~~B29~~, ~~B31~~, ~~B37~~, ~~B38~~, ~~B39~~, ~~B40~~, B26 (surface half) | Gate 1 |
| **20.3** | Identity, RBAC and revocation wiring | B27, B32, B35 (B26 closed early in 20.2 — see below) | Gate 2 |
| **20.4** | Alert generation wiring | B25 | Gate 3 |
| **20.5** | Data completeness and remaining surfaces | B22, B24, B30, B33, B34 | Gate 4 |
| **20.6** | `/api/v2` shim route coverage | Gate 7's coverage requirement | Gate 7 |
| **20.7** | Deployment surface — Helm, dashboards, install scripts | — | Gate 5 |
| **20.8** | Gate execution against a release candidate, and the v1.0 release | — | all seven |

**Ordering rationale.** 20.1 first because B36 is a specification contradiction that decides
whether Gates 1–3 can produce evidence at all, and because the reachability guard it installs is
what stops the next sub-phase reintroducing the defect it is closing. 20.2 → 20.4 in gate order,
each landing its own metrics with its own wiring. 20.5 sweeps the remaining surfaces. 20.6 and
20.7 are independent of all of the above and could run in parallel with them. 20.8 last, always.

**B37 and B38 landed early, out of order, and the reason is worth recording.** Registering the
real EVE SSO application against a live installation — rather than waiting for 20.2 to schedule
it — surfaced both within minutes: the authorization URL carried `scope=` (B37), and deriving the
scope set from the sync set exposed two paths that had been pluralised into non-existence (B38).
Neither was findable from the test suite, and B38 was findable only *because* B37's fix made the
derivation observable. That is the fourth consecutive phase in which running the system found
what running the tests could not.

**One rule adopted across all eight, and it is the lesson of the audit.** A metric is declared
only in the sub-phase that makes it *move*. A metric that exists and reads zero is
indistinguishable from a healthy system — `alert_delivery_total == 0` reads as "a quiet
installation", not as "the emitter has no caller" — so declaring the full metric set up front
would hide the very defects the remaining sub-phases exist to close.

---

### Phase 20.1 — Specification reconciliation, the metric surface & the reachability guard

**Objective.** Settle the contradictions, build the shared measurement surface, and make this
class of defect fail the build.

**Depends on.** Phase 19 and the Phase 20 audit.

**Scope.**

* **B36 resolved.** SRS §0 assigns the metric surface to Phase 20; `04_RELEASE_GATES.md` assigns
  it to Phases 4/11/14 and forbids retroactive instrumentation. The rule's *intent* is that
  instrumentation must not be authored in the same breath as the gate that reads it — the same
  principle as Gate 6's synthetic spec and Gate 7's recorded corpus. §0 of the gate document is
  amended to say so explicitly: instrumentation must land in a phase that **precedes** the
  gate-running phase and ships with its own exit criteria and tests. Phases 4/11/14 remain the
  *preferred* owners; 20.2–20.4 become the actual ones, and 20.8 runs the gates.
* **The metric surface.** `telemetry.NewRegistry` gets a caller, `/metrics` is served by `serve`
  and `work`, and the metrics whose subsystems are **live today** are declared and moved. The
  rest are declared by 20.2–20.4 alongside their wiring, per the rule above.
* **The two measured specification corrections.** SRS §10 and Gate 7.4 both claim the shim
  "reintroduces" IEEE-754 imprecision; measured against legacy's own database it does not, because
  `character_wallet_journals.amount` is a MySQL `DOUBLE` and the loss is already at rest. And SRS
  Appendix C marks only `RoleController`/`RoleLookupController` as breaking, where
  `UserController` and `SquadController` are equally un-shimmable — Gate 7.9 cannot pass until
  Appendix C says so.
* **The reachability guard.** `make check-reachability`, wired into `ci`, failing when a package
  or an exported subsystem entry point loses its production caller. Allowlisted exceptions are
  explicit, justified in-file, and are the record of what is knowingly inert.

**Exit criteria.** `make ci-strict` passes with zero skips; `/metrics` serves a non-empty scrape
from a running binary; the guard fails when a production caller is deliberately removed and
passes when it is restored; the audit's defect register and the gate document no longer disagree.

---

### Phase 20.2 — ESI gateway correctness wiring

**Objective.** Make the gateway's tested-but-uncalled pieces run, against real traffic, and build
Gate 1's harness without running Gate 1.

**Depends on.** Phase 20.1 (the metric surface and the reachability guard) and Phase 20.1.1 (the
sync engine actually polling, which is what made this phase verifiable rather than theoretical).

**Closed.** B23, B28, B29, B31, B39, B40, and the surface half of B26.

**The decisions, and the reasoning that is not recoverable from the diff.**

* **B29 — one classification point.** `internal/esi.Client.Do` called `ClassifyCost` and nothing
  else, so 5.5's reconciliation, its 429 snooze and its headerless-429 signal had no live
  implementation at all. `Do` now calls `ClassifyResponse` once and every branch reads its
  `Outcome`. `X-Ratelimit-Limit`'s ceiling wins over the catalogue's when the server sends one
  (5.5: "reconciled from `X-Ratelimit-Limit`"), and the reconciler is fed
  `Request.RateLimitRealMax` — the UNREDUCED ceiling — so `char-notification`'s five-token
  call-site reserve can never desync the ledger from the truth it exists to import.
* **B29 — two queries deleted rather than wired.** `IncrementErrorBudget` and
  `ResetErrorBudgetWindow` were listed as uncalled. They are the superseded halves of
  `RecordErrorAgainstBudget`, whose single atomic `UPDATE` exists specifically to remove the
  read-then-branch-then-write race those two reintroduce. Giving them callers to satisfy an
  allowlist would be the allowlist wagging the codebase. **Correction on the record:**
  `app.esi_error_budget` reading `error_count = 0` was *not* evidence of a missing caller —
  `Governor2.RecordError` has been reachable from `Client.Do` since Phase 7, and every one of the
  installation's 1371 sync runs returned 200 or 304. Zero was the correct reading.
* **A refusal is not a failure.** Governor 1 exhaustion, a Governor 2 pause, and either breaker
  opening all used to reach River as failed jobs. 5.5 is explicit that the caller "snoozes the
  subscription; it does not spin", so all four — plus a real 429, plus "no eligible acting
  character" — now write `app.sync_subscription.snoozed_until` and a `sync_run` row naming the
  reason. This is what finally gave `SnoozeSyncSubscription` a caller, six phases after the
  column was added.
* **B28 — the entity breaker sits BESIDE re-election, not instead of it.** They answer different
  questions and therefore cannot disagree: the acting-character history answers *which* character
  acts, the breaker answers *whether* to call at all. Five consecutive 403s on an (entity, route)
  pair means five attempts, each of which re-elected afresh from a pool ordered by fewest recent
  403s — so the viable candidates have already been walked, and continuing spends Governor 2's
  installation-wide budget on a request that cannot succeed. One 2XX closes the circuit and clears
  that character's history. The probe interval is 15 minutes against the route breaker's 60
  seconds, because a 5XX clears when ESI recovers and five 403s clear when a human grants a
  corporation role in-game.
* **B28's observability half.** `CorporationWorker.Work` returned `nil` on
  `ErrNoEligibleActingCharacter` and recorded *nothing*, leaving the subscription at
  `last_status = NULL` forever — the same class of silence as the "/status is scheduled" lie
  20.1.1 closed. It now writes one finished `sync_run` with
  `outcome = "unavailable:no_eligible_acting_character"` and snoozes. It deliberately does not
  touch `last_status`: that column holds an HTTP status, and no request was made.
* **B31 — 5.9's two open questions, settled.** `internal/esi/pagination` is now the single
  implementation and `internal/sync/worker` calls it.
  **Concurrency: the spec wins, and the walker fans out at 4.** It is a cap rather than a quota,
  and it cannot cause a breach — every page goes through `Client.Do`, so every page takes a
  Governor 1 reservation, and a walk with no budget is refused and snoozed rather than admitted.
  The serial walker was never safer, only slower.
  **Torn-set detection: the stricter live reading wins, and 5.9 is tightened to match.** A page
  that disagrees with page 1 about whether it carries `Last-Modified` at all is TORN. The lenient
  reading treated the absence of the evidence the rule is built on as evidence of intactness; the
  cost of the strict reading being wrong is one discarded fetch, and the cost of the lenient one
  being wrong is missing rows nobody notices.
* **B31 — the cursor mechanism has a consumer.** `GET /corporations/{id}/projects` returns
  `{cursor, projects}`; 20.1.1 captured the cursor and did not follow it. The walk now runs from
  the start-of-set sentinel, echoes each opaque cursor back verbatim, and reassembles ONE envelope
  of the same shape so the route's handler needs no knowledge that four requests produced it. No
  torn-set check is applied to a cursor walk: 5.9 states that rule under `page` only, and the
  cursor parameters are documented as walking *forwards in time* over a set that may be growing.
* **B31, in passing.** Phase 7's recorded "KNOWN GAP" — the character wallet journal and six other
  character routes are page-paginated and `CharacterWorker` did not walk them, so only page 1 ever
  synced — is closed by the same shared walker.
* **B23 — the ESI language is installation-wide.** `HANGAR_LOCALE` (default `en`), validated at
  boot against `internal/i18n`'s own table. Not per acting user: background sync is the
  overwhelming majority of HANGAR's ESI traffic and has no acting user, and the resolved language
  is part of the cache key (5.3), so a per-user value would fragment one shared cache up to
  ninefold. **Upgrade note, and it is not cosmetic:** the resolved language was already in the
  cache key and was previously empty, so this change alters every key. A deployment taking this
  version re-fetches everything once — `app.esi_cache_entry`'s old rows are never read again and
  age out on their own. Plan the upgrade for a quiet window on a large installation. The language
  is also now SENT as `Accept-Language`, which it never was; keying on a language the request did
  not ask for was the latent half of the same defect.
* **B40 — the first administrator.** A freshly authenticated SSO user held ZERO permissions and no
  route existed by which anyone could grant them one. The decision, from the three options on the
  table: **first-login promotion**, gated on a property of the database rather than on row order —
  `internal/rbac.BootstrapFirstAdmin` promotes the authenticating user to the seeded `admin` role
  if and only if NOBODY currently holds an allowed `superuser` grant by either path, all inside one
  transaction so two simultaneous first logins cannot both promote. An operator who has already
  curated `admin`'s grants keeps their curation. `hangar admin bootstrap-token` is kept and
  unchanged, but an installation whose only route to a usable browser session is a shell command
  on the host cannot be verified in a browser, which is what was blocking this phase.
* **B26's surface half, moved here.** The full role-management surface is registered
  (`internal/api/v1/admin_roles.go`), `GET /api/v1/me/permissions` exists, and — a defect found on
  the way — `PATCH /api/v1/admin/users/{id}` had declared `is_active` and `is_admin` since Phase 15
  and silently dropped both. Separately, the squad membership and squad-role endpoints called the
  RAW generated queries, bypassing `internal/rbac`'s wrappers: the rows were written and
  `app.effective_permission` was never recomputed, so adding somebody to a role-granting squad
  changed no permission they actually held. They go through the wrappers now, and the two
  set-replacing handlers stopped discarding their errors.
* **B39 — not reproducible, and fixed anyway.** The blank unauthenticated `GET /` did not
  reproduce. What the investigation found is that `web/src/routes/__root.tsx` declared no
  `errorComponent`, and TanStack Router renders NOTHING for an error with no handler up the tree —
  so any throw on the unauthenticated path produces exactly the reported symptom, whatever threw.
  A root `errorComponent` and `notFoundComponent` now exist. `web/src/main.tsx`'s global
  `retry: 1` also became a retry predicate: a 4xx is definitionally not transient, and every 401
  and 403 was being issued twice.

**Gate 1's instrumentation, per release-gate rule 7.** `esi_429_total{has_headers}`,
`esi_429_headerless_total{group}`, `esi_420_total` and `esi_error_limit_remaining` are declared
here, with the wiring that moves them, and nowhere earlier. No label anywhere scales with
character count.

**Gate 1's harness, per release-gate rule 6.** `test/load` holds the recording proxy — a genuine
floating window, not a refill bucket, because a refill proxy would certify the client behaviour
5.5 prohibits — Governor 2's fixed 60-second installation-wide window, 1.3's injection schedule,
the /metrics scraper and the seven 1.5 evidence artefacts. It runs as an integration test.
**Gate 1 itself is not run in this phase**; 20.8 runs it against a release candidate at N=1 and
N=3.

**Exit criteria.** Both reachability guards pass with the closed entries removed; the harness's
integration suite proves each 1.3 row against the real client; the installation polls real ESI
with reconciliation moving `app.esi_ledger_bucket.server_remaining`; and a real character can log
in through a browser and see their own data.


---

## Cross-phase edge-case register

Consolidated so the implementing agent can scan for the ones relevant to the current phase.

| # | Edge case | Correct behaviour | Phase |
| :-- | :-- | :-- | :-- |
| 1 | Singular ESI paths (`/corporation/{id}/mining/…`) | read `upstream_path` verbatim; never derive | 2, 8 |
| 2 | Compatibility date rollover at 11:00 UTC | `now().UTC()-11h`, one shared function | 2 |
| 3 | Spec fetched at the app pin instead of `D_max` | permanently blind catalogue — asserted against | 2 |
| 4 | Offline boot | embedded snapshot, marked `stale_snapshot`, surfaced | 2 |
| 5 | Unrecognised `x-cache-mode` | record the value, schedule as `ttl-based` | 2, 6 |
| 6 | `x-cache-age: 0` | resolves to `ttl_floor`, never 0 | 6 |
| 7 | `af`/`ro`/`en` share a cache key | correct and intended | 3, 16 |
| 8 | `zh-CN` → `zh` | region subtag stripped | 3, 16 |
| 9 | 429 with no rate-limit headers | no charge, `Retry-After` or `ttl_floor` snooze, metric | 4 |
| 10 | Routes with no `x-rate-limit` | Governor 2 only — not "unlimited" | 4 |
| 11 | 420 on a Governor 1 route | error limit is global; pause everything | 4 |
| 12 | Out-of-order response settlement | min-heap, not a deque (solo path) | 4 |
| 12a | 429 is also a 4XX | the 0-cost exemption takes precedence | 4 |
| 12b | Transport error / timeout | charge worst case (5); the server may have processed it | 4 |
| 12c | Replica added or lost | mode re-selected from the registry; flush before admitting requests | 4 |
| 12d | Replica dies without deregistering | ages out after 30 s; survivors stay `clustered` meanwhile — the safe direction | 4 |
| 13 | `scp` is a string for single-scope tokens | accept both string and array | 5 |
| 14 | `owner` hash change | invalidate all tokens; urgent revocation | 5, 11 |
| 15 | Concurrent refresh rotation | advisory lock, re-read inside the lock | 5 |
| 16 | Novel scope grammar | ingest and surface; no regex | 5 |
| 17 | Torn page set (`Last-Modified` mismatch) | discard whole payload, retry — **and a page that disagrees about whether the validator is PRESENT is torn too (20.2)** | 3, 8, 20.2 |
| 18 | `X-Pages` absent | treat as one page | 3 |
| 19 | Data-level 404 (`/ship` while docked) | data, not a breaker failure | 7 |
| 20 | `ref_type` unseen value | record in `open_vocabulary`, store the row | 8 |
| 21 | Corp route with no director token | render *unavailable*, not empty | 8, 17 |
| 22 | Unparseable notification YAML | JSONB + generic renderer; queue never halts | 9, 14 |
| 23 | Asset container cycle | depth bound **and** path guard | 1, 9, 17 |
| 24 | UUID project id | `uuid` column, no coercion | 1, 9 |
| 25 | Empty courier contract items | empty ≠ failed | 9, 17 |
| 26 | Cloudflare 1015 as HTML or `{"code":40333}` | sniff both framings | 12 |
| 27 | Discord role above bot position | refuse **without issuing the request** | 12 |
| 28 | Discord 404 on a departed member | reconciled state, not an error | 12 |
| 29 | TS3 error inside HTTP 200 | parse the body, not the status | 13 |
| 30 | TS3 escaping | escape out, unescape in | 13 |
| 31 | Mumble external authenticator unreachable | fail-closed is opt-in, documented | 13 |
| 32 | `char-notification` 15/15min bucket | 600 s polling, permanent 5-token reserve | 14 |
| 33 | Coalesced roll-up exceeds webhook size limit | truncate with "and N more" | 14 |
| 34 | `after` + `before` together | client error | 15 |
| 35 | `'0'` cursor sentinel | start-of-set / end-of-set by direction | 15 |
| 36 | `blocked_by_pin` data | unavailable with explanation, never empty | 15, 17 |
| 37 | Search without an acting character | specific error; every search audited | 15 |
| 38 | Legacy v2 money as JSON number | shim converts back; imprecision documented | 19 |
| 39 | Legacy pagination `total` | expensive; decide and document | 19 |
| 40 | Multiple replicas | cluster-shared ledger, mode auto-selected; **no operator divisor**; Gate 1 run at N=1 and N=3 | 4, 20 |
| 41 | Wars alert domain | notification-derived; no wars route or table may be invented | 14 |
| 42 | `app.permission` is a closed set | Principle 14 governs *external* vocabularies only | 1a, 10 |
| 43 | `jsonb` column on the wire **(B12)** | nested JSON, never hex; `json.RawMessage` is `[]byte` and hits the binary path by accident | 18 |
| 44 | Pin advance without a preview **(B13)** | preview is a separate non-mutating endpoint; advance validates `D_max` server-side and records a real diff, never `{}` | 18 |
| 45 | Pin preview showing no route changes | "nothing changes" is an answer, not an empty state — render it explicitly | 18 |
| 46 | SPA deep link on hard navigation | unknown non-asset paths fall back to `index.html`; `/api`, `/auth`, `/healthz` still 404 honestly | 16, 17 |
| 47 | Cursor-paginated route (`{cursor, items}`) | walk from the start-of-set sentinel, echo the opaque cursor verbatim, reassemble ONE envelope; **no torn-set check — 5.9 states that rule under `page` only** | 20.2 |
| 48 | A gateway refusal (no budget, paused, either breaker) | snooze the subscription and record why; never a failed job, never a retry loop | 20.2 |
| 49 | An installation with no administrator | the first authenticating user is promoted, gated on "nobody holds superuser", inside one transaction | 20.2 |
