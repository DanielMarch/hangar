package domain

import "github.com/shopspring/decimal"

// Money is the one Go type every ISK-valued field must use (Principle 9,
// 01_ARCHITECTURE.md §17 invariant 1). It is a plain alias, not a wrapper,
// so decimal.Decimal's full API (arithmetic, comparison, (Un)MarshalJSON —
// which encodes as a JSON string, matching the wire contract) is available
// without a forwarding layer. `pgtype.Numeric` is acceptable only at the
// pgx driver boundary inside internal/store and must never escape it.
type Money = decimal.Decimal

// moneyTokens is the money vocabulary from 02_DATABASE_SCHEMA.md §3.1: any
// struct field whose name, split into words, contains one of these tokens
// is a money field and must not be float32/float64.
var moneyTokens = map[string]bool{
	"isk":        true,
	"amount":     true,
	"balance":    true,
	"price":      true,
	"total":      true,
	"tax":        true,
	"reward":     true,
	"collateral": true,
	"buyout":     true,
	// Phase 1b additions: real ISK columns encountered in the Tier-2 schema
	// that the §3.1 vocabulary's illustrative list didn't spell out by name.
	"escrow": true, // market_order.escrow — ISK held against a buy order
	"cost":   true, // industry_job.cost — ISK installation cost
	"payout": true, // insurance_price.payout — ISK insurance payout
}

// notMoneyFields is an explicit denylist for names that would otherwise
// false-positive against moneyTokens (§3.1: "quantity, volume (m³) and runs
// are not money"; a tax *rate* is a fraction, not an ISK amount, even though
// it contains the "tax" token).
var notMoneyFields = map[string]bool{
	"TaxRate":        true,
	"SecurityStatus": true,
	"VolumeRemain":   true, // market_order: units remaining, not an ISK value
	"Volume":         true, // m³, Principle 9 explicitly excludes it
	"Quantity":       true,
	"Runs":           true,
}

// IsMoneyFieldName reports whether a Go struct field name (PascalCase or
// snake_case) names an ISK-valued quantity per the §3.1 vocabulary. Used by
// TestNoFloatOnMoneyPaths (money_test.go) to flag any such field typed
// float32/float64 rather than Money.
func IsMoneyFieldName(name string) bool {
	if notMoneyFields[name] {
		return false
	}
	for _, word := range splitFieldWords(name) {
		if moneyTokens[word] {
			return true
		}
	}
	return false
}

// splitFieldWords lower-cases and splits a PascalCase or snake_case
// identifier into its component words, e.g. "TotalRewardISK" ->
// ["total", "reward", "isk"], "volume_remain" -> ["volume", "remain"].
func splitFieldWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	runes := []rune(name)
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	toLower := func(r rune) rune {
		if isUpper(r) {
			return r + ('a' - 'A')
		}
		return r
	}
	for i, r := range runes {
		switch {
		case r == '_' || r == '-':
			flush()
		case isUpper(r):
			// Word boundary before an uppercase letter, except mid-acronym
			// ("ISK" stays one word) — but an acronym followed by a new
			// capitalised word ("ISKValue") still splits at the transition
			// from upper-run into Upper+lower.
			prevUpper := i > 0 && isUpper(runes[i-1])
			nextLower := i+1 < len(runes) && !isUpper(runes[i+1]) && runes[i+1] != '_' && runes[i+1] != '-'
			if len(cur) > 0 && (!prevUpper || nextLower) {
				flush()
			}
			cur = append(cur, toLower(r))
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}
