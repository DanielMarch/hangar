# Gate 5 — Deployment Usability — **PASS**, unqualified

**Twelve conditions, twelve passes, no substitution and no partial. This is the first time in the
project's history that Gate 5 has been fully met.**

Run twice at `a9873ed`, with **identical verdicts** on both runs.

| Condition | rc1 | rc2 | rc3 |
| :-- | :-- | :-- | :-- |
| 5.1 three commands, no editor step | FAIL | pass | **pass** |
| 5.2 pulled from a public registry, no compilation | FAIL | *substituted* | **pass** |
| 5.3-blank-env | pass | pass | **pass** |
| 5.3-callback-mismatch | FAIL | pass | **pass** |
| 5.3-postgres-not-ready | pass | pass | **pass** |
| 5.4 migrations run, healthy inside 5 minutes | pass | pass | **pass** |
| 5.5 default profile is postgres + hangar + migrate | pass | pass | **pass** |
| 5.6 the binary serves the SPA on the API's port | pass | pass | **pass** |
| first-boot-alerts (54 types, 4 thresholds) | pass | pass | **pass** |
| 5.7 re-run after a version bump | pass | pass | **pass** |
| 5.8 manual deployment, external PostgreSQL 18 | FAIL | *partial* | **pass** |
| 5.8-config-name-collision | FAIL | pass | **pass** |

## The two that had never passed

### 5.2 — publication, and a verdict that could not change

At rc2 this was recorded as **permanently substituted**: `raw.githubusercontent.com` 404'd,
`ghcr.io` answered 403, and `git remote -v` was empty. The decision was made explicitly rather
than deferred, and it stood or fell with this phase.

HANGAR is published. All three of §5.1's commands are verifiable by somebody who is not this
repository:

```
raw.githubusercontent.com/DanielMarch/hangar/main/docker-compose.yml   200
raw.githubusercontent.com/DanielMarch/hangar/main/deploy/install.sh    200
docker pull ghcr.io/danielmarch/hangar:v1.0.0-rc3                      OK
```

The pull was performed **after `docker logout ghcr.io` and after deleting the local image**, so
it is a real anonymous pull rather than a cache hit.

**The condition also stopped asserting its own answer.** It used to `record "5.2" "SUBSTITUTED"`
unconditionally, with the 404/403 sentence written into the script — a verdict that cannot change
is not a measurement, and it would have gone on reporting "nothing is published" the day after
something was. It now probes both halves anonymously at run time and records exactly which half
is missing when one is.

**Not under `hangar-project`.** Creating an organisation is not something this session can do, so
the release is published under the operator's account and every documented URL was amended. The
Go module path still says `github.com/hangar-project/hangar`, so `go get` of it does not resolve
— recorded in B-12 rather than left to be discovered.

### 5.8 — the unit file that did not exist

At rc1 and rc2 this was PARTIAL, noting that "systemd itself was NOT exercised — this host is
Windows and has no systemd; the unit file is deploy/ material and remains unverified."

Two things were wrong with that. A host having no systemd does not mean systemd cannot be run
**on** it — a container with systemd as PID 1 does exactly that. And **there was no unit file**:
SRS §9.2 has required one since v3.0 and the repository shipped none, so "unverified" was
describing something that did not exist.

`deploy/hangar.service` exists now, and `tools/gate5-deploy/systemd.sh` runs it under **systemd
252** inside `ubi9-init`, against an external PostgreSQL 18, with five sub-verdicts:

| | |
| :-- | :-- |
| `5.8-verify` | `systemd-analyze` reports no unknown lvalue, unknown section or parse failure |
| `5.8-start` | `active (running)`, with `ExecStartPre` `hangar migrate up` at `status=0/SUCCESS` |
| `5.8-migrate` | the external PostgreSQL 18 migrated inside the unit's own journal |
| `5.8-healthz` | `GET /healthz` → 200 from the systemd-managed process |
| `5.8-stop` | `inactive (dead)` after SIGTERM, not `failed` |

`5.8-verify` checks for the *absence of a parse complaint* rather than the exit code, because an
unknown directive is silently **ignored** at load — the failure mode a hardening block is most
likely to have, and the one a green `systemctl start` will not show you.

**What it does not prove**, recorded rather than glossed: it is a container, so cgroup delegation
and several sandboxing directives are enforced more weakly than they would be on metal.

## What this gate cost, and what that says

Renaming the published URLs broke Gate 5 in **four places that had the old name typed into
them**, and three conditions failed for reasons unrelated to what they measure — 5.1 could not
pull an image the runner had tagged under a different name, 5.3's probes ran an image that does
not exist, and 5.7 tried to *pull* a `:bumped` tag nothing had built. The first-boot check also
had `2>/dev/null` on its psql exec, which made a broken exec and a slow ingest indistinguishable
("got ? and ?").

All of it is derived from `docker-compose.yml` now — the file an operator actually downloads — so
the gate cannot drift from the artefact it tests.

That is three gate-runner defects in this phase, on top of the six Phase 22 found. **The
measuring apparatus has now failed more often than the product**, and it fails quietly. The
correction has been the same every time: derive the value from the artefact under test rather
than typing it into the checker.

## Artefacts

| File | What it holds |
| :-- | :-- |
| `conditions.tsv` | one row per condition: id, verdict, measurement |
| `transcript.txt` | the full run, every command and its output |
| `systemd-verdict.txt` | the five systemd sub-verdicts |
| `systemd-transcript.txt` | `systemctl status`, the unit's journal, `systemd-analyze verify` |
| `publication-check.txt` | the anonymous HTTP probes of the published URLs and registry |
