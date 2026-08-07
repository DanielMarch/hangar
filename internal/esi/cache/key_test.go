package cache

import (
	"net/url"
	"testing"

	"github.com/hangar-project/hangar/internal/i18n"
)

// TestCacheKeyUsesResolvedEsiLanguage — af and en must share one cache
// entry; zh-CN maps to zh (Phase 3 exit criterion, 01_ARCHITECTURE.md
// §5.3: "resolved_esi_language — never the UI locale").
func TestCacheKeyUsesResolvedEsiLanguage(t *testing.T) {
	base := KeyInput{
		Method:            "GET",
		Path:              "/characters/2112625428/",
		CompatibilityDate: "2026-08-04",
		Tenant:            "tranquility",
		TokenSubject:      "CHARACTER:EVE:2112625428",
	}

	resolve := func(ui string) string {
		lang, err := i18n.ResolveESILanguage(ui)
		if err != nil {
			t.Fatal(err)
		}
		return lang
	}

	en := base
	en.ResolvedLanguage = resolve("en")
	af := base
	af.ResolvedLanguage = resolve("af")
	ro := base
	ro.ResolvedLanguage = resolve("ro")

	if Key(en) != Key(af) {
		t.Errorf("en and af must resolve to the same cache key (both -> ESI 'en'); got %s vs %s", Key(en), Key(af))
	}
	if Key(en) != Key(ro) {
		t.Errorf("en and ro must resolve to the same cache key (both -> ESI 'en'); got %s vs %s", Key(en), Key(ro))
	}

	// A genuinely different resolved language must produce a different key.
	zhCN := base
	zhCN.ResolvedLanguage = resolve("zh-CN")
	if zhCN.ResolvedLanguage != "zh" {
		t.Fatalf("zh-CN must resolve to ESI 'zh' (region subtag stripped), got %q", zhCN.ResolvedLanguage)
	}
	if Key(en) == Key(zhCN) {
		t.Error("en and zh-CN resolve to different ESI languages and must not share a cache key")
	}

	// Sanity: the UI locale string itself must never leak into the key —
	// only the resolved ESI language. Building the key directly from the
	// raw UI locale "af" (rather than through i18n.ResolveESILanguage)
	// must NOT equal the correctly-resolved key.
	afRaw := base
	afRaw.ResolvedLanguage = "af"
	if Key(afRaw) == Key(af) {
		t.Error("keying on the raw UI locale must differ from keying on the resolved ESI language — this test's fixture is broken if they match")
	}
}

// TestNormalizationNeverRewritesPathSegments — the singular mining paths
// survive normalisation unchanged (Phase 3 exit criterion,
// 01_ARCHITECTURE.md §5.3).
func TestNormalizationNeverRewritesPathSegments(t *testing.T) {
	paths := []string{
		"/corporation/98000001/mining/extractions",
		"/corporation/98000001/mining/observers",
		"/corporation/98000001/mining/observers/1030569959240",
	}
	for _, p := range paths {
		if got := NormalizePath(p); got != p {
			t.Errorf("NormalizePath(%q) = %q, want unchanged — normalisation must never touch a path segment", p, got)
		}
	}
}

// TestNormalizePathEnvelopeCleanup — the normalisation that IS allowed:
// trailing-slash trim and percent-encoding hex-digit canonicalisation,
// neither of which alters segment content or order.
func TestNormalizePathEnvelopeCleanup(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/characters/2112625428/", "/characters/2112625428"},
		{"/", "/"},                   // root is never trimmed to empty
		{"/foo%2fbar", "/foo%2Fbar"}, // hex digits upper-cased, segment content untouched
	}
	for _, tt := range tests {
		if got := NormalizePath(tt.in); got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeQueryIsDeterministic(t *testing.T) {
	q1 := url.Values{"b": {"2"}, "a": {"1"}}
	q2 := url.Values{"a": {"1"}, "b": {"2"}}
	if NormalizeQuery(q1) != NormalizeQuery(q2) {
		t.Errorf("NormalizeQuery must be order-independent over keys: %q vs %q", NormalizeQuery(q1), NormalizeQuery(q2))
	}

	multi := url.Values{"page": {"2", "1"}}
	if got, want := NormalizeQuery(multi), "page=1&page=2"; got != want {
		t.Errorf("NormalizeQuery(%v) = %q, want %q (values sorted too)", multi, got, want)
	}
}
