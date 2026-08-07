-- Project HANGAR — Phase 1a: alerting.
-- 02_DATABASE_SCHEMA.md §4.5 (#38-#43).

-- +goose Up

-- #38 — 54 seeded rows across 8 domains (db/seed/alert_types.sql).
CREATE TABLE app.alert_type (
    alert_type      text    PRIMARY KEY,        -- CCP notification type or hangar.* event
    domain          text    NOT NULL,           -- structures|characters|platform|wars|
                                                -- corporations|sovereignty|contracts|alliances
    category        text    NOT NULL,           -- 'esi_notification'|'domain_event'|'threshold'
    -- BUILD-TIME RULE: category='threshold' ⇒ source_route_id NOT NULL and that route
    -- must be present in the sync set. Enforced by TestThresholdAlertSourceRoutesScheduled.
    source_route_id uuid    REFERENCES app.esi_route(route_id),
    default_enabled boolean NOT NULL DEFAULT true,
    CONSTRAINT threshold_declares_source
        CHECK (category <> 'threshold' OR source_route_id IS NOT NULL)
);

-- #39 — SMTP / Slack webhook / Discord webhook config.
CREATE TABLE app.alert_channel (
    channel_id uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind       text        NOT NULL CHECK (kind IN ('smtp', 'slack_webhook', 'discord_webhook')),
    name       text        NOT NULL,
    config     jsonb       NOT NULL DEFAULT '{}',
    enabled    boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- #40
CREATE TABLE app.alert_routing_rule (
    rule_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    alert_type  text        NOT NULL REFERENCES app.alert_type(alert_type),
    target_kind text        NOT NULL CHECK (target_kind IN ('user', 'squad', 'corporation', 'alliance', 'installation')),
    target_ref  text,                -- id of target_kind entity; NULL for 'installation'
    channel_id  uuid        NOT NULL REFERENCES app.alert_channel(channel_id) ON DELETE CASCADE,
    mention     text,                -- open vocabulary: platform-specific mention string
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.alert_routing_rule (alert_type);
CREATE INDEX ON app.alert_routing_rule (channel_id);

-- #41 — payload is where an unparseable CCP notification YAML lands verbatim.
CREATE TABLE app.alert_event (
    event_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    alert_type   text        NOT NULL REFERENCES app.alert_type(alert_type),
    dedupe_hash  text        NOT NULL,
    coalesce_key text,
    payload      jsonb       NOT NULL,
    occurred_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (dedupe_hash)
);
CREATE INDEX ON app.alert_event (alert_type, occurred_at DESC);
CREATE INDEX ON app.alert_event (coalesce_key) WHERE coalesce_key IS NOT NULL;

-- #42 — outbox + attempts + dead-letter.
CREATE TABLE app.alert_delivery (
    delivery_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    event_id        uuid        NOT NULL REFERENCES app.alert_event(event_id) ON DELETE CASCADE,
    channel_id      uuid        NOT NULL REFERENCES app.alert_channel(channel_id) ON DELETE CASCADE,
    state           text        NOT NULL DEFAULT 'pending'
                        CHECK (state IN ('pending', 'sent', 'failed', 'dead_letter')),
    attempts        integer     NOT NULL DEFAULT 0,
    last_attempt_at timestamptz,
    next_attempt_at timestamptz,
    error           text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.alert_delivery (event_id);
CREATE INDEX ON app.alert_delivery (state, next_attempt_at) WHERE state = 'pending';

-- #43 — the unknown-types board.
CREATE TABLE app.notification_unknown_type (
    type            text        PRIMARY KEY,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    occurrences     bigint      NOT NULL DEFAULT 1,
    sample_payload  jsonb,
    acknowledged_at timestamptz
);
CREATE INDEX ON app.notification_unknown_type (acknowledged_at) WHERE acknowledged_at IS NULL;

-- +goose Down
DROP TABLE app.notification_unknown_type;
DROP TABLE app.alert_delivery;
DROP TABLE app.alert_event;
DROP TABLE app.alert_routing_rule;
DROP TABLE app.alert_channel;
DROP TABLE app.alert_type;
