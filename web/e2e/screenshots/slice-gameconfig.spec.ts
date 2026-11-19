import { test, expect, type Page } from "@playwright/test";
import { capture as captureDesign } from "./capture";
import path from "path";
import { fileURLToPath } from "node:url";

// Settings > Game configuration (design frames OC804, QFEg9, BguST password
// will be removed, YN8cE new password typed) and the 404 page (Ne5TA), captured at 1440x900 @2x = 2880x1800, same as the frames.
// The JVM screens KJdxg and ZUAq1 use their exported frame sizes.
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

  test("BguST: Server Detail — Settings · Game configuration (password will be removed)", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Game configuration");
    await page.getByRole("button", { name: "Remove password" }).click();
    await expect(page.getByText("Removed when you save. Undo to keep the stored password.")).toBeVisible({ timeout: 10_000 });
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "BguST");
  });

  test("YN8cE: Server Detail — Settings · Game configuration (new password typed)", async ({ page }) => {
    await page.goto("/servers/mc-survival");
    await clickTab(page, "Settings");
    await clickTab(page, "Game configuration");
    await page.getByLabel(/^Server password/).fill("0123456789");
    await expect(page.getByRole("button", { name: "Remove password" })).toHaveCount(0);
    await page.mouse.move(0, 0);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.waitForTimeout(200);
    await capture(page, "YN8cE");
  });

  for (const state of [
    { id: "KJdxg", variant: "jvm-adopted", initial: "1536M", flag: "1536M", notice: /next container start/i, valid: true },
    { id: "ZUAq1", variant: "jvm-adopting", initial: "", flag: "3072M", notice: /may restart once/i, valid: false },
  ]) {
    test(`${state.id}: Minecraft JVM startup — ${state.variant}`, async ({ context, page }) => {
      await context.addCookies([{ name: "e2e_server_variant", value: state.variant, domain: "localhost", path: "/" }]);
      await page.goto("/servers/mc-survival");
      await clickTab(page, "Settings");
      await clickTab(page, "Game configuration");
      await page.getByLabel(/^JVM initial heap/).fill(state.initial);
      await expect(page.getByTestId("config-restart-note")).toHaveText(state.notice);
      await expect(page.getByText(`Heap options before additional JVM arguments: -Xms${state.flag} -Xmx2G`)).toBeVisible();
      const save = page.getByRole("button", { name: /save changes/i });
      if (state.valid) {
        await expect(save).toBeEnabled();
      } else {
        await expect(page.getByText(/The initial heap exceeds the maximum heap/)).toBeVisible();
        await expect(save).toBeDisabled();
      }
      await captureDesign(page, state.id);
    });
  }

  test("Ne5TA: Page not found", async ({ page }) => {
    await page.goto("/this-page-does-not-exist");
    await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "Ne5TA");
  });
});
