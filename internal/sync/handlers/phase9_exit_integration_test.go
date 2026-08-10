//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/alerting/render"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

const corpFixtureDir9 = "../../../testdata/esi/corporation"
const notificationsFixtureDir = "../../../testdata/notifications"

func seedCorp9(t *testing.T, s *store.Store, corporationID int64) {
	t.Helper()
	_, err := s.UpsertCorporation(context.Background(), gen.UpsertCorporationParams{
		CorporationID: corporationID, Name: "Test Corp " + uuid.NewString(), Ticker: "TST9",
	})
	require.NoError(t, err)
}

// TestAssetReconciliationSoftDeletesMissing (roadmap exit criterion):
// items missing from a full sync are soft-deleted, never DELETEd; a
// reappearing item_id is restored, never re-inserted as a new row.
func TestAssetReconciliationSoftDeletesMissing(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const characterID int64 = 90000101
	seedCharacter(t, s, characterID)

	assets, err := handlers.ParseAssets(mustReadFixture(t, "assets.json"))
	require.NoError(t, err)
	require.Len(t, assets, 3)

	_, err = handlers.SyncAssets(ctx, s, "character", characterID, assets, nil)
	require.NoError(t, err)

	missing := assets[0]
	kept := assets[1:]

	// A sync page that no longer contains `missing` soft-deletes it.
	_, err = handlers.SyncAssets(ctx, s, "character", characterID, kept, nil)
	require.NoError(t, err)

	row, err := s.GetAsset(ctx, "character", characterID, missing.ItemID)
	require.NoError(t, err)
	require.NotNil(t, row.DeletedAt, "an item absent from the latest sync must be soft-deleted, not left alone")

	list, err := s.ListAssetsByOwner(ctx, gen.ListAssetsByOwnerParams{
		OwnerKind: "character", OwnerID: characterID, AfterItemID: 0, PageSize: 100,
	})
	require.NoError(t, err)
	for _, a := range list {
		require.NotEqual(t, missing.ItemID, a.ItemID, "a soft-deleted asset must not appear in the active list")
	}

	// The item reappears in a later sync: it must be RESTORED (same PK),
	// never re-inserted as a fresh row.
	_, err = handlers.SyncAssets(ctx, s, "character", characterID, assets, nil)
	require.NoError(t, err)

	restored, err := s.GetAsset(ctx, "character", characterID, missing.ItemID)
	require.NoError(t, err)
	require.Nil(t, restored.DeletedAt, "a reappearing item must be restored (deleted_at cleared)")
	require.Equal(t, missing.ItemID, restored.ItemID, "the restored row must be the SAME item_id, not a new surrogate row")

	// Never a hard delete: exactly 3 rows physically exist regardless of
	// deleted_at, across both syncs above.
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.asset WHERE owner_kind='character' AND owner_id=$1`, characterID).Scan(&count))
	require.Equal(t, 3, count, "reconciliation must never physically DELETE a row")
}

// TestUnparseableNotificationYAMLImportsAsJSONB (roadmap exit criterion):
// a malformed fixture imports, renders generically, and does not halt the
// queue — i.e. a batch containing one bad payload alongside good ones
// still syncs every row.
func TestUnparseableNotificationYAMLImportsAsJSONB(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const characterID int64 = 90000102
	seedCharacter(t, s, characterID)

	validYAML, err := os.ReadFile(filepath.Join(notificationsFixtureDir, "valid_structure_under_attack.yaml"))
	require.NoError(t, err)
	invalidYAML, err := os.ReadFile(filepath.Join(notificationsFixtureDir, "invalid_unquoted_value.yaml"))
	require.NoError(t, err)

	notifications := []handlers.CharacterNotificationDTO{
		{NotificationID: 1, SenderID: 1000137, SenderType: "corporation", Type: "StructureUnderAttack", Text: string(validYAML)},
		{NotificationID: 2, SenderID: 1000137, SenderType: "corporation", Type: "SomeUnrecognizedType", Text: string(invalidYAML)},
		{NotificationID: 3, SenderID: 1000137, SenderType: "corporation", Type: "StructureUnderAttack", Text: string(validYAML)},
	}

	// The whole batch must succeed — one bad payload must not halt the
	// sync queue (Principle 14's most operationally important form).
	res, err := handlers.SyncCharacterNotifications(ctx, s, characterID, notifications)
	require.NoError(t, err)
	require.EqualValues(t, 3, res.RowsAffected)

	good, err := s.ListCharacterNotificationsPage(ctx, characterID, notifications[0].Timestamp.AddDate(1, 0, 0), 10)
	require.NoError(t, err)
	require.Len(t, good, 3)

	bad, err := s.ListUnparseableCharacterNotifications(ctx, characterID)
	require.NoError(t, err)
	require.Len(t, bad, 1, "exactly the one malformed-YAML notification must be flagged")
	require.EqualValues(t, 2, bad[0].NotificationID)
	require.True(t, bad[0].ParseFailed)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(bad[0].Payload, &payload))
	require.Contains(t, payload, "raw", "an unparseable payload must fall back to a {\"raw\": ...} wrapper")

	// The generic renderer must produce SOMETHING for both the parsed and
	// the fallback payload, never panic or error.
	rendered := render.Generic(bad[0].Payload)
	require.NotEmpty(t, rendered)
	require.Contains(t, rendered, "raw:")

	unknown, err := s.ListUnacknowledgedNotificationTypes(ctx)
	require.NoError(t, err)
	found := false
	for _, u := range unknown {
		if u.Type == "SomeUnrecognizedType" {
			found = true
		}
	}
	require.True(t, found, "an unparseable notification must land on the unknown-types board")
}

// TestContractItemsMailBodiesColonyDetailRoundTrip (roadmap exit
// criterion): each of contract items, mail bodies, and PI colony detail
// round-trips from a recorded fixture.
func TestContractItemsMailBodiesColonyDetailRoundTrip(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const characterID int64 = 90000103
	seedCharacter(t, s, characterID)

	t.Run("contract items (including the courier empty-by-design case)", func(t *testing.T) {
		contracts, err := handlers.ParseContracts(mustReadFixture(t, "contracts.json"))
		require.NoError(t, err)
		_, err = handlers.SyncContracts(ctx, s, "character", characterID, contracts)
		require.NoError(t, err)

		items, err := handlers.ParseContractItems(mustReadFixture(t, "contract_items.json"))
		require.NoError(t, err)
		res, err := handlers.SyncContractItems(ctx, s, "character", characterID, 5001, items)
		require.NoError(t, err)
		require.EqualValues(t, 2, res.RowsAffected)

		roundTripped, err := s.ListContractItems(ctx, "character", characterID, 5001)
		require.NoError(t, err)
		require.Len(t, roundTripped, 2)
		require.Equal(t, items[0].TypeID, roundTripped[0].TypeID)
		require.Equal(t, items[0].Quantity, roundTripped[0].Quantity)

		courierItems, err := handlers.ParseContractItems(mustReadFixture(t, "contract_items_courier.json"))
		require.NoError(t, err)
		require.Empty(t, courierItems, "fixture represents a courier contract's empty-by-design item list")
		res2, err := handlers.SyncContractItems(ctx, s, "character", characterID, 5002, courierItems)
		require.NoError(t, err, "an empty items list must sync successfully, not be treated as a failure")
		require.EqualValues(t, 0, res2.RowsAffected)
	})

	t.Run("mail body", func(t *testing.T) {
		headers, err := handlers.ParseMailHeaders(mustReadFixture(t, "mail_headers.json"))
		require.NoError(t, err)
		_, err = handlers.SyncMailHeaders(ctx, s, characterID, headers)
		require.NoError(t, err)

		body, err := handlers.ParseMailBody(mustReadFixture(t, "mail_body.json"))
		require.NoError(t, err)
		_, err = handlers.SyncMailBody(ctx, s, characterID, 7001, body)
		require.NoError(t, err)

		roundTripped, err := s.GetMailBody(ctx, characterID, 7001)
		require.NoError(t, err)
		require.Equal(t, body.Body, roundTripped.Body)
	})

	t.Run("PI colony detail", func(t *testing.T) {
		_, err := s.UpsertPlanetColony(ctx, gen.UpsertPlanetColonyParams{
			CharacterID: characterID, PlanetID: 40000001, SolarSystemID: 30000142, PlanetType: "barren",
			OwnerID: characterID, UpgradeLevel: 1, NumPins: 2,
		})
		require.NoError(t, err)

		detail, err := handlers.ParsePlanetColonyDetail(mustReadFixture(t, "planet_colony_detail.json"))
		require.NoError(t, err)
		_, err = handlers.SyncPlanetColonyDetail(ctx, s, characterID, 40000001, detail)
		require.NoError(t, err)

		roundTripped, err := s.GetPlanetColonyDetail(ctx, characterID, 40000001)
		require.NoError(t, err)
		var pins []json.RawMessage
		require.NoError(t, json.Unmarshal(roundTripped.Pins, &pins))
		require.Len(t, pins, 2)
	})
}

// TestUUIDKeyedProjectInsertAndJoin (roadmap exit criterion): uuid PK
// inserts and joins against bigint character_id without coercion.
func TestUUIDKeyedProjectInsertAndJoin(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const corporationID int64 = 98000101
	const characterID int64 = 90000001 // must match testdata/esi/corporation/project_contributions.json's character_id
	seedCorp9(t, s, corporationID)
	seedCharacter(t, s, characterID)

	projects, err := handlers.ParseCorporationProjects(mustReadCorpFixture9(t, "projects.json"))
	require.NoError(t, err)
	require.Len(t, projects, 1)
	projectID := projects[0].ProjectID
	require.NotEqual(t, uuid.Nil, projectID)

	_, err = handlers.SyncCorporationProjects(ctx, s, corporationID, projects)
	require.NoError(t, err)

	fetched, err := s.GetCorporationProject(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, projectID, fetched.ProjectID, "project_id must round-trip as the same uuid.UUID, never coerced through text or bigint")

	contributions, err := handlers.ParseCorporationProjectContributions(mustReadCorpFixture9(t, "project_contributions.json"))
	require.NoError(t, err)
	_, err = handlers.SyncCorporationProjectContributions(ctx, s, projectID, contributions)
	require.NoError(t, err)

	// The Gate 6 fixture row itself: a uuid PK joining directly against a
	// bigint character_id in the same row.
	contribution, err := s.GetCorporationProjectContribution(ctx, projectID, characterID)
	require.NoError(t, err)
	require.Equal(t, projectID, contribution.ProjectID)
	require.Equal(t, characterID, contribution.CharacterID)
	require.True(t, decimal.NewFromFloat(250.00).Equal(contribution.Amount))
}

// TestMarketOrderIssuedByPersists (new, Phase 9's carry-over fix from
// Phase 8): issued_by round-trips from a recorded fixture into
// app.market_order.issued_by and app.market_order_history.issued_by, for
// both owner kinds.
func TestMarketOrderIssuedByPersists(t *testing.T) {
	pool := newMigratedPool(t)
	ctx := context.Background()
	s := store.New(pool)
	const characterID int64 = 90000105
	const corporationID int64 = 98000102
	seedCharacter(t, s, characterID)
	seedCorp9(t, s, corporationID)

	t.Run("character orders", func(t *testing.T) {
		orders, err := handlers.ParseMarketOrders(mustReadFixture(t, "orders.json"))
		require.NoError(t, err)
		require.NotNil(t, orders[0].IssuedBy)
		_, err = handlers.SyncMarketOrders(ctx, s, "character", characterID, false, orders)
		require.NoError(t, err)

		var issuedBy *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT issued_by FROM app.market_order WHERE owner_kind='character' AND owner_id=$1 AND order_id=$2`, characterID, orders[0].OrderID).Scan(&issuedBy))
		require.NotNil(t, issuedBy)
		require.Equal(t, *orders[0].IssuedBy, *issuedBy)
	})

	t.Run("character order history", func(t *testing.T) {
		history, err := handlers.ParseMarketOrderHistory(mustReadFixture(t, "orders_history.json"))
		require.NoError(t, err)
		require.NotNil(t, history[0].IssuedBy)
		_, err = handlers.SyncMarketOrderHistory(ctx, s, "character", characterID, false, history)
		require.NoError(t, err)

		var issuedBy *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT issued_by FROM app.market_order_history WHERE owner_kind='character' AND owner_id=$1 AND order_id=$2`, characterID, history[0].OrderID).Scan(&issuedBy))
		require.NotNil(t, issuedBy)
		require.Equal(t, *history[0].IssuedBy, *issuedBy)
	})

	t.Run("corporation orders", func(t *testing.T) {
		orders, err := handlers.ParseMarketOrders(mustReadCorpFixture9(t, "market_orders.json"))
		require.NoError(t, err)
		require.NotNil(t, orders[0].IssuedBy)
		_, err = handlers.SyncMarketOrders(ctx, s, "corporation", corporationID, true, orders)
		require.NoError(t, err)

		var issuedBy *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT issued_by FROM app.market_order WHERE owner_kind='corporation' AND owner_id=$1 AND order_id=$2`, corporationID, orders[0].OrderID).Scan(&issuedBy))
		require.NotNil(t, issuedBy)
		require.Equal(t, *orders[0].IssuedBy, *issuedBy)
	})

	t.Run("corporation order history", func(t *testing.T) {
		history, err := handlers.ParseMarketOrderHistory(mustReadCorpFixture9(t, "market_order_history.json"))
		require.NoError(t, err)
		require.NotNil(t, history[0].IssuedBy)
		_, err = handlers.SyncMarketOrderHistory(ctx, s, "corporation", corporationID, true, history)
		require.NoError(t, err)

		var issuedBy *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT issued_by FROM app.market_order_history WHERE owner_kind='corporation' AND owner_id=$1 AND order_id=$2`, corporationID, history[0].OrderID).Scan(&issuedBy))
		require.NotNil(t, issuedBy)
		require.Equal(t, *history[0].IssuedBy, *issuedBy)
	})
}

func mustReadCorpFixture9(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpFixtureDir9, name))
	require.NoError(t, err)
	return b
}
