// UI locale -> ESI Accept-Language resolution (01_ARCHITECTURE.md §13,
// SRS v3.1 §4.6, defect B7). locales.json is the single source of truth,
// shared verbatim with the Go side (internal/i18n/resolve.go) via the
// "@i18n/locales.json" alias configured in vite.config.ts — this file is a
// typed *reader* of that data, never a second copy of it.
import localesData from "@i18n/locales.json";

export interface Locale {
  ui: string;
  esi: string;
  note?: string;
}

const locales: Locale[] = localesData.locales;

const bySource = new Map(locales.map((l) => [l.ui, l]));

/** Every supported UI locale, in locales.json's declared order. */
export function uiLocales(): string[] {
  return locales.map((l) => l.ui);
}

/**
 * Maps a HANGAR UI locale to the ESI Accept-Language value the gateway
 * actually sends and keys its cache on. Throws on an unrecognised UI
 * locale — every UI locale is defined by this table, so there is no
 * legitimate caller that could pass one this table doesn't know.
 */
export function resolveEsiLanguage(uiLocale: string): string {
  const l = bySource.get(uiLocale);
  if (!l) {
    throw new Error(`i18n: "${uiLocale}" is not a known UI locale (see internal/i18n/locales.json)`);
  }
  return l.esi;
}
