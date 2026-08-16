package v2shim_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hangar-project/hangar/internal/api/v2shim"
	"github.com/stretchr/testify/require"
)

// TestReleaseNotesMatchTheSunsetHeader is 04_RELEASE_GATES.md §7.5's fourth
// row — "the `Sunset` header matches the announced date, automated check
// against the release notes" — and it is the check that could not exist
// before Phase 22, because docs/RELEASE_NOTES.md did not (audit item N-8).
//
// Three of §7.5's four requirements are checked against that file. Without
// it, conditions 7.2 and 7.3 could still be measured (every shim response
// DOES carry Deprecation: true and an RFC 8594 Sunset — that is
// TestShimEmitsDeprecationAndSunset's job) but whether the date in the
// header agreed with any announcement was unanswerable, because there was
// no announcement.
//
// The direction matters: the header is authoritative and the notes are
// checked against it, not the other way round. v2shim.SunsetDate is the one
// place the date lives, and a release note that disagrees with the header
// the server actually sends is worse than no release note at all — it is an
// announcement an integrator would act on.
func TestReleaseNotesMatchTheSunsetHeader(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "RELEASE_NOTES.md")
	body, err := os.ReadFile(path)
	require.NoError(t, err,
		"docs/RELEASE_NOTES.md must exist: §7.5 checks three of its four requirements against it, "+
			"and a sunset policy with nothing to check against is a promise with no record")

	notes := string(body)
	announced := v2shim.SunsetDate.UTC().Format("2006-01-02")
	require.True(t, strings.Contains(notes, announced),
		"docs/RELEASE_NOTES.md does not announce the removal date %s that every /api/v2 response's "+
			"Sunset header carries. Either the notes are stale or the date moved without an announcement; "+
			"§10 permits moving it later with an entry in the notes and forbids moving it earlier", announced)

	// The other two file-checked requirements: that the shim's removal is
	// announced at all, and that the replacement is named.
	require.True(t, strings.Contains(notes, "/api/v2"),
		"the notes must name the surface being sunset")
	require.True(t, strings.Contains(notes, "APPENDIX_C_MIGRATION.md"),
		"an announcement with no migration path is not an announcement an integrator can act on")
}
