package normalize_test

import (
	"net/http"
	"testing"

	"github.com/hangar-project/hangar/internal/sync/normalize"
)

func TestOutcome(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		transportErr bool
		want         string
	}{
		{"200", 200, false, "200"},
		{"304", 304, false, "304"},
		{"429", 429, false, "429"},
		{"500 round-trips as its own string, not a generic error", 500, false, "500"},
		{"transport error", 0, true, "error"},
		{"zero status with no transport error", 0, false, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalize.Outcome(tt.status, tt.transportErr); got != tt.want {
				t.Errorf("Outcome(%d, %v) = %q, want %q", tt.status, tt.transportErr, got, tt.want)
			}
		})
	}
}

func TestRowsAffected(t *testing.T) {
	if n, ok := normalize.RowsAffected([]byte(`[1,2,3]`)); !ok || n != 3 {
		t.Errorf("RowsAffected(array) = (%d, %v), want (3, true)", n, ok)
	}
	if _, ok := normalize.RowsAffected([]byte(`{"foo":"bar"}`)); ok {
		t.Errorf("RowsAffected(object) should report ok=false")
	}
	if _, ok := normalize.RowsAffected(nil); ok {
		t.Errorf("RowsAffected(nil) should report ok=false")
	}
	if _, ok := normalize.RowsAffected([]byte(`not json`)); ok {
		t.Errorf("RowsAffected(malformed) should report ok=false")
	}
}

func TestETagAndLastModified(t *testing.T) {
	h := http.Header{}
	h.Set("ETag", `"abc123"`)
	h.Set("Last-Modified", "Sun, 09 Aug 2026 12:00:00 GMT")

	if got := normalize.ETag(h); got != `"abc123"` {
		t.Errorf("ETag = %q", got)
	}
	lm, ok := normalize.LastModified(h)
	if !ok {
		t.Fatal("LastModified: ok = false, want true")
	}
	if lm.Year() != 2026 {
		t.Errorf("LastModified = %v", lm)
	}

	if _, ok := normalize.LastModified(http.Header{}); ok {
		t.Errorf("LastModified with no header should report ok=false")
	}
}
