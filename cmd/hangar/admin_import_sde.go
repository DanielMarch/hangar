package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/hangar-project/hangar/internal/sde"
	"github.com/hangar-project/hangar/internal/store"
)

// ── DEFECT B22, CLOSED IN PHASE 20.5 ─────────────────────────────────────
//
// internal/sde was absent from the binary entirely. Migration 00036 created
// twenty-five `sde.*` tables in Phase 9 and nothing outside the package's
// own tests ever imported it, so on every installation ever deployed those
// tables were empty and could only ever be empty.
//
// ── WHAT ACTUALLY DEPENDS ON IT, ESTABLISHED FIRST ───────────────────────
// This determined whether the job was "wire the import" or "the domain reads
// through it and currently renders raw ids". Every reader was traced:
//
//	sde.type    ← ListSdeTypeNames, the EFT fitting export
//	              (GET /api/v1/characters/{id}/fittings/{fitting_id}/eft).
//	              A type absent from the SDE keeps its `[<type_id>]`
//	              placeholder for that one line; the export still works.
//	sde.type    ← BackfillSkyhookTypeIDFromSDE and
//	              BackfillSovereigntyHubTypeIDFromSDE, which resolve the
//	              structure type BY NAME ('Skyhook', 'Sovereignty Hub')
//	              rather than by a guessed numeric constant (Principle 13).
//	              Both are guarded by EXISTS, so with no import they update
//	              zero rows and the column stays NULL.
//	sde.planet  ← BackfillSkyhookSystemIDFromSDE: planet_id → solar_system_id.
//	              Same EXISTS guard, same outcome.
//
// SO THE ANSWER IS "WIRE THE IMPORT JOB". Every reader ALREADY degrades to
// the id or to NULL — deliberately, with the reasoning written down at each
// site — and not one of them renders a blank or a fabricated name. What was
// missing was any way to make them return something better.
//
// The skyhook parity counters are downstream of the same fact and are
// unaffected either way: catalogue.SkyhookNames counts ALERT TYPES, not SDE
// rows, and Gate 4.4's per-domain counts never touched `sde.*`.
//
// ── HOW IT IS TRIGGERED: AN OPERATOR COMMAND ─────────────────────────────
// Not a startup step, and not a scheduled job.
//
//   - NOT A STARTUP STEP. The export is a multi-gigabyte download from CCP's
//     CDN. Blocking boot on it makes every restart depend on a third party
//     being up; doing it in the background at every boot re-downloads
//     gigabytes to rebuild data that changes a few times a year. And the
//     swap takes an ACCESS EXCLUSIVE lock on a schema the API reads.
//
//   - NOT A SCHEDULED JOB. The SDE changes when CCP ships one, which is
//     roughly per patch. A timer would either download the whole export on a
//     cadence unrelated to whether anything changed, or need a
//     build-number check that is itself an operator decision about when to
//     take the swap. `--if-changed` gives that check to the operator
//     directly (it compares CCP's manifest build against the last verified
//     import and exits 0 having done nothing when they agree), so an
//     operator who WANTS a schedule has a one-line cron and HANGAR has not
//     invented a policy about when to take a lock on their database.
//
//   - AN OPERATOR COMMAND, then, exactly like `hangar admin
//     ingest-catalogue` — the other "fetch a large thing from CCP and
//     replace a reference dataset" action, which this deliberately mirrors
//     rather than inventing a second shape for.
//
// What a never-imported installation renders is stated at the top of this
// comment and is enforced by the readers themselves: THE ID, never a blank
// and never a lie. `hangar admin sde-status` reports whether an import has
// ever run, and serve/work say so once at boot, so an operator learns it
// from HANGAR rather than from a user asking why a fitting export is full of
// numbers.

// sdeImportTimeout bounds the whole pipeline: a multi-gigabyte download, a
// streamed COPY of ~25 tables, the smoke queries, and the swap. Generous,
// because the alternative to waiting is no reference data at all.
const sdeImportTimeout = 2 * time.Hour

// sdeSwapGracePeriod is how long the renamed-away old schema survives before
// it is dropped, so a query that started before the swap can finish against
// the rows it was reading. Short: the rename itself is instantaneous and
// this only has to outlive an in-flight statement.
const sdeSwapGracePeriod = 30 * time.Second

const (
	defaultSDEManifestURL = "https://developers.eveonline.com/static-data/tranquility/latest.jsonl"
	defaultSDEBuildURL    = "https://developers.eveonline.com/static-data/tranquility/eve-online-static-data-%d-jsonl.zip"
)

func newAdminImportSDECmd() *cobra.Command {
	var (
		fromDir     string
		fromZip     string
		manifestURL string
		buildURL    string
		ifChanged   bool
	)

	cmd := &cobra.Command{
		Use:   "import-sde",
		Short: "Import CCP's Static Data Export into the sde.* schema",
		Long: "Streams CCP's JSONL Static Data Export into a fresh `sde_next` schema, verifies it with " +
			"per-table smoke queries, and swaps it into place atomically (02_DATABASE_SCHEMA.md §6). " +
			"The live `sde` schema is never touched until verification passes; a failure drops `sde_next` " +
			"and leaves the previous import serving.\n\n" +
			"Until an import has run, every reader degrades to the raw id — a fitting export renders " +
			"`[<type_id>]`, and the skyhook/sovereignty-hub type and system backfills leave their columns " +
			"NULL. That is a supported state, not a broken one.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			logger := newLogger(cfg)

			pool, err := pgxpool.New(cmd.Context(), cfg.DB.URL.Reveal())
			if err != nil {
				return fmt.Errorf("admin import-sde: connecting to database: %w", err)
			}
			defer pool.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), sdeImportTimeout)
			defer cancel()

			s := store.New(pool)
			client := &http.Client{Timeout: 30 * time.Minute}

			source, cleanup, describe, build, err := resolveSDESource(ctx, client, fromDir, fromZip, manifestURL, buildURL, ifChanged, s, logger)
			if err != nil {
				return fmt.Errorf("admin import-sde: %w", err)
			}
			defer cleanup()
			if source == nil {
				// --if-changed and nothing changed. Exit 0 having done
				// nothing, so a cron wrapper does not need to parse output to
				// know whether to alert.
				fmt.Println("admin import-sde: the live SDE already matches CCP's latest build — nothing to do")
				return nil
			}

			fmt.Printf("admin import-sde: importing from %s\n", describe)
			started := time.Now()
			if err := sde.Import(ctx, pool, s, source, sde.DefaultSmokeQueries(), sdeSwapGracePeriod); err != nil {
				return fmt.Errorf("admin import-sde: %w (the live sde schema is unchanged)", err)
			}

			latest, err := s.GetLatestSdeImport(ctx)
			if err != nil {
				return fmt.Errorf("admin import-sde: import succeeded but reading its record failed: %w", err)
			}
			// Stamp CCP's build onto the record --if-changed will read next
			// time. Read back rather than threaded through sde.Import,
			// because Import's signature is Phase 9's and its integration
			// test asserts against it; the read is of the row this command
			// just caused, one statement later, from a command an operator
			// runs by hand.
			if build > 0 {
				if err := s.RecordSdeImportBuild(ctx, build, latest.ImportID); err != nil {
					logger.WarnContext(ctx, "hangar: could not record the SDE build number; --if-changed will re-import once",
						"import_id", latest.ImportID, "error", err)
				}
			}
			fmt.Printf("admin import-sde: %s in %s (checksum %s)\n", latest.Status, time.Since(started).Truncate(time.Second), derefString(latest.Checksum))
			logger.InfoContext(ctx, "hangar: sde import complete",
				"status", latest.Status, "checksum", derefString(latest.Checksum), "source", describe)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromDir, "from-dir", "", "import from a directory of already-extracted <table>.jsonl files")
	cmd.Flags().StringVar(&fromZip, "from-zip", "", "import from an already-downloaded SDE zip")
	cmd.Flags().StringVar(&manifestURL, "manifest-url", defaultSDEManifestURL, "CCP's latest.jsonl manifest")
	cmd.Flags().StringVar(&buildURL, "build-url", defaultSDEBuildURL, "printf template for the build zip, taking the build number")
	cmd.Flags().BoolVar(&ifChanged, "if-changed", false,
		"exit 0 without importing when CCP's latest build number already matches the last verified import — the flag a cron wrapper wants")
	return cmd
}

// resolveSDESource decides where the data comes from and returns a provider,
// a cleanup, and a human description. A nil provider with a nil error means
// "--if-changed, and nothing changed".
func resolveSDESource(
	ctx context.Context, client *http.Client,
	fromDir, fromZip, manifestURL, buildURL string, ifChanged bool,
	s *store.Store, logger *slog.Logger,
) (sde.SourceProvider, func(), string, int64, error) {
	noop := func() {}
	switch {
	case fromDir != "" && fromZip != "":
		return nil, noop, "", 0, errors.New("--from-dir and --from-zip are mutually exclusive")

	case fromDir != "":
		if _, err := os.Stat(fromDir); err != nil {
			return nil, noop, "", 0, fmt.Errorf("--from-dir %s: %w", fromDir, err)
		}
		// A local source carries no build number: HANGAR did not fetch it and
		// has no way to know which build it is. Recorded as 0, which makes
		// --if-changed re-import next time rather than trust a number it
		// never saw.
		return sde.DirSource{Dir: fromDir}, noop, "directory " + fromDir, 0, nil

	case fromZip != "":
		src, err := sde.OpenZip(fromZip)
		if err != nil {
			return nil, noop, "", 0, err
		}
		return src, noop, "zip " + fromZip, 0, nil
	}

	manifest, err := sde.FetchManifest(ctx, client, manifestURL)
	if err != nil {
		return nil, noop, "", 0, err
	}
	build, err := manifest.LatestBuild()
	if err != nil {
		return nil, noop, "", 0, err
	}

	if ifChanged {
		same, err := liveBuildMatches(ctx, s, build)
		if err != nil {
			return nil, noop, "", 0, err
		}
		if same {
			return nil, noop, "", build, nil
		}
	}

	url := fmt.Sprintf(buildURL, build)
	logger.InfoContext(ctx, "hangar: downloading the SDE", "build", build, "url", url)
	zipSrc, path, err := sde.DownloadZip(ctx, client, url)
	if err != nil {
		return nil, noop, "", 0, err
	}
	return zipSrc, func() { _ = os.Remove(path) }, fmt.Sprintf("CCP build %d", build), build, nil
}

// liveBuildMatches reports whether the last VERIFIED-or-SWAPPED import
// already carries CCP's current build number.
//
// The build is recorded in app.sde_import.row_counts under a reserved key
// rather than in a column of its own — the table is Phase 9's and adding a
// column for a comparison the operator drives would be a migration for a
// convenience. A record with no build recorded (every import before this
// phase) compares as "changed", which is the safe direction: it re-imports
// once and records the build.
func liveBuildMatches(ctx context.Context, s *store.Store, build int64) (bool, error) {
	latest, err := s.GetLatestSdeImport(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // never imported
		}
		return false, fmt.Errorf("reading the last sde import: %w", err)
	}
	if latest.Status != "swapped" {
		return false, nil
	}
	return sdeRecordedBuild(latest.RowCounts) == build, nil
}

func derefString(p *string) string {
	if p == nil {
		return "(none)"
	}
	return *p
}
