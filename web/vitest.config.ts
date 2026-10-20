import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(here, "./src"),
      "@i18n": path.resolve(here, "../i18n/locales"),
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    // Playwright specs live under e2e/ and use @playwright/test, not
    // vitest. Exclude them so `npx vitest run` doesn't try to load
    // them and choke on test.describe() calls.
    exclude: ["e2e/**", "node_modules/**", "dist/**"],
    coverage: {
      provider: "v8",
      reporter: ["text", "lcov", "json-summary"],
      reportsDirectory: "./coverage",
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        "src/main.tsx",
        "src/router/**",
        "src/**/*.d.ts",
        "src/types.ts",
        "src/test/**",
        "src/styles/**",
        "**/*.test.{ts,tsx}",
        "src/lib/config.ts",
      ],
      // Ratchet up via follow-up PRs as more tests land.
      thresholds: {
        lines: 92,
        functions: 76,
        branches: 82,
        statements: 92,
      },
    },
  },
});
