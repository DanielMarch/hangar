import { fileURLToPath, URL } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";

// Project HANGAR SPA build config (SRS §3.1: React 19 / TS 5.9 / Vite 7).
// The build output (dist/) is embedded into the Go binary via embed.FS
// (web/embed.go) — there is no separate web server in production.
//
// tanstackRouter MUST run before @vitejs/plugin-react (its own docs require
// this ordering) — it generates src/routeTree.gen.ts from web/src/routes/**
// and, with autoCodeSplitting on, splits every route's component into its
// own chunk so the 250KB gzipped entry-chunk budget (Phase 16 exit
// criterion) is measured on shell chrome + login, not the whole app.
export default defineConfig({
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
      // 01_ARCHITECTURE.md §13 / SRS v3.1 §4.6 (defect B7): the UI-locale ->
      // ESI-language table has exactly one source file. web/src/lib/locales.ts
      // imports it through this alias rather than a hand-maintained TS copy —
      // the two would drift, and the drift would only surface as an ESI
      // cache-key rejection in production.
      "@i18n/locales.json": fileURLToPath(
        new URL("../internal/i18n/locales.json", import.meta.url),
      ),
    },
  },
  server: {
    fs: {
      // Permits the dev server to read internal/i18n/locales.json, which
      // lives outside web/ (Vite's default fs.allow root).
      allow: [".."],
    },
    proxy: {
      // In dev the SPA is served by Vite on its own port while the Go
      // binary serves the API on :8080 (HANGAR_HTTP_ADDR, default
      // 0.0.0.0:8080) — proxy both so cookies stay same-origin from the
      // browser's point of view. Production has no separate web server:
      // the Go binary serves the built SPA via embed.FS (web/embed.go).
      "/api": "http://localhost:8080",
      "/auth": "http://localhost:8080",
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
    setupFiles: ["./src/test-setup.ts"],
  },
});
