# Gate 2 — re-run STARTED and PAUSED at the operator's request

This sits beside `NOT_RE_RUN.md` rather than replacing it. That file's reasoning
still stands; this one records that the reasoning was being replaced by a
measurement, and that the measurement was interrupted rather than completed.

## Why it was started

`NOT_RE_RUN.md` argued the gate did not need re-running, and named its own
counter-argument: a reader who thinks the entitlement-disable path could perturb
the SLO "is owed an hour of machine time that nobody has spent."

The §4 verdict table put that far more strongly than the derivation did —
"**Nothing this phase touched it.** No change to `internal/provisioning`, the
urgent queue, the revocation triggers or §2.2's measurement path." That summary
is too strong to leave standing. N-4 added `SetEntitlementRuleEnabled`, and it
calls `Urgent.HandleUserChange` — so this phase added a NEW writer to the queue
Gate 2's SLO is defined over. The queue's implementation is unchanged; the set
of things that write to it is not. The row has been corrected to say so.

## How far it got

```
gate2: seeded 5000 identities across 3 platforms (30000 provisioning states)
gate2: enqueueing 5000 urgent revocations over 30m0s (360ms apart) while bulk is saturated
```

Killed during the enqueue phase, roughly two minutes in of a 1h budget. **No
verdict was produced and none should be inferred.** No measurement file was
written, which is why this directory still contains only `NOT_RE_RUN.md` and
this note.

## What the restart has to do first

The run seeded `hangar_gate` and died before cleaning up, so **5,000 identities
and 30,000 provisioning states are still in that database.** A restart that does
not drop and recreate it is measuring a p99 over a population it did not
construct — the throwaway-database rule exists for exactly this case. Reset the
gate database, then re-run:

```
make gate2 GATE_VERSION=v1.0.0-rc3
```

## What this means for the release

Unchanged from `NOT_RE_RUN.md`: `04_RELEASE_GATES.md` §8 blocks release on all
seven gates, and Gate 2 is recorded as PASSING at rc2 and **not measured at
rc3**. An interrupted run is not a failing run, but it is not a passing one
either, and it is not evidence of anything at all.
