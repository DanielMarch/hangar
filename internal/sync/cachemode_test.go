package sync_test

import (
	"testing"

	"github.com/hangar-project/hangar/internal/sync"
)

// TestCacheModePolicyTableDriven (roadmap exit criterion): all four §6.2
// cases, including absent -> ttl-based (both the documented default AND
// the fallback for any value this build has never seen).
func TestCacheModePolicyTableDriven(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want sync.CacheMode
	}{
		{"absent header defaults to ttl-based", "", sync.CacheModeTTLBased},
		{"explicit ttl-based", "ttl-based", sync.CacheModeTTLBased},
		{"event-based", "event-based", sync.CacheModeEventBased},
		{"no-cache (architecture doc's name for the concept)", "no-cache", sync.CacheModeNoCache},
		{"not-cached (the live ESI spec's actual wire value)", "not-cached", sync.CacheModeNoCache},
		{"unrecognised future value falls back to ttl-based", "some-future-mode-ccp-invents", sync.CacheModeTTLBased},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sync.ParseCacheMode(tt.raw); got != tt.want {
				t.Errorf("ParseCacheMode(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
