# Gate 3 — Alert Delivery Integrity

**Verdict: PASS**

16380 occurrences offered, 13454 events, 13454 deliveries; 0 left neither delivered nor dead-lettered.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc3` |
| Started | 2026-08-21T02:54:08Z |
| Finished | 2026-08-21T06:44:54Z |
| Duration | 3h50m46s |
| Channels | 4 stubs: healthy, transiently-failing, permanently-failing, always-down |
| Coalescing window | 5m0s |
| Dispatch interval | 15s |
| Drain window | 2h15m18s |
| Generate window | 1h44m42s |
| Host | windows/amd64, 16 CPUs |
| Retry policy | max 8 attempts, base 1m0s, cap 1h0m0s, dead-letter after 24h0m0s |

## Pass conditions

| # | Condition | Verdict | Measurement |
| :-- | :-- | :-- | :-- |
| 3.1-sample | the run actually generated alerts — "zero dropped" is meaningless over an empty pipeline | **pass** | 16380 occurrences offered (minimum 1000), 13454 events in the database |
| 3.1-categories | all three §4.4 alert categories were exercised (esi_notification, domain_event, threshold) | **pass** | 3 distinct categories, 9 distinct domains (minimum 3 categories) |
| 3.1-occurrences | offered == events_written + suppressed_by_dedupe | **pass** | 16380 offered, 13454 written + 2926 deduplicated = 16380 |
| 3.1-events-persisted | every event the emitter reported writing is in the database | **pass** | emitter reported 13454 events written, database holds 13454 |
| 3.1 | enqueued == messages_sent + coalesced_into + dead_lettered + pending (the §3.1 accounting identity) | **pass** | 13454 enqueued = 595 messages + 5190 coalesced + 7669 dead-lettered + 0 pending + 0 failed |
| 3.1-dropped | no delivery is left neither sent nor dead-lettered at end of run — §3.1's definition of a drop | **pass** | 0 still pending, 0 in the unreachable 'failed' state |
| 3.1-actionable | every generated event has at least one delivery — an event with none can never be acted on | **pass** | 0 of 13454 events have no delivery row |
| 3.2 | unrecognised CCP notification types reached the unknown-types board | **pass** | 42 unknown types boarded during the run |
| 3-domains | the run exercised all eight §4.4 domains | **pass** | 8 of 8 domains produced events |
| 3.7 | channel outages produced retries then dead-letters, never queue blockage | **pass** | 7669 dead-lettered from the down channels while 595 messages went out on the healthy ones |
| 3.1-scheduled-beyond-run | no delivery is scheduled to become claimable after the run ends (see the note — this is about early warnings arriving late, not about drops) | **pass** | 0 deliveries have next_attempt_at after the end of the run |
| 3.4 | coalesced events arrived as ONE message per group (no query can see this — it is read from the channel stub) | **pass** | largest roll-up accepted by a channel carried 11 events |

## Artefacts

| File | Contents |
| :-- | :-- |
| `accounting.json` | both identities — occurrences and deliveries — with every term counted independently. The blocking artefact. |
| `channel-log.csv` | per-stub attempts, messages accepted and the largest roll-up each received (§3.4's evidence). |
| `conditions.json` | the per-condition verdicts in machine-readable form. |

## Notes

Every OUTCOME term is counted from app.alert_event and app.alert_delivery with SQL. The INPUT term cannot be: an occurrence that deduplicated leaves no database trace at all — that is what RecordAlertEvent's ON CONFLICT (dedupe_hash) DO NOTHING means — so the harness counts what it fed in. That is not the system reporting on itself; it is the test's own tally of its own input, and checking it against the tables is the point of the identity.

Conditions 3.3, 3.5, 3.6 and 3.8 are not evaluated from this run's row counts. Each is a statement about a code path or a rendered string rather than about a run's totals, and each is asserted by a test that can actually see the thing: internal/alerting's suite, catalogue's ValidateThresholds under `make check-alert-sources`, and internal/esi/ratelimit's admission tests. Reporting them as passed because a run completed would be inventing evidence.

§3.3's unparseable-YAML case IS exercised here — the generator feeds notification text no strict parser accepts, and the queue must not halt on it — but what that proves from this side is that generation continued, not that the render fell back correctly.
