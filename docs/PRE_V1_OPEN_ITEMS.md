# Pre-v1.0 open items — audit at `c0b35ac`

> **Status after Phase 21.** All seven gates have now been run, at `v1.0.0-rc1`. Evidence is in
> `docs/gate-evidence/v1.0.0-rc1/`, one directory per gate, each with a `SUMMARY.md` stating
> pass/fail and the measurement that decided it.
>
> | Gate | Verdict | The measurement |
> | :-- | :-- | :-- |
> | 1 ESI Load Stability | **FAIL** | no request reached the proxy for **3h58m** (N=1) and **3h43m** (N=3) against a 5m `ttl_floor` |
> | 2 Revocation SLO | **PASS** | p99 **0.134s** against 60s, over 15,000 revocations with the bulk queue saturated |
> | 3 Alert Delivery Integrity | **PASS** | 13,454 enqueued = 595 + 5,190 + 7,669 + **0 pending** + 0 failed |
> | 4 Feature Parity | **PASS** | 58 capability rows, 58 verified, 0 unreachable, 0 false citations |
> | 5 Deployment Usability | **FAIL** | 2 conditions failed, 1 partial, 1 unverifiable — the artefacts are unpublished |
> | 6 Spec-Drift Resilience | **PASS** | four §6.1 outcomes through the production ingest, clean tree at the tag |
> | 7 Third-Party Migration | **PASS** | 16 of 16 served routes byte-identical across 34 legacy routes |
>
> **B-1 is closed** — there is now a runner per gate and every gate has produced its blocking
> artefact. **B-2 and B-3 are closed in code.** **B-4 remains the operator's** and cannot be closed
> from inside the codebase.
>
> Running the gates found **six defects that no test in the suite could have caught**, listed in
> §5–§9 below. One of them (§9) makes HANGAR stop calling ESI permanently after any burst of
> errors, and is the reason Gate 1 failed.

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

### N-8 — Gate 7 §7.5's sunset policy cannot be verified: there are no release notes *(found in Phase 21)*

> **Closed in Phase 22.** `docs/RELEASE_NOTES.md` now exists and announces `/api/v2`'s deprecation
> in the release that introduces it, with the removal date, the replacement and the migration
> guide. §7.5's fourth row — "the `Sunset` header matches the announced date, automated check
> against the release notes" — is `TestReleaseNotesMatchTheSunsetHeader`, which reads
> `v2shim.SunsetDate` and requires the notes to carry it. The direction is deliberate: the header
> is authoritative and the notes are checked against it, because a release note that disagrees with
> the header the server actually sends is worse than none — it is an announcement an integrator
> would act on.

§7.5 checks the sunset policy against `docs/RELEASE_NOTES.md` — "removed no earlier than two minor
versions later", "removal announced at least one minor version in advance", and "the `Sunset`
header matches the announced date, automated check against the release notes". **That file did not
exist.** Three of the four rows in that table therefore have nothing to check against, and the
fourth (the shim ships in v1.0) is the only one this release can evidence.

Conditions 7.2 and 7.3 — that every shim response *carries* `Deprecation: true` and an RFC 8594
`Sunset` — are separate, are numbered pass conditions, and do pass
(`TestShimEmitsDeprecationAndSunset`, run as part of Gate 7's evidence). What is unverifiable is
whether the date in that header agrees with an announcement, because no announcement exists.

Also worth stating plainly, since Gate 7's summary should not be read as covering more than it
does: conditions **7.5** (Laravel pagination synthesised from keyset results), **7.6** (money
emitted as JSON numbers) and **7.9** (Appendix C complete in *both* documents) are asserted by
unit and integration tests rather than by the gate run, and the run's `SUMMARY.md` claims only the
conditions it measured.

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

### N-9 — §4.4's alert pipeline runs only under `work`, so the stock deployment delivers no alerts *(found in Phase 22)*

Found while fixing B-6, by asking the same question B-6 asks — *which process actually does this?*
— of the rest of `work.go` rather than only of its River workers. Re-derived three ways at this
commit:

| Claim | Evidence |
| :-- | :-- |
| The producers are wired only in `work` | `wireAlertGeneration` (the CCP-notification hook) and `runThresholdEvaluator` (the four threshold alerts) are called from `cmd/hangar/work.go` and nowhere else |
| The pump is wired only in `work` | `runAlertDispatcher` and `ensureDefaultAlertChannels`, likewise |
| The stock stack runs one process, `serve` | `docker-compose.yml`'s only `hangar` service is `command: ["serve"]` |

So after B-6 a default installation synchronises, provisions, serves and sweeps — and produces no
alert events and delivers no messages. §4.4 is entirely absent from it. **This is the same defect
class as B-6 and it is not fixed in Phase 22**, for a reason that is worth stating rather than
leaving as a scope note: it cannot be fixed by starting the pump in `serve`, because of N-10 below.

`serve` therefore registers no alert-delivery metrics either, which is why
`buildMetricsRegistry` still receives `nil` for them there.

### N-10 — the alert dispatcher claims without a lease, so two pumps double-send *(found in Phase 22)*

`ClaimPendingAlertDeliveries` (`db/queries/alert.sql`) is a bare

```sql
SELECT * FROM app.alert_delivery
 WHERE state = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= now())
 ORDER BY created_at LIMIT $1;
```

with no `FOR UPDATE SKIP LOCKED`, no lease, and no state transition at claim time —
`alerting.Dispatcher.Tick` makes an HTTP call between claiming and settling. Two dispatcher
processes ticking together therefore claim the **same** rows and send the same message twice.

This is **not** a consequence of B-6 and is not new: nothing has ever stopped an operator running
two `hangar work` replicas, which is River's normal scale-out and what `docker-compose.yml`'s own
comments describe. Gate 3 has never seen it because Gate 3 runs one pump.

`LeasePendingWebhookDeliveries`, twelve lines away in `db/queries/outbox.sql`, is the correct
shape and says why in its own comment: claim-by-lease, not claim-by-read, because the dispatcher
must not hold a transaction open across an HTTP call. The fix is to give the alert claim the same
treatment, and it is the prerequisite for N-9 — starting a second pump before then would turn
"alerts are never delivered on a default installation" into "alerts are delivered twice on a
scaled-out one", which is worse.

Deliberately not done in Phase 22: it is a change to the claim protocol Gate 3 measures, made in
the phase that must then re-run Gate 3, and it was not one of the six defects this phase exists to
close. It belongs with N-9 in a phase of its own.

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

#### Closed in Phase 22 (B-6)

`cmd/hangar/workers.go` now owns `buildWorkerPool`, and **both** `serve` and `work` call it. The
fix is in `serve`, not in compose, because Gate 5.5 requires the default profile to be exactly
postgres + hangar + the one-shot migrate — adding a `work` service would have failed a passing
gate to work around a process that was supposed to do this all along.

Three things were worth getting right rather than merely making it compile:

* **One River client per process.** `serve` previously built a deliberately insert-only client
  (`newInsertOnlyRiverClient`) for the revocation triggers. Keeping both would have put two
  producers in one process against the same queues. The insert-only constructor is **deleted**, and
  `wireRevocationTriggers` takes the real client; `Start` is called after the triggers are wired,
  so nothing is consumed by a process not yet able to produce correctly.
* **A co-running `work` stays valid.** Competing consumers on one queue is River's normal mode
  (`FOR UPDATE SKIP LOCKED`), and it is what "administrators who have outgrown one box" run.
* **The ledger mode selector still lands where it should.** `CountLiveReplicas` counts rows in
  `app.esi_replica` regardless of role, and `serve` has always heartbeated. What changes is that
  its heartbeat now corresponds to a process that really does call ESI, where before `serve`
  counted toward the replica total while building no gateway at all. One `serve` still selects
  solo; `serve` + `work` still selects clustered. Gate 1's topology is untouched —
  `tools/gate1-load` runs `hangar work` replicas and hosts the planner in-process without a
  heartbeat, by design.

The regression guard is `cmd/hangar/workers_test.go`, and it guards the SHAPE rather than the
instance: `river.AddWorker` may appear only in `workers.go`, and both role files must call
`buildWorkerPool` and start the client. A copy is how this happened, so a copy is what it forbids.

## 9. Found by running Gate 1 — the proactive pause never resumes *(blocking, not fixed)*

**This is the most serious defect in this document, and it is the one Gate 1 exists to find.**

Once Governor 2's proactive pause trips, the installation stops making ESI requests **and never
starts again**. Not until the error budget recovers — never, for the life of the process.

The deadlock, in three lines of call graph:

1. `Governor2.applyHysteresis` holds the only resume path
   (`currentlyPaused && remaining >= resumeAt → setPaused(false)`).
2. `applyHysteresis` is called from exactly one place: `Governor2.RecordError`.
3. `RecordError` is called from exactly one place: `esi.Client`'s response path
   (`internal/esi/client.go:502`) — which requires a request to have been **made**.

While paused, no request is made. With no request, no error is recorded. With no error recorded,
the resume condition is never evaluated. The 60-second window never rolls over either, because
`RecordErrorAgainstBudget` is what advances it.

Measured on the four-hour N=1 run at `v1.0.0-rc1`:

| | |
| :-- | :-- |
| Requests served | 8,371 — **all of them in the first 16 minutes** |
| `divergence.csv` coverage | 17 minutes of a 240-minute run |
| `app.esi_error_budget` at end of run | `paused = t`, `error_count = 85`, `window_start` **4h 0m 3s old** |
| `esi_error_limit_remaining` at end of run | 15, unchanged since minute 16 |

So a single burst of errors — 85 of them, against a budget of 100 per minute — permanently halted
an installation with 225,000 subscriptions. In production this is a corporation's ESI sync going
silent after one bad afternoon from CCP, with no alert and no recovery short of a restart.

**Why no test caught it.** `internal/esi/ratelimit`'s own suite drives the state machine by calling
`RecordError` directly, so the resume branch is exercised in every unit test. What no unit test can
see is that in production nothing calls it while paused. This is the B20 defect class — a code path
with no reachable caller in the state that needs it — and it is invisible to the reachability guard
too, because `RecordError` *does* have a production caller; it just cannot be reached from the state
that requires it.

**The fix** is to evaluate the hysteresis on a clock rather than only on the error path: either
re-read the budget row in `IsPaused` when the cached window has expired, or run a small ticker that
calls `applyHysteresis` while paused. Not made in Phase 21 — it changes the binary every gate in
`docs/gate-evidence/v1.0.0-rc1/` was measured against, and it must be measured by a Gate 1 run of
its own.

#### Closed in Phase 22 (B-5)

`Governor2.IsPaused` evaluates the resume, because it is the **only** Governor 2 code that runs
while paused — `esi.Client.Do` calls it before the ledger, before the breakers' verdict is acted
on, before anything is sent. A ticker was the alternative and was rejected: it would need starting,
stopping and owning by every process that builds a gateway, to do on a schedule what the existing
one-second cache refresh already does on demand.

**Reading the row is not enough**, which is why this is a second query rather than a comparison in
Go. `remaining` is derived from `error_count`, and `error_count` belongs to a window that may no
longer apply. `ResumeErrorBudgetIfRecovered` (`db/queries/esi_error_budget.sql`) makes the whole
decision in one atomic UPDATE against the database's own clock:

```
paused AND (window elapsed OR max - error_count >= resume_at)
```

for the two reasons `RecordErrorAgainstBudget` above it already had. `window_start` is a
**database** timestamp, so comparing it against a replica's wall clock would make the resume
sensitive to skew; and two replicas can evaluate this in the same instant with one of them having
rolled the window over in between — a just-rolled window has a fresh `window_start` (the elapsed
test fails) and a small `error_count` (the remaining test succeeds), so the `OR` is what makes that
race harmless.

**§5.7's hysteresis survives.** The threshold is `resume_at`, never `pause_at`. An elapsed window
resumes because a fixed window that has elapsed carries no errors at all — remaining is `max`,
which is `>= resume_at` by construction. The gap between the two thresholds exists to stop a resume
after a single error expires, and nothing here does that.
`TestErrorLimitResumeKeepsHysteresis` pins both sides: remaining 30 (inside the gap) stays paused,
remaining 60 (exactly `resume_at`) resumes.

**The regression test never calls `RecordError` after the pause, and asserts that it didn't.** That
is the whole point — every existing test in the package un-paused by calling `RecordError`, which
is the one branch production can never reach. `TestErrorLimitResumesWithoutARequest` fails against
the rc1 code and passes against this one.

**The same defect existed one driver over, and is fixed with it.**
`internal/provisioning/drivers/discord.InvalidBudget` was written to mirror §5.7's mechanism and
mirrored the deadlock too: `RecordInvalid` held the only un-pause branch, it runs only on a counted
response, and `Client.Do` returns `ErrInvalidBudgetPaused` before it sends — so Discord
provisioning went silent permanently after any burst of 401/403/429. Found by call graph rather
than by measurement (no gate drives a real Discord guild) and closed the same way, with
`ResumeDiscordInvalidBudgetIfWindowElapsed`. There is no hysteresis gap to preserve there: the
window rollover **is** the resume condition, as that type's doc comment has always said.

**Condition 1.6 should have caught this and did not.** §1.2 states it as "throughput never drops to
zero for more than one `ttl_floor`"; `test/load`'s `evaluate()` implements it as `total > 0` — a
different and far weaker claim. The first N=1 run reported 1.6 as a **pass** while serving nothing
for three hours and forty-four minutes. The runner now measures the interval the condition actually
names, and reports the harness's weaker reading beside it as `1.6-raw`.

---

## 8. Found by running Gate 1 — `esi_ledger_mode` reports a default as an observation *(not fixed)*

`ratelimit.NewGovernor1` starts in `ModeSolo` optimistically and only consults the replica
registry inside `ensureMode`, which `Acquire` calls. Between a process starting and its first ESI
request, `esi_ledger_mode` therefore reports an **assumption**, and reports it in exactly the same
shape as a genuine reading.

**No request is served in the wrong mode**, and that is worth stating precisely because it bounds
the defect: `Governor1.Acquire` calls `ensureMode` *before* choosing a ledger, and on the first
call the 2-second throttle cannot skip it (`lastModeCheck` is the zero time). Measured in the
replica logs of an N=3 run: all three replicas logged `mode transition from=solo to=clustered
live_replicas=3` exactly once, and none ever transitioned back.

What it broke is the **gate that reads the gauge**. Condition 1.8 requires `clustered` for the
whole N=3 run; §1.4 requires a mid-run replica restart; a restarted replica reports `solo` until
its first request. The two conditions contradict, and no implementation can satisfy both — the
same shape as the units contradiction Phase 20.4.1 found in condition 1.3. §1.8 is amended in
`04_RELEASE_GATES.md`; the amendment records what was excluded from the measurement and why.

This is `esi_ledger_divergence` reading 0 over zero samples, one metric over. 20.4.1 fixed that one
by giving "not measured" a value distinct from "measured zero". The equivalent fix here is for
`esi_ledger_mode` to emit **no sample** — or an explicit `unknown` series — until the first registry
evaluation. Not made in Phase 21, because it changes the binary the whole of
`docs/gate-evidence/v1.0.0-rc1/` was measured against.

#### Closed in Phase 22 (B-10), **and the §1.2 amendment is withdrawn**

`Governor1.Mode()` returns `(Mode, observed bool)`; `observed` becomes true only when `ensureMode`
has actually read the replica registry, and stays false when the registry is unreachable — holding
the starting assumption because you cannot read is still holding an assumption.
`telemetry.GatewayCollector` emits **no `esi_ledger_mode` sample at all** while `observed` is
false. Silence, not zero and not a guess, following the house rule this repository already applies
to `esi_ledger_divergence` (no sample for a group the server has not reported on) and
`esi_error_limit_remaining` (no sample until the budget row exists).

With that fixed there is no settling interval left to exclude, because there are **no samples** in
it. `04_RELEASE_GATES.md` §1.2's Phase 21 amendment to condition 1.8 is therefore **withdrawn** —
the note is kept, because a condition that was amended and then un-amended should be able to show
both — and `tools/gate1-load`'s `evaluateModeSelection`, `modeSettleWindow` and the `1.8-raw`
companion reading are deleted with it.

**The general lesson, since this is the second time.** Conditions 1.3 and 1.8 both looked like
contradictory gate wording and both turned out to be instrumentation that could not distinguish
"no reading" from "a reading of the default". Before amending a gate condition, check whether the
metric can say "I don't know".

---

## 7. Found by running Gate 3 for the first time — the early warning arrives late *(not fixed)*

`corporation.structure.fuel_low` and `corporation.contract.expiring` are delivered **at the
deadline they warn about**, not when the threshold is crossed.

The mechanism, end to end:

1. `Evaluator.structureFuel` stamps `OccurredAt` as the **expiry**, deliberately and with a
   comment: *"it is what the alert is about, and using it means a burst of structures expiring in
   the same coalescing window rolls up into one message."* `expiringContracts` does the same with
   `date_expired`.
2. `Emitter.Emit` derives the coalescing bucket from `OccurredAt`.
3. `CoalesceKey.Due(window)` is `bucket + window`, and that value is written to
   `app.alert_delivery.next_attempt_at` — the moment the delivery becomes **claimable**.

So a structure whose fuel runs out in 17 hours produces a delivery the dispatcher cannot pick up
for 17 hours. Measured directly on a Gate 3 run: 336 deliveries `pending` with `attempts = 0` and
`next_attempt_at` as far out as the following evening.

The coalescing decision is sound — grouping structures that expire together is exactly right. What
is wrong is that the same timestamp also became the delivery's **due time**, so an alert whose
entire purpose is advance warning is scheduled to arrive at the moment it stops being actionable.
`corporation.member.inactive` is unaffected and its code says why: it passes `now` explicitly,
because "the moment somebody BECAME inactive is months in the past".

Not fixed here (it is a change to `internal/alerting`, and therefore to the binary every gate in
`docs/gate-evidence/v1.0.0-rc1` measured). The fix is to separate the two uses: keep the expiry as
the coalescing bucket, and make the delivery due at `min(bucket + window, now + window)`.

Gate 3's run seeds its threshold subjects with deadlines inside the run so the pipeline is
exercised end to end, and reports this behaviour through its own condition
(`3.1-scheduled-beyond-run`) rather than letting it be scored as a drop. A delivery that has not
come due has not been lost, and §3 is about loss.

#### Closed in Phase 22 (B-9)

The two uses of the timestamp are separated. `CoalesceKey.Due(window)` is unchanged and still
answers "when does this key's window close"; the new `CoalesceKey.DueBy(now, window)` caps that at
`now + window`, and `Emitter.Emit` writes **that** to `app.alert_delivery.next_attempt_at`.

**The coalescing is untouched** — the bucket is still the expiry, so structures that run dry
together still roll up into one message, which is what `structureFuel`'s comment argued for and was
right about. A bucket in the past is also untouched: `corporation.member.inactive` passes `now`
explicitly, and every notification-driven event is the same shape, so `bucket + window` is already
at or before `now + window` and the cap never binds.

**Gate 3's workaround is removed rather than kept.** `tools/gate3-alerts/world.go` seeded every
threshold deadline minutes away so the pipeline could be exercised at all; the subjects now expire
2-46h (structures) and 6-66h (contracts) out, which is the realistic world — an operator's
structures run dry tomorrow afternoon, not in nine minutes. That turns `3.1-scheduled-beyond-run`
from a condition the seeding was arranged to satisfy into one that **measures the fix**: these
deadlines are far outside any run this gate performs, so a single delivery still scheduled past the
end of it means B-9 is not fixed.

---

## 6. Found by running Gate 5 for the first time

Three defects, none of which any test in the suite could have caught, because every test builds its
own connection string and its own configuration. This is what §5 is for.

### 6.1 — `install.sh` generated a database password that breaks the deployment *(fixed here)*

`install.sh` generated `POSTGRES_PASSWORD` with `openssl rand -base64 32`, and
`docker-compose.yml` interpolates that value straight into a URL:

```
HANGAR_DB_URL: postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/...
```

base64's alphabet contains `/`, which **terminates a URL's authority**. Measured:

```
migrate: connecting to database: cannot parse
  `postgres://hangar:xxxxxx@postgres:5432/hangar?sslmode=disable`:
  failed to parse as URL (invalid port ":ab" after host)
```

The `migrate` service exits 1 and the whole turnkey deployment fails at the third command. A
43-character base64 string contains at least one `/` roughly half the time, so **about one
installation in two failed** — non-deterministically, which is worse than always. Both installers
now generate the database password as hex, which is URL-safe by construction; the two 32-byte
base64 secrets are unchanged, because neither is ever placed in a URL.

### 6.2 — the `HANGAR_PUBLIC_URL` / callback mismatch is not reported *(not fixed)*

§5.3 requires: *"Wrong `HANGAR_PUBLIC_URL` → the SSO callback mismatch is reported as a
configuration error with the expected value shown, not as an opaque OAuth failure."*

`internal/config/validate.go` contains no such check, and nothing anywhere compares the two values.
An operator who sets one and not the other learns about it when a user clicks "log in" and EVE SSO
rejects the `redirect_uri` — precisely the opaque OAuth failure the condition forbids.

Not fixed here for the same reason as §5 above: it is a change to `internal/config`, and therefore
to the binary every gate in this directory measured. It is small (compare the two at validation
time, print the expected callback) and belongs in the phase that can re-run the gates after it.

**Closed in Phase 22 (B-8).** `internal/config.Validate` compares them at boot and the error names
the **expected** value, `${HANGAR_PUBLIC_URL}/auth/callback` — because an operator who set one of
them wrong needs the other, and "these two disagree" does not tell them which. A trailing slash on
the public URL is tolerated and scheme/host are compared case-insensitively (neither difference is
a misconfiguration, and failing a boot over one would be the fail-fast contract used as a trap);
everything else is compared exactly, because EVE SSO matches `redirect_uri` against the
registration byte for byte, so a difference forgiven here would simply become the opaque failure
one layer down. `HANGAR_PUBLIC_URL` is now also required to be an absolute URL, which it never was.

The check immediately found a real instance of the misconfiguration it exists to catch:
`web/playwright.config.ts` set `HANGAR_PUBLIC_URL` to port 8099 and left the callback on its
default of 8080. The e2e harness had been running the exact configuration §5.3 forbids, unnoticed,
because nothing compared the two.

### 6.3 — the binary parses a same-named file in its working directory as its config *(not fixed)*

`internal/config.New` sets viper's `SetConfigName("hangar")`, `SetConfigType("yaml")` and
`AddConfigPath(".")`. A file named exactly `hangar` — no extension — in the working directory is
therefore **read as a YAML configuration file**. In a manual deployment (§9.2, and condition 5.8),
the file with that name in that directory is *the binary itself*:

```
Error: config: reading config file: While parsing config: yaml: control characters are not allowed
```

which names neither the file nor the reason. Verified both directions: the same bytes mounted at
`/hangar` with the working directory `/` fail, and mounted at `/opt/hangar-bin` migrate an external
PostgreSQL 18 successfully. So the static binary is fine — what fails is the obvious layout,
`/opt/hangar/hangar` with `WorkingDirectory=/opt/hangar`, which is what a systemd unit does.

The fix is small (drop `SetConfigType`, or require the config file to carry its extension) but it
is a change to `internal/config` and therefore to the binary this directory's evidence measures.

**Closed in Phase 22 (B-7).** `SetConfigType("yaml")` is dropped. viper's `searchInPath` tries
`hangar.<ext>` for every supported extension and only then, **if a config type has been declared**,
falls back to a file named exactly `hangar` — so the declaration was the entire cause.
`./hangar.yaml` and `/etc/hangar/hangar.yaml` are still found by the extension loop, and the format
is now taken from the extension the file actually carries instead of being asserted over it.
`internal/config/configfile_test.go` pins all three: the binary is invisible to the search, `Load`
succeeds in §9.2's layout, and `hangar.yaml` beside the binary is still read.

### 6.4 — the three §5.1 inputs are unpublished

`raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml` → **404**.
The installer URL → **404**. `ghcr.io/hangar-project/hangar` → **403**, no anonymous pull. The
repository has no git remote at all.

B-3 corrected the *path* inside the second URL; it could not make the repository exist. Gate 5 was
therefore run against a substituted local origin, and condition 5.2's "the image is pulled from the
public registry" is recorded as **not met**. Publishing is a release action, not a code change —
but until it happens the documented three-command deployment cannot be performed by anybody.

#### Decided in Phase 22 (B-12) — recorded as permanently substituted, and what that costs

Re-measured at v1.0.0-rc2:
`raw.githubusercontent.com/hangar-project/hangar/main/docker-compose.yml` **404**,
`.../deploy/install.sh` **404**, `ghcr.io/hangar-project/hangar` **403** with no anonymous pull,
`git remote -v` **empty**.

**The decision, made explicitly rather than deferred a third time: condition 5.2 is recorded as
permanently substituted for this release candidate.** Publishing requires a git remote, a GitHub
organisation and registry credentials, and is an outward-facing push — an operator action, not
something a development phase can perform or should perform on the operator's behalf.

What that costs, stated precisely so it is not left to inference: **the documented three-command
deployment cannot be performed by anybody who is not this repository.** Everything else about it is
verified against the real artefacts — 5.1, 5.3, 5.4, 5.5, 5.6, 5.7 and 5.8 all measured — so the
moment the three URLs resolve, 5.2 becomes a pass with no further work of any kind. Until then Gate
5 is not fully met and §8's "release blocks on all seven" applies. **This is the single item
standing between Gate 5 and a real pass**, and it is not a code defect.

Closing it is four steps, none of which touch the codebase: create `hangar-project/hangar`, push
`main` and the tag, push the image to `ghcr.io/hangar-project/hangar` with public read, and re-run
`bash tools/gate5-deploy/run.sh` without the local-origin substitution.

---

## 4. Verdict

### As the audit left it

**Not clear for v1.0 release candidacy.** Four blockers:

1. **B-1** five of seven gates never run, and Gate 1 has no way to run it — the largest item by far
2. **B-2** no retention job; `app.session` accumulates personal data indefinitely
3. **B-3** the documented install command 404s
4. **B-4** two operator actions, external to the codebase

B-3 is minutes of work once the placement decision is made. B-2 is a small, well-understood job.
B-4 is the operator's. **B-1 is the real gate to v1.0**: roughly nine hours of measured runs, an
N=3 deployment, a clean host, and runners that do not exist yet.

### After Phase 21 — `v1.0.0-rc1` is not a release candidate

Five gates pass. **Two fail**, and `04_RELEASE_GATES.md` §8 is unambiguous: *"Release blocks on all
seven."*

**Gate 1 fails on a defect that stops the product.** Once Governor 2's proactive pause trips, the
installation never calls ESI again (§9). Measured at both replica counts: 3h58m and 3h43m with no
request reaching the proxy, on installations holding 225,000 subscriptions. This is not a
performance regression or a tuning question — an ordinary burst of upstream errors, well inside
what ESI produces on a bad day, silently ends all synchronisation until someone restarts the
process. It must be fixed before any release.

**Gate 5 fails on three things, one of them now fixed.** The installer generated a database
password that broke roughly half of all turnkey deployments (§6.1, fixed here); a
`HANGAR_PUBLIC_URL`/callback mismatch is never reported (§6.2); and the binary parses a same-named
file in its working directory as its config, which is exactly what §9.2's manual layout produces
(§6.3). Separately, §5.1's three commands cannot be performed by anyone at all: the compose file,
the installer and the image are all unpublished (§6.4).

**What the failures do not touch, and this matters for what comes next.** The rate-limit ledger —
the subsystem §8 predicted would fail first — was measured under 150,452 requests across three
replicas sharing one Postgres, and produced zero Governor 1 breaches, zero overdrawn buckets and
zero post-reconciliation divergence over 3,739 group-samples. The alert pipeline balanced its
accounting identity exactly with zero drops over 13,454 deliveries. The revocation SLO came in at
p99 0.134s against a 60-second budget while the bulk queue performed 15,000 platform calls of its
own. The `/api/v2` shim is byte-identical on all 16 served routes. The spec-drift ingest handled a
spec nobody anticipated with a provably clean tree.

The gates that pass are the ones that took years of design to get right. The gates that fail are
four small defects and one unpublished repository.

### What blocks `rc2`

| # | Item | Where |
| :-- | :-- | :-- |
| 1 | Governor 2's pause never resumes | §9 — **blocking, stops the product** |
| 2 | `serve` registers no River workers, and the stock compose runs only `serve` | §5 — **blocking, the default deployment never syncs** |
| 3 | The binary parses a same-named file as its config | §6.3 |
| 4 | A public-URL/callback mismatch is never reported | §6.2 |
| 5 | Fuel and contract warnings are delivered at the deadline they warn about | §7 |
| 6 | `esi_ledger_mode` reports its default as an observation | §8 |
| 7 | Publish the repository, the compose file, the installer and the image | §6.4 |
| 8 | The two operator actions | B-4 |

Items 1–6 are all small. None was found by a test; all six were found by running gates that had
never been run. After they land, **every gate must be re-run** — the evidence in
`docs/gate-evidence/v1.0.0-rc1/` describes a binary that no longer exists the moment item 1 is
fixed.
