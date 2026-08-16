# Gate 7 — Third-Party API Migration

**Verdict: PASS**

34 legacy read routes: 16 served and byte-identical, 12 pending, 4 unshimmable, 2 breaking.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc1` |
| Started | 2026-08-16T01:17:07Z |
| Finished | 2026-08-16T01:17:40Z |
| Duration | 33s |
| Comparison | response BYTES through the real handler chain, not parsed objects |
| Corpus | testdata/legacy-api-v2 — responses recorded from a running legacy SeAT instance |
| Verification | TestShimByteCompatibleForAllNineControllers and the four §7.3 condition tests |

## Pass conditions

| # | Condition | Verdict | Measurement |
| :-- | :-- | :-- | :-- |
| 7.1 | every served route is byte-identical to its recording (field order, whitespace, numeric formatting and null-vs-absent all in scope) | **pass** | 16 of 16 served routes byte-identical |
| 7.7 | RoleController and RoleLookupController return a documented 410 with a migration pointer, not a partial shim | **pass** | 2 breaking routes, 2 answered 410 |
| 7.8 | unshimmable and pending routes answer 501 — a clear "not shimmed", never a 404 | **pass** | 4 unshimmable + 12 pending, 16 answered 501 |
| 7-suite | the corpus verification suite passed (7.2 Deprecation, 7.3 Sunset, 7.4 envelope stripped, 7.7 410s, 7.8 write routes) | **pass** | go test exited 0 |

## Artefacts

| File | Contents |
| :-- | :-- |
| `byte-diff-summary.json` | the route counts by status. |
| `byte-diff.csv` | one row per legacy read route: expected and actual byte length, sha256 of each, byte-identical verdict, and the offset of the first difference. The blocking artefact. |

## Notes

Gate 7 is 16 of 34 served, and that is a PRODUCT DECISION recorded as such rather than a defect: 4 routes are permanently unservable and 2 are breaking by construction. Each carries its reason in the `reason` column of byte-diff.csv, re-derived against legacy's source — a surrogate auto-increment key on the wire, MySQL double rounding, an identity space HANGAR does not share. A shim that invented values for those would be byte-compatible with the recording and WRONG on every real installation.

"Byte-identical" here is the strict standard §7.2 defines: field order, JSON whitespace, numeric formatting and null-vs-absent are all in scope. Comparing parsed objects would not satisfy this gate, and the report records a sha256 of both sides so the claim can be checked rather than taken.

One caveat the report cannot state for itself: five served routes rest on single-row recordings, so their own multi-row ORDERING is pinned by inference from the ordering rule measured elsewhere, not by their own recording. See docs/PRE_V1_OPEN_ITEMS.md N-3.
