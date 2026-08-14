# Production-caller audit — Phase 20

**Question asked of every subsystem:** *does any non-test file, outside this package, actually
call this code on a path reachable from `main`?*

This is the audit SRS §0's B20/B22/B23 entries describe, run exhaustively rather than by
inspection. It is a **Gate 4 prerequisite**: Gate 4.1 requires 58 capability rows all at
`status = verified`, and a capability whose implementation is unreachable from `cmd/hangar` is
not delivered, however green its own package tests are.

---

## Method

Two passes, both mechanical and both reproducible.

### Pass 1 — package reachability

```bash
comm -23 <(go list ./... | sort) <(go list -deps ./cmd/hangar | grep hangar-project | sort)
```

A package absent from the binary's transitive imports cannot execute in production under any
input. This is the strongest form of the finding and admits no false positives.

### Pass 2 — function reachability (RTA)

```bash
go run golang.org/x/tools/cmd/deadcode@latest -test=false ./cmd/hangar
```

Rapid Type Analysis over the call graph rooted at `main`. Unlike a grep, it is not fooled by a
symbol that appears only inside a doc comment — which is exactly how B22 and B23 hid, and how
every finding below hid.

Every finding in the tables below was then **confirmed by hand**: grepping the symbol across all
non-test `.go` files and checking each hit is a real call rather than prose. Findings where the
only hits were comments are marked *comment-only*, which is the signature of this defect class.

### Reproduction

```bash
go run golang.org/x/tools/cmd/deadcode@latest -test=false ./cmd/hangar
```

124 unreachable functions at commit `7a74e5d`.

---

## Pass 1 result — packages with no production caller at all

| Package | Status |
| :-- | :-- |
| `internal/sde` | **B22**, already recorded. Whole import pipeline. |
| `internal/i18n` | **B23**, already recorded. Not merely `ResolveESILanguage` — the *package* is absent from the binary, so `UILocales()` (the closed locale set the SRS says Phase 15 middleware validates against) has no caller either. |
| `internal/esi/pagination` | **NEW — B31.** See below. |
| `tools/gen-permission-seed` | Not a defect: a standalone generator `main`, correctly excluded. |

---

## Pass 2 result — subsystems built, tested, and unreachable

Grouped by the gate each one blocks. "T" means the symbol has passing tests and no production
caller — the exact B20 pattern.

### Blocking Gate 1 (ESI Load Stability)

| Defect | Evidence |
| :-- | :-- |
| **B29 — ESI rate-limit header reconciliation never runs.** `ratelimit.ClassifyResponse`, `ParseRemaining`, `ParseRateLimitLimit` are all unreachable [T]. `esi.Client.Do` calls only `ratelimit.ClassifyCost`. | `ClassifyResponse` appears in non-test source exactly once, in a doc comment on `esi.Client.TTLFloor` describing behaviour that does not happen. Gate 1.3 (divergence ≤ 1), Gate 1.4 (proactive pause), and the `429`-headerless / `Retry-After` / server-reports-lower / server-reports-higher adversarial rows in §1.3 have no live implementation to exercise. |
| **B28 — the entity circuit breaker never runs.** `breaker.NewEntityBreaker`, `EntityBreaker.{Allow,RecordSuccess,RecordFailure,State}` unreachable [T]. | §1.3's "5 consecutive 403s on one entity → entity breaker opens; acting character re-elected" has no implementation on the live path. `internal/sync/worker` records a 403 against the acting character in the store, which re-elects, but no breaker opens. |

### Blocking Gate 2 (Revocation SLO)

| Defect | Evidence |
| :-- | :-- |
| **B27 — token lifecycle invalidation never runs.** `sso.NewLifecycle`, `Lifecycle.InvalidateForOwnerHashChange`, `Lifecycle.InvalidateForInvalidGrant` unreachable [T]; `scopes.NeedsReauthorization`, `MergeScopes`, `NewSet`, `Set.{Has,Missing}` unreachable [T]. | Gate 2.3's trigger matrix requires *every* reducing event to enqueue an urgent job. Rows 1–3 — token invalidated (`invalid_grant`), `owner` hash change, scope set reduced — have no producer at all. |
| **B26 — the RBAC mutation surface has no caller.** `rbac.{CreateRole,AssignUserRole,RevokeUserRole,AddRoleGrant,AddSquadRole,AddSquadMember}` and `rbac.Resolve` unreachable [T]; no role-management route is registered in `internal/api/v1/router.go`. | Trigger-matrix rows "RBAC role revoked" and "Squad membership removed" have no producer. It also contradicts `cmd/hangar/serve.go`'s own comment that "§4.9's outbox is written by internal/rbac's mutations" — those mutations never execute, so on a running installation the outbox has no writer. |

### Blocking Gate 3 (Alert Delivery Integrity)

| Defect | Evidence |
| :-- | :-- |
| **B25 — the alert pipeline delivers but never generates.** `alerting.Emitter.{IngestNotification,Emit}` unreachable [T]; `alerting/render.{Render,HasTemplate}` unreachable [T]; the dedupe surface (`Fingerprint.Hash`, `NotificationFingerprint`, `ThresholdFingerprint`, `SemanticFields`) unreachable [T]; coalescing (`NewCoalesceKey`, `CoalesceKey.{String,Due}`) unreachable [T]; `DeadLetterCount` unreachable [T]; `alerting/catalogue.{Thresholds,ThresholdSourceRoutes,ValidateThresholds,Names,CountByDomain,SeededTotal,SkyhookNames}` unreachable [T]. | `alerting.Dispatcher` (the outbox pump) **is** wired, by `cmd/hangar/alerting.go`. So delivery runs and nothing produces anything to deliver. Gate 3.1's accounting identity `generated == delivered + coalesced_into + dead_lettered + suppressed_by_dedupe` has no left-hand side; a 4-hour run drops zero alerts because zero alerts exist. Gate 3.2 (generic fallback renderer), 3.3, 3.4 (40 coalesced events render as one) and 3.6 (dedupe hashes stable across restart) each test a component with no caller. |

### Blocking Gate 4 (Feature Parity)

| Defect | Evidence |
| :-- | :-- |
| **B30 — ESI route handlers that are never dispatched.** `handlers.{SyncCalendarEventDetail,SyncCalendarAttendees,SyncMarketHistory,SyncMarketPrices,SyncPlanetColonyDetail,SyncCorporationProjectContributors}` and their `Parse*` counterparts — 13 functions — unreachable. | Gate 4.2 requires every one of the 106 measured distinct ESI routes to map to a live `app.sync_subscription` route or be recorded as deliberately unmapped. These are neither: they are implemented and never scheduled. |
| **B23** (recorded) — `internal/i18n` absent from the binary. | Gate 4.5 requires all 9 UI locales "each resolving to a valid ESI `Accept-Language`". The resolution runs only in tests. |
| **B22** (recorded) — `internal/sde` absent from the binary. | The `sde.*` tables can only ever be empty. |
| **B32 — entitlement-rule write endpoints are not registered.** `v1.CreateEntitlementRule`, `v1.DeleteEntitlementRule` unreachable [T], with tests in `admin_rules_test.go`. `RegisterAll` never mounts them. | Gate 2's trigger row "Entitlement rule deleted or narrowed" and Gate 4.3's controller mapping both depend on a surface that returns 404. |
| **B33 — `$filter` validation never runs.** `filters.New`, `filters.Validate` unreachable [T]. | The filter grammar is validated only in its own tests. |

### Blocking Gate 5 (Deployment Usability)

| Defect | Evidence |
| :-- | :-- |
| **B24 — §4.9 has no configuration surface.** `crypto.SealWebhookSecret`, `crypto.NewWebhookSecret` unreachable [T]. | Already known and in scope for this phase. `app.webhook_endpoint` rows can be created only by direct SQL, and the HMAC secret is envelope-encrypted with AAD bound to the endpoint's own uuid. |
| **B34 — the Redis L2 tier is never constructed.** `cache.NewRedisL2`, `redisL2.{Get,Set}` unreachable [T]. | `docker-compose.yml` ships a `cache` profile whose service, when enabled, is not used by anything. Principle 7 says Redis must be optional; it does not say the option should be inert. |
| **B35 — the TeamSpeak challenge flow has no caller.** `teamspeak.{IssueChallenge,RedeemChallenge}` unreachable [T]. | TeamSpeak identity linking cannot be completed on a running installation. |

### Not gate-blocking, but the same pattern

| Defect | Evidence |
| :-- | :-- |
| **B31 — `internal/esi/pagination` is a dead duplicate.** The whole package is absent from the binary. | `internal/sync/worker` carries its own complete page-walker with torn-set detection, and it is the one that runs. The two disagree: the dead one fans out at `MaxPageConcurrency = 4` (which is what 01_ARCHITECTURE.md §5.9 specifies), the live one is **serial**; the dead one's `detectTornSet` ignores a page with no validator, the live one treats a missing validator as torn, which is **stricter**. The live behaviour is the safer of the two but is not the specified one, and the cursor mechanism (`CursorQuery`, `FetchAllCursor` — §5.9's other pagination mode) has no live implementation anywhere. Resolve by making one of them the single implementation, not by deleting the evidence. |
| **B36 — the metric surface does not exist.** `telemetry.NewRegistry` has no caller and no `/metrics` endpoint is served. Every metric named in 04_RELEASE_GATES.md's instrumentation table — `esi_ledger_divergence`, `esi_ledger_mode`, `esi_replica_live_count`, `esi_429_total`, `esi_420_total`, `esi_error_limit_remaining`, `provisioning_revocation_latency_seconds`, `alert_delivery_total`, `alert_dead_letter_depth`, `esi_429_headerless_total` — appears **only in comments**. | SRS §0 already assigns the metric surface to Phase 20, so building it now is in scope. But 04_RELEASE_GATES.md's preamble says the opposite — "each gate's *instrumentation* lands in the phase that builds the subsystem. A gate whose metrics are added in Phase 20 cannot be measured retroactively" — and names phases 4, 11 and 14 as the owners. **That is a specification contradiction between the SRS and the gate document**, and it has to be resolved explicitly rather than by picking the convenient reading. Recorded here per Principle 15. |

---

## What this means for Gate 4

Gate 4.1 requires 58 capability rows at `status = verified`. The brief for this phase anticipated
two capabilities being un-delivered. The audit finds that the shortfall is broader: alert
generation, RBAC mutation, token-lifecycle invalidation, six ESI route handlers, SDE import,
locale resolution, entitlement-rule writes and TeamSpeak linking are each implemented, tested,
and unreachable from `main`.

**Gate 4 cannot be signed off in this state**, and per §4.2 a capability with no delivering phase
is a specification defect that blocks the gate rather than something to be marked verified with a
note. The traceability CSV is still worth producing — it is the artefact that makes the shortfall
countable — but it will carry `status = unreachable` rows, and that is a gate failure, correctly
recorded, not a gate pass.

Per rule §0.4, wiring these up during Phase 20 and then running the gates does not convert them
into passes either: "a gate that requires a code change to pass is a failed gate, not a fixed
one." The wiring is still worth doing — an inert subsystem should not stay inert — but the gate
result that follows it is evidence about the *next* release candidate, not this one.

---

## Closure register — Phase 20.2

Recorded here rather than by editing the findings above, so this document keeps meaning "what
the audit found" and not "what is currently broken". Each row names the wiring, not the
intention.

| Defect | Closed by | Verified by |
| :-- | :-- | :-- |
| **B23** — `internal/i18n` absent from the binary | `HANGAR_LOCALE` (installation-wide, default `en`), validated at boot by `internal/config.Validate` against `i18n.UILocales()`, resolved by `cmd/hangar`'s `buildGateway` into `esi.Client.Language` — which now also SENDS `Accept-Language`, which it never had | `TestNoPackageIsAbsentFromTheBinary`; `TestLocaleIsValidatedAtBoot`; `TestEveryUILocaleIsAcceptedByValidate`; `TestResolvedLanguageIsSentAndKeyed` |
| **B28** — the entity circuit breaker never runs | `esi.Client.EntityBreaker`, consulted per `(route, EntityID)` in `Do`, failing only on 403 and resetting only on 2XX/3XX; workers pass the character or corporation id | `TestEntityBreakerIsScopedToTheEntity`; `TestEntityBreakerIgnoresStatusesThatSayNothingAboutAuthorisation`; `TestEntityBreakerOpensAfterFiveConsecutive403s` (integration, against the Gate 1 proxy) |
| **B29** — rate-limit header reconciliation never runs | `Client.Do` calls `ratelimit.ClassifyResponse` once; reconciliation, the 429 snooze and the headerless signal all read its `Outcome`. `IncrementErrorBudget`/`ResetErrorBudgetWindow` DELETED as superseded races, not wired | `TestReconcileUsesTheServersOwnCeiling`; `TestReconcileFallsBackToTheUNREDUCEDCeiling`; `TestAbsentRemainingHeaderIsNotAReadingOfZero`; `TestClientReconcilesAgainstServerHeaders`; `TestHeaderless429SnoozesAndCounts`; `TestRetryAfter429SnoozesForExactlyThatDuration` |
| **B31** — `internal/esi/pagination` is a dead duplicate | It is now the single implementation. Concurrency 4 adopted (the spec's value); the torn-set check tightened to the live, stricter reading; the cursor walker implemented and wired to `/corporations/{id}/projects` | `TestNoPackageIsAbsentFromTheBinary`; `TestMissingValidatorMidSetIsTorn`; `TestNoValidatorAnywhereIsNotTorn`; `TestFetchAllPagesFansOutAtFour`; `TestFetchAllCursorPagesFollowsTheCursor` |
| **B39** — blank unauthenticated page | **Not reproducible.** A root `errorComponent`/`notFoundComponent` added regardless — its absence is what made any throw on the unauthenticated path render nothing at all | manual, cold browser context; see the phase report |
| **B40** — a fresh SSO user holds zero permissions | `rbac.BootstrapFirstAdmin`, called from the SSO callback's `OnUserAuthenticated` hook, gated on `CountSuperuserHolders() == 0` inside one transaction | `TestFirstLoginPromotesExactlyOneAdministrator`; `TestBootstrapRespectsACuratedAdminRole`; `TestBootstrapIsSuppressedByADeniedSuperuser` |
| **B26** — the RBAC mutation surface has no caller | **Surface half closed early** (B40 could not be fixed without it): `internal/api/v1/admin_roles.go` registers role create/delete/get, per-grant add/remove, user-role assign/revoke, the permission vocabulary and `GET /api/v1/me/permissions`; the squad endpoints now go through `internal/rbac`'s wrappers instead of the raw queries | both reachability guards; `admin_roles.go`'s routes appear in `docs/openapi.json` |

**Two symbols from B26 are RECLASSIFIED rather than closed.** `rbac.Resolve` and
`rbac.ResolveLive` remain unreachable and have moved to the allowlist's "not defects" section.
Production resolves in BULK — `RefreshUser` → `ResolveAllLive` → `ResolveAll` — because
materialising one user writes the whole closed set in one pass. The single-permission forms are
the truth-table test surface and the documented materialisation cross-check; the middleware
deliberately never calls them, because it reads the materialised row and a second live path could
disagree with what is about to be enforced. `GetUserByMainCharacterID` likewise stays: nothing in
HANGAR looks a user up by their main character, and inventing an endpoint to give a query a caller
would be the allowlist wagging the codebase.

### Findings made DURING 20.2, not by the original audit

The pattern of the last five phases held: running the system found what running the tests could
not.

| Finding | Evidence | Disposition |
| :-- | :-- | :-- |
| Every deliberate gateway refusal reached River as a FAILED JOB | Governor 1 exhaustion, a Governor 2 pause and both breakers all returned a plain error from `Client.Do`, which River records as a failure and retries on its own backoff — the exact inverse of §5.5's "the caller snoozes the subscription; it does not spin" | **Fixed in 20.2.** `internal/sync/worker/unavailable.go` |
| `PATCH /api/v1/admin/users/{id}` silently dropped `is_active` and `is_admin` | Both declared in `UpdateUserIn` since Phase 15, both advertised in the OpenAPI document, neither read by the handler. The endpoint answered 200 and changed nothing | **Fixed in 20.2** |
| Squad membership/role writes bypassed `internal/rbac` | The endpoints called the raw generated queries, so `app.effective_permission` was never recomputed and the §4.9 outbox row was never written. Adding somebody to a role-granting squad changed no permission they actually held | **Fixed in 20.2** |
| Two squad handlers discarded their errors | `_ = deps.Store.AddSquadRole(...)` reported success for a save that had failed — on a permission-granting operation | **Fixed in 20.2** |
| The ESI cache key omits the compatibility date | `cache.KeyInput` declares `CompatibilityDate` and `esi.Client.cacheKey` never populates it, so advancing the pin does not invalidate a single cached body. §5.3's formula names the field | **NOT fixed. Recorded for 20.3.** `Client` has no pin source, and threading one in invalidates the whole cache — which 20.2 is already doing once for B23. Doing both in one release makes the second invalidation invisible |
| `/corporations/{id}/projects/{project_id}/contributors` is parsed with the wrong DTO | B38 renamed the path from `.../contributions` and kept the `contributions` parser. The spec's `CorporationsProjectsContributors` is an OBJECT of `{contributors, cursor}` whose elements are `{id, name, contributed}`; `ParseCorporationProjectContributions` expects a bare array of `{amount, character_id}`. It has never failed because the route is a fan-out path and therefore not subscribable, so it has never executed | **NOT fixed. Recorded for 20.5**, which owns B30 (route handlers that are never dispatched). Fixing it properly needs a decision about what `contributed` maps to in `app.corporation_project_contribution` |
