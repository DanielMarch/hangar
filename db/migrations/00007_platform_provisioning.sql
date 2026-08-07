-- Project HANGAR — Phase 1a: access provisioning.
-- 02_DATABASE_SCHEMA.md §4.4 (#33-#37).

-- +goose Up

-- #33
CREATE TABLE app.platform (
    platform_id uuid        PRIMARY KEY DEFAULT uuidv7(),
    kind        text        NOT NULL CHECK (kind IN ('discord', 'teamspeak', 'mumble')),  -- HANGAR's supported drivers
    name        text        NOT NULL,
    config      jsonb       NOT NULL DEFAULT '{}',  -- driver-specific connection config
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- #34 — remote group identity: Discord role id, TS3 server group, Mumble ACL group.
CREATE TABLE app.platform_group (
    group_id    uuid        PRIMARY KEY DEFAULT uuidv7(),
    platform_id uuid        NOT NULL REFERENCES app.platform(platform_id) ON DELETE CASCADE,
    remote_ref  text        NOT NULL,
    name        text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (platform_id, remote_ref)
);

-- #35 — seven sources + deny (§4.4).
CREATE TABLE app.entitlement_rule (
    rule_id     uuid        PRIMARY KEY DEFAULT uuidv7(),
    source_kind text        NOT NULL
                    CHECK (source_kind IN ('role', 'squad', 'corporation', 'alliance', 'permission', 'user', 'manual')),
    source_ref  text        NOT NULL,   -- id of the source_kind entity, opaque (shape varies by kind)
    group_id    uuid        NOT NULL REFERENCES app.platform_group(group_id) ON DELETE CASCADE,
    effect      text        NOT NULL DEFAULT 'grant' CHECK (effect IN ('grant', 'deny')),
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON app.entitlement_rule (group_id);
CREATE INDEX ON app.entitlement_rule (source_kind, source_ref);

-- #36 — linked remote identity, challenge token, desired vs actual groups.
-- The exposure board diffs desired_groups against actual_groups directly.
CREATE TABLE app.provisioning_state (
    platform_id         uuid   NOT NULL REFERENCES app.platform(platform_id) ON DELETE CASCADE,
    user_id             uuid   NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    remote_identity     text,           -- linked Discord user id / TS3 uid / Mumble cert hash
    challenge_token     text,
    desired_groups      text[] NOT NULL DEFAULT '{}',
    actual_groups       text[] NOT NULL DEFAULT '{}',
    linked_at           timestamptz,
    last_reconciled_at  timestamptz,
    PRIMARY KEY (platform_id, user_id)
);
CREATE INDEX ON app.provisioning_state (user_id);
CREATE INDEX ON app.provisioning_state (platform_id) WHERE desired_groups <> actual_groups;

-- #37 — Gate 2 evidence. Verbatim from 02_DATABASE_SCHEMA.md §4.4.
CREATE TABLE app.provisioning_audit (
    audit_id                   uuid        PRIMARY KEY DEFAULT uuidv7(),
    platform_id                uuid        NOT NULL REFERENCES app.platform(platform_id),
    user_id                    uuid        NOT NULL,
    action                     text        NOT NULL,   -- 'grant' | 'revoke' | 'link' | 'unlink'
    reason                     text        NOT NULL,   -- open vocabulary
    groups_added               text[]      NOT NULL DEFAULT '{}',
    groups_removed             text[]      NOT NULL DEFAULT '{}',
    -- Gate 2 measures p99 over (platform_call_completed_at - event_at).
    -- event_at is the ORIGINATING event, not job start: queue wait is the part
    -- that fails under load and must be inside the measurement.
    event_at                   timestamptz NOT NULL,
    platform_call_completed_at timestamptz,
    outcome                    text,
    error                      text
);
CREATE INDEX ON app.provisioning_audit (event_at DESC);
CREATE INDEX ON app.provisioning_audit (platform_call_completed_at)
   WHERE platform_call_completed_at IS NULL;      -- the exposure board

-- +goose Down
DROP TABLE app.provisioning_audit;
DROP TABLE app.provisioning_state;
DROP TABLE app.entitlement_rule;
DROP TABLE app.platform_group;
DROP TABLE app.platform;
