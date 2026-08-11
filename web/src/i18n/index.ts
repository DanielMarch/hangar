// i18next bootstrap. `internal/i18n/locales.json` is the single source of
// truth for which UI locales exist (SRS v3.1 §4.6, defect B7) — web/src/lib
// /locales.ts already reads it through the "@i18n/locales.json" Vite alias
// for ESI Accept-Language resolution; this module reuses the same
// `uiLocales()` list rather than hand-rolling a second one.
//
// Every entry in `resources` below is a STATIC import, one per UI locale.
// `assertResourcesComplete()` runs at module-eval time (i.e. on every app
// boot and on every test/build that imports this module) and throws if a UI
// locale from locales.json has no matching resource bundle — an unmapped
// locale is a build failure on the TypeScript side, matching the Go side's
// `TestLocaleResolutionExhaustive` (internal/i18n/resolve_test.go).
import i18next from "i18next";
import { initReactI18next } from "react-i18next";

import { uiLocales } from "@/lib/locales";

import af from "./locales/af.json";
import de from "./locales/de.json";
import en from "./locales/en.json";
import fr from "./locales/fr.json";
import ja from "./locales/ja.json";
import ko from "./locales/ko.json";
import ro from "./locales/ro.json";
import ru from "./locales/ru.json";
import zhCN from "./locales/zh-CN.json";

export const resources = {
  en: { common: en },
  de: { common: de },
  fr: { common: fr },
  ja: { common: ja },
  ko: { common: ko },
  ru: { common: ru },
  "zh-CN": { common: zhCN },
  af: { common: af },
  ro: { common: ro },
} satisfies Record<string, { common: unknown }>;

function assertResourcesComplete(): void {
  const missing = uiLocales().filter((locale) => !(locale in resources));
  if (missing.length > 0) {
    throw new Error(
      `i18n: internal/i18n/locales.json declares UI locale(s) [${missing.join(", ")}] with no matching ` +
        `web/src/i18n/locales/*.json resource bundle. Add the bundle and register it in web/src/i18n/index.ts.`,
    );
  }
}
assertResourcesComplete();

void i18next.use(initReactI18next).init({
  resources,
  lng: "en",
  fallbackLng: "en",
  defaultNS: "common",
  interpolation: { escapeValue: false },
  returnNull: false,
});

export default i18next;
