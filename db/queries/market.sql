-- app.market_order, app.market_order_history, app.market_history,
-- app.market_price (02_DATABASE_SCHEMA.md §5.2).

-- name: UpsertMarketOrder :one
INSERT INTO app.market_order AS t (
    owner_kind, owner_id, order_id, type_id, region_id, location_id, range,
    is_buy_order, is_corporation, escrow, price, volume_total, volume_remain,
    min_volume, duration, issued, wallet_division, issued_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (owner_kind, owner_id, order_id) DO UPDATE
   SET volume_remain = EXCLUDED.volume_remain, escrow = EXCLUDED.escrow,
       issued_by = EXCLUDED.issued_by, updated_at = now()
 WHERE (t.volume_remain, t.escrow, t.issued_by)
    IS DISTINCT FROM (EXCLUDED.volume_remain, EXCLUDED.escrow, EXCLUDED.issued_by)
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
    min_volume, duration, issued, state, wallet_division, issued_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
ON CONFLICT (owner_kind, owner_id, order_id) DO UPDATE
   SET state = EXCLUDED.state, volume_remain = EXCLUDED.volume_remain,
       issued_by = EXCLUDED.issued_by, updated_at = now()
 WHERE (t.state, t.volume_remain, t.issued_by)
    IS DISTINCT FROM (EXCLUDED.state, EXCLUDED.volume_remain, EXCLUDED.issued_by)
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

-- name: ListMarketOrdersByRegion :many
-- PHASE 15.1 — SRS §6.5 `GET /api/v1/markets/{region_id}/orders`.
--
-- SCOPE, stated precisely because Phase 15 got this wrong in the other
-- direction: this is NOT the complete public regional order book. HANGAR
-- syncs orders per tracked owner (`/characters/{id}/orders`,
-- `/corporations/{id}/orders`), so app.market_order contains exactly the
-- orders belonging to characters and corporations this installation
-- tracks. Region-scoping that set is a genuinely useful read — "what are
-- our people trading in Domain" — and app.market_order was built for it:
-- Phase 1b gave the table a `region_id` column AND a dedicated
-- `CREATE INDEX ON app.market_order (region_id)`, an index that is useless
-- to every owner-scoped query (they all lead with owner_kind, owner_id)
-- and only pays for itself here.
--
-- Phase 15 read "no complete public order book" as "no backing table" and
-- answered 501. The table and its index were there the whole time; what is
-- absent is a full-region sync, which nothing in the SRS asks for and
-- which would mean ingesting hundreds of thousands of rows per region
-- across ~100 regions.
SELECT * FROM app.market_order
 WHERE region_id = $1
   AND order_id > sqlc.arg(after_order_id)
 ORDER BY order_id
 LIMIT sqlc.arg(page_size);

-- name: ListMarketTypesByRegion :many
-- PHASE 15.1 — SRS §6.5 `GET /api/v1/markets/{region_id}/types`. Same
-- scope note as ListMarketOrdersByRegion: the distinct type_ids present in
-- the orders HANGAR has synced for this region.
SELECT DISTINCT type_id FROM app.market_order
 WHERE region_id = $1
 ORDER BY type_id;

-- name: ListMarketHistoryPairs :many
-- PHASE 20.5 (B30). The (region_id, type_id) pairs the market-history
-- fan-out walks.
--
-- GET /markets/{region_id}/history needs BOTH a region in the path and a
-- REQUIRED type_id in the query, and a subscription row carries one
-- entity_id and no second identifier — so, exactly like every other detail
-- route, the second identifier is enumerated here from rows an earlier sync
-- already landed. The source is app.market_order: the types this
-- installation's own tracked owners actually trade, not EVE's ~15,000
-- published types. An installation with no orders enumerates nothing and
-- makes no requests, which is the correct amount of work for a question
-- nobody has asked.
--
-- app.market_price is deliberately NOT the source even though the market
-- prices sync fills it with every published type: that would be one request
-- per (region, type) across every region, which is a rate-limit incident
-- rather than a feature.
SELECT DISTINCT region_id, type_id
  FROM app.market_order
 ORDER BY region_id, type_id
 LIMIT sqlc.arg(max_pairs);
