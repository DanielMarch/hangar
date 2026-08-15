# Appendix C — `/api/v2` → `/api/v1` migration guide

**Audience:** you maintain something that talks to a SeAT `/api/v2` endpoint and you are moving
to HANGAR. This tells you what keeps working, what changed, and what broke on purpose.

**Status of the shim:** read-only, deprecated from the day it shipped, and **removed on
2027-08-31**. Every `/api/v2` response carries `Deprecation: true` and an RFC 8594 `Sunset`
header with that date, plus a `Link` header pointing here.

---

## 1. The one-paragraph version

Point your client at `/api/v2` on HANGAR with a HANGAR API token in the `X-Token` header you
already use, and the read routes that are shimmed answer byte-for-byte what SeAT answered.
Anything that writes, anything about roles, and anything keyed by a SeAT `users.id` or
`squads.id` does not work and will not — those are covered below with what to use instead.

---

## 2. Authentication

**Legacy:** `X-Token: <64-char token>`, plus an IP allowlist on the token row.

**HANGAR:** `Authorization: Bearer <token_id>.<secret>`, minted with
`hangar admin bootstrap-token` or through `POST /api/v1/admin/tokens`.

**On `/api/v2` the shim accepts either header.** `X-Token` is an alias for the Bearer scheme and
nothing more: the *value* must still be a HANGAR credential in the `<token_id>.<secret>` form.
A legacy SeAT token is not accepted, because HANGAR has no such tokens.

That trade is deliberate. Requiring every client to change its auth header on day one is not much
of a migration aid; accepting a weaker credential would be worse. So what a migrating client
changes is a **config value**, not its source. Under the hood the alias rewrites the header into
the Bearer form before the single credential path runs — same hash lookup, same revoked/expired
check, same permission cap. There is no second authenticator.

The alias works on `/api/v2` only. On `/api/v1`, use `Authorization`.

### Tokens are scoped, and the scope is a cap

This is new and it will surprise you if you skip it. A HANGAR API token carries its own
`permissions` array. A request is allowed only when the permission is in **both** the owner's
roles **and** the token's own scope. A token scoped to `corporations.view` gets `403` on a
character route even if its owner is a superuser.

Legacy had no such thing — a valid `X-Token` was valid for everything. If your integration only
reads corporation wallets, scope its token to that; the shim will not widen it for you.

### The IP allowlist is gone

Legacy pinned a token to `allowed_src`. HANGAR does not. Use network controls, and revoke tokens
you no longer need (`POST /api/v1/admin/tokens/{id}/revoke`).

---

## 3. Money — read this before you compare numbers

Legacy emitted money as a JSON **number**. HANGAR emits a **string** on `/api/v1`
(`"1234567890.12"`), because money is `NUMERIC(30,2)` all the way down and no `float64` exists on
any money path.

The shim converts back to a number, so shimmed responses look like legacy's. That conversion goes
through `float64` and loses precision above 2⁵³.

**What is usually said about this is that the shim *introduces* IEEE-754 imprecision. That is not
what we measured.** Legacy's own columns are MySQL `DOUBLE`:

```
mysql> SELECT amount FROM character_wallet_journals LIMIT 1;
9.007199254741e15
```

A wallet entry of `9007199254740993.01` ISK was **already** `9007199254741000` in legacy's
database, before anything was serialised — about 7 ISK gone at rest, not on the wire.

So the honest framing:

| | precision |
| :-- | :-- |
| legacy `/api/v2` | `double` — inexact above 2⁵³, at rest and on the wire |
| HANGAR `/api/v2` shim | reproduces legacy's precision exactly |
| HANGAR `/api/v1` | exact decimal, as a string, always |

**If you care about exact ISK, the shim is not the problem and never was — legacy was.** Move to
`/api/v1` and parse the string with a decimal type.

### Worked example

A corporation wallet holding `9,007,199,254,740,993.01` ISK:

| Surface | Response fragment | Value received |
| :-- | :-- | :-- |
| `/api/v1` | `"balance":"9007199254740993.01"` | exact |
| `/api/v2` shim | `"balance":9007199254741000` | 1.01 ISK low |
| legacy SeAT | `"balance":9007199254741000` | 1.01 ISK low |

The last two rows are identical. That is the point of the shim, and it is also why the shim
cannot fix this for you.

---

## 4. Route mapping

| Legacy controller | Status | HANGAR equivalent |
| :-- | :-- | :-- |
| `AllianceController` | **shimmed** | `/api/v1/alliances/*` |
| `CharacterController` | **partly shimmed** | `/api/v1/characters/*` |
| `CorporationController` | **partly shimmed** | `/api/v1/corporations/*` |
| `KillmailsController` | **not yet shimmed** | `/api/v1/{characters,corporations}/{id}/killmails` |
| `RoleController` | **410 Gone — breaking** | `/api/v1/admin/roles`, `/api/v1/admin/scopes` |
| `RoleLookupController` | **410 Gone — breaking** | `/api/v1/admin/users/{id}`, `/api/v1/me` |
| `SquadController` | **501 — not translatable** | `/api/v1/squads/*` |
| `UserController` | **501 — not translatable** | `/api/v1/admin/users`, `/api/v1/me` |
| `ApiController` | n/a | framework base class, no routes |

Shimmed today, byte-identical to recorded legacy responses:

```
GET /api/v2/alliance/contacts/{alliance_id}
GET /api/v2/character/contacts/{character_id}
GET /api/v2/character/corporation-history/{character_id}
GET /api/v2/corporation/contacts/{corporation_id}
GET /api/v2/corporation/sheet/{corporation_id}          [added in Phase 20.6]
```

### The per-route classification is the authority **[Phase 20.6]**

`internal/api/v2shim.Classification()` names **every** legacy read route with one of four
statuses and, for the three that are not `served`, the reason. The router derives every response
from it — there is no second list, and no path falls through to a generic answer.

| Status | HTTP | Meaning |
| :-- | :-- | :-- |
| `served` | 200 | Shimmed **and** byte-identical to its recording. A route that returns plausible JSON but does not match those bytes is not served. |
| `pending` | 501 | Shimmable, not yet shimmed. Unfinished work, recorded as unfinished. |
| `unshimmable` | 501 | Cannot ever be byte-compatible — HANGAR's identifier space differs from legacy's. |
| `breaking` | 410 | The underlying model changed; the concept no longer exists. |

`pending` and `unshimmable` share a status code and carry **different bodies** on purpose: "wait
for a release" and "rewrite your integration" are different instructions, and before 20.6 both
answered the same generic sentence.

**The route count was wrong.** This document and the shim's own comments said legacy `/api/v2`
has 33 read routes. Measured from `testdata/legacy-api-v2/MANIFEST.json`, it is **34**: 32
distinct recorded route patterns (34 recordings, two of which are a second capture of the same
path — a page-2 and an empty-set case) plus the two role routes, which were never recorded
because they are breaking. The count is now derived in the test rather than asserted — the same
correction B6 applied to the Phase 0 baseline.

Three `pending` reasons are worth naming here, because they are not "nobody wrote it yet":

* **`character.sheet`** carries `user_id`, a legacy MySQL integer with no honest HANGAR value
  (see §7), *and* `skillpoints.total_sp` / `unallocated_sp`, which HANGAR **parses from ESI and
  discards** — `CharacterSkillsDTO` reads both on every skills sync and `SyncCharacterSkills`
  writes only the per-skill rows, so no column holds either.
* **`character.wallet-transactions`, `corporation.wallet-journal`, `corporation.wallet-transactions`**
  lead with SeAT's own MySQL auto-increment key (`id` / `internal_id`), a SeAT-internal surrogate
  HANGAR has no column for — the same class as `attacker_hash` in §7.
* **`character.wallet-journal`** is blocked on a measured conflict, not on missing code: the
  recording's `"amount": 9007199254741000` is a *different float64* from the value its own
  fixture seeds (`9007199254740993.01` parses to `9007199254740994`). Legacy's loss is
  MySQL's/PHP's 14-significant-digit rounding, not IEEE-754 nearest, so the shim's `Money()`
  round-trip — which reproduces IEEE-754 nearest — cannot produce those bytes. Every other double
  in the corpus is short enough that the two rules are indistinguishable, so this is the only
  value that could have revealed it. Resolving it means re-running the PHP recorder with
  `serialize_precision` instrumented.

The remaining routes are shimmable — keyed by EVE identifiers HANGAR stores unchanged — and need
a full-set store query plus corpus fixtures. Track them rather than designing around them.

### Write routes are not shimmed, and answer 501 rather than 404

`POST`, `PUT`, `PATCH` and `DELETE` under `/api/v2` all return `501 Not Implemented` with an
explanation. A `404` would send you looking for a typo instead of reading this page.

---

## 5. The breaking changes, and why

### 5.1 Roles and role lookups — `410 Gone`

Legacy roles carried permission *titles* plus per-grant JSON `filters`, attached directly to users
and squads. HANGAR resolves a **closed** permission vocabulary through roles into a materialised
effective-permission set per user, with an explicit allow/**deny** effect.

A partial shim here would not return incomplete data — it would return a **wrong answer about who
can do what**, to a caller whose reason for asking is to make an access decision:

* legacy `filters` have no representation, so the shim would have to drop them, turning a
  narrowly-scoped grant into an unrestricted one;
* HANGAR's `deny` effect has no legacy counterpart, so a role that *revokes* a permission would
  serialise as one that grants nothing.

Both failures are silent. Hence `410`, with a pointer.

`GET /roles/query/permission-check/{character_id}/{permission_name}` has a third problem: it asks
about a **character**, and HANGAR materialises permissions per **user**, who may hold many
characters. There is no correct value to return.

**Migrate to:** `/api/v1/admin/roles`, `PUT /api/v1/admin/scopes` (atomic whole-grant-set
replacement), `GET /api/v1/admin/users/{id}`, `GET /api/v1/me`.

### 5.2 Users and squads — `501`, and a correction to the specification

SRS Appendix C lists only `RoleController` and `RoleLookupController` as breaking. **That is
incomplete.** `UserController` and `SquadController` are equally un-shimmable:

| | legacy | HANGAR |
| :-- | :-- | :-- |
| `users.id` | `bigint` auto-increment | `app.user.user_id` — `uuid` |
| `squads.id` | `bigint` auto-increment | `app.squad.squad_id` — `uuid` |

`"id":1` and `"id":"019ff31f-f91f-752b-814b-6e72a27c1fb6"` are not a formatting difference; they
are different identifier spaces. No translation invents the integer your client stored, and
emitting a uuid where an integer was promised breaks the client just as thoroughly as a 501 does,
only later and more confusingly.

They answer `501` rather than `410` on purpose: the grant-model break is conceptual and permanent,
whereas this one could be addressed by a future release exposing a stable legacy-id mapping.
Saying "Gone" would overstate it.

**Migrate to:** `/api/v1/admin/users`, `/api/v1/me`, `/api/v1/squads/*`, and store HANGAR's uuids.

---

## 6. Response shape differences

### 6.1 The `_sync` envelope is stripped

Every `/api/v1` collection carries a `_sync` block (data freshness, and whether an ESI route is
blocked by the compatibility pin). Legacy `/api/v2` has no such key and the shim **removes** it
rather than passing it through.

You lose real information by staying on the shim. `_sync.stale` and `_sync.blocked_by_pin` are how
you tell "there is no data" from "we could not fetch data", which legacy could not express at all.

### 6.2 Pagination

The shim synthesises Laravel's envelope exactly: `data`, `links` (`first`/`last`/`prev`/`next`),
`meta` (`current_page`, `from`, `last_page`, `path`, `per_page`, `to`, `total`), 15 rows per page,
`?page=N`.

**`total` is computed exactly, not approximated.** Byte-compatibility forces it: `total` is
recorded in the corpus, and `last_page`, `from` and `to` are all derived from it, so an
approximation would corrupt four fields rather than one. The cost is a real count per request, and
the mitigation is migrating off the shim rather than tuning the shim.

**Deep pages are slower than `/api/v1`.** HANGAR prohibits `OFFSET` outright (SRS §6, §17
invariant 10; `sqlc vet`'s `no-offset` rule enforces it at build time), so the shim reads an
ordered page window and slices it. Page 1 — what almost every legacy client asks for — reads
exactly what it returns. Deep pages read more rows than they return, exactly as an `OFFSET` scan
would have.

> **Specification note.** The roadmap asks the shim to synthesise `?page=N` pagination; the repo
> prohibits `OFFSET`. Those are in genuine tension. The invariant won, being the older and broader
> commitment, and the shim is the temporary surface.

`/api/v1` uses opaque keyset cursors (`after`/`before`, `limit` 10–100) which do not drift under
concurrent writes and do not degrade with depth.

### 6.3 Timestamps

Legacy: `"2026-07-15 20:00:00"` — no `T`, no offset, always UTC. `/api/v1`: RFC 3339.
The shim emits legacy's format.

### 6.4 Empty collections

`"data":[]`, never `"data":null`, with `"from":null`, `"to":null`, `"total":0`. An empty result
and a failure must not look alike.

### 6.5 Errors

Legacy error bodies are a **bare JSON string**, not an object — `"Unauthorized"`, not
`{"error":"Unauthorized"}`. The shim reproduces that. `/api/v1` uses RFC 7807 problem details.

One difference: the shim can return `403 Forbidden` where legacy never did, because HANGAR's
tokens are scoped and legacy's were not. The body stays a bare string.

### 6.6 `$filter` (OData) is refused, not ignored

Legacy accepted a `$filter` query parameter on most collection routes. The shim **rejects it with
`400`** rather than ignoring it.

Ignoring it would be dangerous in the one direction that matters: `$filter` *narrows* a result set,
so a client asking for "contacts where standing < 0" and silently receiving *every* contact has
been handed more data than it asked for — and, if it displays that result, possibly more than it
intended to expose. A loud `400` costs you a code change you were going to make anyway.

**Migrate to** `/api/v1`'s filter specification.

---

## 7. Fields the shim cannot reproduce

Byte-compatibility is verified against recorded legacy responses
(`testdata/legacy-api-v2/`, recorded from real Laravel 10 against the real eveapi schema). Three
things in those bytes have no HANGAR source, and are recorded here rather than hidden:

| Field | Where | Why |
| :-- | :-- | :-- |
| `map_id`, `map_name` | asset rows | SeAT's own location-resolution denormalisation. HANGAR resolves locations through `app.asset_location` with a different shape. |
| eager-loaded `type` object | assets, orders, skills, transactions | Legacy embeds the Fuzzwork `invTypes` row (`typeID`, `typeName`, `portionSize`, `basePrice`, …). HANGAR's `sde.*` comes from CCP's modern JSONL export and keeps promoted columns plus the raw row as `jsonb`, so it cannot reproduce that object field-for-field. |
| `SquadResource.logo` | squad rows | `Squad::getLogoAttribute()` *always* returns a rendered PNG data-URL, generating a placeholder avatar when none is stored. No translation layer reproduces a raster image. |
| `attacker_hash` | killmail attackers | A SeAT-internal surrogate. `app.killmail_attacker` has `record_id` instead. |
| `id`, `internal_id` | wallet transactions, corporation wallet journal | **[Phase 20.6]** SeAT's own MySQL auto-increment primary key, emitted as the row's leading field. `app.wallet_transaction` is keyed on `(owner_kind, owner_id, transaction_id, date)` and has no such column; a synthesised counter would differ between two installations holding identical data. |
| `user_id` | character sheet | **[Phase 20.6]** A legacy MySQL integer. `app.character.user_id` is a `uuid` (§4.1) — the same identifier-space break that makes `UserController` unshimmable, inside an otherwise reproducible route. Emitting the uuid breaks every client parsing it as an integer; synthesising an integer invents an id nobody stored. |
| `skillpoints.total_sp`, `skillpoints.unallocated_sp` | character sheet | **[Phase 20.6]** HANGAR ingests both from ESI on every skills sync (`CharacterSkillsDTO`) and persists neither — `SyncCharacterSkills` writes only the per-skill rows. `total_sp` could be summed from `app.character_skill`; `unallocated_sp` cannot be derived at all. |
| the resolved entity `name` | every embedded `{entity_id, …}` object | **[Phase 20.6]** Legacy resolved these from its `universe_names` table and the corpus was recorded with that table empty, so every entity in every recording reads `"Unknown"`. HANGAR usually **knows** the name and deliberately does not use it here: the shim's contract is byte-identity with the recorded response, and `/api/v1` has always served the resolved name. Same rule the SDE `type` object already followed. |

The corpus is recorded against an installation with no SDE imported — a real, supported state —
where legacy's `withDefault()` emits a small deterministic `{"typeID":N,"typeName":"Unknown",…}`
that the shim *does* reproduce exactly. On an SDE-populated legacy installation those objects are
fuller than the shim can produce.

---

## 8. What you gain by finishing the migration

* **Exact money.** Strings, `NUMERIC(30,2)`, no float anywhere.
* **Freshness you can act on.** `_sync` tells you whether data is stale or unavailable.
* **Scoped credentials.** A token that can only do what the integration needs.
* **Signed webhooks.** Stop polling. Webhook endpoints deliver HMAC-SHA256-signed events with
  at-least-once delivery and a stable delivery id for de-duplication — see
  `deploy/verify-webhook-signature.sh`, which validates a live payload and documents the four ways
  receivers get verification wrong.

  > **Not yet reachable through the product.** There is no endpoint-management API or admin screen
  > yet: `app.webhook_endpoint` rows can only be created by direct SQL, and the HMAC secret is
  > envelope-encrypted with the AAD bound to the endpoint's own uuid, so creating one by hand means
  > sealing the secret with the same scheme `internal/crypto.SealWebhookSecret` uses. The pipeline
  > underneath — transactional outbox, fan-out, signed delivery, retry, endpoint breaker — is built,
  > tested and running on every `serve` and `work` process. What is missing is the surface to
  > configure it, which belongs to the phase that can also test the configuration flow.
* **Cursor pagination** that does not drift or degrade.
* **An OpenAPI document** (`docs/openapi.json`) with declared security schemes, so you can generate
  a client.
