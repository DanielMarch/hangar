package rbac

// ── PHASE 20.2, DEFECT B40: THE FIRST ADMINISTRATOR ──────────────────────
//
// THE OBSERVED SYMPTOM. Log into a freshly installed HANGAR with a real EVE
// SSO character. /api/v1/me answers 200 — it needs only a resolved session
// — and every character and corporation endpoint answers 403. The sync
// engine is running, real data is landing in real tables, and the operator
// who installed the thing cannot see a byte of it.
//
// THE CAUSE. app.effective_permission is the ONLY thing
// middleware.RequirePermission reads, and it is written only by
// internal/rbac's materialiser, which runs only when a grant changes.
// db/seed/roles.sql ships `admin` and `member` with NO GRANTS AT ALL,
// deliberately — "safer than shipping a silently over-privileged default" —
// and names "an operator (or a later migration)" as what fills them. Until
// Phase 20.2 there was no such operator action reachable from a browser and
// no route that could create one, so the answer to "how does the first
// person get a permission" was: they run `hangar admin bootstrap-token` in
// a shell, which mints a SEPARATE user with an API token and does nothing
// at all for the human who just completed SSO.
//
// ── THE DECISION, MADE EXPLICITLY ────────────────────────────────────────
//
// Three options were on the table: seed a superuser role on first login,
// require an operator command, or promote through the bootstrap token.
//
// FIRST-LOGIN PROMOTION IS WHAT SHIPS, under one narrow, checkable
// condition: the installation has no administrator yet. Concretely,
// BootstrapFirstAdmin promotes a user if and only if NOBODY currently holds
// an allowed `superuser` grant by any path. That is a property of the
// database, not a "first" counted by row order or timestamp, which matters
// because a restored backup, a re-created user, or a race between two
// simultaneous logins must not each look like "the first".
//
// Why not the operator command alone: it already exists
// (`hangar admin bootstrap-token`) and it is kept — see below — but an
// installation whose ONLY path to a usable browser session is a shell
// command on the host fails Gate 5's usability bar and cannot be verified
// in a browser at all, which is the specific thing blocking this phase.
//
// Why not bootstrap-token promotion: it inverts the dependency. The token
// exists to configure an installation before any human has authenticated;
// making the human's access depend on it means the operator must run a CLI
// command, copy a secret, and make an API call before they can look at
// their own corporation.
//
// WHAT THE FIRST USER GETS: the seeded `admin` role, and — only if that
// role has no grants at all — a single `superuser` allow on it. Superuser
// is a permission resolved through the ordinary grant path, never a bypass
// branch (see Resolve), so it is deniable, revocable, and visible on the
// role-management surface like anything else. An operator who has already
// curated `admin`'s grants gets their curation respected: the role is
// assigned, no grant is added.
//
// WHAT IT IS NOT: it is not a per-installation flag, not an env var, and
// not silent. It logs, it writes an app.outbox_event through the ordinary
// rbac wrappers, and the promotion is recorded in the security log by its
// caller.

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/events"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// BootstrapRoleName is the seeded role (db/seed/roles.sql) the first
// administrator is placed in. cmd/hangar's bootstrap-token command uses the
// same constant so the two paths cannot pick different roles.
const BootstrapRoleName = "admin"

// BootstrapFirstAdmin promotes userID to the seeded `admin` role if and
// only if the installation currently has no administrator.
//
// It reports whether it promoted. A `false` with a nil error is the normal,
// expected outcome on every login after the first and is not a failure.
//
// The whole thing — the has-anybody check, the grant, the assignment and
// the materialisation — runs in ONE transaction, so two first logins racing
// each other cannot both see an empty installation and both promote. The
// second one's check reads the first one's committed state or blocks on its
// rows; either way exactly one promotion happens.
func BootstrapFirstAdmin(ctx context.Context, pool store.Pool, userID uuid.UUID) (promoted bool, err error) {
	err = events.Transact(ctx, pool, func(ctx context.Context, s *store.Store, out *events.Recorder) error {
		existing, err := s.CountSuperuserHolders(ctx, SuperuserPermission)
		if err != nil {
			return fmt.Errorf("rbac: counting existing administrators: %w", err)
		}
		if existing > 0 {
			return nil
		}

		role, err := bootstrapRole(ctx, s)
		if err != nil {
			return err
		}

		grants, err := s.ListRoleGrants(ctx, role.RoleID)
		if err != nil {
			return fmt.Errorf("rbac: reading %q role grants: %w", BootstrapRoleName, err)
		}
		if len(grants) == 0 {
			if _, err := s.AddRoleGrant(ctx, role.RoleID, SuperuserPermission, string(EffectAllow)); err != nil {
				return fmt.Errorf("rbac: granting %s to %q: %w", SuperuserPermission, BootstrapRoleName, err)
			}
		}
		if err := s.AssignUserRole(ctx, userID, role.RoleID, uuid.NullUUID{}); err != nil {
			return fmt.Errorf("rbac: assigning the %q role to %s: %w", BootstrapRoleName, userID, err)
		}

		out.Record(events.Event{
			Aggregate: "user", AggregateID: userID.String(), Type: events.TypeUserRoleAssigned,
			Payload: map[string]any{"role_id": role.RoleID, "reason": "first_administrator_bootstrap"},
		})

		// Materialise, or the grant does not exist as far as any guarded
		// route is concerned — this is the step cmd/hangar's bootstrap-token
		// command had to learn the hard way (defect B21's second half).
		if err := RefreshUser(ctx, s, userID); err != nil {
			return err
		}
		promoted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return promoted, nil
}

// bootstrapRole finds the seeded role by name.
//
// It is located off ListRoles rather than through a name-keyed query for
// the same reason cmd/hangar's bootstrap-token command does it: the role
// set is a handful of seeded rows, and a generated query for one lookup is
// not worth a schema-generation round trip. A missing role means the seed
// never ran, which is an installation error worth naming precisely rather
// than failing later on a nil uuid.
func bootstrapRole(ctx context.Context, s *store.Store) (gen.AppRole, error) {
	roles, err := s.ListRoles(ctx)
	if err != nil {
		return gen.AppRole{}, fmt.Errorf("rbac: listing roles: %w", err)
	}
	for _, r := range roles {
		if r.Name == BootstrapRoleName {
			return r, nil
		}
	}
	return gen.AppRole{}, fmt.Errorf("rbac: the seeded %q role is missing — run 'hangar migrate up' first", BootstrapRoleName)
}
