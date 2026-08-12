//go:build integration

package events_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	hangardb "github.com/hangar-project/hangar/db"
	"github.com/hangar-project/hangar/internal/config"
	"github.com/hangar-project/hangar/internal/crypto"
	"github.com/hangar-project/hangar/internal/events"
	"github.com/hangar-project/hangar/internal/rbac"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// newMigratedPool boots a real, migrated PG18 — the pattern every other
// integration suite in this repo uses. River's queue tables are not needed
// here: nothing in internal/events enqueues a River job.
func newMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("hangar"), tcpostgres.WithUsername("hangar"), tcpostgres.WithPassword("hangar"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	poolCfg, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err)
	poolCfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
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

func testKeyring(t testing.TB) *crypto.Keyring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	kr, err := crypto.NewKeyring(config.CryptoConfig{
		MasterKey:        config.NewSecret(base64.StdEncoding.EncodeToString(key)),
		MasterKeyVersion: 1,
	})
	require.NoError(t, err)
	return kr
}

func seedUser(t testing.TB, s *store.Store) uuid.UUID {
	t.Helper()
	u, err := s.CreateUser(context.Background(), "Events Test "+uuid.NewString())
	require.NoError(t, err)
	return u.UserID
}

// seedEndpoint creates an enabled webhook endpoint pointing at url and
// returns it with the plaintext secret the test needs to verify signatures.
func seedEndpoint(t testing.TB, s *store.Store, kr *crypto.Keyring, owner uuid.UUID, url string, filter []string) (gen.AppWebhookEndpoint, []byte) {
	t.Helper()
	ctx := context.Background()

	secret, err := crypto.NewWebhookSecret()
	require.NoError(t, err)

	// event_filter is `text[] NOT NULL DEFAULT '{}'`, and a nil Go slice
	// binds as SQL NULL, not as the empty array — so "subscribe to
	// everything" has to be spelled with an empty non-nil slice.
	if filter == nil {
		filter = []string{}
	}

	// The endpoint id is part of the AAD, so the row must exist before its
	// secret can be sealed. Seal against a placeholder, insert, then
	// re-seal against the real id — the same two-step every caller of
	// SealWebhookSecret needs, which is worth exercising here rather than
	// papering over.
	placeholder, err := crypto.SealWebhookSecret(kr, uuid.Nil, secret)
	require.NoError(t, err)
	row, err := s.CreateWebhookEndpoint(ctx, gen.CreateWebhookEndpointParams{
		OwnerUserID: owner, Url: url, HmacKeyVersion: int32(placeholder.KeyVersion),
		HmacWrappedDek: placeholder.WrappedDEK, HmacNonce: placeholder.Nonce,
		HmacCiphertext: placeholder.Ciphertext, EventFilter: filter,
	})
	require.NoError(t, err)

	sealed, err := crypto.SealWebhookSecret(kr, row.EndpointID, secret)
	require.NoError(t, err)
	_, err = s.DBTX().Exec(ctx,
		`UPDATE app.webhook_endpoint SET hmac_key_version=$2, hmac_wrapped_dek=$3, hmac_nonce=$4, hmac_ciphertext=$5 WHERE endpoint_id=$1`,
		row.EndpointID, int32(sealed.KeyVersion), sealed.WrappedDEK, sealed.Nonce, sealed.Ciphertext)
	require.NoError(t, err)

	row.HmacKeyVersion, row.HmacWrappedDek, row.HmacNonce, row.HmacCiphertext =
		int32(sealed.KeyVersion), sealed.WrappedDEK, sealed.Nonce, sealed.Ciphertext
	return row, secret
}

func countOutbox(t testing.TB, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM app.outbox_event`).Scan(&n))
	return n
}

func countRoleGrants(t testing.TB, pool *pgxpool.Pool, roleID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM app.role_grant WHERE role_id = $1`, roleID).Scan(&n))
	return n
}

// ── Phase 19 exit criterion 1 ────────────────────────────────────────────

// TestOutboxAtomicWithMutation is the roadmap's first Phase 19 exit
// criterion: "rolling back the mutation rolls back the outbox row".
//
// It asserts the property in BOTH directions, because only one of them is
// interesting and it is not the obvious one:
//
//   - commit  → the mutation AND the outbox row are both present. This
//     proves the event is actually produced, i.e. that the rollback case
//     below is not passing trivially because nothing ever writes an event.
//   - rollback → NEITHER is present. This is the guarantee: an integration
//     is never told about a change that did not happen.
//
// The rollback is forced by an error raised AFTER both writes have been
// issued inside the transaction, which is the real-world shape (a
// constraint violation, a lost connection, a later step failing) rather
// than an artificial "don't write it" path.
func TestOutboxAtomicWithMutation(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)

	role, err := s.CreateRole(ctx, "outbox-atomicity-"+uuid.NewString(), nil, false)
	require.NoError(t, err)

	t.Run("committing keeps both", func(t *testing.T) {
		before := countOutbox(t, pool)
		require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))

		require.Equal(t, 1, countRoleGrants(t, pool, role.RoleID), "the mutation must have committed")
		require.Equal(t, before+1, countOutbox(t, pool), "the outbox row must have committed with it")

		var (
			aggregate, aggregateID, eventType string
			payload                           json.RawMessage
		)
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT aggregate, aggregate_id, event_type, payload FROM app.outbox_event ORDER BY event_id DESC LIMIT 1`).
			Scan(&aggregate, &aggregateID, &eventType, &payload))
		require.Equal(t, "role", aggregate)
		require.Equal(t, role.RoleID.String(), aggregateID)
		require.Equal(t, string(events.TypeRoleGrantChanged), eventType)
		require.Contains(t, string(payload), "characters.view")
	})

	t.Run("rolling back drops both", func(t *testing.T) {
		grantsBefore := countRoleGrants(t, pool, role.RoleID)
		outboxBefore := countOutbox(t, pool)

		boom := errors.New("the mutation failed after the event was recorded")
		err := events.Transact(ctx, pool, func(ctx context.Context, s *store.Store, out *events.Recorder) error {
			// A real mutation...
			_, err := s.AddRoleGrant(ctx, role.RoleID, "corporations.view", string(rbac.EffectAllow))
			require.NoError(t, err)
			// ...and its announcement, recorded...
			out.Record(events.Event{
				Aggregate: "role", AggregateID: role.RoleID.String(), Type: events.TypeRoleGrantChanged,
				Payload: map[string]any{"change": "added", "permission": "corporations.view"},
			})
			// ...then something goes wrong.
			return boom
		})
		require.ErrorIs(t, err, boom)

		require.Equal(t, grantsBefore, countRoleGrants(t, pool, role.RoleID),
			"the mutation must have rolled back")
		require.Equal(t, outboxBefore, countOutbox(t, pool),
			"the outbox row must have rolled back WITH the mutation — this is the whole of SRS §4.9's guarantee")
	})

	t.Run("a failure publishing the event rolls the mutation back too", func(t *testing.T) {
		// The converse direction: the outbox insert is not a best-effort
		// afterthought that can fail quietly. An event that cannot be
		// enqueued must take the mutation down with it, or the integration
		// silently misses a change that did happen.
		grantsBefore := countRoleGrants(t, pool, role.RoleID)
		outboxBefore := countOutbox(t, pool)

		err := events.Transact(ctx, pool, func(ctx context.Context, s *store.Store, out *events.Recorder) error {
			_, err := s.AddRoleGrant(ctx, role.RoleID, "alliances.view", string(rbac.EffectAllow))
			require.NoError(t, err)
			out.Record(events.Event{
				Aggregate: "role", AggregateID: role.RoleID.String(),
				Type:    events.Type("not.a.known.event"),
				Payload: map[string]any{},
			})
			return nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "closed event vocabulary")

		require.Equal(t, grantsBefore, countRoleGrants(t, pool, role.RoleID))
		require.Equal(t, outboxBefore, countOutbox(t, pool))
	})
}

// ── dispatch ─────────────────────────────────────────────────────────────

// recordingReceiver is a stand-in third party: it records what it was sent
// and verifies the signature the way the documentation tells integrators to
// — against the RAW body, never a re-serialisation.
type recordingReceiver struct {
	mu       sync.Mutex
	secret   []byte
	status   int
	requests []receivedRequest
}

type receivedRequest struct {
	Body       []byte
	Signature  string
	Delivery   string
	Attempt    string
	SigValid   error
	ReceivedAt time.Time
}

func (r *recordingReceiver) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body := make([]byte, 0, 1024)
		buf := make([]byte, 1024)
		for {
			n, err := req.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		sig := req.Header.Get(events.SignatureHeader)

		r.mu.Lock()
		r.requests = append(r.requests, receivedRequest{
			Body: body, Signature: sig,
			Delivery: req.Header.Get("X-Hangar-Delivery"),
			Attempt:  req.Header.Get("X-Hangar-Attempt"),
			SigValid: events.Verify(r.secret, body, sig, time.Now(), events.DefaultReplayWindow),
		})
		status := r.status
		r.mu.Unlock()

		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}
}

func (r *recordingReceiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recordingReceiver) setStatus(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

// TestDispatcherSignsAndDeliversWhatTheOutboxProduced walks the whole path:
// a real RBAC mutation writes an outbox row, the dispatcher fans it out to
// a subscribed endpoint, signs it, and a receiver verifying the way the
// reference script does accepts it.
func TestDispatcherSignsAndDeliversWhatTheOutboxProduced(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)
	kr := testKeyring(t)

	receiver := &recordingReceiver{}
	server := httptest.NewServer(receiver.handler())
	t.Cleanup(server.Close)

	owner := seedUser(t, s)
	_, secret := seedEndpoint(t, s, kr, owner, server.URL, nil)
	receiver.secret = secret

	role, err := s.CreateRole(ctx, "dispatch-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))

	d := &events.Dispatcher{Pool: pool, Keyring: kr, Client: server.Client()}
	result, err := d.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.EventsFannedOut)
	require.Equal(t, 1, result.DeliveriesQueued)
	require.Equal(t, 1, result.Sent)

	require.Equal(t, 1, receiver.count())
	got := receiver.requests[0]
	require.NoError(t, got.SigValid, "the receiver could not verify a signature the dispatcher just produced")
	require.Equal(t, "1", got.Attempt)
	require.NotEmpty(t, got.Delivery)

	var envelope events.Envelope
	require.NoError(t, json.Unmarshal(got.Body, &envelope))
	require.Equal(t, string(events.TypeRoleGrantChanged), envelope.EventType)
	require.Equal(t, "role", envelope.Aggregate)
	require.Equal(t, role.RoleID.String(), envelope.AggregateID)

	// Drained: a second tick must not re-deliver.
	second, err := d.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, second.Sent)
	require.Equal(t, 1, receiver.count())

	pending, err := events.PendingCount(ctx, s)
	require.NoError(t, err)
	require.Zero(t, pending)
}

// TestOutboxSurvivesDispatcherCrashBetweenClaimAndSend is the roadmap's
// "the outbox must not lose an event when the dispatcher crashes between
// claim and send".
//
// A crash is simulated the only way that actually proves anything: the
// delivery is leased (committed), and then nothing settles it — exactly the
// state a process left behind when it died mid-HTTP-call. The event must
// still be delivered once the lease expires, and the delivery id must be
// the SAME one, so a receiver can de-duplicate.
func TestOutboxSurvivesDispatcherCrashBetweenClaimAndSend(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)
	kr := testKeyring(t)

	receiver := &recordingReceiver{}
	server := httptest.NewServer(receiver.handler())
	t.Cleanup(server.Close)

	owner := seedUser(t, s)
	_, secret := seedEndpoint(t, s, kr, owner, server.URL, nil)
	receiver.secret = secret

	role, err := s.CreateRole(ctx, "crash-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))

	// A very short lease so the test does not have to wait a real one out.
	crashed := &events.Dispatcher{Pool: pool, Keyring: kr, Client: server.Client(),
		Policy: events.RetryPolicy{Lease: 750 * time.Millisecond}}

	_, queued, err := crashed.FanOut(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, queued)

	// Claim (which commits the lease) and then "die" — no settle.
	leased, err := s.LeasePendingWebhookDeliveries(ctx, 750*time.Millisecond, 10)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	leasedID := leased[0].DeliveryID
	require.Zero(t, receiver.count(), "nothing was sent — this is the crash window")

	// Inside the lease, another dispatcher must NOT pick it up: that is the
	// half of the guarantee that prevents duplicate delivery.
	inLease, err := s.LeasePendingWebhookDeliveries(ctx, 750*time.Millisecond, 10)
	require.NoError(t, err)
	require.Empty(t, inLease, "a leased delivery must be invisible to a second dispatcher")

	// Once the lease expires it becomes claimable again, and a healthy
	// dispatcher delivers it.
	require.Eventually(t, func() bool {
		result, err := crashed.Deliver(ctx)
		require.NoError(t, err)
		return result.Sent == 1
	}, 10*time.Second, 200*time.Millisecond, "the event was lost when the dispatcher crashed between claim and send")

	require.Equal(t, 1, receiver.count())
	require.Equal(t, leasedID.String(), receiver.requests[0].Delivery,
		"the redelivery must carry the SAME delivery id, or a receiver cannot de-duplicate at-least-once delivery")
	require.NoError(t, receiver.requests[0].SigValid)
}

// TestPermanentlyDownEndpointIsCappedAndDisabled is the roadmap's "an
// endpoint that is permanently down must not retain jobs forever — cap
// attempts and disable the endpoint with an admin notification".
func TestPermanentlyDownEndpointIsCappedAndDisabled(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)
	kr := testKeyring(t)

	receiver := &recordingReceiver{}
	receiver.setStatus(http.StatusInternalServerError) // down, but retryably so
	server := httptest.NewServer(receiver.handler())
	t.Cleanup(server.Close)

	owner := seedUser(t, s)
	endpoint, secret := seedEndpoint(t, s, kr, owner, server.URL, nil)
	receiver.secret = secret

	// Tight policy so the caps are reached in a test-sized number of ticks:
	// 3 attempts per delivery, endpoint disabled after 4 consecutive
	// failures, no backoff wait.
	policy := events.RetryPolicy{MaxAttempts: 3, Base: time.Millisecond, Cap: time.Millisecond,
		MaxConsecutiveFailures: 4, Lease: time.Millisecond}
	d := &events.Dispatcher{Pool: pool, Keyring: kr, Client: server.Client(), Policy: policy}

	role, err := s.CreateRole(ctx, "down-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))
	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "corporations.view", rbac.EffectAllow))

	var disabled bool
	for i := 0; i < 20 && !disabled; i++ {
		result, err := d.Tick(ctx)
		require.NoError(t, err)
		if result.EndpointsDisabled > 0 {
			disabled = true
		}
		time.Sleep(20 * time.Millisecond) // let the millisecond lease/backoff expire
	}
	require.True(t, disabled, "a permanently failing endpoint must eventually be disabled")

	row, err := s.GetWebhookEndpoint(ctx, endpoint.EndpointID)
	require.NoError(t, err)
	require.False(t, row.Enabled)
	require.NotNil(t, row.DisabledAt, "an auto-disable must be distinguishable from the owner switching it off")
	require.NotNil(t, row.DisabledReason)
	require.Contains(t, *row.DisabledReason, "consecutive delivery failures")

	// The admin notification.
	var (
		action string
		detail json.RawMessage
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT action, detail FROM app.security_log WHERE action = $1 ORDER BY at DESC LIMIT 1`,
		events.SecurityLogAction).Scan(&action, &detail))
	require.Equal(t, events.SecurityLogAction, action)
	require.Contains(t, string(detail), server.URL)

	// And the queue stops growing without bound: once disabled, no further
	// deliveries are attempted at all.
	sentBefore := receiver.count()
	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "alliances.view", rbac.EffectAllow))
	for i := 0; i < 3; i++ {
		_, err := d.Tick(ctx)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, sentBefore, receiver.count(),
		"a disabled endpoint must stop consuming attempts, or 'permanently down' still costs an HTTP call per event forever")

	// NOTHING is left pending against the disabled endpoint. This is the
	// literal reading of "must not retain jobs forever": a disabled
	// endpoint's queue is unclaimable, so a delivery left in 'pending'
	// would be retained indefinitely in a state neither the pump nor the
	// board can see. Every one of them must be visibly dead-lettered.
	var stillPending int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM app.webhook_delivery WHERE endpoint_id = $1 AND delivered_at IS NULL AND failed_at IS NULL`,
		endpoint.EndpointID).Scan(&stillPending))
	require.Zero(t, stillPending,
		"a disabled endpoint must not retain queued jobs — they are unclaimable, so 'pending' means 'lost and invisible'")

	board, err := events.DeadLetterBoard(ctx, s, 100)
	require.NoError(t, err)
	require.NotEmpty(t, board)
	for _, entry := range board {
		require.NotNil(t, entry.FailedAt)
		require.NotNil(t, entry.Error)
	}
}

// TestSuccessfulDeliveryResetsTheEndpointBreaker — "consecutive" has to
// mean consecutive, or a long-lived healthy endpoint accumulates unrelated
// transient failures until it is disabled while working perfectly.
func TestSuccessfulDeliveryResetsTheEndpointBreaker(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)
	kr := testKeyring(t)

	receiver := &recordingReceiver{}
	server := httptest.NewServer(receiver.handler())
	t.Cleanup(server.Close)

	owner := seedUser(t, s)
	endpoint, secret := seedEndpoint(t, s, kr, owner, server.URL, nil)
	receiver.secret = secret

	policy := events.RetryPolicy{MaxAttempts: 5, Base: time.Millisecond, Cap: time.Millisecond,
		MaxConsecutiveFailures: 3, Lease: time.Millisecond}
	d := &events.Dispatcher{Pool: pool, Keyring: kr, Client: server.Client(), Policy: policy}

	role, err := s.CreateRole(ctx, "breaker-"+uuid.NewString(), nil, false)
	require.NoError(t, err)

	receiver.setStatus(http.StatusInternalServerError)
	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))
	for i := 0; i < 2; i++ {
		_, err := d.Tick(ctx)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
	}
	var failures int32
	require.NoError(t, pool.QueryRow(ctx, `SELECT consecutive_failures FROM app.webhook_endpoint WHERE endpoint_id = $1`,
		endpoint.EndpointID).Scan(&failures))
	require.Positive(t, failures)

	receiver.setStatus(http.StatusOK)
	require.Eventually(t, func() bool {
		result, err := d.Tick(ctx)
		require.NoError(t, err)
		return result.Sent > 0
	}, 10*time.Second, 100*time.Millisecond)

	require.NoError(t, pool.QueryRow(ctx, `SELECT consecutive_failures FROM app.webhook_endpoint WHERE endpoint_id = $1`,
		endpoint.EndpointID).Scan(&failures))
	require.Zero(t, failures, "a success must clear the breaker")

	row, err := s.GetWebhookEndpoint(ctx, endpoint.EndpointID)
	require.NoError(t, err)
	require.True(t, row.Enabled)
}

// TestClientErrorDeadLettersImmediately — a 4xx is the receiver's settled
// opinion; spending five attempts re-proving it is noise. 429/408/425 are
// the documented exceptions, because they are explicit "try again" answers
// that merely happen to live in the 4xx range.
func TestClientErrorDeadLettersImmediately(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)
	kr := testKeyring(t)

	for _, tc := range []struct {
		name         string
		status       int
		wantDeadFast bool
	}{
		{"400 dead-letters on the first attempt", http.StatusBadRequest, true},
		{"404 dead-letters on the first attempt", http.StatusNotFound, true},
		{"429 is retried, not dead-lettered", http.StatusTooManyRequests, false},
		{"408 is retried, not dead-lettered", http.StatusRequestTimeout, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &recordingReceiver{}
			receiver.setStatus(tc.status)
			server := httptest.NewServer(receiver.handler())
			t.Cleanup(server.Close)

			owner := seedUser(t, s)
			endpoint, secret := seedEndpoint(t, s, kr, owner, server.URL, nil)
			receiver.secret = secret
			// Each case's endpoint is switched off at the end of its own
			// subtest. Without this, the previous case's endpoint is still
			// enabled while its httptest server has been closed, so the NEXT
			// case's event fans out to a refused connection and its transport
			// error lands in the shared Tick counters. Asserting on
			// per-endpoint rows below makes the assertions robust to that;
			// this keeps the fan-out itself honest as well.
			t.Cleanup(func() { require.NoError(t, s.RevokeWebhookEndpoint(ctx, endpoint.EndpointID)) })

			role, err := s.CreateRole(ctx, fmt.Sprintf("status-%d-%s", tc.status, uuid.NewString()), nil, false)
			require.NoError(t, err)

			d := &events.Dispatcher{Pool: pool, Keyring: kr, Client: server.Client(),
				Policy: events.RetryPolicy{MaxAttempts: 5, Base: time.Hour, Lease: time.Millisecond}}

			require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))
			_, err = d.Tick(ctx)
			require.NoError(t, err)

			// Asserted on THIS endpoint's delivery rows, not on Tick's
			// aggregate counters, which necessarily include every other
			// endpoint the same fan-out reached.
			var (
				attempt     int32
				failedAt    *time.Time
				nextRetryAt *time.Time
			)
			require.NoError(t, pool.QueryRow(ctx,
				`SELECT attempt, failed_at, next_retry_at FROM app.webhook_delivery WHERE endpoint_id = $1`,
				endpoint.EndpointID).Scan(&attempt, &failedAt, &nextRetryAt))

			require.Equal(t, int32(1), attempt, "exactly one attempt should have been made")
			if tc.wantDeadFast {
				require.NotNil(t, failedAt, "a %d must dead-letter immediately, not consume the whole attempt budget", tc.status)
			} else {
				require.Nil(t, failedAt, "a %d is an explicit 'try again', not a permanent refusal", tc.status)
				require.NotNil(t, nextRetryAt)
			}
		})
	}
}

// TestEventFilterSelectsEndpoints — an endpoint with an empty filter gets
// everything; one with a filter gets only what it asked for. The empty case
// is spelled out in SQL rather than left to the array operator, and this is
// what checks that.
func TestEventFilterSelectsEndpoints(t *testing.T) {
	ctx := context.Background()
	pool := newMigratedPool(t)
	s := store.New(pool)
	kr := testKeyring(t)

	all := &recordingReceiver{}
	allServer := httptest.NewServer(all.handler())
	t.Cleanup(allServer.Close)
	selective := &recordingReceiver{}
	selectiveServer := httptest.NewServer(selective.handler())
	t.Cleanup(selectiveServer.Close)

	owner := seedUser(t, s)
	_, allSecret := seedEndpoint(t, s, kr, owner, allServer.URL, nil)
	all.secret = allSecret
	_, selSecret := seedEndpoint(t, s, kr, owner, selectiveServer.URL, []string{string(events.TypeUserRoleAssigned)})
	selective.secret = selSecret

	role, err := s.CreateRole(ctx, "filter-"+uuid.NewString(), nil, false)
	require.NoError(t, err)
	user := seedUser(t, s)

	d := &events.Dispatcher{Pool: pool, Keyring: kr, Client: allServer.Client()}

	require.NoError(t, rbac.AddRoleGrant(ctx, pool, role.RoleID, "characters.view", rbac.EffectAllow))
	_, err = d.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, all.count(), "an empty event_filter means everything")
	require.Zero(t, selective.count(), "a filtered endpoint must not receive a type it did not subscribe to")

	require.NoError(t, rbac.AssignUserRole(ctx, pool, user, role.RoleID, uuid.NullUUID{}))
	_, err = d.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, all.count())
	require.Equal(t, 1, selective.count(), "the filtered endpoint must receive the type it subscribed to")
}

// TestWebhookSecretAADBindsEndpoint — the §4.6 AAD binding, asserted the
// only way that means anything: move a sealed secret to a different
// endpoint id and it must fail to open rather than yield a plausible key.
func TestWebhookSecretAADBindsEndpoint(t *testing.T) {
	kr := testKeyring(t)
	secret, err := crypto.NewWebhookSecret()
	require.NoError(t, err)

	mine, other := uuid.New(), uuid.New()
	sealed, err := crypto.SealWebhookSecret(kr, mine, secret)
	require.NoError(t, err)

	opened, err := crypto.OpenWebhookSecret(kr, mine, sealed)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(secret), hex.EncodeToString(opened))

	_, err = crypto.OpenWebhookSecret(kr, other, sealed)
	require.Error(t, err, "a secret moved to another endpoint must not decrypt")
	require.Contains(t, err.Error(), "authentication failed")
}
