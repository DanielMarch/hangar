import { describe, expect, it } from "vitest";

import { resources } from "./index";

// PHASE 18. Nothing enforced key parity across the nine locale bundles
// before this — they simply happened to agree, because each phase that
// added copy remembered to add it nine times. Phase 18 adds ~120 keys at
// once, which is where "remembered to" stops being a mechanism.
//
// A missing key does not throw at runtime: i18next falls back to `en`, so
// one forgotten bundle ships a screen silently half in English and nobody
// notices until a user reports it. That is exactly the class of defect a
// build gate is cheaper than.
//
// This is the TypeScript-side sibling of internal/i18n's
// TestLocaleResolutionExhaustive, which covers the ESI Accept-Language
// mapping (the other half of SRS v3.1 §4.6).

function keyPaths(value: unknown, prefix = ""): string[] {
  if (value === null || typeof value !== "object") return [prefix];
  return Object.entries(value as Record<string, unknown>).flatMap(([k, v]) =>
    keyPaths(v, prefix ? `${prefix}.${k}` : k),
  );
}

describe("locale bundles", () => {
  const reference = keyPaths(resources.en.common).sort();
  const locales = Object.keys(resources) as (keyof typeof resources)[];

  it("covers all 9 measured UI locales", () => {
    expect(locales).toHaveLength(9);
  });

  it.each(locales)("%s has exactly the keys en has", (locale) => {
    const keys = keyPaths(resources[locale].common).sort();
    const missing = reference.filter((k) => !keys.includes(k));
    const extra = keys.filter((k) => !reference.includes(k));
    expect({ missing, extra }).toEqual({ missing: [], extra: [] });
  });

  it.each(locales)("%s has no empty translations", (locale) => {
    const empties: string[] = [];
    const walk = (value: unknown, prefix: string) => {
      if (value !== null && typeof value === "object") {
        for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
          walk(v, prefix ? `${prefix}.${k}` : k);
        }
        return;
      }
      if (typeof value !== "string" || value.trim() === "") empties.push(prefix);
    };
    walk(resources[locale].common, "");
    expect(empties).toEqual([]);
  });

  it.each(locales)("%s uses the same interpolation variables as en", (locale) => {
    // A translation that drops `{{count}}` renders a sentence with a hole
    // in it; one that invents `{{total}}` renders the placeholder
    // verbatim. Both look like a typo to a user and neither fails a build
    // without this.
    const vars = (s: unknown): string[] =>
      typeof s === "string"
        ? [...s.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort()
        : [];
    const read = (bundle: unknown, path: string): unknown =>
      path
        .split(".")
        .reduce<unknown>(
          (acc, k) =>
            acc && typeof acc === "object"
              ? (acc as Record<string, unknown>)[k]
              : undefined,
          bundle,
        );

    const mismatched = reference.filter((key) => {
      const want = vars(read(resources.en.common, key));
      const got = vars(read(resources[locale].common, key));
      return want.join(",") !== got.join(",");
    });
    expect(mismatched).toEqual([]);
  });
});
