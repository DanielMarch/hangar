-- Project HANGAR — Phase 9 carry-over fix from Phase 8.
-- `issued_by` (the character who placed the order) is a REQUIRED field on
-- the live embedded spec (internal/esi/catalogue/embedded/openapi.snapshot.json)
-- for both /characters/{id}/orders and /corporations/{id}/orders (and
-- their /history counterparts), but app.market_order and
-- app.market_order_history had no column for it — Phase 8's MarketOrderDTO/
-- MarketOrderHistoryDTO parsed the field and then dropped it on the floor
-- (internal/sync/handlers/market.go's doc comments said so explicitly).
--
-- Nullable, same reasoning as every other nullable FK-shaped column in this
-- schema: a corporation order placed by a since-departed or since-unsynced
-- character is a legitimate case where the character row may not exist (or
-- may exist but the id is simply unknown to us yet). No FK is added for the
-- same reason app.contract.issuer_id etc. don't reference app.character —
-- ESI-sourced character ids arrive far ahead of any guarantee we've synced
-- that character ourselves.

-- +goose Up
ALTER TABLE app.market_order ADD COLUMN issued_by bigint;
ALTER TABLE app.market_order_history ADD COLUMN issued_by bigint;

-- +goose Down
ALTER TABLE app.market_order_history DROP COLUMN issued_by;
ALTER TABLE app.market_order DROP COLUMN issued_by;
