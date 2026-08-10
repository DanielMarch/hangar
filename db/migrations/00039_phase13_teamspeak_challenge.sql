-- Project HANGAR — Phase 13: TeamSpeak single-use challenge tokens.
--
-- 01_ARCHITECTURE.md §9.4: "Identity mapping is by client_unique_identifier
-- (the base64 client UID), established through a single-use challenge
-- token: HANGAR issues a token, the user presents it in-client, HANGAR
-- observes the redemption and binds the UID. The token row records
-- consumed_at and a second redemption must fail."
--
-- app.provisioning_state already has a generic `challenge_token text`
-- column (00007_platform_provisioning.sql, Phase 1a) intended for any
-- platform's link flow, but it carries no expiry or consumed_at — one
-- column, overwritten wholesale by UpsertProvisioningState on every call,
-- with no way to express "already consumed" or "expired" short of
-- clobbering it back to NULL, which loses the audit trail of what was
-- issued and when. A dedicated table (Discord's invalid-budget precedent,
-- 00038: a real Postgres row survives a restart and stays correct across
-- replicas) is Phase 13's own concern, not a repurposing of Phase 1a's
-- column — a future Discord/other-platform link flow can still use
-- provisioning_state.challenge_token for its own, simpler needs without
-- colliding with this table.
--
-- +goose Up

CREATE TABLE app.teamspeak_challenge (
    token                    text        PRIMARY KEY,
    user_id                  uuid        NOT NULL REFERENCES app.user(user_id) ON DELETE CASCADE,
    issued_at                timestamptz NOT NULL DEFAULT now(),
    expires_at               timestamptz NOT NULL,
    consumed_at              timestamptz,
    client_unique_identifier text        -- set at redemption time
);
CREATE INDEX ON app.teamspeak_challenge (user_id);
-- The claim-loop-style partial index (internal/sync/planner's precedent):
-- only unconsumed, not-yet-expired tokens are ever looked up by a
-- redemption attempt.
CREATE INDEX ON app.teamspeak_challenge (expires_at) WHERE consumed_at IS NULL;

-- +goose Down

DROP TABLE app.teamspeak_challenge;
