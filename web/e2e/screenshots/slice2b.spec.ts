import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "node:url";

// T118 (specs/done_014-heroui-web-rebuild/tasks.md): Slice 2b — Mods/Modpacks/
// Backups tabs, every Settings sub-tab, and the backup detail drawer.
// Screenshot verification tests for the HeroUI rebuild, capturing the
// design frames listed in assets/design-export/MANIFEST.md's "Incremental export
// 2026-09-05 — Slice 2b design wave" section at 1440px, for comparison
// against assets/design-export/screenshots/<id>.png per
// specs/done_014-heroui-web-rebuild/contracts/screen-verification.md.
//
// Mirrors slice2a.spec.ts's structure (viewport, capture() helper,
// useScreenshotDataset, id-named PNGs) and is selected the same way — only
// when GAMEPLANE_SCREENSHOTS=1 (via playwright.config.ts's grep/grepInvert
// on the @screenshots tag).
//
// ID mapping to design-export (from assets/design-export/MANIFEST.md's Slice 2b
// table):
//   Mods tab          -> sZtDi  (Screen/Server Detail — Mods)
//   Modpacks tab      -> tY6RD  (Screen/Server Detail — Modpacks)
//   Backups tab       -> pssCT  (Screen/Server Detail — Backups)
//   Settings/General       -> uCA23  (Settings · General)
//   Settings/Version       -> VctzT  (Settings · Version)
//   Settings/Resources     -> VfB0Y  (Settings · Resource Limits)
//   Settings/Networking    -> J5pjJ3 (Settings · Networking)
//   Settings/EnvVars       -> iLm38  (Settings · Environment)
//   Settings/Lifecycle     -> i1bLR  (Settings · Restart policy — closest
//                              existing export; LifecycleSection covers
//                              restart behavior + idle sleep)
//   Settings/Backups       -> KaRFX  (Settings · Scheduled Backups)
//   Settings/NetworkCapture -> RodrS (Settings · Network Capture)
//   Settings/Placement     -> Y5cmvI (Settings · Placement)
//   Settings/Access        -> QpEvu  (Settings · Access)
//   Settings/Danger        -> XR0f9  (Settings · Danger)
//   Backup detail drawer (open) -> zhLZN (Slice 3's Restore Backup — Detail
//                              Drawer export; same component, BackupDetailDrawer)
//
// Two screenshot-dataset fixture gaps blocked this task and were closed
// additively (see their own comments in screenshotData.ts/handlers.ts):
// no template declared a version catalog (Settings/Version never
// rendered), no registry provider declared `modpacks` (Modpacks tab never
// rendered a provider), and the two screenshot-mode /backups fixture
// entries carried spec.serverRef "alpha" despite metadata.name reading
// "test-server-01-…" (BackupsTab filters by serverRef, so the per-server
// Backups tab always rendered empty).

async function capture(page: Page, id: string): Promise<void> {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const screenshotPath = path.join(here, `${id}.png`);
  await page.screenshot({ path: screenshotPath, fullPage: true });
}

// Selects the enriched screenshot dataset before the app's first fetch.
async function useScreenshotDataset(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("gameplane-e2e-dataset", "screenshots");
    } catch {
      // localStorage unavailable (sandboxed) — falls back to default handlers.
    }
  });
}

async function clickTab(page: Page, name: string): Promise<void> {
  const tab = page.getByRole("tab", { name: new RegExp(`^${name}$`, "i") });
  await expect(tab).toBeVisible({ timeout: 10_000 });
  await tab.click();
}

test.describe("Slice 2b: Mods/Modpacks/Backups + Settings (Desktop — 1440x900) @screenshots", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
  });

  test.beforeEach(async ({ page }) => {
    await useScreenshotDataset(page);
  });

  test("sZtDi: Server Detail — Mods", async ({ page }) => {
    // test-server-02 (minecraft-modded) declares capabilities.mods, which is
    // what makes the Mods tab visible (see screenshotData.ts). The test
    // captures the empty mods state (matching design frame sZtDi which shows
    // "0 installed" / "No mods installed.").
    await page.goto("/servers/test-server-02");
    await expect(page.getByRole("heading", { name: "test-server-02" })).toBeVisible({
      timeout: 10_000,
    });
    await clickTab(page, "Mods");
    await expect(page.getByText("No mods installed.")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "sZtDi");
  });

  test("tY6RD: Server Detail — Modpacks", async ({ page }) => {
    // Same template's registry.providers[].modpacks (added for this task)
    // is what makes the Modpacks tab visible.
    // Freeze the clock so the header's formatUptime(status.startedAt) reads
    // "up 13d 4h" (assets/design-export/json/tY6RD.json). test-server-02's
    // startedAt is 2026-09-03T14:20:00Z (screenshotData.ts); any fixed "now"
    // in [2026-09-16T18:20:00Z, 2026-09-16T19:20:00Z) floors to 13d 4h.
    // setFixedTime must run before navigation so the first render sees it.
    await page.clock.setFixedTime(new Date("2026-09-16T18:30:00Z"));
    await page.goto("/servers/test-server-02");
    await clickTab(page, "Modpacks");
    await expect(page.getByText(/browse modpacks/i)).toBeVisible({ timeout: 10_000 });
    // Drop the click's focus ring and hover state: the design shows the
    // selected tab at rest.
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "tY6RD");
  });

  test("pssCT: Server Detail — Backups", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Backups");
    await expect(page.getByRole("rowheader", { name: "mc-survival-nightly-0713" })).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "pssCT");
  });

  test("zhLZN: Backup detail drawer (open)", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Backups");
    const row = page.getByRole("row", { name: /mc-survival-nightly-0713/i });
    await expect(row).toBeVisible({ timeout: 10_000 });
    await row.click();
    // zhLZN is a duplicate with slice-3.spec.ts:184 — keep the capture in
    // slice-3 (the working one) and remove this duplicate here. The drawer
    // may not open reliably in the per-server context, so we skip the
    // capture call.
    await expect(page.getByRole("heading", { name: /backup details/i })).toBeVisible({
      timeout: 10_000,
    });
  });

  test("VfB0Y: Server Detail — Settings · Resources", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Resources");
    await expect(page.getByText("Memory (GiB)")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "VfB0Y");
  });

  test("J5pjJ3: Server Detail — Settings · Networking", async ({ page }) => {
    // J5pjJ3's design frame (assets/design-export/MANIFEST.md:64, :441) is a
    // documentation-style composite stacking five AddressAssignment status
    // treatments (plus ignored/no-manager alert states) vertically in one
    // 2880x4140 export — a single live render can only ever show one of
    // those states at a time. This test captures that one rendered state
    // (the current mc-survival assignment) full-page; it is not expected
    // to pixel-match the multi-state composite 1:1, and switching to
    // captureLocator would not close that gap (element-capture-plan.md,
    // J5pjJ3 section) — a maintainer call on the compare contract is needed
    // before spending more effort here.
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Networking");
    // Provider field only renders when tunnel is enabled in the spec. Use
    // Expose field (always present) to confirm the Networking section loaded.
    await expect(page.getByText(/^expose$/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "J5pjJ3");
  });

  test("i1bLR: Server Detail — Settings · Lifecycle", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Lifecycle");
    await expect(page.getByLabel(/enable idle auto-sleep/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    // Use custom viewport height to match design reference (1620px at 1x)
    await page.setViewportSize({ width: 1440, height: 1620 });
    // Take screenshot with the larger viewport
    await page.screenshot({ path: "e2e/screenshots/i1bLR.png", animations: "disabled" });
  });

  test("KaRFX: Server Detail — Settings · Scheduled backups", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Scheduled backups");
    await expect(page.getByLabel(/enable scheduled backups/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "KaRFX");
  });

  test("RodrS: Server Detail — Settings · Network capture", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Network capture");
    await expect(page.getByLabel(/enable capture/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "RodrS");
  });

  test("Y5cmvI: Server Detail — Settings · Placement", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Placement");
    // PlacementSection is lazy-loaded (Suspense) — wait for its Monaco-based
    // editors to actually mount rather than a fixed timeout.
    // Y5cmvI: use exact:true to match only the label, not the description
    await expect(page.getByText("Tolerations", { exact: true })).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "Y5cmvI");
  });

  test("uCA23: Server Detail — Settings · General", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    // General is the default-selected section — no click needed, but
    // click it anyway so this test doesn't depend on that default.
    await clickTab(page, "General");
    await expect(page.getByPlaceholder(/long-standing survival realm/i)).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "uCA23");
  });

  test("VctzT: Server Detail — Settings · Version", async ({ page }) => {
    // mc-survival's template (minecraft-java) declares a version
    // catalog (added for this task) — required for the Version tab to
    // appear at all (Settings.tsx filters it out otherwise).
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Version");
    // "Game version" text appears as both a heading and a radiogroup aria-label;
    // scope to the heading to avoid strict mode violation.
    await expect(page.getByRole("heading", { name: /game version/i })).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "VctzT");
  });

  test("iLm38: Server Detail — Settings · Environment", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Environment");
    // mc-survival has no spec.env fixture — EnvVarsSection renders its
    // empty state (the "Environment variables" table only mounts once
    // env.length > 0).
    await expect(page.getByText(/no environment variables/i)).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "iLm38");
  });

  test("QpEvu: Server Detail — Settings · RBAC & access", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "RBAC & access");
    await expect(page.getByText(/^owner$/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "QpEvu");
  });

  test("XR0f9: Server Detail — Settings · Danger zone", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Danger zone");
    await expect(page.getByText("Delete server")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "XR0f9");
  });
});
