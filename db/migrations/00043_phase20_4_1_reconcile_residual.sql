-- +goose Up
-- Phase 20.4.1 — Gate 1.3 gets the quantity its own sentence names, and the
-- quantity 20.4 shipped keeps its own name.
--
-- ── WHAT 20.4 MEASURED, AND WHY IT IS NOT §1.3's QUANTITY ────────────────
-- Migration 00042 fixed a real defect: esi_ledger_divergence was
-- subtracting a LIVE local count from a STORED server snapshot, and
-- reported 40-55 on a healthy installation against a tolerance of 1. The
-- fix — persisting the local half in the same statement as the server half,
-- under the reconciler's own bucket lock — was correct and stands.
--
-- Watching the fixed gauge for longer produced this, on the live
-- installation at 14-68 in-flight jobs:
--
--     max |local_at_reading - server_remaining|                = 18
--     max |live_local       - server_remaining|, readings <=5s =  0
--
-- corp-contract held 18 for minutes. Reading that bucket out explains it
-- exactly: 8 settled entries of cost 1 (all 304s) = 8 tokens, against a
-- server reporting 26 consumed, plus one SYNTHETIC entry of cost 18
-- injected by the reconcile that observed the gap. The ledger CONVERGED —
-- it always does; "the server always wins" (§5.5) works — and the residual
-- after that correction was 0.
--
-- So the pair stored by 00042 measures HANGAR's PREDICTION ERROR: how far
-- HANGAR's independent accounting had drifted from the server's before the
-- correction. That is a real quantity and it is the one that surfaced the
-- window question. It is structurally non-zero, for two measured reasons
-- that add:
--
--   * at reconcile time HANGAR deliberately excludes in-flight reservations
--     (SumSettledLedgerEntryCost), so k sibling requests ESI has already
--     processed and HANGAR has not yet settled put up to 5k tokens between
--     the two operands. corp-contract is a fan-out group
--     (/corporations/{id}/contracts/{contract_id}/items), which is why it,
--     and not a single-request group, was the bucket holding 18;
--   * ESI's window has the same LENGTH as HANGAR's and a longer TAIL —
--     measured in this phase, it behaves as a sliding-window counter over
--     fixed 15-minute wall-clock windows, so between 900 s and 1800 s after
--     a request it is still charging a fraction HANGAR has released. See
--     01_ARCHITECTURE.md §5.5.
--
-- A bound of 1 on that quantity is not reachable by any implementation of
-- predictive reservation against a server that accounts this way, and
-- lowering the bar until it is reachable would be picking a number to make
-- a number pass.
--
-- ── WHAT §1.3 ACTUALLY NAMES ─────────────────────────────────────────────
-- "Zero divergence between the local ledger and X-Ratelimit-Remaining
-- beyond a one-request tolerance" is a statement about whether the ledger
-- AGREES with the server, which is the state AFTER reconciliation. 20.4
-- rejected storing that as "vacuous — it reads ~0 by construction on a
-- healthy and a broken installation alike". That rejection was incomplete.
-- It reads 0 while the reconciler CONVERGES, and non-zero exactly when it
-- cannot: nothing left to evict, or an injection the ledger could not
-- absorb. A metric that reads 0 until convergence fails is a real signal,
-- not a dead one — it is the same shape as esi_420_total, which Gate 1.2
-- reads and which is also 0 on every healthy run.
--
-- Both quantities are therefore kept, under two names, because they answer
-- two different operator questions and conflating them is what made this
-- take three phases to see:
--
--   esi_ledger_divergence{group}       post-correction residual — the gate
--   esi_ledger_prediction_error{group} pre-correction gap — recorded only
--
-- ── THE RESIDUAL'S BOUND IS 0, AND THAT REQUIRED A CODE CHANGE ───────────
-- Injection closes the gap exactly (a synthetic entry of arbitrary cost).
-- Eviction did not: evictUntil/evictOldestUntil deleted whole entries until
-- availability REACHED the target, so a cost-5 entry at the head of the
-- queue closing a 1-token gap over-forgave 4 tokens. That is not only a
-- 4-token floor under the gate's metric — it is over-forgiveness in the
-- direction that CAUSES breaches, since it leaves HANGAR believing it has
-- headroom the server has not granted (Gate condition 1.1). Both ledgers
-- now reduce the boundary entry's cost by exactly the remainder instead of
-- deleting it whole, so convergence is exact in both directions and the
-- residual's bound is 0 rather than 4.
ALTER TABLE app.esi_ledger_bucket
    ADD COLUMN local_remaining_after_reading integer;

COMMENT ON COLUMN app.esi_ledger_bucket.local_remaining_after_reading IS
    'HANGAR''s own remaining headroom immediately AFTER the reconciliation correction, under the same bucket lock that wrote local_remaining_at_reading and server_remaining. esi_ledger_divergence (04_RELEASE_GATES.md §1.3, as amended by Phase 20.4.1) is |this - least(server_remaining, max_tokens)|, and its bound is 0: non-zero means the reconciler COULD NOT converge. NULL means no reading, which is not a divergence of zero. Phase 20.4.1.';

COMMENT ON COLUMN app.esi_ledger_bucket.local_remaining_at_reading IS
    'HANGAR''s own remaining headroom as it stood at the instant server_remaining was recorded, under the same bucket lock — BEFORE the reconciliation correction. Paired with server_remaining it is esi_ledger_prediction_error, which is RECORDED and not bounded: it is structurally proportional to the number of sibling requests in flight in this bucket, because reconciliation deliberately excludes reservations. NULL means no reading, which is not a prediction error of zero. Phase 20.4, re-scoped in 20.4.1.';

-- +goose Down
ALTER TABLE app.esi_ledger_bucket
    DROP COLUMN local_remaining_after_reading;
