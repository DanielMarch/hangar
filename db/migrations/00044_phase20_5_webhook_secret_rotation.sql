-- Project HANGAR — Phase 20.5, defect B24: the §4.9 webhook configuration
-- surface, and what a secret rotation does to an event already in the outbox.
--
-- ── THE DEFECT ───────────────────────────────────────────────────────────
-- The outbound dispatcher has run since Phase 19 and internal/crypto's
-- SealWebhookSecret/NewWebhookSecret have existed since Phase 4, with no
-- production caller either. app.webhook_endpoint rows could be created only
-- by hand-written SQL against a table whose HMAC secret is envelope-encrypted
-- with AAD bound to the endpoint's own uuid — which is to say, not by any
-- operator. §4.9's subscriber side had no surface at all.
--
-- ── WHY A ROTATION NEEDS COLUMNS AND NOT JUST AN UPDATE ──────────────────
-- The requirement is that a secret is "rotatable without dropping in-flight
-- deliveries". A plain UPDATE cannot satisfy it, and the reason is a timing
-- fact about this pipeline rather than an implementation detail:
--
--   * a delivery is SIGNED AT SEND TIME, not at enqueue time
--     (events.Dispatcher.deliverOne opens the secret per attempt), and
--   * app.outbox_event rows can sit undispatched for a full pump interval,
--     and app.webhook_delivery rows retry for hours on the backoff schedule.
--
-- So an operator who rotates has already-queued events that WILL go out
-- under the new secret. Whichever secret the subscriber holds at that
-- instant, one side is wrong: rotate-then-tell and every queued delivery
-- fails signature verification until the subscriber is updated; tell-then-
-- rotate and the subscriber rejects everything sent in between.
--
-- ── THE DECISION: A ROTATION IS AN OVERLAP, NOT A SWAP ───────────────────
-- The new secret becomes the signing secret immediately. The PREVIOUS secret
-- stays valid for a grace window, during which every delivery carries BOTH
-- signatures in the one header — `t=<unix>,v1=<new>,v1=<previous>` — and a
-- receiver accepts if EITHER matches. The header format has allowed repeated
-- elements since Phase 19 (internal/events/sign.go's note on why `v1=` is
-- labelled at all); this phase makes Verify honour them.
--
-- CONSEQUENCE, STATED PLAINLY: an event already in the outbox when a
-- rotation happens is neither dropped nor re-signed nor delayed. It goes out
-- dual-signed and verifies against the old secret and the new one, so a
-- subscriber that has not yet updated keeps working and one that has updated
-- already works. Nothing is lost in either direction, which is what
-- "without dropping in-flight deliveries" has to mean.
--
-- The previous secret is cleared the moment it expires, so the overlap is
-- bounded rather than permanent — a second valid secret that never goes away
-- is a second secret to steal.

-- +goose Up

ALTER TABLE app.webhook_endpoint
    -- The superseded secret, sealed exactly as the live one is: same AAD
    -- (endpoint_id ‖ key_version ‖ 'webhook_secret'), same envelope shape.
    -- All four columns move together — NULL means "no rotation is in
    -- progress" and is the steady state.
    ADD COLUMN prev_hmac_key_version integer,
    ADD COLUMN prev_hmac_wrapped_dek bytea,
    ADD COLUMN prev_hmac_nonce       bytea,
    ADD COLUMN prev_hmac_ciphertext  bytea,
    ADD COLUMN prev_hmac_expires_at  timestamptz,
    -- Bookkeeping the owner sees on the read endpoint. The SECRET is never
    -- returned by any read; when it was last changed is not a secret and is
    -- the first thing anyone debugging a signature failure asks.
    ADD COLUMN rotated_at            timestamptz;

-- All-or-nothing: a half-written previous secret would be opened with a
-- nonce from one rotation and a ciphertext from another, and AEAD would
-- report that as an authentication failure — a confusing way to learn about
-- a partial UPDATE.
ALTER TABLE app.webhook_endpoint
    ADD CONSTRAINT webhook_endpoint_prev_secret_complete CHECK (
        (prev_hmac_key_version IS NULL AND prev_hmac_wrapped_dek IS NULL
         AND prev_hmac_nonce IS NULL AND prev_hmac_ciphertext IS NULL
         AND prev_hmac_expires_at IS NULL)
     OR (prev_hmac_key_version IS NOT NULL AND prev_hmac_wrapped_dek IS NOT NULL
         AND prev_hmac_nonce IS NOT NULL AND prev_hmac_ciphertext IS NOT NULL
         AND prev_hmac_expires_at IS NOT NULL)
    );

COMMENT ON COLUMN app.webhook_endpoint.prev_hmac_expires_at IS
    'Phase 20.5 (B24): the instant the superseded secret stops being accepted. '
    'Until then every delivery carries two v1= signatures and a receiver may match either, '
    'so a rotation drops nothing that is already in the outbox. Cleared, along with the '
    'other prev_* columns, on the first dispatch after it passes.';

COMMENT ON COLUMN app.webhook_endpoint.rotated_at IS
    'Phase 20.5 (B24): when the signing secret was last replaced. Returned by the read '
    'endpoint; the secret itself never is, and is shown exactly once, at the moment it is minted.';

-- +goose Down

ALTER TABLE app.webhook_endpoint
    DROP CONSTRAINT webhook_endpoint_prev_secret_complete;

ALTER TABLE app.webhook_endpoint
    DROP COLUMN prev_hmac_key_version,
    DROP COLUMN prev_hmac_wrapped_dek,
    DROP COLUMN prev_hmac_nonce,
    DROP COLUMN prev_hmac_ciphertext,
    DROP COLUMN prev_hmac_expires_at,
    DROP COLUMN rotated_at;
