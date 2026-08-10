// grant.go wraps every mutation that can change a user's effective
// permissions: role_grant add/remove, user_role assign/revoke,
// squad_role add/remove, squad_member add/remove, and role deletion.
// Every wrapper runs the mutation and its materialize.go refresh in the
// SAME transaction via store.WithTx (02_DATABASE_SCHEMA.md §4.2 /
// roadmap edge case: "Materialisation must be transactionally consistent
// with the grant change that triggered it").
package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// AddRoleGrant adds one (role, permission, effect) grant and refreshes
// every user that role reaches (directly or via a squad).
func AddRoleGrant(ctx context.Context, pool store.Pool, roleID uuid.UUID, permission string, effect Effect) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if _, err := s.AddRoleGrant(ctx, roleID, permission, string(effect)); err != nil {
			return fmt.Errorf("rbac: adding grant %s=%s to role %s: %w", permission, effect, roleID, err)
		}
		affected, err := UsersAffectedByRole(ctx, s, roleID)
		if err != nil {
			return err
		}
		return RefreshUsers(ctx, s, affected)
	})
}

// RemoveRoleGrant removes one grant by id and refreshes every user its
// role reached — the role_id is looked up first (GetRoleGrant) since the
// DELETE itself only takes grant_id and the affected set needs role_id.
func RemoveRoleGrant(ctx context.Context, pool store.Pool, grantID uuid.UUID) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		grant, err := s.GetRoleGrant(ctx, grantID)
		if err != nil {
			return fmt.Errorf("rbac: looking up grant %s before removal: %w", grantID, err)
		}
		if err := s.RemoveRoleGrant(ctx, grantID); err != nil {
			return fmt.Errorf("rbac: removing grant %s: %w", grantID, err)
		}
		affected, err := UsersAffectedByRole(ctx, s, grant.RoleID)
		if err != nil {
			return err
		}
		return RefreshUsers(ctx, s, affected)
	})
}

// AssignUserRole grants a role directly to a user and refreshes that one
// user (a direct user_role change affects nobody else's effective
// permissions).
func AssignUserRole(ctx context.Context, pool store.Pool, userID, roleID uuid.UUID, grantedBy uuid.NullUUID) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if err := s.AssignUserRole(ctx, userID, roleID, grantedBy); err != nil {
			return fmt.Errorf("rbac: assigning role %s to user %s: %w", roleID, userID, err)
		}
		return RefreshUser(ctx, s, userID)
	})
}

// RevokeUserRole is AssignUserRole's inverse.
func RevokeUserRole(ctx context.Context, pool store.Pool, userID, roleID uuid.UUID) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if err := s.RevokeUserRole(ctx, userID, roleID); err != nil {
			return fmt.Errorf("rbac: revoking role %s from user %s: %w", roleID, userID, err)
		}
		return RefreshUser(ctx, s, userID)
	})
}

// AddSquadRole grants a role to every member of a squad and refreshes
// every user with a character in that squad.
func AddSquadRole(ctx context.Context, pool store.Pool, squadID, roleID uuid.UUID) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if err := s.AddSquadRole(ctx, squadID, roleID); err != nil {
			return fmt.Errorf("rbac: adding role %s to squad %s: %w", roleID, squadID, err)
		}
		affected, err := UsersAffectedBySquadRole(ctx, s, squadID)
		if err != nil {
			return err
		}
		return RefreshUsers(ctx, s, affected)
	})
}

// RemoveSquadRole is AddSquadRole's inverse. squadID is enough on its own
// to compute the affected set — a squad_role removal potentially affects
// every current member of the squad regardless of which role was removed,
// since RefreshUser recomputes the user's WHOLE permission set anyway.
func RemoveSquadRole(ctx context.Context, pool store.Pool, squadID, roleID uuid.UUID) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if err := s.RemoveSquadRole(ctx, squadID, roleID); err != nil {
			return fmt.Errorf("rbac: removing role %s from squad %s: %w", roleID, squadID, err)
		}
		affected, err := UsersAffectedBySquadRole(ctx, s, squadID)
		if err != nil {
			return err
		}
		return RefreshUsers(ctx, s, affected)
	})
}

// AddSquadMember adds one character to a squad and refreshes that
// character's linked user, if any (a character with no user_id affects
// nobody's effective_permission — see UserAffectedBySquadMember).
func AddSquadMember(ctx context.Context, pool store.Pool, squadID uuid.UUID, characterID int64) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if err := s.AddSquadMember(ctx, squadID, characterID); err != nil {
			return fmt.Errorf("rbac: adding character %d to squad %s: %w", characterID, squadID, err)
		}
		userID, ok, err := UserAffectedBySquadMember(ctx, s, characterID)
		if err != nil || !ok {
			return err
		}
		return RefreshUser(ctx, s, userID)
	})
}

// RemoveSquadMember is AddSquadMember's inverse — looked up BEFORE the
// delete, since app.squad_member -> app.character is still joinable at
// that point (character rows are never removed by this operation).
func RemoveSquadMember(ctx context.Context, pool store.Pool, squadID uuid.UUID, characterID int64) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		userID, ok, err := UserAffectedBySquadMember(ctx, s, characterID)
		if err != nil {
			return err
		}
		if err := s.RemoveSquadMember(ctx, squadID, characterID); err != nil {
			return fmt.Errorf("rbac: removing character %d from squad %s: %w", characterID, squadID, err)
		}
		if !ok {
			return nil
		}
		return RefreshUser(ctx, s, userID)
	})
}

// DeleteRole deletes a non-system role (db/queries/rbac.sql's DeleteRole
// guards system roles at the SQL level: `AND NOT is_system`, so this is a
// safe no-op — not an error — against `admin`/`member`) and refreshes
// every user the role reached, computed BEFORE the delete cascades away
// app.role_grant/app.user_role/app.squad_role rows referencing it (all
// three carry ON DELETE CASCADE — 00005_platform_rbac.sql).
func DeleteRole(ctx context.Context, pool store.Pool, roleID uuid.UUID) error {
	return store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		affected, err := UsersAffectedByRole(ctx, s, roleID)
		if err != nil {
			return err
		}
		if err := s.DeleteRole(ctx, roleID); err != nil {
			return fmt.Errorf("rbac: deleting role %s: %w", roleID, err)
		}
		return RefreshUsers(ctx, s, affected)
	})
}

// CreateRole is a thin pass-through — role creation alone changes no
// user's effective permissions until a grant/assignment follows, so no
// materialize call is needed here.
func CreateRole(ctx context.Context, s *store.Store, name string, description *string, isSystem bool) (gen.AppRole, error) {
	return s.CreateRole(ctx, name, description, isSystem)
}
