package api

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/api/filters"
)

// ── DEFECT B33's REGRESSION GUARD ────────────────────────────────────────
//
// What a hostile filter did before this phase: nothing at all. huma binds
// declared parameters and ignores the rest, so an undeclared one produced a
// 200 with the whole collection. These tests pin the three answers that are
// now possible — accepted, refused as unknown, refused as type-confused —
// and the one that must never come back.

type pagedIn struct {
	ID     int64  `path:"id"`
	After  string `query:"after"`
	Before string `query:"before"`
	Limit  int32  `query:"limit" default:"50"`
}

type filteredIn struct {
	RegionID int32  `path:"region_id"`
	TypeID   int32  `query:"type_id" required:"true"`
	UserID   string `query:"user_id,omitempty" format:"uuid"`
	Enabled  bool   `query:"enabled"`
}

func TestDeclaredParametersAreAccepted(t *testing.T) {
	spec := filters.SpecFromQueryTags("list-market-region-history", filteredIn{})
	require.NoError(t, filterQuery(spec, map[string]string{
		"type_id": "34",
		"user_id": "0192f4d0-0000-7000-8000-000000000001",
		"enabled": "true",
	}))
}

func TestAnUndeclaredParameterIsRefused(t *testing.T) {
	spec := filters.SpecFromQueryTags("list-character-contacts", pagedIn{})
	err := filterQuery(spec, map[string]string{"standing": "-10"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "standing")
	require.Contains(t, err.Error(), "unknown filter field")
}

func TestTheLegacyODataFilterIsRefusedByName(t *testing.T) {
	spec := filters.SpecFromQueryTags("list-character-contacts", pagedIn{})
	err := filterQuery(spec, map[string]string{"$filter": "standing lt 0"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "$filter")
	// The message must say WHY, not merely that it was rejected — a client
	// that gets "unknown field" learns nothing about where to go next.
	require.Contains(t, err.Error(), "MORE data than was asked for")
}

func TestATypeConfusedValueIsRefused(t *testing.T) {
	spec := filters.SpecFromQueryTags("list-market-region-history", filteredIn{})
	require.ErrorContains(t, filterQuery(spec, map[string]string{"type_id": "'; DROP TABLE app.asset; --"}), "not an integer")
	require.ErrorContains(t, filterQuery(spec, map[string]string{"user_id": "not-a-uuid"}), "not a uuid")
	require.ErrorContains(t, filterQuery(spec, map[string]string{"enabled": "yes-please"}), "not a bool")
}

// TestAnOpaqueCursorIsNotMistakenForAnInjection is the reason FieldOpaque
// exists. base64url's alphabet includes '-', so a legitimate cursor can
// contain '--', which containsSQLMeta rejects outright. Typing cursors as
// FieldString would have 422'd real pagination.
func TestAnOpaqueCursorIsNotMistakenForAnInjection(t *testing.T) {
	spec := filters.SpecFromQueryTags("list-character-contacts", pagedIn{})
	require.NoError(t, filterQuery(spec, map[string]string{"after": "eyJvcmRlcl9pZCI6MX0--_x"}))
	require.NoError(t, filterQuery(spec, map[string]string{"before": "0"}))
}

// TestAnOperationWithNoQueryParametersRefusesEveryone — an endpoint that
// declares none has a closed set of size zero, and anything at all is
// undeclared. This is the case that used to be silently ignored most often.
func TestAnOperationWithNoQueryParametersRefusesEveryone(t *testing.T) {
	type emptyIn struct{}
	spec := filters.SpecFromQueryTags("get-me", emptyIn{})
	require.Error(t, filterQuery(spec, map[string]string{"anything": "1"}))
	require.NoError(t, filterQuery(spec, map[string]string{}))
}

// TestSpecFromQueryTagsMatchesTheDocumentedParameters proves the derivation
// rather than the enforcement: the closed set comes from the same `query:`
// tags huma builds the OpenAPI parameter list from, so the two cannot drift.
func TestSpecFromQueryTagsMatchesTheDocumentedParameters(t *testing.T) {
	spec := filters.SpecFromQueryTags("x", filteredIn{})
	require.Len(t, spec.Fields, 3, "path parameters are not filters and must not be in the set")
	require.Equal(t, filters.FieldInt, spec.Fields["type_id"].Type)
	require.Equal(t, filters.FieldUUID, spec.Fields["user_id"].Type, `format:"uuid" must win over the Go string kind`)
	require.Equal(t, filters.FieldBool, spec.Fields["enabled"].Type)
	require.NotContains(t, spec.Fields, "region_id")
}
