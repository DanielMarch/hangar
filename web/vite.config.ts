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
