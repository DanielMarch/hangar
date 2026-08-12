package v2shim_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hangar-project/hangar/internal/api/v2shim"
)

// TestPHPDoubleFormatting pins the number formatting against values
// MEASURED from PHP 8.2.33's json_encode — the same PHP version and
// default ini the corpus was recorded under. Written as literals rather
// than derived from the formatter, because a test that re-derives the
// expected value from the code under test cannot catch the code being
// wrong.
func TestPHPDoubleFormatting(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		// Plain notation, including the whole-number floats that appear
		// all over the corpus (`"reward":0`, `"volume":27289`).
		{0, "0"},
		{-0, "0"},
		{27289, "27289"},
		{1.5, "1.5"},
		{-2.25, "-2.25"},
		{4.95, "4.95"},
		{0.1, "0.1"},
		{1234567.89, "1234567.89"},
		{1000000.25, "1000000.25"},
		{5.55, "5.55"},
		{10000000.5, "10000000.5"},
		{9007199254741000, "9007199254741000"},
		{9007199254740992, "9007199254740992"},
		{10000000000000002, "10000000000000002"},
		{123456789012345.59375, "123456789012345.6"},
		{0.0001, "0.0001"},
		{0.00012345, "0.00012345"},
		{0.001, "0.001"},
		{1e14, "100000000000000"},
		{1e16, "10000000000000000"},
		{1.5e16, "15000000000000000"},

		// Exponent notation starts at 1e17 and below 1e-4, with PHP's
		// mandatory ".0" mantissa and unpadded exponent.
		{1e17, "1.0e+17"},
		{1.5e17, "1.5e+17"},
		{1e18, "1.0e+18"},
		{1e20, "1.0e+20"},
		{1e21, "1.0e+21"},
		{-1e17, "-1.0e+17"},
		{1e-5, "1.0e-5"},
		{1.5e-5, "1.5e-5"},
		{123456789012345678, "1.2345678901234568e+17"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			got, err := v2shim.Encode(v2shim.Num(tc.in))
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

// TestStringEscapingMatchesPHPDefaults covers the three places Go's
// encoding/json and PHP's json_encode disagree. Each one appears in the
// recorded corpus, so each one is a real byte mismatch rather than a
// hypothetical.
func TestStringEscapingMatchesPHPDefaults(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"slashes are escaped": {
			// Every URL in a legacy pagination envelope.
			in:   "http://seat.local/api/v2/users",
			want: `"http:\/\/seat.local\/api\/v2\/users"`,
		},
		// Exactly the bytes testdata/legacy-api-v2/responses/character.sheet.json
		// carries for the fixture's character description.
		"non-ascii becomes \\uXXXX": {
			in:   "ünïcode",
			want: `"\u00fcn\u00efcode"`,
		},
		"astral planes become surrogate pairs": {
			in:   "\U0001F680",
			want: `"\ud83d\ude80"`,
		},
		"HTML characters are NOT escaped": {
			// Go escapes < > & by default; PHP does not. A corporation
			// description containing HTML is entirely ordinary.
			in:   `<b>a & b</b> 'quoted'`,
			want: `"<b>a & b<\/b> 'quoted'"`,
		},
		"quotes and backslashes": {
			in:   `say "hi"\done`,
			want: `"say \"hi\"\\done"`,
		},
		"control characters": {
			in:   "a\nb\tc\x01",
			want: `"a\nb\tc\u0001"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := v2shim.Encode(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

// TestObjPreservesInsertionOrder — the reason Obj exists at all.
func TestObjPreservesInsertionOrder(t *testing.T) {
	obj := v2shim.NewObj(4).
		Set("zebra", v2shim.Int(1)).
		Set("apple", v2shim.Int(2)).
		Set("mango", v2shim.Int(3))

	got, err := v2shim.Encode(obj)
	require.NoError(t, err)
	require.Equal(t, `{"zebra":1,"apple":2,"mango":3}`, string(got))

	// Re-setting a key must keep its position — PHP's `$a['k'] = v` does.
	obj.Set("zebra", v2shim.Int(9))
	got, err = v2shim.Encode(obj)
	require.NoError(t, err)
	require.Equal(t, `{"zebra":9,"apple":2,"mango":3}`, string(got))
}

// TestEmptyArrayIsNotNull is the Phase 18 lesson applied here: an empty
// success and a failure must not look alike. Legacy emits `"data":[]` for
// a collection with no rows, and the shim must never render that as null.
func TestEmptyArrayIsNotNull(t *testing.T) {
	empty, err := v2shim.Encode(v2shim.Arr{})
	require.NoError(t, err)
	require.Equal(t, "[]", string(empty))

	var absent v2shim.Arr
	null, err := v2shim.Encode(absent)
	require.NoError(t, err)
	require.Equal(t, "null", string(null),
		"a nil Arr must stay distinguishable from an empty one, or 'no rows' and 'no answer' collapse into the same bytes")
}

// TestEncodeRefusesUnknownTypes — every value in a shim response has to be
// an explicit shim type. Accepting a bare float64 or int would let a
// translator silently pick Go's formatting instead of PHP's.
func TestEncodeRefusesUnknownTypes(t *testing.T) {
	_, err := v2shim.Encode(3.14)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot encode float64")

	_, err = v2shim.Encode(map[string]any{"a": 1})
	require.Error(t, err, "a Go map has no field order and must never reach the encoder")
}

func TestEncodeRejectsNonFiniteNumbers(t *testing.T) {
	_, err := v2shim.Encode(v2shim.Num(inf()))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no JSON representation")
}

func inf() float64 {
	zero := 0.0
	return 1 / zero
}
