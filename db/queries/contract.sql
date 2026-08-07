-- app.contract, app.contract_item, app.contract_bid (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertContract :one
INSERT INTO app.contract AS t (
    owner_kind, owner_id, contract_id, issuer_id, issuer_corporation_id, assignee_id,
    acceptor_id, start_location_id, end_location_id, type, status, title,
    for_corporation, availability, date_issued, date_expired, date_accepted,
    days_to_complete, date_completed, price, reward, collateral, buyout, volume
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24
)
ON CONFLICT (owner_kind, owner_id, contract_id) DO UPDATE
   SET status = EXCLUDED.status, acceptor_id = EXCLUDED.acceptor_id,
       date_accepted = EXCLUDED.date_accepted, date_completed = EXCLUDED.date_completed,
       price = EXCLUDED.price, updated_at = now()
 WHERE (t.status, t.acceptor_id, t.date_accepted, t.date_completed, t.price)
    IS DISTINCT FROM
       (EXCLUDED.status, EXCLUDED.acceptor_id, EXCLUDED.date_accepted, EXCLUDED.date_completed, EXCLUDED.price)
RETURNING *;

-- name: GetContract :one
SELECT * FROM app.contract WHERE owner_kind = $1 AND owner_id = $2 AND contract_id = $3;

-- name: ListContractsPage :many
SELECT * FROM app.contract
 WHERE owner_kind = $1 AND owner_id = $2 AND contract_id > sqlc.arg(after_contract_id)
 ORDER BY contract_id
 LIMIT sqlc.arg(page_size);

-- name: UpsertContractItem :one
INSERT INTO app.contract_item AS t (
    owner_kind, owner_id, contract_id, record_id, type_id, quantity, raw_quantity,
    is_singleton, is_included, is_blueprint_copy, material_efficiency, time_efficiency, runs
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (owner_kind, owner_id, contract_id, record_id) DO UPDATE
   SET quantity = EXCLUDED.quantity, is_included = EXCLUDED.is_included
 WHERE (t.quantity, t.is_included) IS DISTINCT FROM (EXCLUDED.quantity, EXCLUDED.is_included)
RETURNING *;

-- name: ListContractItems :many
SELECT * FROM app.contract_item
 WHERE owner_kind = $1 AND owner_id = $2 AND contract_id = $3 ORDER BY record_id;

-- name: UpsertContractBid :one
INSERT INTO app.contract_bid AS t (owner_kind, owner_id, contract_id, bid_id, bidder_id, date_bid, amount)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (owner_kind, owner_id, contract_id, bid_id) DO UPDATE
   SET amount = EXCLUDED.amount
 WHERE t.amount IS DISTINCT FROM EXCLUDED.amount
RETURNING *;

-- name: ListContractBids :many
SELECT * FROM app.contract_bid
 WHERE owner_kind = $1 AND owner_id = $2 AND contract_id = $3 ORDER BY date_bid;
