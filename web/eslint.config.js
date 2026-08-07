import js from "@eslint/js";
import i18next from "eslint-plugin-i18next";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

// Flat ESLint config (ESLint 9). `pnpm run lint` runs with --max-warnings=0
// (Makefile `lint` target), so every rule enabled here is a hard build gate.
export default tseslint.config(
  { ignores: ["dist", "src/api/schema.d.ts"] },
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
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
      // eslint-plugin-i18next's literal-string rule is enabled once
      // web/src/i18n lands (Phase 3, SRS §13 / defect B7) — the plugin is
      // registered here so that phase only needs to flip a rule on.
    },
  },
);
