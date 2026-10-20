import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "node:url";
import { captureLocator } from "./capture";

// T191 (specs/done_014-heroui-web-rebuild/tasks.md): Slice 5 — Share links screen
// captures, for comparison against assets/design-export/screenshots/<id>.png per
// contracts/screen-verification.md. Mirrors slice2a.spec.ts's structure
// (viewport, capture() helper, id-named PNGs, dark @screenshots-tagged
// describe block) and is selected the same way — only when
// GAMEPLANE_SCREENSHOTS=1 (via playwright.config.ts's grep/grepInvert on
// the @screenshots tag).
//
// Ten screen ids were designed for this slice
// (specs/done_014-heroui-web-rebuild/contracts/share-link-ui.md): five Settings ·
// Share links frames (xCJlu, dQV9N, atqRh, VM7ro, S7SCDc) and five public
// /share/$token page states (C2LQE4, q31B6w, qFLfB, EcoGD, epZO2).
// qFLfB is a historical view-only design: server phase cannot establish
// permission. Its former inferred-permission scenario now verifies recovery
// against q31B6w. FBiMN captures the transient Start cooldown, keeping five
// reachable public states under browser capture.
//
// T179 mounted ShareLinksSection into Settings.tsx's SECTIONS ("Share
// links" in the vertical Tabs nav). The two full-page Settings frames,
// xCJlu (list) and dQV9N (empty state), are captured by
// all-screens.spec.ts; their stubs below stay skipped only as pointers.
// The three dialog frames (atqRh create, VM7ro created, S7SCDc revoke) are
// captured here with captureLocator() (see ./capture), because each
// assets/design-export/screenshots/<id>.png is a tight modal crop, not a
// full-page frame. This mirrors slice4.spec.ts's Kp48V and slice-3.spec.ts's
// DMnEi. Their reference PNGs are light theme and carry Pencil's drop-shadow
// bleed. See REFERENCE_CROP_ALLOWLIST in web/scripts/compare-screenshots.mjs.
//
// The public route answers a single GET /shares/{token} (and POST
// …/start) with no per-token branching in the shared MSW handlers
// (src/test/handlers.ts always answers "Running"), and editing those
// shared handlers is out of scope for this file — so, like slice5.spec.ts
// (the mock functional spec), each test below installs its own
// window.fetch shim via addInitScript to script the resolve/start
// responses for its one token. See that file's mockShareResolve doc
// comment for why this is the correct technique (page.route cannot see
// traffic MSW's service worker answers in mock mode).

interface ScriptedResponse {
  status: number;
  body?: unknown;
  headers?: Record<string, string>;
}

// Local-time "YYYY-MM-DD" for `days` days from now, matching the format the
// create dialog's native date input expects. Uses getFullYear/getMonth/
// getDate (not toISOString, which would shift the day for negative UTC
// offsets) — mirrors ShareLinks.test.tsx's isoDateNDaysFromNow so both files
// compute this the same way.
function isoDateNDaysFromNow(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

async function mockShareResolve(
  page: Page,
  responses: Record<string, ScriptedResponse | ScriptedResponse[]>,
): Promise<void> {
  await page.addInitScript((responsesJson: string) => {
    const table = JSON.parse(responsesJson) as Record<
      string,
      ScriptedResponse | ScriptedResponse[]
    >;
    const calls: Record<string, number> = {};
    const originalFetch = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      const match = /\/shares\/([^/?]+)(\/start)?/.exec(url);
      if (match) {
        const token = decodeURIComponent(match[1]);
        const key = match[2] ? `${token}:start` : token;
        const entry = table[key];
        if (entry) {
          const steps = Array.isArray(entry) ? entry : [entry];
          const i = Math.min(calls[key] ?? 0, steps.length - 1);
          calls[key] = (calls[key] ?? 0) + 1;
          const step = steps[i];
          return Promise.resolve(
            new Response(step.body !== undefined ? JSON.stringify(step.body) : null, {
              status: step.status,
              headers: { "Content-Type": "application/json", ...step.headers },
            }),
          );
        }
      }
      return originalFetch(input, init);
    };
  }, JSON.stringify(responses));
}

async function capture(page: Page, id: string): Promise<void> {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const screenshotPath = path.join(here, `${id}.png`);
  await page.screenshot({ path: screenshotPath, fullPage: true });
}

// Selects the enriched screenshot dataset before the app's first fetch.
// Mirrors all-screens.spec.ts, whose xCJlu capture reaches the same
// Settings · Share links section this way.
async function useScreenshotDataset(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("gameplane-e2e-dataset", "screenshots");
    } catch {
      // localStorage unavailable (sandboxed) — falls back to default handlers.
    }
  });
}

// Forces the app's own light/dark toggle (AppLayout.tsx THEME_STORAGE_KEY),
// which takes priority over the context's prefers-color-scheme. atqRh,
// VM7ro and S7SCDc were exported from Pencil's light palette, like Kp48V
// and DMnEi. Mirrors slice4.spec.ts's and slice-3.spec.ts's setTheme().
async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.addInitScript((t: string) => {
    try {
      window.localStorage.setItem("gameplane-theme", t);
    } catch {
      // localStorage unavailable (sandboxed)
    }
  }, theme);
}

// Pins POST /servers/{name}:shares to a fixed token. The shared MSW handler
// (src/test/handlers.ts) returns a random token_<base36>. That makes
// CreatedDialog's URL a different length on every run, and in break-all
// monospace a different wrap and dialog height. The token below makes
// `http://localhost:5173/share/<token>` exactly 65 characters, the same
// length as the design's sample URL in VM7ro. It is the same fetch-shim
// technique as mockShareResolve above: page.route cannot see traffic that
// MSW's service worker answers.
const SHOT_SHARE_TOKEN = "8f3ac1e0b2d94f7c9a5e6b7d1c0e2f4a5b6c7";

async function mockShareCreate(page: Page, token: string): Promise<void> {
  await page.addInitScript((t: string) => {
    const originalFetch = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      const method = (init?.method ?? "GET").toUpperCase();
      if (method === "POST" && /\/servers\/[^/?]+:shares(\?|$)/.test(url)) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              id: "share-shot",
              createdAt: "2026-07-28T00:00:00Z",
              expiresAt: null,
              canStart: false,
              token: t,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return originalFetch(input, init);
    };
  }, token);
}

// Exact-match tab click, mirroring slice2b.spec.ts's clickTab(). Used for
// the outer "Settings" tab and for the "Share links" entry in Settings.tsx's
// vertical Tabs list.
async function clickTab(page: Page, name: string): Promise<void> {
  const tab = page.getByRole("tab", { name: new RegExp(`^${name}$`, "i") });
  await expect(tab).toBeVisible({ timeout: 10_000 });
  await tab.click();
}

// Opens /servers/mc-survival → Settings → Share links. mc-survival's mock
// GET :shares returns two rows (share-1, share-2), so the list renders:
// one header "Create link" button and two "Revoke" buttons.
async function openShareLinks(page: Page): Promise<void> {
  await page.goto("/servers/mc-survival");
  await clickTab(page, "Settings");
  await clickTab(page, "Share links");
  await expect(page.getByRole("heading", { name: "Share links", exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByRole("button", { name: /^revoke$/i }).first()).toBeVisible({
    timeout: 10_000,
  });
}

test.describe("Slice 5: Share links — Settings surfaces (Desktop — 1440x900) @screenshots", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
  });

  test.beforeEach(async ({ page }) => {
    await useScreenshotDataset(page);
  });

  // Captured (full page) by all-screens.spec.ts. Kept here as pointers only.
  test.skip("xCJlu: Settings — Share links (list)", async () => {});
  test.skip("dQV9N: Settings — Share links (empty state)", async () => {});

  test("atqRh: Settings — Share links (create dialog)", async ({ page }) => {
    // Design PNG is light theme — see setTheme()'s note.
    await setTheme(page, "light");
    await openShareLinks(page);
    await page.getByRole("button", { name: /^create link$/i }).click();
    const dialog = page.getByRole("dialog", { name: /^create share link for mc-survival$/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    // Default state, matching the design: expiry 7 days, "Allow starting" off.
    await expect(dialog.getByText("Allow starting the server", { exact: true })).toBeVisible();
    await page.waitForTimeout(200);
    await captureLocator(page, "atqRh", dialog);
  });

  test("tr6cE: Settings — Share links (create dialog, no expiry)", async ({ page }) => {
    // Design PNG is light theme — see setTheme()'s note.
    await setTheme(page, "light");
    await openShareLinks(page);
    await page.getByRole("button", { name: /^create link$/i }).click();
    const dialog = page.getByRole("dialog", { name: /^create share link for mc-survival$/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    // Drive the HeroUI Select over to "No expiry" (ShareLinks.test.tsx's
    // pattern): open it (default "30 days") and pick the option.
    const expiryTrigger = dialog.getByRole("button", { name: /30 days/i });
    await expiryTrigger.click();
    const noExpiryOption = await page.getByRole("option", { name: /^no expiry$/i });
    await noExpiryOption.click();
    // FR-002 warning replaces the days-Select's helper area.
    await expect(dialog.getByText("This link works until you revoke it.")).toBeVisible();
    await page.waitForTimeout(200);
    await captureLocator(page, "tr6cE", dialog);
  });

  test("oPF1n: Settings — Share links (create dialog, custom date)", async ({ page }) => {
    // Design PNG is light theme — see setTheme()'s note.
    await setTheme(page, "light");
    await openShareLinks(page);
    await page.getByRole("button", { name: /^create link$/i }).click();
    const dialog = page.getByRole("dialog", { name: /^create share link for mc-survival$/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    // Drive the HeroUI Select over to "Custom".
    const expiryTrigger = dialog.getByRole("button", { name: /30 days/i });
    await expiryTrigger.click();
    const customOption = await page.getByRole("option", { name: /^custom$/i });
    await customOption.click();
    // Any date 365+ days out exercises the OD-6 long-lived warning shown in
    // the design frame; computed relative to the real clock (400 days out),
    // like ShareLinks.test.tsx's isoDateNDaysFromNow, so this doesn't rot
    // once the design's sample date (2027-11-04) is no longer 365+ days
    // away. The captured date text therefore deliberately mismatches the
    // frame's sample date "2027-11-04" — an accepted content-mismatch per
    // contracts/screen-verification.md, same as VM7ro's URL text below.
    await dialog.getByLabel("Expires on").fill(isoDateNDaysFromNow(400));
    await expect(
      dialog.getByText("Long-lived link — it stays valid for over a year unless you revoke it."),
    ).toBeVisible();
    await page.waitForTimeout(200);
    await captureLocator(page, "oPF1n", dialog);
  });

  test("VM7ro: Settings — Share links (created dialog)", async ({ page }) => {
    await setTheme(page, "light");
    await mockShareCreate(page, SHOT_SHARE_TOKEN);
    await openShareLinks(page);
    await page.getByRole("button", { name: /^create link$/i }).click();
    const createDialog = page.getByRole("dialog", {
      name: /^create share link for mc-survival$/i,
    });
    await expect(createDialog).toBeVisible({ timeout: 10_000 });
    // CreateDialog.onSuccess closes this dialog and opens CreatedDialog
    // (ShareLinks.tsx).
    await createDialog.getByRole("button", { name: /^create link$/i }).click();
    const createdDialog = page.getByRole("dialog", { name: /^share link created$/i });
    await expect(createdDialog).toBeVisible({ timeout: 10_000 });
    await expect(createDialog).toBeHidden({ timeout: 10_000 });
    await expect(createdDialog.getByText(SHOT_SHARE_TOKEN, { exact: false })).toBeVisible();
    await page.waitForTimeout(200);
    // The URL text still differs from the design's sample
    // (https://play.gameplane.example/s/…). That is content-mismatch, an
    // accepted verdict per contracts/screen-verification.md. Its length and
    // wrap are held equal by SHOT_SHARE_TOKEN.
    await captureLocator(page, "VM7ro", createdDialog);
  });

  test("S7SCDc: Settings — Share links (revoke dialog)", async ({ page }) => {
    await setTheme(page, "light");
    await openShareLinks(page);
    await page.getByRole("button", { name: /^revoke$/i }).first().click();
    // RevokeDialog renders HeroUI's AlertDialog/AlertDialogDialog
    // (role="alertdialog", not "dialog"), like Kp48V.
    const dialog = page.getByRole("alertdialog", { name: /^revoke this share link\?$/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await captureLocator(page, "S7SCDc", dialog);
  });
});

test.describe("Slice 5: Share links — public page (Desktop — 1440x900) @screenshots", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
  });

  test("C2LQE4: Share page — Up", async ({ page }) => {
    await mockShareResolve(page, {
      "shot-up": {
        status: 200,
        body: {
          serverName: "mc-survival",
          status: "Running",
          address: { host: "play.gameplane.example", port: 25565 },
          playersOnline: 3,
        },
      },
    });
    await page.goto("/share/shot-up");
    await expect(page.getByRole("heading", { name: "mc-survival" })).toBeVisible({
      timeout: 10_000,
    });
    // C2LQE4: use exact:true to match only the Online badge text
    await expect(page.getByText("Online", { exact: true })).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "C2LQE4");
  });

  test("q31B6w: Share page — Asleep, can start before and after a pending wake", async ({ page }) => {
    await mockShareResolve(page, {
      "shot-asleep-start": {
        status: 200,
        body: { serverName: "mc-survival", status: "Suspended" },
      },
      "shot-asleep-start:start": { status: 202, body: {} },
    });
    await page.goto("/share/shot-asleep-start");
    await expect(page.getByRole("heading", { name: "mc-survival" })).toBeVisible({
      timeout: 10_000,
    });
    const startButton = page.getByRole("button", { name: /start server/i });
    await expect(startButton).toBeVisible();
    await expect(startButton).toBeEnabled();
    await startButton.click();
    await expect(page.getByText(/starting up/i)).toBeVisible();
    await expect(page.getByText("Asleep", { exact: true })).toBeVisible({ timeout: 15_000 });
    await expect(startButton).toBeVisible();
    await expect(startButton).toBeEnabled();
    await expect(page.getByText(/check back later/i)).toHaveCount(0);
    // The old qFLfB view-only scenario inferred permission from phase. A
    // still-asleep poll must retain retry, matching the existing q31B6w
    // reference. Capture once after recovery to avoid duplicate output IDs.
    await page.waitForTimeout(200);
    await capture(page, "q31B6w");
  });

  test("FBiMN: Share page — Asleep, Start cooldown", async ({ page }) => {
    await mockShareResolve(page, {
      "shot-start-cooldown": {
        status: 200,
        body: { serverName: "mc-survival", status: "Suspended" },
      },
      "shot-start-cooldown:start": { status: 429, headers: { "Retry-After": "120" } },
    });
    await page.goto("/share/shot-start-cooldown");
    await expect(page.getByRole("heading", { name: "mc-survival" })).toBeVisible({
      timeout: 10_000,
    });
    await page.getByRole("button", { name: /start server/i }).click();
    await expect(page.getByText("Asleep", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Try again shortly", exact: true })).toBeDisabled();
    await page.waitForTimeout(200);
    await capture(page, "FBiMN");
  });

  test("EcoGD: Share page — Starting", async ({ page }) => {
    await mockShareResolve(page, {
      "shot-starting": { status: 200, body: { serverName: "mc-survival", status: "Starting" } },
    });
    await page.goto("/share/shot-starting");
    await expect(page.getByRole("heading", { name: "mc-survival" })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText("Starting...").first()).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "EcoGD");
  });

  test("epZO2: Share page — Invalid/expired", async ({ page }) => {
    await mockShareResolve(page, { "shot-gone": { status: 404 } });
    await page.goto("/share/shot-gone");
    await expect(page.getByRole("heading", { name: /link not available/i })).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "epZO2");
  });
});
