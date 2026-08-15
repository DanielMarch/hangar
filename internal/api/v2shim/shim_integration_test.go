//go:build integration

package v2shim_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/api"
	"github.com/hangar-project/hangar/internal/api/v2shim"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// corpusHost is the host the corpus was recorded at. Every pagination link
// and `meta.path` in a recorded response is absolute, so the shim's
// responses can only be byte-identical if the test issues its requests at
// the same host — which is correct behaviour, not a fudge: those links must
// reflect the host the client actually reached.
const corpusHost = "seat.local"

func newMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Eventually(t, func() bool { return pool.Ping(ctx) == nil }, 20*time.Second, 250*time.Millisecond)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(hangardb.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(sqlDB, "migrations"))
	require.NoError(t, hangardb.ApplySeeds(ctx, pool))
	return pool
}

// corpusFixture seeds HANGAR with the SAME facts the legacy recorder's
// fixtures.php seeded into MySQL. It is the other half of the byte
// comparison: the corpus is what legacy emitted for this data, so the shim
// has to be reading this data to be comparable.
//
// Values are transcribed from testdata/legacy-api-v2/recorder/src/fixtures.php.
func corpusFixture(t testing.TB, s *store.Store) {
	t.Helper()
	ctx := context.Background()

	const (
		charID     = int64(90000001)
		otherChar  = int64(90000002)
		corpID     = int64(98000001)
		otherCorp  = int64(98000002)
		allianceID = int64(99000001)
	)

	require.NoError(t, s.UpsertCorporationStub(ctx, corpID, "Test Corporation", "TSTC"))
	require.NoError(t, s.UpsertCorporationStub(ctx, otherCorp, "Other Corporation", "OTHR"))

	// app.character_corporation_history foreign-keys into app.character, so
	// the characters have to exist before their history does.
	corp := corpID
	for _, character := range []struct {
		id   int64
		name string
	}{{charID, "Pilot One"}, {otherChar, "Pilot Two"}} {
		_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{
			CharacterID: character.id, Name: character.name, CorporationID: &corp,
			OwnerHash: "corpus-" + character.name,
		})
		require.NoError(t, err)
	}

	// ── contacts, for all three contact routes ───────────────────────────
	type contact struct {
		ownerKind          string
		ownerID, contactID int64
		contactType        string
		standing           float64
		blocked, watched   *bool
		labelIDs           []int64
	}
	yes, no := true, false
	for _, c := range []contact{
		{"character", charID, otherChar, "character", 10, &no, &yes, []int64{1}},
		{"character", charID, corpID, "corporation", -5, &yes, &no, []int64{}},
		{"corporation", corpID, otherChar, "character", 5, nil, nil, []int64{2}},
		{"alliance", allianceID, corpID, "corporation", 7.5, nil, nil, []int64{}},
	} {
		_, err := s.UpsertContact(ctx, gen.UpsertContactParams{
			OwnerKind: c.ownerKind, OwnerID: c.ownerID, ContactID: c.contactID,
			ContactType: c.contactType, Standing: c.standing,
			IsBlocked: c.blocked, IsWatched: c.watched, LabelIds: c.labelIDs,
		})
		require.NoError(t, err)
	}
	for _, label := range []struct {
		ownerKind string
		ownerID   int64
		labelID   int64
		name      string
	}{
		{"character", charID, 1, "Friendly"},
		{"corporation", corpID, 2, "Blue"},
	} {
		_, err := s.UpsertContactLabel(ctx, gen.UpsertContactLabelParams{
			OwnerKind: label.ownerKind, OwnerID: label.ownerID, LabelID: label.labelID, Name: label.name,
		})
		require.NoError(t, err)
	}

	// ── corporation history ──────────────────────────────────────────────
	for _, entry := range []struct {
		recordID, corporationID int64
		deleted                 bool
		start                   string
	}{
		{1, corpID, false, "2020-01-01T00:00:00Z"},
		{2, otherCorp, true, "2018-06-15T10:30:00Z"},
	} {
		start, err := time.Parse(time.RFC3339, entry.start)
		require.NoError(t, err)
		_, err = s.InsertCharacterCorporationHistory(ctx, gen.InsertCharacterCorporationHistoryParams{
			CharacterID: charID, RecordID: entry.recordID, CorporationID: entry.corporationID,
			IsDeleted: entry.deleted, StartDate: start,
		})
		require.NoError(t, err)
	}

	corpusSheetFixture(t, s, corpID, charID, allianceID)
}

// corpusSheetFixture seeds the corporation.sheet recording's corporation and
// its alliance. Values are read off the recording, not invented.
func corpusSheetFixture(t testing.TB, s *store.Store, corpID, ceoID, allianceID int64) {
	t.Helper()
	ctx := context.Background()

	allianceFounded := time.Date(2014, 2, 3, 4, 5, 6, 0, time.UTC)
	ticker := "TSTA"
	creator, creatorCorp, executor := ceoID, corpID, corpID
	_, err := s.UpsertAlliance(ctx, gen.UpsertAllianceParams{
		AllianceID: allianceID, Name: "Test Alliance", Ticker: &ticker,
		CreatorID: &creator, CreatorCorporationID: &creatorCorp,
		ExecutorCorporationID: &executor, DateFounded: &allianceFounded,
	})
	require.NoError(t, err)

	corpFounded := time.Date(2014, 1, 2, 3, 4, 5, 0, time.UTC)
	memberCount := int32(42)
	taxRate := 0.1
	description := "Corp description"
	url := "https://example.invalid/corp"
	homeStation := int64(60003760)
	shares := int64(1000)
	_, err = s.UpsertCorporation(ctx, gen.UpsertCorporationParams{
		CorporationID: corpID, Name: "Test Corporation", Ticker: "TSTC",
		MemberCount: &memberCount, CeoID: &ceoID, AllianceID: &allianceID,
		Description: &description, TaxRate: &taxRate, DateFounded: &corpFounded,
		CreatorID: &ceoID, Url: &url, HomeStationID: &homeStation, Shares: &shares,
		Palette: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
}

// grantedToken creates a user with `permissions` materialised, plus an API
// token scoped to `scope`, and returns the Bearer credential.
func grantedToken(t testing.TB, pool *pgxpool.Pool, s *store.Store, permissions, scope []string) string {
	t.Helper()
	ctx := context.Background()

	user, err := s.CreateUser(ctx, "Shim Test "+uuid.NewString())
	require.NoError(t, err)
	role, err := s.CreateRole(ctx, "shim-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	for _, permission := range permissions {
		require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, permission, rbac.EffectAllow))
	}
	require.NoError(t, rbac.AssignUserRole(ctx, pool, user.UserID, role.RoleID, uuid.NullUUID{}))

	// A fresh secret per call: app.api_token has a UNIQUE index on the
	// hashed secret, so a fixed one makes the second token in a test
	// collide with the first.
	secret := uuid.New()
	sum := sha256.Sum256(secret[:])
	token, err := s.CreateApiToken(ctx, gen.CreateApiTokenParams{
		UserID: user.UserID, Name: "shim", HashedSecret: sum[:], Permissions: scope,
	})
	require.NoError(t, err)
	return token.TokenID.String() + "." + base64.RawURLEncoding.EncodeToString(secret[:])
}

// shimServer builds the real handler chain — api.Handler wrapping a mux
// with the shim registered — so the tests exercise the same middleware
// ordering production does.
func shimServer(t testing.TB, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	s := store.New(pool)
	mux := http.NewServeMux()
	v2shim.Register(mux, v2shim.Deps{Store: s})
	return api.Handler(mux, s)
}

func get(t testing.TB, handler http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://"+corpusHost+path, nil)
	req.Host = corpusHost
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func corpusBytes(t testing.TB, name string) []byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(root, "testdata", "legacy-api-v2", "responses", name+".json"))
	require.NoError(t, err, "corpus recording %q is missing", name)
	return body
}

// ── Phase 19 exit criteria ───────────────────────────────────────────────

// TestShimByteCompatibleForAllNineControllers is the roadmap's
// byte-compatibility criterion, iterated PER ROUTE.
//
// ── WHY THIS TEST CHANGED IN PHASE 20.6 ──────────────────────────────────
// It used to iterate CONTROLLERS, and a controller counted as "covered" the
// moment any one of its routes was served. CharacterController passed on the
// strength of 2 routes out of 15. So the test that existed to police the
// shim's coverage was structurally unable to see 29 of the 33 routes, and
// the honest "4 of 33" number lived only in a comment.
//
// Now every route in Classification() gets its own assertion, chosen by its
// status:
//
//   - StatusServed must be BYTE-IDENTICAL to its recording;
//   - StatusBreaking must answer 410 with a migration pointer;
//   - StatusUnshimmable and StatusPending must answer 501 — with DIFFERENT
//     bodies, because "rewrite your integration" and "wait for a release"
//     are different instructions and a client acting on the wrong one wastes
//     real time.
//
// The controller roll-up is kept as a final subtest, because ApiController
// owning no route at all is still worth asserting.
func TestShimByteCompatibleForAllNineControllers(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	corpusFixture(t, s)

	credential := grantedToken(t, pool, s,
		[]string{"characters.view", "corporations.view", "alliances.view"},
		[]string{"characters.view", "corporations.view", "alliances.view"})
	handler := shimServer(t, pool)
	auth := map[string]string{"Authorization": "Bearer " + credential}

	covered := map[string]bool{}
	for _, route := range v2shim.Classification() {
		route := route
		covered[route.Controller] = true

		name := route.Corpus
		if name == "" {
			name = route.Controller
		}
		t.Run(string(route.Status)+"/"+name, func(t *testing.T) {
			switch route.Status {
			case v2shim.StatusServed:
				require.NotEmpty(t, route.Corpus,
					"a served route with no recording cannot be byte-verified, so it is not served")
				path := corpusPath(t, route.Corpus)
				rec := get(t, handler, path, auth)
				require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
				require.Equal(t, string(corpusBytes(t, route.Corpus)), rec.Body.String(),
					"route %s is not byte-identical to its recording", route.Pattern)

			case v2shim.StatusBreaking:
				rec := get(t, handler, legacyPathFor(t, route), auth)
				require.Equal(t, http.StatusGone, rec.Code, "body: %s", rec.Body.String())
				require.Contains(t, rec.Body.String(), "BREAKING CHANGE",
					"a 410 must say what broke, not merely refuse")

			case v2shim.StatusUnshimmable:
				rec := get(t, handler, legacyPathFor(t, route), auth)
				require.Equal(t, http.StatusNotImplemented, rec.Code, "body: %s", rec.Body.String())
				require.Contains(t, rec.Body.String(), "cannot be",
					"an unshimmable route must say the break is permanent")
				require.NotEmpty(t, route.Reason, "an unshimmable route must carry its reason")

			case v2shim.StatusPending:
				rec := get(t, handler, legacyPathFor(t, route), auth)
				require.Equal(t, http.StatusNotImplemented, rec.Code, "body: %s", rec.Body.String())
				require.Contains(t, rec.Body.String(), "not shimmed yet",
					"a pending route must read as unfinished work, never as a permanent break")
				require.NotEmpty(t, route.Reason, "a pending route must carry the reason it is not done")
			}
		})
	}

	t.Run("every legacy controller is accounted for", func(t *testing.T) {
		// ApiController is the abstract base class: it declares the
		// security scheme and owns no route, so "accounted for" means
		// "correctly has nothing".
		covered["ApiController"] = true
		for _, controller := range []string{
			"AllianceController", "ApiController", "CharacterController", "CorporationController",
			"KillmailsController", "RoleController", "RoleLookupController", "SquadController",
			"UserController",
		} {
			require.True(t, covered[controller],
				"legacy controller %s has no shim route, no documented break and no 501 — it would 404 silently", controller)
		}
	})

	// ── PHASE 20.6: THE ROUTE COUNT IS MEASURED, NOT ASSERTED ────────────
	// This package's own comments said "legacy's /api/v2 has 33 GET routes"
	// for five phases. Nothing measured it, and it is wrong: MANIFEST.json
	// holds 34 recordings which collapse to 32 distinct route patterns (two
	// are second recordings of the same path — a page-2 and an empty-set
	// case), and the two role routes were never recorded because they are
	// breaking. 32 + 2 = 34.
	//
	// That is exactly defect B6's shape — a count stated rather than
	// derived — so the expected number is computed from the corpus here
	// rather than replacing one hard-coded number with another.
	t.Run("classification covers every legacy read route", func(t *testing.T) {
		recorded := distinctRecordedRoutePatterns(t)
		byStatus := v2shim.ByStatus()
		breaking := len(byStatus[v2shim.StatusBreaking])

		total := 0
		for _, routes := range byStatus {
			total += len(routes)
		}
		require.Equal(t, recorded+breaking, total,
			"Classification must name every recorded route plus the unrecorded breaking ones "+
				"(%d recorded patterns + %d breaking)", recorded, breaking)

		t.Logf("legacy read routes = %d (%d recorded patterns + %d unrecorded breaking): "+
			"served=%d pending=%d unshimmable=%d breaking=%d",
			total, recorded, breaking,
			len(byStatus[v2shim.StatusServed]), len(byStatus[v2shim.StatusPending]),
			len(byStatus[v2shim.StatusUnshimmable]), breaking)
	})
}

// distinctRecordedRoutePatterns counts the route patterns MANIFEST.json
// covers, collapsing the recordings that are second captures of the same
// path (character.wallet-journal.page2, character.assets.empty) onto the
// pattern they exercise.
func distinctRecordedRoutePatterns(t testing.TB) int {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "testdata", "legacy-api-v2", "MANIFEST.json"))
	require.NoError(t, err)

	var manifest []struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	trailingID := regexp.MustCompile(`/\d+$`)
	patterns := map[string]bool{}
	for _, entry := range manifest {
		patterns[trailingID.ReplaceAllString(entry.Path, "/{id}")] = true
	}
	require.NotEmpty(t, patterns, "the manifest yielded no routes — the count would be vacuous")
	return len(patterns)
}

// legacyPathFor turns a classified route into a concrete request path. A
// route with a recording uses the recorded path, so the test asks for
// exactly what a legacy client asked for; the two role controllers have no
// recording (they were never shimmable) and use their pattern with the id
// placeholder filled in.
func legacyPathFor(t testing.TB, route v2shim.LegacyRoute) string {
	t.Helper()
	if route.Corpus != "" {
		return corpusPath(t, route.Corpus)
	}
	return strings.ReplaceAll(route.Pattern, "{id}", "1")
}

// corpusPath reads the legacy path a recording was made at straight out of
// MANIFEST.json, so the test cannot drift from the corpus by hard-coding a
// path the recorder did not use.
func corpusPath(t testing.TB, name string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "testdata", "legacy-api-v2", "MANIFEST.json"))
	require.NoError(t, err)

	var manifest []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		// `query` is deliberately not decoded: PHP encodes an empty array
		// as `[]` and a populated one as an object, so a single Go type
		// cannot hold both — and the path is all this needs.
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	for _, entry := range manifest {
		if entry.Name == name {
			return entry.Path
		}
	}
	t.Fatalf("MANIFEST.json has no entry named %q", name)
	return ""
}

// TestShimEmitsDeprecationAndSunset — both headers on EVERY shim response,
// including the error and breaking-change paths. A client whose only
// /api/v2 traffic is 401s is exactly the one that needs to be told the
// surface is going away.
func TestShimEmitsDeprecationAndSunset(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	corpusFixture(t, s)
	credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"characters.view"})
	handler := shimServer(t, pool)

	for name, tc := range map[string]struct {
		path    string
		headers map[string]string
		want    int
	}{
		"a successful collection": {
			path: "/api/v2/character/contacts/90000001", want: http.StatusOK,
			headers: map[string]string{"Authorization": "Bearer " + credential},
		},
		"an unauthenticated request": {
			path: "/api/v2/character/contacts/90000001", want: http.StatusUnauthorized,
		},
		"a forbidden request": {
			path: "/api/v2/alliance/contacts/99000001", want: http.StatusForbidden,
			headers: map[string]string{"Authorization": "Bearer " + credential},
		},
		"a reshaped route": {
			path: "/api/v2/roles", want: http.StatusGone,
		},
		"a route that is not shimmed": {
			path: "/api/v2/users", want: http.StatusNotImplemented,
		},
		"an unknown /api/v2 path": {
			path: "/api/v2/nothing/here", want: http.StatusNotImplemented,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := get(t, handler, tc.path, tc.headers)
			require.Equal(t, tc.want, rec.Code, "body: %s", rec.Body.String())

			require.Equal(t, "true", rec.Header().Get(v2shim.DeprecationHeader))

			sunset := rec.Header().Get(v2shim.SunsetHeader)
			require.NotEmpty(t, sunset)
			parsed, err := http.ParseTime(sunset)
			require.NoError(t, err, "Sunset must be an RFC 8594 IMF-fixdate, got %q", sunset)
			require.Equal(t, v2shim.SunsetDate.UTC().Truncate(time.Second), parsed.UTC().Truncate(time.Second))
			require.True(t, parsed.After(time.Now()), "the sunset date has passed — the shim should have been removed")

			require.Contains(t, strings.Join(rec.Header().Values(v2shim.LinkHeader), " "), "rel=\"sunset\"",
				"RFC 8594 §3: a Sunset without a link leaves the client knowing WHEN but not WHAT TO DO")
		})
	}
}

// TestShimStripsSyncEnvelope — SRS Appendix C: legacy v2 has no `_sync`
// envelope, and the shim must strip it rather than pass it through.
//
// Asserted on the RAW BYTES, not on a parsed structure: `_sync` could
// appear nested inside a row as well as at the top level, and a
// key-existence check on a decoded map would only ever look where the test
// author thought to look.
func TestShimStripsSyncEnvelope(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	corpusFixture(t, s)
	credential := grantedToken(t, pool, s,
		[]string{"characters.view", "corporations.view", "alliances.view"},
		[]string{"characters.view", "corporations.view", "alliances.view"})
	handler := shimServer(t, pool)
	auth := map[string]string{"Authorization": "Bearer " + credential}

	for _, route := range v2shim.ByStatus()[v2shim.StatusServed] {
		t.Run(route.Corpus, func(t *testing.T) {
			rec := get(t, handler, corpusPath(t, route.Corpus), auth)
			require.Equal(t, http.StatusOK, rec.Code)

			body := rec.Body.String()
			require.NotContains(t, body, "_sync",
				"the _sync envelope must be stripped, not passed through — legacy v2 has no such key")

			// And the legacy envelope IS present, so "stripped" cannot be
			// satisfied by returning nothing at all.
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
			require.Contains(t, envelope, "data")
			require.NotContains(t, envelope, "page", "`page` is /api/v1's cursor block; legacy had `meta`")

			// PHASE 20.6: which envelope to expect is read off the RECORDING
			// rather than from a second list here. Legacy's collection routes
			// carry links/meta and its single-resource routes (corporation
			// sheet, killmail detail, squad/user show) carry only `data` —
			// so asserting links/meta unconditionally, as this test did until
			// corporation.sheet became the first served item route, would
			// fail a route for being correctly shaped.
			var recorded map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(corpusBytes(t, route.Corpus), &recorded))
			if _, isCollection := recorded["links"]; isCollection {
				require.Contains(t, envelope, "links")
				require.Contains(t, envelope, "meta")
			} else {
				require.NotContains(t, envelope, "links",
					"a single-resource route must not grow a pagination envelope the recording does not have")
				require.NotContains(t, envelope, "meta")
			}
		})
	}
}

// TestReshapedRoutesReturn410WithMigrationPointer — RoleController and
// RoleLookupController.
func TestReshapedRoutesReturn410WithMigrationPointer(t *testing.T) {
	pool := newMigratedPool(t)
	handler := shimServer(t, pool)

	for _, path := range []string{
		"/api/v2/roles",
		"/api/v2/roles/1",
		"/api/v2/roles/query/permissions",
		"/api/v2/roles/query/role-check/90000001/admin",
		"/api/v2/roles/query/permission-check/90000001/characters.view",
		// A path under the reshaped prefixes that legacy never had: it must
		// still explain the break rather than 404, or a client with a typo
		// goes looking for the typo instead of reading the guide.
		"/api/v2/roles/query/anything-at-all",
	} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, handler, path, nil)
			require.Equal(t, http.StatusGone, rec.Code)

			var message string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message),
				"legacy's error body is a bare JSON string; the shim must not change the shape")
			require.Contains(t, message, "BREAKING CHANGE")
			require.Contains(t, message, "/api/v1/admin/",
				"a breaking-change response without a migration pointer is just a failure")
			require.Contains(t, message, v2shim.DeprecationDocsURL)
		})
	}

	t.Run("the reason is explained, not just asserted", func(t *testing.T) {
		rec := get(t, handler, "/api/v2/roles", nil)
		var message string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
		require.Contains(t, message, "filters")
		require.Contains(t, message, "deny")
	})
}

// TestShimAuthenticatesLikeV1AndCannotExceedTokenScope is the exit
// criterion this phase adds for itself: an unauthenticatable shim is a shim
// nobody can migrate to, and a shim that authenticates DIFFERENTLY is worse
// than one that cannot authenticate at all.
//
// The four things that have to hold:
//  1. no credential  → 401, in legacy's body shape;
//  2. a HANGAR Bearer token → works;
//  3. legacy's X-Token header carrying the same credential → also works,
//     because a migration aid that demands a source change on day one is
//     not much of an aid;
//  4. a token whose OWN scope omits the permission → 403 even though its
//     owner holds it. This is the Phase 18 B21 cap, and the shim must not
//     be a way around it.
func TestShimAuthenticatesLikeV1AndCannotExceedTokenScope(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	corpusFixture(t, s)
	handler := shimServer(t, pool)

	const path = "/api/v2/character/contacts/90000001"

	t.Run("no credential is 401 in legacy's shape", func(t *testing.T) {
		rec := get(t, handler, path, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		var message string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
		require.Equal(t, "Unauthorized", message,
			"legacy answered a bare JSON string; a client parsing it as one must keep working")
	})

	t.Run("a HANGAR bearer token works", func(t *testing.T) {
		credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"characters.view"})
		rec := get(t, handler, path, map[string]string{"Authorization": "Bearer " + credential})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("legacy's X-Token header is accepted as an alias", func(t *testing.T) {
		credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"characters.view"})
		rec := get(t, handler, path, map[string]string{v2shim.LegacyTokenHeader: credential})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("X-Token is NOT accepted on /api/v1", func(t *testing.T) {
		// The alias is a compatibility affordance for the surface being
		// retired. Letting it leak onto /api/v1 would make the legacy
		// header permanent.
		credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"characters.view"})
		req := httptest.NewRequest(http.MethodGet, "http://"+corpusHost+"/api/v1/me", nil)
		req.Header.Set(v2shim.LegacyTokenHeader, credential)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusOK, rec.Code)
	})

	t.Run("a scoped token cannot exceed its scope", func(t *testing.T) {
		// The owner holds characters.view; the TOKEN does not. B21's cap
		// says the request must fail — a scoped integration must never be
		// silently upgraded to its owner's full authority, and a shim route
		// must not be the place that happens.
		credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"corporations.view"})
		rec := get(t, handler, path, map[string]string{"Authorization": "Bearer " + credential})
		require.Equal(t, http.StatusForbidden, rec.Code,
			"the shim bypassed the API token permission cap Phase 18 introduced")
	})

	t.Run("a revoked token stops working", func(t *testing.T) {
		credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"characters.view"})
		ok := get(t, handler, path, map[string]string{"Authorization": "Bearer " + credential})
		require.Equal(t, http.StatusOK, ok.Code)

		tokenID, _, _ := strings.Cut(credential, ".")
		id, err := uuid.Parse(tokenID)
		require.NoError(t, err)
		require.NoError(t, s.RevokeApiToken(context.Background(), id))

		rec := get(t, handler, path, map[string]string{"Authorization": "Bearer " + credential})
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// TestShimRefusesFilterRatherThanIgnoringIt — a dropped `$filter` returns
// MORE data than the client asked for, which is the dangerous direction.
func TestShimRefusesFilterRatherThanIgnoringIt(t *testing.T) {
	pool := newMigratedPool(t)
	s := store.New(pool)
	corpusFixture(t, s)
	credential := grantedToken(t, pool, s, []string{"characters.view"}, []string{"characters.view"})
	handler := shimServer(t, pool)

	rec := get(t, handler,
		"/api/v2/character/contacts/90000001?"+url("$filter", "standing lt 0"),
		map[string]string{"Authorization": "Bearer " + credential})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var message string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
	require.Contains(t, message, "$filter")
	require.Contains(t, message, "MORE data than was asked for")
}

// TestWriteRoutesAreNotShimmed — SRS §10: write routes "must return a clear
// 'not shimmed' response rather than a 404".
func TestWriteRoutesAreNotShimmed(t *testing.T) {
	pool := newMigratedPool(t)
	handler := shimServer(t, pool)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "http://"+corpusHost+"/api/v2/users", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotImplemented, rec.Code,
				"a 404 sends an integrator looking for a typo instead of reading the migration guide")
			require.Equal(t, "true", rec.Header().Get(v2shim.DeprecationHeader))
			var message string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &message))
			require.Contains(t, message, "read-only")
		})
	}
}

func url(key, value string) string {
	return fmt.Sprintf("%s=%s", strings.ReplaceAll(key, "$", "%24"), strings.ReplaceAll(value, " ", "%20"))
}
