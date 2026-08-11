-- app.platform, app.platform_group, app.entitlement_rule,
-- app.provisioning_state, app.provisioning_audit (02_DATABASE_SCHEMA.md §4.4
-- #33-#37).

-- name: CreatePlatform :one
INSERT INTO app.platform (kind, name, config)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListPlatforms :many
SELECT * FROM app.platform WHERE enabled ORDER BY name;

-- name: GetPlatform :one
SELECT * FROM app.platform WHERE platform_id = $1;

-- name: CreatePlatformGroup :one
INSERT INTO app.platform_group (platform_id, remote_ref, name)
VALUES ($1, $2, $3)
ON CONFLICT (platform_id, remote_ref) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: ListPlatformGroups :many
SELECT * FROM app.platform_group WHERE platform_id = $1 ORDER BY name;

-- name: CreateEntitlementRule :one
INSERT INTO app.entitlement_rule (source_kind, source_ref, group_id, effect)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListEntitlementRulesForGroup :many
SELECT * FROM app.entitlement_rule WHERE group_id = $1 AND enabled ORDER BY created_at;

-- name: ListEntitlementRulesForSource :many
SELECT * FROM app.entitlement_rule
 WHERE source_kind = $1 AND source_ref = $2 AND enabled
 ORDER BY created_at;

-- name: ListEntitlementRulesForPlatform :many
-- Every enabled rule that targets one of platform_id's groups — what
-- evaluate.go is run against for a single platform's reconcile/preview/
-- urgent-revocation recompute (Phase 11).
SELECT er.*
  FROM app.entitlement_rule er
  JOIN app.platform_group pg ON pg.group_id = er.group_id
 WHERE pg.platform_id = $1 AND er.enabled
 ORDER BY er.created_at;

-- name: SetEntitlementRuleEnabled :exec
UPDATE app.entitlement_rule SET enabled = $2 WHERE rule_id = $1;

-- name: DeleteEntitlementRule :one
-- Returns the deleted row (specifically group_id) so the caller can
-- resolve the affected platform and drive the bulk urgent-revocation edge
-- case (roadmap: "deleting a rule reduces entitlements for everyone it
-- matched — that is a bulk urgent revocation").
DELETE FROM app.entitlement_rule WHERE rule_id = $1 RETURNING *;

-- name: GetPlatformGroup :one
SELECT * FROM app.platform_group WHERE group_id = $1;

-- name: UpsertProvisioningState :exec
INSERT INTO app.provisioning_state (platform_id, user_id, remote_identity, challenge_token, desired_groups, actual_groups, linked_at, last_reconciled_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (platform_id, user_id) DO UPDATE
   SET remote_identity    = EXCLUDED.remote_identity,
       challenge_token     = EXCLUDED.challenge_token,
       desired_groups      = EXCLUDED.desired_groups,
       actual_groups       = EXCLUDED.actual_groups,
       linked_at           = coalesce(app.provisioning_state.linked_at, EXCLUDED.linked_at),
       last_reconciled_at  = EXCLUDED.last_reconciled_at;

-- name: GetProvisioningState :one
SELECT * FROM app.provisioning_state WHERE platform_id = $1 AND user_id = $2;

-- name: GetProvisioningStateByRemoteIdentity :one
-- The reverse lookup a platform-side identity assertion needs — Phase
-- 13's Mumble external-authenticator path resolves a connecting client's
-- certificate hash back to the HANGAR user it's linked to (remote_identity
-- is unique per platform by construction: UpsertProvisioningState is
-- keyed one row per (platform_id, user_id), and a real link flow never
-- binds the same remote identity to two different users on one platform).
SELECT * FROM app.provisioning_state WHERE platform_id = $1 AND remote_identity = $2;

-- name: ListExposedProvisioningStates :many
-- The exposure board: desired and actual groups disagree.
SELECT * FROM app.provisioning_state
 WHERE platform_id = $1 AND desired_groups <> actual_groups;

-- name: ListProvisioningStatesForUser :many
-- Every platform this user is linked to — urgent.go's recompute scope for
-- one user (Phase 11): only linked platforms are ever revoked from, never
-- a platform the user has no provisioning_state row on. Backed by the
-- index on app.provisioning_state(user_id).
SELECT * FROM app.provisioning_state WHERE user_id = $1;

-- name: ListAllProvisioningStatesForPlatform :many
-- Every user linked to platform_id, matched or not — reconcile.go's bulk
-- scope (Phase 11), as distinct from ListExposedProvisioningStates which
-- only returns rows already known to disagree.
SELECT * FROM app.provisioning_state WHERE platform_id = $1;

-- name: UpdateProvisioningStateGroups :exec
-- Updates only desired/actual groups and the reconciliation timestamp —
-- deliberately narrower than UpsertProvisioningState, which would clobber
-- remote_identity/challenge_token/linked_at with NULLs on every
-- entitlement recompute. Affects zero rows if the user was never linked
-- (no INSERT branch) — reconcile.go/urgent.go only ever call this against
-- a provisioning_state row obtained from one of the List* queries above.
UPDATE app.provisioning_state
   SET desired_groups     = $3,
       actual_groups      = $4,
       last_reconciled_at = $5
 WHERE platform_id = $1 AND user_id = $2;

-- name: RecordProvisioningAudit :one
INSERT INTO app.provisioning_audit (platform_id, user_id, action, reason, groups_added, groups_removed, event_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetProvisioningAudit :one
-- Read back by the provision-urgent/provision-bulk worker: the audit row
-- itself already carries groups_added/groups_removed, so the worker needs
-- nothing else from the triggering transaction to drive the platform call.
SELECT * FROM app.provisioning_audit WHERE audit_id = $1;

-- name: CompleteProvisioningAudit :exec
UPDATE app.provisioning_audit
   SET platform_call_completed_at = now(), outcome = $2, error = $3
 WHERE audit_id = $1;

-- name: ListPendingProvisioningAudit :many
-- The exposure board's audit-side view: event_at set, platform call never
-- completed. Gate 2 measures p99 over (platform_call_completed_at - event_at).
SELECT * FROM app.provisioning_audit
 WHERE platform_call_completed_at IS NULL
 ORDER BY event_at;

-- name: ListPendingProvisioningAuditForPlatform :many
-- PHASE 18. The per-platform exposure board's audit side.
-- GetExposureBoard is scoped to one platform, but the query above is not,
-- so the board for platform A was listing platform B's pending
-- revocations alongside its own. Added as a scoped variant rather than by
-- adding a predicate to ListPendingProvisioningAudit, which Gate 2's
-- installation-wide latency measurement uses unscoped and correctly so.
--
-- `age` is deliberately NOT computed here: the exposure board's exit
-- criterion is that the age comes from event_at, and event_at is on the
-- row. Computing an age server-side would freeze it at response time,
-- which is the same "measured from the wrong instant" mistake the
-- criterion exists to rule out.
SELECT * FROM app.provisioning_audit
 WHERE platform_call_completed_at IS NULL
   AND platform_id = $1
 ORDER BY event_at;

-- name: ListRecentProvisioningAudit :many
SELECT * FROM app.provisioning_audit ORDER BY event_at DESC LIMIT sqlc.arg(page_size);

-- name: SetPlatformLockdown :one
-- PHASE 15.1 — SRS §6.8 `POST /api/v1/admin/platforms/{id}/lockdown`.
-- Deliberately distinct from `enabled` (see 00040's column comment):
-- `enabled` is "is this platform in use at all", lockdown is "freeze
-- outbound provisioning right now, and record who froze it and why".
UPDATE app.platform
   SET locked_down     = $2,
       locked_down_at  = CASE WHEN $2 THEN now() ELSE NULL END,
       locked_down_by  = CASE WHEN $2 THEN sqlc.narg(actor)::uuid ELSE NULL END,
       lockdown_reason = CASE WHEN $2 THEN sqlc.narg(reason)::text ELSE NULL END,
       updated_at      = now()
 WHERE platform_id = $1
RETURNING *;
