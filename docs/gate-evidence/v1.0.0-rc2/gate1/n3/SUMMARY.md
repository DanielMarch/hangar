# Gate 1 — ESI Load Stability

**Verdict: PASS**

4h0m0s at N=3 against the recording proxy: 5000 characters, 225008 enabled sync subscriptions.

| | |
| :-- | :-- |
| Release | `v1.0.0-rc2` |
| Started | 2026-08-17T09:34:52Z |
| Finished | 2026-08-17T13:34:55Z |
| Duration | 4h0m1s |
| Characters | 5000 |
| Error budget | 100 per 1m0s, pause at 20, resume at 60 |
| Host | windows/amd64, 16 CPUs |
| Replicas | 3 |
| Requested duration | 4h0m0s |
| Sync subscriptions | 225008 |
| TTL floor | 5m0s |

## Pass conditions

| # | Condition | Verdict | Measurement |
| :-- | :-- | :-- | :-- |
| 1.1 | Zero Governor 1 breaches | **pass** | proxy recorded 0 requests admitted with available <= 0 |
| 1.2 | Zero Governor 2 breaches | **pass** | esi_420_total peaked at 0 |
| 1.3 | Ledger divergence == 0 tokens after reconciliation, per group | **pass** | max(esi_ledger_divergence) = 0 (group "char-asset") over 30232 group-samples |
| 1.3a | Ledger prediction error recorded (no threshold) | **pass** | max(esi_ledger_prediction_error) = 7 (group "char-location") over 30232 group-samples; recorded as evidence, not bounded |
| 1.4 | Proactive error-limit pause fired, and no 420 followed | **pass** | esi_error_limit_remaining reached 15 against a pause threshold of 20; esi_420_total = 0 |
| 1.5 | Failure stayed scoped | **pass** | 1145965 requests served, 1145851 of them 200 while adversarial conditions were active |
| 1.6 | No stall — throughput never drops to zero for more than one ttl_floor | **pass** | longest interval with no request reaching the proxy: 1m0s, against a ttl_floor of 5m0s, over 962 throughput samples |
| 1.6-raw | the weaker reading 1.6 used to be — whether the run served any request AT ALL. Kept so the difference stays visible | **pass** | 1145965 requests reached the proxy over 4h0m1s |
| 1.7 | Aggregate consumption respected at N>1 | **pass** | 0 proxy-side samples showed consumption above max_tokens (replicas=3) |
| 1.8 | Mode selection correct throughout | **pass** | esi_ledger_mode observed as [clustered], expected "clustered" throughout |
| 1.4-transition | the mid-run replica kill and restart happened (§1.4) | **pass** | 1 killed, 1 restarted; recorded in transition-log.jsonl |

## Artefacts

| File | Contents |
| :-- | :-- |
| `adversarial-log.jsonl` | every §1.3 condition the proxy injected and the response it produced. |
| `aggregate-consumption.csv` | proxy-side view of total consumption per bucket — condition 1.7's evidence at N>1. |
| `breaches.json` | Governor 1 violations recorded by the proxy. §1.2 condition 1.1 requires this to be EMPTY. |
| `conditions.json` | the per-condition verdicts in machine-readable form. |
| `divergence.csv` | per-minute, per-group max ledger divergence (the post-reconciliation residual, bound 0) beside max prediction error (recorded, unbounded). |
| `environment.json` | §0 rule 3's environment record. |
| `logs/` | each replica's stdout and stderr for the whole run. |
| `metrics.prom` | the final Prometheus scrape, verbatim. |
| `transition-log.jsonl` | §1.4's mid-run replica kill and restart. |

## Notes

The proxy is an independent measurement, not a mirror of the client: it restates §5.5's cost table from the server's side rather than importing internal/esi/ratelimit's, because a gate that shares its implementation with the thing it measures is not a measurement.

The sync planner runs inside the runner rather than as a `hangar serve` or `hangar schedule` process. That is not a convenience: every role writes a heartbeat into app.esi_replica and CountLiveReplicas counts rows regardless of role, so a planner process would make this run report one more live replica than it has — and at N=1 that is the difference between the solo path §1.4 requires and the clustered one.

At N=3 this run exercises the CLUSTERED ledger: the shared-ledger transaction, condition 1.7's aggregate budget across three replicas sharing one bucket, and acquire latency under real contention. §1.4 requires both results; neither alone is sufficient.
