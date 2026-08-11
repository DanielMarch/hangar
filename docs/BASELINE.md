# BASELINE — Phase 0 measured legacy footprint

**Status: measured, not asserted (SRS v3.1 Principle 15 / defect B6).** Gate 4 (Feature Parity)
verifies against the counts in *this file*, not against SRS Appendix B. Every count below was
independently reproduced against legacy repository HEAD and the ESI spec on 2026-08-06; see
"Method" under each dimension for the exact command run.

**Result: every measured count agrees with SRS v3.1 Appendix B's expected value.** No
disagreement to raise as a specification defect for this measurement pass.

**Phase 14.1 addendum.** One dimension has since been measured in more detail than this pass
recorded: §4 "Concrete alert types" captured only the total, and the per-domain split it omitted
turned out to be where a specification defect was hiding. §4a below records that split, taken with
the same command at the same pinned commit. The total is unchanged and still agrees with Appendix
B; the newly measured detail corrects one figure in SRS §4.4. The lesson is recorded here rather
than only in the phase that hit it: **a total that matches is not evidence that its breakdown
does.**

## Repositories measured

Shallow-cloned (`git clone --depth 1`) on 2026-08-06:

| Repo | HEAD commit | Commit date |
| :-- | :-- | :-- |
| `eveseat/eveapi` | `ba25892e810ff1893d5462ad6e779c3b2cc57555` | 2026-07-24 |
| `eveseat/web` | `cd11287006bddbf00c48cf13fd1fa704b0ea25d6` | 2026-05-05 |
| `eveseat/notifications` | `844f7de7746b8c5161a0ad61cc7690af61eaf092` | 2026-06-16 |
| `eveseat/services` | `b2db97fd75f03c68cf587dd05da5beba2718f360` | 2025-10-09 |
| `eveseat/api` | `fe523ffed5e298ea913242998a2b2274ff8a65e5` | 2025-02-22 |

`eveseat/services` was cloned per the roadmap's repository table but has no dimension assigned to
it in the measurement table below — it is legacy reference material for Phase 4's job-scheduling
semantics, not a count source.

---

## 1. ESI call sites

**Expected (SRS Appendix B): 107. Measured: 107.** ✅ Match.

**Method.** `eveseat/eveapi`, `src/Jobs/**/*.php`. Concrete job classes declare their route as
`protected $endpoint = '...'`; the abstract base class `Jobs/EsiBase.php` also matches this
pattern (`protected $endpoint = '';`) and must be excluded — it has no route.

```sh
grep -rlE '\$endpoint = ' --include=*.php src/Jobs | grep -v '/EsiBase.php$' | wc -l
# => 106
```

Per the roadmap, add the one inline mail-body call site in `Jobs/Mail/`: `Mails.php` declares
`$endpoint = '/characters/{character_id}/mail/'` (list) for its own job, and separately makes a
second, inline ESI call inside its `handle()` method to a *different* route
(`/characters/{character_id}/mail/{mail_id}/`, the individual mail body) that is not exposed
through the `$endpoint` field:

```sh
grep -n "invoke(" src/Jobs/Mail/Mails.php
# => 118:  $body = $this->esi->setCompatibilityDate('2025-08-09')->invoke('get', '/characters/{character_id}/mail/{mail_id}/', [...])
```

106 declared + 1 inline = **107**.

## 2. Distinct ESI routes

**Expected: 106. Measured: 106.** ✅ Match.

**Method.** Dedupe the 106 declared `$endpoint` values, then add the inline mail-body route from
above if it is not already present.

```sh
grep -rlE '\$endpoint = ' --include=*.php src/Jobs | grep -v '/EsiBase.php$' \
  | xargs -I{} grep -m1 '\$endpoint = ' {} \
  | sed -E "s/.*\\\$endpoint = '([^']*)'.*/\1/" | sort > endpoints.txt
sort -u endpoints.txt | wc -l
# => 105
sort endpoints.txt | uniq -d
# => /corporations/{corporation_id}/assets/locations/   (bound twice — matches the roadmap's note)
```

105 distinct declared routes (106 declarations, one duplicate) + 1 new route from the inline
mail-body call (not present in the declared set) = **106**.

## 3. UI controller classes

**Expected: 72. Measured: 72.** ✅ Match.

**Method.** `eveseat/web`.

```sh
find . -path "*/src/Http/Controllers/*Controller.php" | wc -l
# => 72
```

## 4. Concrete alert types

**Expected: 54. Measured: 54.** ✅ Match.

**Method.** `eveseat/notifications`, `src/Notifications/**`. Each alert type is implemented once
per delivery channel (`Discord/`, `Slack/`, `Mail/` subdirectories under each category), so a
straight file count over-counts by roughly 2.5x (135 `.php` files total). The correct count dedupes
by `(category, base filename)` across channel subdirectories, and excludes:

* the three top-level abstract base classes (`AbstractDiscordNotification.php`,
  `AbstractMailNotification.php`, `AbstractSlackNotification.php`)
* the `Structures/Traits/` directory (helper traits, not alert types)
* two **per-channel** abstract classes that only appear nested inside a channel directory and
  are easy to miss with a top-level-only exclusion: `Structures/Discord/AbstractDiscordMoonMiningExtraction.php`
  and `Structures/Slack/AbstractSlackMoonMiningExtraction.php`

```sh
find src/Notifications -name "*.php" \( -path "*/Discord/*" -o -path "*/Slack/*" -o -path "*/Mail/*" \) \
  | grep -v /Traits/ \
  | sed -E 's#/(Discord|Slack|Mail)/#/#' \
  | sed 's#^\./##' | sort -u | grep -v '/Abstract' | wc -l
# => 54
```

### 4a. Per-domain breakdown **[added in Phase 14.1]**

The measurement above recorded only the total. Phase 14 needed the per-domain split and could not
obtain it — `eveseat/notifications` was unreachable from that build environment — so it reported
the resulting inconsistency (SRS §4.4's eight domain counts summed to 53 against a stated 54) and
shipped 53 rather than guessing which domain was short. Phase 14.1 obtained access and re-ran
**this same pipeline, at the same pinned commit**, grouped by category:

```sh
find src/Notifications -name "*.php" \( -path "*/Discord/*" -o -path "*/Slack/*" -o -path "*/Mail/*" \) \
  | grep -v /Traits/ \
  | sed -E 's#/(Discord|Slack|Mail)/#/#' \
  | sed 's#^\./##' | sort -u | grep -v '/Abstract' \
  | awk -F/ '{print $3}' | sort | uniq -c
# =>  23 Structures     7 Characters    7 Seat      6 Wars
#      5 Corporations   4 Sovereignties 1 Contracts 1 Alliances     (total 54)
```

**Result: the total of 54 is confirmed; SRS §4.4's "Structures (22 …)" was understated by one.**
Seven of the eight domain figures are correct, Skyhook is correctly 5, and the arithmetic gap
Phase 14 reported is fully accounted for. §4.4 has been corrected to 23.

**Independent cross-check.** `src/Config/notifications.alerts.php` maps alert key → handler
classes and holds **55 keys**, one of which (`test_integration`) is marked `'visible' => false`:
54 again, from a different artefact. It distributes differently (Structures 25, because
`StructureLowReagentsAlert` and `StructureNoReagentsAlert` are keys that reuse another class's
file and so have no file of their own). The file-based reading above is the one adopted, because
it is the method this document recorded and it agrees with §4.4 in seven domains rather than five.

**Upstream defect noted in passing.** `src/Notifications/Characters/{Discord,Mail,Slack}/NewMailMessage.php`
exists in all three channels but has **no key** in `notifications.alerts.php`, so nothing upstream
can dispatch it. It is counted by the file-based method and absent from the key-based one. Recorded
as an observation about the upstream; HANGAR does not reproduce it.

The full measurement — every entry, its domain, and whether it is a CCP notification type or an
observer-computed SeAT alert — is committed at
`testdata/upstream/eveseat_notifications_alerts.txt` and read back by
`TestCatalogueMatchesMeasuredUpstream`, so the alert catalogue's provenance is reproducible in CI
rather than resting on this document.

### 4b. Upstream ESI route existence checks **[added in Phase 15.1]**

Phase 15 declined to implement four SRS §6 endpoints on the stated grounds that they had no
backing table or no upstream equivalent. Phase 15.1 verified each claim against the authoritative
machine-readable source — the ingested spec snapshot at
`internal/esi/catalogue/embedded/openapi.snapshot.json`, not the live site — before deciding
whether to build or strike.

```sh
node -e "const s=require('./internal/esi/catalogue/embedded/openapi.snapshot.json');
         Object.keys(s.paths).filter(p=>/markets|members|status/.test(p)).forEach(p=>console.log(p))"
```

| SRS §6 endpoint | Upstream route in the snapshot | Phase 15 claim | Verdict |
| :-- | :-- | :-- | :-- |
| `/markets/{region_id}/orders` | `/markets/{region_id}/orders` ✅ exists | "no backing table" | **Claim wrong.** `app.market_order` carries `region_id` and a dedicated index on it. Implemented, with the scope stated in SRS §6.5. |
| `/markets/{region_id}/types` | `/markets/{region_id}/types` ✅ exists | "no backing table" | **Claim wrong**, same table. Implemented. |
| `/corporations/{id}/members/limit` | `/corporations/{corporation_id}/members/limit` ✅ exists | "no backing column" | **Claim correct.** Column added in `00040`, sync handler added, route implemented. |
| `/meta/server-status` | `/status` ✅ exists (`x-cache-age` 30s; `players`, `server_version`, `start_time`, `vip`) | "no backing table" | **Claim correct.** Synced into `app.setting` rather than a table of its own — one global row, overwritten, no history. |

Nothing was struck from the SRS. All four are implemented. The lesson matching §4a's: **a
"not backed" conclusion is a measurement, and must be measured** — two of the four were
contradicted by the schema and the snapshot that were already in the repository.

## 5. UI locales

**Expected: 9 (`af de en fr ja ko ro ru zh-CN`). Measured: 9.** ✅ Match.

**Method.** `eveseat/web`.

```sh
find . -maxdepth 3 -type d -path "*/resources/lang/*" | sort
# => src/resources/lang/{af,de,en,fr,ja,ko,ro,ru,zh-CN}
```

## 6. ESI scopes

**Expected: 70. Measured: 70.** ✅ Match.

**Method.** Not a legacy-repository count — the roadmap specifies "distinct scope strings in the
2026-05-19 spec", i.e. the live CCP ESI OpenAPI document ingested with that compatibility date
(Route Catalogue boot sequence, SRS §4.1.1).

```sh
curl -fsSL -H "X-Compatibility-Date: 2026-05-19" https://esi.evetech.net/meta/openapi.json \
  -o esi-openapi-2026-05-19.json
# info.version in the response: "2026-05-19"
```

`components.securitySchemes.*.flows.*.scopes` declares **72** scope strings, but 2 of them are not
referenced by the `security` requirement of any operation in the document:
`esi.activity.char:read` and `esi.cosmetic.char:read`. The roadmap's count is scopes actually
**usable** — i.e. referenced by at least one operation — which is:

```js
// distinct strings across every operation's `security[].<scheme>` array
// => 70
```

**Note for a future phase:** the Route Catalogue ingest (Phase 2) should use the
operation-referenced set (70), not the full securitySchemes declaration (72), as the source of
truth for `internal/scopes/` — the declared-but-unused pair look like either scopes CCP has
deprecated in-place or reserved for an endpoint not yet public.

## 7. `/api/v2` controllers

**Expected: 9. Measured: 9.** ✅ Match.

**Method.** `eveseat/api`.

```sh
find . -path "*/src/Http/Controllers/Api/v2/*.php" | sort | wc -l
# => AllianceController, ApiController, CharacterController, CorporationController,
#    KillmailsController, RoleController, RoleLookupController, SquadController, UserController
# => 9
```

---

## Summary

| Dimension | Expected (SRS Appendix B) | Measured | Match |
| :-- | --: | --: | :-- |
| ESI call sites | 107 | 107 | ✅ |
| Distinct ESI routes | 106 | 106 | ✅ |
| UI controller classes | 72 | 72 | ✅ |
| Concrete alert types | 54 | 54 | ✅ |
| UI locales | 9 | 9 | ✅ |
| ESI scopes (2026-05-19) | 70 | 70 | ✅ |
| `/api/v2` controllers | 9 | 9 | ✅ |

Gate 4 may proceed against these counts with no open disagreement.
