//go:build integration

package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newMigratedPool9(t *testing.T) *pgxpool.Pool {
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

	return pool
}

// TestMailBodyRoutedThroughCatalogue (roadmap exit criterion): the
// mail-body call is made with UpstreamPath taken from the app.esi_route
// catalogue row, substituted through internal/esi.Client exactly like
// every other route — never a hand-built URL string. Verified by pointing
// the route at a deliberately unusual upstream_path (still containing the
// {character_id}/{mail_id} placeholders CCP's spec uses) and asserting
// the test server actually received a request built from THAT template.
func TestMailBodyRoutedThroughCatalogue(t *testing.T) {
	pool := newMigratedPool9(t)
	ctx := context.Background()
	s := store.New(pool)

	const characterID int64 = 90000201
	const mailID int64 = 7042
	_, err := s.UpsertCharacter(ctx, gen.UpsertCharacterParams{CharacterID: characterID, Name: "Mail Test", OwnerHash: "oh-9"})
	require.NoError(t, err)
	_, err = s.UpsertMailHeader(ctx, gen.UpsertMailHeaderParams{
		CharacterID: characterID, MailID: mailID, SentAt: time.Now(), Labels: []int64{},
	})
	require.NoError(t, err)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"body": "hi"})
	}))
	defer srv.Close()

	route, err := s.UpsertEsiRoute(ctx, gen.UpsertEsiRouteParams{
		OperationID: "get_characters_character_id_mail_mail_id", Method: http.MethodGet,
		UpstreamPath: mailBodyPath, SpecFragment: json.RawMessage(`{}`), IdentifierTypes: json.RawMessage(`{}`),
		CompatibilityDate: pgtype.Date{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	sub, err := s.UpsertSyncSubscription(ctx, gen.UpsertSyncSubscriptionParams{
		EntityKind: "character", EntityID: characterID, RouteID: route.RouteID,
	})
	require.NoError(t, err)

	w := &CharacterWorker{Gateway: &esi.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}, Policy: sync.PolicyConfig{}}
	rows, _, err := w.doMailBodyFanout(ctx, s, sub, route, characterID, "test-token")
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)

	require.Equal(t, "/characters/90000201/mail/7042", gotPath,
		"the fetched path must come from route.UpstreamPath's {character_id}/{mail_id} substitution, not a hand-built URL")

	body, err := s.GetMailBody(ctx, characterID, mailID)
	require.NoError(t, err)
	require.Equal(t, "hi", body.Body)
}
