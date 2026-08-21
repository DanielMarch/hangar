package main

import (
	"context"
	"errors"
	"fmt"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations: River's queue schema, then Goose's app/sde schemas",
	}
	cmd.AddCommand(newMigrateUpCmd(), newMigrateDownCmd())
	return cmd
}

func newMigrateUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrateUp(cmd.Context())
		},
	}
}

func newMigrateDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Roll back one Goose migration (River migrations are never rolled back automatically)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrateDown(cmd.Context())
		},
	}
}

func connectMigratePool(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, cfg.DB.URL.Reveal())
	if err != nil {
		return nil, fmt.Errorf("migrate: connecting to database: %w", err)
	}
	return pool, nil
}

// runMigrateUp applies River's own queue-table migrations first (they own
// the `river` schema and River requires them before a client can start),
// then Goose's plain-SQL migrations for `app` and `sde`. db/migrations is
// empty until Phase 1a/1b/9 populate it — goose.Up on zero migration files
// is a documented no-op, matching the progressive-CI philosophy elsewhere in
// this repository.
func runMigrateUp(ctx context.Context) error {
	pool, err := connectMigratePool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("migrate: building river migrator: %w", err)
	}
	if _, err := riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("migrate: river migrate up: %w", err)
	}
	fmt.Println("migrate up: river schema current")

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(hangardb.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: setting goose dialect: %w", err)
	}
	// db/migrations is empty until Phase 1a/1b/9 populate it. Unlike River's
	// migrator, goose.Up treats zero matching files as an error rather than a
	// no-op — tolerate exactly that one sentinel so `hangar migrate up` stays
	// a normal, successful boot step on a Phase-0-only schema.
	if err := goose.Up(sqlDB, "migrations"); err != nil && !errors.Is(err, goose.ErrNoMigrationFiles) {
		return fmt.Errorf("migrate: goose up: %w", err)
	}

	// PHASE 20.11. "schema current" used to be printed on the strength of
	// goose's own bookkeeping, which records which FILES ran and knows
	// nothing about whether their tables still exist. The two came apart on
	// the development installation — one table dropped out of band, and
	// every subsequent `migrate up` reported the schema current — so the
	// claim is now verified before it is made.
	//
	// This FAILS rather than warns. Leaving the schema correct is precisely
	// this command's contract; a `migrate up` that exits 0 over a missing
	// object is the defect, not a nicety.
	//
	// PHASE 23 (N-6): tables, COLUMNS and INDEXES. Until this phase it
	// verified tables only, so a dropped index passed — and a dropped index
	// is the drift an operator is least likely to attribute to the schema,
	// because it costs performance rather than correctness and nothing says
	// why. See db/schemadrift.go.
	drift, err := hangardb.MissingObjects(ctx, pool)
	if err != nil {
		return fmt.Errorf("migrate: verifying schema: %w", err)
	}
	if !drift.Empty() {
		return fmt.Errorf("migrate: schema is NOT current — %s", hangardb.FormatDrift(drift))
	}
	fmt.Println("migrate up: app/sde schema current — tables, columns and indexes verified against the migrations, not just goose's log")

	// Phase 10 fix: db/seed/*.sql existed since Phase 1a with nothing
	// applying it — harmless until app.role_grant's FK to app.permission
	// made an empty app.permission table load-bearing. See db/seed.go's
	// doc comment.
	if err := hangardb.ApplySeeds(ctx, pool); err != nil {
		return fmt.Errorf("migrate: applying seed data: %w", err)
	}
	fmt.Println("migrate up: seed data applied")

	// PHASE 20.4.1. On a FRESH installation four of the 54 alert types
	// cannot be seeded here: they are THRESHOLD types, whose NOT NULL
	// source_route_id is resolved by a join against app.esi_route, and
	// nothing has ingested the spec yet. They complete themselves on the
	// first catalogue ingest (see cmd/hangar's ingestCatalogue), so this is
	// not an operator step — but "four alert types do not exist yet" is not
	// something anybody discovers on their own, and until they do exist no
	// routing rule can even be created for them, because
	// app.alert_routing_rule has a foreign key to app.alert_type.
	//
	// Printed rather than logged: `migrate up` is a foreground command whose
	// output an operator is reading right now.
	reportDeferredAlertTypes(ctx, pool)
	return nil
}

func reportDeferredAlertTypes(ctx context.Context, pool *pgxpool.Pool) {
	var thresholds, total int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE category = 'threshold'), count(*) FROM app.alert_type`).
		Scan(&thresholds, &total); err != nil {
		fmt.Printf("migrate up: could not count alert types (%v)\n", err)
		return
	}
	if thresholds > 0 {
		fmt.Printf("migrate up: %d alert types seeded, %d of them threshold types\n", total, thresholds)
		return
	}
	fmt.Printf("migrate up: %d alert types seeded, and 4 THRESHOLD types are DEFERRED.\n", total)
	fmt.Println("migrate up:   corporation.structure.fuel_low, corporation.starbase.fuel_low,")
	fmt.Println("migrate up:   corporation.member.inactive, corporation.contract.expiring")
	fmt.Println("migrate up: each declares an ESI source route, and app.esi_route is empty until the")
	fmt.Println("migrate up: catalogue is ingested. They complete automatically on the first ingest —")
	fmt.Println("migrate up: `serve` does one at startup, or run `hangar admin ingest-catalogue`.")
	fmt.Println("migrate up: Until then no routing rule can be created for them and they cannot fire.")
}

func runMigrateDown(ctx context.Context) error {
	pool, err := connectMigratePool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(hangardb.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: setting goose dialect: %w", err)
	}
	if err := goose.Down(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("migrate: goose down: %w", err)
	}
	fmt.Println("migrate down: rolled back one app/sde migration")
	return nil
}
