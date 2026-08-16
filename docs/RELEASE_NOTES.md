# HANGAR release notes

This file is the **announcement record**. It is not a changelog of commits — the git history is
that — it is the place where HANGAR states, in advance and in writing, the things integrators are
entitled to be told in advance: what is deprecated, when it is removed, and what replaces it.

It exists because `04_RELEASE_GATES.md` §7.5 checks three of its four requirements *against this
file*, and until Phase 22 the file did not exist, so three of the four could not be evaluated at
all (audit item N-8). A sunset policy with nothing to check against is a promise with no record.

**The `Sunset` header and the date in §7.5's table are the same value**, taken from
`internal/api/v2shim.SunsetDate` and verified by `TestReleaseNotesMatchTheSunsetHeader` — a
release note that disagrees with the header the server actually sends is worse than no release
note, because it is an announcement an integrator would act on.

---

## v1.0.0 — unreleased

The first release. Everything below is stated for the record rather than as a change from a
previous version, since there is no previous version.

### Deprecated on arrival: `/api/v2`

`/api/v2` ships **deprecated in v1.0.0**. It is the read-only compatibility shim SRS §10 requires
so that an installation migrating from the legacy PHP application can point its existing
integrations at HANGAR without rewriting them on the day of the cutover. It is not a supported
long-term API and it never was one.

| | |
| :-- | :-- |
| **Status** | Deprecated in v1.0.0, the release that introduces it |
| **Removal** | **2027-08-31**, and no earlier |
| **Announced** | here, in the v1.0.0 notes — the release that ships it |
| **Replacement** | `/api/v1`, documented at `/api/v1/openapi.json` |
| **Migration** | `docs/APPENDIX_C_MIGRATION.md`, route by route, including the routes that are **not** shimmed and why |

Every `/api/v2` response carries `Deprecation: true`, a `Sunset` header (RFC 8594) set to the date
above, and two `Link` headers pointing at the migration guide. A client that reads none of those
still has until the removal date; a client that reads any of them has been told.

**What the policy guarantees.** §10's promise is "removed no earlier than two minor versions
later", with the removal announced at least one minor version in advance. Both are satisfied by
construction here: the announcement is in the release that introduces the shim, and the removal
date is roughly twelve months out — long enough that an integrator with a twice-yearly maintenance
window gets two of them.

**Moving the date.** Later is a deliberate decision and needs an entry in these notes. Earlier
breaks the §10 promise and must not happen. Either way the value lives in exactly one place,
`v2shim.SunsetDate`, and this file is checked against it.

**16 of 34 legacy routes are served.** That is a product decision, not an omission, and the
eighteen that are not are enumerated with a reason each in `APPENDIX_C_MIGRATION.md`. Fourteen of
them are permanently unservable: they expose MySQL auto-increment surrogate keys, MySQL `double`
rounding, or legacy-schema columns that have no honest value in HANGAR's data model. A shim that
invented values for those would be byte-identical to a recording and wrong on every real
installation.

### Known limitations at v1.0.0

Stated here rather than discovered. `docs/PRE_V1_OPEN_ITEMS.md` carries the full derivation of
each.

- **§4.4's alert pipeline runs only under `hangar work`.** The stock single-service deployment
  syncs, provisions and serves, but produces and delivers no alerts (item N-9).
- **Absent product surface.** Subscription management (snooze, disable, cache opt-out), alert
  routing and channel CRUD, and platform/entitlement configuration have no API or UI, and are
  reachable only by SQL (item N-4).
- **`/api/v2` is read-only.** Every write route answers 405 with a pointer to the `/api/v1`
  equivalent, deliberately: SRS §10 scopes the shim to reads.
