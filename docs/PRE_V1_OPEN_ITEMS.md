# Pre-v1.0 open items — audit at `c0b35ac`

> **Status after Phase 21.** B-2 and B-3 are closed in code (see below for what each turned out to
> be, precisely). B-1 is closed by building a runner per gate and running them: the evidence is in
> `docs/gate-evidence/v1.0.0-rc1/`, one directory per gate, each with a `SUMMARY.md` stating
> pass/fail and the measurement. B-4 remains open and is the operator's — it cannot be closed from
> inside the codebase.
>
> Phase 21 also found one thing this audit did not: **the stock `docker-compose.yml` runs `serve`
> only, and `serve` registers no River workers**, so on a default installation the planner enqueues
> sync jobs that nothing consumes. See §5.

**Question asked:** are Phases 0–20.11 actually complete, do all seven gates pass, and what
blocks a v1.0 release candidate?

**Method.** Every claim below was re-derived at this commit, not carried forward from a phase
report. Gate evidence was listed from `docs/gate-evidence/`, deferrals from the two reachability
allowlists, `/api/v2` blocker reasons from eveseat's source at the commits
`testdata/legacy-api-v2/README.md` pins, and live counts from the running installation.

---

## 1. What is genuinely finished

| Area | Evidence |
| :-- | :-- |
| Gate 4 — Feature Parity | `make gate4-evidence` exits 0: 58 capability rows, 58 verified, 0 unreachable, 0 deferred, 0 false test citations, 0 false endpoint citations |
| Gate 6 — Spec-Drift Resilience (conditions) | All four SRS conditions asserted: `TestGate6PostPinRouteIsBlocked`, `TestGate6UUIDPathIdentifier`, `TestGate6NovelScopeGrammar`, `TestUnknownCacheModeDefaultsToTtlBased`, plus `TestIdentifierTypeChangeFailsLoudly` and `TestRetiredRouteMarkedNotDeleted`. The synthetic spec was authored in Phase 2 and is committed unchanged |
| Full regression | `go build`, `go vet` (both tag sets), `gofmt -l` empty, `go test -race -shuffle=on ./...`, `go test -race -tags=integration -timeout=40m ./...`, `golangci-lint` 0 issues, `go tool sqlc vet`, `make verify-generated`, `make ci-strict` exit 0 with zero skips, both reachability guards |
| Code hygiene | Zero `TODO`/`FIXME`/`HACK` in non-generated, non-test Go. All six `t.Skip` calls are `-short`- or race-guarded with stated reasons |
| Schema integrity | `hangar migrate up` now verifies that the objects the migrations create still exist, and fails if not (Phase 20.11) |

---

## 2. Blockers for v1.0 release candidacy

### B-1 — Five of seven gates have never been run *(blocking)*

`docs/gate-evidence/` contains **only** `gate4`, for phases 20.6–20.10. Gates 1, 2, 3, 5 and 6
have produced **no blocking artefact at any point in the project's history**, and
`04_RELEASE_GATES.md` §8 states "Release blocks on all seven."

| Gate | Blocking artefact | Harness | Run? |
| :-- | :-- | :-- | :-- |
| 1 ESI Load Stability | `divergence.csv`, `breaches.json`, `aggregate-consumption.csv` | **exists** (`test/load/gate1_esi.go`, all seven artefacts written; `TestHarnessRunProducesEveryEvidenceArtefact` proves it) | **No** |
| 2 Revocation SLO | p99 report from `provisioning_audit` | exists (`test/load/gate2_*`) | **No** |
| 3 Alert Delivery Integrity | accounting identity report | exists (`test/load/gate3_*`) | **No** |
| 5 Deployment Usability | recorded 3-command transcript | n/a — manual procedure | **No** |
| 6 Spec-Drift Resilience | ingest report + clean `git status` at the tagged commit | conditions tested; §6.2 tag-and-prove procedure not performed | **No** |

The harnesses are driven from tests at 150–200 ms durations. A real Gate 1 run is
`Duration: 4 * time.Hour` at **N=1 and N=3** — and there is **no `make` target or command that
invokes it**. It is reachable only from a Go test.

**Correction needed:** a runner for each gate (a `make gate1` / `tools/` binary rather than a
test), an N=3 deployment for Gate 1, and ~9 hours of wall clock (4 h + 4 h + 1 h) plus a clean
host for Gate 5.

### B-2 — No housekeeping runs anywhere; `app.session` grows without bound *(blocking)*

Four generated queries exist for retention and **none has a production caller**, confirmed by
grep and by the guard's own allowlist, which files them under "HOUSEKEEPING THAT NEVER RUNS —
rows accumulate forever":

`DeleteExpiredSessions`, `DeleteExpiredEsiCacheEntries`, `DeleteStaleReplicas`,
`CountEsiCacheEntries`.

There is no janitor loop, cron, or River periodic job. Measured on the live installation:
**19 of 22 `app.session` rows are expired** and will never be removed.

Stated precisely, so it is neither over- nor under-sold:

* **Not an authentication hole.** `GetSession` filters `expires_at > now()`
  (`db/queries/user.sql:55`), so an expired row cannot authenticate.
* **It is a data-retention defect.** `app.session` holds `ip_address`, `user_agent` and
  `pkce_verifier`, retained indefinitely with no deletion path.
* `app.esi_cache_entry` is **bounded** in practice (380 rows, 0 expired) because entries are
  overwritten per cache key — the allowlist's "accumulate forever" overstates that one.
* `app.esi_replica` accumulates a row per force-killed process. Harmless to mode selection
  (`CountLiveReplicas` filters on the heartbeat window) but never cleaned.

**Correction needed:** a periodic housekeeping job wiring the three delete queries, with a
documented retention window for sessions.

#### Closed in Phase 21

`internal/housekeeping.Sweeper`, run from **`serve`** on a timer
(`HANGAR_HOUSEKEEPING_INTERVAL`, 1h), with the windows documented in `.env.example` and in the
package header. The three queries left `generated_allowlist.txt` in the same commit.

**The owner is `serve`, not `work`, and that is the whole point.** `work` is the natural-looking
home — it is the background-job role. It is also the process the **stock `docker-compose.yml` does
not run**: its only `hangar` service runs `serve`. Wiring retention into `work` would have shipped
a janitor that never runs on a default installation, which is precisely how §4.9's webhook outbox
shipped write-only, and `serve.go` already carries that lesson in a comment two lines above where
the sweeper is now started.

One design point worth keeping: `DeleteStaleReplicas` takes a **retention window, not the liveness
threshold**, and the sweeper refuses to run it below a 10-minute floor. `app.esi_replica` is what
`CountLiveReplicas` reads to choose solo or clustered mode, so deleting the registration of a
replica whose heartbeat was merely *late* would make each survivor believe it is alone and spend
the full ESI bucket — a Governor 1 breach (Gate 1.1) manufactured by housekeeping.

The allowlist's claim that `app.esi_cache_entry` accumulates forever was **wrong** and is
corrected rather than inherited: `UpsertEsiCacheEntry` is keyed on `cache_key` and overwrites, so
the table is bounded by the number of distinct cache keys in flight.

### B-3 — Gate 5's documented install URL 404s *(blocking, trivial)*

`04_RELEASE_GATES.md` §5.1 command 2 of 3 is:

```
curl -fsSL https://raw.githubusercontent.com/hangar-project/hangar/main/install.sh | sh
```

The installers exist at **`deploy/install.sh`** and **`deploy/install.bat`**, so that URL is a
404 and the documented three-command deployment cannot succeed on a fresh host.

**Correction needed — a decision, not just an edit.** Either move the two installers to the
repository root (preserving the short, memorable URL the gate wants) or amend the gate procedure
to `main/deploy/install.sh`. Not resolved here because the choice is about the product's install
UX, not about correctness.

#### Decided in Phase 21 — the installers stay in `deploy/`, the procedure is amended

What settled it: **`deploy/install.sh`'s own header already documents the URL as
`main/deploy/install.sh`.** The file that would have had to move is the one place that had the
path right all along, so this was a stale reference in two other documents (§5.1 and
`docker-compose.yml`'s header comment), not a misplaced installer. The layout is also written down
as `deploy/` in `01_ARCHITECTURE.md` and `03_IMPLEMENTATION_ROADMAP.md`: moving the files would
make three documents wrong to make one right, to save seven characters in a URL that is
copy-pasted once. Both stale references are corrected.

### B-4 — Two operator actions outstanding, unchanged since Phase 20.8 *(blocking, external)*

Re-measured at this commit: **52** GET scopes derived (`go run ./tools/scopedump`), **50** in
`app.character_token_scope`. The diff is exactly `esi-universe.read_structures.v1` and
`esi-alliances.read_contacts.v1`, and neither route has a subscription row — the reconciler's
scope gate working correctly.

* **Re-authorization.** Capability #41's structure half and #37's alliance contacts have never run
  against real ESI. Requires revoking HANGAR at EVE account management → Third Party
  Applications, then a fresh login. Cannot be done from here (Cloudflare CAPTCHA, and it is the
  operator's account).
* **Alliance membership.** `app.alliance` holds 0 rows, so `ReconcileAllianceSubscriptions`
  produces nothing and `AllianceWorker` has never been dispatched against real ESI. Only the
  seeded-integration claim (`TestSyncAllianceSubscriptionsAreOrdered`) can be made.

Note, measured in 20.9 and still true: when the grant lands the structure fan-out will resolve
**nothing and not even 403** — `ListCharacterStructureIDs` unions four sources and all four hold
zero rows on this installation.

---

## 3. Non-blocking open items

### N-1 — Gate 7 is 16 of 34, and that is a product decision, not a defect

12 pending, 4 unshimmable, 2 breaking. **Fourteen of the eighteen unserved routes are
permanently unservable** with reasons re-derived against legacy source in this audit:

| Reason | Routes | Audit verdict |
| :-- | :-- | :-- |
| `reasonSurrogateID` | 3 wallet | **Confirmed.** `CharacterWalletTransaction`/`CorporationWalletTransaction` declare `$primaryKey='id'`, `CorporationWalletJournal` `'internal_id'`, all `$incrementing=true` and none hidden — the auto-increment key is on the wire |
| `reasonMySQLDoubleRounding` | character.wallet-journal | Confirmed in 20.7 against PHP 8.2.33 |
| `reasonContractDoublePrice` | 2 contracts | Confirmed in 20.10; `contract_details.price` is a MySQL `double` |
| `reasonAssetMapColumns` | 2 assets | Confirmed in 20.10; `map_id`/`map_name` are real persisted columns and row two of the recording carries real values |
| `reasonKillmailHash` | 3 killmails | **Confirmed.** `attacker_hash` is the leading key of every attacker object in `killmails.detail` |
| `reasonIdentitySpace` | 4 users/squads | **Confirmed.** All four recordings lead with `"id": 1`, a MySQL auto-increment |
| `reasonGrantModel` | 2 roles (breaking) | Not re-derived; never recorded, breaking by construction |
| `reasonCharacterSheetFields` | character.sheet | **Reason holds; its evidence was mis-stated — corrected in this commit.** See N-2 |

### N-2 — `character.sheet`'s reason cited evidence that does not exist *(corrected here)*

The reason said `user_id` "has no honest value". The recording actually shows
`"user_id": null` — because `CharacterSheetResource` emits `$this->user->id` and
`CharacterInfo::user()` is a `hasOneThrough` over `refresh_tokens`, which `fixtures.php` never
seeds.

So a shim emitting a constant `null` would be **byte-identical to the recording and wrong on
every real installation**. The route correctly stays pending; the reason now says so accurately.
This is the `corporation.structures`/`services` trap in a new place, and it is the third time a
Gate 7 reason has been true-but-mis-evidenced (B55, B57, this).

**Correction available:** seed one `refresh_tokens` row in `fixtures.php` and re-record. That
pins the populated case permanently, in whichever direction it falls.

### N-3 — Five served routes still rest on single-row recordings

`character.{industry, jump-clones, market-orders, skills, skill-queue}` and
`corporation.{industry, market-orders, member-tracking}` are byte-verified against recordings
holding one row. Field names, order, types and formatting are pinned; **their own multi-row
ordering is not.**

The ordering *rule* is no longer an inference — the 20.10 re-recording inserted two structures in
descending id order and two services non-alphabetically, and legacy returned both ascending by
key. Adding a second row to any of these recordings is cheap and would close the gap.

### N-4 — 50 generated queries and 36 symbols have no production caller

Both are policed by guards that fail if an entry gains a caller, so the registers are accurate.
The groups that represent **absent product surface** rather than test doubles:

* Subscription management — an operator cannot snooze, disable or opt a subscription out of
  caching except by SQL (`SetSyncSubscriptionEnabled`, `SetSyncNoCacheOptIn`, `ListRecentSyncRuns`)
* Alert routing/channel CRUD and the unknown-type boards (8 queries)
* Platform and entitlement configuration (5 queries) — no surface at all
* Open-vocabulary boards (4 queries) — Gate 6 surfaces these; no reader is wired
* Webhook dead-letter board and outbox backlog gauge (`events.DeadLetterBoard`, `events.PendingCount`)

None is named by a gate. Each is a real capability gap for an operator and should be a
scoping decision before v1.0, not a surprise after it.

### N-5 — Latent cache-key collision, recorded and not fixed

`esi.Client.cacheKey` hashes the templated upstream path and never `PathParams`, so every item of
a detail fan-out shares one cache key. Not live: the cache is read only on a 304 the caller
conditioned, and no fan-out sends validators. **It becomes real the moment anyone adds per-item
ETags to a fan-out.**

### N-6 — Schema integrity check covers tables only

`db.MissingTables` (Phase 20.11) verifies tables. A dropped index, constraint, column or
partition would still pass. Named for what it checks; extending it is a larger parse.

### N-7 — No SDE imported on the development installation

`hangar admin import-sde` resolves it. Not a defect — EFT exports render `[<type_id>]`
placeholders and the skyhook/sovereignty-hub backfills leave columns NULL until it runs.

---

## 5. Found in Phase 21 — the default deployment runs no workers *(blocking)*

Not in the audit above, found while building Gate 1's runner (which had to answer "which process
actually calls ESI?"), and re-derived three ways:

| Claim | Evidence |
| :-- | :-- |
| The stock stack runs one HANGAR process, `serve` | `docker-compose.yml`'s only `hangar` service is `command: ["serve"]`; the sole other service is the one-shot `migrate` |
| `serve` registers **no** River workers | `river.AddWorker` appears in exactly one non-test file: `cmd/hangar/work.go`. `serve` builds a River client that is insert-only and never `Start()`ed |
| The planner enqueues work that therefore has no consumer | `internal/sync/planner/claim.go` inserts `sync_route` jobs; the only registered Worker for that kind is `work.go`'s `DispatchWorker` |

So on a default installation the planner claims due subscriptions every 5 s, enqueues River jobs,
and **nothing ever works them**. The same holds for `provision_urgent` and `provision_bulk`.

**This contradicts the architecture's own decision**, which is not ambiguous —
`01_ARCHITECTURE.md` §2: *"[DECISION] Single-process default. Gate 5 forbids operational
ceremony. `serve` does everything; `work`/`schedule` exist for administrators who have outgrown
one box."* `serve`'s own cobra `Short` string says it runs an "in-process worker pool". It does
not.

It is the B20/B25 defect class again, in the largest place it can occur: a documented capability
with no implementation in the process that is supposed to have it, invisible because every test
constructs the worker it needs. It is also invisible to Gate 5 as §5.2 writes it — the stack comes
up healthy, migrations run, the SPA serves, and nothing syncs.

**Not fixed in Phase 21, deliberately.** The fix is to register `work.go`'s three workers and its
queue budgets in `serve`, and that changes the behaviour of the process every gate run measures —
so doing it after tagging the release candidate would invalidate the gate evidence this phase
exists to produce, and doing it before would land a significant untested change inside a phase
whose job was to measure. Release-gate rule 6 (instrumentation, and by the same argument the
subject of the measurement, lands in a phase earlier than the run) points the same way. It needs
its own phase, its own exit criteria and its own tests.

---

## 4. Verdict

**Not clear for v1.0 release candidacy.** Four blockers:

1. **B-1** five of seven gates never run, and Gate 1 has no way to run it — the largest item by far
2. **B-2** no retention job; `app.session` accumulates personal data indefinitely
3. **B-3** the documented install command 404s
4. **B-4** two operator actions, external to the codebase

B-3 is minutes of work once the placement decision is made. B-2 is a small, well-understood job.
B-4 is the operator's. **B-1 is the real gate to v1.0**: roughly nine hours of measured runs, an
N=3 deployment, a clean host, and runners that do not exist yet.
