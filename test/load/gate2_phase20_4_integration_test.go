//go:build integration

package load_test

// Phase 20.4 closes the two Gate 2 trigger-matrix rows Phase 20.3 found
// open and deliberately did not close: row 6 ("Corporation / alliance
// departure", which had no producer at all) and row 8 ("Admin platform
// lockdown", which was a specification question).
//
// Both are tested HERE, beside gate2_integration_test.go, because both are
// statements about §2.3's matrix rather than about any one package: row 6
// spans internal/sync/handlers and internal/provisioning through a seam
// neither imports across, and row 8 turned out to span the API handler,
// both worker paths and a column nobody read.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/test/load"
)

// ── ROW 6: CORPORATION / ALLIANCE DEPARTURE ──────────────────────────────

// TestCorporationDepartureEnqueuesAnUrgentRevocation is §2.3 row 6, which
// had NO PRODUCER before this phase: `grep -rln provisioning internal/sync`
// returned nothing, so a character who left the corporation that granted
// their Discord roles kept them until the next NIGHTLY bulk reconcile,
// against §2.1's 60-second p99 bound.
//
// It drives the REAL sync handler — the one a character-sheet job calls —
// rather than firing the hook directly, because the defect was never in the
// hook: it was that nothing invoked one.
func TestCorporationDepartureEnqueuesAnUrgentRevocation(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()
	s := store.New(pool)

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	urgent := &provisioning.Urgent{River: client}

	// The seam, wired exactly as cmd/hangar/revocation.go wires it.
	handlers.AffiliationChangedHook = func(ctx context.Context, change handlers.AffiliationChange) error {
		reason := "alliance_departure"
		if change.CorporationChanged() {
			reason = "corporation_departure"
		}
		return urgent.HandleCharacterChange(ctx, pool, change.CharacterID, reason)
	}
	t.Cleanup(func() { handlers.AffiliationChangedHook = nil })

	// A user linked to a platform, holding a group, with a character in the
	// corporation they are about to leave.
	user, err := s.CreateUser(ctx, "Departing User")
	require.NoError(t, err)
	platform, err := s.CreatePlatform(ctx, "discord", "Row6 Platform "+uuid.NewString(), []byte(`{}`))
	require.NoError(t, err)
	_, err = s.CreatePlatformGroup(ctx, platform.PlatformID, "role-9", "Row6 Group")
	require.NoError(t, err)
	identity := "remote-" + uuid.NewString()
	now := time.Now()
	require.NoError(t, s.UpsertProvisioningState(ctx, gen.UpsertProvisioningStateParams{
		PlatformID: platform.PlatformID, UserID: user.UserID, RemoteIdentity: &identity,
		DesiredGroups: []string{"role-9"}, ActualGroups: []string{"role-9"},
		LinkedAt: &now, LastReconciledAt: &now,
	}))

	const characterID = int64(2124613505)
	const oldCorporation = int64(98000001)
	const newCorporation = int64(98000002)
	for _, corp := range []int64{oldCorporation, newCorporation} {
		_, err := pool.Exec(ctx,
			`INSERT INTO app.corporation (corporation_id, name, ticker) VALUES ($1, 'Corp', 'C')
			 ON CONFLICT (corporation_id) DO NOTHING`, corp)
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app.character (character_id, name, owner_hash, user_id, corporation_id)
		VALUES ($1, 'Departing Pilot', 'hash', $2, $3)`, characterID, user.UserID, oldCorporation)
	require.NoError(t, err)

	auditsBefore := countRevocationAudits(t, pool)

	// The character-sheet sync that observes the departure.
	_, err = handlers.SyncCharacterSheet(ctx, s, characterID, handlers.CharacterSheetDTO{
		CorporationID: newCorporation, Name: "Departing Pilot", Gender: "male",
		RaceID: 1, BloodlineID: 1, Birthday: time.Now().Add(-24 * time.Hour),
	})
	require.NoError(t, err)

	require.Greater(t, countRevocationAudits(t, pool), auditsBefore,
		"leaving a corporation must enqueue an urgent revocation — the entitlement that corporation "+
			"granted is now unearned, and waiting for the nightly bulk reconcile is §2.1's whole point")

	var reason string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT reason FROM app.provisioning_audit WHERE action = 'revoke' ORDER BY event_at DESC LIMIT 1`).
		Scan(&reason))
	require.Equal(t, "corporation_departure", reason,
		"the audit must name which move happened, so an operator need not join back to character history")
}

// TestFirstSightingOfACharacterIsNotADeparture is the other half, and it
// matters more than it looks: every character HANGAR has ever seen gets a
// first character-sheet sync, and treating that as a corporation change
// would enqueue an urgent revocation for every character on every fresh
// installation — a self-inflicted revocation storm at the worst possible
// moment.
func TestFirstSightingOfACharacterIsNotADeparture(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()
	s := store.New(pool)

	fired := 0
	handlers.AffiliationChangedHook = func(context.Context, handlers.AffiliationChange) error {
		fired++
		return nil
	}
	t.Cleanup(func() { handlers.AffiliationChangedHook = nil })

	const characterID = int64(2124613599)
	_, err := pool.Exec(ctx,
		`INSERT INTO app.corporation (corporation_id, name, ticker) VALUES (98000003, 'Corp', 'C')`)
	require.NoError(t, err)
	// No corporation_id: HANGAR has never recorded an affiliation for this
	// character, which is what a Phase 5 SSO-callback row looks like.
	_, err = pool.Exec(ctx,
		`INSERT INTO app.character (character_id, name, owner_hash) VALUES ($1, 'New Pilot', 'hash')`, characterID)
	require.NoError(t, err)

	_, err = handlers.SyncCharacterSheet(ctx, s, characterID, handlers.CharacterSheetDTO{
		CorporationID: 98000003, Name: "New Pilot", Gender: "male",
		RaceID: 1, BloodlineID: 1, Birthday: time.Now().Add(-24 * time.Hour),
	})
	require.NoError(t, err)
	require.Zero(t, fired, "a first sighting is not a departure: nothing was granted on an affiliation nobody knew")

	// And a re-sync of the SAME affiliation is not a departure either.
	_, err = handlers.SyncCharacterSheet(ctx, s, characterID, handlers.CharacterSheetDTO{
		CorporationID: 98000003, Name: "New Pilot", Gender: "male", SecurityStatus: float64p(1.5),
		RaceID: 1, BloodlineID: 1, Birthday: time.Now().Add(-24 * time.Hour),
	})
	require.NoError(t, err)
	require.Zero(t, fired, "a sheet update that changed a security status is not an affiliation change")
}

func float64p(f float64) *float64 { return &f }

// ── ROW 8: ADMIN PLATFORM LOCKDOWN ───────────────────────────────────────

// TestLockdownActuallyStopsOutboundProvisioning is the finding Phase 20.4
// made while settling row 8's specification question, and it is larger than
// the question was.
//
// Phase 15.1 added app.platform.locked_down, the endpoint that writes it,
// the audit entry, and the admin UI badge. NOTHING READ IT.
// ListEnabledPlatforms filters on `enabled` alone; UrgentWorker.Work looked
// the driver up and called it. An administrator freezing a compromised
// integration during an incident got an audit trail saying they had frozen
// it and a platform that carried on being written to.
func TestLockdownActuallyStopsOutboundProvisioning(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()

	w := seedGate2World(t, pool, 3)
	require.NoError(t, freezePlatform(t, pool, w.platformID))

	drivers := provisioning.NewDrivers()
	stub := load.NewRateLimitedStub(load.DiscordLimits)
	drivers.Register(w.platformID.String(), stub)

	runUrgentRevocations(t, w, drivers, time.Now().Add(-5*time.Second))

	require.Zero(t, revokedCount(stub),
		"a frozen platform must receive no outbound calls at all — that is what the freeze IS")

	outcomes := auditOutcomes(t, pool)
	require.Equal(t, 3, outcomes[provisioning.OutcomeSkippedLockedDown],
		"every skipped revocation must SAY it was skipped because of the freeze, rather than being "+
			"recorded as a success that did nothing")

	// The exposure survives with its groups intact: nothing was revoked, so
	// actual_groups must still hold the group and the exposure board must
	// still show it. §2.4 condition 2.3, for a platform that is down on
	// purpose.
	var stillHeld int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM app.provisioning_state
		 WHERE platform_id = $1 AND 'role-1' = ANY (actual_groups)`, w.platformID).Scan(&stillHeld))
	require.Equal(t, 3, stillHeld,
		"a freeze must not pretend the groups were removed — the exposure is real and stays visible")
}

// TestUnfreezingReconcilesWhatTheFreezeDeferred is row 8's settlement:
// LOCKING enqueues nothing (freezing is not revoking), UNLOCKING enqueues a
// full platform reconcile, because everything that changed during the
// freeze is owed the moment it lifts.
//
// The API handler is not exercised here — that is internal/api/v1's suite —
// but the enqueue it performs is, since the enqueue is the part §2.3's
// matrix is actually about.
func TestUnfreezingReconcilesWhatTheFreezeDeferred(t *testing.T) {
	pool := newGate2Pool(t)
	ctx := context.Background()
	w := seedGate2World(t, pool, 1)

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	urgent := &provisioning.Urgent{River: client}

	require.NoError(t, urgent.EnqueuePlatformReconcile(ctx, w.platformID))

	var queued int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'platform_id' = $2`,
		provisioning.KindProvisionBulk, w.platformID.String()).Scan(&queued))
	require.Equal(t, 1, queued,
		"unfreezing must queue the catch-up reconcile — an operator who has just ended an incident "+
			"must not wait for the nightly pass to learn whether their platform is correct again")
}

// TestThawedPlatformProvisionsAgain closes the loop: the freeze is a
// SUSPENSION, not a permanent state, and lifting it must restore outbound
// provisioning with no further intervention.
func TestThawedPlatformProvisionsAgain(t *testing.T) {
	pool := newGate2Pool(t)
	w := seedGate2World(t, pool, 2)

	drivers := provisioning.NewDrivers()
	stub := load.NewRateLimitedStub(load.DiscordLimits)
	drivers.Register(w.platformID.String(), stub)

	require.NoError(t, freezePlatform(t, pool, w.platformID))
	runUrgentRevocations(t, w, drivers, time.Now().Add(-5*time.Second))
	require.Zero(t, revokedCount(stub))

	require.NoError(t, thawPlatform(t, pool, w.platformID))
	runUrgentRevocations(t, w, drivers, time.Now().Add(-5*time.Second))
	require.Equal(t, 2, revokedCount(stub),
		"a thawed platform must provision again — a freeze that cannot be lifted is not a freeze")
}

func freezePlatform(t testing.TB, pool *pgxpool.Pool, platformID uuid.UUID) error {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE app.platform SET locked_down = true, locked_down_at = now(), lockdown_reason = 'incident'
		  WHERE platform_id = $1`, platformID)
	return err
}

func thawPlatform(t testing.TB, pool *pgxpool.Pool, platformID uuid.UUID) error {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE app.platform SET locked_down = false, locked_down_at = NULL WHERE platform_id = $1`, platformID)
	return err
}

func countRevocationAudits(t testing.TB, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM app.provisioning_audit WHERE action = 'revoke'`).Scan(&n))
	return n
}

func auditOutcomes(t testing.TB, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT coalesce(outcome, 'unset'), count(*) FROM app.provisioning_audit GROUP BY 1`)
	require.NoError(t, err)
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var outcome string
		var n int
		require.NoError(t, rows.Scan(&outcome, &n))
		out[outcome] = n
	}
	require.NoError(t, rows.Err())
	return out
}

// revokedCount reads the stub's revoke tally.
func revokedCount(stub *load.RateLimitedStub) int {
	_, revokes := stub.Counts()
	return revokes
}
