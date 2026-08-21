-- name: UpsertEsiScope :exec
-- scope is opaque and never parsed (SRS v3.1 §4.5). Any string ESI hands
-- back in a security block is recorded, first-seen-wins.
INSERT INTO app.esi_scope (scope)
VALUES ($1)
ON CONFLICT (scope) DO NOTHING;

-- name: ListUnacknowledgedEsiScopes :many
SELECT * FROM app.esi_scope WHERE acknowledged_at IS NULL ORDER BY first_seen_at;

-- name: AcknowledgeEsiScope :exec
UPDATE app.esi_scope SET acknowledged_at = now() WHERE scope = $1;

-- name: GetEsiScope :one
-- ── PHASE 23 (N-4): A DELIBERATE KEEP, AND WHY ──────────────────────────
--
-- No production caller, and it stays. It was briefly deleted this phase on
-- the reasoning that nothing in HANGAR wants ONE scope row — the board
-- lists the unacknowledged ones and the acknowledge endpoint takes a name
-- and writes — and that was wrong, because it is not the product that
-- wants it.
--
-- It is GATE 6's assertion instrument. TestGate6NovelScopeGrammar reads
-- back `esi::synthetic~widget/read@v3` through this query to prove §6.1's
-- novel scope grammar was recorded rather than rejected, and
-- TestUnknownScopeStaysAcknowledged reads it to prove UpsertEsiScope's ON
-- CONFLICT does not reset acknowledged_at and refill the board.
--
-- The alternative is those tests issuing raw SQL, which is strictly worse:
-- a test that queries the table directly passes while the query layer that
-- production would use is broken, which is the exact gap between "the data
-- is there" and "HANGAR can read it" that Gate 6 exists to close.
--
-- Same category as UpsertReplicaHeartbeat below and internal/rbac.
-- ResolveLive in the sibling allowlist: a measurement, not a mechanism.
SELECT * FROM app.esi_scope WHERE scope = $1;