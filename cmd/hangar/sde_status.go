package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/hangar-project/hangar/internal/store"
)

// ── PHASE 20.5 (B22): SAYING SO, RATHER THAN LETTING IT BE DISCOVERED ────
//
// Every `sde.*` reader degrades to the raw id when no import has run, which
// is the correct behaviour and is why the absence survived eleven phases
// without anybody noticing: a fitting export full of `[587]` looks like a
// rendering bug in the fitting export, not like a whole subsystem that was
// never wired.
//
// So HANGAR now says it, in the two places an operator looks: once at boot
// (reportSDEState, called by serve and work), and on demand.

// sdeRecordedBuild reads the build number `hangar admin import-sde` stamped
// onto a completed import. Zero means "not recorded" — every import from
// before this phase, and every import from a local --from-dir/--from-zip
// source, which HANGAR did not fetch and cannot identify.
func sdeRecordedBuild(rowCounts []byte) int64 {
	if len(rowCounts) == 0 {
		return 0
	}
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(rowCounts, &counts); err != nil {
		return 0
	}
	raw, ok := counts["_ccp_build"]
	if !ok {
		return 0
	}
	var build int64
	if err := json.Unmarshal(raw, &build); err != nil {
		return 0
	}
	return build
}

// sdeTypeCount is the one measurement that answers "is there reference data
// here", asked of the table every reader actually joins against. Counting
// app.sde_import rows would not: an import that failed verification leaves a
// row saying so and a schema that is still empty.
func sdeTypeCount(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `SELECT count(*) FROM sde.type`).Scan(&n)
	return n, err
}

// reportSDEState logs, once at boot, whether this installation has reference
// data — and, when it does not, exactly what that changes, by name.
//
// A WARN and not an ERROR: an installation with no SDE is a real, supported
// state (APPENDIX_C_MIGRATION.md §7 records the legacy parity corpus as
// having been captured against one). It is not a failure, it is a fact the
// operator should not have to deduce.
func reportSDEState(ctx context.Context, pool *pgxpool.Pool, s *store.Store, logger *slog.Logger) {
	types, err := sdeTypeCount(ctx, pool)
	if err != nil {
		logger.WarnContext(ctx, "hangar: could not read the SDE state", "error", err)
		return
	}
	if types == 0 {
		logger.WarnContext(ctx, "hangar: NO Static Data Export has been imported — "+
			"item, station and system NAMES are unavailable, so EFT fitting exports render [<type_id>] "+
			"placeholders and the skyhook / sovereignty-hub type and system backfills leave their columns NULL. "+
			"Nothing is broken and nothing renders blank; run 'hangar admin import-sde' to resolve ids to names",
			"sde_types", 0)
		return
	}

	latest, err := s.GetLatestSdeImport(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Data with no record: an sde schema populated out of band.
			logger.InfoContext(ctx, "hangar: SDE reference data present, with no import record", "sde_types", types)
			return
		}
		logger.WarnContext(ctx, "hangar: could not read the last SDE import record", "error", err)
		return
	}
	logger.InfoContext(ctx, "hangar: SDE reference data present",
		"sde_types", types, "status", latest.Status, "imported_at", latest.CompletedAt,
		"ccp_build", sdeRecordedBuild(latest.RowCounts))
}

func newAdminSDEStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sde-status",
		Short: "Report whether a Static Data Export has been imported, and what that means",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			pool, err := pgxpool.New(cmd.Context(), cfg.DB.URL.Reveal())
			if err != nil {
				return fmt.Errorf("admin sde-status: connecting to database: %w", err)
			}
			defer pool.Close()

			ctx := cmd.Context()
			types, err := sdeTypeCount(ctx, pool)
			if err != nil {
				return fmt.Errorf("admin sde-status: reading sde.type: %w", err)
			}

			s := store.New(pool)
			latest, err := s.GetLatestSdeImport(ctx)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				fmt.Println("admin sde-status: no import has ever been attempted on this installation.")
			case err != nil:
				return fmt.Errorf("admin sde-status: reading the last import: %w", err)
			default:
				fmt.Printf("admin sde-status: last import %s (%s), started %s\n",
					latest.Status, latest.ImportID, latest.StartedAt.Format("2006-01-02 15:04:05Z07:00"))
				if latest.Error != nil {
					fmt.Printf("admin sde-status:   error: %s\n", *latest.Error)
				}
				if build := sdeRecordedBuild(latest.RowCounts); build > 0 {
					fmt.Printf("admin sde-status:   CCP build: %d\n", build)
				}
			}

			fmt.Printf("admin sde-status: sde.type holds %d rows.\n", types)
			if types == 0 {
				fmt.Println("admin sde-status: WITHOUT reference data, every reader degrades to the raw id and nothing renders blank:")
				fmt.Println("admin sde-status:   - EFT fitting exports render [<type_id>] instead of an item name")
				fmt.Println("admin sde-status:   - app.corporation_skyhook.type_id / .system_id stay NULL")
				fmt.Println("admin sde-status:   - app.corporation_sovereignty_hub.type_id stays NULL")
				fmt.Println("admin sde-status: run 'hangar admin import-sde' to resolve them.")
			}
			return nil
		},
	}
}
