# Gate evidence

`04_RELEASE_GATES.md` §0 rule 1: **a gate is evidence, not an opinion.** Each gate produces a
machine-readable artefact committed under `docs/gate-evidence/<version>/`, and "we ran it and it
looked fine" is a fail.

## Layout

```
docs/gate-evidence/
  <version>/
    gate1/n1/   gate1/n3/   ESI Load Stability, at both replica counts (§1.4 requires both)
    gate2/                  Revocation SLO
    gate3/                  Alert Delivery Integrity
    gate4/                  Feature Parity
    gate5/                  Deployment Usability
    gate6/                  Spec-Drift Resilience
    gate7/                  Third-Party API Migration
```

Every directory carries a `SUMMARY.md` with the same shape: a mechanical **PASS/FAIL** verdict,
the window the run covered, the environment (§0 rule 3), one row per numbered pass condition with
the measurement that decided it, and an index of the artefacts beside it. The verdict is PASS only
when every condition passed **and at least one condition was evaluated** — a gate that measured
nothing has not passed, it has not run.

## How each one is produced

| Gate | Command | Cost |
| :-- | :-- | :-- |
| 1 | `make gate1` (or `gate1-n1` / `gate1-n3`) | 4 h at N=1 **and** 4 h at N=3 |
| 2 | `make gate2` | ~1 h |
| 3 | `make gate3` | 4 h |
| 4 | `make gate4-evidence` | static |
| 5 | `bash tools/gate5-deploy/run.sh <version>` | ~15 min, and see its header |
| 6 | `make gate6 GATE_TAG=<tag>` | ~2 min |
| 7 | `make gate7` | ~5 min |

All of them take `GATE_VERSION`. **Set it to the release version before the first run** —
`make gate4-evidence` writes into `docs/gate-evidence/$(GATE_VERSION)/`, and leaving it at its
phase-numbered default overwrites a previous phase's evidence rather than creating this one's.

Gates 1, 2, 3 and 6 need a **throwaway database**. They migrate it and seed thousands of rows;
Gate 1 also clears `app.esi_replica`. The runners refuse to start unless `HANGAR_DB_URL` names a
database whose name contains `gate`. Derive that URL from `.env` by substituting **only** the
database name — hand-writing the credentials is how a run dies at "password authentication
failed" in a way that reads like a code failure and is not.

## Two ordering constraints that are easy to get wrong

**Gate 6 must run first, on a clean tree.** §6.2's proof is that the ingest required no source
change: `git status --porcelain` empty and `git rev-parse HEAD` equal to the release tag. Every
other gate writes untracked files into this directory, so running one of them first makes Gate 6's
own check fail on evidence rather than on source. Gate 6 performs its git check *before* writing
its own artefacts, so it is clean-tree-safe on a re-run.

**Gate 1 wants a quiet machine.** It measures a rate-limit ledger under contention. Anything else
heavy on the box — another gate, a test suite, a build — is measuring the apparatus. Its two
replica counts are separate runs for the same reason, and §1.4 is explicit that a pass at one
count says nothing about the other.

## What a failed gate looks like

It looks like a committed directory whose `SUMMARY.md` says **FAIL**, naming the condition and the
number that decided it. That is the intended outcome of a gate that does not pass — §0 rule 4 and
the phase that produced this directory both take the view that a recorded failure is worth more
than an unrun gate, and considerably more than a gate quietly skipped.

## `v1.0.0-rc2`: what the second full run added

All seven gates pass at `v1.0.0-rc2`. `v1.0.0-rc1/` is kept beside it — a release that had to
correct itself should be able to show what it corrected — and the two directories are directly
comparable, because the runners changed but the harness's conditions did not weaken.

Two extra directories exist at rc2 and are deliberate:

* **`gate3-first-attempt/`** — a recorded FAIL that measured the apparatus rather than HANGAR. It
  is the only artefact showing what a stale gate database does to a verdict, including a condition
  that reported **pass while measuring nothing**. Its own `WHY_THIS_IS_HERE.md` has the derivation.
* **`gate1/n1/` and `gate1/n3/`** now cover the whole run: `divergence.csv` holds **240 of 240
  minutes** at N=1 (rc1: 18) and 241 at N=3 (rc1: 32), because the installation no longer stops
  serving sixteen minutes in.

## `v1.0.0-rc3`: what the third run added, and what it cost

**Gate 5 is unqualified for the first time.** Both conditions that had never passed do:

* **5.2** — HANGAR is published, and the condition stopped asserting its own answer. It used to
  `record "5.2" "SUBSTITUTED"` unconditionally with the 404/403 sentence written into the script,
  which would have gone on saying "nothing is published" the day after something was. It probes
  both halves anonymously at run time now.
* **5.8** — was PARTIAL on the note that "the unit file … remains unverified", and **there was no
  unit file**. §9.2 has required one since v3.0. It exists and starts under systemd 252.

Two directories exist at rc3 that no previous candidate has:

* **`bench/`, `bench-second/`, `bench-third/`** — eleven runs of the Phase 4 exit criterion off
  Docker Desktop's Windows port forwarding, **nine of which pass**, plus a transport-floor
  benchmark that measures what the same 32 workers cost doing nothing. It is the first committed
  measurement showing that criterion met.
* **`b4/`** — the granted and derived ESI scope sets, after the operator's re-authorisation took
  them from 50 to 52.

### The runner defects this run found

Three more, all the same shape as Phase 22's six, and all in Gate 5:

* **Four hard-coded image names.** The runner tagged `ghcr.io/hangar-project/hangar` while the
  compose file named a different registry, so `docker compose up` could not pull, 5.3's probes ran
  an image that does not exist, and 5.7 tried to *pull* a `:bumped` tag nothing had built. Three
  conditions failed for reasons unrelated to what they measure.
* **A hard-coded origin.** The "substituted origin" section probed URLs that had been typed into
  the script, so it reported 404 twice however the documented URLs actually read.
* **`2>/dev/null` on the first-boot poll's psql exec**, which made a broken exec and a slow
  catalogue ingest indistinguishable: it reported "got ? and ?" and there was no way to tell which.

**The correction was the same in every case: derive the value from the artefact under test rather
than typing it into the checker.** Gate 5 reads its image name, its origin URLs and 5.2's whole
verdict out of `docker-compose.yml` now — the file an operator downloads.

Counting rc2's six and rc3's three, **the measuring apparatus has failed nine times and the
product three**, across the two phases that ran the gates repeatedly. That ratio is the argument
for the section below.

### One gate was deliberately run fewer times

Gate 1 was run **once at each replica count** rather than twice, on the operator's explicit
decision, to save roughly eight hours of wall clock. It is a documented deviation from the rule
below rather than an oversight. The specific risk that rule guards — a verdict that depends on
whether the gate has been run before — is the one Phase 22 closed for Gate 1 by making it
`TRUNCATE` what it owns (defect 10.5), so of the seven gates it is the one where a second run
would have added least. Every other gate ran twice with its verdicts diffed.

## RUN EACH GATE TWICE BEFORE YOU TRUST IT

This is the lesson of Phase 22 and it is worth more than any single fix in it. Running the gates a
second time found **six defects in the runners** — and five of the six would have produced a wrong
VERDICT rather than an error:

* `make build` wrote `bin/hangar` while every runner executes `bin/hangar.exe` on Windows, so the
  gates would have measured a five-week-old binary and said nothing.
* Gate 5 leaked a `hangar serve` container per run; three were found still retrying 21 hours later.
* `make gate3` — the command this file names — could not start with its shipped defaults.
* Gate 3 and Gate 1 never reset their databases, so their verdicts depended on whether they had
  been run before. Gate 3's second run turned that into a false FAIL on four conditions **and a
  false PASS on the one condition testing that release's headline fix**.

`tools/gate1-load` had cleared `app.esi_replica` since it was written, for exactly this reason one
table smaller. The instinct was right and had been applied too narrowly.

**Gates 1 and 3 now reset the state they own before measuring.** Gate 2 creates a fresh population
per run and needed no change. If you add a gate, make it start from a known state, and then run it
twice and diff the verdicts.

## Where `v1.0.0-rc2` points, and the one commit after it

`v1.0.0-rc2` is `e1d668c`. Exactly one commit follows it, and it contains exactly two files:

```
$ git log --oneline v1.0.0-rc2..HEAD
6d37c6d test(gate6): re-run at the final tag, so §6.2's proof holds where the release points

$ git diff --name-only v1.0.0-rc2 HEAD
docs/gate-evidence/v1.0.0-rc2/gate6/SUMMARY.md
docs/gate-evidence/v1.0.0-rc2/gate6/zero-code-changes.json
```

That is unavoidable rather than untidy, and it is worth stating so nobody has to work it out
twice. §6.2 requires a clean tree at the tag; Gate 6 checks that **before** writing its own
artefacts, so the check at `e1d668c` was valid — but committing the artefacts it then produces
necessarily moves HEAD past the tag. Tagging the later commit would only move the problem, since
re-running Gate 6 there would produce another commit.

So the tag names the commit whose tree Gate 6 verified clean, and the only thing after it is Gate
6's own evidence for that verification.

Gate 1's and Gate 3's runners were also fixed after the first Gate 6 run of this release (the
database resets, and gate3's default split). The binary is unchanged across all of them, and that
is checkable rather than asserted:

```bash
git diff --name-only bf1d136 v1.0.0-rc2 | grep -vE '^tools/|^docs/'
```

returns nothing. Nothing under `tools/` or `docs/` is compiled into `cmd/hangar`. The image was
rebuilt `--no-cache` at the final commit regardless, and reports
`hangar version v1.0.0-rc2 (commit e1d668c)`.

## One note on the rc1 tag: the runners were fixed while the gates ran

Running these gates for the first time found defects **in the runners** as well as in the product,
and each fix moved the `v1.0.0-rc1` tag forward. Some evidence here was therefore produced at an
earlier commit than the tag now points at.

**The release-candidate build is identical across every one of those moves.** Each is confined to
`tools/` (the gate runners), `docs/`, or a `_test.go` file — none is compiled into `cmd/hangar`.
That is checkable rather than asserted:

```bash
git diff --name-only <earlier-commit> v1.0.0-rc1 | grep -vE '^tools/|^docs/|^deploy/|_test\.go$'
```

returns nothing for each. `deploy/` is in that list for a reason worth stating rather than
waving through: the two installers changed after Gate 2 ran (the base64 password defect Gate 5
found). Neither is compiled into anything — no `go:embed` covers `deploy/`, and the image's final
stage copies exactly one file, `/out/hangar`, into a base with no shell at all. The binary and the
image are the same artefact before and after. The cheap gates (4, 6, 7) were re-run at the final tag; Gate 2's hour was
not spent again to watch the same binary produce the same number. Saying so is better than implying
everything ran at the final commit.

The runner defects are worth listing, because each would have produced a wrong verdict rather than
an error:

* Gates 1 and 6 answered `/meta/compatibility-dates` with a bare array where the catalogue decodes
  an object — so the ingest silently fell back to the embedded snapshot, and **Gate 6 spent its
  first run asserting the four synthetic outcomes against the real spec** and reporting four
  failures that said nothing about drift resilience.
* Gate 2 saturated the bulk queue against the same platforms it measured, so the bulk pass revoked
  everything first and the urgent path — the one §2.2 measures — had nothing left to do.
* Gate 1's condition 1.4 could not pass at any duration, because the harness's default adversarial
  schedule produces ~20 errors against a budget of 100 per 60-second window.
* Gate 3's notification generator advanced through the catalogue too slowly to reach two of the
  eight domains inside a short run, so "all eight domains" depended on run length.
* Gate 5 checked its first-boot alert counts nine seconds after `/healthz` went green, before the
  background catalogue ingest had landed, and read 50/0 — which looks exactly like defect B41 and
  was not.
