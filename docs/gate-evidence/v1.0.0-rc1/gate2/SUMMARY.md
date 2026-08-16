# Gate 2 — Revocation SLO

**Verdict: PASS**

p99 of (platform_call_completed_at − event_at) over 15000 successful revocations: 0.134s against a 60s SLO.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc1` |
| Started | 2026-08-15T23:48:01Z |
| Finished | 2026-08-16T00:19:52Z |
| Duration | 31m51s |
| Drain budget | 1h0m0s |
| Event arrival | spread evenly across 30m0s |
| Host | windows/amd64, 16 CPUs |
| Identities | 5000 |
| Platforms | 3 (discord, teamspeak, mumble — rate-limited stubs) |
| Provisioning states | 30000 |
| Queue budgets | provision-urgent 32 workers, provision-bulk 8 workers |

## Pass conditions

| # | Condition | Verdict | Measurement |
| :-- | :-- | :-- | :-- |
| 2.1-sample | the run produced enough successful revocations to have a p99 | **pass** | 15000 successful revocations, minimum 5000 |
| 2.1 | p99 of (platform_call_completed_at - event_at) < 1m0s | **pass** | p50=0.038s p95=0.094s p99=0.134s max=0.324s over 15000 revocations |
| 2.3 | revocations still owed remain visible with their true age | **pass** | 0 pending, oldest 0.0s old |
| 2.4 | provision-urgent was never starved by provision-bulk — the p99 above was measured with bulk saturated | **pass** | 3 bulk reconcile jobs completed during the run; urgent queue drained: true |

## Artefacts

| File | Contents |
| :-- | :-- |
| `conditions.json` | the per-condition verdicts in machine-readable form. |
| `latencies.csv` | every measured revocation's latency in seconds, so the quantile can be recomputed independently. |
| `latency-report.json` | the full distribution (p50/p95/p99/max), outcome counts and pending revocations — the blocking artefact. |
| `platform-calls.csv` | per-platform grant and revoke counts served by the rate-limited stubs. |

## Notes

The verdict comes from app.provisioning_audit with SQL, never from provisioning_revocation_latency_seconds. A Prometheus histogram answers a p99 by interpolating within a bucket and 60s is exactly a boundary, and a gate that took its verdict from the application's own instrumentation would be asking the system whether it thinks it passed. The metric's outcome counts are recorded in platform-calls.csv as a cross-check.

Only SUCCESSFUL revocations are in the distribution. A failed platform call did not remove the group — the exposure is still open — so counting how fast it failed as a revocation latency would be the most flattering possible reading of the case the SLO exists to bound.

Conditions 2.2 (every trigger enqueues in the mutating transaction) and 2.5 (rolling back the mutation rolls back the job) are not evaluated here. Both are statements about code paths rather than about a run's numbers, and both are asserted directly by test/load/gate2_integration_test.go and internal/provisioning's own suite. Reporting them as passed because a run completed would be inventing evidence.
