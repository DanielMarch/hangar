-- +goose Up
-- Phase 15.1 — defect closure for Phase 15's HTTP API layer.
--
-- Two columns, both closing a route Phase 15 had to answer 501 on because
-- the schema had no home for the value.

-- 1. app.corporation.member_limit
--
-- SRS §6.3 lists `GET /api/v1/corporations/{id}/members/limit`, and the
-- ingested ESI snapshot confirms the upstream route exists
-- (/corporations/{corporation_id}/members/limit, a bare integer response).
-- Phase 1a's app.corporation carried member_COUNT but never member_LIMIT,
-- so Phase 15 could not register the route at all and reported the gap.
-- They are genuinely different facts: member_count is how many pilots are
-- in the corporation now, member_limit is how many it is permitted to
-- hold (a function of its Corporation Management skills). The fuel-low
-- style "you are about to hit a ceiling" reading needs both.
ALTER TABLE app.corporation
    ADD COLUMN member_limit integer;

COMMENT ON COLUMN app.corporation.member_limit IS
    'Maximum members the corporation may hold (ESI /corporations/{id}/members/limit). NOT member_count, which is current occupancy. Phase 15.1.';

-- 2. app.platform.locked_down
--
-- SRS §6.8 lists `POST /api/v1/admin/platforms/{id}/lockdown`. Phase 15
-- answered 501 on the grounds that "internal/provisioning has no lockdown
-- primitive". app.platform.enabled is close but not the same thing:
-- `enabled` is the administrator's ordinary on/off switch for whether the
-- platform participates in provisioning at all, and flipping it to false
-- to handle an incident would be indistinguishable afterwards from an
-- installation that simply never configured that platform.
--
-- locked_down is the incident switch: it stops all outbound provisioning
-- for the platform the same way, but records WHY and WHEN, so the
-- exposure board and the audit trail can tell "deliberately frozen during
-- an incident" apart from "not in use".
ALTER TABLE app.platform
    ADD COLUMN locked_down    boolean     NOT NULL DEFAULT false,
    ADD COLUMN locked_down_at timestamptz,
    ADD COLUMN locked_down_by uuid        REFERENCES app.user(user_id),
    ADD COLUMN lockdown_reason text;

COMMENT ON COLUMN app.platform.locked_down IS
    'Incident freeze: suspends outbound provisioning without erasing the fact that the platform is configured. Distinct from `enabled`, the ordinary on/off switch. Phase 15.1.';

-- +goose Down
ALTER TABLE app.platform
    DROP COLUMN lockdown_reason,
    DROP COLUMN locked_down_by,
    DROP COLUMN locked_down_at,
    DROP COLUMN locked_down;

ALTER TABLE app.corporation
    DROP COLUMN member_limit;
