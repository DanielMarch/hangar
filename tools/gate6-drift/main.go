// Command gate6-drift runs Gate 6 — Spec-Drift Resilience
// (docs/04_RELEASE_GATES.md §6) — as §6.2 specifies it, and writes the
// ingest report and the zero-code-changes proof.
//
// ── WHAT WAS ALREADY DONE, AND WHAT WAS NOT ──────────────────────────────
// The four §6.1 conditions have been asserted since Phase 2 by
// internal/esi/catalogue's own tests. Those are PARSE-level assertions:
// they show ParseSpec types a uuid, keeps a novel scope verbatim, blocks a
// post-pin route and defaults an unknown cache mode.
//
// §6.2 asks for something else, and it had never been performed:
//
//  1. tag the release candidate commit
//  2. run the CATALOGUE INGEST against the synthetic spec
//  3. assert the four outcomes
//  4. `git status --porcelain` must be EMPTY, and `git rev-parse HEAD`
//     must equal the tag
//
// Steps 2 and 4 are the gate. The ingest is the production path — the same
// `hangar admin ingest-catalogue` an operator runs — against a database,
// not a parse in a test; and step 4 is the claim that ingesting a spec
// nobody anticipated required NO SOURCE CHANGE. §0 rule 4: a gate that
// requires a code change to pass is a failed gate. If this program's
// assertions fail, the correct response is to make the ingest data-driven,
// never to edit the ingest and re-run.
//
//	go run ./tools/gate6-drift -tag=v1.0.0-rc1 -version=v1.0.0-rc1
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/test/load"
	"github.com/hangar-project/hangar/tools/gaterun"
)

// The four synthetic operations, by the operation ids the committed
// fixture declares. Named as constants so a fixture edit that renamed one
// would fail loudly here rather than silently checking nothing.
const (
	opFuturePin  = "GetSyntheticFutureRoute"
	opUUIDPath   = "GetSyntheticWidgetsWidgetId"
	opNovelScope = "GetSyntheticScopeGrammar"
	opCacheMode  = "GetSyntheticCacheMode"

	novelScope     = "esi::synthetic~widget/read@v3"
	novelCacheMode = "quantum-entangled"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gate6-drift: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		tag      = flag.String("tag", "", "the release-candidate tag the run must be at (§6.2 step 1)")
		version  = flag.String("version", "", "release version the evidence belongs to")
		outDir   = flag.String("out", "", "evidence directory (default docs/gate-evidence/<version>/gate6)")
		specPath = flag.String("spec", filepath.Join("test", "drift", "gate6_synthetic_spec.json"), "the synthetic spec")
		binary   = flag.String("binary", gaterun.DefaultBinary(), "path to the hangar binary under test")
		force    = flag.Bool("force", false, "run against a database whose name does not look like a gate database")
	)
	flag.Parse()

	if *version == "" || *tag == "" {
		return errors.New("-tag and -version are both required: §6.2's proof is that the ingest ran at a TAGGED commit with a clean tree")
	}
	dir, err := gaterun.EvidenceDir(*outDir, *version, "gate6")
	if err != nil {
		return err
	}

	cfg, err := config.Load(config.New())
	if err != nil {
		return fmt.Errorf("loading configuration (source .env first): %w", err)
	}
	dbURL := cfg.DB.URL.Reveal()
	if err := gaterun.GuardDatabase(dbURL, *force); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := time.Now()

	spec, err := os.ReadFile(*specPath)
	if err != nil {
		return fmt.Errorf("reading the synthetic spec: %w", err)
	}

	// ── §6.2 step 2: the ingest, through the production path ──────────────
	// The spec is served over HTTP and the real `hangar admin
	// ingest-catalogue` is pointed at it, rather than calling ParseSpec in
	// process. The gate's claim is about what an INSTALLATION does when CCP
	// publishes a spec like this, and the discovery call, the fetch at
	// D_max, the upsert and the pin comparison are all part of that.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case catalogue.CompatibilityDatesPath:
			w.Header().Set("Content-Type", "application/json")
			// The OBJECT shape ESI actually returns, which
			// fetchCompatibilityDates decodes into
			// {"compatibility_dates": [...]}. A bare array decodes to an
			// error, the whole fetch fails, and Boot falls back to the
			// EMBEDDED SNAPSHOT — which is a silent, complete defeat of this
			// gate: it would then assert the four synthetic outcomes against
			// the real spec, find none of them, and report four failures
			// that say nothing about spec-drift resilience. It did exactly
			// that on the first run.
			//
			// D_max is deliberately later than the synthetic route's own
			// compatibility date, so the spec is fetched at a date that
			// INCLUDES the post-pin route. A D_max that hid it would make
			// condition (a) unobservable.
			_, _ = w.Write([]byte(`{"compatibility_dates":["2026-09-03"]}`))
		case catalogue.OpenAPIPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(spec)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	env := append(os.Environ(), "HANGAR_DB_URL="+dbURL, "HANGAR_ESI_BASE_URL="+server.URL)
	if err := gaterun.RunBinary(ctx, *binary, env, "migrate", "up"); err != nil {
		return fmt.Errorf("migrating the gate database: %w", err)
	}
	if err := gaterun.RunBinary(ctx, *binary, env, "admin", "ingest-catalogue"); err != nil {
		return fmt.Errorf("the synthetic spec was REJECTED by the ingest — Gate 6 condition failure: %w", err)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connecting to the gate database: %w", err)
	}
	defer pool.Close()

	// ── §6.2 step 3: the four outcomes ────────────────────────────────────
	conditions, report, err := assertOutcomes(ctx, pool, *binary, env)
	if err != nil {
		return err
	}

	// ── §6.2 step 4: the zero-code-changes proof ──────────────────────────
	gitConditions, gitState, err := assertCleanAtTag(ctx, *tag)
	if err != nil {
		return err
	}
	conditions = append(conditions, gitConditions...)

	if err := gaterun.WriteJSON(filepath.Join(dir, "ingest-report.json"), report); err != nil {
		return err
	}
	if err := gaterun.WriteJSON(filepath.Join(dir, "zero-code-changes.json"), gitState); err != nil {
		return err
	}

	if err := load.WriteSummary(dir, load.Summary{
		Gate: "6", Name: "Spec-Drift Resilience", Version: *version,
		StartedAt: started, FinishedAt: time.Now(),
		Headline: "The committed synthetic spec was ingested through `hangar admin ingest-catalogue` " +
			"with zero source changes, and the four §6.1 outcomes were asserted against the database.",
		Conditions: conditions,
		Environment: map[string]string{
			"Tag":            *tag,
			"HEAD":           gitState.Head,
			"Synthetic spec": *specPath,
			"Ingest path":    "hangar admin ingest-catalogue (production path), against the spec served over HTTP",
		},
		Artefacts: map[string]string{
			"ingest-report.json":     "what the ingest produced for each of the four synthetic operations, read back from app.esi_route, app.esi_scope and app.open_vocabulary.",
			"zero-code-changes.json": "`git status --porcelain` and `git rev-parse HEAD` at the moment the assertions passed — §6.2 step 4.",
		},
		Notes: "§0 rule 4 and §6.2: ANY source change needed to make this pass — including adding a case " +
			"to a switch, extending a regex, or adding an enum value — is a Gate 6 FAILURE, not a fix. The " +
			"correct response would be to redesign the ingest to be data-driven. The synthetic spec was " +
			"authored in Phase 2, before any of this could be run, and is committed unchanged; a fixture " +
			"edited in response to a failure does not test what it claims to.\n\n" +
			"The `git status --porcelain` check is run against the whole working tree, so it also catches " +
			"the subtler failure: a generated artefact (sqlc output, openapi.json, schema.d.ts) that the " +
			"ingest caused to drift would show up as an uncommitted change even though no hand-written " +
			"source was touched.",
	}); err != nil {
		return err
	}

	failed := 0
	for _, c := range conditions {
		verdict := "FAIL"
		if c.Passed {
			verdict = "pass"
		} else {
			failed++
		}
		fmt.Printf("  %-14s %-5s %s\n", c.ID, verdict, c.Measurement)
	}
	if failed > 0 {
		return fmt.Errorf("GATE 6 FAILED (%d conditions) — see %s", failed, filepath.Join(dir, "SUMMARY.md"))
	}
	fmt.Printf("gate6: PASS — %s\n", filepath.Join(dir, "SUMMARY.md"))
	return nil
}

// ingestReport is what the ingest produced, read back from the database.
type ingestReport struct {
	FutureRoute routeRow `json:"future_route"`
	UUIDPath    routeRow `json:"uuid_path_identifier"`
	NovelScope  routeRow `json:"novel_scope_grammar"`
	CacheMode   routeRow `json:"unrecognised_cache_mode"`
	ScopeStored bool     `json:"novel_scope_stored_verbatim"`
	VocabValues []string `json:"cache_mode_open_vocabulary"`
	Schedulable []string `json:"schedulable_synthetic_operations"`
	AppPin      string   `json:"app_pin"`
}

type routeRow struct {
	OperationID     string            `json:"operation_id"`
	UpstreamPath    string            `json:"upstream_path"`
	CompatDate      string            `json:"compatibility_date"`
	BlockedByPin    bool              `json:"blocked_by_pin"`
	CacheMode       *string           `json:"cache_mode"`
	IdentifierTypes map[string]string `json:"identifier_types"`
	Present         bool              `json:"present"`
}

func assertOutcomes(ctx context.Context, pool *pgxpool.Pool, binary string, env []string) ([]load.ConditionResult, *ingestReport, error) {
	report := &ingestReport{}
	var err error

	if report.FutureRoute, err = readRoute(ctx, pool, opFuturePin); err != nil {
		return nil, nil, err
	}
	if report.UUIDPath, err = readRoute(ctx, pool, opUUIDPath); err != nil {
		return nil, nil, err
	}
	if report.NovelScope, err = readRoute(ctx, pool, opNovelScope); err != nil {
		return nil, nil, err
	}
	if report.CacheMode, err = readRoute(ctx, pool, opCacheMode); err != nil {
		return nil, nil, err
	}

	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.esi_scope WHERE scope = $1)`, novelScope).
		Scan(&report.ScopeStored); err != nil {
		return nil, nil, fmt.Errorf("reading app.esi_scope: %w", err)
	}
	if report.VocabValues, err = readVocabulary(ctx, pool, "cache_mode"); err != nil {
		return nil, nil, err
	}
	if report.Schedulable, err = readSchedulable(ctx, pool); err != nil {
		return nil, nil, err
	}
	_ = pool.QueryRow(ctx, `SELECT value FROM app.setting WHERE key = 'esi.compatibility_pin'`).Scan(&report.AppPin)

	var conditions []load.ConditionResult

	// (a) post-pin route: row created, blocked, and excluded from scheduling.
	blockedAndExcluded := report.FutureRoute.Present && report.FutureRoute.BlockedByPin &&
		!contains(report.Schedulable, opFuturePin)
	conditions = append(conditions, load.ConditionResult{
		ID:          "6.1(a)",
		Description: "a route dated past the app pin is created, blocked_by_pin, and excluded from the scheduling query",
		Passed:      blockedAndExcluded,
		Measurement: fmt.Sprintf("row present=%v, blocked_by_pin=%v, compatibility_date=%s, in scheduling set=%v",
			report.FutureRoute.Present, report.FutureRoute.BlockedByPin, report.FutureRoute.CompatDate,
			contains(report.Schedulable, opFuturePin)),
	})

	// (b) uuid path identifier: typed uuid, not bigint and not text.
	uuidTyped := report.UUIDPath.IdentifierTypes["widget_id"] == "uuid"
	conditions = append(conditions, load.ConditionResult{
		ID:          "6.1(b)",
		Description: "a string/uuid path parameter records identifier type `uuid` — never bigint, never text",
		Passed:      uuidTyped,
		Measurement: fmt.Sprintf("identifier_types = %v", report.UUIDPath.IdentifierTypes),
	})

	// (b) continued: the offline identifier check must pass over this spec.
	verifyErr := gaterun.RunBinary(ctx, binary, env, "admin", "verify-identifier-types")
	conditions = append(conditions, load.ConditionResult{
		ID:          "6.1(b)-verify",
		Description: "`hangar admin verify-identifier-types` passes against the ingested synthetic spec",
		Passed:      verifyErr == nil,
		Measurement: verifyResult(verifyErr),
	})

	// (c) novel scope grammar: stored verbatim, nothing rejected it.
	conditions = append(conditions, load.ConditionResult{
		ID:          "6.1(c)",
		Description: "a scope matching neither live grammar is stored verbatim as a text primary key and the route survives",
		Passed:      report.ScopeStored && report.NovelScope.Present,
		Measurement: fmt.Sprintf("app.esi_scope holds %q = %v; the operation was ingested = %v",
			novelScope, report.ScopeStored, report.NovelScope.Present),
	})

	// (d) unrecognised cache mode: recorded, scheduled as ttl-based, not rejected.
	modeStored := report.CacheMode.CacheMode != nil && *report.CacheMode.CacheMode == novelCacheMode
	conditions = append(conditions, load.ConditionResult{
		ID:          "6.1(d)",
		Description: "an unrecognised x-cache-mode is recorded in app.open_vocabulary, schedules as ttl-based, and the route is NOT rejected",
		Passed:      modeStored && contains(report.VocabValues, novelCacheMode) && contains(report.Schedulable, opCacheMode),
		Measurement: fmt.Sprintf("cache_mode stored verbatim=%v, open_vocabulary cache_mode values=%v, still schedulable=%v",
			modeStored, report.VocabValues, contains(report.Schedulable, opCacheMode)),
	})

	return conditions, report, nil
}

func readRoute(ctx context.Context, pool *pgxpool.Pool, operationID string) (routeRow, error) {
	row := routeRow{OperationID: operationID}
	var identifiers []byte
	var compatDate time.Time
	err := pool.QueryRow(ctx, `
		SELECT upstream_path, compatibility_date, blocked_by_pin, cache_mode, identifier_types
		  FROM app.esi_route WHERE operation_id = $1`, operationID).
		Scan(&row.UpstreamPath, &compatDate, &row.BlockedByPin, &row.CacheMode, &identifiers)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return row, nil
		}
		return row, fmt.Errorf("reading route %s: %w", operationID, err)
	}
	row.Present = true
	row.CompatDate = compatDate.Format("2006-01-02")
	if len(identifiers) > 0 {
		_ = json.Unmarshal(identifiers, &row.IdentifierTypes)
	}
	return row, nil
}

func readVocabulary(ctx context.Context, pool *pgxpool.Pool, vocabulary string) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT value FROM app.open_vocabulary WHERE vocabulary = $1 ORDER BY value`, vocabulary)
	if err != nil {
		return nil, fmt.Errorf("reading app.open_vocabulary: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// readSchedulable lists the synthetic operations a scheduler would consider
// — i.e. not blocked by the pin and not retired. Condition (a) requires the
// post-pin route to be ABSENT from this set and (d) requires the
// unknown-cache-mode route to be PRESENT in it.
func readSchedulable(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT operation_id FROM app.esi_route
		 WHERE NOT blocked_by_pin AND retired_at IS NULL AND operation_id LIKE 'GetSynthetic%'
		 ORDER BY operation_id`)
	if err != nil {
		return nil, fmt.Errorf("reading the schedulable set: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// gitState is §6.2 step 4's artefact.
type gitState struct {
	Tag       string    `json:"tag"`
	TagCommit string    `json:"tag_commit"`
	Head      string    `json:"head"`
	Porcelain []string  `json:"git_status_porcelain"`
	CheckedAt time.Time `json:"checked_at"`
}

func assertCleanAtTag(ctx context.Context, tag string) ([]load.ConditionResult, *gitState, error) {
	state := &gitState{Tag: tag, CheckedAt: time.Now()}

	head, err := git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	state.Head = head

	tagCommit, tagErr := git(ctx, "rev-list", "-n", "1", tag)
	state.TagCommit = tagCommit

	porcelain, err := git(ctx, "status", "--porcelain")
	if err != nil {
		return nil, nil, err
	}
	if porcelain != "" {
		state.Porcelain = strings.Split(porcelain, "\n")
	}

	return []load.ConditionResult{
		{
			ID:          "6.2-clean",
			Description: "`git status --porcelain` is EMPTY — the ingest required no source change",
			Passed:      len(state.Porcelain) == 0,
			Measurement: fmt.Sprintf("%d modified paths%s", len(state.Porcelain), firstFew(state.Porcelain)),
		},
		{
			ID:          "6.2-at-tag",
			Description: "`git rev-parse HEAD` equals the release-candidate tag",
			Passed:      tagErr == nil && tagCommit != "" && tagCommit == head,
			Measurement: fmt.Sprintf("HEAD=%s, %s=%s", short(head), tag, short(tagCommit)),
		},
	}, state, nil
}

func git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func firstFew(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	if len(paths) > 5 {
		paths = paths[:5]
	}
	return ": " + strings.Join(paths, "; ")
}

func verifyResult(err error) string {
	if err == nil {
		return "exit 0"
	}
	return "FAILED: " + err.Error()
}
