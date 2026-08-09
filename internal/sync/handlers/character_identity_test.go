package handlers_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/stretchr/testify/require"
)

// TestCharacterDTOMatchesPin20260804 (roadmap exit criterion, corrected
// against the live spec — see character_identity.go's doc comment):
// achievement_score, character_title_id (a UUID — the spec's own
// "cosmetic title" concept), and corporation_title (a string, NOT
// corporation_title_id — there is no such field, and no `title_id` field
// exists on this endpoint at all in the 2026-08-04 spec) must all be
// present; no field named title_id or corporation_title_id may exist.
func TestCharacterDTOMatchesPin20260804(t *testing.T) {
	tags := jsonTags(t, handlers.CharacterSheetDTO{})

	require.Contains(t, tags, "achievement_score", "the 2026-08-04 pin adds achievement_score")
	require.Contains(t, tags, "character_title_id", "the 2026-08-04 pin adds character_title_id (a UUID cosmetic title, not a corp title)")
	require.Contains(t, tags, "corporation_title", "the live spec's actual field is corporation_title (a string), not corporation_title_id")

	require.NotContains(t, tags, "title_id", "no such field exists on GET /characters/{character_id} in the 2026-08-04 spec")
	require.NotContains(t, tags, "corporation_title_id", "the roadmap's summary claimed this name; the live spec does not have it — see character_identity.go")

	// character_title_id must decode as a UUID string, not a bigint —
	// Principle 13's exact trap: an "_id"-suffixed field is not
	// automatically an identifier of the usual numeric kind.
	golden, err := json.Marshal(map[string]any{
		"achievement_score": 100, "alliance_id": nil, "birthday": "2015-03-24T11:37:00Z",
		"bloodline_id": 1, "character_title_id": "3868eaed-8278-4cb7-9709-7d7de9c20dc7",
		"corporation_id": 1, "corporation_title": "Title", "gender": "male", "name": "N", "race_id": 1,
	})
	require.NoError(t, err)
	dto, err := handlers.ParseCharacterSheet(golden)
	require.NoError(t, err)
	require.NotNil(t, dto.CharacterTitleID)
	require.Equal(t, int64(100), dto.AchievementScore)
	require.NotNil(t, dto.CorporationTitle)
	require.Equal(t, "Title", *dto.CorporationTitle)
}

// jsonTags returns the (comma-stripped) `json` tag name for every field of
// v's type — used to assert a DTO's wire shape without depending on Go
// field-name casing.
func jsonTags(t *testing.T, v any) map[string]bool {
	t.Helper()
	tags := map[string]bool{}
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := tag
		for j, c := range tag {
			if c == ',' {
				name = tag[:j]
				break
			}
		}
		tags[name] = true
	}
	return tags
}
