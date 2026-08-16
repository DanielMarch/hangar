# Gate 1, N=3 — addendum: what the clustered ledger proved, and what stopped it

## The clustered ledger did its job, and this run is the first evidence of that

Before the stall, this run put **150,452 requests** through three replicas sharing one Postgres and
one rate-limit ledger. Over that traffic:

* **1.1 — zero Governor 1 breaches.** `breaches.json` is empty. Three replicas issuing predictive
  reservations against shared buckets never admitted a request the server had no headroom for.
* **1.7 — zero overdrawn buckets.** No proxy-side sample showed aggregate consumption above
  `max_tokens` with three replicas sharing a bucket. This is the condition §1.4 exists for and the
  one a single-replica run cannot answer at all.
* **1.3 — `max(esi_ledger_divergence) = 0` over 3,739 group-samples.** The post-reconciliation
  residual was zero everywhere it was observed, at more than five times the sample count of the
  N=1 run.
* **1.3a — `max(esi_ledger_prediction_error) = 7`**, recorded and unbounded per the 20.4.1
  amendment. Seven tokens is a little over one in-flight 2XX pair on a fan-out group, which is what
  that amendment predicts and the reason it refuses to put a threshold on this quantity.

§8 of the release gates calls Gate 1 the most likely to fail first, "because shared-ledger
transaction correctness under real contention is easy to get subtly wrong". Under 150,000 requests
of real contention, it was not wrong.

## Both failures are the same defect, once

**1.6 — longest interval with no request reaching the proxy: 3h43m45s**, against a `ttl_floor` of
5m. Governor 2's proactive pause tripped roughly sixteen minutes in and the installation never
resumed. The deadlock is described in `docs/PRE_V1_OPEN_ITEMS.md` §9 and is identical to the one
the N=1 run hit.

**1.8 — `esi_ledger_mode` observed as `[clustered solo]`** — and this is a *consequence* of 1.6,
not an independent finding. The timeline, from `transition-log.jsonl` and the replica logs:

| time | event |
| :-- | :-- |
| 09:23:07 | all three replicas log `mode transition from=solo to=clustered live_replicas=3` — **once each, and never again** |
| 09:23:08 | traffic starts |
| ~09:39 | the error budget is exhausted, the proactive pause trips, traffic stops |
| 11:23:07 | §1.4's replica kill |
| 11:24:37 | §1.4's replica restart — **two hours into the stall** |

The restarted replica came up into an installation that had not called ESI for two hours. With no
work to claim it never called `Acquire`, so it never consulted the replica registry, so it reported
`Governor1`'s optimistic `solo` default for the remaining two and a half hours. Its log carries a
single mode transition — the one from before it was killed.

So mode selection was **correct every time it was performed**: three replicas, three transitions to
`clustered` at `live_replicas=3`, no flapping. What 1.8 caught is the gauge reporting a default
(`docs/PRE_V1_OPEN_ITEMS.md` §8), surfaced by a replica that the *pause deadlock* left with nothing
to do.

## Reading the two runs together

N=1 served 13,257 requests before stalling; N=3 served 150,452. The difference is not the replica
count — it is that the N=3 run had `Injector.Reset()` and therefore spent its error budget across
sixteen minutes of real traffic rather than in the opening seconds. Both stalled, both stayed
stalled, and neither recovered. That is the finding.
