# 04 — Verification & Testing Matrix (Release Gates 1–7)

**Derived from:** [`00_SRS_v3.1.md`](00_SRS_v3.1.md) §7 Phase 20
**Executed in:** Phase 20.8. Each gate's *instrumentation* must land in a phase that **precedes**
the gate-running phase and ships with its own exit criteria and tests — see rule 6.

---

## 0. Rules that apply to every gate

1. **A gate is evidence, not an opinion.** Each gate produces a machine-readable artefact
   committed under `docs/gate-evidence/<version>/`. "We ran it and it looked fine" is a fail.
2. **A gate is run against a release candidate build**, not a development tree.
3. **Environment is recorded**: replica count, CPU, RAM, Postgres version and settings, network
   latency to ESI. Gate 1 in particular is meaningless without the replica count (see §1.4).
4. **A gate that requires a code change to pass is a failed gate**, not a fixed one. This applies
   most sharply to Gate 6, whose entire premise is zero code changes.
5. **Gates are re-run on every minor release.** Gates 4 and 7 are re-run on every patch release
   that touches an endpoint.
6. **Instrumentation precedes the gate that reads it.** A gate's metrics, harnesses and fixtures
   must land in a phase *earlier* than the one that runs the gate, and must ship with their own
   exit criteria and their own tests. Authoring instrumentation in the same phase that runs the
   gate is the same defect as authoring Gate 6's synthetic spec in response to a failure, or
   re-recording Gate 7's corpus to fit the shim: the measurement stops being independent of the
   thing measured.
7. **A metric is declared only by the change that makes it move.** A metric that exists and reads
   zero is indistinguishable from a healthy system — `alert_delivery_total == 0` reads as "a quiet
   installation", not as "the emitter has no caller". Declaring a metric ahead of its
   implementation therefore *hides* the gap it was supposed to expose. This rule is written in
   the blood of defect B25 (see `PRODUCTION_CALLER_AUDIT.md`), where a fully wired alert
   dispatcher drained an outbox that nothing wrote to.

### Instrumentation ownership

**Amended in Phase 20.1 (defect B36).** This table originally assigned Gates 1–3's metrics to
Phases 4, 11 and 14, and the header above forbade adding them in Phase 20. Neither happened: the
Phase 20 audit found that `telemetry.NewRegistry` had no caller, no `/metrics` endpoint was
served, and every metric named below existed only inside doc comments. SRS §0 meanwhile assigned
the metric surface to Phase 20 outright, so the two documents contradicted each other.

Resolved per Principle 15 by naming the *property* rather than a phase number: instrumentation
lands before the gate that reads it (rule 6), and is declared by the change that moves it (rule
7). Phases 4/11/14 remain where this work *should* have landed; the "actual" column records where
it did. A future subsystem's metrics belong in that subsystem's own phase — the split below is
remediation, not a precedent.

| Gate | Metric / artefact | Intended phase | Actual |
| :-- | :-- | :-- | :-- |
| 1 | `esi_ledger_divergence`, `esi_ledger_mode`, `esi_replica_live_count`, `esi_429_total{has_headers}`, `esi_420_total`, `esi_error_limit_remaining` | 4 | **20.2**, with the B28/B29 wiring that makes them move |
| 2 | `app.provisioning_audit.event_at` / `platform_call_completed_at`, `provisioning_revocation_latency_seconds` | 11 | **20.3**, with the B26/B27 wiring |
| 3 | `alert_delivery_total{channel,outcome}`, `alert_dead_letter_depth`, dedupe hash log | 14 | **20.4**, with the B25 wiring |
| 4 | `docs/BASELINE.md` measured counts, capability→endpoint→phase traceability table | 0, 15 | 0, 15 (+ **20.5**) |
| 5 | container image published; compose file with no `build:` key | 0 | 0 (+ **20.7** for Helm and dashboards) |
| 6 | `test/drift/gate6_synthetic_spec.json` **authored and committed in Phase 2**; catalogue ingest report | 2 | 2 — verified present and unmodified |
| 7 | `testdata/legacy-api-v2/**` recorded corpus | 19 | 19 — verified present; coverage extended in **20.6** |
| — | the shared registry and `/metrics` endpoint every gate above scrapes | — | **20.1** |

---

## 1. Gate 1 — ESI Load Stability

> 4-hour, 5000-character test with **zero rate-limit breaches across both governors**, and
> **zero divergence between the local ledger and `X-Ratelimit-Remaining` beyond a one-request
> tolerance**.

### 1.1 Harness

`test/load/gate1_esi.go` drives a full installation against a **recording proxy** that
faithfully reproduces ESI's two governors:

* **Governor 1 simulation** must be a genuine floating window, not a refill bucket — the proxy
  releases each request's cost exactly one `window_size` after that individual request. A proxy
  implementing a refill bucket would let a refill-based client pass, which defeats the gate.
* **Governor 2 simulation**: 100 non-2XX/3XX per fixed 60-second window, installation-wide,
  returning 420 on every route when exceeded.
* The proxy injects the documented adversarial conditions on a schedule (§1.3).

5000 characters, realistic route mix derived from `app.sync_subscription` at steady state, run
for 4 hours wall-clock with no restarts.

### 1.2 Pass conditions

| # | Condition | Measurement |
| :-- | :-- | :-- |
| 1.1 | Zero Governor 1 breaches | proxy records zero requests admitted with `available < 0` |
| 1.2 | Zero Governor 2 breaches | `esi_420_total == 0` |
| 1.3 | Ledger divergence ≤ 1 request | `max(esi_ledger_divergence)` over the run ≤ 1, per group |
| 1.4 | Proactive error-limit pause worked | at least one pause fired at the configured threshold, and no 420 followed |
| 1.5 | Failure stayed scoped | an induced 403 on one entity did not reduce throughput on siblings by more than noise |
| 1.6 | No stall | throughput never drops to zero for more than one `ttl_floor` |
| 1.7 | Aggregate consumption respected at N>1 | with 3 replicas sharing a bucket, the proxy admitted no request that took aggregate consumption above `max_tokens` |
| 1.8 | Mode selection was correct throughout | `esi_ledger_mode` was `clustered` for the whole N=3 run and `solo` for the whole N=1 run; no unexpected flapping |

### 1.3 Adversarial conditions injected during the run

| Condition | Expected behaviour |
| :-- | :-- |
| Burst of 4XX on one group | predictive reservation prevents overdraw; no breach |
| **429 with no rate-limit headers** | no charge, snooze the affected subscription only, `esi_429_headerless_total` increments, no stall |
| 429 with `Retry-After` | subscription snoozed for *exactly* that duration; siblings unaffected |
| Server reports lower `X-Ratelimit-Remaining` than local | local converges downward within one request |
| Server reports higher | local converges upward, never above `max_tokens` |
| Sustained 5XX on one route | circuit breaker opens after 10; half-open probe at route TTL |
| 5 consecutive 403s on one entity | entity breaker opens; acting character re-elected; route stays live for other entities |
| Error budget driven to the threshold | proactive pause **before** any 420; resume only at the higher threshold, with no oscillation |
| A replica killed mid-run (N=3) | mode holds `clustered`; the dead replica's reservations expire and are reclaimed; no breach |
| A replica restarted mid-run (N=1 → 2 → 1) | mode follows the registry; both flushes preserve the live-cost sum |

### 1.4 The gate runs at two replica counts

SRS v3.0 made ledger state per-replica and relied on header reconciliation to correct drift. That
does not work: reconciliation is reactive, so N replicas each holding what they believe is the
full `max_tokens` spend N× the budget on a synchronised burst before any correction lands. A Gate
1 pass at N=1 said nothing about N=3.

**SRS v3.1 §4.1.3 corrects this** — the ledger is cluster-shared, with a `solo` in-process fast
path selected automatically when exactly one replica is live. A correct implementation therefore
passes at any replica count.

The gate still runs at **N=1 and N=3**, as two separately recorded results:

* **N=1** exercises the `solo` path and its performance target.
* **N=3** exercises the `clustered` path, its aggregate-budget correctness (condition 1.7), and
  the acquire-latency target under real contention.

Both must pass for release. Running only one is not sufficient even though the design intends
both to pass — the second run is what demonstrates the mode selection and the shared-ledger
transaction actually behave, rather than that they were written.

**Mid-run mode transition.** Once per run, kill one replica and restart it. `esi_ledger_mode` must
follow the registry, the flush must lose no entries, and conditions 1.1–1.3 must continue to hold
across the transition.

### 1.5 Evidence artefact

```
docs/gate-evidence/<version>/gate1/
  n1/  and  n3/
    environment.json        replica count, mode observed, CPU/RAM, PG settings, proxy version
    divergence.csv          per-group, per-minute max divergence
    breaches.json           must be empty
    aggregate-consumption.csv   N=3 only: proxy-side view of total consumption per bucket
    adversarial-log.jsonl   each injected condition and the observed response
    transition-log.jsonl    the mid-run replica kill/restart and its effect
    metrics.prom            full Prometheus scrape at end of run
```

---

## 2. Gate 2 — Revocation SLO

> 5000 identities across 3 platforms meeting **< 60 s revocation p99**.

### 2.1 Harness

`test/load/gate2_revocation.go`. Three stub platforms (Discord, TeamSpeak, Mumble) reproducing
each platform's real rate limits — a stub that answers instantly proves nothing, because the
Discord bucket wait is a material part of the budget.

Steady-state load: a full background reconciliation running concurrently. The urgent path must
be measured **while the bulk path is saturated**, since queue starvation is the realistic
failure mode.

### 2.2 Measurement definition — precise

p99 of `platform_call_completed_at − event_at` from `app.provisioning_audit`, where `event_at`
is the timestamp of the **originating entitlement-reducing event**, not job start and not job
claim. Queue wait is inside the measurement because queue wait is what fails under load.

### 2.3 Trigger matrix — every reducing event must be exercised

| Event | Source | Must enqueue urgent |
| :-- | :-- | :-- |
| Token invalidated (`invalid_grant`) | Phase 5 | ✓ |
| `owner` hash change (character transferred) | Phase 5 | ✓ |
| Scope set reduced | Phase 5 | ✓ |
| RBAC role revoked | Phase 10 | ✓ |
| Squad membership removed | Phase 11 | ✓ |
| Corporation / alliance departure | Phase 8 | ✓ |
| Entitlement rule deleted or narrowed | Phase 11 | ✓ (bulk urgent) |
| Admin platform lockdown | Phase 11 | ✓ |
| Strict Mode trip on any alt | Phase 11 | ✓ |

### 2.4 Pass conditions

| # | Condition |
| :-- | :-- |
| 2.1 | p99 < 60 s across 5000 identities × 3 platforms |
| 2.2 | Every row in the trigger matrix produces an urgent job in the mutating transaction |
| 2.3 | Zero revocations lost when a platform is down: they retry and remain on the exposure board with their true age |
| 2.4 | `provision-urgent` is never starved by `provision-bulk` — p99 measured with bulk saturated |
| 2.5 | Rolling back the mutating transaction also rolls back the job (transactional atomicity) |

---

## 3. Gate 3 — Alert Delivery Integrity

> 4-hour alert load test **drops zero alerts**.

### 3.1 Definition of "dropped"

An alert is dropped if it was generated and neither **delivered** nor **dead-lettered**.
Dead-lettering is not a drop — it is a visible, actionable outcome. Silent loss is the failure.

Accounting: `generated == delivered + coalesced_into + dead_lettered + suppressed_by_dedupe`.
The identity must hold exactly at end of run.

### 3.2 Harness

`test/load/gate3_alerts.go` generates across all eight domains and all three categories (ESI
notifications, domain events, threshold alerts) with channel stubs that inject failures.

### 3.3 Pass conditions

| # | Condition |
| :-- | :-- |
| 3.1 | The accounting identity in §3.1 holds exactly |
| 3.2 | Unrecognised CCP notification types deliver via the **generic fallback renderer** and appear on the unknown-types board; **the queue never halts** |
| 3.3 | Unparseable notification YAML imports as JSONB and renders generically |
| 3.4 | 40 coalesced events render as **one** message, within each channel's size limit, truncated with "and N more" if needed |
| 3.5 | `char-notification` polling at 600 s holds the **5-token reserve** intact for the whole run |
| 3.6 | Dedupe hashes are stable across a process restart mid-run |
| 3.7 | Channel outages produce retries then dead-letters, never queue blockage |
| 3.8 | **Build-time**: every threshold alert's source route is present in the sync set |

Condition 3.8 is checked at build, not at load — but it is listed here because a threshold alert
with no data source silently generates zero alerts, which a drop test cannot detect.

---

## 4. Gate 4 — Feature Parity

> All **58 capabilities** verified against the **measured** baseline of 106 distinct ESI routes,
> 72 UI controllers, 54 alert types and 9 locales.

### 4.1 Measured, not asserted (Principle 15)

Gate 4 compares against `docs/BASELINE.md`, produced in Phase 0 by measuring the five legacy
repositories at HEAD — **not** against the SRS's stated numbers. If a measured count disagrees
with the SRS, that disagreement is a specification defect to be raised and resolved before the
gate can pass. Adopting either number silently is a Principle 15 violation.

### 4.2 Traceability artefact

`docs/gate-evidence/<version>/gate4/traceability.csv`, one row per capability:

```
capability_id, capability_name, upstream_esi_routes, legacy_controllers,
alert_types, hangar_endpoints, delivering_phase, verification_test, status
```

Per SRS §11, a capability with no phase — or a phase with no exit criterion — is a specification
defect and blocks the gate.

### 4.3 Pass conditions

| # | Condition |
| :-- | :-- |
| 4.1 | 58 capability rows, all `status = verified` |
| 4.2 | Every one of the 106 measured distinct ESI routes maps to at least one `app.sync_subscription` route, or is explicitly recorded as deliberately unmapped with a reason |
| 4.3 | All 72 legacy UI controllers map to a HANGAR endpoint or screen |
| 4.4 | All 54 alert types seeded, with per-domain counts matching (Structures **23** incl. 5 Skyhook, Characters 7, platform 7, Wars 6, Corporations 5, Sovereignty 4, Contracts 1, Alliances 1). Structures corrected from 22 in Phase 14.1 against a direct measurement of the upstream — see docs/BASELINE.md §4a |
| 4.5 | All 9 UI locales present and each resolving to a valid ESI `Accept-Language` |
| 4.6 | The three retired health-check routes (`/ping`, `/status/`, `/status.json`) are **absent** from the catalogue; `/meta/status` and `/status` are present as two distinct capabilities |
| 4.7 | The two declared scope reductions (in-process plugins, versioned `/api/v2`) are recorded as intentional, not counted as gaps |
| 4.8 | The capability matrix's band ranges agree with their enumerated member counts (the SRS's own numbering note) |

### 4.4 Known legacy defects that must **not** be reproduced

Verified as absent:

* Ad-hoc per-call compatibility pinning (legacy calls `setCompatibilityDate('2025-08-09')` four
  times in `Jobs/Mail/`). HANGAR sends the app pin from one place, always.
* ESI calls bypassing the endpoint registry. Every HANGAR request resolves through
  `app.esi_route`; the mail-body call in particular must not be inline.
* A health check against a route CCP has deleted.

---

## 5. Gate 5 — Deployment Usability

> `docker-compose.yml` deploys from a blank environment in **3 commands**, with **no source
> compilation**.

### 5.1 Procedure

Executed on a freshly provisioned host with only Docker installed. No repository clone, no Go
toolchain, no Node.

```bash
curl -fsSLO https://raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml
```
```bash
curl -fsSL https://raw.githubusercontent.com/hangar-project/hangar/main/install.sh | sh
```
```bash
docker compose up -d
```

`install.sh` generates `.env`, creates a random database password and the two 32-byte secrets,
and prompts for exactly two values: the EVE SSO Client ID and Secret. `install.bat` is the
Windows equivalent.

### 5.2 Pass conditions

| # | Condition |
| :-- | :-- |
| 5.1 | Exactly three commands, no editor step |
| 5.2 | **No compilation** — the `hangar` service has no `build:` key and the image is pulled from the public registry |
| 5.3 | The administrator supplies only the SSO Client ID and Secret |
| 5.4 | Migrations run automatically and the stack is healthy within 5 minutes |
| 5.5 | The default profile is exactly `postgres` + `hangar` + the one-shot `migrate` — **Redis absent** (Principle 7) |
| 5.6 | The SPA is served from the binary; no separate web server, no separate web root |
| 5.7 | Re-running `docker compose up -d` after a version bump migrates forward without data loss |
| 5.8 | Manual deployment (§9.2) verified separately: a static binary boots from a systemd unit with a `.env` and an external PostgreSQL 18 |

### 5.3 Failure modes to test explicitly

* Blank `.env` → the binary aborts with a **named, actionable** error, not a stack trace.
* Wrong `HANGAR_PUBLIC_URL` → the SSO callback mismatch is reported as a configuration error
  with the expected value shown, not as an opaque OAuth failure.
* Postgres not yet ready → the `migrate` service waits on the healthcheck rather than failing.

---

## 6. Gate 6 — Spec-Drift Resilience

> A synthetic spec containing (a) a route with a compatibility date newer than the pin, (b) a
> UUID path identifier, (c) a novel scope grammar, and (d) an unrecognised `x-cache-mode` value
> is ingested with **zero code changes**, correctly gating (a), typing (b), storing (c), and
> defaulting (d) to `ttl-based`.

This is the gate that decides whether HANGAR actually implements "the spec is the schedule", or
merely claims to. It is the highest-value gate in the set.

### 6.1 The synthetic spec

`test/drift/gate6_synthetic_spec.json` is a valid OpenAPI 3.1 document built from the real
snapshot plus four injected operations. **It must be authored before the gate is run and
committed unchanged**; authoring it in response to a failure invalidates the gate.

| Condition | Injected operation | Expected outcome |
| :-- | :-- | :-- |
| **(a) Post-pin route** | `GET /synthetic/future-route` with `x-compatibility-date: <app pin + 30 days>` | row created; `blocked_by_pin = true`; excluded from the scheduling query; appears on `/admin/esi/catalogue/blocked` with a diff |
| **(b) UUID path identifier** | `GET /synthetic/widgets/{widget_id}` where `widget_id` is `type: string, format: uuid` | `identifier_types` records `uuid`; the projected column is `uuid`, **not** `bigint` and **not** `text`; a `bigint` value is rejected; `hangar admin verify-identifier-types` passes |
| **(c) Novel scope grammar** | operation `security` requires `esi::synthetic~widget/read@v3` — deliberately matching **neither** live grammar | stored verbatim as a `text` primary key in `app.esi_scope`; appears on `/admin/scopes/unknown`; **no regex, no split, no prefix check rejects it** |
| **(d) Unrecognised cache mode** | `x-cache-mode: quantum-entangled` | value recorded in `app.open_vocabulary`; scheduling behaves as `ttl-based`; the route is **not** rejected |

### 6.2 The "zero code changes" condition — how it is proven

1. Tag the release candidate commit.
2. Run the catalogue ingest against the synthetic spec.
3. Assert the four outcomes above.
4. `git status --porcelain` must be **empty** and `git rev-parse HEAD` must equal the tag.

Any source change — including adding a case to a switch, extending a regex, or adding an enum
value — is a **Gate 6 failure**. The correct response is to redesign the ingest to be data-driven,
not to make the change and re-run.

### 6.3 Additional drift conditions worth injecting

Not required by the SRS, but cheap to add and they exercise the same property:

| Condition | Expected |
| :-- | :-- |
| A route whose `upstream_path` is irregularly singular | stored verbatim |
| An operation with no `x-rate-limit` | Governor 2 only |
| An operation with `x-cache-age: 0` and `x-cache-mode: event-based` | scheduled at `ttl_floor`, never 0 |
| A previously-present route removed from the spec | marked `retired_at`, not deleted; its subscriptions survive |
| An identifier that changes type between two ingests (`bigint` → `uuid`) | build fails loudly with a named error, rather than coercing |
| A new `x-required-roles` value never seen before | stored as `text`, surfaced, not rejected |

The identifier-type-change case is worth special attention: CCP has explicitly warned that code
assuming numeric IDs needs a different data type for UUID routes, so a silent coercion is the
exact failure mode Principle 13 exists to prevent.

---

## 7. Gate 7 — Third-Party API Migration

> The `/api/v2` shim returns **byte-compatible** payloads against recorded legacy responses for
> every migrated read route.

### 7.1 The corpus

`testdata/legacy-api-v2/` holds responses recorded from a running legacy SeAT instance across the
nine controllers: Alliance, Character, Corporation, Killmails, Role, RoleLookup, Squad, User, and
the base `ApiController`. Each fixture records the request (path, query, headers) and the exact
response bytes.

Coverage requirement: every **read** route of every shimmed controller. Write routes are not
shimmed and are recorded as such.

### 7.2 "Byte-compatible" — the exact standard

Byte-compatible means the response body is **byte-identical** after normalising only:

* the HTTP `Date` header and other transport-level headers,
* values that are inherently time-dependent (timestamps of the recording itself),
* values that are inherently instance-dependent (IDs seeded differently).

Field **order**, JSON whitespace and formatting, numeric formatting, and null-vs-absent
distinctions are **in scope** and must match. A consumer parsing with a strict schema will break
on any of them. Comparing parsed objects instead of bytes is not sufficient and does not satisfy
this gate.

### 7.3 Pass conditions

| # | Condition |
| :-- | :-- |
| 7.1 | Byte-identical (per §7.2) for every migrated read route across all nine controllers |
| 7.2 | Every shim response carries `Deprecation: true` |
| 7.3 | Every shim response carries an RFC 8594 `Sunset` header set to the announced removal date |
| 7.4 | The `_sync` envelope is **stripped** — legacy consumers never see it |
| 7.5 | Laravel-style pagination (`current_page`, `last_page`, `per_page`, `total`) is synthesised correctly from keyset results; the `total` strategy is documented |
| 7.6 | Money fields are emitted as JSON **numbers** as legacy did, and legacy's own at-rest imprecision (§7.4) is documented with a worked example |
| 7.7 | `RoleController` and `RoleLookupController` return a documented breaking-change response (`410`) with a migration pointer — **not** a broken or partial shim |
| 7.8 | Write routes return a clear "not shimmed" response, not a 404 |
| 7.9 | Appendix C mapping is complete **in both places it is written** — `docs/APPENDIX_C_MIGRATION.md` and SRS Appendix C — with every legacy controller carrying an entry and routes with no equivalent flagged. This includes `UserController` and `SquadController` being marked un-shimmable (`501`) on their identity fields, corrected in Phase 20.1; a correction present in the migration guide but absent from the SRS still fails this condition, because the SRS is the document Gate 4 traces capabilities against |

### 7.4 The money-precision caveat — **corrected in Phase 20.1 against a measurement**

HANGAR stores money as `NUMERIC(30,2)` and emits JSON strings (Principle 9). Legacy emitted JSON
numbers. The shim converts back to a number to be byte-compatible.

**What this paragraph used to say, and why it was wrong.** It said the conversion "reintroduces
IEEE-754 imprecision for large ISK values", and SRS §10 said the same. Measured against legacy's
own database while recording the Gate 7 corpus, it does not. `character_wallet_journals.amount`
is a MySQL **`DOUBLE`**, not a decimal: `9007199254740993.01` ISK is already `9007199254741000`
*at rest*, before anything is serialised. The ~7 ISK is legacy's own loss, and it happens whether
or not HANGAR exists.

So the shim **reproduces** legacy's precision rather than degrading anything. That distinction is
not pedantry — it decides where an integrator looks when the numbers disagree. Told the shim
introduces error, they will diff the shim; told the value was never exact, they will read the
column type and stop.

What remains true: the shim must emit numbers, the migration guide must carry a worked example
(`testdata/legacy-api-v2/` pins one), and emitting strings from the shim would break Gate 7 and
every existing consumer. HANGAR's own `/api/v1` is exact; the imprecision is a property of the
legacy surface being reproduced, and it ends when the migration does.

### 7.5 Sunset policy verification

| Requirement | Check |
| :-- | :-- |
| Shim ships in v1.0 | present in the v1.0 release artefact |
| Removed no earlier than two minor versions later | recorded in `docs/RELEASE_NOTES.md` |
| Removal announced at least one minor version in advance | release-note entry exists before removal |
| `Sunset` header matches the announced date | automated check against the release notes |

---

## 8. Gate summary sheet

| Gate | Name | Duration | Blocking artefact | Owner phase for instrumentation |
| :-- | :-- | :-- | :-- | :-- |
| 1 | ESI Load Stability | 4 h at N=1 **and** 4 h at N=3 | `divergence.csv`, `breaches.json`, `aggregate-consumption.csv` | 4 |
| 2 | Revocation SLO | ~1 h | p99 report from `provisioning_audit` | 11 |
| 3 | Alert Delivery Integrity | 4 h | accounting identity report | 14 |
| 4 | Feature Parity | static | `traceability.csv` vs `BASELINE.md` | 0, 15 |
| 5 | Deployment Usability | ~15 min | recorded 3-command transcript | 0 |
| 6 | Spec-Drift Resilience | ~10 min | ingest report + clean `git status` | 2 |
| 7 | Third-Party Migration | ~30 min | byte-diff report over the corpus | 19 |

**Release blocks on all seven.** Gates 1 and 6 remain the two most likely to fail on a first
attempt: Gate 1 because shared-ledger transaction correctness under real contention is easy to get
subtly wrong, and Gate 6 because "the spec is the schedule" is easy to claim and hard to actually
implement.

Schedule their harnesses early. Gate 6's synthetic spec is a **Phase 2 deliverable** by SRS v3.1
§7 — a fixture authored in response to a failure does not test what it claims to. Gate 1's
recording proxy should exist by the end of Phase 4 so the cluster-correctness assertions can run
as an integration test long before the 4-hour run.
