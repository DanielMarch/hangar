# Software Requirements Specification (SRS): Project HANGAR

**Version:** 3.1
**Status:** Approved for development
**Compatibility date pin:** `2026-08-04`
**Supersedes:** SRS v3.0 (which must not be implemented from — see §0)
**Revision basis:** Architectural review of 2026-08-06 during scaffolding and roadmap definition (11 defects corrected)

> **This document is the authoritative specification.** Where `docs/01`–`docs/04` elaborate a
> mechanism, they elaborate *this* document. The v3.0 text remains only as history; every
> section corrected below is marked **[v3.1]** and the v3.0 wording is superseded, not merely
> annotated.

---

## 0. Revision Record (v3.0 → v3.1)

Eleven defects were found in v3.0. Seven were structural (B1–B7); four were under-specifications
that would have produced divergent implementations (B8–B11). All are corrected in this revision.
Two more (B12–B13) were raised later, against the running system, at the Phase 17 close-out, and
eight more (B14–B21) at the Phase 18 close-out; both sets are recorded in their own subsections
below. Every one of B1–B21 is closed.

| # | Defect in v3.0 | Resolution in v3.1 | Sections |
| :--- | :--- | :--- | :--- |
| **B1** | **Per-replica ledger state does not compose.** §4.1.3 made Governor 1 bucket state in-process and unshared, relying on header reconciliation. Reconciliation is *reactive*: N replicas each believe they hold the full `max_tokens`, so a synchronised burst spends N× the budget before any correction lands. Gate 1 at N=1 certified nothing about N>1. | Governor 1's ledger is **cluster-shared by default**, held in an UNLOGGED Postgres table with per-bucket row locking. A single-replica installation automatically selects an equivalent in-process fast path via a live-replica registry, so the turnkey deployment pays no database cost. Gate 1 is executed at N=1 **and** N=3. Adds `GET /api/v1/admin/esi/replicas`. | §4.1.3, §5.2, §6.8, §7 Phase 4, Gate 1 |
| **B2** | **"~48 core tables" undercounts by ≈2.5×.** Projecting the §6 endpoint contract, the UUID-keyed tables and the eight detail tables — even with maximal owner-polymorphism — yields ≈129 tables. Phase 1 was unschedulable as written. | Phase 1 is split. **1a = 51 platform tables**, **1b ≈ 78 domain projection tables**. Both land before Phase 2. | §5.2, §7 Phase 1 |
| **B3** | **"No custom `.css` files" is not implementable.** Tailwind CSS 4 is CSS-first configured: `@import "tailwindcss"`, `@theme` and `@custom-variant` must live in a stylesheet. Zero `.css` files cannot build. | Exactly **one** sanctioned stylesheet, `web/src/styles/index.css`, restricted to the Tailwind import, the `@theme` token block, the dark-mode variant and the shadcn base layer. CI fails if a second `.css` file exists under `web/src/`. | §8.1, §7 Phase 16 |
| **B4** | **The ZeroC Ice fallback was required in-binary.** No maintained Go Ice binding exists, and CGO-linking Ice breaks the statically linked binary mandated by §9.2. The requirement was unsatisfiable alongside §9.2. | gRPC MurmurRPC is the **only** in-binary Mumble driver. Ice support ships as an **optional out-of-process bridge** exposing the same gRPC contract, distributed as a separate container image. | §4.3, §7 Phase 13, §9 |
| **B5** | **Contradictory error-limit scope.** §4.1.3 declared the error limit installation-wide but declared governor state per-replica and unshared. At N replicas that permits N×100 errors per window and guarantees a 420. | Governor 2's budget is **explicitly cluster-shared** through a single Postgres row, with resume hysteresis to prevent oscillation. | §4.1.3 |
| **B6** | **Baseline asserted, not measured**, contradicting Principle 15. Appendix B's counts were stated as fact but nothing in the schedule reproduced them, and Gate 4 depended on them. | Phase 0 emits `docs/BASELINE.md` by measuring legacy repositories at HEAD. **Gate 4 compares against `BASELINE.md`, not against this document.** A disagreement between measured and stated counts is a specification defect that blocks Gate 4. | Appendix B, §7 Phase 0, Gate 4 |
| **B7** | **Locale table specified in two places.** §4.6 placed the resolution table in `internal/i18n/`, but §8 and Phase 16 require the SPA to resolve locales too. Two copies drift silently. | **One source of truth**: `internal/i18n/locales.json`, embedded into the Go binary and imported by the SPA build. Both sides run the exhaustive table-driven test. | §4.6, §7 Phase 16 |
| **B8** | **Cost table self-contradictory.** §4.1.3 assigned 4XX = 5 and separately exempted 429, which *is* a 4XX. Precedence was undefined. Transport errors were unspecified entirely. | The 429 exemption **takes precedence** over the 4XX rule. Transport errors and timeouts are charged the worst case (5), because the server may have processed the request. | §4.1.3 |
| **B9** | **Ledger timestamp unspecified.** "One `window-size` after *that individual request*" does not say whether the request is stamped at issue or at response. The two differ by request latency and bias available headroom in opposite directions. | `consumed_at` is the **response** timestamp. Reservations are stamped at issue and expire at the configured request timeout. | §4.1.3 |
| **B10** | **Wars had no data source.** §4.4 introduced Wars as an alert domain with 6 types, but §6 exposes no wars endpoint and §5.2 defines no wars table. | Recorded explicitly: the six war alerts are **notification-derived**. No wars route and no wars table are required, and none may be invented. | §4.4 |
| **B11** | **`app.permission` appeared to contradict Principle 14.** §5.2 declared it a closed Go set while Principle 14 forbids closed sets. | Principle 14 is scoped explicitly to **external** vocabularies. Vocabularies HANGAR itself owns and defines may be closed. | Principle 2 (P14), §5.2 |

### Findings raised during implementation **[added at the Phase 17 close-out]**

Two further specification defects were found while implementing Phases 16–17. They continue the
B-series numbering because they are defects of the same kind — this document under-specifying
something an implementation then diverged on — but they were raised against the running system
rather than against v3.0, and are corrected here for Phase 18 to build on.

| # | Defect | Resolution in v3.1 | Sections |
| :--- | :--- | :--- | :--- |
| **B12** | **Structured `jsonb` columns had no stated wire representation.** §6 pins down money (a string) and the `_sync` envelope but says nothing about how a `jsonb` column reaches the client. The generic row converter (`internal/api/dto/row.go`) hex-encodes any `[]byte`, and Go's `json.RawMessage` **is** `[]byte`, so every structured column reaches the wire as an opaque hex string: a starbase's `fuels` (the fuel-low alert's own data), a skyhook's and sovereignty hub's `reagents`, a structure's `services`, a planetary colony's `pins`/`links`/`routes`, and an ESI pin advance's `route_diff`. 42 such fields exist across the generated models. No client can render one without first decoding a HANGAR response field, which no part of this document asks it to do. | A `jsonb` column is emitted as **nested JSON**, never as an encoded scalar. Hex-encoding is reserved for genuinely binary columns (hashes, ciphertext, wrapped key material) — none of which may leave the server in the first place. | §6, §7 Phase 18 |
| **B13** | **The pin-advance preview was named but never specified as an operation.** §6.8 lists a single mutating `POST /api/v1/admin/esi/catalogue/pin` *"(advance the compatibility date, with preview diff)"*. Principle 12 and Phase 18 both require the administrator to see the route diff **before** the pin moves, which one mutating call cannot provide. The delivered handler compounds this: it passes a `nil` diff (recorded as `{}`, so no diff is ever computed) and performs no `D_max` bound check, leaving both of Phase 18's pin exit criteria unsatisfiable against the API as built. | Preview is a **separate, non-mutating endpoint**, `POST /api/v1/admin/esi/catalogue/pin/preview`, returning the full route diff in both directions (newly blocked **and** newly unblocked) for a candidate date without changing state. The advance endpoint validates the candidate against `D_max` server-side and records the computed diff rather than `{}`. | §6.8, §7 Phase 18 |

### Findings raised during implementation **[added at the Phase 18 close-out]**

Closing B12 and B13 required touching every §6.8 surface, and doing so surfaced eight further
defects (B14–B21). Most are in the delivered system rather than in this document, and are recorded
here because §6.8's endpoint list is the thing they contradict; B14 is a genuine
under-specification of the same kind as B12. **All eight are fixed in Phase 18** — none is
outstanding — and they are written down rather than reconciled silently because each was found by
running the thing, not by reading it.

| # | Defect | Resolution | Sections |
| :--- | :--- | :--- | :--- |
| **B14** | **`date` and `interval` columns had no stated wire representation either** — the same gap as B12, one type family over. `pgtype.Date` and `pgtype.Interval` implement no `json.Marshaler`, so `dto.Row`'s generic struct branch recursed into their fields: a compatibility date reached the wire as `{"time":"2026-08-11T00:00:00Z","infinity_modifier":0,"valid":true}` and a route's `cache_age` as `{"microseconds":300000000,"days":0,"months":0,"valid":true}`. `esi_pin_history.old_pin`/`new_pin` and `esi_route.compatibility_date` are all affected, and all three are rendered by Phase 18 screens. | A `date` column is emitted as a **`YYYY-MM-DD` string** — never a timestamp, which would invite a local-time conversion that shifts it a day — and an `interval` as **whole seconds**. NULL is `null` in both cases, never a zero date or a zero duration. | §6, §7 Phase 18 |
| **B15** | **Nullable `jsonb` was not covered by B12's fix.** `sqlc.yaml` had a `jsonb -> json.RawMessage` override with no `nullable: true` variant, so a nullable `jsonb` column generated a plain `[]byte` — indistinguishable by type from `bytea`, and therefore still hex-encoded after B12's converter fix. Three columns: `notification_unknown_type.sample_payload` (which Phase 18's unknown-types board renders), `corporation.palette`, `character_notification.payload`. | The override gained its nullable variant. B12's rule is about the **column's type, not its nullability**, and is now enforceable by Go type rather than by inspecting bytes. | §6, §7 Phase 18 |
| **B16** | **`GET /api/v1/admin/provisioning/exposures` was unreachable.** It was registered with an input shape declaring a **path** parameter `id`, but §6.8's path for this route has no `{id}` segment — so the parameter could never be supplied, and the handler's `parseUUID("")` failed on every request. The exposure board answered `400` to every call ever made to it. It is the subject of a Phase 18 exit criterion, which is how it surfaced. | The platform moves to a **query** parameter, `?platform_id=`; §6.8's path is the contract and is unchanged. | §6.8, §7 Phase 18 |
| **B17** | **The two unknown boards could be read but never cleared.** §6.8 lists `GET /admin/scopes/unknown` and `GET /admin/alerts/unknown-types` but no acknowledge operation for either, though `acknowledged_at` exists on both tables, both queries have existed since Phases 2 and 14, and `alerting.unknown_types.acknowledge` has been in the permission vocabulary since Phase 14. Boards that only grow get ignored. | Two operations added: `POST /admin/scopes/unknown/acknowledge` (scope in the **body**, not the path — a scope string is opaque and may contain characters a proxy will normalise) and `POST /admin/alerts/unknown-types/{type}/acknowledge`. A new permission, `admin.scopes.acknowledge`, is the scope-side twin of the alerting one. | §6.8, §5.1, §7 Phase 18 |
| **B18** | **The entitlement rule editor had nothing to save through.** §6.8 lists the rule *preview* and *lockdown* operations for a platform but no operation that writes a rule, and Phase 11's `CreateEntitlementRule`/`DeleteEntitlementRule` were left as unwired seams. Phase 18's "a rule editor that mandates preview confirmation before saving" was therefore vacuous — there was no save. | Three operations added: `GET /admin/platforms/{id}/groups`, `GET /admin/platforms/{id}/rules`, and `PUT /admin/platforms/{id}/rules` (a full, transactional replace). The save requires a **`preview_token`** returned by the preview endpoint and recomputed server-side over the rules actually submitted, so an unpreviewed — or a since-edited — rule set cannot be saved by **any** client, not merely by one that renders the editor. | §6.8, §7 Phase 18 |
| **B19** | **The exposure board mixed platforms.** `GetExposureBoard` is scoped to one platform, but its audit-side query had no platform predicate, so platform A's board listed every other platform's pending revocations alongside its own. | A platform-scoped variant backs the board. The unscoped query remains, correctly, for Gate 2's installation-wide latency measurement. | §6.8, Gate 2 |

Three further observations were made. Two were initially deferred and then **closed inside Phase
18** after review, because both were functional gaps in the delivered system rather than
scheduling questions — a deployment that cannot ingest its route catalogue and cannot authenticate
a third-party token is not a working deployment, whatever its exit criteria say. They are written
up in full because each had been latent for many phases:

| # | Defect | Resolution | Sections |
| :--- | :--- | :--- | :--- |
| **B20** | **`catalogue.Boot` had no caller in the running system.** Phase 2 delivered the whole boot sequence (discover `D_max`, fetch the spec **at** `D_max`, ingest every operation, mark routes newer than the pin `blocked_by_pin`) and nothing outside an integration test ever invoked it — no `serve` path, no command, no job. A deployed installation therefore never populated `app.esi_route`; and since `app.sync_subscription` carries a `route_id` foreign key into that table, **nothing in the ESI sync layer could run, or even be configured**. Phase 18's own Route Catalogue viewer and blocked-by-pin board were empty on every real deployment, which is how it surfaced. | Two callers, one definition. `hangar admin ingest-catalogue` is the explicit operator action; `serve` additionally runs the same ingest in the background at startup, non-fatally, so a single-box installation needs no second command (§2's "single-process default", the same reasoning the in-process planner already uses). It is idempotent, so every replica doing it on every restart is safe, and **the pin is never advanced as a side effect** — `Boot` only reads and seeds it. | §5.1, §7 Phase 2, §7 Phase 18 |
| **B21** | **`hangar admin bootstrap-token` minted a token no middleware accepted.** The command has created `app.api_token` rows since Phase 1 and printed its secret with the words "Use it as a Bearer token ... once Phase 15 lands". Phase 15 landed and wired the token *management* endpoints, but nothing ever authenticated a request **by** a token — `hangar_session` was the only credential any middleware read. So the bootstrap token, whose entire purpose is to be the way into a fresh installation before any human has completed SSO, was useless, and §12's third-party surface (which Principle 6 and Phase 19's `/api/v2` shim both depend on) was unreachable. | `internal/api/middleware/apitoken.go` resolves `Authorization: Bearer <token_id>.<secret>` by SHA-256 hash, rejecting revoked and expired tokens, and populates the same context user id `ResolveSession` does. Critically the token's own `permissions` array is applied as a **cap**: §12 and capability 47 both call these tokens *scoped*, so resolving one to its owner and stopping there would hand every narrowly-scoped integration the owner's full RBAC. A permission must now be in **both** the user's materialised set and the token's scope. Token resolution runs before cookie resolution and a request presenting both gets the token's — the *lesser* — authority. | §6.1, §12, §7 Phase 18 |

The third is genuinely a later phase's, and is left open deliberately:

* **`esi_ledger_divergence` and the rest of §16's metric set do not exist.** `internal/telemetry
  /metrics.go` is a bare Prometheus registry. Phase 20 owns the metric surface alongside the gate
  harnesses that read it. Phase 18's rate-limit dashboard computes divergence from
  `app.esi_ledger_bucket` and `app.esi_ledger_entry` directly rather than waiting for it, so no
  Phase 18 criterion depends on the metric existing.

### The B20 pattern, and two more instances of it **[found in the Phase 19 close-out audit]**

B20 was not a one-off. A subsystem can be fully built, fully tested and never called, because the
phase that builds a component and the phase that wires it up are usually the same phase — and when
the wiring is one line, it is the line that gets forgotten. Nothing fails: the package's own tests
construct what they need, so the suite is green and the feature is inert.

Auditing for the pattern directly — *does any non-test file outside this package construct this
type?* — turned up three instances. One was Phase 19's own and is fixed; two are open and are
recorded here rather than reconciled silently.

| # | Defect | Status | Sections |
| :--- | :--- | :--- | :--- |
| **B22** | **`internal/sde`'s import pipeline has no caller.** `Build`, `Verify`, `Swap`, `AbortBuild`, `FetchManifest` and `DownloadZip` — the whole Phase 9 SDE ingest — are imported by exactly one file, `internal/sde/swap_integration_test.go`. There is no `hangar admin sde ...` command, no job and no `serve` path, and the CLI's full command list is `admin {bootstrap-token, ingest-catalogue, verify-identifier-types}`, `migrate {up,down}`, `openapi`, `schedule`, `serve`, `work`, `healthcheck`. The `sde.*` tables created by migration 00036 can therefore only ever be empty on a real installation. §12 requires the `sde` schema to stay joinable from `app` because military campaigns join definitions from it; nothing joins to rows that cannot be loaded. | **OPEN — Phase 9's, surfaced in Phase 19.** Needs an operator command and a decision about whether `serve` seeds it in the background the way it does the route catalogue. Note the interaction with Gate 5: a "blank environment → running installation in 3 commands" that leaves the SDE empty is a different claim from one that does not. | §5.1, §12, §7 Phase 9 |
| **B23** | **ESI language resolution is never invoked.** `internal/i18n.ResolveESILanguage` maps the 9 UI locales onto ESI's `Accept-Language` values (capability 58, and the `af`/`ro`/`en` and `zh-CN`→`zh` edge cases both have passing tests). Its only importer outside the package is `internal/esi/cache/key_test.go`. `esi.Client.Language` — documented in place as "the resolved ESI Accept-Language value (internal/i18n)" — is never assigned by any non-test code, so it is the empty string on every real request and `esi_cache`'s key records an empty `ResolvedLanguage`. The locale machinery is correct and unreachable. | **OPEN — Phase 3's, surfaced in Phase 19.** The fix is to resolve the acting user's locale where the client is constructed; the risk is that it changes the cache key, so a deployment doing it mid-flight re-fetches. Gate 4 counts capability 58 as delivered, which it is not end to end. | §4.6, §7 Phase 3 |

Both are recorded as defects rather than fixed in Phase 19: each belongs to a phase that can test
its wiring against real data (an SDE import against a real manifest; a language-resolved request
against a real cache), and a one-line wiring added blind is how this class of defect is created,
not how it is closed.

### Findings carried forward from v3.0's own revision record

The A-, P- and R-series resolutions from v2.0 → v3.0 remain in force and are unchanged by this
revision.

---

## 1. Introduction

### 1.1 Purpose
This document defines the Software Requirements Specification for Project HANGAR, a ground-up
rebuild of the EVE Online corporation management tool, SeAT. HANGAR is designed to solve
structural inadequacies in the legacy system, primarily regarding ESI rate-limit handling,
architectural bottlenecks, and abandoned third-party access-provisioning plugins.

### 1.2 Scope
Project HANGAR will deliver a high-performance, single-binary Go backend using PostgreSQL 18 for
data persistence and transactional job queuing (via River). The frontend will be an embedded
React 19 Single Page Application. HANGAR elevates Access Provisioning (Discord, TeamSpeak,
Mumble) and Alerting to first-class, natively supported core subsystems.

It guarantees full feature parity with the legacy system across **58 verified capabilities**,
reconciling a legacy footprint of **106 distinct ESI routes (107 call sites), 72 UI controllers,
54 concrete alert types, 70 ESI scopes and 9 locales**. See Appendix A for the capability matrix
and Appendix B for the route-level reconciliation.

**[v3.1 — B6]** Those counts are the *expected* result of the Phase 0 measurement, not an
assertion. The measured baseline in `docs/BASELINE.md` is authoritative for Gate 4.

To accommodate users with little to no IT experience, the application will provide a turnkey
Docker deployment, while still allowing experienced administrators to deploy manually.

### 1.3 Declared Scope Reductions
Three legacy capabilities are **intentionally not replaced**. They are recorded here so that
"full feature parity" is not read as covering them:

1. **In-process plugin model.** SeAT's Laravel service-provider plugin ecosystem allowed third
   parties to inject routes, migrations, jobs and views into the running application. HANGAR
   replaces this with an out-of-process extension model: the OpenAPI-generated REST surface (§6),
   scoped API tokens (§6.1), and signed outbound webhooks (§4.9). No third-party code executes
   inside the HANGAR binary.
2. **Versioned `/api/v2` third-party surface.** Superseded by `/api/v1` under the One API
   principle. A time-boxed compatibility shim and migration mapping are specified in §10 and
   Appendix C.
3. **[v3.1 — B4] In-binary ZeroC Ice support for Mumble.** Delivered instead as an optional
   out-of-process bridge (§4.3). No Ice runtime is linked into the HANGAR binary.

---

## 2. Architectural Principles

All system implementation must adhere to the following binding principles:

1. **The spec is the schedule:** ESI route configurations (TTLs, cache modes, rate-limit groups,
   required roles, scopes, compatibility dates, pagination style) must be dynamically ingested
   from the live OpenAPI spec, never hardcoded. This uses the vendor extensions `x-cache-age`,
   `x-cache-mode`, `x-rate-limit`, `x-required-roles`, `x-pagination` and `x-compatibility-date`.
2. **Never guess what the server tells you:** Rate limits and error limits must be reconciled
   against authoritative server response headers.
3. **Failure is scoped:** A failure on one character or route must never halt synchronization for
   the entire installation.
4. **Freshness is per-route:** Entities are polled based on per-endpoint TTLs and cache modes, not
   flat global intervals.
5. **Conditional requests default:** 304 Not Modified is the cheapest correct request and must be
   utilized via ETag/Last-Modified caching.
6. **One API:** The SPA and third-party extensions must consume the identical OpenAPI-generated
   REST surface.
7. **Two-service deployment:** Operational overhead must be kept to a single Go binary and a
   PostgreSQL database. Redis is strictly optional.
8. **Secrets never leave the gateway:** Refresh tokens must be envelope-encrypted at rest and
   decrypted only in memory during ESI calls.
9. **Money is exact:** All ISK values must be stored as `NUMERIC(30,2)` in Postgres and serialized
   as strings in JSON.
10. **Schema drift is a compile error:** End-to-end type safety using `sqlc` and
    `openapi-typescript`.
11. **Revocations are synchronous:** Access provisioning is a security control; any event reducing
    a user's entitlements must trigger a priority revocation with a p99 SLA of < 60 seconds.
12. **Compatibility is pinned, discovery is not.** `X-Compatibility-Date` must be sent on **every**
    request — an absent header resolves to the *oldest* available date, which is never correct.
    Two distinct dates exist and must not be conflated: the **app pin**
    (`esi.compatibility_date`, used for all data requests, advanced only by explicit administrator
    action) and the **discovery date** (`D_max`, the newest value from
    `/meta/compatibility-dates`, used only to fetch the OpenAPI spec). Pinning discovery to the
    app pin would render the catalogue permanently blind to routes newer than the pin.
13. **Identifiers are typed by the spec.** Identifier column types are derived from the OpenAPI
    schema, never assumed. `type: integer, format: int64` maps to `bigint`;
    `type: string, format: uuid` maps to `uuid`. Coercing one to the other is prohibited. CCP
    ships both, and has stated that UUID identifiers will continue to appear.
14. **[v3.1 — B11] External vocabularies and grammars are open.** Enumerations, notification
    types and OAuth scope strings **that originate outside HANGAR** are stored as `text` and are
    never validated against a pattern, regex, or closed set. An unrecognised value must be
    ingested and surfaced to an administrator, never rejected.
    *This principle governs external vocabularies only.* Vocabularies HANGAR itself defines and
    controls — the permission set, grant effects, entity kinds, alert categories — may be closed
    sets, because HANGAR changes them through its own release process.
15. **The baseline is measured, not asserted.** Any parity claim in this document must be
    traceable to a count taken from legacy repository HEAD. **[v3.1 — B6]** Phase 0 performs that
    measurement and records it in `docs/BASELINE.md`; Appendix B states the expected result.
16. **[v3.1 — B1] Shared limits require shared state.** Any budget that ESI enforces across the
    whole installation — the Governor 1 consumption ledger per `(group, userID)` and the Governor 2
    error limit — must be accounted in state shared by every HANGAR replica. Per-replica
    accounting of an installation-wide budget is prohibited. Where a single-replica deployment
    permits an in-process fast path, that path must be semantically identical and selected
    automatically, never by operator configuration.

---

## 3. System Architecture & Tech Stack

### 3.1 Core Technologies
* **Backend Language:** Go 1.26+
* **HTTP / API Framework:** Huma (on `net/http` + `chi` router) generating OpenAPI 3.1. Pinned to
  v2.39.1.
* **Database:** PostgreSQL 18
* **DB Query Layer:** pgx v5 + sqlc (compile-time-checked SQL generation)
* **Migrations:** Goose v3 (embedded, versioned, plain-SQL)
* **Job Queue:** River (PostgreSQL-backed, `SELECT ... FOR UPDATE SKIP LOCKED`). Pinned to v0.43.0.
* **Frontend SPA:** React 19+ / TypeScript 5.9 / Vite 7 (embedded in the Go binary via `embed.FS`)
* **Frontend Routing & State:** TanStack Router v1, TanStack Query v5, Zustand v5
* **Frontend UI/Components:** Tailwind CSS 4, shadcn/ui (Radix primitives), TanStack Table v8
* **Authentication:** EVE SSO OAuth 2.0 (Authorization Code + PKCE S256), offline JWT validation
* **Observability:** `log/slog` (JSON), OpenTelemetry, Prometheus metrics
* **Deployment:** Turnkey Docker Compose stack, and a standalone static Go binary

### 3.2 Package Layout
* **`cmd/hangar/`**: Entry point and Cobra CLI (`serve`, `work`, `schedule`, `migrate`, `admin`).
* **`internal/config/`**: Viper configuration, validation, environment secret loading.
* **`internal/esi/`**: API gateway, Route Catalogue, conditional cache, rate governors, circuit
  breakers.
* **`internal/sso/`**: OAuth2, **offline** JWT validation (JWKS-cached), token lifecycle.
* **`internal/scopes/`**: Opaque scope catalogue, requirement enforcement, re-authorization
  workflows.
* **`internal/crypto/`**: Envelope encryption for at-rest credentials.
* **`internal/sync/`**: Sync planner, subscription management, route normalizers.
* **`internal/domain/`**: Entities, invariants, domain events.
* **`internal/store/`**: sqlc-generated queries and repository facades.
* **`internal/rbac/`**: SQL-backed grant model and effective-permission resolution.
* **`internal/alerting/`**: Alert catalogue, interpret logic, routing, delivery channels.
* **`internal/provisioning/`**: Entitlement engine, reconciliation, drivers.
* **`internal/events/`**: Transactional outbox and webhook dispatch.
* **`internal/api/`**: Huma handlers, DTOs, filter specifications, and the `/api/v2` sunset shim.
* **`internal/i18n/`**: Locale registry and ESI `Accept-Language` resolution. **[v3.1 — B7]**
  Holds `locales.json`, the single source of truth consumed by both the Go binary and the SPA
  build.
* **`internal/sde/`**: **[v3.1]** SDE JSONL streaming import and atomic schema swap. Added
  because Phase 9 requires it and v3.0 assigned it no owner.
* **`internal/telemetry/`**: **[v3.1]** slog redaction handler, OpenTelemetry wiring, Prometheus
  registry. Added because the Phase 0 redaction requirement has no owner otherwise and every
  package imports it.
* **`db/`**: Goose migrations and sqlc query sources.
* **`web/`**: React SPA source.

---

## 4. Core Subsystems & Design Specifications

### 4.1 ESI Gateway & Route Catalogue

#### 4.1.1 Route Catalogue and compatibility dates
The catalogue boot sequence is strictly ordered:

1. `GET /meta/compatibility-dates` → resolve `D_max`, the newest published date.
2. `GET https://esi.evetech.net/meta/openapi.json` with `X-Compatibility-Date: D_max`. **The app
   pin must not be used for this request.**
3. Ingest every operation into `app.esi_route`, persisting `x-cache-age`, `x-cache-mode`,
   `x-rate-limit` (`group`, `max-tokens`, `window-size`), `x-required-roles`, `x-pagination`,
   `x-compatibility-date`, the operation's declared scopes, the verbatim upstream path template,
   and the declared type of every identifier appearing in the operation (Principle 13).
4. For each route, compare its `x-compatibility-date` against the app pin. Routes newer than the
   pin are marked `blocked_by_pin`, excluded from scheduling, and surfaced in the admin Route
   Catalogue with a diff.
5. The pin is **never** advanced automatically. Advancement is an administrator action taken
   against the `blocked_by_pin` list and `/meta/changelog`.

The API changes date at 11:00 UTC; date arithmetic must use `now() - 11h`. Future dates are
rejected by ESI. Offline boot falls back to an embedded spec snapshot, which must record the
`D_max` at which it was captured and must mark the catalogue as running from a stale snapshot.

An ingest that maps zero operations is a failure, not an empty success: a truncated download must
never silently empty the catalogue.

**Pin for v1.0: `2026-08-04`.** DTOs must reflect the state of the API at that date, including:
`title_id` renamed to `corporation_title_id` on `GET /characters/{character_id}`, the addition of
`character_title_id` and `achievement_score` (2026-06-09), and the corporation palette fields
(2026-07-21).

#### 4.1.2 Path handling and normalization
`app.esi_route.upstream_path` is populated verbatim from the spec and is the sole authority for
request construction. **Paths must never be derived, pluralized, or inferred from HANGAR's own
resource naming.** ESI contains deliberate irregularities, including:

* `/corporation/{corporation_id}/mining/extractions` — **singular** `corporation`, unlike every
  sibling route.
* `/corporation/{corporation_id}/mining/observers` and `/observers/{observer_id}` — likewise
  singular.

Cache-key normalization operates only on the request envelope — trailing-slash removal,
scheme/host casing, query-parameter sorting, and percent-encoding consistency. It must never
rewrite path segments. Cache keys combine: method, normalized path, sorted query, compatibility
date, tenant, **resolved ESI language** (§4.6), and token subject.

#### 4.1.3 Rate limiting **[v3.1 — B1, B5, B8, B9]**

Two mutually exclusive governors apply per route, as declared by the spec and confirmed by
response headers.

##### Governor 1 — Floating-window consumption ledger (`X-Ratelimit-*`)

ESI does **not** operate a token bucket. It operates a floating window: the tokens consumed by a
request are returned to the bucket exactly one `window-size` after *that individual request*, not
on a continuous refill schedule. HANGAR must model this exactly.

**Ledger state.** Per `(group, userID)` bucket, a **cost-weighted expiry ledger**: an ordered
collection of `(cost, consumed_at)` entries. Available tokens =
`max_tokens − Σ cost` over entries where `consumed_at > now − window_size`, minus outstanding
reservations. Entries are evicted on read.

A continuous-refill token bucket is **prohibited**. It over-estimates available headroom
immediately after a burst — precisely the condition Gate 1 exercises.

`userID` = `applicationID:characterID` on authenticated routes; `sourceIP` or
`sourceIP:applicationID` on unauthenticated routes.

**[v3.1 — B1] Ledger state is cluster-shared.** ESI enforces this budget across the whole
installation, so HANGAR must account for it across the whole installation (Principle 16). The
ledger is held in an UNLOGGED PostgreSQL table, with each `(group, userID)` bucket serialised by a
row lock on its own bucket row. Contention is per-bucket and therefore per-character on
authenticated routes, which is negligible.

Two execution modes exist, and **the mode is selected automatically, never configured**:

| Mode | Selected when | Behaviour |
| :--- | :--- | :--- |
| `solo` | the live-replica registry shows exactly one replica | pure in-process ledger; no database round-trip per request |
| `clustered` | two or more live replicas | shared Postgres ledger with an in-process read-through mirror |

Each replica heartbeats into `app.esi_replica` every 10 seconds; a replica is live if its
heartbeat is under 30 seconds old. On transition from `solo` to `clustered`, the in-process ledger
is flushed into the shared table before any further request is admitted. On transition from
`clustered` to `solo`, the shared table is read into memory before the fast path engages. Both
transitions must be proven not to lose or double-count entries.

Operator configuration of the mode is prohibited: a divisor or a mode flag that an administrator
can set wrongly reintroduces the defect this revision exists to remove.

**Cost by response status.**

| Response | Cost |
| :--- | :--- |
| 2XX | 2 |
| 3XX | 1 |
| 4XX | 5 |
| 5XX | 0 |
| **429** | **0 — the exemption takes precedence over the 4XX rule** |
| **transport error, timeout, or no response** | **5 — the server may have processed the request** |

**[v3.1 — B9] Entry timestamp.** `consumed_at` is the **response** timestamp. The server keys its
window on when it processed the request, which is at or after HANGAR's issue time; using the
response timestamp therefore releases the cost no earlier than the server does, so any error is in
the safe direction. Reservations are stamped at issue time and expire at the configured request
timeout, so a lost response cannot leak headroom indefinitely.

**Predictive reservation.** The acquirer reserves the worst-case cost (5) before issuing a request
and settles to the observed cost after the response. Reserving the optimistic cost allows a run of
4XX responses to overdraw the window.

**Header reconciliation.** `X-Ratelimit-Limit` is parsed as `<max-tokens>/<window>` where the
window suffix is `m` (minutes) or `h` (hours). `X-Ratelimit-Remaining` and `X-Ratelimit-Used`
reconcile the ledger against the server on every response; **the server value always wins**. When
the server reports less headroom than the ledger holds, a synthetic entry expiring a full window
from now is injected; when it reports more, the oldest entries are evicted until the values agree,
never exceeding `max_tokens`.

**429 handling.** `Retry-After` (seconds) is authoritative and the affected subscription is snoozed
for exactly that duration. Sibling subscriptions are unaffected (Principle 3). The reconciler must
tolerate 429 responses that arrive **without** rate-limit headers — CCP documents in-monolith
limiters that produce these, and they are being deprecated but are still live. A headerless 429
charges nothing, snoozes for `Retry-After` if present and otherwise for `ttl_floor`, and must not
be interpreted as `remaining = 0`.

**Absent `x-rate-limit`.** Rate limiting is not yet active on all routes. Absence means Governor 2
applies alone — it does not mean unlimited, and it does not mean a default bucket.

##### Governor 2 — Error limit (`X-Esi-Error-Limit-*`) **[v3.1 — B5]**

A global budget of 100 non-2XX/3XX responses per fixed 60-second window, across the entire
installation. Exceeding it returns 420 on **all** routes, including those under Governor 1.

Because the budget is installation-wide, its state is **cluster-shared** through a single
PostgreSQL row updated on every non-2XX/3XX response and read through a one-second in-process
cache. Per-replica error budgets are prohibited: at N replicas they would permit N×100 errors per
window and guarantee a 420.

HANGAR must pause proactively at a configurable remaining threshold (default: 20 remaining) rather
than waiting for a 420, and must resume only at a higher threshold (default: 60 remaining) so the
installation does not oscillate. Any observed 420 is a critical alert.

#### 4.1.4 Pagination
Two mechanisms, selected by `x-pagination`:

* **Cursor (`x-pagination: cursor`)** — query parameters `after`, `before`, `limit`. `after` and
  `before` are **mutually exclusive**; supplying both is a client error. The sentinel `'0'` means
  start-of-set (with `after`) or end-of-set (with `before`). `limit` is an integer with
  **minimum 10, maximum 100, default 10**; HANGAR requests 100. Cursor values are opaque strings
  and must never be parsed or generated locally.
* **Page-based (`page` + `X-Pages`)** — loops capped at concurrency 4. `Last-Modified` must match
  across all pages of a set; a torn dataset is discarded and retried rather than partially
  committed. An absent `X-Pages` is treated as a single page.

#### 4.1.5 Conditional caching
304 Not Modified is the default operation. Two-tier cache: in-process L1 (ristretto) and Postgres
UNLOGGED L2. `If-None-Match` and `If-Modified-Since` are sent whenever a validator is held, except
on routes declaring `x-cache-mode: no-cache`, which must additionally perform no L1 or L2 write.

Redis may optionally replace the L2 tier. It is never authoritative: a Redis error degrades to a
cache miss, never to a request failure (Principle 7).

#### 4.1.6 Circuit breaker
Opens on ≥ 10 consecutive 5XX errors for a route, or ≥ 5 consecutive 403s for the same
`(route, entity)` pair. Half-open probes resume at the route TTL. The 403 breaker is
entity-scoped so that one director losing a corporation role does not disable the route for every
other corporation (Principle 3).

A response status that is data rather than failure — a 404 from `/characters/{id}/ship` for a
character with no current ship, for example — must not advance a breaker counter.

### 4.2 Sync Engine

#### 4.2.1 Scheduling unit
Granular `(entity, route)` rows in `app.sync_subscription`, replacing legacy flat character
sweeps. The planner loop is leader-elected via Postgres advisory locks, claims due work every 5
seconds, and transactionally enqueues to River.

#### 4.2.2 Cache-mode scheduling policy
`x-cache-mode` is declared on only a minority of cached routes. The policy is defined for all four
cases:

| `x-cache-mode` | Scheduling behaviour |
| :--- | :--- |
| *absent* (**default**) | Treat as `ttl-based`. This covers the majority of routes and must be the fallback for any unrecognised future value. |
| `ttl-based` | `next_due_at = last_success + max(x-cache-age, ttl_floor) + jitter`. |
| `event-based` | `x-cache-age` is a hint, not a contract. Poll at `max(x-cache-age, ttl_floor)`, rely on ETag revalidation, and apply 1.5ⁿ adaptive backoff on consecutive 304s up to `backoff_cap`. |
| `no-cache` | Never written to L1 or L2; no conditional headers sent. Scheduled at `ttl_floor` only, and only for subscriptions that explicitly opt in. |

An unrecognised value is recorded as an open vocabulary observation and surfaced to an
administrator; the route is scheduled as `ttl-based` and is never rejected.

`x-cache-age: 0` never means "poll continuously." Combined with `event-based` it means CCP
declares no TTL contract; HANGAR applies `ttl_floor` (default 300s). A configured `ttl_floor` is
enforced globally regardless of what the spec declares.

Full jitter is applied to every computed `next_due_at` to prevent thundering herds. Adaptive
backoff is reset on any 200, and specifically **not** on a 304.

#### 4.2.3 Acting-character election
Deterministically selects the healthiest corporation director token for corp-scoped endpoints,
honouring `x-required-roles`, and falling back automatically on 403. The ordering must be
deterministic — token validity, then required scopes, then required corporation roles, then fewest
recent 403s, then lowest character id — so that a 403 investigation is reproducible.

### 4.3 Access Provisioning (Core Subsystem)
* **Entitlement Engine:** Pure function evaluating seven grant sources (user, role, corporation,
  alliance, corp title, squad, public). Deny rules take absolute precedence. Being a pure
  function — no I/O, no clock, no randomness — is what makes the dry-run preview provably correct.
* **Strict Mode Gate:** ESI token validity is a mandatory precondition of platform access,
  evaluated via a per-character SQL `NOT EXISTS` query. An invalid token on **any** of a user's
  characters denies platform access for that user.
* **Synchronous Revocation SLO:** Any event reducing entitlements (token invalidation, owner-hash
  change, scope reduction, role change, squad removal, corporation departure, rule deletion, admin
  lockdown) transactionally enqueues a priority reconciliation to `provision-urgent`. p99 from
  event to platform API call completion < 60 seconds, measured from the originating event and not
  from job start. `provision-urgent` must never share a worker pool with `provision-bulk`.
* **Discord Driver:** Tracks `X-RateLimit-Bucket`. API version sourced from configuration with an
  allowlist (e.g. v10), validated at boot. Enforces the global 50/s limit and tracks the
  10k/10min Cloudflare invalid-request budget (warn at 50%, pause at 80%); that budget is
  installation-wide and is shared through the same mechanism as Governor 2. Handles undocumented
  Cloudflare 1015 bans delivered as HTML or as `{"code": 40333}` without standard 4XX framing.
  Proactively blocks role assignments at or above the bot's position in the hierarchy **without
  issuing the request**, since a failed attempt consumes the invalid-request budget it is meant to
  protect.
* **TeamSpeak Driver:** TS3 WebQuery client mapping `client_unique_identifier` via single-use
  challenge tokens. TS3 query escaping applies to values even over WebQuery, and TS3 reports
  errors inside HTTP 200 responses — the body, not the status, is the error signal.
* **Mumble Driver: [v3.1 — B4]** gRPC (MurmurRPC) implementation handling ACL groups, with an
  optional external-authenticator mode for absolute connection denial. gRPC is the **only**
  in-binary driver. ZeroC Ice support, where required, is delivered by an **optional
  out-of-process bridge** that exposes the same gRPC contract and is distributed as a separate
  container image. No Ice runtime may be linked into the HANGAR binary, because doing so requires
  CGO and would break the statically linked binaries mandated by §9.2.
  Fail-closed behaviour when HANGAR is unreachable in external-authenticator mode is an explicit
  administrator opt-in; failing closed by default would lock every user out during a restart.

### 4.4 Alerting & Notifications (Core Subsystem)
* **Alert Catalogue:** 54 concrete alert types seeded across eight domains — Structures (**23**,
  including 5 Skyhook types), Characters (7), HANGAR platform events (7), **Wars (6)**,
  Corporations (5), Sovereignty (4), Contracts (1), Alliances (1) — categorized as ESI
  notifications, domain events, or internally evaluated threshold alerts. The seed count and the
  per-domain counts are asserted at build time.
  * **[v3.1 — corrected in Phase 14.1] Structures is 23, not 22.** As originally written this
    list read "Structures (22 …)", and its eight numbers summed to **53** against a stated total
    of 54 — an inconsistency Phase 14 reported rather than silently reconciling, shipping 53 with
    the per-domain counts exact because the upstream needed to settle it was unreachable from
    that build environment. Phase 14.1 measured `eveseat/notifications` directly at the commit
    docs/BASELINE.md §4 already pins, applying that section's own recorded pipeline: it
    reproduces BASELINE's total of **54** and yields the per-domain breakdown BASELINE never
    recorded, in which Structures is **23**. The total was right and this one domain figure was
    understated by one; the other seven are confirmed correct, as is Skyhook (5). The measurement
    is committed at `testdata/upstream/eveseat_notifications_alerts.txt` and is read back by
    `TestCatalogueMatchesMeasuredUpstream`, so the catalogue's provenance is reproducible rather
    than asserted. A second, independent artefact agrees on the total: upstream's
    `src/Config/notifications.alerts.php` holds 55 alert keys of which one is marked not visible.
  * **HANGAR's membership is the upstream's wherever the upstream entry is a CCP notification
    type.** It differs in exactly four documented places, each recorded in
    `internal/alerting/catalogue/seed.go` and enforced by the same test: the platform domain
    carries HANGAR's own seven events rather than SeAT's; upstream's two observer-computed
    entries (`inactive_member`, `contract_created`) become HANGAR threshold alerts over the same
    data; upstream's `Killmail`/`NewMailMessage` become HANGAR domain events; and CCP's
    `StructureFuelAlert`/`TowerResourceAlertMsg` are displaced by the two computed fuel-low
    thresholds this section mandates below. A displaced type is not lost — Principle 14's
    open-vocabulary path registers it on first sighting
    (`TestDisplacedUpstreamTypeStillDeliversViaOpenVocabulary`).
* **[v3.1 — B10] Wars are notification-derived.** The six war alert types are produced from CCP
  notifications. §6 exposes no wars endpoint and §5.2 defines no wars table, and neither may be
  invented to satisfy this domain.
* **Threshold alerts** (fuel low, expiring contracts, extraction due) are computed from synced
  detail data. Each threshold alert must declare its source route; a threshold alert whose source
  route is not in the sync set is a build-time error. Structure and starbase fuel alerts depend on
  `/corporations/{id}/structures` and `/corporations/{id}/starbases/{starbase_id}` respectively.
* **Delivery Channels:** SMTP (email), Slack webhooks, Discord webhooks, with per-group and
  per-user routing and mentions. A coalesced roll-up that exceeds a channel's payload limit is
  truncated with an explicit remainder count, never dropped.
  * **[v3.1 — known limitation, recorded in Phase 14.1] Per-USER email routing is not
    implemented, and cannot be without a schema change.** `app.user` (§5.2 #1) has no email
    column and EVE SSO never supplies an address, so a routing rule with `target_kind = 'user'`
    has nothing to resolve to a recipient. SMTP channels therefore carry an installation-wide
    recipient list in `app.alert_channel.config.to` (`HANGAR_SMTP_TO`), and user-targeted rules
    route to the chat channels, which identify a user by a platform handle they have already
    linked. Closing this needs an `app.user.email` column *and* an address-verification flow —
    an unverified address is a delivery target and therefore an abuse vector — which is a phase
    of its own, not a Phase 14 omission.
  * A webhook URL is a **credential**: it carries its own token and anyone holding it can post to
    the channel. It must never reach a stored error string or a log line
    (`internal/alerting/channels.scrubURL`, added in Phase 14 after the image verification caught
    an unreachable endpoint writing its full URL to the dead-letter board).
* **Generic Fallback:** CCP notification YAML shape changes must never halt the queue.
  Unrecognised payloads render as generic key/value pairs and are logged to the unknown-types
  board. Per Principle 14, unknown notification types are ingested, never rejected. CCP payloads
  are not always strictly valid YAML; a parse failure is an expected path, not an exception.
* **Ingestion Tuning:** The `char-notification` bucket is limited to 15 tokens / 15 minutes.
  Polled at 600s with jitter, holding a permanent 5-token reserve against bucket exhaustion.
* **Delivery Guarantees:** Transactional outbox, hash-based deduplication stable across process
  restarts, coalescing window roll-ups, and an admin-visible dead-letter queue. An alert is lost
  only if it was neither delivered nor dead-lettered; dead-lettering is a visible outcome, not a
  loss.

### 4.5 ESI Scope Subsystem
* **Opaque catalogue.** Scopes are read from the spec's `securitySchemes` and from each
  operation's `security` block, and stored as `text` primary keys. HANGAR **must not** parse,
  validate, version-match, or pattern-check scope strings.
* At least two grammars are in production simultaneously and more are expected:
  * `esi-<group>.<action>.v1` — 70 scopes as of the 2026-05-19 spec.
  * `esi.<domain>.<subject>:<action>` — introduced 2026-08-04 (e.g. `esi.activity.char:read`).
  Any implementation that assumes the first grammar will reject the second.
* **Requirement enforcement** maps route → required scopes directly from the ingested spec.
  Corporation role requirements come from `x-required-roles`, whose values are likewise an open
  vocabulary.
* **Re-authorization:** Configuration changes to the requested scope set force a re-authorization
  flow, integrated with Strict Mode. Newly required scopes are surfaced to administrators before
  enforcement.

### 4.6 Localisation and ESI Language Resolution **[v3.1 — B7]**
HANGAR's UI locale set and ESI's `Accept-Language` enum are **different sets**, and conflating
them produces cache keys ESI will reject. The resolution table is authoritative:

| HANGAR UI locale | ESI `Accept-Language` | Note |
| :--- | :--- | :--- |
| `en` | `en` | |
| `de` | `de` | |
| `fr` | `fr` | |
| `ja` | `ja` | |
| `ko` | `ko` | |
| `ru` | `ru` | |
| `zh-CN` | `zh` | Region subtag stripped |
| `af` | `en` | No ESI equivalent — falls back |
| `ro` | `en` | No ESI equivalent — falls back |

* ESI additionally supports `es`, which has no legacy UI locale. Adding `es` to the UI is a
  post-v1.0 item (§12) and is not a parity requirement.
* **Cache keys use the resolved ESI language, never the UI locale.** `af`, `ro` and `en` users
  therefore share one cache entry, which is correct and intended.
* **[v3.1 — B7] Single source of truth.** The table is defined once, as
  `internal/i18n/locales.json`. It is embedded into the Go binary via `embed.FS` and imported by
  the SPA build. A hand-maintained second copy in TypeScript is prohibited, because the two would
  drift and the drift would only surface as an ESI cache-key rejection in production.
* Both the Go and TypeScript consumers run an exhaustive table-driven test over the same file. Any
  UI locale without a mapping is a compile-time error on both sides.

### 4.7 Search and Discovery Controls
The public `/search` route no longer exists. All search is performed through
`/characters/{character_id}/search`, which requires an acting character token and the search
scope. Consequently:

* `POST /api/v1/support/search` requires a resolved acting character; it is not available to
  unauthenticated or character-less sessions, which must receive a specific error explaining the
  requirement rather than a generic authorization failure.
* CCP prohibits using ESI to discover structures, characters or other entities. HANGAR must
  therefore restrict result visibility to entities the requesting user can already see under RBAC,
  apply a per-user search rate limit, and log every search to the security audit log.
* The UI must not present an unrestricted entity-lookup surface.

### 4.8 Health and Status
Two distinct upstream signals, never conflated:

* **ESI service health** — `GET /meta/status` (status values OK, Degraded, Down, Recovering;
  covers all routes). This replaces `/status.json`, removed 24 March 2026, and `/ping`, which is
  not in the OpenAPI spec. This signal, and only this signal, informs gateway scheduling
  decisions.
* **Tranquility server status** — `GET /status` (players online, server version, VIP mode). This
  is game-server state and drives the dashboard, not gateway health decisions.

**Token verification is offline only.** The `/verify` endpoint began redirecting on 24 March 2026
and the redirect was removed 28 April 2026. `internal/sso/` validates JWTs locally against cached
JWKS; no verification round-trip is permitted, and no code path capable of making one may exist.

### 4.9 Events and Webhooks
Transactional outbox producing signed outbound webhooks (HMAC-SHA256) for third-party consumers.
Data mutation and webhook enqueue occur in the same transaction. This is the sole extension
mechanism for out-of-process integrations (§1.3). Signature verification must be constant-time,
and a reference verification script ships with the release so integrators can prove their
implementation.

---

## 5. Database Schema Design & Principles

### 5.1 Design Rules
* **Exact ISK:** All money columns are `NUMERIC(30,2)`, serialized as strings in JSON. No
  `float64` on any money path. Values that are not money — quantities, volumes in m³, standings,
  run counts, positions — must not be typed as money.
* **Identifiers are typed by the spec**: column type is derived from the OpenAPI schema of the
  corresponding field.
  * `type: integer, format: int64` → `bigint`
  * `type: string, format: uuid` → `uuid`
  * Coercion between the two is prohibited, and storing a UUID as `text` is likewise prohibited.
    Routes already using UUID identifiers include corporation projects, freelance jobs, mercenary
    tactical operations, and military campaigns.
  * A generation-time check compares every identifier column against the ingested spec; a mismatch
    fails the build. An identifier whose type changes between two ingests must fail loudly rather
    than coerce.
* **Vocabularies:** Open CCP vocabularies (`ref_type`, `location_type`, notification `type`, scope
  strings, `x-required-roles` values) are `text` linked to lookup tables. No Postgres `ENUM`s for
  external data. **[v3.1 — B11]** Vocabularies HANGAR itself owns may be closed sets.
* **Time-Series Partitioning:** `wallet_journal`, `wallet_transaction`, `character_notification`,
  `killmail`, and `market_history` use `PARTITION BY RANGE` (monthly), with a `DEFAULT` partition
  and maintenance that creates at least three months ahead.
* **Updates:** `updated_at` changes only when data values actually change
  (`INSERT ON CONFLICT DO UPDATE ... WHERE ... IS DISTINCT FROM`).
* **Mutations:** Destructive DML (DELETE, TRUNCATE) is banned in Goose migrations. Retention is by
  partition detachment; removal of synced rows is by soft delete.

### 5.2 Key Table Structures **[v3.1 — B1, B2]**

**Schema size.** The complete schema is **≈129 tables**, in two tiers:

| Tier | Contents | Count | Delivered by |
| :--- | :--- | :--- | :--- |
| **1 — Platform** | identity and access, RBAC and squads, ESI gateway and sync metadata, access provisioning, alerting, events, shared reference and open vocabularies | **51** | Phase 1a |
| **2 — Domain projections** | the ESI datasets behind §6, using owner-polymorphic tables (`owner_kind`, `owner_id`) wherever a concept exists for both characters and corporations | **≈78** | Phase 1b |

Owner polymorphism is a requirement, not a preference: it is what keeps the schema at this size and
makes the character and corporation handlers one implementation rather than two.

Named structures:

* **RBAC:** `app.role`, `app.permission` (a HANGAR-owned closed set, seeded from Go with a CI
  divergence check — see Principle 14), `app.role_grant`, `app.user_role`,
  `app.effective_permission`.

  > **[v3.1 — Phase 15.1] Every §6 endpoint group must name a permission.**
  > Phase 15 found that the closed set covered characters, corporations,
  > squads and the *write* side of administration, but had nothing at all for
  > §6.4 (alliances/sovereignty), §6.5 (markets), §6.7's reference lookups, or
  > the *read* side of §6.8 — so those routes shipped gated on "any resolved
  > session" or on `superuser`, the latter meaning an operator who should only
  > see sync health had to be handed the one permission that bypasses every
  > other check. Phase 15.1 added the twelve missing names
  > (`alliances.view`, `sovereignty.view`, `markets.view`, `tools.view`,
  > `admin.sync.view`, `admin.esi.view`, `admin.platforms.view`,
  > `admin.scopes.view`, `provisioning.exposures.view`,
  > `alerting.unknown_types.view`, `alerting.deadletter.view`,
  > `alerting.deadletter.requeue`).
  >
  > Two groups remain **deliberately** session-only, and this is a design
  > decision rather than an outstanding gap: the `/api/v1/me*` family is
  > self-scoped by definition (a permission governing "may this user read
  > their own account" is meaningless), and `/api/v1/meta/*` is installation
  > health the SPA shell renders for every signed-in user — gating it would
  > break the dashboard for ordinary members.
* **Provisioning:** `app.platform`, `app.platform_group`, `app.entitlement_rule`,
  `app.provisioning_state`, `app.provisioning_audit`. The audit table records both the originating
  `event_at` and `platform_call_completed_at`, because the revocation SLO is measured across queue
  wait.
* **Sync & Metadata:** `app.esi_route` (including `upstream_path`, `cache_mode`,
  `pagination_style`, `compatibility_date`, `blocked_by_pin`, `identifier_types`),
  `app.esi_scope` (`scope text primary key`), `app.esi_route_scope`, `app.esi_route_role`,
  `app.esi_pin_history`, `app.sync_subscription`, `app.sync_run`, and the `sde` schema.
* **[v3.1 — B1] Cluster-shared rate governor state:** `app.esi_ledger_bucket` and
  `app.esi_ledger_entry` (both UNLOGGED) hold the Governor 1 consumption ledger;
  `app.esi_error_budget` holds the Governor 2 budget; `app.esi_replica` is the heartbeat registry
  that selects `solo` or `clustered` mode. These are UNLOGGED because losing them on crash costs a
  conservative re-reconciliation from response headers, not a correctness failure.
* **Conditional cache:** `app.esi_cache_entry`, UNLOGGED.
* **Open vocabularies:** `app.open_vocabulary`, keyed `(vocabulary, value)` with `first_seen_at`
  and a nullable `acknowledged_at`. One table serves every administrator "unknown value" board.
* **Assets:** `app.asset` with compound PK `(owner_kind, owner_id, item_id)` to survive asset
  transfers, designed for single-query `WITH RECURSIVE` tree generation with both a depth bound and
  a cycle guard.
* **UUID-keyed tables:** `app.corporation_project (project_id uuid)`,
  `app.corporation_project_contributor`, `app.corporation_project_contribution`, and — post-v1.0 —
  `app.freelance_job (job_id uuid)`, `app.military_campaign (campaign_id uuid)`,
  `app.military_campaign_objective (objective_id uuid)`.
* **Detail tables added for parity:** `app.mail_body`, `app.contract_item`, `app.contract_bid`,
  `app.starbase_detail`, `app.planet_colony_detail`, `app.calendar_event_detail`,
  `app.corporation_skyhook`, `app.corporation_sovereignty_hub`.

---

## 6. API Endpoints Contract (OpenAPI 3.1)

All endpoints reside under `/api/v1`. Responses include keyset pagination cursors and a `_sync`
envelope detailing data freshness (`last_modified_at`, `next_due_at`, `stale`, `blocked_by_pin`).
Internal cursors are opaque base64; `limit` accepts 10–100 with a default of 50; `OFFSET` is
prohibited.

Data from a `blocked_by_pin` route is rendered as **unavailable with an administrator-facing
explanation**, never as an empty result. Empty and unavailable are distinct states.

**Structured columns are nested JSON on the wire. [v3.1 — B12]** A PostgreSQL `jsonb` column — a
starbase's fuel bay, a skyhook's or sovereignty hub's reagents, a structure's service list, a
planetary colony's pins/links/routes, an ESI pin advance's route diff — is emitted as a nested
JSON object or array, never as an encoded scalar. A client must never have to decode a field of a
HANGAR response before it can read it. Hex encoding is reserved for genuinely binary columns
(hashes, ciphertext, wrapped key material), none of which may be serialised to a client at all.

**Dates are date strings and intervals are seconds. [v3.1 — B14]** A PostgreSQL `date` column — a
route's `compatibility_date`, a pin advance's `old_pin`/`new_pin` — is emitted as a `YYYY-MM-DD`
string, never as a timestamp (which would invite a local-time conversion that shifts it a day) and
never as the driver's own struct. An `interval` column — a route's `cache_age`, a rate-limit
window — is emitted as whole seconds. A NULL of either is `null`, never a zero date or a zero
duration: the same empty-versus-unavailable distinction this section draws for collections.

The endpoint set is unchanged from v3.0 and is reproduced here in full so that this document is
self-contained. `docs/03_IMPLEMENTATION_ROADMAP.md` records the phase that delivers each group.

### 6.1 Authentication, Users & Third-Party Tokens
* `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout`
* `GET /api/v1/me`, `GET /api/v1/me/characters`, `POST /api/v1/me/characters/{id}/reauthorize`,
  `DELETE /api/v1/me/characters/{id}`
* `GET /api/v1/me/share-links`, `POST /api/v1/me/share-links`,
  `DELETE /api/v1/me/share-links/{id}`
* `GET /api/v1/api-tokens`, `POST /api/v1/api-tokens`, `DELETE /api/v1/api-tokens/{id}`,
  `GET /api/v1/api-tokens/access-log`

### 6.2 Character Endpoints
* `GET /api/v1/characters/{id}` — includes `corporation_title_id`, `character_title_id`,
  `achievement_score` (pin 2026-08-04)
* `GET /api/v1/characters/{id}/skills`, `/skillqueue`, `/attributes`, `/clones`, `/implants`
* `GET /api/v1/characters/{id}/fittings`, `/fittings/{fitting_id}`, `/fittings/{fitting_id}/eft`
* `GET /api/v1/characters/{id}/assets`, `/assets/tree/{location_id}`
* `GET /api/v1/characters/{id}/wallet/journal`, `/wallet/transactions`, `/wallet/summary`
* `GET /api/v1/characters/{id}/contracts`, **`/contracts/{contract_id}/items`**,
  **`/contracts/{contract_id}/bids`**
* `GET /api/v1/characters/{id}/industry/jobs`, `/mining`
* `GET /api/v1/characters/{id}/planets`, **`/planets/{planet_id}`**
  *(colony pins, extractors, routes)*
* `GET /api/v1/characters/{id}/calendar`, **`/calendar/{event_id}`**,
  `/calendar/{event_id}/attendees`
* `GET /api/v1/characters/{id}/mail`, **`/mail/{mail_id}`** *(body)*, `/mail/labels`, `/mail/lists`
* `GET /api/v1/characters/{id}/notifications`, **`/notifications/contacts`**
* `GET /api/v1/characters/{id}/contacts`, `/contacts/labels`, `/killmails`
* `GET /api/v1/characters/{id}/blueprints`, `/agents_research`, `/loyalty/points`, `/medals`,
  `/standings`, `/titles`, `/roles`, `/corporationhistory`, `/fatigue`, `/location`, `/online`,
  `/ship`
* **`GET /api/v1/characters/{id}/intel`** *(interaction graph derived from mail, contacts,
  killmails and standings)*

### 6.3 Corporation Endpoints
* `GET /api/v1/corporations/{id}` — includes palette fields (2026-07-21)
* `GET /api/v1/corporations/{id}/members`, `/member-tracking`, `/members/limit`,
  **`/members/titles`**, `/roles`, `/roles/history`, `/titles`, `/divisions`
* `GET /api/v1/corporations/{id}/wallets`, `/wallets/{division}/journal`,
  `/wallets/{division}/transactions`
* `GET /api/v1/corporations/{id}/assets`, `/assets/tree/{location_id}`
* `GET /api/v1/corporations/{id}/projects`, **`/projects/{project_id}`**,
  `/projects/{project_id}/contributors`,
  **`/projects/{project_id}/contribution/{character_id}`**
* `GET /api/v1/corporations/{id}/structures`, **`/structures/skyhooks`**,
  **`/structures/skyhooks/{skyhook_id}`**, `/structures/sovereignty-hubs`,
  **`/structures/sovereignty-hubs/{hub_id}`**
* `GET /api/v1/corporations/{id}/starbases`, **`/starbases/{starbase_id}`**
  *(fuel bay and settings — required by the fuel-low alert)*
* `GET /api/v1/corporations/{id}/customs-offices`, `/containers/logs`, `/facilities`
* `GET /api/v1/corporations/{id}/contracts`, **`/contracts/{contract_id}/items`**,
  **`/contracts/{contract_id}/bids`**
* `GET /api/v1/corporations/{id}/industry/jobs`, `/blueprints`, `/killmails`
* `GET /api/v1/corporations/{id}/mining/extractions`, `/mining/observers`,
  `/mining/observers/{observer_id}` *(upstream paths are singular — see §4.1.2)*
* `GET /api/v1/corporations/{id}/medals`, `/medals/issued`, `/standings`, `/shareholders`,
  `/contacts`, `/contacts/labels`, `/alliancehistory`
* `GET /api/v1/corporations/{id}/ledger/bounties`, `/ledger/pi`, `/ledger/mining`

### 6.4 Alliance & Sovereignty Endpoints
* `GET /api/v1/alliances`, `/alliances/{id}`, `/alliances/{id}/corporations`,
  `/alliances/{id}/contacts`, `/alliances/{id}/contacts/labels`
* `GET /api/v1/sovereignty/campaigns`, `/sovereignty/systems`

### 6.5 Market Endpoints
* `GET /api/v1/characters/{id}/orders`, `/characters/{id}/orders/history`
* `GET /api/v1/corporations/{id}/orders`, `/corporations/{id}/orders/history`
* `GET /api/v1/markets/{region_id}/orders`, `/markets/{region_id}/history`,
  `/markets/{region_id}/types`
* `GET /api/v1/markets/prices`

> **Scope clarification [v3.1 — Phase 15.1].** `/markets/{region_id}/orders`
> and `/markets/{region_id}/types` return the orders **HANGAR has synced for
> the characters and corporations it tracks**, region-scoped — not the
> complete public regional order book. HANGAR syncs orders per tracked owner
> (`/characters/{id}/orders`, `/corporations/{id}/orders`); ingesting every
> public order would mean hundreds of thousands of rows per region across
> ~100 regions, a scale commitment nothing in this document makes.
> `app.market_order` was designed for the region-scoped read — Phase 1b gave
> it both a `region_id` column and a dedicated index on it, an index no
> owner-scoped query can use. Phase 15 misread "no complete public order
> book" as "no backing table" and answered 501; Phase 15.1 implemented the
> read and recorded this note so the ambiguity cannot recur.

### 6.6 Squads
* `GET /api/v1/squads`, `POST /api/v1/squads`, `PATCH /api/v1/squads/{id}`,
  `DELETE /api/v1/squads/{id}`
* **`GET /api/v1/squads/{id}/members`**, **`POST /api/v1/squads/{id}/members`**,
  **`DELETE /api/v1/squads/{id}/members/{character_id}`**
* **`GET /api/v1/squads/{id}/moderators`**, **`PUT /api/v1/squads/{id}/moderators`**
* **`GET /api/v1/squads/{id}/roles`**, **`PUT /api/v1/squads/{id}/roles`**
* `GET /api/v1/squads/{id}/applications`, `POST /api/v1/squads/{id}/applications/resolve`

### 6.7 Utilities & Support
* `POST /api/v1/public/mumble/auth` *(shared-secret signed; the only unauthenticated write route)*
* `POST /api/v1/tools/moon-report/parse`
* **`POST /api/v1/support/search`** *(acting character required — see §4.7)*
* `POST /api/v1/support/resolve` *(names and affiliations)*,
  `GET /api/v1/support/universe/structures`, `/support/universe/stations`
* `GET /api/v1/tools/insurance`, `/tools/character/{id}/notes`, `/tools/standings`
* **`GET /api/v1/meta/esi-status`** *(ESI service health — drives gateway decisions)*
* **`GET /api/v1/meta/server-status`** *(Tranquility: players online, VIP mode, version)*

> **[v3.1 — Phase 15.1]** `/meta/server-status` is backed by a global sync of
> ESI's `GET /status/` (upstream `x-cache-age` 30s), stored as a single
> overwritten row in `app.setting` under `esi.server_status` — one global
> value with no history, no owner and no foreign keys, which is what
> `app.setting` is for. Before the first successful sync the route renders
> **unavailable with an explanation**, never zeroes: "0 players online" would
> be a false reading, not an empty result. The two status endpoints remain
> strictly distinct — `/meta/esi-status` is ESI's own service health and
> drives gateway decisions; this one is the game server's state and drives
> the dashboard.

### 6.8 Administration & Observability
* `GET /api/v1/admin/sync/routes`, `/sync/subscriptions`, `/sync/health`
* **`GET /api/v1/admin/esi/catalogue/blocked`** *(routes gated by the compatibility pin)*
* **`POST /api/v1/admin/esi/catalogue/pin/preview`** *(compute the full route diff for a candidate
  compatibility date — newly blocked **and** newly unblocked routes — without changing any state;
  new in v3.1, §0 B13)*
* **`POST /api/v1/admin/esi/catalogue/pin`** *(advance the compatibility date. Rejects a candidate
  newer than `D_max` server-side, and records the computed route diff — never an empty one.
  Preview is a separate call above, because Principle 12 requires the administrator to see the
  diff **before** the pin moves; one mutating endpoint cannot satisfy that. **[v3.1 — B13]**)*
* `GET /api/v1/admin/esi/ratelimits`, `/esi/errorlimit`
* **`GET /api/v1/admin/esi/replicas`** *(live replica registry and the resulting ledger mode —
  new in v3.1, §4.1.3)*
* **`GET /api/v1/admin/esi/catalogue/pin/history`** *(recorded pin advances, each with its
  computed route diff — new in v3.1, §0 B13: a diff nobody can read afterwards is not an audit
  trail)*
* `GET /api/v1/admin/platforms`, `POST /api/v1/admin/platforms/{id}/rules/preview`,
  `POST /api/v1/admin/platforms/{id}/lockdown`
* **`GET /api/v1/admin/platforms/{id}/groups`**, **`GET /api/v1/admin/platforms/{id}/rules`**,
  **`PUT /api/v1/admin/platforms/{id}/rules`** *(the rule editor's read and write sides. The PUT is
  a full transactional replace and requires the `preview_token` the preview endpoint returned for
  that exact rule set: an unpreviewed — or a since-edited — rule set cannot be saved by any client.
  New in v3.1, §0 B18)*
* **`GET /api/v1/admin/provisioning/exposures?platform_id=`** *(query parameter, not a path
  segment — §0 B16)*, `/provisioning/audit`
* `GET /api/v1/admin/alerts/dead-letter`, `/alerts/unknown-types`
* **`POST /api/v1/admin/alerts/unknown-types/{type}/acknowledge`** *(writes `acknowledged_at` —
  new in v3.1, §0 B17)*
* **`GET /api/v1/admin/scopes/unknown`** *(newly observed scope strings pending acknowledgement)*
* **`POST /api/v1/admin/scopes/unknown/acknowledge`** *(the scope travels in the BODY: it is
  opaque under Principle 14 and may carry characters a proxy would normalise in a path segment.
  New in v3.1, §0 B17)*
* `GET /api/v1/admin/scopes`, `PUT /api/v1/admin/scopes`
* `GET /api/v1/admin/users`, `PATCH /api/v1/admin/users/{id}`
* `GET /api/v1/admin/security-log`

---

## 7. Development Roadmap (Phased Implementation)

Development is sequenced into 21 phases (0–20). Each phase must pass isolated integration tests
before the next begins. `docs/03_IMPLEMENTATION_ROADMAP.md` is the granular execution plan; this
section states the objective and exit criteria that plan must satisfy.

### Phase 0: Repository & Toolchain Bootstrap
* **Objective:** Skeleton Go backend, React/Vite SPA shell, structured `slog` logging with secret
  redaction, Docker multi-stage builds, CI.
* **[v3.1 — B6] Additional deliverable:** `docs/BASELINE.md`, produced by measuring the legacy
  repositories at HEAD. Gate 4 is verified against this file.
* **Exit:** `make ci` passes; `docker compose up` starts healthy; redact handler strips secrets
  via recursive test; static binary builds cleanly for all three targets; CI pushes the image to a
  public registry; `docs/BASELINE.md` exists and records the command used for each measurement.

### Phase 1: Database Schema & Migrations **[v3.1 — B2]**
Split into two sub-phases, both of which must land before Phase 2.

* **Phase 1a — Platform tier.** The 51 tables of §5.2 Tier 1, including the cluster-shared rate
  governor tables and the replica registry.
* **Phase 1b — Domain tier.** The ≈78 owner-polymorphic domain projection tables, including the
  UUID-keyed and detail tables of §5.2.
* **Exit:** Clean `goose up`/`down`; `sqlc generate` clean; recursive asset-tree CTE proven with
  both a depth bound and a cycle guard; **reflection test proves zero `float64` on money paths**;
  **identifier-type check proves every identifier column matches the spec's declared type,
  including `uuid` columns**; no destructive DML in any migration; partition maintenance proven to
  create three months ahead.

### Phase 2: Route Catalogue (OpenAPI Ingest)
* **Objective:** Map `openapi.json` into `app.esi_route`.
* **Exit:** All operations mapped with a non-zero floor on ingest count; **test asserts the spec
  is fetched at `D_max` and never at the app pin**; test asserts routes newer than the pin are
  marked `blocked_by_pin` and excluded from scheduling; test asserts 11:00 UTC rollover; test
  proves offline boot uses the embedded snapshot and marks the catalogue stale; **test asserts
  `upstream_path` is stored verbatim, using `/corporation/{corporation_id}/mining/extractions` as
  the singular-path fixture**; test asserts an unrecognised `x-cache-mode` is recorded and
  scheduled as `ttl-based`.
* **[v3.1] Additional deliverable:** the Gate 6 synthetic drift spec is authored and committed at
  the end of this phase, not at Phase 20. A gate fixture written in response to a failure does not
  test what it claims to.

### Phase 3: ESI Gateway I (HTTP Core & Cache)
* **Objective:** Conditional requests via ETag and `Last-Modified`; two-tier cache.
* **Exit:** 304 proven on a cached ETag; torn pagination (mismatched `Last-Modified` across pages)
  discards the payload; **`no-cache` routes prove no L1/L2 write and no conditional headers**;
  **cache key proven to use the resolved ESI language, with `af` and `en` sharing an entry**;
  normalization proven never to rewrite a path segment; Redis L2 failure proven to degrade to a
  miss.

### Phase 4: ESI Gateway II (Rate Limiting & Resilience) **[v3.1 — B1, B5, B8, B9]**
* **Objective:** The cluster-shared floating-window consumption ledger and the error-limit engine.
* **Scope:** `internal/esi/ratelimit/`, predictive reservation, header reconciler, the replica
  registry and automatic `solo`/`clustered` mode selection, per-route and per-entity circuit
  breakers.
* **Exit:**
  * **Ledger fidelity test:** a simulated 15-minute window proves tokens are released exactly one
    window after each individual request, and that a continuous-refill bucket would have
    diverged — the test must fail if a refill model is substituted.
  * Predictive reservation proven: a run of 4XX responses does not overdraw the window.
  * 429 snoozes the subscription for exactly `Retry-After` without blocking siblings; 429 charges
    zero cost; a transport error charges the worst case.
  * Header reconciler processes 429 responses that carry no rate-limit headers, and converges in
    both directions.
  * 420 triggers a global pause; proactive pause fires at the configured remaining threshold
    before a 420 is observed; resume uses the higher hysteresis threshold.
  * **Cluster correctness:** three replicas sharing one bucket never exceed `max_tokens` in
    aggregate; the `solo` → `clustered` and `clustered` → `solo` transitions lose no entries and
    double-count none.
  * **Performance:** the `solo` in-process path completes 1M ledger operations in < 2 seconds; the
    `clustered` path sustains ≥ 2,000 acquire/settle pairs per second per replica at p99 < 10 ms.

### Phase 5: EVE SSO & Token Lifecycle
* **Objective:** PKCE SSO, offline JWT validation, AES-256-GCM envelope encryption, opaque scope
  catalogue.
* **Exit:** End-to-end token exchange passes; **JWT validated entirely offline against cached JWKS
  with no network round-trip**; the `scp` claim parses in both its string and array forms;
  an `owner` hash change invalidates every token for the character; concurrent refreshes are
  serialised by advisory lock and produce exactly one rotation; AAD test prevents decryption under
  a mismatched character ID; CLI bootstrap token issues correctly; **scope catalogue ingests both
  `esi-<group>.<action>.v1` and `esi.<domain>.<subject>:<action>` grammars, and an adversarial test
  proves no regex or pattern validation rejects an unknown scope shape.**

### Phase 6: Sync Engine (Planner & Subscriptions)
* **Objective:** Job generator evaluating `next_due_at`.
* **Exit:** `FOR UPDATE SKIP LOCKED` proven atomic; 1.5ⁿ backoff proven on 304s and reset on 200;
  **table-driven test covers all four cache-mode cases including absent → `ttl-based`, and proves
  `x-cache-age: 0` resolves to `ttl_floor`, never 0**; `blocked_by_pin` and snoozed subscriptions
  excluded in the claim predicate; 30-minute soak creates zero duplicate jobs.

### Phase 7: Route Handlers I (Character Core)
* **Objective:** Character endpoints with idempotent bulk upserts.
* **Exit:** Golden-file tests parse recorded ESI responses; update tests prove zero `updated_at`
  changes on unchanged data; character DTO matches the 2026-08-04 pin; a data-level 404 does not
  advance a breaker counter.

### Phase 8: Route Handlers II (Corporation & Wallets)
* **Objective:** Corp endpoints and partitioned exact-money ledgers.
* **Exit:** Wallet sync passes page-mechanism cursor tests including torn-set discard; exact-money
  round-trip proven at 10²⁰ with no precision loss; **starbase detail populates the fuel bay used
  by the fuel-low alert**; skyhook and sovereignty-hub detail round-trip; the singular mining
  paths are issued verbatim from `upstream_path`; acting-character re-election on 403 is
  deterministic and does not disable the subscription.

### Phase 9: Route Handlers III (Assets, Contracts, Market, Notifications, SDE)
* **Objective:** Remaining complex datasets and SDE streaming.
* **Exit:** Asset reconciliation soft-deletes missing items and restores reappearing ones;
  unparseable notification YAML imports as JSONB and does not halt the queue; SDE atomic swap
  passes and leaves the live schema untouched on failure; **contract items, mail bodies and colony
  detail each round-trip from a recorded fixture**; **UUID-keyed project rows insert and join
  without coercion**; the mail-body call is routed through the route catalogue rather than inline.

### Phase 10: RBAC & Authorization
* **Exit:** Truth-table tests prove `deny` precedes `allow` across all seven sources; the
  materialised projection agrees with a from-scratch recomputation; a user with no roles has no
  permissions; 5000-user benchmark resolves < 2ms.

### Phase 11: Access Provisioning Core (Entitlements & Revocation)
* **Exit:** Strict mode fails a user if any alt token is invalid; the revocation job is enqueued in
  the mutating transaction and rolls back with it; p99 < 60s revocation under load measured from
  the originating event with the bulk queue saturated; dry-run preview returns exact gains and
  losses per user.

### Phase 12: Discord Driver
* **Exit:** API version enforced via config allowlist at boot; processing halts at 80% of the
  Cloudflare invalid-request budget; Cloudflare 1015 blocks parsed from both HTML and JSON framing
  without being reported as transport errors; role-hierarchy guard blocks assignments above the
  bot without issuing the request; buckets keyed on the response header rather than the URL.

### Phase 13: TeamSpeak & Mumble Drivers **[v3.1 — B4]**
* **Scope:** TS3 WebQuery; Mumble gRPC. The ZeroC Ice bridge is a separate, optional deliverable
  and is **not** in the HANGAR binary.
* **Exit:** Challenge tokens verified and immediately consumed, with a second redemption failing;
  TS3 escaping round-trips values containing spaces, pipes and slashes; a TS3 `error id=` inside an
  HTTP 200 is detected as a failure; Mumble gRPC adds and removes ACL groups;
  external-authenticator mode denies connection; fail-closed behaviour is opt-in and documented.

### Phase 14: Alerting & Notifications
* **Objective:** Delivery pipeline with coalescing and dead-lettering across SMTP, Slack and
  Discord.
* **Exit:** Unrecognised CCP types deliver via the fallback renderer; 40 coalesced events render as
  one message within each channel's size limit; `char-notification` polling tested at 600s with the
  5-token reserve intact; dedupe hashes stable across restart; **build-time check proves every
  threshold alert's source route is present in the sync set**; the alert catalogue seeds exactly 54
  types with the specified per-domain counts.

### Phase 15: HTTP API Layer
* **Objective:** Serve frontend routes and generate `openapi.json`.
* **Exit:** Generated `api.d.ts` clean and diff-free; adversarial query tests reject
  non-whitelisted filters with 422; zero `OFFSET` in SQL output; **cursor validation rejects
  simultaneous `after` and `before`, enforces `limit` 10–100, and handles the `'0'` sentinel in
  both directions**; search without an acting character returns the specific error and is audited;
  `blocked_by_pin` data renders unavailable rather than empty.

### Phase 16: Frontend I (Shell, Auth, Dashboard, Localisation) **[v3.1 — B3, B7]**
* **Exit:** SPA entry chunk < 250KB gzipped; ESLint blocks `Number()` on ISK strings; ESLint blocks
  hardcoded English in `.tsx`; **exhaustive test proves all 9 UI locales resolve to a valid ESI
  `Accept-Language`, with `af`/`ro` → `en` and `zh-CN` → `zh`, and that an unmapped locale fails
  the build — run against `internal/i18n/locales.json` on both the Go and TypeScript sides**;
  breadcrumbs derived from router state with no per-page definition; **exactly one `.css` file
  exists under `web/src/`, and CI fails if a second appears.**

### Phase 17: Frontend II (Character & Corporation Views)
* **Exit:** Virtualized wallet UI scrolls 100k rows at 60fps; asset tree renders to depth 5 in
  < 2s from a single request; `SyncBadge` displays fresh, stale and blocked-by-pin as distinct
  states; contract detail renders items and bids including the empty-items courier case; mail
  reader renders and sanitises bodies; a failing data module does not crash its route.

### Phase 18: Frontend III (Admin & Observability)
* **Exit:** Entitlement rule UI mandates preview confirmation before saving; exposure board lists
  pending revocations with exact ages computed from the originating event; pin-advance preview
  shows the route diff — including newly blocked routes — before the pin can be changed, and
  refuses a date newer than `D_max`; unknown-value boards support acknowledgement.

### Phase 19: Events, Webhooks & Third-Party Migration
* **Exit:** Test verifies atomic generation of data and webhook trigger by rolling back and proving
  neither survives; signature validation proven with the reference script; **shim returns
  legacy-shaped payloads for all nine legacy controllers' read routes and emits `Deprecation` and
  `Sunset` headers**; the `_sync` envelope is stripped; reshaped routes return a documented
  breaking-change response rather than a partial shim.

### Phase 20: Load Testing & Release
* **Objective:** Pass all **seven** release gates.
* **Exit Criteria — Release Gates:**
  * **Gate 1 (ESI Load Stability):** 4-hour, 5000-character test with zero rate-limit breaches
    across both governors, and zero divergence between the ledger and `X-Ratelimit-Remaining`
    beyond a one-request tolerance. **[v3.1 — B1] The gate is executed at N=1 and at N=3 replicas,
    and both results are recorded.** A pass at one replica count certifies only that count.
  * **Gate 2 (Revocation SLO):** 5000 identities across 3 platforms meeting < 60s revocation p99,
    measured from the originating event with the bulk reconciliation queue saturated.
  * **Gate 3 (Alert Delivery Integrity):** 4-hour alert load test drops zero alerts, where the
    accounting identity `generated = delivered + coalesced + dead_lettered + deduplicated` holds
    exactly.
  * **Gate 4 (Feature Parity):** All 58 capabilities verified against **`docs/BASELINE.md`**, the
    measured baseline produced in Phase 0. **[v3.1 — B6]** A disagreement between the measured
    counts and Appendix B is a specification defect that blocks this gate.
  * **Gate 5 (Deployment Usability):** `docker-compose.yml` deploys from a blank environment in 3
    commands, with no source compilation, with Redis absent.
  * **Gate 6 (Spec-Drift Resilience):** A synthetic spec containing (a) a route with a
    compatibility date newer than the pin, (b) a UUID path identifier, (c) a novel scope grammar,
    and (d) an unrecognised `x-cache-mode` value is ingested with **zero code changes**, correctly
    gating (a), typing (b), storing (c), and defaulting (d) to `ttl-based`. The fixture is authored
    in Phase 2 and committed unchanged.
  * **Gate 7 (Third-Party Migration):** The `/api/v2` shim returns byte-compatible payloads against
    recorded legacy responses for every migrated read route, where byte-compatible includes field
    order and JSON formatting.

---

## 8. User Interface & Experience (UI/UX) Requirements

### 8.1 Design System & Theming **[v3.1 — B3]**
* **Component Exclusivity:** `shadcn/ui` (Radix primitives) and Tailwind CSS 4 only. No external
  component libraries (MUI, AntD, Bootstrap). shadcn components are vendored into the repository
  and owned by it; they are not an npm dependency.
* **Stylesheets.** All component styling is expressed as Tailwind utilities. **Exactly one
  stylesheet may exist under `web/src/`**: `web/src/styles/index.css`, whose contents are
  restricted to the Tailwind import, the `@theme` design-token block, the dark-mode custom variant,
  and the shadcn base layer. No component-level, module-level or page-level `.css` file may be
  created. CI fails the build if a second `.css` file appears under `web/src/`.
  *Rationale: Tailwind 4 is CSS-first configured and cannot be configured from JavaScript. The
  v3.0 requirement of zero `.css` files was not buildable; this preserves its intent — no bespoke
  component CSS — while being implementable.*
* **Theme:** Dark-mode-first. Neutral palette maps to `zinc`; destructive actions to `red`;
  informational and active states to `cyan`.
* **Typography:** `sans` configured to `Inter` or system sans-serif. All data tables, ISK values
  and identifiers must use `font-mono` or `tabular-nums` for decimal alignment.

### 8.2 Standardized Layout Patterns
* **App Shell:** Persistent collapsible left sidebar; top header for contextual actions,
  breadcrumbs and session controls.
* **Routing & Breadcrumbs:** TanStack Router nested routes with persistent URL state; every view
  implements a dynamic breadcrumb **derived from router state**, not defined per page.
* **Mobile Responsiveness:** Mobile-first Tailwind prefixes. Complex tables use `overflow-x-auto`
  without introducing horizontal scrolling on the shell itself.

### 8.3 Data Rendering & State Management
* **Virtualized Tables:** `@tanstack/react-table` with `@tanstack/react-virtual` for up to 100k
  rows, through one generic column-driven table component reused by every view.
* **Deterministic Loading:** No blocking full-screen spinners. React `<Suspense>` with
  shape-matched `Skeleton` components.
* **Error Boundaries:** Every distinct data module wrapped in an error boundary rendering a local
  retry, never crashing the route.
* **State ownership:** server state belongs to TanStack Query; URL state to the router; client-only
  state to Zustand. Server data must never be copied into Zustand.
* **Freshness:** Every data surface renders a `SyncBadge` from the `_sync` envelope. Data from a
  `blocked_by_pin` route renders as unavailable with an administrator-facing explanation, never as
  empty.

---

## 9. Deployment & Distribution Strategies

### 9.1 Turnkey Deployment (Novice Administrators)
* **Strategy:** Zero-compilation Docker environment as the primary distribution method.
* **Deliverables:** Pre-configured `docker-compose.yml`, `.env.example`, and
  `install.sh` / `install.bat`.
* **Requirements:** CI builds and pushes the production image to a public registry. The script
  handles database creation, generates the master and session keys, injects connection strings, and
  pulls the pre-compiled image. Users supply only their EVE SSO Client ID and Secret. The default
  compose profile contains PostgreSQL and HANGAR only; Redis is absent.

### 9.2 Manual Deployment (Experienced Administrators)
* **Strategy:** Deployable without Docker on custom orchestration or bare metal.
* **Deliverables:** Self-contained statically linked binaries with the embedded SPA for
  `linux/amd64`, `linux/arm64` and `windows/amd64`, attached to every GitHub Release. **[v3.1 —
  B4]** No component of the binary may require CGO; this is why the Mumble Ice bridge is
  out-of-process.
* **Requirements:** Boots via a standard systemd unit given a valid `.env` and a manually
  provisioned PostgreSQL 18 instance.

### 9.3 Optional Companion Images **[v3.1 — B4]**
* **`hangar-mumble-ice-bridge`** — an optional container providing ZeroC Ice connectivity to
  legacy Murmur deployments, exposing the same gRPC contract the in-binary driver consumes.
  Not required for any release gate.

---

## 10. Third-Party API Migration

Unchanged from v3.0 in substance. Legacy SeAT exposes a versioned `/api/v2` surface across nine
controllers (Alliance, Character, Corporation, Killmails, Role, RoleLookup, Squad, User, plus the
base controller). HANGAR's One API principle replaces this with `/api/v1`.

* **Sunset shim.** `/api/v2/*` read routes are served by a translation layer that maps legacy
  request and response shapes onto `/api/v1` handlers. Write routes are not shimmed and must return
  a clear "not shimmed" response rather than a 404.
* **Signalling.** Every shim response carries `Deprecation: true` and a `Sunset` header (RFC 8594)
  set to the removal date.
* **Duration.** The shim ships in v1.0 and is removed no earlier than two minor versions later.
  Removal requires a release-note announcement at least one minor version in advance.
* **Mapping.** Appendix C records the route-level legacy → HANGAR mapping and flags routes with no
  direct equivalent.
* **Known lossy property.** Legacy emitted money as JSON numbers; HANGAR emits strings
  (Principle 9). Byte compatibility therefore requires the shim to convert back to a number,
  reintroducing IEEE-754 imprecision for large ISK values. This is unavoidable, is a deliberate
  property of the shim, and must be documented in the migration guide with a worked example.
* **Verification.** Gate 7.

---

## 11. Traceability

Every capability in Appendix A maps to: one or more upstream ESI routes (or `n/a` for HANGAR-native
features), the legacy controller(s) it replaces, its alert types where applicable, and the phase
that delivers it. Appendix B holds the reconciliation. A capability without a phase, or a phase
without an exit criterion, is a specification defect.

---

## 12. Post-v1.0 Backlog

Explicitly **not** parity items and **not** in scope for v1.0:

| Item | Upstream | Note |
| :--- | :--- | :--- |
| Military Campaigns | 6 routes, released 2026-08-04 | UUID identifiers; new `esi.activity.char:read` scope grammar; definitions join from SDE |
| Freelance Jobs | 4 routes, released 2025-12-16 | UUID identifiers; cursor pagination |
| Access Lists | 2 routes, released 2026-05-19 | Character access-list read API |
| Mercenary Dens & Tactical Operations | 4 routes | Equinox-era structures |
| Public contracts | 3 routes | Region-scoped public contract browsing |
| Structure market orders | `/markets/{structure_id}` | Player-structure market data |
| `es` UI locale | n/a | Supported by ESI, absent from the legacy locale set |
| Fleets | 7 routes | Never implemented in legacy SeAT |

Because military campaigns join definitions from the SDE, the `sde` schema must remain joinable
from `app` — that is, in the same database, not a separate service.

---

## Appendix A: 58-Capability Feature Parity Matrix

Unchanged from v3.0. Gate 4 requires a 1:1 replacement mapping against the **measured** legacy
footprint recorded in `docs/BASELINE.md`.

| # | Domain | Verified Capability |
| :--- | :--- | :--- |
| **1–14** | **Character** | Assets (+ location/name resolution, recursive tree); Blueprints; Calendar (events, **detail**, attendees); Clones & Implants; Contacts & Labels; Contracts (**headers, items, bids**); Fatigue; Fittings (+ EFT export); Industry Jobs & Mining Ledger; Mail (headers, labels, lists, **bodies**); Notifications (+ **contact notifications**); Planetary Interaction (colonies + **colony detail**); Skills, Queue & Attributes; Wallet (balance, journal, transactions) |
| **15–17** | **Character (cont.)** | Sheet aggregate (corporation history, medals, standings, titles, roles, loyalty points, agents research); Location, Online & Ship; **Intel (interaction graph)** |
| **18–30** | **Corporation** | Assets (+ resolution, tree); Blueprints; Contacts & Labels; Contracts (**headers, items, bids**); Divisions & Facilities; Industry Jobs; Members, Tracking, Limits & **Member Titles**; Projects (list, **detail**, contributors, **per-character contribution**); Roles, Titles & Role History; Starbases (+ **starbase detail**); Structures, **Skyhooks** & Sovereignty Hubs; Wallets & Ledgers (bounties, PI, mining); Moon Extractions & Observers |
| **31–33** | **Corporation (cont.)** | Customs Offices & Container Logs; Medals & Issued Medals; Shareholders, Standings & Alliance History |
| **34–36** | **Market** | Character Orders & History; Corporation Orders & History; Global Market (regional orders, history, types, prices) |
| **37–39** | **Alliance & Sovereignty** | Alliance Information, Members & Contacts; Sovereignty (Campaigns, Systems, Hubs); Killmails (character, corporation, detail) |
| **40–44** | **Utilities** | **Character-scoped Search with anti-discovery controls**; ID & Name Resolution (+ affiliations, structures, stations); Insurance Prices; Character Notes; Standings Tool & Moon Report Parser |
| **45–46** | **Status** | **ESI Service Health (`/meta/status`)**; **Tranquility Server Status (`/status`)** |
| **47–52** | **Admin & Auth** | Third-Party API Tokens (scoping, revocation, access log); ESI SSO Scope Administration (**opaque, dual-grammar**); **Compatibility-Pin Administration (blocked-route board, pin advance)**; User Administration; Security & Audit Log; Sync, Health & Route Catalogue |
| **53–55** | **Squads** | Squad CRUD; **Members & Moderators**; **Squad Roles & Applications** |
| **56–58** | **Alerts & Provisioning** | Access Provisioning Drivers (Discord, TeamSpeak, Mumble) + Webhook Channels (Discord, Slack, SMTP); **54-Type Alert Catalogue across 8 domains, including Wars (6) and Skyhooks (5)**; 9-Locale Internationalization with ESI language resolution |

> **Numbering note:** the matrix enumerates exactly 58 capabilities. Grouped bands list their
> members in order; the band range and the member count must always agree. Per Principle 15, any
> disagreement between a stated count and the enumerated rows is a specification defect, and Gate 4
> checks this explicitly.

---

## Appendix B: Expected Legacy Baseline **[v3.1 — B6]**

These are the **expected** results of the Phase 0 measurement against
`github.com/eveseat/{seat,eveapi,web,services,notifications,api,console}` at HEAD. They are not
themselves authoritative: `docs/BASELINE.md`, produced by that measurement, is what Gate 4
verifies against. Where the two disagree, the disagreement is a specification defect to be
resolved before Gate 4 can pass — neither number may be silently adopted.

| Dimension | Expected count | Method |
| :--- | :--- | :--- |
| ESI call sites | **107** | 106 job classes declaring `$endpoint`, plus `/characters/{character_id}/mail/{mail_id}/` invoked inline in `Jobs/Mail/Mails.php` |
| ESI routes consumed (distinct) | **106** | 105 distinct among the job classes — `/corporations/{corporation_id}/assets/locations` is bound by two jobs — plus the inline mail-body route |
| Health-check calls (retired) | 3 | `/ping`, `/status/`, `/status.json` — the last removed by CCP 24 March 2026 |
| UI controller classes | **72** | `web/src/Http/Controllers/**/*Controller.php` |
| Concrete alert types | **54** | `notifications/src/Notifications/**`, excluding abstract classes and traits |
| Delivery integrations | 3 | `notifications.integrations.php`: discord, slack, mail |
| UI locales | **9** | `af, de, en, fr, ja, ko, ro, ru, zh-CN` |
| ESI scopes | **70** | Distinct scope strings in the 2026-05-19 spec (one grammar); a second grammar appeared 2026-08-04 |
| Third-party API controllers | 9 | `api/src/Http/Controllers/Api/v2/` |

**Known legacy defects not to be reproduced:** ad-hoc per-call compatibility pinning
(`setCompatibilityDate('2025-08-09')` appears four times in `Jobs/Mail/Mails.php`); ESI calls
bypassing the endpoint registry; a health check against a route CCP has deleted.

---

## Appendix C: `/api/v2` → `/api/v1` Migration Mapping

| Legacy controller | HANGAR equivalent | Notes |
| :--- | :--- | :--- |
| `AllianceController` | `/api/v1/alliances/*` | Direct |
| `CharacterController` | `/api/v1/characters/*` | Direct; `_sync` envelope added, stripped by the shim |
| `CorporationController` | `/api/v1/corporations/*` | Direct; `_sync` envelope added, stripped by the shim |
| `KillmailsController` | `/api/v1/characters/{id}/killmails`, `/corporations/{id}/killmails` | Split by owner |
| `RoleController` | `/api/v1/admin/*` (RBAC) | Reshaped — grant model differs; **breaking, no shim** |
| `RoleLookupController` | `/api/v1/admin/users/{id}` | Folded into user administration; **breaking, no shim** |
| `SquadController` | `/api/v1/squads/*` | Expanded (members, moderators, roles) |
| `UserController` | `/api/v1/admin/users`, `/api/v1/me` | Split by scope of access |
| `ApiController` (base) | n/a | Framework-level; no equivalent |

Shim coverage is read-only. Reshaped routes are documented as breaking with no shim, since the
underlying grant model is not translatable; they return a documented breaking-change response with
a migration pointer rather than a partial shim.
