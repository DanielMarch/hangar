-- Project HANGAR — Phase 8: acting-character election history.
-- 01_ARCHITECTURE.md §6.3 orders candidates by "fewest recent 403s", but
-- app.sync_subscription.consecutive_403 (Phase 1a) is a single counter on
-- the SUBSCRIPTION, not on each candidate CHARACTER — it resets to 0 on
-- ANY success, including a success by a newly-elected different character,
-- which erases the very history the ordering needs to distinguish among
-- untried or previously-failed candidates on a later re-election. This is
-- a genuine gap in the Phase 1a/6 schema the roadmap's Phase 8 prompt seed
-- doesn't call out explicitly but §6.3 requires: election is keyed
-- per (entity_kind, entity_id, route_id, character_id) — one row per
-- candidate the elector has ever scored for that subscription's
-- (entity, route) pair, not per subscription.
--
-- +goose Up

CREATE TABLE app.sync_acting_character_history (
    entity_kind     text        NOT NULL,
    entity_id       bigint      NOT NULL,
    route_id        uuid        NOT NULL REFERENCES app.esi_route(route_id) ON DELETE CASCADE,
    character_id    bigint      NOT NULL,
    consecutive_403 integer     NOT NULL DEFAULT 0,
    last_403_at     timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_kind, entity_id, route_id, character_id)
);
CREATE INDEX ON app.sync_acting_character_history (entity_kind, entity_id, route_id);

-- +goose Down
DROP TABLE app.sync_acting_character_history;
