// Playwright configuration. `@playwright/test` and a `pnpm run e2e` script
// have been dependencies since Phase 0 with NO suite behind them and no
// phase owning one — which is why Phase 17 had to verify its 60fps
// criterion by proxy. Phase 18 wires the suite up, deliberately SMALL:
// two specs covering the two confirmation flows this phase introduces.
// It is not a retrofit of e2e coverage over Phases 16-17; that remains
// open.
//
// The two flows here are exactly what jsdom verifies weakly. A jsdom test
// can assert `button.disabled === true`; it cannot tell you that a real
// browser refuses the click, that the network stayed quiet, or that the
// gate survives a re-render. Everything else this phase built is verified
// by Go tests (the server halves) and Vitest (the component halves).
//
// This runs against the REAL binary serving the REAL SPA against a REAL,
// seeded Postgres — `go run ./cmd/hangar serve` serves web/dist through
// embed.FS, so there is no Vite dev server and no API stub anywhere in
// this suite. HANGAR_DB_URL must point at a throwaway database; e2e/
// global-setup.ts migrates and seeds it.
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.HANGAR_E2E_PORT ?? 8099);
const baseURL = process.env.HANGAR_E2E_BASE_URL ?? `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup.ts",
  // Serial. The specs mutate one shared, seeded database — the pin is a
  // single app.setting row and the rule set is one platform's — so running
  // them in parallel would have each spec observing the other's writes.
  // Two specs do not need the parallelism.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 30_000,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    // The real binary, from the repo root. `reuseExistingServer` lets a
    // developer keep a server running between runs; CI always starts its
    // own.
    command: `go run ./cmd/hangar serve`,
    cwd: "..",
    // Readiness probe. `/api/v1/openapi.json`, not `/api/v1/openapi` —
    // Huma's OpenAPIPath is a prefix and it serves `.json`/`.yaml` under
    // it, so the bare path 404s and the probe would wait out its full
    // timeout against a server that started fine.
    url: `${baseURL}/api/v1/openapi.json`,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
    stdout: "pipe",
    stderr: "pipe",
    env: {
      HANGAR_HTTP_ADDR: `127.0.0.1:${PORT}`,
      HANGAR_PUBLIC_URL: baseURL,
    },
  },
});
