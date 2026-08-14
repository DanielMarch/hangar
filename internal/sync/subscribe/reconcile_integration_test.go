//go:build integration

// Defect B42's regression cover. The reconciliation logic IS the SQL — four
// set-based statements whose correctness lives entirely in their joins and
// their NOT EXISTS scope gate — so a unit test against a fake store would
// assert that Go called a method, which is exactly the kind of test that
// let B42 exist in the first place. These run against real Postgres.
package subscribe_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/subscribe"
	"github.com/hangar-project/hangar/internal/sync/worker"
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

// seedRoute inserts one catalogued GET route at an upstream path, with the
// scopes it requires.
func seedRoute(t *testing.T, s *store.Store, path string, scopes ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	route, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID:       "op" + uuid.NewString(),
		Method:            "GET",
		UpstreamPath:      path,
		CompatibilityDate: pgtype.Date{Time: mustDate(t, "2026-01-01"), Valid: true},
		SpecFragment:      []byte(`{}`),
		IdentifierTypes:   []byte(`{}`),
	})
	require.NoError(t, err)

	for _, scope := range scopes {
		require.NoError(t, s.UpsertEsiScope(ctx, scope))
		require.NoError(t, s.AddEsiRouteScope(ctx, route.RouteID, scope))
	}
	return route.RouteID
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return d
}

// seedCharacter creates a character with a valid token carrying `granted`.
func seedCharacter(t *testing.T, s *store.Store, characterID int64, granted ...string) {
	t.Helper()
	ctx := context.Background()

	_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{
		CharacterID: characterID, Name: "Test", OwnerHash: "hash-" + uuid.NewString(),
	})
	require.NoError(t, err)
	require.NoError(t, s.UpsertCharacterToken(ctx, gen.UpsertCharacterTokenParams{
		CharacterID: characterID, KeyVersion: 1,
		WrappedDek: []byte("dek"), Nonce: []byte("nonce"), Ciphertext: []byte("ct"),
		OwnerHash: "hash",
	}))
	for _, scope := range granted {
		require.NoError(t, s.UpsertEsiScope(ctx, scope))
		require.NoError(t, s.AddCharacterTokenScope(ctx, characterID, scope))
	}
}

func countSubscriptions(t *testing.T, pool *pgxpool.Pool, kind sync.EntityKind) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM app.sync_subscription WHERE entity_kind = $1`, string(kind)).Scan(&n))
	return n
}

// TestScopeGateExcludesRoutesTheTokenCannotCall is the heart of it. A
// subscription whose route needs a scope the token never granted produces a
// guaranteed 403 on every attempt, and Governor 2 counts every 403 against
// an installation-wide budget of 100/minute — so creating those rows would
// spend the error budget on requests that cannot possibly succeed.
func TestScopeGateExcludesRoutesTheTokenCannotCall(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	characterPaths := worker.SubscribablePathsFor(sync.EntityCharacter)
	require.NotEmpty(t, characterPaths)
	granted, ungranted := characterPaths[0], characterPaths[1]

	seedRoute(t, s, granted, "esi-test.granted.v1")
	seedRoute(t, s, ungranted, "esi-test.NOT-granted.v1")

	const characterID = int64(90000001)
	seedCharacter(t, s, characterID, "esi-test.granted.v1")

	created, err := s.ReconcileCharacterSubscriptions(ctx, characterPaths, characterID)
	require.NoError(t, err)
	require.Equal(t, int64(1), created, "only the route whose scope was granted may be subscribed")

	var subscribedPath string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT r.upstream_path FROM app.sync_subscription s
		  JOIN app.esi_route r ON r.route_id = s.route_id`).Scan(&subscribedPath))
	require.Equal(t, granted, subscribedPath)
}

// TestPublicRouteNeedsNoScope — a route declaring no scopes satisfies the
// NOT EXISTS gate trivially, which is correct: the public routes
// (corporation history, alliance names) are callable by anyone.
func TestPublicRouteNeedsNoScope(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	path := worker.SubscribablePathsFor(sync.EntityCharacter)[0]
	seedRoute(t, s, path) // no scopes at all

	const characterID = int64(90000001)
	seedCharacter(t, s, characterID) // no scopes granted either

	created, err := s.ReconcileCharacterSubscriptions(ctx, []string{path}, characterID)
	require.NoError(t, err)
	require.Equal(t, int64(1), created, "a route requiring no scope must be subscribable by any valid token")
}

// TestReconciliationIsIdempotent — it runs on every replica, on a timer and
// on every login, so convergence has to be structural rather than the
// result of careful sequencing.
func TestReconciliationIsIdempotent(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	path := worker.SubscribablePathsFor(sync.EntityCharacter)[0]
	seedRoute(t, s, path, "esi-test.granted.v1")
	const characterID = int64(90000001)
	seedCharacter(t, s, characterID, "esi-test.granted.v1")

	first, err := subscribe.ForCharacter(ctx, s, characterID)
	require.NoError(t, err)
	require.Equal(t, int64(1), first.CharacterCreated)

	for range 3 {
		again, err := subscribe.ForCharacter(ctx, s, characterID)
		require.NoError(t, err)
		require.True(t, again.Empty(), "a reconciled installation must report a no-op, got %+v", again)
	}
	require.Equal(t, 1, countSubscriptions(t, pool, sync.EntityCharacter))
}

// TestCorporationSubscriptionsWaitForTheCharacterSync pins the ordered
// bootstrap, which is the reason reconciliation is periodic rather than a
// single pass at link time. A corporation subscription needs
// app.character.corporation_id, and that column is populated BY a character
// route — so at the moment a character authorises, their corporation is
// unknown and no amount of care at link time can create the rows.
func TestCorporationSubscriptionsWaitForTheCharacterSync(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	corpPath := worker.SubscribablePathsFor(sync.EntityCorporation)[0]
	seedRoute(t, s, corpPath, "esi-test.corp.v1")

	const characterID = int64(90000001)
	const corporationID = int64(98000001)
	seedCharacter(t, s, characterID, "esi-test.corp.v1")

	// Before the character sheet has synced, corporation_id is NULL.
	created, err := s.ReconcileCorporationSubscriptions(ctx, []string{corpPath})
	require.NoError(t, err)
	require.Zero(t, created, "no corporation is known yet, so nothing can be subscribed")

	// The character sheet syncs and fills in the corporation.
	_, err = s.UpsertCorporation(ctx, gen.UpsertCorporationParams{
		CorporationID: corporationID, Name: "Test Corp", Ticker: "TEST",
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE app.character SET corporation_id = $2 WHERE character_id = $1`,
		characterID, corporationID)
	require.NoError(t, err)

	// The NEXT pass creates it. This is the whole bootstrap sequence.
	created, err = s.ReconcileCorporationSubscriptions(ctx, []string{corpPath})
	require.NoError(t, err)
	require.Equal(t, int64(1), created, "once corporation_id is known the subscription must appear")
}

// TestScopeRevocationDisablesRatherThanDeletes — the row carries sync state
// (etag, last_modified, cursor_after) that is expensive to rebuild and still
// valid, so a scope that comes back should resume conditionally rather than
// re-fetch a whole collection. Deleting would also erase the evidence that
// the route WAS being polled.
func TestScopeRevocationDisablesRatherThanDeletes(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	path := worker.SubscribablePathsFor(sync.EntityCharacter)[0]
	seedRoute(t, s, path, "esi-test.granted.v1")
	const characterID = int64(90000001)
	seedCharacter(t, s, characterID, "esi-test.granted.v1")

	_, err := subscribe.ForCharacter(ctx, s, characterID)
	require.NoError(t, err)
	require.Equal(t, 1, countSubscriptions(t, pool, sync.EntityCharacter))

	// The user re-authorises with a narrower grant.
	_, err = pool.Exec(ctx, `DELETE FROM app.character_token_scope WHERE character_id = $1`, characterID)
	require.NoError(t, err)

	changed, err := s.DisableUnscopedSubscriptions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)

	var enabled bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT enabled FROM app.sync_subscription`).Scan(&enabled))
	require.False(t, enabled, "a subscription whose scope was revoked must be disabled")
	require.Equal(t, 1, countSubscriptions(t, pool, sync.EntityCharacter), "and must NOT be deleted")

	// Re-granting resumes it, rather than leaving it disabled forever.
	require.NoError(t, s.AddCharacterTokenScope(ctx, characterID, "esi-test.granted.v1"))
	changed, err = s.DisableUnscopedSubscriptions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)
	require.NoError(t, pool.QueryRow(ctx, `SELECT enabled FROM app.sync_subscription`).Scan(&enabled))
	require.True(t, enabled, "restoring the scope must re-enable the subscription")
}

// TestInvalidTokenDisablesSubscriptions — an invalidated token cannot call
// anything, so its subscriptions must stop being claimed.
func TestInvalidTokenDisablesSubscriptions(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	path := worker.SubscribablePathsFor(sync.EntityCharacter)[0]
	seedRoute(t, s, path, "esi-test.granted.v1")
	const characterID = int64(90000001)
	seedCharacter(t, s, characterID, "esi-test.granted.v1")
	_, err := subscribe.ForCharacter(ctx, s, characterID)
	require.NoError(t, err)

	reason := "invalid_grant"
	require.NoError(t, s.InvalidateCharacterToken(ctx, characterID, &reason))

	changed, err := s.DisableUnscopedSubscriptions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)

	var enabled bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT enabled FROM app.sync_subscription`).Scan(&enabled))
	require.False(t, enabled)
}

// TestGlobalSubscriptionsNeedNoCharacter — /status must be polled on an
// installation that has no characters at all. entity_id is 0 and
// acting_character_id stays NULL, because the route is unauthenticated.
func TestGlobalSubscriptionsNeedNoCharacter(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	globalPaths := worker.SubscribablePathsFor(sync.EntityGlobal)
	require.NotEmpty(t, globalPaths)
	seedRoute(t, s, globalPaths[0])

	created, err := s.ReconcileGlobalSubscriptions(ctx, globalPaths)
	require.NoError(t, err)
	require.Equal(t, int64(1), created)

	var entityID int64
	var acting *int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT entity_id, acting_character_id FROM app.sync_subscription`).Scan(&entityID, &acting))
	require.Zero(t, entityID, "a global subscription's entity_id is 0")
	require.Nil(t, acting, "a global route is unauthenticated and needs no acting character")
}

// TestBlockedAndRetiredRoutesAreNeverSubscribed — a route newer than the
// compatibility pin must not be polled (that is what blocked_by_pin means),
// and a route that has vanished from the spec keeps its row but must stop
// being polled.
func TestBlockedAndRetiredRoutesAreNeverSubscribed(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	ctx := context.Background()

	paths := worker.SubscribablePathsFor(sync.EntityCharacter)
	blocked, retired := paths[0], paths[1]
	blockedID := seedRoute(t, s, blocked)
	retiredID := seedRoute(t, s, retired)

	_, err := pool.Exec(ctx, `UPDATE app.esi_route SET blocked_by_pin = true WHERE route_id = $1`, blockedID)
	require.NoError(t, err)
	require.NoError(t, s.RetireEsiRoute(ctx, retiredID))

	const characterID = int64(90000001)
	seedCharacter(t, s, characterID)

	created, err := s.ReconcileCharacterSubscriptions(ctx, paths, characterID)
	require.NoError(t, err)
	require.Zero(t, created, "neither a pin-blocked nor a retired route may be subscribed")
}

// TestFanOutRoutesAreNotSubscribable guards the partition itself. Every
// detail route carries a SECOND dynamic path parameter beyond the owner, and
// a subscription row has exactly one entity_id — so a subscription naming
// one could never be resolved to a URL. They are in SyncSet() (they are
// polled, by their parent's sync) and must never be in SubscribableRoutes().
func TestFanOutRoutesAreNotSubscribable(t *testing.T) {
	subscribable := worker.SubscribableRoutes()
	for path := range worker.SyncSet() {
		if _, ok := subscribable[path]; ok {
			continue
		}
		require.Contains(t, path, "}/", "route %q is in the sync set but not subscribable, "+
			"which is only correct for a fan-out detail route carrying a second path parameter", path)
	}
	for path := range subscribable {
		require.NotEmpty(t, path)
	}
}
