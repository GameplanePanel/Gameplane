import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

export default defineConfig({
  testDir: ".",
  testMatch: "monaco.browser.ts",
  timeout: 90_000,
  expect: { timeout: 10_000 },
  reporter: "list",
  use: {
    baseURL: "http://gameplane.test:4175",
    // Map DNS only. Chromium still classifies this HTTP site as untrustworthy.
    launchOptions: { args: ["--host-resolver-rules=MAP gameplane.test 127.0.0.1"] },
    trace: "retain-on-failure",
  },
  webServer: {
    cwd: fileURLToPath(new URL("../../", import.meta.url)),
    command: "npx vite preview --host 127.0.0.1 --port 4175 --strictPort",
    url: "http://127.0.0.1:4175",
    env: { __VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS: "gameplane.test" },
  },
});
