//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// expectedDomainTables is 02_DATABASE_SCHEMA.md §5.2's table map, verbatim
// by name — the schema-diff source of truth for TestAllDomainTablesPresent.
var expectedDomainTables = []string{
	// Corporation structure (16)
	"corporation_member", "corporation_member_tracking", "corporation_title",
	"corporation_member_title", "corporation_role", "corporation_role_history",
	"corporation_division", "corporation_shareholder", "corporation_facility",
	"corporation_customs_office", "corporation_container_log", "corporation_structure",
	"corporation_starbase", "starbase_detail", "corporation_skyhook", "corporation_sovereignty_hub",
	// Corporation projects (3, UUID-keyed)
	"corporation_project", "corporation_project_contributor", "corporation_project_contribution",
	// History (2)
	"character_corporation_history", "corporation_alliance_history",
	// Assets (2)
	"asset", "asset_location",
	// Wallets (3)
	"wallet_balance", "wallet_journal", "wallet_transaction",
	// Contracts (3)
	"contract", "contract_item", "contract_bid",
	// Industry & mining (6)
	"industry_job", "blueprint", "mining_ledger", "mining_extraction", "mining_observer", "mining_observer_record",
	// Market (4)
	"market_order", "market_order_history", "market_history", "market_price",
	// Killmails (3)
	"killmail", "killmail_attacker", "killmail_item",
	// Social (5)
	"contact", "contact_label", "standing", "medal", "medal_issued",
	// Character sheet (11)
	"character_skill", "character_skillqueue", "character_attributes", "character_clone",
	"character_implant", "character_jump_fatigue", "character_loyalty_point",
	"character_agent_research", "character_title", "character_role", "character_location",
	// Fittings (2)
	"character_fitting", "character_fitting_item",
	// Mail (5)
	"mail_header", "mail_body", "mail_recipient", "mail_label", "mail_list",
	// Notifications (2)
	"character_notification", "notification_contact",
	// Calendar (3)
	"calendar_event", "calendar_event_detail", "calendar_event_attendee",
	// Planetary interaction (2)
	"planet_colony", "planet_colony_detail",
	// Sovereignty (2)
	"sovereignty_campaign", "sovereignty_system",
	// Tools (3)
	"character_note", "insurance_price", "moon_report",
	// Intel (1)
	"character_intel_edge",
}

// The five DEFAULT partitions created by their owning migrations are real
// app-schema tables too (each is a normal, if empty, child table) and must
// be added to the platform+domain counts when asserting the schema's total
// table count.
var defaultPartitionTables = []string{
	"wallet_journal_default", "wallet_transaction_default", "market_history_default",
	"killmail_default", "character_notification_default",
}

// TestAllDomainTablesPresent — schema-diff against 02_… §5.2 (Phase 1b exit
// criterion). Also proves the combined platform+domain table count matches
// SRS v3.1 §5.2's ≈129 (51 + 78, plus the five materialised DEFAULT
// partitions that are ordinary tables in information_schema) — PLUS Phase
// 8's one legitimate platform-table addition, sync_acting_character_history
// (see expectedPlatformTables' PHASE 8 ADDITION comment in
// migrations_integration_test.go), for 135 total.
func TestAllDomainTablesPresent(t *testing.T) {
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

	for _, want := range expectedDomainTables {
		if !got[want] {
			t.Errorf("expected domain table app.%s not found", want)
		}
	}

	wantTotal := len(expectedPlatformTables) + len(expectedDomainTables) + len(defaultPartitionTables)
	require.Len(t, got, wantTotal,
		"table count must match 52 platform (51 + Phase 8's sync_acting_character_history) + 78 domain + 5 default partitions = %d: %v", wantTotal, got)
}

// TestAssetTreeRecursiveCTE — 5-level nesting resolves; an injected cycle
// terminates at the depth bound (Phase 1b exit criterion,
// 02_DATABASE_SCHEMA.md §5.3).
func TestAssetTreeRecursiveCTE(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()
	s := store.New(pool)

	owner := domain.Owner{Kind: domain.OwnerCharacter, ID: 1001}
	rootLocationID := int64(60000000) // a station: the tree's root, not itself an asset

	insertAsset := func(itemID, locationID int64) {
		_, err := s.UpsertAsset(ctx, gen.UpsertAssetParams{
			OwnerKind:    string(owner.Kind),
			OwnerID:      owner.ID,
			ItemID:       itemID,
			TypeID:       587,
			LocationID:   locationID,
			LocationType: "station",
			LocationFlag: "Hangar",
			Quantity:     1,
			IsSingleton:  true,
		})
		require.NoError(t, err)
	}
	// Five nested containers: root -> item1 -> item2 -> item3 -> item4 -> item5.
	insertAsset(1, rootLocationID)
	insertAsset(2, 1)
	insertAsset(3, 2)
	insertAsset(4, 3)
	insertAsset(5, 4)

	tree, err := s.AssetTree(ctx, owner, rootLocationID, 32)
	require.NoError(t, err)
	require.Len(t, tree, 5, "all five nested items must resolve")
	require.Equal(t, 5, tree[len(tree)-1].Depth, "the deepest item must be at depth 5")

	// Inject a cycle: item 2 is re-parented under item 5, which is itself
	// (transitively) inside item 2 — a torn sync scenario. The cycle guard
	// must still terminate rather than loop or error.
	_, err = pool.Exec(ctx, `UPDATE app.asset SET location_id = 5 WHERE owner_kind = $1 AND owner_id = $2 AND item_id = 2`,
		string(owner.Kind), owner.ID)
	require.NoError(t, err)

	cyclic, err := s.AssetTree(ctx, owner, rootLocationID, 32)
	require.NoError(t, err, "a cyclic asset graph must terminate the query, not hang or error")
	require.LessOrEqual(t, len(cyclic), 32, "cycle guard must bound the result even with a loop present")
}

// TestPartitionMaintenanceCreatesThreeMonths — a fast-forwarded clock
// creates partitions ahead, never behind (Phase 1b exit criterion,
// 02_DATABASE_SCHEMA.md §3.4).
func TestPartitionMaintenanceCreatesThreeMonths(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()
	s := store.New(pool)

	partitionExists := func(table, partition string) bool {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_inherits
			  JOIN pg_class parent ON pg_inherits.inhparent = parent.oid
			  JOIN pg_class child  ON pg_inherits.inhrelid  = child.oid
			 WHERE parent.relname = $1 AND child.relname = $2`, table, partition).Scan(&n))
		return n == 1
	}

	from := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.EnsureMonthlyPartitionsAhead(ctx, from, 3))

	for _, pt := range store.PartitionedTables {
		for i := 0; i < 3; i++ {
			month := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
			name := store.PartitionName(pt.Table, month)
			require.Truef(t, partitionExists(pt.Table, name),
				"expected partition %s to exist after ensuring 3 months ahead from August", name)
		}
		// The fourth month ahead must NOT have been created yet — "three,
		// not more" is as load-bearing as "three, not fewer": creating
		// unboundedly far ahead would mask a stuck maintenance job.
		novemberName := store.PartitionName(pt.Table, time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC))
		require.Falsef(t, partitionExists(pt.Table, novemberName),
			"must not create partitions beyond the requested window (%s)", novemberName)
	}

	// Fast-forward the clock a month and re-run: it must extend forward,
	// never re-create or move anything backward.
	require.NoError(t, s.EnsureMonthlyPartitionsAhead(ctx, from.AddDate(0, 1, 0), 3))
	for _, pt := range store.PartitionedTables {
		octoberName := store.PartitionName(pt.Table, time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC))
		require.Truef(t, partitionExists(pt.Table, octoberName),
			"fast-forwarding a month must create the new third month ahead (%s)", octoberName)
	}
}

// TestUpsertGuardUsesIsDistinctFrom — re-applying identical data changes no
// updated_at (Phase 1b exit criterion, 02_DATABASE_SCHEMA.md §3.5).
func TestUpsertGuardUsesIsDistinctFrom(t *testing.T) {
	pool, _ := newMigratedContainer(t)
	ctx := context.Background()
	s := store.New(pool)

	params := gen.UpsertCharacterSkillParams{
		CharacterID:  90000001,
		SkillID:      3300,
		ActiveLevel:  5,
		TrainedLevel: 5,
		Skillpoints:  256000,
	}

	// character_skill FKs to app.character; seed the parent row.
	_, err := pool.Exec(ctx, `INSERT INTO app.character (character_id, name, owner_hash) VALUES ($1, 'Test Pilot', 'hash')`, params.CharacterID)
	require.NoError(t, err)

	first, err := s.UpsertCharacterSkill(ctx, params)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond) // ensure now() would differ if the guard failed to hold

	// Re-applying identical data hits the ON CONFLICT ... WHERE ... IS
	// DISTINCT FROM guard, which suppresses the UPDATE entirely — and
	// because no row was touched, `RETURNING *` legitimately returns zero
	// rows, which pgx reports as pgx.ErrNoRows on a :one query. That is the
	// guard working, not a failure: assert the specific error rather than
	// require.NoError, then confirm updated_at truly never moved by
	// re-reading the row directly.
	_, err = s.UpsertCharacterSkill(ctx, params)
	require.ErrorIs(t, err, pgx.ErrNoRows,
		"re-applying identical data must be a no-op RETURNING zero rows — the ON CONFLICT ... WHERE ... IS DISTINCT FROM guard suppressed the update")

	unchanged, err := s.Queries.ListCharacterSkills(ctx, params.CharacterID)
	require.NoError(t, err)
	require.Len(t, unchanged, 1)
	require.True(t, first.UpdatedAt.Equal(unchanged[0].UpdatedAt),
		"updated_at must be unchanged after the no-op re-apply")

	// Now change one field and confirm updated_at DOES move — the guard
	// must not be so strict it never fires.
	params.Skillpoints = 512000
	third, err := s.UpsertCharacterSkill(ctx, params)
	require.NoError(t, err)
	require.True(t, third.UpdatedAt.After(first.UpdatedAt), "a real data change must update updated_at")
}
