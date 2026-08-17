# Gate 3, first attempt at `v1.0.0-rc2` — a FAIL that measured the apparatus

This directory is kept deliberately. It is a recorded **FAIL**, and it is not a finding about
HANGAR: it is what Gate 3 reports when it runs a second time against a database that still holds
the first run's rows. The corrected run is in `../gate3/`.

## What it reported

| # | Verdict | Measurement |
| :-- | :-- | :-- |
| 3.1-sample | pass | 15,120 occurrences offered, **2,520** events in the database |
| 3.1-categories | **FAIL** | 1 distinct category, 1 distinct domain (minimum 3 categories) |
| 3.1-occurrences | pass | 15,120 offered = 2,520 written + **12,600 deduplicated** |
| 3.1 | pass | 5,040 enqueued = 0 sent + 0 coalesced + 5,040 dead-lettered + 0 pending |
| 3-domains | **FAIL** | 1 of 8 domains; missing characters, platform, wars, corporations, sovereignty, contracts, alliances |
| 3.7 | **FAIL** | 5,040 dead-lettered from the down channels while **0** messages went out on the healthy ones |
| 3.4 | **FAIL** | largest roll-up accepted by a channel carried **0** events |

## Why, re-derived from the database rather than inferred

`hangar_gate3` at the end of the run held **15,974** events, **8** channels, **110** routing rules
and **18,494** deliveries — that is v1.0.0-rc1's run plus this one, in one database.

* **12,600 of 15,120 occurrences were deduplicated.** `app.alert_event.dedupe_hash` is a permanent
  uniqueness constraint, correctly so (§4.4: re-reading the same notification on the next poll is
  the common case, not an error). The generator produces deterministic content, so on a second run
  against the same database it produces nothing new. Only the **threshold** category still fired,
  because its subjects are re-evaluated rather than replayed — hence 1 category and 1 domain.
* **0 messages to a healthy channel.** Routing deals the four stub behaviours round-robin across
  the catalogue's alert types. `corporation.structure.fuel_low` — the only type that fired — lands
  on the *permanently failing* stub. Every one of its 2,520 events went to two permanent channels
  (this run's and rc1's leftover), and both dead-lettered. 3.7 and 3.4 follow arithmetically.

## The part that matters most, because it is a pass rather than a failure

`3.1-scheduled-beyond-run` reported **pass, 0 deliveries scheduled beyond the run** — and it
measured nothing.

`seedEntities` uses `ON CONFLICT DO NOTHING`, so the structures kept rc1's `fuel_expires` values:
minutes out when rc1 wrote them, and **two days expired** by the time this run read them. A
deadline in the past is precisely the case where B-9's `min(bucket + window, now + window)` cap
cannot engage, so the condition that exists to test the fix passed without exercising it.

A false pass is worse than a false failure, and this directory exists mostly to record that one.

## What changed

`tools/gate3-alerts/world.go` gained `world.reset`, which truncates the alerting tables and the
threshold subject tables before seeding — the same discipline `tools/gate1-load` already applies
when it clears `app.esi_replica`, and for the same reason. **A gate whose verdict depends on
whether it has been run before is not evidence.**
