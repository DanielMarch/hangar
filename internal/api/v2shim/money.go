package v2shim

import (
	"fmt"
	"math"
	"strconv"

	"github.com/shopspring/decimal"
)

// ── PHASE 20.6 (GATE 7): THESE TOOK THE WRONG TYPE ───────────────────────
// Money, MoneyOrNull, MoneyOrZero and numericString were written against
// pgtype.Numeric and sat on the reachability allowlist for five phases as
// "a helper waiting for the routes that would use it". That was half the
// story. sqlc.yaml overrides every `pg_catalog.numeric` column to
// shopspring/decimal (Principle 9 is enforced there, not by convention), so
// app.wallet_journal.amount arrives as decimal.NullDecimal and app.
// wallet_balance.balance as decimal.Decimal. No store row has ever produced
// a pgtype.Numeric.
//
// So these helpers were not merely uncalled — they were UNCALLABLE, and a
// route that tried to use them would not have compiled. Writing the wallet
// routes is what surfaced it, which is the argument for writing the routes
// rather than carrying the helpers.
//
// The conversion they perform is unchanged and so is the reasoning below;
// only the input type moved to the one the codebase actually has.

// Money converts HANGAR's exact decimal money to the JSON **number**
// legacy emitted.
//
// ── THE LOSSY CONVERSION, STATED PLAINLY ─────────────────────────────────
// HANGAR stores money as NUMERIC(30,2) and serialises it as a JSON string
// (Principle 9, SRS §5.1: "No float64 on any money path"). Legacy stored it
// as a MySQL DOUBLE and serialised it as a JSON number. Byte-compatibility
// therefore REQUIRES going back through float64, and above 2^53 that loses
// precision.
//
// SRS §10 calls this "reintroducing IEEE-754 imprecision", which is half
// right and worth correcting: legacy never had the precision to begin with.
// Its `character_wallet_journals.amount` column is `double`, so a balance of
// 9007199254740993.01 ISK was already 9007199254741000 in legacy's own
// database, before any serialisation. Measured, not assumed — see
// testdata/legacy-api-v2/README.md.
//
// So the honest framing is not "the shim degrades your data" but "the shim
// reproduces the precision legacy had". A client that wants exact ISK has
// to move to /api/v1, where it is a string and always was exact. That is
// the sentence the migration guide leads with.
//
// This function is the ONE place the conversion happens, so there is
// exactly one thing to audit and one thing to point at.
func Money(d decimal.Decimal) (Num, error) {
	text := numericString(d)
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("v2shim: %q is not a number: %w", text, err)
	}
	return Num(phpPrecision(value)), nil
}

// phpPrecision applies the rounding legacy's WRITE path applied, so the
// shim emits the number legacy's database actually holds.
//
// ── PHASE 23 (N-3): MEASURED, AT LAST ───────────────────────────────────
//
// The comment above says a balance of 9007199254740993.01 ISK "was already
// 9007199254741000 in legacy's own database". That was right, and for five
// phases nobody knew WHY — the reachability allowlist recorded it as
// "MySQL's/PHP's 14-significant-digit rounding, not IEEE-754 nearest" and
// said closing it "needs the PHP recorder re-run with serialize_precision
// measured". Phase 23 ran that measurement, and the answer was neither of
// the two things that had been guessed:
//
//	PHP 8.2.33   precision=14, serialize_precision=-1
//	             json_encode(9007199254740993.01) = 9007199254740994
//	MySQL 8.4.11 SELECT CAST(9007199254740993.01 AS DOUBLE)
//	             = 9.007199254740994e15
//
// Neither the encoder nor the database loses those digits. Both render the
// nearest double exactly. But the value SITTING IN THE FIXTURE TABLE is
//
//	SELECT price FROM character_orders WHERE order_id = 8999
//	= 9.007199254741e15
//
// — thirteen significant digits. The loss happened at INSERT: PDO binds a
// PHP float by STRINGIFYING it with the `precision` ini, which is 14, so
// MySQL received the text "9.007199254741E+15" and stored the double
// nearest to that. Legacy's write path, not its read path, and not MySQL.
//
// It is therefore reproducible, deterministically, from the exact decimal:
// render to 14 significant digits, parse back. That is the whole function.
//
// ── WHY THIS IS THE SHIM'S JOB AND NOT A CORRUPTION ─────────────────────
//
// A byte-compatibility shim exists to emit what legacy emits. Legacy's
// stored value IS 9007199254741000 — a client reading /api/v2 today gets
// that number, and giving them 9007199254740994 instead would be a silent
// change in the data they have been reconciling against. The exact value
// is on /api/v1, as a string, and always has been.
//
// This is v2shim-only. Principle 9 forbids float64 on any money path and
// /api/v1 has none; the conversion has exactly one call site, Money above,
// which is why that function's doc says there is one thing to audit.
//
// Values below 2^53 with two decimal places — every real ISK amount — pass
// through unchanged: 5.55 stays 5.55, 10000000.5 stays 10000000.5, 0.01
// stays 0.01. TestPHPPrecisionMatchesTheRecordedCorpus is the guard.
func phpPrecision(value float64) float64 {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	// %.14G is PHP's own stringification under precision=14 — the same
	// conversion PDO performs when binding a float parameter.
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'G', phpIniPrecision, 64), 64)
	if err != nil {
		// Unreachable for a finite float64: FormatFloat's output always
		// parses. Returning the input keeps the failure a no-op rather
		// than a zero.
		return value
	}
	return rounded
}

// phpIniPrecision is PHP's `precision` ini setting, MEASURED at 14 against
// the pinned recorder image (php 8.2.33) rather than assumed. It is PHP's
// compiled-in default and has been for the language's whole history; an
// installation that has changed it stores different bytes, which is a
// property of that installation and not of this shim.
const phpIniPrecision = 14

// MoneyOrNull is Money for a nullable column: a SQL NULL becomes JSON
// null, which is what legacy emitted for one.
func MoneyOrNull(n decimal.NullDecimal) (any, error) {
	if !n.Valid {
		return nil, nil
	}
	value, err := Money(n.Decimal)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// MoneyOrZero is for a NOT NULL money column read through a nullable Go
// type. A NULL there means the row is not what the schema says it is, so
// this reports it rather than quietly emitting 0 — a zero balance and a
// missing balance are different facts and only one of them is an incident.
func MoneyOrZero(n decimal.NullDecimal) (Num, error) {
	if !n.Valid {
		return 0, errNullMoney
	}
	return Money(n.Decimal)
}

var errNullMoney = fmt.Errorf("v2shim: money value is NULL where the schema says NOT NULL")

// numericString renders the decimal as its exact text — the same value
// /api/v1 puts on the wire as a string. Going through text rather than
// decimal's own Float64() keeps the exact value visible at the boundary, so
// the single float64 conversion above is the only place precision is lost
// and it is obvious where.
func numericString(d decimal.Decimal) string { return d.String() }

// Float is for a column that is genuinely a float in BOTH schemas — an
// asset's x/y/z position, a contact's standing, a corporation's tax rate.
// Named separately from Money so that a reader can tell at the call site
// whether a value went through the lossy money path or was always a float,
// and so a grep for Money finds every money conversion and nothing else.
func Float(f *float64) any {
	if f == nil {
		return nil
	}
	return Num(*f)
}

// FloatValue is Float for a NOT NULL column.
func FloatValue(f float64) Num { return Num(f) }
