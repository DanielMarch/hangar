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

**17 of 34 legacy routes are served, and the other 17 will never be.** That is a product
decision, not an omission, and every one is enumerated with a reason in
`APPENDIX_C_MIGRATION.md`. They expose MySQL auto-increment surrogate keys, a MySQL `double`
money column, a legacy-computed attacker hash, or a legacy identifier space — values that come
from legacy's own storage and have no honest source in HANGAR's. A shim that invented them would
be byte-identical to a recording and wrong on every real installation.

**Nothing answers "not yet".** At rc2 twelve of those routes were marked pending, which told an
integrator to wait for a release that was never coming. Every route now serves, or says
permanently why it does not.

### Known limitations at v1.0.0

Stated here rather than discovered. `docs/PRE_V1_OPEN_ITEMS.md` carries the full derivation of
each.

**Both limitations this section named at rc2 are gone.** §4.4's pipeline runs under `serve`
(N-9, proven on a stock compose stack), and every absent product surface N-4 named has an API
route and a UI screen — including platform configuration, which had **no production writer at
all**, so no installation could create a platform row by any means the product offered.

What is left is what the release has decided to ship with. Each is a decision with a
measurement behind it, not an unfinished task.

- **`/api/v2` is read-only.** Every write route answers 405 with a pointer to the `/api/v1`
  equivalent, deliberately: SRS §10 scopes the shim to reads.
- **17 of legacy's 34 `/api/v2` read routes are served; the other 17 never will be.** Fifteen
  answer 501 with a permanent reason and two answer 410. Nothing answers "not yet" any more —
  every unserved route says *why*, and each reason was derived against legacy's own source. The
  short version: legacy puts values on the wire that come from its own storage — a MySQL
  auto-increment, a `double` money column, a computed attacker hash — and HANGAR's storage is
  correct rather than merely different. Reproducing them would mean storing legacy's mistakes.
  `APPENDIX_C_MIGRATION.md` maps every one to its `/api/v1` equivalent.
- **`AllianceWorker` has never run against real ESI**, and cannot on this installation: the
  development corporation is not in an alliance, so `app.alliance` is empty and there is nothing
  to reconcile. Capability #37 rests on its seeded integration test. This is a limitation of the
  *evidence*, not of the code, and it applies to any installation whose corporations are
  unallied.
- **Capability #41's structure fan-out resolves nothing on an installation with no structure
  ids.** It runs — verified against real ESI at rc3, HTTP 200 — and
  `ListCharacterStructureIDs` unions four sources, all four of which are empty on a fresh
  install. It will do useful work on an installation that has synced structures; it has never
  been observed doing so.
- **`sde.station.name` is empty for every station.** CCP's JSONL export ships no station names —
  the client composes them — so the column imports empty. Nothing in HANGAR reads it today.
- **The systemd unit is verified in a container, not on metal.** `deploy/hangar.service` starts,
  migrates, serves and stops cleanly under systemd 252, but a few sandboxing directives are
  enforced more weakly inside a container than they would be on a real host.
