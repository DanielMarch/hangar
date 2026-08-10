// Package api holds the HTTP layer's cross-cutting pieces (envelope,
// cursor, errors, router assembly, OpenAPI generation) that every
// internal/api/v1 handler builds on. cursor.go is the opaque keyset cursor
// codec SRS §6 and the Phase 15 roadmap entry both specify: "Internal
// cursors are opaque base64 over a keyset tuple; limit accepts 10–100 with
// a default of 50; OFFSET is prohibited." sqlc's `no-offset` vet rule
// (sqlc.yaml) already enforces the SQL side; this file is what keeps the Go
// side honest — nothing here ever turns a cursor into a row-count skip.
package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// MinLimit, MaxLimit and DefaultLimit are SRS §6's pagination bounds:
	// "limit accepts 10–100 with a default of 50".
	MinLimit     = 10
	MaxLimit     = 100
	DefaultLimit = 50
)

// ZeroSentinel is the one cursor value that is never base64-decoded: "the
// '0' sentinel means start-of-set with `after` and end-of-set with
// `before`" (roadmap Phase 15 edge cases). Supplying it is valid input, not
// an error — it differs from omitting the parameter only in that `before:
// "0"` explicitly requests backward pagination from the end of the set,
// where omitting `before` entirely just leaves the query in its default
// forward mode.
const ZeroSentinel = "0"

// Direction is which way a keyset query walks relative to Cursor.
type Direction int

const (
	// Forward is the default: no cursor, or `after` supplied (including the
	// "0" sentinel, meaning start-of-set).
	Forward Direction = iota
	// Backward is `before` supplied (including the "0" sentinel, meaning
	// end-of-set).
	Backward
)

// Keyset is the opaque tuple encoded inside a non-sentinel cursor: the
// ordered sort-key values of the last row the caller saw, addressed by
// column name so a handler's decode never has to match encode order by
// position. Every value round-trips through JSON, so callers read back
// float64/string/bool exactly as encoded — a handler that needs an int64
// row id must reconstitute it from the JSON number itself, not assume Go's
// static type.
type Keyset map[string]any

// PageRequest is a validated after/before/limit triple, ready to drive a
// keyset SQL query (`WHERE key > sqlc.arg(after) ... LIMIT sqlc.arg(limit)`
// — never OFFSET).
type PageRequest struct {
	Limit     int32
	Direction Direction
	// Cursor is nil at start-of-set (Forward) or end-of-set (Backward) —
	// covers both "no cursor supplied" and the explicit "0" sentinel.
	// Otherwise it is the decoded keyset tuple.
	Cursor Keyset
}

// Sentinel errors this package's validation returns. errors.go wraps these
// into the HTTP-facing huma.StatusError.
var (
	// ErrCursorBothDirections is "after and before are mutually exclusive —
	// supplying both is a client error" (roadmap Phase 15 edge cases).
	ErrCursorBothDirections = errors.New("api: cursor: `after` and `before` are mutually exclusive")
	// ErrCursorLimitOutOfRange is limit < 10 or > 100.
	ErrCursorLimitOutOfRange = errors.New("api: cursor: limit must be between 10 and 100")
	// ErrCursorMalformed is a non-sentinel cursor that isn't valid
	// base64(JSON object) — never surfaced as a 500, always a client error.
	ErrCursorMalformed = errors.New("api: cursor: malformed cursor value")
)

// ParsePageRequest validates the three raw query parameters HTTP handlers
// receive (`after`, `before`, `limit`) into a PageRequest. limit == nil
// means the parameter was omitted, applying DefaultLimit.
func ParsePageRequest(after, before string, limit *int32) (PageRequest, error) {
	if after != "" && before != "" {
		return PageRequest{}, ErrCursorBothDirections
	}

	l := int32(DefaultLimit)
	if limit != nil {
		l = *limit
	}
	if l < MinLimit || l > MaxLimit {
		return PageRequest{}, ErrCursorLimitOutOfRange
	}

	switch {
	case after != "":
		ks, err := decodeCursor(after)
		if err != nil {
			return PageRequest{}, err
		}
		return PageRequest{Limit: l, Direction: Forward, Cursor: ks}, nil
	case before != "":
		ks, err := decodeCursor(before)
		if err != nil {
			return PageRequest{}, err
		}
		return PageRequest{Limit: l, Direction: Backward, Cursor: ks}, nil
	default:
		return PageRequest{Limit: l, Direction: Forward, Cursor: nil}, nil
	}
}

// decodeCursor decodes one cursor value. The "0" sentinel decodes to (nil,
// nil) in both directions — ParsePageRequest's caller distinguishes
// start-of-set from end-of-set purely by which parameter (`after` vs
// `before`) carried it, exactly as the roadmap's edge case specifies.
func decodeCursor(v string) (Keyset, error) {
	if v == ZeroSentinel {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCursorMalformed, err)
	}
	var ks Keyset
	if err := json.Unmarshal(raw, &ks); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCursorMalformed, err)
	}
	if len(ks) == 0 {
		return nil, fmt.Errorf("%w: empty keyset", ErrCursorMalformed)
	}
	return ks, nil
}

// EncodeCursor is the inverse of decodeCursor, used by handlers to build
// PageInfo.NextCursor/PrevCursor. A nil keyset encodes to the "0" sentinel
// — the correct value for "no further rows in this direction", which a
// client can feed straight back in as `before`/`after` and get the
// opposite end of the set.
func EncodeCursor(ks Keyset) string {
	if len(ks) == 0 {
		return ZeroSentinel
	}
	raw, err := json.Marshal(ks)
	if err != nil {
		// Keyset values are always JSON-marshalable primitives (int64,
		// string, float64, bool, time.Time via encoding/json) produced by
		// this package's own handlers, never client input — a marshal
		// failure here is a programming error, not a request to reject.
		panic(fmt.Sprintf("api: cursor: encoding keyset: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
