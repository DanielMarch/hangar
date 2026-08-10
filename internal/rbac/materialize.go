package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

// RefreshUser recomputes and writes every permission in HANGAR's closed
// set for one user into app.effective_permission. RefreshEffectivePermission
// (db/queries/rbac.sql) is already IS DISTINCT FROM-guarded (02_DATABASE_
// SCHEMA.md §3.5), so re-running this for a user whose grants didn't
// actually change touches no updated_at-equivalent (`refreshed_at`) rows.
//
// Writes ALL permissions, not just the ones with an explicit grant —
// necessary because a superuser grant can newly imply `permitted = true`
// for a permission with zero direct grants at all, and a revoked
// superuser grant must newly imply `permitted = false` for those same
// rows. Recomputing one user's ~27-row closed set is cheap; the
// 5000-user benchmark is about NOT doing this for every user on every
// grant change, which is what the affected-set functions below exist to
// bound.
func RefreshUser(ctx context.Context, s *store.Store, userID uuid.UUID) error {
	permitted, err := ResolveAllLive(ctx, s, userID)
	if err != nil {
		return err
	}
	for permission, ok := range permitted {
		if err := s.RefreshEffectivePermission(ctx, userID, permission, ok); err != nil {
			return fmt.Errorf("rbac: refreshing effective_permission for user %s permission %q: %w", userID, permission, err)
		}
	}
	if PermissionsChangedHook != nil {
		if err := PermissionsChangedHook(ctx, s, userID); err != nil {
			return fmt.Errorf("rbac: permissions-changed hook for user %s: %w", userID, err)
		}
	}
	return nil
}

// RefreshUsers is RefreshUser over a set, de-duplicated so a user
// reachable by more than one path in the caller's affected-set
// computation is only ever recomputed once.
func RefreshUsers(ctx context.Context, s *store.Store, userIDs []uuid.UUID) error {
	seen := make(map[uuid.UUID]bool, len(userIDs))
	for _, id := range userIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := RefreshUser(ctx, s, id); err != nil {
			return err
		}
	}
	return nil
}

// UsersAffectedByRole is the affected set for a role_grant change
// (a permission/effect added or removed on a role) or a role deletion:
// every user holding that role, directly or via a squad.
func UsersAffectedByRole(ctx context.Context, s *store.Store, roleID uuid.UUID) ([]uuid.UUID, error) {
	direct, err := s.ListUsersWithRoleDirect(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("rbac: listing direct holders of role %s: %w", roleID, err)
	}
	viaSquad, err := s.ListUsersWithRoleViaSquad(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("rbac: listing squad-derived holders of role %s: %w", roleID, err)
	}
	out := make([]uuid.UUID, 0, len(direct)+len(viaSquad))
	out = append(out, direct...)
	for _, u := range viaSquad {
		if u.Valid {
			out = append(out, u.UUID)
		}
	}
	return out, nil
}

// UsersAffectedBySquadRole is the affected set for a squad_role change
// (a role added to or removed from a squad): every user with a character
// in that squad.
func UsersAffectedBySquadRole(ctx context.Context, s *store.Store, squadID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.ListUsersInSquad(ctx, squadID)
	if err != nil {
		return nil, fmt.Errorf("rbac: listing users in squad %s: %w", squadID, err)
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, u := range rows {
		if u.Valid {
			out = append(out, u.UUID)
		}
	}
	return out, nil
}

// UserAffectedBySquadMember is the affected set for one character joining
// or leaving a squad: just that character's linked user, if any — a
// character with no user_id affects nobody's effective_permission.
func UserAffectedBySquadMember(ctx context.Context, s *store.Store, characterID int64) (uuid.UUID, bool, error) {
	userID, err := s.GetCharacterUserID(ctx, characterID)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("rbac: resolving user for character %d: %w", characterID, err)
	}
	if !userID.Valid {
		return uuid.Nil, false, nil
	}
	return userID.UUID, true, nil
}
