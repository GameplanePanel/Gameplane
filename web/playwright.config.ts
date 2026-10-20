// Playwright configuration for the Gameplane dashboard.
//
// Two run modes selected by GAMEPLANE_E2E_TARGET (default "mock"):
//
//   mock — vite serves the dashboard with VITE_E2E_MOCK=true so MSW
//          intercepts every fetch in the browser. No cluster needed.
//          Used for `npm run test:e2e:mock` (and the CI web-e2e-mock job).
//
//   live — vite proxies fetches to a kubectl port-forward on the
//          gameplane-e2e cluster. Tests authenticate as the admin user
//          bootstrapped by test/e2e/api_auth_e2e_test.go (password
//          handed off via test/e2e/.tmp/admin-password). globalSetup
//          spawns the port-forward; globalTeardown kills it.

import { defineConfig, devices } from "@playwright/test";

const target = (process.env.GAMEPLANE_E2E_TARGET ?? "mock") as "mock" | "live";

// screenshot capture (web/e2e/specs/screenshots.spec.ts, tagged @screenshots)
// is excluded from the regular mock/live runs and selected explicitly by
// `npm run screenshots` (web/package.json), which sets GAMEPLANE_SCREENSHOTS=1 (OD-3b).
//
// A CLI --grep does NOT override a config-level grepInvert: Playwright
// applies project.grepInvert while building the project's test suite
// (runner/loadUtils.js createProjectSuite, unconditional), separately from
// and before the CLI grep/grep-invert matcher built from --grep/--grep-invert
// (same file, createRootSuite's cliTitleMatcher). An unconditional
// `grepInvert: /@screenshots/` here would therefore remove every
// @screenshots-tagged test from the suite before --grep ever runs,
// leaving `npm run screenshots` matching zero tests. So the invert (and,
// symmetrically, the positive grep for the screenshot run) must switch on
// an environment variable set by the npm script, not CLI-sniffing.
const screenshotsRequested = process.env.GAMEPLANE_SCREENSHOTS === "1";

// en-XA run (spec 019 T100): VITE_I18N_PSEUDO=1 (npm run screenshots:pseudo)
// builds the pseudo-language into the dev server and replaces the chromium
// project with an en-XA project over the screenshot suite. The specs keep
// driving the UI in English; e2e/screenshots/capture.ts switches to en-XA for
// each capture (OD-018).
const pseudo = process.env.VITE_I18N_PSEUDO === "1";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // login flow shares cookies; serial keeps it predictable
  forbidOnly: !!process.env.CI,
  grep: screenshotsRequested ? /@screenshots/ : undefined,
  grepInvert: screenshotsRequested ? undefined : /@screenshots/,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "http://localhost:5173",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    // In live mode, every test starts authenticated. globalSetup logs in
    // once and writes the session+csrf cookies to e2e/.auth/storage.json
    // so each per-test browser context inherits the same trust state.
    // Mock mode uses a stub /users/me, so no storage state is needed.
    storageState: target === "live" ? "./e2e/.auth/storage.json" : undefined,
  },
  globalSetup: target === "live" ? "./e2e/globalSetup.ts" : undefined,
  globalTeardown: target === "live" ? "./e2e/globalTeardown.ts" : undefined,
  webServer: {
    // Vite dev server. In mock mode VITE_E2E_MOCK is exposed to the
    // client; in live mode it's stomped to "false" so MSW never starts
    // and the proxy is pinned to the kubectl port-forward globalSetup
    // spawns at the same port. The webServer is spawned BEFORE
    // globalSetup runs, so we can't depend on env vars exported there —
    // instead the port is fixed (18080) at config-load time.
    // In mock mode, use `vite --mode mock` so vite auto-loads .env.mock
    // (which sets VITE_E2E_MOCK=true and exposes it as
    // import.meta.env.VITE_E2E_MOCK in the SPA). Process-env vars don't
    // reach import.meta.env in vite — only .env-file-loaded ones do.
    command:
      target === "mock"
        ? "npx vite --mode mock --port 5173"
        : "npx vite --port 5173",
    url: "http://localhost:5173",
    reuseExistingServer: !process.env.CI && !pseudo,
    timeout: 60_000,
    env: {
      // Spread first — Playwright's webServer.env REPLACES process.env
      // when set (without spread, vite runs without PATH/HOME).
      ...(process.env as Record<string, string>),
      // Live mode: vite.config.ts reads GAMEPLANE_API_URL to point its
      // proxy at the kubectl port-forward globalSetup spawns.
      GAMEPLANE_API_URL:
        target === "live"
          ? (process.env.GAMEPLANE_API_URL ?? "http://localhost:18080")
          : "http://localhost:8000",
    },
  },
  projects: pseudo
    ? [{ name: "en-XA", testDir: "./e2e/screenshots", use: { ...devices["Desktop Chrome"] } }]
    : [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
