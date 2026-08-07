// Package i18n resolves HANGAR's UI locale set to the distinct, smaller set
// ESI's own Accept-Language enum supports (01_ARCHITECTURE.md §13,
// 00_SRS_v3.1.md §4.6, defect B7). locales.json is the single source of
// truth: embedded into the Go binary here, and imported directly by the
// Vite build (web/vite.config.ts's alias, web/src/lib/locales.ts) — never
// hand-copied. Landing this in Phase 3 rather than Phase 16 (where the
// locale switcher UI actually ships) is deliberate: the ESI cache key
// (internal/esi/cache/key.go) includes the resolved ESI language, so this
// package is a Phase 3 dependency, not a frontend nicety.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed locales.json
var localesFS embed.FS

// Locale is one row of the resolution table.
type Locale struct {
	UI   string `json:"ui"`
	ESI  string `json:"esi"`
	Note string `json:"note,omitempty"`
}

type localesFile struct {
	Locales []Locale `json:"locales"`
}

// Table is the parsed, immutable content of locales.json, loaded once at
// package init. A malformed embedded file is a build-breaking panic, not a
// runtime error — the file is compiled into the binary, so if it doesn't
// parse, nothing downstream can possibly work either.
var Table = mustLoadTable()

// bySource indexes Table by UI locale for O(1) resolution.
var bySource = func() map[string]Locale {
	m := make(map[string]Locale, len(Table))
	for _, l := range Table {
		m[l.UI] = l
	}
	return m
}()

func mustLoadTable() []Locale {
	b, err := localesFS.ReadFile("locales.json")
	if err != nil {
		panic(fmt.Errorf("i18n: reading embedded locales.json: %w", err))
	}
	var f localesFile
	if err := json.Unmarshal(b, &f); err != nil {
		panic(fmt.Errorf("i18n: parsing embedded locales.json: %w", err))
	}
	if len(f.Locales) == 0 {
		panic("i18n: embedded locales.json declares zero locales")
	}
	return f.Locales
}

// ResolveESILanguage maps a HANGAR UI locale to the ESI Accept-Language
// value the gateway must actually send and key its cache on. An unknown UI
// locale is a programmer error (every UI locale is defined by this very
// table — there is no third list to drift from it), so it returns an error
// rather than silently falling back; callers at the HTTP boundary should
// have already rejected an unrecognised UI locale before reaching here.
func ResolveESILanguage(uiLocale string) (string, error) {
	l, ok := bySource[uiLocale]
	if !ok {
		return "", fmt.Errorf("i18n: %q is not a known UI locale (see internal/i18n/locales.json)", uiLocale)
	}
	return l.ESI, nil
}

// UILocales returns every supported UI locale, in locales.json's declared
// order — the closed set the frontend's locale switcher (Phase 16) and any
// input-validating HTTP middleware (Phase 15) both enumerate from this one
// file rather than a second, hand-maintained list.
func UILocales() []string {
	out := make([]string, len(Table))
	for i, l := range Table {
		out[i] = l.UI
	}
	return out
}
