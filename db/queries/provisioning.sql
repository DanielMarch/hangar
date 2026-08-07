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

-- name: SetEntitlementRuleEnabled :exec
UPDATE app.entitlement_rule SET enabled = $2 WHERE rule_id = $1;

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

-- name: ListExposedProvisioningStates :many
-- The exposure board: desired and actual groups disagree.
SELECT * FROM app.provisioning_state
 WHERE platform_id = $1 AND desired_groups <> actual_groups;

-- name: RecordProvisioningAudit :one
INSERT INTO app.provisioning_audit (platform_id, user_id, action, reason, groups_added, groups_removed, event_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

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

-- name: ListRecentProvisioningAudit :many
SELECT * FROM app.provisioning_audit ORDER BY event_at DESC LIMIT sqlc.arg(page_size);
