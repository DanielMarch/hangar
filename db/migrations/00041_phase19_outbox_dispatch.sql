-- +goose Up
-- Phase 19 — the columns and indexes the events schema (Phase 1a, migration
-- 00009) needs before a dispatcher can actually run against it.
--
-- 02_DATABASE_SCHEMA.md §4.6 describes three tables and the ONE invariant
-- Phase 19 is graded on (the outbox insert shares the mutating
-- transaction). That invariant needed nothing new. Everything below exists
-- because the *dispatch* half — which §4.6 does not model — has failure
-- modes the Phase 1a shape cannot represent.

-- 1. app.webhook_delivery.failed_at — the dead-letter marker.
--
-- Phase 1a gave a delivery exactly two terminal-ish states: delivered_at
-- set, or next_retry_at set. There is no way to say "this one is finished
-- and it did NOT succeed". That is not a cosmetic gap:
-- ClaimPendingWebhookDeliveries selects
--   delivered_at IS NULL AND (next_retry_at IS NULL OR next_retry_at <= now())
-- so a delivery whose attempts are exhausted, written back with a NULL
-- next_retry_at, matches `next_retry_at IS NULL` and is claimed again
-- IMMEDIATELY — forever, as fast as the pump runs. The roadmap's "an
-- endpoint that is permanently down must not retain jobs forever" is
-- unimplementable without a third state, and the naive encoding is not
-- merely wrong but a hot loop.
ALTER TABLE app.webhook_delivery
    ADD COLUMN failed_at timestamptz;

COMMENT ON COLUMN app.webhook_delivery.failed_at IS
    'Dead-lettered: attempts exhausted, permanently not delivered. The third state — distinct from delivered_at (succeeded) and next_retry_at (still owed an attempt). Phase 19.';

-- The claim predicate must exclude dead-lettered rows, so the partial index
-- has to as well.
DROP INDEX IF EXISTS app.webhook_delivery_next_retry_at_idx;
CREATE INDEX webhook_delivery_claimable_idx
    ON app.webhook_delivery (next_retry_at)
    WHERE delivered_at IS NULL AND failed_at IS NULL;

-- 2. app.webhook_endpoint disable bookkeeping.
--
-- `enabled` alone cannot distinguish "the owner switched this off" from
-- "HANGAR switched this off because it has failed 100 times running" — the
-- same distinction migration 00040 drew between app.platform.enabled and
-- locked_down, and for the same reason: an administrator who cannot see WHY
-- an endpoint is off cannot decide whether re-enabling it is safe.
ALTER TABLE app.webhook_endpoint
    ADD COLUMN disabled_at        timestamptz,
    ADD COLUMN disabled_reason    text,
    ADD COLUMN consecutive_failures integer NOT NULL DEFAULT 0;

COMMENT ON COLUMN app.webhook_endpoint.disabled_at IS
    'When HANGAR itself disabled the endpoint (attempt cap reached). NULL when the owner disabled it, or when it is enabled. Phase 19.';
COMMENT ON COLUMN app.webhook_endpoint.consecutive_failures IS
    'Reset to 0 by any successful delivery. The endpoint-level circuit breaker: per-delivery attempt caps alone never disable a permanently-dead endpoint, they just dead-letter each job individually. Phase 19.';

-- 3. app.outbox_event dispatch ordering.
--
-- 00009 created `(event_id) WHERE dispatched_at IS NULL`, which is right for
-- the claim. The dispatcher also needs to answer "which endpoints care
-- about this event_type" cheaply; that is an endpoint-side lookup, and
-- event_filter is a text[] the fan-out scans with the array containment
-- operator.
CREATE INDEX webhook_endpoint_event_filter_idx
    ON app.webhook_endpoint USING gin (event_filter)
    WHERE enabled;

-- 4. WHERE THE ADMIN NOTIFICATION GOES — and why not the alert catalogue.
--
-- The obvious home for "HANGAR disabled your webhook endpoint" is §4.4's
-- alert pipeline, and it is the wrong one. app.alert_type is a MEASURED
-- parity artefact: BASELINE §4/§4a pins it at exactly 54 concrete types
-- across 8 domains, catalogue.DocumentedTotal encodes that, and
-- TestAlertCatalogueSeeds54AcrossEightDomains and Gate 4 both check it.
-- A 55th row would either break that count or sit in the table with no
-- catalogue entry backing it. Worse, the only category that fits an
-- operational threshold is 'threshold', and the CHECK constraint
-- threshold_declares_source requires a source_route_id — an ESI route.
-- A dead webhook endpoint has no ESI route; it is not an ESI fact at all.
--
-- So the notification is written to app.security_log (#9, "every admin
-- action"), which Phase 18 already surfaces as capability 51's security and
-- audit log. That needs no schema change — it is recorded here so the next
-- reader does not "fix" the omission by adding the alert type.

-- +goose Down
DROP INDEX IF EXISTS app.webhook_endpoint_event_filter_idx;

ALTER TABLE app.webhook_endpoint
    DROP COLUMN consecutive_failures,
    DROP COLUMN disabled_reason,
    DROP COLUMN disabled_at;

DROP INDEX IF EXISTS app.webhook_delivery_claimable_idx;
CREATE INDEX webhook_delivery_next_retry_at_idx
    ON app.webhook_delivery (next_retry_at)
    WHERE delivered_at IS NULL;

ALTER TABLE app.webhook_delivery
    DROP COLUMN failed_at;
