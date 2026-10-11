import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "node:url";

// Settings > Game configuration (design frames OC804, QFEg9, j72iI0 password
// will be removed, x8CTg6 new password typed) and the 404 page (Ne5TA), captured at 1440x900 @2x = 2880x1800, same as the frames.
// Frames wxINm (read-only) and e9FnC (orphan key) are intentionally not
// captured: they need per-state server fixtures.

async function capture(page: Page, id: string): Promise<void> {
  const here = path.dirname(fileURLToPath(import.meta.url));
  await page.screenshot({ path: path.join(here, `${id}.png`), fullPage: true });
}

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

test.describe("Game configuration + 404 (Desktop — 1440x900) @screenshots", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
  });

  test.beforeEach(async ({ page }) => {
    await useScreenshotDataset(page);
  });

  test("OC804: Server Detail — Settings · Game configuration", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Game configuration");
    await expect(page.getByRole("heading", { name: "Game configuration" })).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/^Max players/)).toHaveValue("8");
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "OC804");
  });

  test("QFEg9: Server Detail — Settings · Game configuration (invalid value)", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Game configuration");
    await page.getByLabel(/^Max players/).fill("900");
    await expect(page.getByText("Must be between 1 and 255.")).toBeVisible({ timeout: 10_000 });
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "QFEg9");
  });

  test("j72iI0: Server Detail — Settings · Game configuration (password will be removed)", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Game configuration");
    await page.getByRole("button", { name: "Remove password" }).click();
    await expect(page.getByText("Removed when you save. Undo to keep the stored password.")).toBeVisible({ timeout: 10_000 });
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "j72iI0");
  });

  test("x8CTg6: Server Detail — Settings · Game configuration (new password typed)", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Game configuration");
    await page.getByLabel(/^Server password/).fill("0123456789");
    await expect(page.getByRole("button", { name: "Remove password" })).toHaveCount(0);
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "x8CTg6");
  });

  test("Ne5TA: Page not found", async ({ page }) => {
    await page.goto("/this-page-does-not-exist");
    await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "Ne5TA");
  });
});
