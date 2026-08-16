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

## One note on the tag and Gate 2

Gate 2 was run at commit `3ac0014`; the `v1.0.0-rc1` tag was subsequently moved forward to pick up
harness fixes for Gates 1 and 3. **The release-candidate build is identical across that move** —
every change between the two commits is under `tools/` (the gate runners), `docs/`, or a `_test.go`
file, and none of them is compiled into `cmd/hangar`. The check is mechanical:

```bash
git diff --name-only 3ac0014 v1.0.0-rc1 | grep -vE '^tools/|^docs/|_test\.go$'
```

which returns nothing. Gates 4, 6 and 7 were re-run at the final tag because they are cheap;
re-running Gate 2's hour to observe the same binary produce the same number was not a good use of
the machine, and saying so is better than implying it ran later than it did.
