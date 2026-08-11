// Phase 16 exit criteria `TestESLintBlocksNumberOnISK` and
// `TestESLintBlocksHardcodedEnglish`: lints fixture snippets with the exact
// rule modules web/eslint.config.js wires up (not a re-implementation of
// them), using ESLint's Linter class directly so this runs under `vitest`
// alongside every other unit test rather than needing a second CLI
// invocation.
import i18next from "eslint-plugin-i18next";
import { Linter } from "eslint";
import { parser as tsParser } from "typescript-eslint";
import { describe, expect, it } from "vitest";

// @ts-expect-error — plain JS rule module, no .d.ts (see eslint.config.js)
import noNumberOnIsk from "../../eslint-rules/no-number-on-isk.js";

const linter = new Linter();

const baseLanguageOptions = {
  ecmaVersion: 2022 as const,
  sourceType: "module" as const,
  parser: tsParser,
  parserOptions: { ecmaFeatures: { jsx: true } },
};

describe("local/no-number-on-isk (TestESLintBlocksNumberOnISK)", () => {
  const config: Linter.Config[] = [
    {
      languageOptions: baseLanguageOptions,
      plugins: { local: { rules: { "no-number-on-isk": noNumberOnIsk } } },
      rules: { "local/no-number-on-isk": "error" },
    },
  ];

  it.each([
    "Number(character.iskBalance)",
    "parseFloat(walletIsk)",
    "Number(row.isk_balance)",
    "Number.parseFloat(totalIsk)",
  ])("flags %s", (snippet) => {
    const messages = linter.verify(`const x = ${snippet};`, config);
    expect(messages.some((m) => m.ruleId === "local/no-number-on-isk")).toBe(
      true,
    );
  });

  it.each([
    "Number(pageSize)",
    "parseFloat(percentage)",
    "Number(characterId)",
  ])("does not flag %s", (snippet) => {
    const messages = linter.verify(`const x = ${snippet};`, config);
    expect(messages.some((m) => m.ruleId === "local/no-number-on-isk")).toBe(
      false,
    );
  });
});

describe("i18next/no-literal-string (TestESLintBlocksHardcodedEnglish)", () => {
  const config: Linter.Config[] = [
    {
      files: ["**/*.tsx"],
      languageOptions: baseLanguageOptions,
      plugins: { i18next },
      rules: {
        "i18next/no-literal-string": [
          "error",
          {
            mode: "jsx-text-only",
            "jsx-attributes": { exclude: ["className"] },
          },
        ],
      },
    },
  ];

  it("flags a bare English string literal as JSX text", () => {
    const code = `function C() { return <div>Hello world</div>; }`;
    const messages = linter.verify(code, config, { filename: "fixture.tsx" });
    expect(messages.some((m) => m.ruleId === "i18next/no-literal-string")).toBe(
      true,
    );
  });

  it("does not flag text routed through t()", () => {
    const code = `function C() { return <div>{t("greeting.hello")}</div>; }`;
    const messages = linter.verify(code, config, { filename: "fixture.tsx" });
    expect(messages.some((m) => m.ruleId === "i18next/no-literal-string")).toBe(
      false,
    );
  });

  it("does not flag className or other ignored attributes", () => {
    const code = `function C() { return <div className="flex items-center">{t("x")}</div>; }`;
    const messages = linter.verify(code, config, { filename: "fixture.tsx" });
    expect(messages.some((m) => m.ruleId === "i18next/no-literal-string")).toBe(
      false,
    );
  });
});
