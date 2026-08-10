package teamspeak_test

import (
	"testing"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/teamspeak"
	"github.com/stretchr/testify/require"
)

// TestTS3EscapingRoundTrip (roadmap exit criterion): values containing
// spaces, pipes, and slashes survive both directions.
func TestTS3EscapingRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"space", "Hangar Alliance"},
		{"pipe", "Corp|Alt"},
		{"slash", "Path/To/Channel"},
		{"backslash", `back\slash`},
		{"mixed", `A corp | with / lots \ of chars`},
		{"tab and newline", "a\tb\nc"},
		{"empty", ""},
		{"already-looks-escaped input is still round-tripped as raw data", `literal \s not a real space`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			escaped := teamspeak.Escape(tc.raw)
			require.Equal(t, tc.raw, teamspeak.Unescape(escaped), "round trip must recover the exact original value")
		})
	}
}

// TestTS3EscapeProducesExpectedWireForm pins down the exact wire-format
// output for the characters the roadmap names explicitly, so a future
// change to escape.go that happens to still round-trip correctly (e.g.
// swapping which character maps to which escape) still gets caught.
func TestTS3EscapeProducesExpectedWireForm(t *testing.T) {
	require.Equal(t, `a\sb`, teamspeak.Escape("a b"))
	require.Equal(t, `a\pb`, teamspeak.Escape("a|b"))
	require.Equal(t, `a\/b`, teamspeak.Escape("a/b"))
	require.Equal(t, `a\\b`, teamspeak.Escape(`a\b`))
}

// TestTS3EscapeEscapesBackslashFirst: a literal backslash immediately
// followed by a character that looks like another escape's second half
// (e.g. a literal "\" followed by literal "s") must not be misread as
// that escape sequence — the classic ambiguity naive sequential
// find-and-replace escaping/unescaping is vulnerable to.
func TestTS3EscapeEscapesBackslashFirst(t *testing.T) {
	raw := `\s` // literal backslash followed by literal 's' — NOT a space
	escaped := teamspeak.Escape(raw)
	require.Equal(t, `\\s`, escaped, "the literal backslash must be doubled, leaving the 's' untouched")
	require.Equal(t, raw, teamspeak.Unescape(escaped))
}
