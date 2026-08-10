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
	// PHASE 8 ADDITION (1): sync_acting_character_history
	// (00031_phase8_acting_character_history.sql) — a platform/sync-engine
	// table alongside sync_subscription/sync_run above, not a domain
	// projection; §5.2's original table map has no phase-8-specific gap for
	// it because the gap it fixes (per-candidate 403 history for §6.3
	// election, which app.sync_subscription.consecutive_403 alone cannot
	// serve) was only discovered during Phase 8 implementation. Reported as
	// a specification gap, fixed with a real migration rather than worked
	// around — see that migration's header for the full account. This is
	// the one legitimate addition to SRS v3.1 §5.2's "≈129" platform+domain
	// total; TestAllDomainTablesPresent's wantTotal below is 135, not 134,
	// for exactly this reason.
	"sync_acting_character_history",
	// PHASE 12 ADDITION (1): discord_invalid_budget
	// (00038_phase12_discord_invalid_budget.sql) — the installation-wide
	// Discord invalid-request (401/403/429) counter, alongside
	// esi_error_budget above; §4.4's original access-provisioning table
	// map (the 5 tables platform/platform_group/entitlement_rule/
	// provisioning_state/provisioning_audit) predates Phase 12's driver
	// entirely, so there was never a slot reserved for this. Same
	// shape/reasoning as Phase 8's addition above — a real migration, not a
	// worked-around gap. TestAllDomainTablesPresent's wantTotal is 136, not
	// 135, for exactly this reason.
	"discord_invalid_budget",
	// PHASE 13 ADDITION (1): teamspeak_challenge
	// (00039_phase13_teamspeak_challenge.sql) — the single-use TS3 linking
	// token table, alongside session/api_token above; §4.4's original
	// table map has no slot for this either, for the same "predates the
	// driver phase" reason as Phase 12's addition. Same shape/reasoning
	// again. TestAllDomainTablesPresent's wantTotal is 137, not 136.
	"teamspeak_challenge",
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

	var before int
	require.NoError(t, sqlDB.QueryRow(
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app'`,
	).Scan(&before))

	require.NoError(t, goose.DownTo(sqlDB, "migrations", 0))
	require.NoError(t, goose.Up(sqlDB, "migrations"))

	// The exact total (51 platform + 78 domain + 5 default partitions) is
	// asserted by TestAllDomainTablesPresent, which runs on this file's own
	// freshly-migrated database; here the point is idempotency — up, down,
	// up again must land on exactly the same table count it started with,
	// not some fixed constant that only held before Phase 1b existed.
	var after int
	require.NoError(t, sqlDB.QueryRow(
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app'`,
	).Scan(&after))
	require.Equal(t, before, after, "table count after up-down-up must match the count after the first up")
}

// TestAllPlatformTablesPresent — all 51 tables of 02_… §4 exist. This
// checks presence only (a subset of the full app schema, which by Phase 1b
// also contains the 78 domain-projection tables) — the exact combined
// total is TestAllDomainTablesPresent's job (db/migrations_domain_integration_test.go).
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
