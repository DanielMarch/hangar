package v2shim

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

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
func Money(n pgtype.Numeric) (Num, error) {
	if !n.Valid {
		return 0, errNullMoney
	}
	text, err := numericString(n)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("v2shim: %q is not a number: %w", text, err)
	}
	return Num(value), nil
}

// MoneyOrNull is Money for a nullable column: a SQL NULL becomes JSON
// null, which is what legacy emitted for one.
func MoneyOrNull(n pgtype.Numeric) (any, error) {
	if !n.Valid {
		return nil, nil
	}
	value, err := Money(n)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// MoneyOrZero is for a NOT NULL money column read through a nullable Go
// type. A NULL there means the row is not what the schema says it is, so
// this reports it rather than quietly emitting 0 — a zero balance and a
// missing balance are different facts and only one of them is an incident.
func MoneyOrZero(n pgtype.Numeric) (Num, error) {
	if !n.Valid {
		return 0, errNullMoney
	}
	return Money(n)
}

var errNullMoney = fmt.Errorf("v2shim: money value is NULL where the schema says NOT NULL")

// numericString renders a pgtype.Numeric as its exact decimal text — the
// same value /api/v1 puts on the wire as a string. Going through text
// rather than pgtype's Float64Value() keeps the exact value visible at the
// boundary, so the single float64 conversion above is the only place
// precision is lost and it is obvious where.
func numericString(n pgtype.Numeric) (string, error) {
	if n.NaN {
		return "", fmt.Errorf("v2shim: money value is NaN")
	}
	if n.InfinityModifier != pgtype.Finite {
		return "", fmt.Errorf("v2shim: money value is infinite")
	}
	if n.Int == nil {
		return "0", nil
	}

	unscaled := new(big.Int).Set(n.Int)
	if n.Exp >= 0 {
		unscaled.Mul(unscaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n.Exp)), nil))
		return unscaled.String(), nil
	}

	rational := new(big.Rat).SetFrac(unscaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n.Exp)), nil))
	return rational.FloatString(int(-n.Exp)), nil
}

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
