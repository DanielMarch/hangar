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
	fmt.Println("migrate up: app/sde schema current")

	// Phase 10 fix: db/seed/*.sql existed since Phase 1a with nothing
	// applying it — harmless until app.role_grant's FK to app.permission
	// made an empty app.permission table load-bearing. See db/seed.go's
	// doc comment.
	if err := hangardb.ApplySeeds(ctx, pool); err != nil {
		return fmt.Errorf("migrate: applying seed data: %w", err)
	}
	fmt.Println("migrate up: seed data applied")
	return nil
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
