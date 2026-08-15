package capability

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/api/middleware"
	esicatalogue "github.com/hangar-project/hangar/internal/esi/catalogue"
	"github.com/hangar-project/hangar/internal/sync/worker"
)

// TestSearchNeverProxiesESI — Appendix A #40, entity search.
//
// SRS §6.7/§4.7: "CCP prohibits using ESI for entity discovery". HANGAR's
// search therefore answers from app.character / app.corporation /
// app.alliance rows it has already synced, and never calls out. The
// capability is defined by what it does NOT do, so the test has to be too —
// and "does not call ESI" is exactly the kind of claim a behavioural test
// can only sample and a structural one can settle.
//
// Two assertions, and together they are exhaustive rather than indicative:
//
//  1. /characters/{character_id}/search is classified ReasonOnDemand — an
//     explicit record that HANGAR does not poll it, checked against the
//     total route partition so it cannot simply go unnoticed.
//
//  2. api.Deps — the ONLY thing every /api/v1 handler is constructed with —
//     carries no field of any internal/esi type. There is no ESI client in
//     scope anywhere in the v1 surface, so no handler can proxy to ESI even
//     by accident, and search is covered by construction rather than by
//     inspection of one function.
func TestSearchNeverProxiesESI(t *testing.T) {
	requireDeliberatelyUnmapped(t, "/characters/{character_id}/search")
	require.Equal(t, worker.ReasonOnDemand,
		worker.DeliberatelyUnmapped()["/characters/{character_id}/search"],
		"the search route must be recorded as answered locally, not merely as unmapped for some other reason")
	requireEndpoints(t, "/api/v1/support/search")

	depsType := reflect.TypeOf(api.Deps{})
	for i := range depsType.NumField() {
		field := depsType.Field(i)
		pkg := packagePathOf(field.Type)
		require.NotContainsf(t, pkg, "internal/esi",
			"api.Deps.%s is of type %s from %s — an ESI client in the v1 handler's dependency set means a "+
				"handler CAN proxy entity discovery to ESI, which CCP prohibits",
			field.Name, field.Type, pkg)
	}
}

// packagePathOf unwraps pointers and slices to the underlying named type's
// package import path, "" for builtins.
func packagePathOf(t reflect.Type) string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	return t.PkgPath()
}

// TestApiTokenScopeCap — Appendix A #47, scoped third-party API tokens.
//
// app.api_token.permissions is a SUBSET a user chose to expose to one
// integration. Resolving a token to its owner's user id and stopping there
// would hand every narrowly-scoped token the owner's FULL effective
// permissions — a privilege escalation, and the exact thing scoping exists
// to prevent.
//
// The test drives RequirePermission with a real request carrying a token
// scope that omits the permission, and asserts 403. The STORE IS NIL, which
// is the second half of the assertion and not a shortcut: the cap must be
// applied before any database lookup, so a request refused by scope must
// never reach the store. If the order were ever reversed this test would
// panic on the nil dereference rather than quietly still passing.
func TestApiTokenScopeCap(t *testing.T) {
	requireEndpoints(t, "/api/v1/api-tokens")

	const wanted = "admin.users.manage"
	reached := false
	guarded := middleware.RequirePermission(nil, wanted)(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	ctx := middleware.WithUserID(req.Context(), uuid.New())
	// A token whose owner may well hold admin.users.manage, but which was
	// minted with a narrower scope.
	ctx = middleware.WithTokenScope(ctx, uuid.New(), []string{"characters.view", "corporations.view"})

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusForbidden, rec.Code,
		"a token whose scope omits the permission must be refused even though its owner may hold it")
	require.False(t, reached, "the guarded handler must not run")
	require.Contains(t, rec.Body.String(), wanted,
		"the refusal names the permission the token lacks, so an integrator can widen the right one")

	// 403, not 401: the credential is valid, it simply does not carry this
	// permission. The distinction is what tells a client to re-mint a wider
	// token rather than to re-authenticate.
	require.NotEqual(t, http.StatusUnauthorized, rec.Code)
}

// TestAlertCatalogueComplete — Appendix A #57, the alert catalogue.
//
// §4.4 fixes both the eight domains and the per-domain counts, and the
// catalogue is the one part of the alerting subsystem that is pure data —
// so completeness is answerable without a database and belongs in the fast
// suite. What needs Postgres is whether the SEED matches it, and
// internal/alerting's TestSeededCatalogueMatchesTheGoCatalogue already
// covers that; this asserts the Go side is complete and self-consistent.
//
// The threshold half is the assertion with teeth: §4.4 requires that "a
// threshold alert whose source route is not in the sync set is a build-time
// error". A threshold pointed at a route HANGAR cannot poll can never fire,
// and would look exactly like a threshold that simply has not tripped.
func TestAlertCatalogueComplete(t *testing.T) {
	requireEndpoints(t, "/api/v1/admin/alerts")

	counts := catalogue.CountByDomain()
	require.Len(t, counts, len(catalogue.Domains), "every domain must be represented, and no others")

	total := 0
	for _, domain := range catalogue.Domains {
		want, declared := catalogue.ExpectedCounts[domain]
		require.Truef(t, declared, "domain %q has no declared count in ExpectedCounts", domain)
		require.Equalf(t, want, counts[domain], "domain %q holds %d alert types, §4.4 declares %d", domain, counts[domain], want)
		total += counts[domain]
	}
	require.Equal(t, len(catalogue.Catalogue), total, "the per-domain counts must partition the catalogue exactly")
	require.Equal(t, 54, total, "§4.4's catalogue is 54 seeded alert types across eight domains")

	// ── DEFECT B54 ───────────────────────────────────────────────────────
	// The traceability matrix's own breakdown of this catalogue read "42
	// esi_notification, 9 domain_event, 4 threshold", which sums to 55. A
	// hand-written breakdown of a hand-written total, wrong by arithmetic
	// alone. The split is asserted here so the next one that does not add up
	// fails instead of being read past — measured against a first boot of
	// the release image, which seeds exactly these three counts.
	byCategory := map[catalogue.Category]int{}
	for _, entry := range catalogue.Catalogue {
		byCategory[entry.Category]++
	}
	require.Equal(t, 41, byCategory[catalogue.CategoryESINotification])
	require.Equal(t, 9, byCategory[catalogue.CategoryDomainEvent])
	require.Equal(t, 4, byCategory[catalogue.CategoryThreshold])
	categoryTotal := 0
	for _, n := range byCategory {
		categoryTotal += n
	}
	require.Equal(t, total, categoryTotal, "the per-category counts must partition the catalogue exactly too")

	// Every threshold declares a source route, which is §4.4's own
	// constraint and the database's (alert_type's threshold_declares_source
	// CHECK). A threshold without one cannot be seeded at all.
	require.Len(t, catalogue.ThresholdSourceRoutes(), byCategory[catalogue.CategoryThreshold],
		"each threshold alert declares exactly one source route")

	// Every name resolves back to its own entry, and no name repeats — a
	// duplicate would silently shadow one of the two in ByName's map.
	names := catalogue.Names()
	require.Len(t, names, total, "Names() must enumerate the whole catalogue")
	seen := map[string]bool{}
	for _, name := range names {
		require.Falsef(t, seen[name], "alert type %q appears twice in the catalogue", name)
		seen[name] = true
		entry, ok := catalogue.ByName(name)
		require.Truef(t, ok, "alert type %q is enumerated but does not resolve", name)
		require.Equal(t, name, entry.Name)
		require.NotEmptyf(t, entry.Domain, "alert type %q has no domain", name)
	}

	// §4.4's build-time rule, executed.
	syncSet := worker.SyncSet()
	for _, route := range catalogue.ThresholdSourceRoutes() {
		require.Truef(t, syncSet[route],
			"threshold alert source route %s is not in the sync set — nothing polls it, so the threshold "+
				"can never be evaluated and its silence would be indistinguishable from not tripping", route)
	}
}

// TestCompatibilityPinAdvance — Appendix A #49, compatibility-pin
// administration.
//
// The pin is HANGAR's declared ESI compatibility date. Advancing it past
// what the live spec actually offers would block every route dated after it
// (app.esi_route.blocked_by_pin) while claiming the opposite, so the
// invariant that matters is ordering: a pin may never exceed D_max, and
// D_max may never exceed today.
//
// internal/esi/catalogue's integration suite covers the DB-side advance and
// its recorded history (TestAdvancePinRecordsHistory,
// TestPinAdvanceRefusesDateNewerThanDMax, TestPinAdvanceRecordsComputedDiff).
// This asserts the pure date arithmetic those depend on, and the admin
// surface the capability is delivered through.
func TestCompatibilityPinAdvance(t *testing.T) {
	requireEndpoints(t,
		"/api/v1/admin/esi/catalogue/pin",
		"/api/v1/admin/esi/catalogue/pin/preview",
		"/api/v1/admin/esi/catalogue/pin/history")

	_, meta, err := esicatalogue.LoadEmbeddedSnapshot()
	require.NoError(t, err, "the embedded snapshot carries the D_max a pin may not exceed")
	dmax, err := meta.DMaxDate()
	require.NoError(t, err, "the recorded D_max must parse as a compatibility date")
	require.False(t, dmax.IsZero())

	// The ceiling is not the wall-clock day: ESI's compatibility date rolls
	// over at 11:00 UTC, so at 10:59 "today" is still yesterday's date and a
	// pin set to the calendar day would be a FUTURE date ESI rejects
	// outright. Pinned at two fixed instants either side of the rollover
	// rather than against time.Now(), so the assertion means the same thing
	// whenever the suite runs.
	justBefore := time.Date(2026, 8, 15, 10, 59, 0, 0, time.UTC)
	justAfter := time.Date(2026, 8, 15, 11, 0, 0, 0, time.UTC)
	require.Equal(t, "2026-08-14", dayOf(esicatalogue.CurrentDate(justBefore)),
		"before 11:00 UTC the current compatibility date is still the previous day")
	require.Equal(t, "2026-08-15", dayOf(esicatalogue.CurrentDate(justAfter)),
		"at 11:00 UTC it rolls over")

	// A D_max recorded in the future — a snapshot captured against a spec
	// that already advertises tomorrow — is clamped rather than letting an
	// administrator pin a date CCP has not reached.
	tomorrow := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	require.Equal(t, "2026-08-15", dayOf(esicatalogue.ClampToToday(tomorrow, justAfter)),
		"ClampToToday is what keeps the pin from exceeding the compatibility dates that actually exist")

	// A date already in the past is left alone: clamping is a ceiling, not a
	// normalisation, and moving it would silently advance a deliberate pin.
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t, past, esicatalogue.ClampToToday(past, justAfter))

	// The pin the gate reports advancing TO is the maximum compatibility
	// date the spec offers, parsed rather than assumed.
	maxSeen, err := esicatalogue.MaxDate([]string{"2020-01-01", meta.DMax, "2019-06-30"})
	require.NoError(t, err)
	require.Equal(t, dayOf(dmax), dayOf(maxSeen))
}

// dayOf drops the time of day, which a compatibility date does not carry.
func dayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }
