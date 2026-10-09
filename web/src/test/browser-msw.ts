// Browser-side MSW setup, only loaded when VITE_E2E_MOCK is true.
// main.tsx dynamically imports this module so it never appears in a
// production build (Vite tree-shakes the dead branch).
//
// Reuses the same `handlers` array as the vitest suite — the contract
// the dashboard expects from the API is declared once and exercised
// from both unit tests and Playwright mock-mode runs.
//
// When localStorage.getItem("gameplane-e2e-dataset") === "screenshots",
// uses an enriched handler set with diverse test data for demo/screenshot runs.
// When it is "sso-only", overrides /auth/providers to report no local-login
// provider, so Login.tsx renders its SSO-only branch.

import { http, HttpResponse } from "msw";
import { setupWorker } from "msw/browser";
import { handlers, buildScreenshotHandlers, buildSsoOnlyHandlers } from "./handlers";
import { makeConfig } from "./factories";
import type { TelemetryInfo, TelemetryPreview } from "@/lib/api";

let noticePending = true;
let telemetryInstallId = "3f2b8c1e-5d4a-4e7b-9c21-8a6f0d3e9b12";

function getHandlerSet(): Parameters<typeof setupWorker>[0][] {
  // Wrap in try/catch in case localStorage is unavailable (e.g., sandboxed iframe)
  try {
    if (typeof window !== "undefined") {
      const dataset = window.localStorage.getItem("gameplane-e2e-dataset");
      if (dataset === "screenshots") {
        return buildScreenshotHandlers();
      }
      if (dataset === "sso-only") {
        return buildSsoOnlyHandlers();
      }
      if (dataset === "telemetry-notice") {
        return [
          http.get("/admin/telemetry/notice", () =>
            HttpResponse.json(
              noticePending
                ? { pending: true, destination: { kind: "default", host: "telemetry.example.org" } }
                : { pending: false },
            ),
          ),
          http.post("/admin/telemetry/notice", () => {
            noticePending = false;
            return new HttpResponse(null, { status: 204 });
          }),
          ...handlers,
        ];
      }
      if (dataset === "telemetry-notice-bundled") {
        return [
          http.get("/admin/telemetry/notice", () =>
            HttpResponse.json(
              noticePending
                ? { pending: true, destination: { kind: "bundled", host: "gameplane-telemetry-receiver.gameplane.svc" } }
                : { pending: false },
            ),
          ),
          http.post("/admin/telemetry/notice", () => {
            noticePending = false;
            return new HttpResponse(null, { status: 204 });
          }),
          ...handlers,
        ];
      }
      if (dataset === "telemetry-on") {
        const preview: TelemetryPreview = {
          version: "v0.3.0",
          servers: 3,
          templates: 8,
          ext: {
            schema: 1,
            installId: telemetryInstallId,
            env: {
              k8s: "v1.28.0",
              distro: "ubuntu",
              arch: ["amd64"],
              nodes: "1",
            },
            games: {
              official: { minecraft: 2, terraria: 1 },
              custom: 0,
            },
            features: {
              wakeOnConnect: true,
              tunnels: ["frp"],
              capture: true,
              backups: true,
              sso: false,
              auditForwarding: true,
              clusters: "1",
              db: "sqlite",
              language: "en",
            },
            key: "telemetry-key-preview",
            sentAt: "2026-10-07T09:14:00Z",
          },
        };

        const telemetryInfo: TelemetryInfo = {
          destination: { kind: "default", host: "telemetry.gameplane.net" },
          operatorDisabled: false,
          consent: { basic: true, extended: true, source: "admin" },
          installId: telemetryInstallId,
          preview,
          status: {
            lastAttemptAt: "2026-10-07T09:14:00Z",
            lastSuccessAt: "2026-10-07T09:14:00Z",
            lastOutcome: "ok",
            lastIdRotationAt: null,
          },
        };

        return [
          http.get("/admin/config", () =>
            HttpResponse.json(makeConfig({ telemetry: { sendMetrics: true, extended: true } }))
          ),
          http.get("/admin/telemetry", () => HttpResponse.json(telemetryInfo)),
          http.post("/admin/telemetry/install-id", () => {
            telemetryInstallId = "9c1d7a42-2e6b-4f3a-b8d5-1e0f7c6a4b39";
            telemetryInfo.installId = telemetryInstallId;
            if (telemetryInfo.preview?.ext) {
              telemetryInfo.preview.ext.installId = telemetryInstallId;
            }
            return HttpResponse.json({ installId: telemetryInstallId });
          }),
          ...handlers,
        ];
      }
    }
  } catch {
    // localStorage unavailable; fall through to default
  }
  return handlers;
}

const worker = setupWorker(...getHandlerSet());

export async function startMSW(): Promise<void> {
  await worker.start({
    // Don't error on requests we haven't mocked — pass them through to
    // the real network. Lets us mix mocked auth/list endpoints with a
    // real fetch for things we don't care to mock.
    onUnhandledRequest: "bypass",
    serviceWorker: { url: "/mockServiceWorker.js" },
  });
}
