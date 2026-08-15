package v2shim

import "testing"

// TestFormatPHPDoubleMatchesMeasuredPHP8233 pins formatPHPDouble against
// output MEASURED from PHP 8.2.33, not inferred from documentation.
//
// ── PHASE 20.7: THE MEASUREMENT THAT SETTLED serialize_precision ─────────
// The open question was whether legacy's doubles lose precision by PHP's
// 14-significant-digit rounding or by IEEE-754 shortest-round-trip, and it
// mattered because every double in the recorded corpus is short enough that
// the two are indistinguishable — so this function had never been exercised
// on a value that could tell them apart.
//
// It was settled by running the recorder's own pinned interpreter
// (testdata/legacy-api-v2/recorder/Dockerfile: php:8.2-cli) with
// serialize_precision instrumented. The result, on PHP 8.2.33:
//
//	serialize_precision = -1        (the default since PHP 7.1)
//	precision           = 14
//	json_encode(9007199254740993.01) = 9007199254740994
//
// So json_encode uses SHORTEST ROUND-TRIP, and the 14-digit hypothesis is
// wrong. It is also wrong about the spelling: forcing
// serialize_precision=14 yields "9.007199254741e+15" — exponent form —
// which is not the corpus's "9007199254741000" either. The corpus value is
// simply a different double (9007199254741000.0 is exactly representable,
// and PHP prints it as those digits), not a rounded rendering of
// 9007199254740993.01.
//
// CONCLUSION: formatPHPDouble was already correct and is unchanged. The
// shim CAN reproduce money above 2^53 — subject to the ordinary caveat that
// a float64 cannot represent every such value in the first place, which is
// upstream of anything this function does.
//
// The exponent-form boundary was measured in the same run and matches the
// implementation exactly: plain form through 1e16, exponent form from 1e17
// (phpSerializePrecisionDigits), and exponent form below 1e-4.
func TestFormatPHPDoubleMatchesMeasuredPHP8233(t *testing.T) {
	// Every expectation below is a literal transcript of what PHP 8.2.33
	// printed. Do not "correct" one by reasoning about it — re-run the
	// measurement instead.
	cases := []struct {
		in   float64
		want string
	}{
		// The values that distinguish the two hypotheses.
		{9007199254740993.01, "9007199254740994"}, // parses to the nearest double
		{9007199254740992, "9007199254740992"},    // 2^53
		{9007199254740994, "9007199254740994"},
		{9007199254741000, "9007199254741000"}, // the corpus's own value
		{-9007199254740994, "-9007199254740994"},

		// The exponent-form boundary, upward.
		{1e14, "100000000000000"},
		{1e15, "1000000000000000"},
		{1e16, "10000000000000000"},
		{1e17, "1.0e+17"},
		{1e18, "1.0e+18"},
		{1e21, "1.0e+21"},
		{1e22, "1.0e+22"},

		// ...and downward.
		{0.1, "0.1"},
		{0.01, "0.01"},
		{0.001, "0.001"},
		{1e-4, "0.0001"},
		{1e-5, "1.0e-5"},
		{1e-6, "1.0e-6"},
		{1e-7, "1.0e-7"},

		// Extremes.
		{1.2345678901234567e19, "1.2345678901234567e+19"},
		{1.7976931348623157e308, "1.7976931348623157e+308"},
		{5e-324, "5.0e-324"},
	}

	for _, c := range cases {
		got, err := formatPHPDouble(c.in)
		if err != nil {
			t.Errorf("formatPHPDouble(%v) returned an error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("formatPHPDouble(%v) = %q, PHP 8.2.33 printed %q", c.in, got, c.want)
		}
	}
}
