-- +goose Up
-- Phase 20.4 — esi_ledger_divergence stops subtracting two different moments.
--
-- ── THE DEFECT, MEASURED THREE TIMES NOW ─────────────────────────────────
-- 04_RELEASE_GATES.md §1.3 bounds max(|local_remaining − server_remaining|)
-- at 1 per rate-limit group. Both readers of that quantity —
-- internal/telemetry's gatewayCollector and internal/api/v1's
-- ledgerDivergenceHandler — computed `local_remaining` LIVE, by summing
-- app.esi_ledger_entry at SCRAPE time, while `server_remaining` was
-- whatever the last response carrying X-Ratelimit-Remaining had stored on
-- this row. Every reconcile makes the two agree; every settle afterwards
-- moves local away and leaves server where it was. The gauge therefore
-- reported (requests settled since that reading) × 2 — throughput, not
-- ledger accuracy.
--
-- Measured on the live installation immediately after Phase 20.3 landed,
-- sampling /metrics every 3 s for two minutes: char-social 55,
-- char-detail 51, corp-industry 50, char-industry 46, corp-detail 43,
-- corp-member 42, char-wallet 21, corp-wallet 20 — each persisting 3-6 s,
-- against a tolerance of 1. Ten samples at steady state (~4.3 jobs/s, one
-- character) read max 1, which is exactly why this hid: the defect is
-- invisible unless the same bucket is busy.
--
-- This is the THIRD instance of one defect class, and the first two fixes
-- cannot reach it. Phase 20.2's DivergenceRow.readingIsCurrent drops a
-- reading older than one window; Phase 20.2's SumSettledLedgerEntryCost
-- excludes in-flight reservations. Neither helps, because these operands
-- are not stale and not reservation-contaminated — they are simply from
-- different moments, microseconds apart, and no freshness rule has that
-- resolution.
--
-- ── THE FIX IS A DEFINITION CHANGE, NOT A PATCH ──────────────────────────
-- Stop comparing a live count with a snapshot. The reconciler already
-- computes local availability under the bucket's own FOR UPDATE lock, one
-- statement before it stores the server's reading — the two numbers are as
-- close to simultaneous as this system can make them, and they are the
-- exact pair `reconcileAction` judges. Persisting the local half alongside
-- the server half turns esi_ledger_divergence from "how much has happened
-- since the last reading" into "how far apart were the ledger and the
-- server the last time both were known", which is the quantity §1.3 is
-- about.
--
-- What this deliberately does NOT store is the local reading AFTER the
-- correction. Reconcile exists to make local agree with server, so a
-- post-correction pair would read ~0 by construction on a healthy AND on a
-- badly broken installation alike — a metric that cannot move is not a
-- measurement, and Gate 1.3 would become a vacuous pass of exactly the kind
-- §3.1's "zero dropped on an empty run" warns about.
ALTER TABLE app.esi_ledger_bucket
    ADD COLUMN local_remaining_at_reading integer;

COMMENT ON COLUMN app.esi_ledger_bucket.local_remaining_at_reading IS
    'HANGAR''s own remaining headroom as it stood at the instant server_remaining was recorded, under the same bucket lock — BEFORE the reconciliation correction. The other operand of esi_ledger_divergence (04_RELEASE_GATES.md §1.3). NULL means no reading, which is not a divergence of zero. Phase 20.4.';

COMMENT ON COLUMN app.esi_ledger_bucket.server_remaining IS
    'Last authoritative X-Ratelimit-Remaining. Paired with local_remaining_at_reading, written in the same statement so the two describe one instant. Phase 20.4.';

-- +goose Down
ALTER TABLE app.esi_ledger_bucket
    DROP COLUMN local_remaining_at_reading;
