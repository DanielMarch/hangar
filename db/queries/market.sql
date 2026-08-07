-- app.market_order, app.market_order_history, app.market_history,
-- app.market_price (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertMarketOrder :one
INSERT INTO app.market_order AS t (
    owner_kind, owner_id, order_id, type_id, region_id, location_id, range,
    is_buy_order, is_corporation, escrow, price, volume_total, volume_remain,
    min_volume, duration, issued, wallet_division
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (owner_kind, owner_id, order_id) DO UPDATE
   SET volume_remain = EXCLUDED.volume_remain, escrow = EXCLUDED.escrow, updated_at = now()
 WHERE (t.volume_remain, t.escrow) IS DISTINCT FROM (EXCLUDED.volume_remain, EXCLUDED.escrow)
RETURNING *;

-- name: ListMarketOrdersByOwner :many
SELECT * FROM app.market_order WHERE owner_kind = $1 AND owner_id = $2 ORDER BY issued DESC;

-- name: DeleteMarketOrdersNotIn :exec
-- An order that vanished from the live page is closed/expired/cancelled; it
-- is projected into market_order_history by the sync engine before this
-- runs, so removing it from the open-orders table is not a data-loss delete.
DELETE FROM app.market_order
 WHERE owner_kind = $1 AND owner_id = $2 AND NOT (order_id = ANY(sqlc.arg(keep_order_ids)::bigint[]));

-- name: UpsertMarketOrderHistory :one
INSERT INTO app.market_order_history AS t (
    owner_kind, owner_id, order_id, type_id, region_id, location_id, range,
    is_buy_order, is_corporation, escrow, price, volume_total, volume_remain,
    min_volume, duration, issued, state, wallet_division
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (owner_kind, owner_id, order_id) DO UPDATE
   SET state = EXCLUDED.state, volume_remain = EXCLUDED.volume_remain, updated_at = now()
 WHERE (t.state, t.volume_remain) IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.volume_remain)
RETURNING *;

-- name: ListMarketOrderHistoryByOwner :many
SELECT * FROM app.market_order_history WHERE owner_kind = $1 AND owner_id = $2 ORDER BY issued DESC;

-- name: UpsertMarketHistory :one
INSERT INTO app.market_history AS t (region_id, type_id, date, average, highest, lowest, order_count, volume)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (region_id, type_id, date) DO UPDATE
   SET average = EXCLUDED.average, highest = EXCLUDED.highest, lowest = EXCLUDED.lowest,
       order_count = EXCLUDED.order_count, volume = EXCLUDED.volume
 WHERE (t.average, t.highest, t.lowest, t.order_count, t.volume)
    IS DISTINCT FROM (EXCLUDED.average, EXCLUDED.highest, EXCLUDED.lowest, EXCLUDED.order_count, EXCLUDED.volume)
RETURNING *;

-- name: ListMarketHistory :many
SELECT * FROM app.market_history WHERE region_id = $1 AND type_id = $2 ORDER BY date DESC LIMIT sqlc.arg(page_size);

-- name: UpsertMarketPrice :one
INSERT INTO app.market_price AS t (type_id, adjusted_price, average_price)
VALUES ($1,$2,$3)
ON CONFLICT (type_id) DO UPDATE
   SET adjusted_price = EXCLUDED.adjusted_price, average_price = EXCLUDED.average_price, updated_at = now()
 WHERE (t.adjusted_price, t.average_price) IS DISTINCT FROM (EXCLUDED.adjusted_price, EXCLUDED.average_price)
RETURNING *;

-- name: ListMarketPrices :many
SELECT * FROM app.market_price ORDER BY type_id;
