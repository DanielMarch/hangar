# Gate 2 — addendum: what "bulk saturated" actually measured

Condition 2.4's own measurement line reads *"3 bulk reconcile jobs completed during the run"*, and
on its own that reads like an idle queue. It is not what happened, and the difference matters
because §2.4 is the condition that makes the p99 mean anything.

`provision_bulk` is **ByArgs-unique per platform**: enqueueing a reconcile for a platform that
already has one pending is deduplicated rather than stacked. So the job COUNT is the number of
full passes that ran to completion, not the amount of work done. Three completed passes over three
platforms is one full reconcile each, and each of those passes walked its whole population.

The work is visible in `app.provisioning_audit`, read from the run's own database:

| action | rows | first event | last completion |
| :-- | --: | :-- | :-- |
| `reconcile` (bulk path) | **15,000** | 23:48:01Z | 23:58:34Z |
| `revoke` (urgent path) | **15,000** | 23:48:16Z | 00:19:46Z |

So the bulk path performed **15,000 rate-limited platform calls of its own**, concurrently with the
urgent path's 15,000, sharing one River client, one worker budget split (32 urgent / 8 bulk) and
one database. The first urgent revocation was enqueued 15 seconds after the bulk pass began and
the bulk pass was still running for the next ten minutes.

That is the contention §2.4 asks for, and it is why the p99 of **0.134s** against a 60-second SLO
is a measurement rather than a formality.

---

*This file is an addendum rather than a corrected `SUMMARY.md` because the summary is the
runner's own output and re-running an hour of measurement to reword one line would be a worse
trade than saying plainly what the line meant.*
