//go:build integration

package db_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	hangardb "github.com/hangar-project/hangar/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// expectedPlatformTables is 02_DATABASE_SCHEMA.md §4's 51-table list,
// verbatim by name — the schema-diff source of truth for
// TestAllPlatformTablesPresent.
var expectedPlatformTables = []string{
	// §4.1 identity and access (10)
	"user", "character", "character_token", "character_token_scope", "session",
	"api_token", "api_token_access_log", "share_link", "security_log", "setting",
	// §4.2 RBAC and squads (10)
	"role", "permission", "role_grant", "user_role", "effective_permission",
	"squad", "squad_member", "squad_moderator", "squad_role", "squad_application",
	// §4.3 ESI gateway and sync metadata (12)
	"esi_route", "esi_scope", "esi_route_scope", "esi_route_role", "esi_pin_history",
	"esi_cache_entry", "esi_error_budget", "esi_ledger_bucket", "esi_ledger_entry",
	"esi_replica", "sync_subscription", "sync_run",
	// §4.4 access provisioning (5)
	"platform", "platform_group", "entitlement_rule", "provisioning_state", "provisioning_audit",
	// §4.5 alerting (6)
	"alert_type", "alert_channel", "alert_routing_rule", "alert_event", "alert_delivery", "notification_unknown_type",
	// §4.6 events and webhooks (3)
	"webhook_endpoint", "outbox_event", "webhook_delivery",
	// §4.7 shared reference and open vocabularies (5)
	"corporation", "alliance", "location", "open_vocabulary", "sde_import",
}

func newMigratedContainer(t *testing.T) (*pgxpool.Pool, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"),
		tcpostgres.WithUsername("hangar"),
		tcpostgres.WithPassword("hangar"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// The container's own wait strategy already blocks for two occurrences
	// of Postgres's "ready to accept connections" log line plus the port
	// listening, but on Docker Desktop for Windows the very first TCP
	// connection through the forwarded port sometimes races the daemon's
	// port-proxy setup and drops with an EOF during SSL negotiation before
	// the pool has connected at all — a host/platform transient, not a
	// schema or migration issue. Retry the first ping rather than let one
	// slow port-proxy attempt fail the whole test.
	require.Eventually(t, func() bool {
		return pool.Ping(ctx) == nil
	}, 20*time.Second, 250*time.Millisecond, "database never became reachable through the container's forwarded port")

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })

	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	return pool, sqlDB
}

// TestGooseUpDownIdempotent — clean up, clean down, clean up again on an
// empty PG18 (Phase 1a exit criterion).
func TestGooseUpDownIdempotent(t *testing.T) {
	_, sqlDB := newMigratedContainer(t)

	require.NoError(t, goose.DownTo(sqlDB, "migrations", 0))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	var n int
	require.NoError(t, sqlDB.QueryRow(
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app'`,
	).Scan(&n))
	require.Equal(t, len(expectedPlatformTables), n, "table count after up-down-up must match the 51-table platform tier")
}

// TestAllPlatformTablesPresent — all 51 tables of 02_… §4 exist.
func TestAllPlatformTablesPresent(t *testing.T) {
	_, sqlDB := newMigratedContainer(t)

	rows, err := sqlDB.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'app' ORDER BY table_name`)
	require.NoError(t, err)
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		got[name] = true
	}
	require.NoError(t, rows.Err())

	for _, want := range expectedPlatformTables {
		if !got[want] {
			t.Errorf("expected table app.%s not found", want)
		}
	}
	require.Len(t, got, len(expectedPlatformTables), "unexpected extra or missing tables in app schema: %v", got)
}

// TestLedgerTablesUnloggedAndCascade — both ledger tables are UNLOGGED;
// esi_ledger_entry cascades from esi_ledger_bucket.
func TestLedgerTablesUnloggedAndCascade(t *testing.T) {
	pool, sqlDB := newMigratedContainer(t)
	ctx := context.Background()

	for _, table := range []string{"esi_ledger_bucket", "esi_ledger_entry", "esi_cache_entry"} {
		var persistence string
		require.NoError(t, sqlDB.QueryRow(
			`SELECT relpersistence FROM pg_class JOIN pg_namespace n ON n.oid = relnamespace
			  WHERE n.nspname = 'app' AND relname = $1`, table,
		).Scan(&persistence))
		require.Equalf(t, "u", persistence, "app.%s must be UNLOGGED", table)
	}

	_, err := pool.Exec(ctx, `INSERT INTO app.esi_ledger_bucket (rate_limit_group, user_key, max_tokens, "window")
		VALUES ('test-group', 'test-user', 400, '1 minute'::interval)`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO app.esi_ledger_entry (rate_limit_group, user_key, cost, consumed_at, state)
		VALUES ('test-group', 'test-user', 5, now(), 'settled')`)
	require.NoError(t, err)

	var before int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.esi_ledger_entry WHERE rate_limit_group = 'test-group'`).Scan(&before))
	require.Equal(t, 1, before)

	_, err = pool.Exec(ctx, `DELETE FROM app.esi_ledger_bucket WHERE rate_limit_group = 'test-group' AND user_key = 'test-user'`)
	require.NoError(t, err, "application-level DELETE (not a migration) is how the bucket is retired")

	var after int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.esi_ledger_entry WHERE rate_limit_group = 'test-group'`).Scan(&after))
	require.Equal(t, 0, after, "esi_ledger_entry must cascade-delete when its bucket is removed")
}

// TestReplicaLivenessPredicate — two fresh heartbeats ⇒ 2 live; one aged
// past 30s ⇒ 1 live.
func TestReplicaLivenessPredicate(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()

	insert := `INSERT INTO app.esi_replica (replica_id, role, version, started_at, last_heartbeat)
	           VALUES ($1, 'serve', 'test', now(), $2)`
	_, err := pool.Exec(ctx, insert, uuid.New(), time.Now())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, insert, uuid.New(), time.Now())
	require.NoError(t, err)

	countLive := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM app.esi_replica WHERE last_heartbeat > now() - interval '30 seconds'`,
		).Scan(&n))
		return n
	}

	require.Equal(t, 2, countLive(), "two fresh heartbeats must count as 2 live replicas (clustered mode)")

	// Age one replica's heartbeat past the 30s liveness threshold.
	_, err = pool.Exec(ctx,
		`UPDATE app.esi_replica SET last_heartbeat = now() - interval '31 seconds' WHERE replica_id = (
		     SELECT replica_id FROM app.esi_replica ORDER BY started_at LIMIT 1
		 )`)
	require.NoError(t, err)

	require.Equal(t, 1, countLive(), "an aged-out heartbeat must drop the live count to 1 (solo mode)")
}
