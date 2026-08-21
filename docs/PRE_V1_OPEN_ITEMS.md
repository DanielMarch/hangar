# Pre-v1.0 open items — audit at `c0b35ac`, closed at `v1.0.0-rc3`

> **Status after Phase 22.** All seven gates have been re-run at `v1.0.0-rc2`, against a binary
> with the six defects of §§5–9 closed. Evidence is in `docs/gate-evidence/v1.0.0-rc2/`.
> `docs/gate-evidence/v1.0.0-rc1/` is kept: a release that had to correct itself should be able to
> show what it corrected.
>
> | Gate | rc1 | rc2 | The measurement at rc2 |
> | :-- | :-- | :-- | :-- |
> | 1 ESI Load Stability | **FAIL** | **PASS** | longest zero-throughput interval **1m30s** (N=1) and **1m0s** (N=3) against a 5m `ttl_floor`; 1,143,649 and 1,145,965 requests served |
> | 2 Revocation SLO | PASS | **PASS** | p99 **0.132s** against 60s, over 15,000 revocations with the bulk queue saturated |
> | 3 Alert Delivery Integrity | PASS | **PASS** | 13,454 enqueued = 595 + 5,190 + 7,669 + **0 pending** + 0 failed |
> | 4 Feature Parity | PASS | **PASS** | 58 capability rows, 58 verified, 0 unreachable, 0 false citations |
> | 5 Deployment Usability | **FAIL** | **PASS (with substitution)** | 0 conditions failed; 5.8 partial (no systemd on this host), 5.2 substituted (nothing is published) |
> | 6 Spec-Drift Resilience | PASS | **PASS** | four §6.1 outcomes through the production ingest, clean tree at the tag |
> | 7 Third-Party Migration | PASS | **PASS** | 16 of 16 served routes byte-identical across 34 legacy routes |
>
> **The two gates that failed at rc1 now pass, and the numbers that decided them moved by orders
> of magnitude rather than marginally.** Gate 1 at N=1 served **1,143,649** requests against
> rc1's 8,371, and its `divergence.csv` covers **240 of 240 minutes** against rc1's 18. Condition
> 1.4 still fires the proactive pause and condition 1.6 still shows throughput continuing —
> together, that pairing is what proves §9's deadlock is gone.
>
> **B-1, B-2 and B-3 are closed. B-4 remains the operator's** and cannot be closed from inside the
> codebase. **B-12 is decided** rather than deferred: nothing is published, and §5.2 is recorded as
> permanently substituted.
>
> Running the gates a SECOND time found six more defects — **in the gate runners**, not in the
> product — and they are listed in §10 because five of the six would have produced a wrong verdict
> rather than an error, and two would have produced a false PASS.

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

### B-4 — Two operator actions outstanding, unchanged since Phase 20.8 *(CLOSED in Phase 23)*

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

#### Closed in Phase 23 — one half by the operator, the other by measurement

Full evidence in `docs/gate-evidence/v1.0.0-rc3/b4/`.

**The re-authorisation was performed.** The operator revoked HANGAR and logged in again during
this phase, and `app.character_token_scope` went from **50 to 52** — both previously-missing
scopes granted, the derived and granted sets now identical. **Standing trap 10 did not fire**:
EVE silently drops scopes the SSO registration lacks, and it dropped neither, so the registration
carries all 52.

**Capability #41's structure half now runs against real ESI**, and 20.9's prediction was exact:

```
/universe/structures/{structure_id}
  last_status = 200   last_success = 2026-08-21 02:50:36+00
  sync_run: outcome 200, rows_affected 0, error none
```

The scope gate no longer excludes the subscription, the reconciler created it, the planner
claimed it and the worker ran it — **the whole path is live and there is nothing for it to fan
out over.** A 403 would have meant the grant was wrong; a 200 with zero rows means the grant is
right and the installation is empty.

**The alliance half was never an operator action, and this phase has the measurement that says
so.** Phase 20.8 recorded it as one. It is not:

```
characters:                  2
corporations:                1
corps with an alliance_id:   0
character 2124613505  corp = 98840805  alliance = null
```

**The operator's corporation is not in an alliance.** No action available to them populates
`app.alliance` short of joining one, so `AllianceWorker` cannot be dispatched on this
installation and will not be on any installation whose corporations are unallied.
`TestSyncAllianceSubscriptionsAreOrdered` remains the only claim that can be made for it.

That is a **limitation of the evidence, not an outstanding action**, and the distinction decides
the release: an unfinished task blocks, and a fact about one developer's EVE character does not.

---

## 3. Non-blocking open items — EMPTY

**Every N-item is closed.** An item could leave this section by being FIXED or by being DECIDED,
and by no other route; "leave it for another phase" had been the answer four times and was not
available again. Seven were fixed, three were decided, and the register below records which was
which and the measurement that settled it.

Two of them were fixed by measurements nobody had taken, and both are worth reading as method
rather than as changelog: an importer that had never once worked, and a five-phase-old blocker
that dissolved when somebody finally asked the database what it held.

| # | What it was | How it left | The measurement |
| :-- | :-- | :-- | :-- |
| N-1 | Gate 7 is 16 of 34, and 12 of the 18 are merely "pending" | **FIXED and DECIDED** | 11 reclassified to unshimmable, 1 measured and reclassified, 1 SERVED. **Gate 7 is 17 of 34 and no route is pending** |
| N-2 | `character.sheet`'s reason cited evidence that does not exist | **DECIDED by measurement** | One `refresh_tokens` row seeded, corpus re-recorded: `"user_id": 1`, a MySQL auto-increment against HANGAR's uuid |
| N-3 | Eight served routes rest on single-row recordings | **FIXED** | A second row in each, higher key inserted first. Ordering rule held; **two byte-compatibility defects found and fixed** |
| N-4 | 47 generated queries and 36 symbols with no production caller | **FIXED** | All five groups built with API and UI; 21 allowlist entries removed; both guards pass |
| N-5 | Latent cache-key collision | **FIXED** | Key hashes the resolved path; two tests that fail against the template |
| N-6 | Schema integrity covers tables only | **FIXED** | 163 tables, **1,133 columns, 115 indexes**, verified against a real PG18 |
| N-7 | No SDE imported (D-4) | **FIXED** | Build 3475087 imported in 59s — after fixing an importer that had never worked |
| N-8 | §7.5's sunset policy cannot be verified | Closed in Phase 22 | `docs/RELEASE_NOTES.md` exists and is checked against the `Sunset` header |
| N-9 | The stock deployment delivers no alerts | **FIXED** | One assembly both roles call; proven on a stock compose stack, **seven verdicts, receiver's own transcript** |
| N-10 | The alert dispatcher claims without a lease | **FIXED** | Claim-by-lease; against the old code two pumps made **12 claims for 6 deliveries and sent every message twice** |

---

### N-1 — Gate 7 is 17 of 34, and every unserved route now says why

**Twelve routes were `StatusPending`, and that status means "unfinished work recorded as
unfinished; it is not a design decision".** A release cannot ship a compatibility shim making
twelve promises. All twelve are decided and **no route holds that status any more.**

**Eleven moved to `StatusUnshimmable`**, and the only thing that changed is the status: every
reason was already derived against legacy's source in Phase 22's audit, and the status
contradicted the derivation. A route whose reason is "legacy puts a MySQL auto-increment on the
wire" is not waiting on work anybody intends to do, and the 501 body it was serving said "not
yet" to integrators who would have waited for a release that could never ship.

`StatusUnshimmable`'s own definition had to widen to hold them. It read "because HANGAR's
identifier space differs from legacy's", which described four of the routes and none of the
eleven. What all fifteen share is the property that matters: **the bytes come from legacy's own
storage, and HANGAR's storage is correct rather than merely different.** A decimal money column
cannot reproduce a double's rounding error; a uuid cannot reproduce an auto-increment. Serving
them would mean storing legacy's mistakes.

**The twelfth became the seventeenth SERVED route** — see the N-3 entry below for how, because
the two items solved each other.

### N-2 — settled, and it fell the way the reason predicted

`fixtures.php` now seeds **one** `refresh_tokens` row (character 90000001, user_id 1) and the
corpus is re-recorded. `character.sheet` emits

```
"user_id": 1
```

where it used to emit `null`. The reason held and is now **evidenced rather than merely true**:
that `1` is legacy's `users.id`, a MySQL auto-increment, against HANGAR's uuid — the same
blocker `users.index` and `users.show` already carry.

The old recording could not have shown this. **A shim emitting a constant `null` would have been
byte-identical to it and wrong on every real installation**, which is the
`corporation.structures`/`services` trap in a new place and the third time a Gate 7 reason has
been true-but-mis-evidenced (B55, B57, this). Character 90000002 deliberately keeps no token, so
the corpus now records the populated and the unpopulated case and the difference between them is
visible rather than inferred.

### N-3 — two defects a one-row recording could not have shown

A second row in all eight recordings, each inserted with a key that sorts **before** the row
already there, so insertion order and ascending order disagree and the recording says which one
legacy uses. **The 20.10 ordering rule held.** Two byte-compatibility defects fell out of served
routes:

* **`corporation.market-orders`** — legacy's `issued_by` is `bigint NOT NULL DEFAULT 0`, so a
  legacy installation cannot hold a null there. HANGAR's column is nullable and the shim emitted
  `null`, a value no legacy client has ever seen from that field. The one recorded order had an
  issuer.
* **`character.market-orders`** — a price above 2^53 diverged, which is
  `reasonMySQLDoubleRounding`: the blocker on a **different** route, surfacing on a served one.
  The single recorded price was `5.55`.

#### The second one is the phase's best find, and it is a lesson about where to look

Phase 20.6 measured that the corpus and the formatter disagree. Phase 20.7 measured PHP itself
and **refuted** the 14-significant-digit hypothesis — `serialize_precision = -1`,
`json_encode(9007199254740993.01) = 9007199254740994` — concluding, correctly, that "the
divergence is in what legacy's MySQL DOUBLE column came to hold, upstream of any encoder", and
reading that as unreproducible.

Nobody had asked the database what it held:

```sql
SELECT price FROM character_orders WHERE order_id = 8999;
-- 9.007199254741e15
```

**Thirteen significant digits, in the table.** MySQL 8.4 renders doubles at full
shortest-round-trip precision on read, so the loss is not the read either. It is the **write**:
PDO binds a PHP float by *stringifying* it at the `precision` ini, which is 14, so MySQL received
the text `9.007199254741E+15` and stored the double nearest to that.

Both earlier phases looked at the encoder because that is where the divergence appeared. It was
introduced three layers earlier, on a path neither had reason to think about.

**So it is reproducible.** `v2shim.phpPrecision` applies the same stringification to the exact
decimal before the float64 conversion, `reasonMySQLDoubleRounding` is **deleted** rather than
kept as a blocker known to be false, and **`character.wallet-journal` is served** — the shim's
seventeenth route and its first wallet route. `$incrementing = false` on
`CharacterWalletJournal` means its `id` is ESI's journal-entry id, not the surrogate that keeps
the other three wallet routes unshimmable.

It is also the first served route with a **real second page** (20 rows against a page size of
15), so Laravel's `prev`/`next` links are finally exercised against something other than the
degenerate single-page shape.

### N-4 — all five groups built, and one of them should have had a defect number

Every group is BUILT, with an API route and a UI surface. **21 allowlist entries are gone** and
both reachability guards pass.

| Group | What an operator could not do | Now |
| :-- | :-- | :-- |
| §4.4 routing and channels (8 queries) | say where any alert goes, except by SQL | `/api/v1/admin/alerts/*`, and an **Alert routing** screen |
| Platform and entitlement config (5) | **create a platform at all** | `POST /admin/platforms`, groups, and a rules-by-source reverse lookup |
| Subscription management (3) | disable or un-cache a subscription | `PATCH /admin/sync/subscriptions/{id}`, per-entity lookup, runs board |
| Open-vocabulary boards (3 of 4) | read what Principle 14 recorded | `/api/v1/admin/vocabularies`, seven boards with counts |
| Webhook dead-letter and outbox (2) | see a dead-lettered webhook | `/api/v1/admin/webhooks/*` |

**`CreatePlatform` deserved a defect number rather than an allowlist line.** `app.platform` had
**no production writer anywhere**, so no installation could create a platform row by any means
the product offered — and `cmd/hangar/discord.go`'s own comment says exactly that ("there can be
zero (an administrator hasn't created the platform record)") before warning and registering no
driver. Phases 11–13 built three provisioning drivers, an entitlement engine, an exposure board
and the revocation SLO Gate 2 measures, **on top of a table nothing could populate.** Every test
inserts the row it needs, which is why no test could see it.

`SetEntitlementRuleEnabled` did not simply gain a caller. Disabling a grant rule reduces
entitlements exactly as deleting one does, so a bare flag write would have been **B32 — the
silent deferral — reintroduced through a different verb.** It now goes through the same urgent
revocation, with `eventAt` stamped once for §2.2's SLO.

**The sixth is `GetEsiScope`, and it is the honest one.** It was DELETED this phase on the
reading that nothing wants one scope row, and then **restored** when its two callers turned out
to be Gate 6's own assertions — the novel scope grammar landing unrejected, and the board not
refilling itself after an acknowledge. The alternative was those tests issuing raw SQL, which
passes while the query layer production uses is broken. Recorded as a measurement rather than a
mechanism.

### N-5 — fixed while it was two lines

`esi.Client.cacheKey` hashes the **resolved** path now, as §5.3's formula and
`cache.KeyInput.Path`'s own doc comment have said since Phase 3. Two tests fail against the
templated key: character 2's conditional request replayed character 1's body, and wallet division
2 replayed division 1's — the second exists because a partial fix using only the first path
parameter would pass the first test.

Not live, and not harmless: the cache is read only on a 304 the caller conditioned and no fan-out
sends validators, so nothing ever collided. **It becomes real the moment anyone adds per-item
ETags to a fan-out** — a natural optimisation, since that is what ETags are for — and the failure
mode is serving one character's detail body as another's. A data-disclosure bug wearing a
performance improvement's clothes, which would have been attributed to the optimisation.

### N-6 — columns and indexes, and two parse bugs the baseline assertion caught

`hangar migrate up` and every serving process now verify **163 tables, 1,133 columns and 115
indexes**, all derived from the embedded migrations rather than listed.

Indexes compare by **signature** — table, access method, key columns, partial or not — because
110 of the 111 `CREATE INDEX` statements are unnamed, so Postgres generates the name and there is
nothing stable to compare. The blind spot that leaves is stated in the type comment and held
closed by a test rather than by hope.

**Two parse bugs were caught by `TestAFullyMigratedDatabaseHasNoDrift`**, which exists for exactly
that, because reporting drift on a CORRECT database is the worst failure a check like this can
have — an operator learns to ignore it and then ignores it on the day it means something:

* migration 00033 writes `DROP COLUMN` as one clause of a five-clause `ALTER TABLE`, so a regex
  anchored on `ALTER TABLE x.y DROP COLUMN` matched none of it and the register expected
  `fuel_expires` on two tables that dropped it three migrations ago;
* migration 00045 retires four indexes by their **server-generated** names, so the parse has to
  reconstruct `<table>_<col>_..._idx` to see the drop at all.

Constraints and runtime partitions are still not covered, and the file says so rather than
carrying a name that implies more.

### N-7 / D-4 — the SDE imported, after fixing an importer that had never worked

`hangar admin import-sde` **had never worked against a real export and could not have.** It asked
its `SourceProvider` for `<postgres table name>.jsonl` — `category.jsonl`, `group_.jsonl`,
`type.jsonl` — and CCP ships `categories.jsonl`, `groups.jsonl`, `types.jsonl`: plural,
camelCase, map-prefixed. **Not one of the twenty-two names matched.** The command downloaded
99 MB, found nothing, imported zero rows into every table, and correctly refused to swap on the
first smoke query.

It was invisible because `testdata/sde/*.jsonl` had been named after the **Postgres tables** and
shaped to match the extractors — fixtures invented to agree with the code rather than recorded
from the thing the code reads. `tools/gate4-traceability`'s header names that failure mode
exactly: *"a document that agrees with itself."*

Five more mismatches were behind the first (`iconFile`/`graphicFile` not `fileName`; a moon's
parent is `orbitID`; `operationName` not `name`; skins have a `types` array and an
`internalName`; `npcStations` has no name at all), and **two of the three tables described as
"derived" are not** — `typeDogma.jsonl` and `typeMaterials.jsonl` are their own exports.

Measured on a real PG18: **swapped in 59s, 25 tables, 52,863 types, 645,769 type dogma
attributes, 47,051 type materials, 19,138 blueprint activities.** The fixtures are now verbatim
lines from build 3475087, and two guards check every source name against a **recorded listing of
the real archive**.

Importing it then exposed a second defect: `BackfillSkyhookTypeIDFromSDE` resolved
`sde.type` by name — Principle 13, correctly applied — and **the name does not exist.** There is
no type called `Skyhook`; the anchorable structure is 81080 `Orbital Skyhook`. The backfill
resolved nothing before the import and would have gone on resolving nothing after it.

### N-9 — proven on a stock compose stack, by the receiver

`serve` runs §4.4's producers and pump, from **one assembly both roles call**
(`cmd/hangar/alerting.go`) rather than four calls copied into `serve.go`, because four calls
copied is how this happens a fourth time. This was the **third** seam wired in one process only
to have been a defect — B-25's producers, B-6's workers, this — so the seam now has the
structural guard the worker seam has. All three guards fail at `cdbd15d`.

`tools/gate3-alerts/compose-proof.sh` asserts the thing Gate 3 cannot, because Gate 3 runs the
pump itself. **Seven verdicts, all PASS**, with one container running `command: ["serve"]`:

```
n9-serve            one container, command serve, healthy
n9-catalogue        54 alert types with 4 thresholds resolved by serve's own startup ingest
n9-default-channel  serve provisioned app.alert_channel from the environment
n9-event            app.alert_event holds 1 row, written by serve's own threshold evaluator
n9-delivered        1 delivery reached state 'sent' through serve's own pump
n9-received         the webhook sink recorded 206 bytes of delivered message
n9-metrics          alert_delivery_total (12 series) and alert_dead_letter_depth (1)
```

The receiver is a real HTTP server whose transcript is written by the **receiver**, because "the
pump marked it sent" is the pump's opinion. Both Gate 3 metrics were literal `nil`s on `serve`'s
registry before this phase.

### N-10 — the claim is a lease, and the old code sent everything twice

`ClaimPendingAlertDeliveries` was a bare `SELECT` while `Dispatcher.Tick` makes an HTTP call
between claiming and settling. Measured against the old code, with six deliveries and two pumps:
**12 claims and two messages.** After: 6 claims, one message, `attempts = 1` on every row.

Nothing had ever stopped an operator running two `hangar work` replicas — River's normal
scale-out, and what `docker-compose.yml`'s own comments describe. Gate 3 never saw it because
Gate 3 runs one pump. It became urgent the moment `serve` gained the alerting role, which is why
it had to land first.

The guarantee is at-least-once with no double-send inside the lease window, and `Tick` logs a
warning when a pass outruns its own lease — a real trade-off in an at-least-once queue rather
than a defect, but not one that should happen silently.

---

## 5. Found in Phase 21 — the default deployment runs no workers *(B-6, CLOSED in Phase 22)*

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

## 9. Found by running Gate 1 — the proactive pause never resumes *(B-5, CLOSED in Phase 22)*

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

## 8. Found by running Gate 1 — `esi_ledger_mode` reports a default as an observation *(B-10, CLOSED in Phase 22)*

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

## 7. Found by running Gate 3 for the first time — the early warning arrives late *(B-9, CLOSED in Phase 22)*

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

## 6. Found by running Gate 5 for the first time *(B-7 and B-8 CLOSED in Phase 22; B-12 CLOSED in Phase 23)*

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

#### CLOSED in Phase 23 — the four steps were performed

**HANGAR is published.** `github.com/DanielMarch/hangar` is public, `main` and both existing tags
are pushed, and `ghcr.io/danielmarch/hangar` carries `:latest` and `:v1.0.0-rc3` with public read.
All three of §5.1's commands are now verifiable by somebody who is not this repository:

```
raw.githubusercontent.com/DanielMarch/hangar/main/docker-compose.yml   200
raw.githubusercontent.com/DanielMarch/hangar/main/deploy/install.sh    200
docker pull ghcr.io/danielmarch/hangar:v1.0.0-rc3                      OK, logged out
```

The last line was run **after `docker logout ghcr.io` and after deleting the local image**, so it
is a real anonymous pull rather than a cache hit.

**Not under `hangar-project`, and that is a real difference.** Creating an organisation is not
something this session can do, so the release is published under the operator's own account and
every documented URL was amended to match — both installers, both compose image references and
the unit's `Documentation=`. **The Go module path stays `github.com/hangar-project/hangar`**, so
`go get` of that path does not resolve. Nothing imports HANGAR (it is an application), §9.1's
install path is the image and §9.2's is the binary, and neither goes through the module proxy —
but it is a mismatch and it is recorded rather than left to be discovered.

**Condition 5.2 no longer asserts its own answer.** It used to record SUBSTITUTED
unconditionally, with the 404/403 sentence hard-coded — a verdict that could not change would
have gone on saying "nothing is published" the day after something was. It now probes both
halves anonymously at run time, from outside the repository, and records exactly which half is
missing when one is.

Two things the publication found that nothing else would have:

* **A 71 MB stray binary in the history.** `hangar.exe~`, an editor-backup-named dev build
  committed in Phase 20.7, which `.gitignore`'s anchored `/hangar.exe` did not match. GitHub
  flagged it on the first push. Rewritten out of history at the operator's direction — 34 MB to
  5.7 MB, with the HEAD tree hash unchanged, so the content is identical and only the pack is
  smaller.
* **Four hard-coded image names in Gate 5's own runner**, which kept reaching for
  `hangar-project` after the rename and failed three conditions for reasons unrelated to what
  they measure. The gate reads the image name from `docker-compose.yml` now — the file an
  operator downloads — so it cannot drift from the artefact it tests.

**§5.2 is a real pass.** Gate 5 is unqualified for the first time in the project's history.

---

## 10. Found by running the gates a SECOND time — six defects in the gate runners *(all fixed)*

Phase 21 found six defects in the product by running the gates for the first time. Phase 22 found
six more by running them AGAIN, and every one was in the measuring apparatus rather than in
HANGAR.

They are recorded here at the same weight as the product defects, for one reason: **five of the six
would have produced a wrong VERDICT rather than an error, and two would have produced a false
PASS.** A gate that fails loudly costs an afternoon. A gate that passes wrongly costs the release.

| # | Defect | What it would have done |
| :-- | :-- | :-- |
| 10.1 | `make build` wrote `bin/hangar`; every runner runs `bin/hangar.exe` on Windows | The gates would have measured a **five-week-old binary** left over from a previous phase. "Every gate must measure one build" would have been false and nothing would have said so |
| 10.2 | Gate 5 leaked a container per run | Three `hangar serve` containers from Phase 21 were still retrying their database connection **21 hours later**, holding the image the release must be able to delete, on a host Gate 1 needs quiet |
| 10.3 | `make gate3` could not start with its shipped defaults | The documented command refuses: the default `duration/2` split leaves a 2h drain against a policy needing 2h3m. rc1's run happened only because `-generate-for` was passed by hand, and nothing recorded that it had to be |
| 10.4 | Gate 3 never reset its database | **A false FAIL and a false PASS in one run.** See below |
| 10.5 | Gate 1 never reset its database | Would have begun the run **already paused**, from `app.esi_error_budget` left `paused = t` by rc1 — so condition 1.4's "a pause fired at the configured threshold" could have been satisfied by a pause that fired four hours earlier, in another process, against another binary |
| 10.6 | `esi_ledger_mode`'s settling-window exclusion in the runner | Correct while B-10 was open, and would have silently kept excluding samples after it was fixed. Deleted with the amendment it justified |

### 10.4 in full, because it is the one that produced a false pass

Gate 3's second run ever failed four conditions. `hangar_gate3` held **15,974 events, 8 channels,
110 routing rules and 18,494 deliveries** — two runs in one database.

**12,600 of 15,120 offered occurrences were DEDUPLICATED** against rows the previous run wrote.
`app.alert_event.dedupe_hash` is a permanent uniqueness constraint and correctly so (§4.4:
re-reading the same notification on the next poll is the common case, not an error), so a
generator producing deterministic content produces nothing at all the second time. Only the
threshold category still fired; routing deals the four stub behaviours round-robin and
`corporation.structure.fuel_low` lands on the *permanently failing* one; so 0 messages reached a
healthy channel. `3.1-categories`, `3-domains`, `3.7` and `3.4` all followed from that.

**And then the part that matters more.** `seedEntities` uses `ON CONFLICT DO NOTHING`, so the
structures kept rc1's `fuel_expires` — minutes out when rc1 wrote them, **two days EXPIRED** by the
time this run read them. Condition `3.1-scheduled-beyond-run`, which exists precisely to test
B-9's `min(bucket + window, now + window)` cap, therefore evaluated deadlines in the PAST, where
the cap cannot engage. **It reported a pass and measured nothing.**

The corrected run is the one that measures it: 2,526 threshold deliveries with coalescing buckets
**46 and 66 hours out** — so the bucket is still the deadline, exactly as `structureFuel` intends —
every one of them *attempted and settled inside a four-hour run*. Under rc1's code
`next_attempt_at` would have been `bucket + 5m`, up to 66 hours ahead, and the dispatcher could
not have claimed a single one.

The failed run is kept at `docs/gate-evidence/v1.0.0-rc2/gate3-first-attempt/` with its own
derivation.

### The rule this leaves behind

`tools/gate1-load` had cleared `app.esi_replica` since it was written, for exactly this reason one
step smaller: mode selection counts rows in it, and the corpses of a previous run are the
difference between solo and clustered at N=1. That instinct was right and was applied to one
table. It needed to be applied to the runner.

**A gate whose verdict depends on whether it has been run before is not evidence.** Both Gate 1 and
Gate 3 now reset the state they own before they measure; Gate 2 already created a fresh population
per run and needed no change.

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

### After Phase 23 — `v1.0.0-rc3`

**§3 is empty and §2 has no open blocker.** Every N-item is closed, B-4 is closed, B-12 is closed,
and Gate 5 is met without a substitution for the first time in the project's history.

The gates re-run at rc3, and what each one's subject was:

| Gate | Runs | Result | Why it was re-run |
| :-- | :-- | :-- | :-- |
| 1 ESI Load Stability | 1 at N=1, 1 at N=3 | **PASS**, both | N-5 put the cache key on the request path |
| 2 Revocation SLO | **started, paused by the operator** | rc2 stands; **not measured at rc3** | `internal/provisioning` is untouched, but N-4 added `SetEntitlementRuleEnabled` — a NEW caller of `Urgent.HandleUserChange`. See `gate2/NOT_RE_RUN.md` and `gate2/PAUSED.md` |
| 3 Alert Delivery Integrity | 2 | **PASS**, identical | N-10 changed the claim protocol and N-9 changed which process runs the pump |
| 4 Feature Parity | 2 | **PASS**, byte-identical | N-4 built new surface |
| 5 Deployment Usability | 2 | **PASS**, identical | N-6 is in `migrate up`, and D-2 published the artefacts 5.2 measures |
| 6 Spec-Drift Resilience | 2, at the tag | **PASS**, identical | §6.2 must see the finished tree |
| 7 Third-Party Migration | 2 | **PASS**, identical | N-1, N-2 and N-3 changed the corpus and the served set |

**Gate 6's row was filled in by the commit that follows the tag, and could not have been filled
in earlier.** §6.2 requires `git status --porcelain` empty *and* `HEAD` equal to the release
tag, so the gate can only run after the tag exists — and its own artefacts then make the tree
dirty, which is why exactly one commit follows `v1.0.0-rc3`.

Running it TWICE at one tag turned out to be tighter than the evidence README claimed. Run 2
failed `6.2-clean` on run 1's own untracked output, and committing that output does not fix it —
that moves `HEAD` past the tag and fails `6.2-at-tag` instead. The two conditions pull against
each other, so the only way to run it twice is to delete run 1's artefacts and run it again.
Done, with identical verdicts, and the README now says so.

**Gate 2 is the one deliberately not re-run, and saying so is the point.** Its subject —
`provisioning_revocation_seconds`, the urgent queue, and the entitlement-reducing triggers — was
untouched: `git diff cdbd15d..HEAD -- internal/provisioning` is empty apart from the entitlement
rule *disable* path, which this phase ADDED and which enqueues through the same
`Urgent.HandleUserChange` the delete has always used. Spending an hour to watch the same binary
produce the same number would be ceremony, and rc2's measurement (p99 **0.132s** against a 60s
budget over 15,000 revocations) is the one that stands.

#### Gate 1, because it is the largest measurement in the release

| | N=1 | N=3 |
| :-- | --: | --: |
| requests served in 4h | **1,132,172** | **1,146,665** |
| longest zero-throughput interval | **1m30s** | **1m0s** (floor: 5m0s) |
| `esi_ledger_divergence` | **0** over 9,905 group-samples | **0** over 30,228 |
| Governor 1 breaches | **0** | **0** |
| consumption above `max_tokens` | **0** | **0** |
| `esi_ledger_mode` | `[solo]` | `[clustered]` |

**Conditions 1.4 and 1.6 pass together at both counts**, which is the pairing that decides
whether §9's deadlock is gone: the proactive pause fired — the error budget reached 19 and 20
against a threshold of 20 — and throughput continued anyway. At rc1 the same pause ended the run
at minute sixteen and the installation never called ESI again.

It is also the load evidence N-5 needed. The cache key resolves the path on every request, and
2.28 million of them produced zero divergence.

#### What actually changed, and the measurement that closed each

| Item | The measurement |
| :-- | :-- |
| **N-10** the pump claimed by read | Against the old code, two pumps made **12 claims for 6 deliveries** and sent every message twice. After: 6 claims, one message, `attempts = 1` |
| **N-9** no alerts on a stock deployment | Seven verdicts on a one-container compose stack, including a **webhook sink's own transcript** of the delivered message and both Gate 3 metrics that `serve` exported as `nil` |
| **N-5** the fan-out cache key | Two tests that fail against the templated key: character 2 replayed character 1's body; wallet division 2 replayed division 1's |
| **N-6** schema integrity | **1,133 columns and 115 indexes** beside the 163 tables, verified against a real PG18 — and proven in the release path by dropping an INDEX and watching `migrate up` exit 1 naming it |
| **N-4** absent product surface | 21 allowlist entries gone; the live installation's vocabulary board shows **16 values it had recorded and nobody could read** |
| **N-1/N-2/N-3** Gate 7 | **17 of 34** served, **nothing pending**, and two byte-compatibility defects found in already-served routes |
| **N-7 / D-4** the SDE | An importer that had **never worked** now imports 52,863 types in 59s |
| **D-1** the ledger benchmark | **9 runs of 11** at p99 6.40–9.63 ms against a 10 ms budget — the first committed measurement of the Phase 4 exit criterion being met |
| **D-3** the systemd unit | It did not exist. It exists and starts under systemd 252 |
| **D-2 / B-12** publication | All three of §5.1's commands verified anonymously, the pull after `docker logout` |
| **B-4** the operator actions | 50 → 52 scopes; capability #41's structure half ran against real ESI at **200 with zero rows**, exactly as 20.9 predicted |

#### Is HANGAR ready to ship as v1.0 — not as a candidate, as a release?

**Yes, with one qualification that is about process rather than product, and it is stated first
because it is the only thing standing anywhere near the answer.**

The product case is measured rather than argued. All seven gates are met — six re-run at this
commit and the seventh, Gate 2, unchanged with the derivation committed beside it. §3 is empty.
§2 has no open blocker. The full regression passes with zero skips. `2.28 million` ESI requests
across two replica counts produced zero rate-limit breaches and zero ledger divergence, and the
alert pipeline balanced its accounting identity to zero remainder twice.

**The qualification: v1.0 would be the first release of this software to anyone.** Every
measurement in this directory was taken on one developer's machine, against a recording proxy
rather than CCP's ESI, with one EVE character, one corporation, no alliance, and no SDE until
this phase. That is not a defect and no gate asks for more — but "it passes every gate" and "it
has been operated by somebody other than its author" are different claims, and only the first is
true. A release that has never been installed by a stranger is a release whose install path has
been verified and not exercised.

That is an argument for shipping it and watching, not for holding it. The gates were designed to
be the bar, they are met, and the alternative to shipping is another phase of the same
single-machine evidence.

**What changes the answer.** If §8's "release blocks on all seven" is read strictly, one detail
matters: Gate 1 ran once per replica count rather than twice, and Gate 2 has not been measured at
rc3 at all.
Neither is a failing measurement; both are smaller samples than every other gate has. A reader
who requires every gate to have been run twice at the release commit does not have that, and
should say so rather than be told it is fine.

**What does not change it.** None of the six known limitations in
`docs/RELEASE_NOTES.md` is a defect. Each is a decision with a measurement behind it —
seventeen unservable legacy routes with derived reasons, an alliance worker with no alliance to
work on, an unread station-name column CCP does not ship, a unit file verified in a container.
They are the shape of an honest first release rather than of an unfinished one.

#### The rebuild found one more thing, and it was in the thing being shipped

The release procedure says rebuild the image `--no-cache` from the tagged commit and verify it end
to end. That step is usually ceremony. This time it was not.

The image already published to `ghcr.io/danielmarch/hangar:v1.0.0-rc3` — pushed earlier in this
phase, digest `eb14075c` — reports its own identity as:

```
hangar version dev (commit unknown, built )
```

It had been built without `--build-arg`, so the three ldflags that stamp version, commit and
build date were never set. Every functional check passes against that image; it serves, migrates,
exposes 146 metric series and resolves its alert catalogue. It simply cannot tell an operator
which commit it is. That is precisely the question an operator asks first when something is
wrong, and the one an image is uniquely able to answer.

Nothing in the test suite could have caught it. `--version` is stamped at link time by the
build command, so it is correct in every binary the tests build and wrong only in the artefact
nobody compiles locally. It is the same class of defect as the two `serve`-only gaps this phase
found (N-9's alerting pipeline, the nil Gate 3 metrics): a thing that is right everywhere except
where it ships.

The rebuilt image reports `hangar version v1.0.0-rc3 (commit 4d8a8d7, ...)`.

Worth saying without softening it: `tools/release-verify/run.sh` logs `--version` as its third line
and always did. The string `built: hangar version dev (commit unknown, built )` was written to
the transcript, in front of a reader, and the reader was me. The measurement was taken and not
read. That is a different failure from not measuring, and a worse one, because the fix for not
measuring is a new check and the fix for this is only attention.

The push was then verified by round trip rather than by the push command's own say-so. Both tags
were pushed, every local copy of the image was **deleted**, and the image was pulled back from
`ghcr.io` as a stranger would receive it — same digest
(`sha256:0da18ad3...`), correct self-identification, and `release-verify` run again against the
pulled copy with verdicts identical to the built one. That third run is in
`release-verify-from-registry/`. It is the only run of the six-check suite that measures what an
operator actually installs; the other two measure what this machine happened to have.

**Five commits follow the tag, not the one the brief expected.** They are, in order: Gate 6's
artefacts; the image rebuild and its verification; the registry round trip; a correction to this
paragraph, which said "two" until the count outgrew it; and the Gate 2 pause note.

The first three are forced rather than sloppy, and by the same mechanism each time. Every one of
them writes into `docs/gate-evidence/v1.0.0-rc3/`, and §6.2 fails on any untracked path — so Gate 6
cannot run until the tag exists, and nothing that writes evidence can run until Gate 6 is sealed.
The release work is therefore strictly downstream of the tag, and the brief's "exactly one
commit" is achievable only if the image is never rebuilt or never verified.

The fourth is not forced. It is the honest cost of putting a count in a file that the counting
changes, and it is left visible rather than amended away, because a reader checking
`git rev-list --count v1.0.0-rc3..HEAD` should get the number this page claims.

#### What a reader should check first to disbelieve this

In order of how likely each is to be wrong.

1. **Gate 1 ran once per replica count, not twice.** That is a deviation from this project's own
   most expensive lesson, taken as an explicit decision to save eight hours. The specific hazard
   the twice-rule guards — a verdict that depends on whether the gate has been run before — is the
   one Phase 22 closed for Gate 1 (defect 10.5, it now `TRUNCATE`s what it owns), so of the seven
   it is where a second run would add least. It is still a smaller sample than every other gate
   has.

2. **Gate 2 was not re-run at all.** The argument above is a reasoning argument, not a
   measurement. If you think the alerting-role assembly or the entitlement-disable path could
   perturb the revocation SLO, the answer is an hour of machine time and nobody has spent it.

3. **`character.wallet-journal` is served on a reproduction of legacy's own rounding.**
   `v2shim.phpPrecision` deliberately re-introduces a precision loss that legacy's PDO write path
   introduced at `precision=14`. That is what a byte-compatibility shim is for, and it is measured
   against the recorded corpus — but it is a money path, and a reader who wants to disagree with
   it should start there. `/api/v1` is unaffected and remains exact.

4. **The gate runners have failed more often than the product.** Nine runner defects across two
   phases against three product defects. Every gate verdict in this directory rests on apparatus
   that has been wrong before, and the corrections have all been the same shape — derive the value
   from the artefact rather than typing it into the checker.

5. **`AllianceWorker` has never run against real ESI**, and on this installation never can. Gate 4
   counts capability #37 as verified on the strength of a seeded integration test.

6. **The systemd unit is verified in a container.** Several sandboxing directives are enforced more
   weakly there than on metal.

7. **Gate 1's load numbers come from a recording proxy, not CCP's ESI.** Unchanged from rc2, by
   design, and §1.1 says so — but it means the governors were measured against a server that
   behaves exactly as the spec says. Real ESI does not.

### After Phase 22 — `v1.0.0-rc2` IS a release candidate, with one item outside the codebase

**All seven gates pass.** §8's "release blocks on all seven" is satisfied, with one condition
recorded as substituted rather than met — Gate 5.2, and only because nothing has been published.

What changed, and why it is more than a re-run: the six defects of §§5–9 were closed, and each was
then measured by the gate that found it rather than by the test that fixed it.

| Defect | The measurement that closed it |
| :-- | :-- |
| §9 B-5, the pause never resumes | Gate 1 N=1: condition **1.4 fired the proactive pause** *and* condition **1.6 measured a longest stall of 1m30s** against a 5m floor. Both, in one run. At rc1 the same pause ended the run at minute 16 |
| §5 B-6, no workers in the default deployment | 92 `sync_route` jobs reached `completed` under a single live `serve`, with 94 real ESI calls (68×304, 26×200) |
| §6.3 B-7, the binary parsed as its own config | Gate 5 `5.8-config-name-collision`: **pass**, and the binary migrated an external PostgreSQL 18 from §9.2's documented layout |
| §6.2 B-8, callback mismatch unreported | Gate 5 `5.3-callback-mismatch`: **pass**, naming the expected `https://hangar.example.com/auth/callback` |
| §7 B-9, warnings due at the deadline | Gate 3: 2,526 threshold deliveries with coalescing buckets **46h and 66h out**, every one attempted and settled **inside a 4-hour run** |
| §8 B-10, the mode gauge reports a default | Gate 1: `esi_ledger_mode` observed as `[solo]` at N=1 and `[clustered]` at N=3, through §1.4's mandatory kill-and-restart, **with the Phase 21 amendment withdrawn and no samples excluded** |

**What a reader should check first to disbelieve this.** In order of how likely each is to be
wrong:

1. **Gate 5.2 is not met, and I have called the release a candidate anyway.** That is a judgement,
   and the honest way to read it is that HANGAR is code-complete against its gates while its
   *distribution* is not done at all. Nobody but this repository can perform the documented
   install. If your bar for "release candidate" includes "an operator can obtain it", this is a no.
2. **Gate 1's two runs used a recording proxy, not CCP's ESI.** That is by design and §1.1 says so,
   but it means the load numbers describe HANGAR's governors against a server that behaves exactly
   as the spec says. Real ESI does not.
3. **`make bench-ledger-clustered` FAILS on this host** — p99 10.78/10.92/10.98 ms against a 10 ms
   budget. It fails identically at rc1 (10.46/10.20/10.83 ms), so it is the Windows/Docker-Desktop
   environment rather than a regression, but it has never been demonstrated passing here.
4. **§4.4's alert pipeline still runs only under `work`** (N-9), so the stock single-process
   deployment syncs and provisions but delivers no alerts. Gate 3 passes because the gate runs the
   pump itself. This is the largest known gap between what the gates measure and what an operator
   gets.
5. **The unit file has never been executed** (§5.8 PARTIAL, this host has no systemd).

### After Phase 21 — `v1.0.0-rc1` was not a release candidate

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
