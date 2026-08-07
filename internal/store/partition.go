package store

import (
	"context"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store/gen"
)

// PartitionedTable names one of the five time-series tables
// PARTITION BY RANGE (02_DATABASE_SCHEMA.md §3.4) and the column they are
// ranged on.
type PartitionedTable struct {
	Schema string
	Table  string
	Column string
}

// PartitionedTables is every table partitioned monthly by this schema.
// Phase 6 wires EnsureMonthlyPartitionsAhead into a River periodic job
// named "partition-maintenance" that runs it daily; Phase 1b ships the
// mechanism and proves it against a fast-forwarded clock.
var PartitionedTables = []PartitionedTable{
	{Schema: "app", Table: "wallet_journal", Column: "date"},
	{Schema: "app", Table: "wallet_transaction", Column: "date"},
	{Schema: "app", Table: "character_notification", Column: "sent_at"},
	{Schema: "app", Table: "killmail", Column: "killmail_time"},
	{Schema: "app", Table: "market_history", Column: "date"},
}

// PartitionName is the deterministic name EnsureMonthlyPartitionsAhead uses
// for the partition covering the calendar month containing `t` — exported
// so tests and administrator tooling can check for a specific partition's
// existence without duplicating the naming scheme.
func PartitionName(table string, t time.Time) string {
	return fmt.Sprintf("%s_y%04dm%02d", table, t.Year(), int(t.Month()))
}

// EnsureMonthlyPartitionsAhead creates, for every table in PartitionedTables,
// monthly RANGE partitions covering the `monthsAhead` calendar months
// starting with the month containing `from` (inclusive), if they do not
// already exist (CREATE TABLE IF NOT EXISTS). It never creates a partition
// dated behind `from`'s month, and it is idempotent — safe to call from a
// daily job. 02_DATABASE_SCHEMA.md §3.4 requires at least three months
// ahead so a job outage never causes an insert failure; that count is the
// caller's choice, not hard-coded here.
func (s *Store) EnsureMonthlyPartitionsAhead(ctx context.Context, from time.Time, monthsAhead int) error {
	for _, pt := range PartitionedTables {
		if err := ensurePartitionsAhead(ctx, s.db, pt, from, monthsAhead); err != nil {
			return fmt.Errorf("partition maintenance for %s.%s: %w", pt.Schema, pt.Table, err)
		}
	}
	return nil
}

func ensurePartitionsAhead(ctx context.Context, db gen.DBTX, pt PartitionedTable, from time.Time, monthsAhead int) error {
	monthStart := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < monthsAhead; i++ {
		lower := monthStart.AddDate(0, i, 0)
		upper := lower.AddDate(0, 1, 0)
		name := PartitionName(pt.Table, lower)
		// pt.Table/pt.Schema/name come only from the static PartitionedTables
		// list above, never from caller input, so building this DDL string
		// with fmt.Sprintf carries no injection risk — Postgres has no
		// parameter placeholder for identifiers or FOR VALUES bounds anyway.
		stmt := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %[1]s.%[2]s PARTITION OF %[1]s.%[3]s FOR VALUES FROM ('%[4]s') TO ('%[5]s')`,
			pt.Schema, name, pt.Table, lower.Format("2006-01-02"), upper.Format("2006-01-02"),
		)
		if _, err := db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("creating partition %s: %w", name, err)
		}
	}
	return nil
}
