package i18n

import "testing"

// TestLocaleResolutionExhaustive is the exhaustive table-driven test
// 01_ARCHITECTURE.md §13 / 00_SRS_v3.1.md §4.6 require on both the Go and
// TypeScript sides of locales.json. Every one of the 9 UI locales measured
// in docs/BASELINE.md (af de en fr ja ko ro ru zh-CN) is asserted here by
// name — silently losing a row from locales.json must fail this test, not
// just shrink UILocales().
func TestLocaleResolutionExhaustive(t *testing.T) {
	tests := []struct {
		ui   string
		want string
	}{
		{"en", "en"},
		{"de", "de"},
		{"fr", "fr"},
		{"ja", "ja"},
		{"ko", "ko"},
		{"ru", "ru"},
		{"zh-CN", "zh"}, // region subtag stripped
		{"af", "en"},    // no ESI equivalent — falls back
		{"ro", "en"},    // no ESI equivalent — falls back
	}
	if len(tests) != 9 {
		t.Fatalf("this test table itself must cover all 9 measured UI locales, has %d", len(tests))
	}

	for _, tt := range tests {
		t.Run(tt.ui, func(t *testing.T) {
			got, err := ResolveESILanguage(tt.ui)
			if err != nil {
				t.Fatalf("ResolveESILanguage(%q) returned an error: %v", tt.ui, err)
			}
			if got != tt.want {
				t.Errorf("ResolveESILanguage(%q) = %q, want %q", tt.ui, got, tt.want)
			}
		})
	}

	if got := len(UILocales()); got != 9 {
		t.Errorf("UILocales() returned %d locales, want 9 (docs/BASELINE.md's measured count)", got)
	}
}

// TestUnknownLocaleErrors — an unrecognised UI locale is an error, never a
// silent fallback (unlike af/ro, which are *known* locales that
// deliberately fall back to ESI's en).
func TestUnknownLocaleErrors(t *testing.T) {
	if _, err := ResolveESILanguage("xx"); err == nil {
		t.Error("expected an error for an unrecognised UI locale, got nil")
	}
}

// TestAfRoEnShareOneESILanguage — the cache-key consequence of the
// resolution table: af, ro and en users must resolve to the identical ESI
// language string, byte for byte, so they share one cache entry.
func TestAfRoEnShareOneESILanguage(t *testing.T) {
	en, err := ResolveESILanguage("en")
	if err != nil {
		t.Fatal(err)
	}
	for _, ui := range []string{"af", "ro"} {
		got, err := ResolveESILanguage(ui)
		if err != nil {
			t.Fatal(err)
		}
		if got != en {
			t.Errorf("ResolveESILanguage(%q) = %q, want it to equal ResolveESILanguage(\"en\") = %q", ui, got, en)
		}
	}
}
