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

## One note on the tag: the runners were fixed while the gates ran

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
