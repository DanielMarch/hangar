-- app.squad, app.squad_member, app.squad_moderator, app.squad_role,
-- app.squad_application (02_DATABASE_SCHEMA.md §4.2 #16-#20).

-- name: CreateSquad :one
INSERT INTO app.squad (name, type, owner_user_id, description)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSquad :one
SELECT * FROM app.squad WHERE squad_id = $1;

-- name: ListSquads :many
SELECT * FROM app.squad ORDER BY name;

-- name: UpdateSquad :one
-- Phase 15 addition (internal/api/v1): PATCH /api/v1/squads/{id} — no
-- mutation beyond Create existed for squad's own row before this phase.
UPDATE app.squad AS t
   SET name = $2, description = $3, updated_at = now()
 WHERE t.squad_id = $1
RETURNING *;

-- name: DeleteSquad :exec
-- Phase 15 addition: DELETE /api/v1/squads/{id}. Child rows
-- (squad_member/squad_moderator/squad_role/squad_application) are removed
-- by their own FK ON DELETE CASCADE (02_DATABASE_SCHEMA.md §4.2's squad
-- family) — this is a hard delete of the squad itself, not a soft one,
-- matching how a squad (a HANGAR-native grouping, not an ESI-synced
-- projection) has no "last known state" worth retaining once its owner
-- deletes it.
DELETE FROM app.squad WHERE squad_id = $1;

-- name: AddSquadMember :exec
INSERT INTO app.squad_member (squad_id, character_id)
VALUES ($1, $2)
ON CONFLICT (squad_id, character_id) DO NOTHING;

-- name: RemoveSquadMember :exec
DELETE FROM app.squad_member WHERE squad_id = $1 AND character_id = $2;

-- name: ListSquadMembers :many
SELECT * FROM app.squad_member WHERE squad_id = $1 ORDER BY joined_at;

-- name: ListSquadsForUser :many
-- Direct squad membership for a user, via any of their characters
-- (app.squad_member is keyed by character_id, not user_id). Phase 11's
-- entitlement engine reads this for the `squad` source_kind — a wholly
-- separate provisioning mechanism from a squad's own app.squad_role RBAC
-- grant (internal/rbac's squad-derived role path); this query answers
-- "which squads is this user directly IN", never "which roles does squad
-- membership grant".
SELECT DISTINCT sm.squad_id
  FROM app.squad_member sm
  JOIN app.character c ON c.character_id = sm.character_id
 WHERE c.user_id = $1;

-- name: AddSquadModerator :exec
INSERT INTO app.squad_moderator (squad_id, user_id)
VALUES ($1, $2)
ON CONFLICT (squad_id, user_id) DO NOTHING;

-- name: RemoveSquadModerator :exec
DELETE FROM app.squad_moderator WHERE squad_id = $1 AND user_id = $2;

-- name: IsSquadModerator :one
SELECT EXISTS (
    SELECT 1 FROM app.squad_moderator WHERE squad_id = $1 AND user_id = $2
) AS is_moderator;

-- name: AddSquadRole :exec
INSERT INTO app.squad_role (squad_id, role_id)
VALUES ($1, $2)
ON CONFLICT (squad_id, role_id) DO NOTHING;

-- name: RemoveSquadRole :exec
DELETE FROM app.squad_role WHERE squad_id = $1 AND role_id = $2;

-- name: ListSquadRoles :many
SELECT role_id FROM app.squad_role WHERE squad_id = $1;

-- name: CreateSquadApplication :one
INSERT INTO app.squad_application (squad_id, character_id, message)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListPendingSquadApplications :many
SELECT * FROM app.squad_application
 WHERE squad_id = $1 AND status = 'pending'
 ORDER BY created_at;

-- name: ResolveSquadApplication :exec
UPDATE app.squad_application
   SET status = $2, resolved_by = $3, resolved_at = now()
 WHERE application_id = $1;
