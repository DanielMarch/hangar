package handlers_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/sync/handlers"
)

// ── DEFECT B38's LEFTOVER, PINNED ────────────────────────────────────────
//
// The route was parsed with the DTO of `.../contributions`, a path ESI has
// never had. These tests assert the three facts that were wrong, against the
// live spec's `CorporationsProjectsContributors` schema:
//
//	1. the body is an OBJECT of {contributors, cursor}, not a bare array;
//	2. `id` is the contributor's CHARACTER id ($ref CharacterID);
//	3. `contributed` is int64 PROGRESS, and it is what lands in
//	   app.corporation_project_contribution.amount.

func TestContributorsIsAnEnvelopeNotABareArray(t *testing.T) {
	got, err := handlers.ParseCorporationProjectContributors([]byte(
		`{"contributors":[{"id":90000001,"name":"Contributor Name","contributed":250}],"cursor":{"after":"x"}}`))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.EqualValues(t, 90000001, got[0].ID)
	require.EqualValues(t, 250, got[0].Contributed)
	require.Equal(t, "Contributor Name", got[0].Name)
}

// TestTheOldContributionsShapeNoLongerParses is the regression the defect
// itself could not produce: the DTO that WAS attached to this route accepted
// a bare array and would have silently produced zero contributors from a
// real body. Now a bare array is a parse error, which is what a wrong shape
// should be.
func TestTheOldContributionsShapeNoLongerParses(t *testing.T) {
	_, err := handlers.ParseCorporationProjectContributors([]byte(`[{"amount":250.00,"character_id":90000001}]`))
	require.Error(t, err)
}

// TestAbsentContributorsFieldIsEmptyNotAnError — the schema makes
// `contributors` required, but an empty project is a real state and an
// envelope with no array must not fail a whole sync pass.
func TestAbsentContributorsFieldIsEmptyNotAnError(t *testing.T) {
	got, err := handlers.ParseCorporationProjectContributors([]byte(`{"cursor":{}}`))
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestContributedIsAnIntegerNotMoney guards the mapping decision. ESI
// declares `contributed` as format int64 and describes it as contributed
// PROGRESS; the project's isk field is reward_isk. A future change that
// reads it as a decimal string would compile and be wrong.
func TestContributedIsAnIntegerNotMoney(t *testing.T) {
	_, err := handlers.ParseCorporationProjectContributors([]byte(
		`{"contributors":[{"id":1,"name":"n","contributed":"250.00"}]}`))
	require.Error(t, err, "contributed is int64 in the spec; a quoted decimal must not silently coerce")
}
