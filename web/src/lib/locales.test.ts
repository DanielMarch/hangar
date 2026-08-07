import { describe, expect, it } from "vitest";

import { resolveEsiLanguage, uiLocales } from "./locales";

// Exhaustive table-driven test mirroring internal/i18n/resolve_test.go's
// TestLocaleResolutionExhaustive — both sides run the same assertions over
// the same locales.json (01_ARCHITECTURE.md §13 / SRS v3.1 §4.6, defect B7).
describe("resolveEsiLanguage", () => {
  const table: Array<[string, string]> = [
    ["en", "en"],
    ["de", "de"],
    ["fr", "fr"],
    ["ja", "ja"],
    ["ko", "ko"],
    ["ru", "ru"],
    ["zh-CN", "zh"], // region subtag stripped
    ["af", "en"], // no ESI equivalent — falls back
    ["ro", "en"], // no ESI equivalent — falls back
  ];

  it("covers all 9 measured UI locales (docs/BASELINE.md)", () => {
    expect(table).toHaveLength(9);
    expect(uiLocales()).toHaveLength(9);
  });

  it.each(table)("resolves %s -> %s", (ui, want) => {
    expect(resolveEsiLanguage(ui)).toBe(want);
  });

  it("throws on an unrecognised UI locale", () => {
    expect(() => resolveEsiLanguage("xx")).toThrow();
  });

  it("af, ro and en share one resolved ESI language (and thus one cache entry)", () => {
    const en = resolveEsiLanguage("en");
    expect(resolveEsiLanguage("af")).toBe(en);
    expect(resolveEsiLanguage("ro")).toBe(en);
  });
});
