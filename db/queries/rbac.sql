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
-- Deny precedence: a deny anywhere wins regardless of allow count
-- (02_DATABASE_SCHEMA.md §4.2, Phase 10 truth table).
SELECT NOT bool_or(g.effect = 'deny') AND bool_or(g.effect = 'allow') AS permitted
  FROM app.user_role ur
  JOIN app.role_grant g USING (role_id)
 WHERE ur.user_id = $1 AND g.permission = $2;

-- name: RefreshEffectivePermission :exec
INSERT INTO app.effective_permission (user_id, permission, permitted)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, permission) DO UPDATE
   SET permitted = EXCLUDED.permitted, refreshed_at = now()
 WHERE app.effective_permission.permitted IS DISTINCT FROM EXCLUDED.permitted;

-- name: ListEffectivePermissions :many
SELECT permission FROM app.effective_permission WHERE user_id = $1 AND permitted ORDER BY permission;
