package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// ingestTimeout bounds the whole boot sequence — two unauthenticated HTTP
// calls to ESI plus the upsert of ~400 routes. Generous, because the
// alternative to waiting is an empty catalogue; bounded, because `serve`
// runs this in the background at startup and a hung ingest must not leak a
// goroutine for the life of the process.
const ingestTimeout = 3 * time.Minute

// newAdminIngestCatalogueCmd wires the ESI route catalogue ingest to an
// explicit operator action.
//
// PHASE 18 CLOSE-OUT DEFECT. internal/esi/catalogue.Boot — the entire
// Phase 2 boot sequence (discover D_max, fetch the spec AT D_max, ingest
// every operation, mark routes newer than the app pin blocked_by_pin) —
// had NO caller anywhere outside an integration test. No serve path, no
// command, no job. So a deployed installation never populated
// app.esi_route, and because app.sync_subscription carries a route_id
// foreign key into it (db/queries/sync_subscription.sql joins the two),
// nothing in the ESI sync layer could run at all: no subscription could
// even be created. Phase 18's Route Catalogue viewer and blocked-by-pin
// board were correspondingly empty on any real deployment, which is how it
// surfaced.
//
// The pin is NEVER advanced here. Boot reads it (seeding the default on
// first boot) and marks routes against it; advancing it is exclusively
// catalogue.AdvancePin's job, reachable only through an explicit
// administrator action on the API (01_ARCHITECTURE.md §5.1).
func newAdminIngestCatalogueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ingest-catalogue",
		Short: "Fetch ESI's OpenAPI spec and ingest it into app.esi_route",
		Long: "Runs the Phase 2 boot sequence: discover D_max from /meta/compatibility-dates, " +
			"fetch /meta/openapi.json AT D_max (never at the app pin), ingest every operation, " +
			"and mark routes newer than the app pin as blocked_by_pin. Idempotent — running it " +
			"twice changes nothing beyond updated_at. Falls back to the embedded snapshot when " +
			"ESI is unreachable, and says so. The compatibility pin is never advanced.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			logger := newLogger(cfg)

			pool, err := pgxpool.New(cmd.Context(), cfg.DB.URL.Reveal())
			if err != nil {
				return fmt.Errorf("admin ingest-catalogue: connecting to database: %w", err)
			}
			defer pool.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), ingestTimeout)
			defer cancel()

			result, err := ingestCatalogue(ctx, pool, cfg.ESI.BaseURL, logger)
			if err != nil {
				return fmt.Errorf("admin ingest-catalogue: %w", err)
			}
			fmt.Printf("admin ingest-catalogue: %d routes ingested (%d blocked by the pin, %d retired) from %s\n",
				result.Ingested, result.Blocked, result.Retired, result.Source)
			fmt.Printf("admin ingest-catalogue: D_max %s, app pin %s (unchanged — advancing it is a separate, explicit action)\n",
				catalogue.FormatDate(result.DMax), catalogue.FormatDate(result.Pin))
			if result.StaleSnapshot {
				fmt.Println("admin ingest-catalogue: WARNING — ESI was unreachable; this ingest used the EMBEDDED SNAPSHOT and is not current.")
			}
			return nil
		},
	}
}

// ingestCatalogue runs one boot pass. Shared by the command above and by
// serve's startup ingest so there is exactly one definition of what
// "ingest the catalogue" means.
func ingestCatalogue(ctx context.Context, pool *pgxpool.Pool, baseURL string, logger *slog.Logger) (catalogue.BootResult, error) {
	s := store.New(pool)
	client := &http.Client{Timeout: 60 * time.Second}

	result, err := catalogue.Boot(ctx, client, s, baseURL, time.Now())
	if err != nil {
		return catalogue.BootResult{}, err
	}
	logger.InfoContext(ctx, "hangar: esi catalogue ingested",
		"routes", result.Ingested,
		"blocked_by_pin", result.Blocked,
		"retired", result.Retired,
		"source", result.Source,
		"stale_snapshot", result.StaleSnapshot,
		"d_max", catalogue.FormatDate(result.DMax),
		"pin", catalogue.FormatDate(result.Pin),
		"d_max_recorded", result.DMaxRecorded,
	)

	// ── PHASE 20.4.1: THE SEEDS THAT COULD NOT RUN YET NOW RUN ───────────
	// db/seed/alert_types.sql inserts its four THRESHOLD rows through a JOIN
	// against app.esi_route, because app.alert_type.source_route_id is NOT
	// NULL and the threshold_declares_source CHECK enforces it. `migrate up`
	// applies seeds BEFORE anything has ingested the spec, so on a FRESH
	// installation that JOIN matches nothing and the four rows are silently
	// skipped. Verified on the 20.4 release image against a throwaway
	// Postgres: the first `migrate up` produced 50 alert types and 0
	// thresholds; a second, after the ingest had landed 225 routes, produced
	// 54 and 4.
	//
	// The consequence is not a crash, which is exactly what makes it worth
	// closing here rather than documenting. app.alert_routing_rule has a
	// foreign key to app.alert_type, so an operator on a fresh installation
	// cannot create a routing rule for a threshold alert AT ALL — and the
	// evaluator then reports it as unrouted, skips it, and looks completely
	// healthy while being structurally incapable of ever firing. SRS §4.4's
	// own sentence about a threshold alert that "silently generates zero
	// alerts" describes this exactly.
	//
	// Re-applying the whole seed set is deliberate, rather than singling out
	// the one file: every seed file is idempotent by construction (see
	// db.ApplySeeds), `migrate up` already re-applies them all on every run,
	// and doing it by name here would leave the NEXT catalogue-dependent
	// seed to rediscover this. A failure is logged and not returned — the
	// catalogue ingest itself succeeded, and refusing to report that because
	// a follow-up upsert failed would lose the more important fact.
	if err := hangardb.ApplySeeds(ctx, pool); err != nil {
		logger.ErrorContext(ctx, "hangar: re-applying seed data after catalogue ingest failed — "+
			"threshold alert types may still be missing; run 'hangar migrate up' once more",
			"error", err)
		return result, nil
	}
	logDeferredAlertTypes(ctx, pool, logger)
	return result, nil
}

// logDeferredAlertTypes tells the operator, in the log, whether the four
// threshold alert types are present — because "four of your alert types do
// not exist" is not something anybody discovers by reading a table they
// have never heard of.
//
// It runs after every ingest, not only the first, so the answer is on the
// most recent boot's output rather than buried in whichever boot happened
// to be the one that fixed it.
func logDeferredAlertTypes(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	var thresholds, total int64
	err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE category = 'threshold'), count(*) FROM app.alert_type`).
		Scan(&thresholds, &total)
	if err != nil {
		logger.WarnContext(ctx, "hangar: could not count alert types after seeding", "error", err)
		return
	}
	if thresholds == 0 {
		logger.WarnContext(ctx, "hangar: NO threshold alert types are seeded — "+
			"structure and starbase fuel, member inactivity and contract expiry alerts cannot be routed or fired on this installation. "+
			"They are seeded through a join against the ESI route catalogue; run 'hangar admin ingest-catalogue' and they will complete",
			"alert_types", total)
		return
	}
	logger.InfoContext(ctx, "hangar: alert type catalogue complete",
		"alert_types", total, "threshold_types", thresholds)
}
