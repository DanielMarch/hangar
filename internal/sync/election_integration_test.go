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
	for _, id := range []int64{charA, charC} {
		_, err := s.ReplaceCorporationRole(ctx, gen.ReplaceCorporationRoleParams{CorporationID: corporationID, CharacterID: id, Role: "Director"})
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
