# Gate 1's environment at rc3 — what else was on the box

§0 rule 3 asks for the environment, and `n1/environment.json` and `n3/environment.json` carry the
machine-readable part. This is the part a JSON field does not capture.

**The host was not perfectly quiet.** Before either run started, `hangar serve` was stopped
(trap 31), port 8080 was confirmed closed, `Get-Process | Where-Object { $_.ProcessName -like
'hangar*' }` returned nothing (trap 4 — the plain `-Name hangar` form matches none of the
`hangar-208`/`-final`/`-2011` spellings), and the corpus recorder's MySQL was stopped.

What remained running, and was **not** stopped because it belongs to a different project on this
machine, was six idle containers of an unrelated test stack — an Oracle, a PostgreSQL, a Redis and
an ElasticMQ, all reporting healthy and none under load.

**Why this is recorded rather than glossed.** Gate 1 measures a rate-limit ledger under
contention, and this file's sibling in `docs/gate-evidence/README.md` says in as many words that
"anything else heavy on the box is measuring the apparatus". Those containers were idle rather
than heavy, and both runs cleared their conditions with margin — the longest zero-throughput
interval was 1m30s and 1m0s against a 5m floor, and divergence was 0 across 40,133 group-samples.
But "idle" is a judgement and the honest thing is to say what was there rather than to claim a
bare machine.

The numbers are also directly comparable to rc2's, which were taken on the same host:

| | rc2 N=1 | rc3 N=1 | rc2 N=3 | rc3 N=3 |
| :-- | --: | --: | --: | --: |
| requests served | 1,143,649 | 1,132,172 | 1,145,965 | 1,146,665 |
| longest stall | 1m30s | 1m30s | 1m0s | 1m0s |
| divergence | 0 | 0 | 0 | 0 |

Within 1% on requests and identical on the stall, which is the condition that decides the gate.
