package v1

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/hangar-project/hangar/internal/api"
)

// TestCursorTimeIDRoundTrip covers defect B46's Go half, the part that needs
// no database: what the (time, id) cursor carries, and what happens to a
// cursor that does not carry it.
func TestCursorTimeIDRoundTrip(t *testing.T) {
	// A cursor built by a handler must decode back to the same pair. The id
	// is deliberately larger than 2^53 — encode it as a JSON number and this
	// is the assertion that fails, which is the whole reason timeIDKeyset
	// writes a decimal string instead.
	const bigID = int64(9007199254740993) // 2^53 + 1
	when := time.Date(2026, 8, 6, 17, 10, 55, 123456789, time.UTC)

	encoded := api.EncodeCursor(timeIDKeyset("date", when, "journal_id", bigID))
	page, err := api.ParsePageRequest("", encoded, nil)
	if err != nil {
		t.Fatalf("ParsePageRequest(%q) = %v", encoded, err)
	}
	gotTime, gotID, err := cursorTimeID(page, "date", "journal_id")
	if err != nil {
		t.Fatalf("cursorTimeID = %v", err)
	}
	if !gotTime.Equal(when) {
		t.Errorf("time round-trip = %v, want %v", gotTime, when)
	}
	if gotID != bigID {
		t.Errorf("id round-trip = %d, want %d — a JSON number would round to %d here",
			gotID, bigID, int64(float64(bigID)))
	}
}

// TestCursorTimeIDStartOfSet pins the sentinel pair. Both components must be
// maximal: the comparison is lexicographic, so an id sentinel below the
// maximum would exclude a row that happened to carry the farFuture date
// rather than including it.
func TestCursorTimeIDStartOfSet(t *testing.T) {
	for _, name := range []string{"no cursor", "zero sentinel"} {
		t.Run(name, func(t *testing.T) {
			raw := ""
			if name == "zero sentinel" {
				raw = api.ZeroSentinel
			}
			page, err := api.ParsePageRequest("", raw, nil)
			if err != nil {
				t.Fatalf("ParsePageRequest = %v", err)
			}
			gotTime, gotID, err := cursorTimeID(page, "date", "journal_id")
			if err != nil {
				t.Fatalf("cursorTimeID = %v", err)
			}
			if !gotTime.Equal(farFuture) {
				t.Errorf("start-of-set time = %v, want %v", gotTime, farFuture)
			}
			if gotID != math.MaxInt64 {
				t.Errorf("start-of-set id = %d, want MaxInt64", gotID)
			}
		})
	}
}

// TestCursorTimeIDRejectsIncompleteCursor is the assertion that would have
// caught the notifications endpoint serving page one forever.
//
// That handler encoded `api.Keyset{"before": <the entire row struct>}` and
// then decoded the key "sent_at", which of course was absent. The old helper
// answered "absent" with the start-of-set sentinel, so every "next page"
// request restarted at the newest row and returned 200 while doing it. A
// present-but-undecodable cursor is a client error; only its ABSENCE means
// start-of-set.
func TestCursorTimeIDRejectsIncompleteCursor(t *testing.T) {
	when := time.Date(2026, 8, 6, 17, 10, 55, 0, time.UTC)

	cases := map[string]api.Keyset{
		"missing the id":           {"date": when.Format(time.RFC3339Nano)},
		"missing the timestamp":    {"journal_id": "25893163003"},
		"legacy date-only":         {"date": when.Format(time.RFC3339Nano)},
		"id is a JSON number":      {"date": when.Format(time.RFC3339Nano), "journal_id": 25893163003},
		"id is not an integer":     {"date": when.Format(time.RFC3339Nano), "journal_id": "not-a-number"},
		"timestamp is not RFC3339": {"date": "6 August 2026", "journal_id": "25893163003"},
		"wrong keys entirely":      {"before": "whatever"},
	}

	for name, ks := range cases {
		t.Run(name, func(t *testing.T) {
			page, err := api.ParsePageRequest("", api.EncodeCursor(ks), nil)
			if err != nil {
				t.Fatalf("ParsePageRequest = %v", err)
			}
			if _, _, err := cursorTimeID(page, "date", "journal_id"); !errors.Is(err, api.ErrCursorMalformed) {
				t.Fatalf("cursorTimeID error = %v, want ErrCursorMalformed — "+
					"silently restarting at page 1 is how the notifications endpoint looped", err)
			}
		})
	}
}
