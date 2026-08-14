//go:build integration

package rbac_test

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	poolCfg, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)
	poolCfg.MaxConns = 50
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

func seedUser(t testing.TB, s *store.Store) uuid.UUID {
	t.Helper()
	u, err := s.CreateUser(context.Background(), "Test User "+uuid.NewString())
	require.NoError(t, err)
	return u.UserID
}

var nextCharacterID int64 = 90050000000

func seedCharacterForUser(t testing.TB, s *store.Store, userID uuid.UUID) int64 {
	t.Helper()
	nextCharacterID++
	id := nextCharacterID
	_, err := s.UpsertCharacter(context.Background(), gen.UpsertCharacterParams{
		CharacterID: id, UserID: uuid.NullUUID{UUID: userID, Valid: true},
		Name: "Char " + uuid.NewString(), OwnerHash: "oh-" + uuid.NewString(),
	})
	require.NoError(t, err)
	return id
}

func seedRole(t testing.TB, s *store.Store, name string, grants map[string]rbac.Effect) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r, err := s.CreateRole(ctx, name+"-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	for permission, effect := range grants {
		_, err := s.AddRoleGrant(ctx, r.RoleID, permission, string(effect))
		require.NoError(t, err)
	}
	return r.RoleID
}

// TestMaterializedMatchesRecomputed (roadmap exit criterion): the
// materialised table agrees with a from-scratch resolution for 1000
// random users. "From scratch" means internal/rbac.ResolveAllLive, which
// never reads app.effective_permission — only app.role_grant/user_role/
// squad_role/squad_member/character — so this genuinely cross-checks
// what materialize.go wrote against an independent re-derivation.
func TestMaterializedMatchesRecomputed(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	// A handful of roles with overlapping, sometimes-conflicting grants
	// (including one denying a permission another role allows, and one
	// carrying the superuser fallback) so real deny-precedence and
	// superuser interaction is exercised across the random population,
	// not just independent grants.
	roleAllowCharacters := seedRole(t, s, "allow-characters", map[string]rbac.Effect{"characters.view": rbac.EffectAllow})
	roleDenyCharacters := seedRole(t, s, "deny-characters", map[string]rbac.Effect{"characters.view": rbac.EffectDeny})
	roleAllowSquads := seedRole(t, s, "allow-squads", map[string]rbac.Effect{"squads.view": rbac.EffectAllow, "squads.apply": rbac.EffectAllow})
	roleSuperuser := seedRole(t, s, "superuser-role", map[string]rbac.Effect{rbac.SuperuserPermission: rbac.EffectAllow})
	roleDenySuperuser := seedRole(t, s, "deny-superuser-role", map[string]rbac.Effect{rbac.SuperuserPermission: rbac.EffectDeny})
	directRoles := []uuid.UUID{roleAllowCharacters, roleDenyCharacters, roleAllowSquads, roleSuperuser, roleDenySuperuser}

	squadOwner := seedUser(t, s)
	var squadIDs []uuid.UUID
	squadRolePool := [][]uuid.UUID{{roleAllowSquads}, {roleDenyCharacters}, {roleSuperuser, roleDenySuperuser}}
	for _, roles := range squadRolePool {
		sq, err := s.CreateSquad(ctx, gen.CreateSquadParams{Name: "squad-" + uuid.NewString(), Type: "open", OwnerUserID: squadOwner})
		require.NoError(t, err)
		for _, roleID := range roles {
			require.NoError(t, rbac.AddSquadRole(ctx, pool, sq.SquadID, roleID))
		}
		squadIDs = append(squadIDs, sq.SquadID)
	}

	rng := rand.New(rand.NewSource(42))
	const numUsers = 1000
	userIDs := make([]uuid.UUID, numUsers)
	for i := 0; i < numUsers; i++ {
		userID := seedUser(t, s)
		userIDs[i] = userID
		characterID := seedCharacterForUser(t, s, userID)

		// 0-2 direct roles.
		for n := rng.Intn(3); n > 0; n-- {
			role := directRoles[rng.Intn(len(directRoles))]
			require.NoError(t, rbac.AssignUserRole(ctx, pool, userID, role, uuid.NullUUID{}))
		}
		// membership in 0-1 squads.
		if rng.Intn(2) == 1 {
			squad := squadIDs[rng.Intn(len(squadIDs))]
			require.NoError(t, rbac.AddSquadMember(ctx, pool, squad, characterID))
		}

		// A user who drew zero direct roles AND zero squad memberships
		// above never triggered a RefreshUser call via either wrapper —
		// materialize explicitly here so every user in this test has a
		// real baseline row set to compare against (RefreshUser always
		// writes every permission, including explicit permitted=false
		// ones, so this is never a no-op for such a user; it IS a no-op,
		// harmlessly, for any user already materialized above).
		require.NoError(t, rbac.RefreshUser(ctx, s, userID))
	}

	mismatches := 0
	for _, userID := range userIDs {
		recomputed, err := rbac.ResolveAllLive(ctx, s, userID)
		require.NoError(t, err)

		rows, err := pool.Query(ctx, `SELECT permission, permitted FROM app.effective_permission WHERE user_id = $1`, userID)
		require.NoError(t, err)
		materialized := make(map[string]bool, len(domain.Permissions))
		for rows.Next() {
			var permission string
			var permitted bool
			require.NoError(t, rows.Scan(&permission, &permitted))
			materialized[permission] = permitted
		}
		rows.Close()

		for _, p := range domain.Permissions {
			want := recomputed[p.Name]
			got, ok := materialized[p.Name]
			if !ok || got != want {
				mismatches++
				t.Logf("user %s permission %s: recomputed=%v materialized=%v (present=%v)", userID, p.Name, want, got, ok)
			}
		}
	}
	require.Zero(t, mismatches, "the materialised table must agree with a from-scratch resolution for every user/permission pair")
}

// TestNoRolesMeansNoPermissionsDB is the DB-backed counterpart to
// resolve_test.go's pure TestNoRolesMeansNoPermissions: a user who has
// never been granted any role, directly or via a squad, is materialised
// (or resolves live) to zero permitted permissions — never the absence of
// a row being treated as anything but denied.
func TestNoRolesMeansNoPermissionsDB(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	userID := seedUser(t, s)
	require.NoError(t, rbac.RefreshUser(ctx, s, userID))

	permitted, err := s.ListEffectivePermissions(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, permitted, "a user with zero roles must have zero permitted rows")

	live, err := rbac.ResolveAllLive(ctx, s, userID)
	require.NoError(t, err)
	for permission, ok := range live {
		require.Falsef(t, ok, "permission %q must resolve false for a user with zero roles", permission)
	}
}

// TestMaterializationIsTransactionallyConsistentWithGrantChange (roadmap
// edge case): rolling back the mutating transaction must prove neither
// the grant change nor the materialisation refresh survived — the same
// pattern as the outbox-event transactional test from an earlier phase.
func TestMaterializationIsTransactionallyConsistentWithGrantChange(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	role, err := s.CreateRole(ctx, "rollback-role-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	userID := seedUser(t, s)
	require.NoError(t, s.AssignUserRole(ctx, userID, role.RoleID, uuid.NullUUID{}))
	require.NoError(t, rbac.RefreshUser(ctx, s, userID)) // baseline: permitted=false for everything

	forcedErr := errors.New("forced rollback")
	err = store.WithTx(ctx, pool, func(ctx context.Context, s *store.Store) error {
		if _, err := s.AddRoleGrant(ctx, role.RoleID, "characters.view", string(rbac.EffectAllow)); err != nil {
			return err
		}
		if err := rbac.RefreshUser(ctx, s, userID); err != nil {
			return err
		}
		return forcedErr // never commits
	})
	require.ErrorIs(t, err, forcedErr)

	grants, err := s.ListRoleGrants(ctx, role.RoleID)
	require.NoError(t, err)
	require.Empty(t, grants, "the role_grant insert must not have survived the rollback")

	row, err := s.GetEffectivePermission(ctx, userID, "characters.view")
	require.NoError(t, err) // the baseline RefreshUser call above DID commit, writing permitted=false
	require.False(t, row.Permitted, "the materialisation refresh inside the rolled-back transaction must not have survived either")
}

// TestResolve5000UsersUnderBudget (roadmap exit criterion,
// BenchmarkResolve5000Users): < 2 ms per resolution at 5000 users, against
// the materialised table's single indexed lookup — never a live
// role_grant join. A plain Test (not just a `go test -bench` benchmark)
// so the budget is actually enforced in normal CI runs, matching this
// repository's existing "ledger timing budget" precedent
// (internal/esi/ratelimit) of skipping the strict timing assertion only
// under -race, whose instrumentation overhead would make the budget
// spuriously fail without reflecting a real regression.
func TestResolve5000UsersUnderBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)

	const numUsers = 5000
	userIDs := make([]uuid.UUID, numUsers)
	for i := 0; i < numUsers; i++ {
		userIDs[i] = seedUser(t, s)
	}

	// Bulk-populate app.effective_permission directly — this test
	// benchmarks the READ path (GetEffectivePermission's single indexed
	// lookup), not materialize.go's per-user write loop, so setup uses
	// one fast set-based INSERT instead of 5000 individual RefreshUser
	// calls (each of which would itself issue ~27 round trips).
	_, err := pool.Exec(ctx, `
		INSERT INTO app.effective_permission (user_id, permission, permitted)
		SELECT u, p.permission, (row_number() OVER () % 3 = 0)
		  FROM unnest($1::uuid[]) AS u
		 CROSS JOIN app.permission p`, userIDs)
	require.NoError(t, err)

	benchmarkResolve5000Users(t, s, userIDs)
}

func benchmarkResolve5000Users(t testing.TB, s *store.Store, userIDs []uuid.UUID) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))
	const iterations = 2000

	start := time.Now()
	for i := 0; i < iterations; i++ {
		userID := userIDs[rng.Intn(len(userIDs))]
		permission := domain.Permissions[rng.Intn(len(domain.Permissions))].Name
		_, err := s.GetEffectivePermission(ctx, userID, permission)
		require.NoError(t, err)
	}
	elapsed := time.Since(start)
	perOp := elapsed / iterations
	t.Logf("GetEffectivePermission: %s total, %s/op over %d lookups against %d users", elapsed, perOp, iterations, len(userIDs))
	require.Lessf(t, perOp, 2*time.Millisecond, "materialised-table lookup must stay under the 2ms/resolution budget at 5000 users, got %s/op", perOp)
}

// BenchmarkResolve5000Users is the roadmap's named benchmark, runnable via
// `go test -tags integration -bench BenchmarkResolve5000Users ./internal/rbac/...`.
// It reuses the same setup as TestResolve5000UsersUnderBudget so both give
// the same answer through different Go testing entry points.
func BenchmarkResolve5000Users(b *testing.B) {
	pool := newMigratedPool(b)
	ctx := context.Background()
	s := store.New(pool)

	const numUsers = 5000
	userIDs := make([]uuid.UUID, numUsers)
	for i := 0; i < numUsers; i++ {
		userIDs[i] = seedUser(b, s)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO app.effective_permission (user_id, permission, permitted)
		SELECT u, p.permission, (row_number() OVER () % 3 = 0)
		  FROM unnest($1::uuid[]) AS u
		 CROSS JOIN app.permission p`, userIDs)
	require.NoError(b, err)

	rng := rand.New(rand.NewSource(7))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		userID := userIDs[rng.Intn(len(userIDs))]
		permission := domain.Permissions[rng.Intn(len(domain.Permissions))].Name
		_, err := s.GetEffectivePermission(ctx, userID, permission)
		require.NoError(b, err)
	}
}

// ── PHASE 20.2, DEFECT B40 ───────────────────────────────────────────────

// TestFirstLoginPromotesExactlyOneAdministrator is B40's exit criterion. A
// freshly installed HANGAR seeds `admin` and `member` with NO GRANTS, so
// before this phase the first SSO user held zero permissions and no route
// existed by which anyone could grant them one.
func TestFirstLoginPromotesExactlyOneAdministrator(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	first := seedUser(t, s)
	promoted, err := rbac.BootstrapFirstAdmin(ctx, pool, first)
	require.NoError(t, err)
	require.True(t, promoted, "an installation with no administrator must promote its first authenticated user")

	// The promotion is only real if app.effective_permission carries it —
	// middleware.RequirePermission reads nothing else, and an unmaterialised
	// grant is a grant that does not exist (defect B21's second half).
	row, err := s.GetEffectivePermission(ctx, first, domain.SuperuserPermission)
	require.NoError(t, err)
	require.True(t, row.Permitted, "the superuser grant must be materialised, not merely inserted")

	perms, err := s.ListEffectivePermissions(ctx, first)
	require.NoError(t, err)
	require.NotEmpty(t, perms, "the first administrator must hold the whole closed set through superuser")

	// The SECOND user logging in is an ordinary member. This is the guard
	// that stops "first login" meaning "every login".
	second := seedUser(t, s)
	promoted, err = rbac.BootstrapFirstAdmin(ctx, pool, second)
	require.NoError(t, err)
	require.False(t, promoted, "an installation that already has an administrator must not promote anyone else")

	_, err = s.GetEffectivePermission(ctx, second, domain.SuperuserPermission)
	require.Error(t, err, "the second user must hold nothing — zero roles is zero permissions")
}

// TestBootstrapRespectsACuratedAdminRole covers the operator who has
// already decided what `admin` may do. The role is still assigned — someone
// has to be able to administer the installation — but its curated grant set
// is not silently replaced by a superuser grant.
func TestBootstrapRespectsACuratedAdminRole(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	roles, err := s.ListRoles(ctx)
	require.NoError(t, err)
	var adminRole gen.AppRole
	for _, r := range roles {
		if r.Name == rbac.BootstrapRoleName {
			adminRole = r
		}
	}
	require.NotEqual(t, uuid.Nil, adminRole.RoleID, "db/seed/roles.sql must have seeded the admin role")

	// An operator curated the role before anyone logged in.
	_, err = s.AddRoleGrant(ctx, adminRole.RoleID, "admin.sync.view", "allow")
	require.NoError(t, err)

	user := seedUser(t, s)
	promoted, err := rbac.BootstrapFirstAdmin(ctx, pool, user)
	require.NoError(t, err)
	require.True(t, promoted)

	grants, err := s.ListRoleGrants(ctx, adminRole.RoleID)
	require.NoError(t, err)
	require.Len(t, grants, 1, "a curated admin role must not gain a superuser grant behind the operator's back")
	require.Equal(t, "admin.sync.view", grants[0].Permission)

	sync, err := s.GetEffectivePermission(ctx, user, "admin.sync.view")
	require.NoError(t, err)
	require.True(t, sync.Permitted, "the curated grant must still reach the user")
}

// TestBootstrapIsSuppressedByADeniedSuperuser pins the agreement between
// CountSuperuserHolders and internal/rbac.Resolve's absolute deny
// precedence. A user whose superuser grant is DENIED is not an
// administrator, so an installation holding only that user still has none —
// and must still be able to bootstrap one. If the two disagreed, such an
// installation would be permanently locked out.
func TestBootstrapIsSuppressedByADeniedSuperuser(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	denied := seedUser(t, s)
	role, err := rbac.CreateRole(ctx, s, "denied-super-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	_, err = s.AddRoleGrant(ctx, role.RoleID, domain.SuperuserPermission, "allow")
	require.NoError(t, err)
	_, err = s.AddRoleGrant(ctx, role.RoleID, domain.SuperuserPermission, "deny")
	require.NoError(t, err)
	require.NoError(t, s.AssignUserRole(ctx, denied, role.RoleID, uuid.NullUUID{}))

	live, err := rbac.ResolveLive(ctx, s, denied, domain.SuperuserPermission)
	require.NoError(t, err)
	require.False(t, live, "deny beats allow absolutely — this user is not an administrator")

	promoted, err := rbac.BootstrapFirstAdmin(ctx, pool, seedUser(t, s))
	require.NoError(t, err)
	require.True(t, promoted,
		"a denied superuser must not count as an administrator, or the installation can never gain one")
}

// TestBootstrapNamesAMissingSeededRole — cmd/hangar's SSO hook swallows a
// bootstrap error so a failed promotion cannot fail a login, which makes
// the error text the only thing an operator will ever see. It has to say
// what to do, not just what failed.
func TestBootstrapNamesAMissingSeededRole(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, "DELETE FROM app.role WHERE name = $1", rbac.BootstrapRoleName)
	require.NoError(t, err)

	_, err = rbac.BootstrapFirstAdmin(ctx, pool, seedUser(t, s))
	require.Error(t, err)
	require.Contains(t, err.Error(), "hangar migrate up",
		"the error must tell an operator what to do, not just what failed")
}
