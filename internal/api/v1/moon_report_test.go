package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMoonReportParser — Appendix A capability #44, the moon-scan paste tool.
//
// ── DEFECT B51 (PHASE 20.8) ──────────────────────────────────────────────
// The traceability matrix has cited this test name since Phase 15 and no
// such test existed. It lives here rather than in test/capability because
// parseMoonReportText is unexported, and the capability IS the parse: the
// endpoint around it stores whatever comes out.
//
// ── WHAT A MOON SCAN ACTUALLY LOOKS LIKE ─────────────────────────────────
// The EVE client copies a survey to the clipboard as tab-separated lines,
// with a header row and a blank line or two, and a user pastes it whole. So
// the interesting cases are all about a paste that is not clean: the
// parser's documented contract is that unrecognised lines are SKIPPED rather
// than erroring the whole report, because a partially-parseable paste is
// more useful returned than rejected.
//
// The tests below pin both halves of that contract — what is kept and what
// is dropped — because "skips bad lines" and "silently returns nothing" look
// identical from a caller that only checks for an error.
func TestMoonReportParser(t *testing.T) {
	t.Run("a clean tab-separated paste yields one entry per ore line", func(t *testing.T) {
		got := parseMoonReportText("Bitumens\t0.35\nSylvite\t0.25\nCoesite\t0.40\n")
		require.Len(t, got, 3)
		require.Equal(t, map[string]string{"ore": "Bitumens", "percentage": "0.35"}, got[0])
		require.Equal(t, map[string]string{"ore": "Sylvite", "percentage": "0.25"}, got[1])
		require.Equal(t, map[string]string{"ore": "Coesite", "percentage": "0.40"}, got[2])
	})

	t.Run("lines with no tab are skipped, not errored", func(t *testing.T) {
		got := parseMoonReportText("Moon Survey\n\nBitumens\t0.35\nnot an ore line\nSylvite\t0.25")
		require.Len(t, got, 2, "the header, the blank line and the prose line are dropped; the two ore lines survive")
		require.Equal(t, "Bitumens", got[0]["ore"])
		require.Equal(t, "Sylvite", got[1]["ore"])
	})

	t.Run("a Windows paste keeps no carriage return in the value", func(t *testing.T) {
		// The EVE client on Windows copies CRLF. A trailing \r left on the
		// percentage would be stored in app.moon_report.parsed and would
		// compare unequal to the same figure pasted from any other client.
		got := parseMoonReportText("Bitumens\t0.35\r\nSylvite\t0.25\r\n")
		require.Len(t, got, 2)
		require.Equal(t, "0.35", got[0]["percentage"])
		require.Equal(t, "0.25", got[1]["percentage"])
	})

	t.Run("extra columns beyond ore and percentage do not break the line", func(t *testing.T) {
		// A survey copied with the moon and system columns still attached is
		// a normal paste, not a malformed one.
		got := parseMoonReportText("Bitumens\t0.35\tMoon 1\tJita\n")
		require.Len(t, got, 1)
		require.Equal(t, "Bitumens", got[0]["ore"])
		require.Equal(t, "0.35", got[0]["percentage"])
	})

	t.Run("a paste with nothing parseable yields no entries and no panic", func(t *testing.T) {
		require.Empty(t, parseMoonReportText("nothing here at all"))
		require.Empty(t, parseMoonReportText(""))
	})
}
