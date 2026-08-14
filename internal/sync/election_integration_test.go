//go:build integration

package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	return pool
}

// seedElectionFixture builds one corporation with three member characters,
// a director-role-required route, and gives each candidate a distinct
// eligibility profile:
//   - char A (lowest id): valid token, has the required scope, has the
//     required role, zero 403s -> the correct winner.
//   - char B: valid token, has scope, MISSING the required role -> ineligible.
//   - char C (highest id): valid token, has scope, has role, but with
//     recorded 403 history against this route -> eligible but worse-ranked
//     than A.
func seedElectionFixture(t *testing.T, s *store.Store, corporationID int64) (routeID uuid.UUID, charA, charB, charC int64) {
	t.Helper()
	ctx := context.Background()

	_, err := s.UpsertCorporation(ctx, gen.UpsertCorporationParams{CorporationID: corporationID, Name: "Test Corp", Ticker: "TEST"})
	require.NoError(t, err)

	charA, charB, charC = 90000001, 90000002, 90000003
	for _, id := range []int64{charA, charB, charC} {
		_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{CharacterID: id, Name: "Char", OwnerHash: "hash-" + uuid.NewString()})
		require.NoError(t, err)
		_, err = s.UpsertCorporationMember(ctx, corporationID, id)
		require.NoError(t, err)
	}

	route, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID: "test-op-" + uuid.NewString(), Method: "GET",
		UpstreamPath:      "/corporations/{corporation_id}/structures",
		CompatibilityDate: pgtype.Date{Time: time.Now(), Valid: true},
		SpecFragment:      []byte(`{}`), IdentifierTypes: []byte(`{}`),
	})
	require.NoError(t, err)
	routeID = route.RouteID

	require.NoError(t, s.UpsertEsiScope(ctx, "esi-corporations.read_structures.v1"))
	require.NoError(t, s.AddEsiRouteScope(ctx, routeID, "esi-corporations.read_structures.v1"))
	require.NoError(t, s.AddEsiRouteRole(ctx, routeID, "Director"))

	for _, id := range []int64{charA, charB, charC} {
		require.NoError(t, s.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
			CharacterID: id, KeyVersion: 1, WrappedDek: []byte("dek"), Nonce: []byte("nonce"),
			Ciphertext: []byte("ct"), OwnerHash: "owner-" + uuid.NewString(),
		}))
		require.NoError(t, s.AddCharacterTokenScope(ctx, id, "esi-corporations.read_structures.v1"))
	}

	// A and C have the Director role; B does not.
	//
	// PHASE 20.1.1 (defect B45): seeded into app.character_role, which is
	// where GET /characters/{character_id}/roles actually lands, rather than
	// app.corporation_role. This fixture used the latter, and so did the
	// elector — but NOTHING in production writes app.corporation_role, so
	// both the test and the code agreed on a table that is permanently empty
	// on every real installation. The test passed and the feature could
	// never work, which is the same shape as B20 one layer down.
	for _, id := range []int64{charA, charC} {
		_, err := s.ReplaceCharacterRole(ctx, gen.ReplaceCharacterRoleParams{CharacterID: id, Role: sync.RoleDirector})
		require.NoError(t, err)
	}

	// C has a recorded 403 against this exact (entity, route); A and B do not.
	require.NoError(t, s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
		EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: routeID, CharacterID: charC,
	}))

	return routeID, charA, charB, charC
}

// TestActingCharacterElectionIsDeterministic (roadmap exit criterion):
// same inputs, same character elected, every time — run the election many
// times against the identical fixture and assert every run agrees.
func TestActingCharacterElectionIsDeterministic(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	elector := sync.DBElector{Store: s}
	ctx := context.Background()

	const corporationID = 98000001
	routeID, charA, charB, _ := seedElectionFixture(t, s, corporationID)
	_ = charB

	var winners []int64
	for i := 0; i < 20; i++ {
		winner, err := elector.Elect(ctx, sync.EntityCorporation, corporationID, routeID)
		require.NoError(t, err)
		winners = append(winners, winner)
	}
	for i, w := range winners {
		require.Equalf(t, charA, w, "run %d elected %d, want the deterministic winner %d (valid token + scope + role + fewest 403s + lowest id)", i, w, charA)
	}
}

// TestActingCharacterFallbackOn403 (roadmap exit criterion): a 403
// re-elects deterministically and does not disable the subscription. This
// test proves the FALLBACK half directly: once the current winner (charA)
// itself accumulates enough 403s to rank behind another eligible candidate,
// Elect switches to that candidate on the very next call — no inline retry,
// no disabling, just a fresh election reading updated history.
func TestActingCharacterFallbackOn403(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	elector := sync.DBElector{Store: s}
	ctx := context.Background()

	const corporationID = 98000002
	routeID, charA, _, charC := seedElectionFixture(t, s, corporationID)

	winner, err := elector.Elect(ctx, sync.EntityCorporation, corporationID, routeID)
	require.NoError(t, err)
	require.Equal(t, charA, winner, "precondition: charA must win before any 403 against it")

	// charA now 403s twice — worse than charC's single recorded 403 from
	// the fixture — so the NEXT election (a fresh call, simulating the
	// next scheduled attempt, never an inline retry within the same one)
	// must pick charC instead.
	require.NoError(t, s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
		EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: routeID, CharacterID: charA,
	}))
	require.NoError(t, s.RecordActingCharacter403(ctx, gen.RecordActingCharacter403Params{
		EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: routeID, CharacterID: charA,
	}))

	reElected, err := elector.Elect(ctx, sync.EntityCorporation, corporationID, routeID)
	require.NoError(t, err)
	require.Equal(t, charC, reElected, "after charA accumulates more 403s than charC, re-election must switch to charC")
	require.NotEqual(t, charA, reElected, "re-election must not return the same character that just failed")

	// The subscription itself is untouched by any of this — 403 handling
	// never disables anything (roadmap: "does not disable the subscription").
	sub, err := s.UpsertSyncSubscription(ctx, gen.UpsertSyncSubscriptionParams{
		EntityKind: string(sync.EntityCorporation), EntityID: corporationID, RouteID: routeID, ActingCharacterID: &reElected,
	})
	require.NoError(t, err)
	require.True(t, sub.Enabled, "electing/re-electing must never disable the subscription")
}

// TestCeoAndDirectorHoldEveryRoleImplicitly is defect B46's regression
// cover, and it is a rule about EVE rather than about HANGAR.
//
// Corporation roles are hierarchical, and ESI reports them literally: the
// CEO holds every role and cannot be stripped of any, and a Director
// likewise holds all roles implicitly, so ESI never enumerates the
// subordinate roles for either. A literal set-membership test against the
// returned list therefore refuses the single most privileged character in
// the corporation — usually the only one who can serve the route at all.
//
// Found against a real CEO's token: the character held Director and was
// being refused all 32 corporation routes.
func TestCeoAndDirectorHoldEveryRoleImplicitly(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()
	elector := sync.DBElector{Store: s}

	const corporationID = int64(98000050)
	const ceo = int64(90000050)
	const director = int64(90000051)
	const accountant = int64(90000052)

	_, err := s.UpsertCorporation(ctx, gen.UpsertCorporationParams{
		CorporationID: corporationID, Name: "Hierarchy Corp", Ticker: "HIER",
	})
	require.NoError(t, err)

	require.NoError(t, s.UpsertEsiScope(ctx, "esi-corporations.read_divisions.v1"))

	for _, id := range []int64{ceo, director, accountant} {
		_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{
			CharacterID: id, Name: "Char", OwnerHash: "owner-" + uuid.NewString(),
		})
		require.NoError(t, err)
		_, err = s.UpsertCorporationMember(ctx, corporationID, id)
		require.NoError(t, err)
		require.NoError(t, s.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
			CharacterID: id, KeyVersion: 1, WrappedDek: []byte("dek"), Nonce: []byte("nonce"),
			Ciphertext: []byte("ct"), OwnerHash: "owner-" + uuid.NewString(),
		}))
		require.NoError(t, s.AddCharacterTokenScope(ctx, id, "esi-corporations.read_divisions.v1"))
	}

	// A route requiring Accountant, which is a SUBORDINATE role.
	route, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID: "test-op-" + uuid.NewString(), Method: "GET",
		UpstreamPath:      "/corporations/{corporation_id}/divisions",
		CompatibilityDate: pgtype.Date{Time: time.Now(), Valid: true},
		SpecFragment:      []byte(`{}`), IdentifierTypes: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, s.AddEsiRouteScope(ctx, route.RouteID, "esi-corporations.read_divisions.v1"))
	require.NoError(t, s.AddEsiRouteRole(ctx, route.RouteID, "Accountant"))

	// The CEO is recorded ONLY as ceo_id — no role rows at all, which is
	// exactly how a corporation sheet describes them.
	_, err = pool.Exec(ctx, `UPDATE app.corporation SET ceo_id = $2 WHERE corporation_id = $1`, corporationID, ceo)
	require.NoError(t, err)

	// The Director holds only "Director"; ESI does not list Accountant.
	_, err = s.ReplaceCharacterRole(ctx, gen.ReplaceCharacterRoleParams{CharacterID: director, Role: sync.RoleDirector})
	require.NoError(t, err)

	// The third character holds Accountant literally.
	_, err = s.ReplaceCharacterRole(ctx, gen.ReplaceCharacterRoleParams{CharacterID: accountant, Role: "Accountant"})
	require.NoError(t, err)

	// All three must be eligible. The election's own ordering picks the
	// lowest character id among equals, which is the CEO here — what
	// matters for this test is that it succeeds at all, and that removing
	// the literal Accountant holder does not change that.
	elected, err := elector.Elect(ctx, sync.EntityCorporation, corporationID, route.RouteID)
	require.NoError(t, err)
	require.Contains(t, []int64{ceo, director, accountant}, elected)

	// With the literal role-holder gone, a CEO or Director must still serve
	// the route — this is the case that was failing in production.
	_, err = pool.Exec(ctx, `DELETE FROM app.corporation_member WHERE character_id = $1`, accountant)
	require.NoError(t, err)

	elected, err = elector.Elect(ctx, sync.EntityCorporation, corporationID, route.RouteID)
	require.NoError(t, err, "a CEO or Director must satisfy a subordinate role requirement")
	require.Contains(t, []int64{ceo, director}, elected)

	// And with ONLY the CEO left — no role rows whatsoever.
	_, err = pool.Exec(ctx, `DELETE FROM app.corporation_member WHERE character_id = $1`, director)
	require.NoError(t, err)

	elected, err = elector.Elect(ctx, sync.EntityCorporation, corporationID, route.RouteID)
	require.NoError(t, err, "the CEO holds every role implicitly and must be electable with no role rows at all")
	require.Equal(t, ceo, elected)
}
