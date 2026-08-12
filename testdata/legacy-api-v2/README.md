# `testdata/legacy-api-v2/` — the Gate 7 corpus

**What this is.** `responses/*.json` are the **byte-exact HTTP response bodies legacy SeAT's
`/api/v2` emits** for the fixture dataset in `recorder/src/fixtures.php`. They are the comparison
target for `TestShimByteCompatibleForAllNineControllers`, and they are *recorded*, not written by
hand — the roadmap's Phase 19 edge case is explicit that "byte-compatibility is measured against
recorded bytes, never against a hand-written expectation of what legacy *should* emit."

`MANIFEST.json` lists every recorded response with its status, byte length and SHA-256.

---

## How they were recorded

`recorder/` is the harness, committed so the recording is reproducible rather than asserted. It
runs **real Laravel 10 and real eveseat code** — no reimplementation of Laravel's pagination
envelope, resource transformation or JSON encoding, because those are precisely the parts a
hand-derivation gets subtly wrong.

| Component | Pinned at | Why this pin |
| :-- | :-- | :-- |
| `eveseat/api` | `fe523ffed5e298ea913242998a2b2274ff8a65e5` | `docs/BASELINE.md`'s measured commit |
| `eveseat/eveapi` | `ba25892e810ff1893d5462ad6e779c3b2cc57555` | `docs/BASELINE.md`'s measured commit |
| `eveseat/services` | `b2db97fd75f03c68cf587dd05da5beba2718f360` | `docs/BASELINE.md`'s measured commit |
| `eveseat/web` | `cd11287006bddbf00c48cf13fd1fa704b0ea25d6` | `docs/BASELINE.md`'s measured commit |
| `laravel/framework` | 10.50.2 | `eveseat/api` requires `^10.0` |
| PHP | 8.2.33 | `eveseat/api` requires `^8.1` |
| MySQL | 8.4.11 | see "Why MySQL" below |

The pipeline:

1. `recorder/src/migrate.php` applies **all 472 migration files** from `services`, `eveapi` and
   `web` in filename order to a real MySQL 8 database. 471 apply cleanly (see "Deviations").
2. `recorder/src/record.php` seeds `fixtures.php`, then renders each route through the **verbatim**
   `Seat\Api\Http\Resources\*` classes over real Eloquent models, and writes
   `$response->getContent()` unmodified.

To re-record:

```bash
docker network create hangar-corpus && docker run -d --name corpus-mysql --network hangar-corpus -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=seat mysql:8
```

then build `recorder/Dockerfile`, clone the four pinned repos into `recorder/seat-{api,eveapi,services,web}`,
and run `run.sh migrate.php` followed by `run.sh record.php`.

### Why MySQL and not SQLite

Field **order** is what Gate 7 measures, and for the routes that serialise a raw Eloquent model
the order is the table's physical column order. That order is an artefact of 472 migrations doing
`add`, `drop`, `rename` and `after()`, and MySQL and SQLite disagree about it: `->after()` is a
MySQL-only positioning hint that Laravel's SQLite grammar silently ignores, and a dropped-then-
re-added column lands in a different place. `character_assets` is the worked example — its
`location_type` was renamed away and re-added twice, so it ends up **last**, after `updated_at`,
and `is_blueprint_copy` sits between `is_singleton` and `x` because one 2026 migration asked for
it there. Recording on SQLite would have produced a plausible, wrong answer.

### Deviations from a stock SeAT install (all deliberate, all recorded)

1. **One patched migration.** `web/2019_11_12_220840_drop_groups_table.php` contains
   `CAST(user_settings.value AS INT)`, which MySQL 8.4 rejects (`SIGNED` is the MySQL spelling;
   `INT` is MariaDB's). Patched to `AS SIGNED` in the recorder's copy. It is a data-backfill query
   over an empty table and defines no column, so the schema is unaffected — but without the patch
   the migration aborts before adding `users.main_character_id`, which `UserResource` emits.
2. **`balance_buckets` is not applied** (1 of 472). It seeds ESI job-scheduling buckets and
   touches no table this corpus reads.
3. **The SDE is not imported.** SeAT's `invTypes`/`invGroups`/`solar_systems` tables come from
   `php artisan seat:sde:update`, not from migrations. `recorder/src/sde.php` creates them empty —
   a real, supported state for an installation that has not yet run that import. See the "Known
   divergence" section below for what this costs and why it is the right recording.
4. **`auth()->check()` is false.** This is faithful, not a shortcut: legacy's `/api/v2` routes sit
   behind the `api.auth` middleware, which validates an `X-Token` header against `api_tokens` and
   never logs a Laravel user in. `SquadScope`'s first line short-circuits on exactly that.

---

## What the fixture is chosen to prove

`fixtures.php` is small on purpose — the corpus is a byte target, so every value has to be
reproducible exactly — but each value is there to break something if the shim gets it wrong:

* **Money beyond `2^53`.** `9007199254740993.01` goes in; legacy emits `9007199254741000`. That
  ~7 ISK is **legacy's own loss, at rest**: `character_wallet_journals.amount` is a MySQL
  `DOUBLE`, not a decimal, so the imprecision happens before anything is serialised. This
  corrects a reading of SRS §10 that the shim *introduces* the imprecision — it does not; it
  reproduces legacy's. See `docs/APPENDIX_C_MIGRATION.md`.
* **Whole-number floats.** `reward` and `collateral` are `0.0` and serialise as `0`, `volume` is
  `27289.0` and serialises as `27289` — no decimal point, no exponent. A Go encoder using
  `strconv.FormatFloat(v, 'g', -1, 64)` emits `9.007199254741e+15` and fails the comparison.
* **An empty collection.** `character.assets.empty` pins `"data":[]` with `"from":null`,
  `"to":null`, `"total":0` — never `"data":null`. Phase 18 found that an empty success is easy to
  mistake for a failure, and the shim is where that mistake would be silent.
* **A real second page.** 20 wallet-journal rows against a page size of 15, recorded at
  `?page=1` and `?page=2`, so `prev`/`next`/`from`/`to`/`last_page` are all exercised with
  non-degenerate values.
* **Escaped slashes.** Laravel's `response()->json()` uses default flags, so every URL in
  `links`/`meta` is `http:\/\/…`. `encoding/json` does not do this by default.
* **Non-ASCII and quotes.** The character description carries `"` and `ünïcode`, recorded as
  `\"` and `ü` — Laravel escapes non-ASCII, Go's `json.Marshal` does too, but only for
  some ranges.

## Known divergence, recorded rather than hidden

Two things in these bytes the shim **cannot** reproduce from HANGAR's schema for arbitrary data:

1. **Eager-loaded SDE sub-objects.** With the SDE absent, legacy's `withDefault()` emits a small,
   deterministic `{"typeID":N,"typeName":"Unknown","group":{"groupName":"Unknown"}}` — which the
   shim reproduces exactly. With the SDE **present**, legacy emits the full Fuzzwork `invTypes`
   row (`portionSize`, `basePrice`, `raceID`, …) in that column vocabulary; HANGAR's `sde.*`
   schema is built from CCP's modern JSONL export (migration 00036) and keeps a promoted column
   set plus the raw row as `jsonb`, so it cannot produce that object field-for-field.
2. **`SquadResource.logo`.** `Squad::getLogoAttribute()` *always* returns a rendered PNG data-URL
   — it generates a placeholder avatar through `intervention/image` when no logo is stored. That
   is a raster image produced by a specific library and font; no translation layer reproduces it.

Both are documented in `docs/APPENDIX_C_MIGRATION.md`, and the shim's byte test names the `logo`
substitution explicitly and fails if any *other* byte differs.
