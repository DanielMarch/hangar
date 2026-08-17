# Gate 6 — Spec-Drift Resilience

**Verdict: PASS**

The committed synthetic spec was ingested through `hangar admin ingest-catalogue` with zero source changes, and the four §6.1 outcomes were asserted against the database.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc2` |
| Started | 2026-08-17T13:38:28Z |
| Finished | 2026-08-17T13:38:28Z |
| Duration | 0s |
| HEAD | e1d668c8d4ba037bef978a568881d431582f8457 |
| Ingest path | hangar admin ingest-catalogue (production path), against the spec served over HTTP |
| Synthetic spec | test\drift\gate6_synthetic_spec.json |
| Tag | v1.0.0-rc2 |

## Pass conditions

| # | Condition | Verdict | Measurement |
| :-- | :-- | :-- | :-- |
| 6.1(a) | a route dated past the app pin is created, blocked_by_pin, and excluded from the scheduling query | **pass** | row present=true, blocked_by_pin=true, compatibility_date=2026-09-03, in scheduling set=false |
| 6.1(b) | a string/uuid path parameter records identifier type `uuid` — never bigint, never text | **pass** | identifier_types = map[widget_id:uuid] |
| 6.1(b)-verify | `hangar admin verify-identifier-types` passes against the ingested synthetic spec | **pass** | exit 0 |
| 6.1(c) | a scope matching neither live grammar is stored verbatim as a text primary key and the route survives | **pass** | app.esi_scope holds "esi::synthetic~widget/read@v3" = true; the operation was ingested = true |
| 6.1(d) | an unrecognised x-cache-mode is recorded in app.open_vocabulary, schedules as ttl-based, and the route is NOT rejected | **pass** | cache_mode stored verbatim=true, open_vocabulary cache_mode values=[quantum-entangled ttl-based], still schedulable=true |
| 6.2-clean | `git status --porcelain` is EMPTY — the ingest required no source change | **pass** | 0 modified paths |
| 6.2-at-tag | `git rev-parse HEAD` equals the release-candidate tag | **pass** | HEAD=e1d668c8d4ba, v1.0.0-rc2=e1d668c8d4ba |

## Artefacts

| File | Contents |
| :-- | :-- |
| `ingest-report.json` | what the ingest produced for each of the four synthetic operations, read back from app.esi_route, app.esi_scope and app.open_vocabulary. |
| `zero-code-changes.json` | `git status --porcelain` and `git rev-parse HEAD` at the moment the assertions passed — §6.2 step 4. |

## Notes

§0 rule 4 and §6.2: ANY source change needed to make this pass — including adding a case to a switch, extending a regex, or adding an enum value — is a Gate 6 FAILURE, not a fix. The correct response would be to redesign the ingest to be data-driven. The synthetic spec was authored in Phase 2, before any of this could be run, and is committed unchanged; a fixture edited in response to a failure does not test what it claims to.

The `git status --porcelain` check is run against the whole working tree, so it also catches the subtler failure: a generated artefact (sqlc output, openapi.json, schema.d.ts) that the ingest caused to drift would show up as an uncommitted change even though no hand-written source was touched.
