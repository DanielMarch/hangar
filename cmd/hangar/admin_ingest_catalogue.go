package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

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

			result, err := ingestCatalogue(ctx, pool, logger)
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
func ingestCatalogue(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (catalogue.BootResult, error) {
	s := store.New(pool)
	client := &http.Client{Timeout: 60 * time.Second}

	result, err := catalogue.Boot(ctx, client, s, time.Now())
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
	return result, nil
}
