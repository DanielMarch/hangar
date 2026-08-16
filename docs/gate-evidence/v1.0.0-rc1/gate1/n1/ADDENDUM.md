# Gate 1, N=1 — addendum: reading this run correctly

## The failure is real, and it is the point of the gate

Condition **1.6 failed**: the longest interval in which no request reached the proxy was
**3h58m**, against a `ttl_floor` of 5m, over 961 throughput samples. The installation served
13,257 requests and then stopped, permanently.

The cause is a deadlock in Governor 2's proactive pause, described in full in
`docs/PRE_V1_OPEN_ITEMS.md` §9: the only resume path is inside `applyHysteresis`, whose only
caller is `RecordError`, whose only caller is the response path of a request — so a paused
installation records no errors, never re-evaluates the resume threshold, and never resumes. The
database confirms it: `paused = t`, `error_count = 85`, `window_start` four hours old at the end of
the run.

**`1.6-raw` passed on the same run.** That is the harness's own implementation of condition 1.6,
which asks only whether the run served any request at all. Both readings are kept side by side
because the difference between them is the whole lesson: a gate condition whose implementation is
weaker than its wording will report a pass for the thing it was written to catch.

## Two caveats about this particular run

**The adversarial schedule fired all at once.** §1.3's offsets are relative to the injector's
start, and this run constructed the injector before migrating, ingesting the catalogue and seeding
5,000 characters — which together take longer than the schedule spans. So every injection was
already eligible when traffic began: `adversarial-log.jsonl` shows the whole table landing in the
opening seconds rather than across sixteen minutes. The runner now calls `Injector.Reset()` when
the measurement window opens, which is what that method exists for.

This changes *when* the budget was exhausted, not *whether* the installation recovered. An earlier
four-hour N=1 run, before the 1.6 measurement was corrected, exhausted the budget at minute 16 and
was equally stuck at the end of hour four — its `divergence.csv` covers 17 minutes of 240.

**`1.3-schedule` failed as a consequence, not a cause.** Five injections never fired: the last five
of the 85-request 4XX burst. Traffic had already stopped by the time they were due, because the
first eighty had tripped the pause. It is the stall showing up a second time, in a different
column.

## What passed, and is worth keeping

Everything about the ledger itself, over the window in which the installation was running:

* **1.1** — zero Governor 1 breaches. `breaches.json` is `null`, i.e. empty.
* **1.2** — `esi_420_total` peaked at 0. The proactive pause fired *before* ESI ever error-limited
  the installation, which is the property §1.3's error-budget row asks for. The pause working is
  not in question; only the resume.
* **1.3** — `max(esi_ledger_divergence) = 0` over 680 group-samples. The post-reconciliation
  residual was zero everywhere, every time it was observed.
* **1.3a** — `max(esi_ledger_prediction_error) = 0`, recorded and unbounded per the 20.4.1
  amendment.
* **1.8** — `solo` throughout, as §1.4 requires at this replica count.
