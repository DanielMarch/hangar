package handlers_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/stretchr/testify/require"
)

// goldenParsers maps testdata/esi/character/<key>.json to the Parse
// function that should consume it with no field loss. Every key here is
// one of Phase 7's nineteen character sub-resources plus one extra fixture
// for the "never logged in" edge case (both map onto ParseCharacterOnline).
var goldenParsers = map[string]func([]byte) (any, error){
	"character_sheet":        func(b []byte) (any, error) { return handlers.ParseCharacterSheet(b) },
	"skills":                 func(b []byte) (any, error) { return handlers.ParseCharacterSkills(b) },
	"skillqueue":             func(b []byte) (any, error) { return handlers.ParseCharacterSkillQueue(b) },
	"attributes":             func(b []byte) (any, error) { return handlers.ParseCharacterAttributes(b) },
	"clones":                 func(b []byte) (any, error) { return handlers.ParseCharacterClones(b) },
	"implants":               func(b []byte) (any, error) { return handlers.ParseCharacterImplants(b) },
	"contacts":               func(b []byte) (any, error) { return handlers.ParseCharacterContacts(b) },
	"contact_labels":         func(b []byte) (any, error) { return handlers.ParseCharacterContactLabels(b) },
	"standings":              func(b []byte) (any, error) { return handlers.ParseCharacterStandings(b) },
	"titles":                 func(b []byte) (any, error) { return handlers.ParseCharacterTitles(b) },
	"roles":                  func(b []byte) (any, error) { return handlers.ParseCharacterRoles(b) },
	"medals":                 func(b []byte) (any, error) { return handlers.ParseCharacterMedals(b) },
	"loyalty_points":         func(b []byte) (any, error) { return handlers.ParseCharacterLoyaltyPoints(b) },
	"agents_research":        func(b []byte) (any, error) { return handlers.ParseCharacterAgentResearch(b) },
	"fatigue":                func(b []byte) (any, error) { return handlers.ParseCharacterFatigue(b) },
	"corporationhistory":     func(b []byte) (any, error) { return handlers.ParseCharacterCorporationHistory(b) },
	"location":               func(b []byte) (any, error) { return handlers.ParseCharacterLocation(b) },
	"online":                 func(b []byte) (any, error) { return handlers.ParseCharacterOnline(b) },
	"online_never_logged_in": func(b []byte) (any, error) { return handlers.ParseCharacterOnline(b) },
	"ship":                   func(b []byte) (any, error) { return handlers.ParseCharacterShip(b) },
}

const testdataDir = "../../../testdata/esi/character"

// TestGoldenFileParsesAllCharacterDomains (roadmap exit criterion): every
// recorded ESI response parses into its DTO with no field loss. Every
// *.json file under testdata/esi/character must have a registered parser
// (missing one fails loudly, so a fixture never silently goes untested);
// every field present in the source JSON, at every nesting level, must
// still be reachable in the DTO after a marshal round trip.
func TestGoldenFileParsesAllCharacterDomains(t *testing.T) {
	entries, err := os.ReadDir(testdataDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "no golden fixtures found under %s", testdataDir)

	tested := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		key := entry.Name()[:len(entry.Name())-len(".json")]
		t.Run(key, func(t *testing.T) {
			parse, ok := goldenParsers[key]
			require.Truef(t, ok, "testdata/esi/character/%s.json has no registered parser in golden_test.go's goldenParsers map", key)

			raw, err := os.ReadFile(filepath.Join(testdataDir, entry.Name()))
			require.NoError(t, err)

			dto, err := parse(raw)
			require.NoErrorf(t, err, "parsing %s", entry.Name())

			assertNoFieldLoss(t, raw, dto)
		})
		tested++
	}
	require.Equal(t, len(goldenParsers), tested, "every registered parser must have a fixture and vice versa")
}

// assertNoFieldLoss marshals dto back to JSON and asserts every key
// present in the original raw JSON, at every level of nesting, is still
// present in the round-tripped output. Value-format differences (e.g. a
// re-serialised date-time, float precision) are not field loss and are
// deliberately not compared.
func assertNoFieldLoss(t *testing.T, raw []byte, dto any) {
	t.Helper()

	roundTripped, err := json.Marshal(dto)
	require.NoError(t, err)

	var src, got any
	require.NoError(t, json.Unmarshal(raw, &src))
	require.NoError(t, json.Unmarshal(roundTripped, &got))

	assertKeysPreserved(t, "$", src, got)
}

func assertKeysPreserved(t *testing.T, path string, src, got any) {
	t.Helper()
	switch s := src.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		require.Truef(t, ok, "%s: expected an object in the round-tripped output, got %T", path, got)
		for k, sv := range s {
			if isEmptyJSONValue(sv) {
				// A JSON null, empty array, or empty string carries no
				// information beyond "absent" — encoding/json's
				// `omitempty` legitimately drops a zero-value field
				// (including a non-nil empty slice) from the
				// round-tripped output, which is not field loss.
				continue
			}
			gv, present := g[k]
			require.Truef(t, present, "%s.%s: present in the source JSON but lost after parsing into the DTO", path, k)
			assertKeysPreserved(t, path+"."+k, sv, gv)
		}
	case []any:
		g, ok := got.([]any)
		require.Truef(t, ok, "%s: expected an array in the round-tripped output, got %T", path, got)
		require.Lenf(t, g, len(s), "%s: array length changed after round-tripping through the DTO", path)
		for i := range s {
			assertKeysPreserved(t, fmt.Sprintf("%s[%d]", path, i), s[i], g[i])
		}
	default:
		// Scalars (string/number/bool/nil): presence was already
		// confirmed by the caller; format differences are not loss.
	}
}

// isEmptyJSONValue reports whether v decodes to something encoding/json's
// `omitempty` would drop on the way back out: null, an empty array, or an
// empty string. Such a value is indistinguishable from "the field was
// never in the DTO at all" once round-tripped, so it must not be required
// to reappear.
func isEmptyJSONValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []any:
		return len(x) == 0
	case string:
		return x == ""
	default:
		return false
	}
}
