import { fileURLToPath, URL } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";

// Project HANGAR SPA build config (SRS §3.1: React 19 / TS 5.9 / Vite 7).
// The build output (dist/) is embedded into the Go binary via embed.FS
// (web/embed.go) — there is no separate web server in production.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
      // 01_ARCHITECTURE.md §13 / SRS v3.1 §4.6 (defect B7): the UI-locale ->
      // ESI-language table has exactly one source file. web/src/lib/locales.ts
      // imports it through this alias rather than a hand-maintained TS copy —
      // the two would drift, and the drift would only surface as an ESI
      // cache-key rejection in production.
      "@i18n/locales.json": fileURLToPath(new URL("../internal/i18n/locales.json", import.meta.url)),
    },
  },
  server: {
    fs: {
      // Permits the dev server to read internal/i18n/locales.json, which
      // lives outside web/ (Vite's default fs.allow root).
      allow: [".."],
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
  },
  test: {
    environment: "jsdom",
    globals: true,
    passWithNoTests: true,
    css: false,
  },
});
