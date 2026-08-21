package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	hangardb "github.com/hangar-project/hangar/db"
)

// reportSchemaIntegrity states, once at boot, whether the objects the
// migrations create are actually present.
//
// ── WHY A RUNNING PROCESS CHECKS THIS AT ALL ─────────────────────────────
// `hangar migrate up` verifies the same thing and FAILS on a mismatch, which
// is the right severity for the command whose entire contract is "leave the
// schema correct". But an operator does not necessarily run it — a drifted
// database is discovered by whatever starts next, and until Phase 20.11 that
// discovery took the form of a metric counter climbing and a rate limiter
// quietly making the wrong choice.
//
// The concrete case that motivated it: app.esi_replica went missing from the
// development installation (a housekeeping step applied as DROP TABLE rather
// than DELETE), goose considered migration 00006 applied so `migrate up`
// would never restore it, and the consequences were all indirect —
// GatewayCollector counted a scrape failure once a minute, ReplicaHeartbeat
// skipped silently at DEBUG, and Governor 1 held the solo mode it starts in,
// so every replica believed it alone held the full ESI error budget. Three
// components, three different reactions, no statement of the actual fact.
//
// ── WHY THIS LOGS AND DOES NOT EXIT ──────────────────────────────────────
// Deliberately ERROR-and-continue rather than fatal, and the asymmetry with
// `migrate up` is the point rather than an oversight:
//
//   - The harm is real but CONDITIONAL. A single-replica installation in solo
//     mode is behaving correctly; the budget hazard needs two or more. This
//     check cannot tell which it is looking at, and refusing to boot over a
//     condition that is harmless in the common deployment would trade a
//     quiet degradation for a loud outage.
//   - Serving is not schema management. A process that can still answer
//     requests should say loudly what is wrong and keep answering them; the
//     command that exists to fix schemas is the one that should refuse.
//
// ERROR rather than WARN because, unlike the SDE report next door, this is
// not a supported state. There is no schema without these tables.
//
// PHASE 23 (N-6): this now covers COLUMNS and INDEXES as well as tables.
// The severity argument above is unchanged and applies to all three; what
// changes is that a dropped index — the drift whose only symptom is a query
// getting slower, which nobody attributes to the schema — is now stated
// rather than inferred.
func reportSchemaIntegrity(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	drift, err := hangardb.MissingObjects(ctx, pool)
	if err != nil {
		logger.WarnContext(ctx, "hangar: could not verify the schema against the migrations", "error", err)
		return
	}
	if drift.Empty() {
		return
	}

	names := func(n int, render func(int) string) []string {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, render(i))
		}
		return out
	}
	logger.ErrorContext(ctx, "hangar: SCHEMA DRIFT — "+hangardb.FormatDrift(drift),
		"missing_tables", names(len(drift.Tables), func(i int) string { return drift.Tables[i].String() }),
		"missing_columns", names(len(drift.Columns), func(i int) string { return drift.Columns[i].String() }),
		"missing_indexes", names(len(drift.Indexes), func(i int) string { return drift.Indexes[i].String() }),
		"missing_count", drift.Count())
}
