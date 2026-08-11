-- app.role, app.permission, app.role_grant, app.user_role,
-- app.effective_permission (02_DATABASE_SCHEMA.md §4.2 #11-#15).

-- name: ListPermissions :many
SELECT * FROM app.permission ORDER BY category, permission;

-- name: CreateRole :one
INSERT INTO app.role (name, description, is_system)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetRole :one
SELECT * FROM app.role WHERE role_id = $1;

-- name: ListRoles :many
SELECT * FROM app.role ORDER BY name;

-- name: AddRoleGrant :one
INSERT INTO app.role_grant (role_id, permission, effect)
VALUES ($1, $2, $3)
ON CONFLICT (role_id, permission, effect) DO NOTHING
RETURNING *;

-- name: GetRoleGrant :one
-- Needed by internal/rbac/grant.go's RemoveRoleGrant wrapper: the
-- affected-user-set computation (UsersAffectedByRole) needs role_id,
-- which the grant_id-only DELETE below doesn't carry back to the caller.
SELECT * FROM app.role_grant WHERE grant_id = $1;

-- name: RemoveRoleGrant :exec
DELETE FROM app.role_grant WHERE grant_id = $1;

-- name: ListRoleGrants :many
SELECT * FROM app.role_grant WHERE role_id = $1 ORDER BY permission;

-- name: AssignUserRole :exec
INSERT INTO app.user_role (user_id, role_id, granted_by)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, role_id) DO NOTHING;

-- name: RevokeUserRole :exec
DELETE FROM app.user_role WHERE user_id = $1 AND role_id = $2;

-- name: ListUserRoles :many
SELECT r.* FROM app.role r
  JOIN app.user_role ur ON ur.role_id = r.role_id
 WHERE ur.user_id = $1
 ORDER BY r.name;

-- name: CheckPermission :one
-- PHASE 10: extended from the Phase 1a stub, which only ever looked at
-- app.user_role — a role granted via squad membership contributed
-- nothing to a permission check. `role_ids` is every role this user
-- holds via EITHER path: directly (app.user_role) or through a squad
-- they belong to (app.squad_member -> app.character.user_id ->
-- app.squad_role) — 02_DATABASE_SCHEMA.md §4.2 has exactly these two
-- paths, not the seven-source split that belongs to Phase 11's
-- entitlement engine (01_ARCHITECTURE.md §9.1 is a different model over
-- app.entitlement_rule; do not conflate the two).
--
-- Deny precedence: a deny anywhere wins regardless of allow count
-- (02_DATABASE_SCHEMA.md §4.2, Phase 10 truth table), computed with
-- FILTER rather than a second query so this stays one round trip.
-- `sqlc.arg(superuser_permission)` is passed in by the caller
-- (domain.SuperuserPermission) rather than hardcoded here, so the SQL
-- and the Go closed set can never drift on the magic string. Superuser
-- is resolved through this SAME grant path — never a code bypass — and
-- is itself deniable: a deny on the checked permission always wins even
-- when the user also holds an allowed, non-denied superuser grant; a
-- deny on `superuser` itself simply removes it as a fallback.
-- COALESCE(..., true/false) makes an empty grant set (a user with zero
-- roles via either path) resolve to NOT permitted, never NULL
-- ("A user with zero roles gets zero permissions — not default allow").
WITH role_ids AS (
    SELECT role_id FROM app.user_role WHERE user_id = sqlc.arg(user_id)::uuid
    UNION
    SELECT sr.role_id
      FROM app.squad_role sr
      JOIN app.squad_member sm ON sm.squad_id = sr.squad_id
      JOIN app.character c ON c.character_id = sm.character_id
     WHERE c.user_id = sqlc.arg(user_id)::uuid
), grants AS (
    SELECT g.permission, g.effect FROM app.role_grant g WHERE g.role_id IN (SELECT role_id FROM role_ids)
)
SELECT
    COALESCE(NOT bool_or(effect = 'deny') FILTER (WHERE permission = sqlc.arg(permission)::text), true)
    AND (
        COALESCE(bool_or(effect = 'allow') FILTER (WHERE permission = sqlc.arg(permission)::text), false)
        OR (
            COALESCE(bool_or(effect = 'allow') FILTER (WHERE permission = sqlc.arg(superuser_permission)::text), false)
            AND COALESCE(NOT bool_or(effect = 'deny') FILTER (WHERE permission = sqlc.arg(superuser_permission)::text), true)
        )
    ) AS permitted
FROM grants;

-- name: ListUserGrants :many
-- The raw (permission, effect) tuples reachable by a user via either
-- grant path (same role_ids union as CheckPermission above). This is
-- what internal/rbac.Resolve/ResolveAll fetch ONCE per user and then
-- resolve entirely in Go — the "no I/O beyond the query it's built on"
-- contract — rather than issuing one CheckPermission round trip per
-- permission when materializing the whole closed set for one user.
SELECT DISTINCT g.permission, g.effect
  FROM app.role_grant g
 WHERE g.role_id IN (
    SELECT role_id FROM app.user_role WHERE user_id = sqlc.arg(user_id)::uuid
    UNION
    SELECT sr.role_id
      FROM app.squad_role sr
      JOIN app.squad_member sm ON sm.squad_id = sr.squad_id
      JOIN app.character c ON c.character_id = sm.character_id
     WHERE c.user_id = sqlc.arg(user_id)::uuid
 );

-- name: RefreshEffectivePermission :exec
INSERT INTO app.effective_permission (user_id, permission, permitted)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, permission) DO UPDATE
   SET permitted = EXCLUDED.permitted, refreshed_at = now()
 WHERE app.effective_permission.permitted IS DISTINCT FROM EXCLUDED.permitted;

-- name: GetEffectivePermission :one
-- The middleware's hot path (02_DATABASE_SCHEMA.md §4.2: "the hot path
-- is a single indexed lookup"): one row from the materialised table,
-- never a live role_grant join. A missing row (this user was never
-- materialized) is the caller's job to treat as not-permitted — see
-- internal/api/middleware/authorize.go.
SELECT * FROM app.effective_permission WHERE user_id = $1 AND permission = $2;

-- name: ListEffectivePermissions :many
SELECT permission FROM app.effective_permission WHERE user_id = $1 AND permitted ORDER BY permission;

-- name: DeleteRole :exec
-- Guards system roles (`admin`, `member`, db/seed/roles.sql) at the SQL
-- level, not just by Go-side convention — NOT is_system in the WHERE
-- clause means a DELETE against a system role's role_id affects zero
-- rows rather than erroring, so the caller must check rows-affected (or
-- re-GetRole) to detect a no-op deletion attempt.
DELETE FROM app.role WHERE role_id = $1 AND NOT is_system;

-- Phase 10 materialize.go affected-user-set queries. Each mutation kind
-- that can change a user's effective permissions needs its own
-- "who is affected" query — recomputing effective_permission for every
-- user on every grant change would blow the 5000-user < 2ms benchmark.

-- name: ListUsersWithRoleDirect :many
-- Affected set for a role_grant change (permission/effect added or
-- removed on a role) or a role deletion: every user holding that role
-- directly.
SELECT user_id FROM app.user_role WHERE role_id = $1;

-- name: ListUsersWithRoleViaSquad :many
-- The other half of a role_grant change's / role deletion's affected
-- set: every user reachable through a squad that grants this role.
-- DISTINCT + user_id IS NOT NULL because a squad can have many
-- characters belonging to the same user, and a character need not be
-- linked to a user account at all.
SELECT DISTINCT c.user_id AS user_id
  FROM app.squad_role sr
  JOIN app.squad_member sm ON sm.squad_id = sr.squad_id
  JOIN app.character c ON c.character_id = sm.character_id
 WHERE sr.role_id = $1 AND c.user_id IS NOT NULL;

-- name: ListUsersInSquad :many
-- Affected set for a squad_role change (a role added/removed from a
-- squad): every user with a character in that squad.
SELECT DISTINCT c.user_id AS user_id
  FROM app.squad_member sm
  JOIN app.character c ON c.character_id = sm.character_id
 WHERE sm.squad_id = $1 AND c.user_id IS NOT NULL;

-- name: GetCharacterUserID :one
-- Affected set for a squad_member change (one character joining or
-- leaving a squad): just that character's own user, if linked.
SELECT user_id FROM app.character WHERE character_id = $1;

-- name: DeleteRoleGrants :exec
-- PHASE 15.1 — the delete half of `PUT /api/v1/admin/scopes` (SRS §6.8's
-- bulk grant replace). Phase 15 answered 501 because only per-grant
-- AddRoleGrant/RemoveRoleGrant existed and a read-modify-write loop over
-- them is not a replace: a caller and a concurrent editor could interleave
-- into a grant set neither of them asked for. Paired with AddRoleGrant
-- inside one transaction (see internal/api/v1/admin.go), this makes the
-- replace atomic.
--
-- Flagged by sqlc's flag-delete rule for review, like every other DELETE
-- in this file: app.role_grant is a pure join of (role, permission,
-- effect) with no history worth soft-deleting — internal/rbac's
-- materialisation is what carries the audit consequence, and it is
-- recomputed from the surviving rows.
DELETE FROM app.role_grant WHERE role_id = $1;
