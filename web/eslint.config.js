import js from "@eslint/js";
import i18next from "eslint-plugin-i18next";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

import noNumberOnIsk from "./eslint-rules/no-number-on-isk.js";

// Flat ESLint config (ESLint 9). `pnpm run lint` runs with --max-warnings=0
// (Makefile `lint` target), so every rule enabled here is a hard build gate.
export default tseslint.config(
  { ignores: ["dist", "src/api/schema.d.ts", "src/routeTree.gen.ts"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      i18next,
      local: { rules: { "no-number-on-isk": noNumberOnIsk } },
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
      // Phase 16 exit criterion `TestESLintBlocksNumberOnISK`: money is a
      // JSON string on the wire everywhere (Principle 9); Number()/
      // parseFloat() on anything ISK-named silently reintroduces float
      // imprecision. See web/eslint-rules/no-number-on-isk.js.
      "local/no-number-on-isk": "error",
    },
  },
  {
    // shadcn/ui convention (SRS §8.1 vendoring): each primitive file
    // exports both the component and its `cva` variants function
    // (`buttonVariants`, `badgeVariants`, ...) so callers can compose them.
    // That's an intentional, permanent shape for these files, not a
    // fast-refresh smell — react-refresh/only-export-components doesn't
    // apply here.
    files: ["src/components/ui/**/*.tsx"],
    rules: { "react-refresh/only-export-components": "off" },
  },
  {
    // Phase 16 exit criterion `TestESLintBlocksHardcodedEnglish`: every
    // user-facing string in a component must route through i18next
    // (t()/<Trans>), never a bare literal. Scoped to .tsx only — .ts files
    // (stores, api client, lib helpers) legitimately contain string
    // literals (query keys, cookie names, class names) that are not UI copy.
    // *.test.tsx is exempt: test fixtures assert against literal English on
    // purpose (mocked API rows, expected DOM text) and are never shipped.
    files: ["**/*.tsx"],
    ignores: ["**/*.test.tsx"],
    plugins: { i18next },
    rules: {
      "i18next/no-literal-string": [
        "error",
        {
          mode: "jsx-text-only",
          "should-validate-template": true,
          "jsx-attributes": {
            exclude: [
              "className",
              "style",
              "type",
              "key",
              "id",
              "width",
              "height",
              "data-testid",
              "data-slot",
              "htmlFor",
              "rel",
              "target",
              "role",
              "name",
              "to",
              "href",
              "viewBox",
              "xmlns",
              "d",
              "fill",
              "stroke",
              "strokeWidth",
              "strokeLinecap",
              "strokeLinejoin",
            ],
          },
          callees: {
            exclude: [
              "i18n(ext)?",
              "t",
              "require",
              "addEventListener",
              "removeEventListener",
              "postMessage",
              "getElementById",
              "dispatch",
              "commit",
              "includes",
              "indexOf",
              "endsWith",
              "startsWith",
              "cn",
              "cva",
              "clsx",
            ],
          },
        },
      ],
    },
  },
);
