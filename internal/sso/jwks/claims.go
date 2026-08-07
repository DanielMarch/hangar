// Package jwks implements 01_ARCHITECTURE.md §7.2's offline JWT
// validation: a cached, periodically-refreshed set of EVE SSO's RSA
// signing keys, checked against with zero network calls per validation.
package jwks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// StringOrSlice unmarshals a JSON value that is either a bare string or an
// array of strings into a []string — the shape the `scp` claim is: a
// string when exactly one scope was granted, an array otherwise
// (01_ARCHITECTURE.md §7.2's "edge case that will bite").
type StringOrSlice []string

func (s *StringOrSlice) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = StringOrSlice{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("jwks: scp claim is neither a string nor an array of strings: %w", err)
	}
	*s = many
	return nil
}

// MarshalJSON round-trips a single-element slice back to a bare string,
// matching the shape EVE SSO itself emits, in case a caller re-serialises
// (e.g. a test fixture).
func (s StringOrSlice) MarshalJSON() ([]byte, error) {
	if len(s) == 1 {
		return json.Marshal(s[0])
	}
	return json.Marshal([]string(s))
}

// Claims is the EVE SSO access token's claim set (it, not a separate ID
// token, is what carries character identity — 01_ARCHITECTURE.md §7.2).
type Claims struct {
	jwt.RegisteredClaims
	Scopes StringOrSlice `json:"scp,omitempty"`
	Owner  string        `json:"owner"`
	Name   string        `json:"name"`
}

// characterIDPrefix is the fixed, non-negotiable format EVE SSO uses for
// `sub`. Checked with plain string operations, not a regex — this is a
// structural protocol constant HANGAR itself depends on to extract an
// identifier, not an external vocabulary Principle 14 forbids validating.
const characterIDPrefix = "CHARACTER:EVE:"

// CharacterID extracts the character ID from `sub`, which must be exactly
// "CHARACTER:EVE:<digits>".
func (c *Claims) CharacterID() (int64, error) {
	sub := c.Subject
	if !strings.HasPrefix(sub, characterIDPrefix) {
		return 0, fmt.Errorf("jwks: sub %q does not start with %q", sub, characterIDPrefix)
	}
	digits := sub[len(characterIDPrefix):]
	if digits == "" {
		return 0, fmt.Errorf("jwks: sub %q has no digits after %q", sub, characterIDPrefix)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("jwks: sub %q: %q is not all digits", sub, digits)
		}
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("jwks: sub %q: %w", sub, err)
	}
	return id, nil
}

// audienceContains reports whether aud contains want, exact match.
func audienceContains(aud jwt.ClaimStrings, want string) bool {
	for _, a := range aud {
		if a == want {
			return true
		}
	}
	return false
}
