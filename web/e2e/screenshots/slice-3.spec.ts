import { test, expect, type Page } from "@playwright/test";
import { capture as sharedCapture, captureLocator } from "./capture";

// T137 (specs/done_014-heroui-web-rebuild/tasks.md): Slice 3 — Onboarding flow
// (Create Server wizard steps 1-5), Modules Catalog, and Backups
// (Index/Schedules/Restores + detail drawer/restore dialog/list item).
// Screenshot verification tests for the HeroUI rebuild, capturing the 12
// design frames listed in assets/design-export/MANIFEST.md's "Incremental export
// 2026-09-05 — Slice 3 design wave" section at 1440px, for comparison
// against assets/design-export/screenshots/<id>.png per
// specs/done_014-heroui-web-rebuild/contracts/screen-verification.md.
//
// Mirrors slice2a.spec.ts's structure (viewport, capture() helper,
// id-named PNGs, GAMEPLANE_SCREENSHOTS=1 gating via playwright.config.ts's
// grep/grepInvert on the @screenshots tag) and reuses the same
// "screenshots" MSW dataset (web/src/test/screenshotData.ts,
// buildScreenshotHandlers() in web/src/test/handlers.ts) so sample data
// (server names, phases, counts) matches the redrawn frames.
//
// One additive fixture change backs this spec: the minecraft-java entry in
// screenshotTemplates (web/src/test/screenshotData.ts) gained a
// spec.versions catalog, because no existing screenshot template declared
// one and the wizard's "Version" step (CreateServer.tsx's stepsFor()) only
// appears when a template has versions — see the comment at that fixture.

// Full-page capture for this spec's routed-screen ids. Delegates to the
// shared web/e2e/screenshots/capture.ts helper (viewport sized to match the
// reference design frame, animations disabled) instead of a local
// `fullPage: true` screenshot — was a local duplicate with a different sizing
// strategy; consolidated so every capture in this file goes through one
// path. The element-crop variant (dialogs/drawers/rows) is `captureLocator`,
// imported directly from `./capture` above — see its per-id use below.
// All full-screen ids in this file now capture through this one shared
// viewport-sizing helper (capture.ts's `capture()`, which resizes the
// browser viewport to match the reference frame's dimensions before
// screenshotting) rather than any per-spec sizing strategy.
async function capture(page: Page, id: string): Promise<void> {
  await sharedCapture(page, id);
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

// Forces the app's own light/dark toggle (AppLayout.tsx THEME_STORAGE_KEY),
// which takes priority over the context's prefers-color-scheme — mirrors
// slice4.spec.ts's setTheme() helper, used the same way here: some
// component-crop design PNGs (E9EEv0, DMnEi) were exported from Pencil's
// light palette while the rest of this suite stays on the suite's dark
// colorScheme (test.use() below).
async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.addInitScript((t: string) => {
    try {
      window.localStorage.setItem("gameplane-theme", t);
    } catch {
      // localStorage unavailable (sandboxed)
    }
  }, theme);
}

test.describe("Slice 3: Create Server, Modules, Backups (Desktop — 1440x900) @screenshots", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
  });

  test.beforeEach(async ({ page }) => {
    await useScreenshotDataset(page);
  });

  test("W8idqY: Create Server — Step 1 (Template)", async ({ page }) => {
    await page.goto("/servers/new");
    await expect(page.getByText(/new game server/i)).toBeVisible({ timeout: 10_000 });
    // screenshotTemplates seeds 8 templates; the first is Minecraft Java Edition.
    await expect(
      page.getByRole("button", { name: /Minecraft Java Edition/i }),
    ).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "W8idqY");
  });

  test("nNL3E: Create Server — Step 2 Version", async ({ page }) => {
    await page.goto("/servers/new");
    await page.getByRole("button", { name: /Minecraft Java Edition/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    // Only reachable because the minecraft-java fixture now declares
    // spec.versions (T137 additive fixture change) — stepsFor() inserts the
    // "version" step for a template with a version catalog.
    await expect(page.getByText(/choose a version/i)).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole("button", { name: /1\.21 \(Vanilla\)/i })).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "nNL3E");
  });

  test("vUqMl: Create Server — Step 3 Configure", async ({ page }) => {
    await page.goto("/servers/new");
    await page.getByRole("button", { name: /Minecraft Java Edition/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    await page.getByRole("button", { name: /1\.21 \(Vanilla\)/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    // The label "Server name" appears in the step, scoped to just the label (not the error message)
    await expect(page.locator("label:has-text('Server name')")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByPlaceholder(/mc-hardcore/i)).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "vUqMl");
  });

  test("QQtUD: Create Server — Step 4 Network (as built)", async ({ page }) => {
    await page.goto("/servers/new");
    await page.getByRole("button", { name: /Minecraft Java Edition/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    await page.getByRole("button", { name: /1\.21 \(Vanilla\)/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    await page.getByPlaceholder(/mc-hardcore/i).fill("mc-survival");
    await page.getByRole("button", { name: /continue/i }).click();
    await expect(page.getByText(/^expose$/i)).toBeVisible({ timeout: 10_000 });
    // Fill in address pool and requested address to trigger the warning alerts
    await page.getByPlaceholder("pool-us-west").fill("pool-us-west");
    await page.getByPlaceholder("203.0.113.50").fill("203.0.113.50");
    await page.waitForTimeout(200);
    await capture(page, "QQtUD");
  });

  test("UMJli: Create Server — Step 5 Review", async ({ page }) => {
    await page.goto("/servers/new");
    await page.getByRole("button", { name: /Minecraft Java Edition/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    await page.getByRole("button", { name: /1\.21 \(Vanilla\)/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    await page.getByPlaceholder(/mc-hardcore/i).fill("e2e-screenshot-srv");
    await page.getByRole("button", { name: /continue/i }).click();
    await page.getByRole("button", { name: /continue/i }).click();
    await expect(page.getByRole("button", { name: /create server/i })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator("span.font-mono:has-text('e2e-screenshot-srv')")).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "UMJli");
  });

  test("kK8Ji: Modules Catalog", async ({ page }) => {
    await page.goto("/modules");
    await expect(page.getByRole("heading", { name: /^modules$/i })).toBeVisible({
      timeout: 10_000,
    });
    // The catalog fixture (buildScreenshotHandlers' /modules/catalog) seeds
    // an uninstalled Minecraft entry and an installed Valheim entry.
    const grid = page.locator('[data-testid="modules-grid"]');
    await expect(grid.getByText("Minecraft (Vanilla)", { exact: true })).toBeVisible({
      timeout: 10_000,
    });
    await expect(grid.getByText("Valheim", { exact: true })).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "kK8Ji");
  });

  test("DPrYX: Backups — Index", async ({ page }) => {
    await page.goto("/backups");
    await expect(page.getByRole("heading", { name: /^backups$/i })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText("mc-survival-nightly-0713")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText("mc-survival-nightly-0712")).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "DPrYX");
  });

  test("fK8Bi: Backups — Schedules", async ({ page }) => {
    await page.goto("/backups");
    await expect(page.getByRole("heading", { name: /^backups$/i })).toBeVisible({
      timeout: 10_000,
    });
    await clickTab(page, "Schedules");
    // Screenshot dataset's schedules (screenshotSchedules), not the
    // "alpha-daily" default-dataset fixture used by e2e/specs/backups.spec.ts.
    await expect(page.getByText("test-server-01-daily")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "fK8Bi");
  });

  test("tTSdi: Backups — Restores", async ({ page }) => {
    await page.goto("/backups");
    await expect(page.getByRole("heading", { name: /^backups$/i })).toBeVisible({
      timeout: 10_000,
    });
    await clickTab(page, "Restores");
    await expect(page.getByText("restore-test-server-01-1")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "tTSdi");
  });

  test("zhLZN: Backup Detail Drawer", async ({ page }) => {
    // Design PNG is light theme — see setTheme()'s note.
    await setTheme(page, "light");
    // Freeze the clock so BackupDetailDrawer's formatRelative(startTime) /
    // formatRelative(completionTime) read "3h ago" (assets/design-export/json/
    // zhLZN.json) instead of drifting with the real wall clock. The
    // mc-survival-nightly-0713 fixture (handlers.ts) has startTime
    // 2026-07-13T00:12:04Z and completionTime 2026-07-13T00:14:41Z — 2m37s
    // apart — so any fixed "now" in [2026-07-13T03:14:41Z,
    // 2026-07-13T04:12:04Z) floors both to 3h. setFixedTime must run before
    // navigation so the drawer's first render already sees it.
    await page.clock.setFixedTime(new Date("2026-07-13T03:20:00Z"));
    const listed = page.waitForResponse((response) => new URL(response.url()).pathname === "/fleet/backups");
    await page.goto("/backups");
    const fleetResponse = await listed;
    expect(fleetResponse.ok()).toBe(true);
    const fleet = await fleetResponse.json();
    expect(fleet.items).toContainEqual(expect.objectContaining({
      target: expect.objectContaining({ cluster: "local", namespace: "default", name: "mc-survival-nightly-0713" }),
    }));
    // The cell now includes its location beside the name. Select the complete
    // resource identity so the intended populated drawer remains unambiguous.
    const row = page.getByRole("row").filter({ hasText: "mc-survival-nightly-0713" }).filter({ hasText: "local / default" });
    await expect(row).toBeVisible({ timeout: 10_000 });
    const detailLoaded = page.waitForResponse((response) => new URL(response.url()).pathname === "/backups/mc-survival-nightly-0713");
    await row.getByRole("rowheader").click();
    const detailResponse = await detailLoaded;
    expect(detailResponse.ok()).toBe(true);
    const detailURL = new URL(detailResponse.url());
    expect(detailURL.searchParams.get("cluster") ?? "local").toBe("local");
    expect(detailURL.searchParams.get("namespace")).toBe("default");
    await expect(page.getByText(/backup details/i)).toBeVisible({ timeout: 10_000 });
    // BackupDetailDrawer.tsx now uses HeroUI's Drawer.Heading (slot="title"),
    // which wires aria-labelledby — enabling accessible-name scoped capture.
    const dialog = page.getByRole("dialog", { name: /backup details/i });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("a1b2c3d4e5f6", { exact: true })).toBeVisible();
    await expect(dialog.getByText("1.4 GiB", { exact: true })).toBeVisible();
    await captureLocator(page, "zhLZN", dialog);
  });

  test("E9EEv0: Restore Backup dialog", async ({ page }) => {
    // Design PNG is light theme — see setTheme()'s note.
    await setTheme(page, "light");
    await page.goto("/backups");
    const row = page.getByRole("row", { name: /mc-survival-nightly-0713/i });
    await expect(row).toBeVisible({ timeout: 10_000 });
    // mc-survival-nightly-0713 (default makeBackup()) is Succeeded with a
    // snapshotID, so BackupRow's "Restore" action is enabled on it.
    await row.getByRole("button", { name: /^restore$/i }).click();
    // RestoreDialog.tsx uses HeroUI's ModalHeading (slot="title"), which does
    // wire aria-labelledby — so, unlike zhLZN's drawer below, this dialog can
    // be scoped by accessible name for an element-level capture.
    const dialog = page.getByRole("dialog", { name: /restore backup/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/restore backup/i)).toBeVisible();
    await page.waitForTimeout(200);
    await captureLocator(page, "E9EEv0", dialog);
  });

  test("DMnEi: Add module source dialog", async ({ page }) => {
    // Design PNG is light theme — see setTheme()'s note.
    await setTheme(page, "light");
    // DMnEi's design panel shows the whole 480px dialog (~881 CSS px tall)
    // with every field visible. At the suite's 1440x900 viewport, ModalBody's
    // max-h-[80vh] (SourceDialog.tsx) scrolls the body and captureLocator
    // screenshots only the visible box, cutting off Allow list / Refresh
    // interval. Give only this test a taller viewport so the form fits.
    await page.setViewportSize({ width: 1440, height: 1200 });
    // #376 resolved: assets/design-export/screenshots/DMnEi.png is genuinely the
    // "Add module source" dialog (Gameplane/Dialog/Add Module Source,
    // (-17205,28535)) — MANIFEST.md mislabeled the node "Gameplane/Backup
    // List Item". This test now captures what the reference actually shows.
    // ModuleSourcesPanel is rendered in AdminSettings only when the section
    // state is 'modules' (default is 'general'), so navigate to /admin,
    // click the nav button to switch to the Module sources section, then
    // click the Add source button.
    await page.goto("/admin");
    await page.getByRole("button", { name: /^Module sources$/i }).click();
    await page.getByRole("button", { name: /add source/i }).click();
    const dialog = page.getByRole("dialog", { name: /add module source/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await captureLocator(page, "DMnEi", dialog);
  });
});
