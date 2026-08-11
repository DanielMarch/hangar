-- app.wallet_balance, app.wallet_journal, app.wallet_transaction
-- (02_DATABASE_SCHEMA.md §5.3). journal and transaction are partitioned by
-- date; the partition key rides along in every WHERE/ON CONFLICT target.

-- name: UpsertWalletBalance :one
INSERT INTO app.wallet_balance AS t (owner_kind, owner_id, division, balance)
VALUES ($1, $2, $3, $4)
ON CONFLICT (owner_kind, owner_id, division) DO UPDATE
   SET balance = EXCLUDED.balance, updated_at = now()
 WHERE t.balance IS DISTINCT FROM EXCLUDED.balance
RETURNING *;

-- name: GetWalletBalance :one
SELECT * FROM app.wallet_balance WHERE owner_kind = $1 AND owner_id = $2 AND division = $3;

-- name: ListWalletBalances :many
SELECT * FROM app.wallet_balance WHERE owner_kind = $1 AND owner_id = $2 ORDER BY division;

-- name: UpsertWalletJournalEntry :one
-- Journal rows are immutable once posted by CCP; ON CONFLICT DO UPDATE is
-- retained only to make a re-synced page a no-op rather than a constraint
-- error (the WHERE guard means it can never actually change a value).
INSERT INTO app.wallet_journal AS t (
    owner_kind, owner_id, division, journal_id, ref_type, amount, balance, tax,
    tax_receiver_id, first_party_id, second_party_id, context_id, context_id_type,
    reason, description, date
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
)
ON CONFLICT (owner_kind, owner_id, journal_id, date) DO UPDATE
   SET amount = EXCLUDED.amount, balance = EXCLUDED.balance, tax = EXCLUDED.tax,
       description = EXCLUDED.description
 WHERE (t.amount, t.balance, t.tax, t.description)
    IS DISTINCT FROM (EXCLUDED.amount, EXCLUDED.balance, EXCLUDED.tax, EXCLUDED.description)
RETURNING *;

-- name: ListWalletJournalPage :many
SELECT * FROM app.wallet_journal
 WHERE owner_kind = $1 AND owner_id = $2 AND division = $3
   AND (date, journal_id) < (sqlc.arg(before_date), sqlc.arg(before_journal_id))
 ORDER BY date DESC, journal_id DESC
 LIMIT sqlc.arg(page_size);

-- name: UpsertWalletTransaction :one
INSERT INTO app.wallet_transaction AS t (
    owner_kind, owner_id, division, transaction_id, client_id, date, is_buy,
    is_personal, journal_ref_id, location_id, quantity, type_id, unit_price
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
ON CONFLICT (owner_kind, owner_id, transaction_id, date) DO UPDATE
   SET quantity = EXCLUDED.quantity, unit_price = EXCLUDED.unit_price
 WHERE (t.quantity, t.unit_price) IS DISTINCT FROM (EXCLUDED.quantity, EXCLUDED.unit_price)
RETURNING *;

-- name: ListWalletTransactionsPage :many
SELECT * FROM app.wallet_transaction
 WHERE owner_kind = $1 AND owner_id = $2 AND division = $3
   AND (date, transaction_id) < (sqlc.arg(before_date), sqlc.arg(before_transaction_id))
 ORDER BY date DESC, transaction_id DESC
 LIMIT sqlc.arg(page_size);

-- name: AggregateWalletJournalByRefType :many
-- PHASE 15.1 — backs SRS §6.3's `/ledger/bounties` and `/ledger/pi`.
--
-- Phase 15 answered 501 on both with "derived from wallet journal ref_type
-- filtering, not wired in this phase". That description was right; this is
-- the derivation. Both ledgers are the same shape — "who generated how
-- much of this kind of income for the corporation" — so one parameterised
-- query serves both rather than two near-identical ones, with the caller
-- supplying the ref_type set (internal/api/v1/corporations.go).
--
-- ref_type is an OPEN vocabulary (Principle 14, app.open_vocabulary's
-- `ref_type`): CCP adds new ones without notice, so the set is passed in
-- as data rather than baked into the SQL, and an unrecognised ref_type
-- simply doesn't match rather than breaking the query.
--
-- total_amount is NUMERIC and therefore decimal.Decimal on the Go side
-- (sqlc.yaml's Principle 9 override) — a JSON string on the wire, never a
-- float.
SELECT first_party_id,
       ref_type,
       COUNT(*)::bigint                        AS entry_count,
       COALESCE(SUM(amount), 0)::numeric(30,2) AS total_amount
  FROM app.wallet_journal
 WHERE owner_kind = $1
   AND owner_id   = $2
   AND ref_type   = ANY(sqlc.arg(ref_types)::text[])
 GROUP BY first_party_id, ref_type
 ORDER BY total_amount DESC;
