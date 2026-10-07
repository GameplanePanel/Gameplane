import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import fs from "fs";
import { LoginPage } from "../pages/LoginPage";

const __filename = fileURLToPath(import.meta.url);
const OUT = path.resolve(path.dirname(__filename), "../../website-screenshots");

test.describe("@screenshots website gallery", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    timezoneId: "UTC",
    colorScheme: "dark",
  });

  // Helper to capture screenshot at 1440×900 (scaled to 2880×1800) PNG
  async function shoot(page: Page, name: string): Promise<void> {
    await page.waitForLoadState("load");
    await page.waitForTimeout(400);
    await page.screenshot({
      path: path.join(OUT, name + ".png"),
      type: "png",
      fullPage: false,
    });
  }

  // Mock mode answers /users/me with the admin user whenever the
  // e2e_force_401 cookie is absent, so a bare visit to /login redirects to
  // the dashboard before the form can be filled. Land on /login with the
  // cookie set, drop it, then submit the form so the SPA runs its real
  // post-login navigation (and receives the CSRF cookie).
  async function loginAsAdmin(page: Page): Promise<void> {
    await page.context().addCookies([
      { name: "e2e_force_401", value: "1", url: "http://localhost:5173" },
    ]);
    await page.goto("/login");
    await expect(page.getByRole("textbox", { name: /email or username/i })).toBeVisible({ timeout: 10_000 });
    await page.context().clearCookies();
    const login = new LoginPage(page);
    const username =
      process.env.ADMIN_USERNAME ?? process.env.GAMEPLANE_E2E_ADMIN_USERNAME ?? "e2e-admin";
    const password =
      process.env.ADMIN_PASSWORD ?? process.env.GAMEPLANE_E2E_ADMIN_PASSWORD ?? "any-non-empty";
    await login.login(username, password);
    await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 10_000 });
  }

  // Helper to click a tab/section button with visibility assertion
  async function clickTab(page: Page, name: string): Promise<void> {
    const tab = page
      .getByRole("tab", { name: new RegExp(`^${name}$`, "i") })
      .or(page.getByRole("button", { name, exact: true }));
    await expect(tab).toBeVisible({ timeout: 10_000 });
    await tab.click();
  }

  // Ensure output directory exists before any test runs
  test.beforeAll(async () => {
    fs.mkdirSync(OUT, { recursive: true });
  });

  // Install dataset switch on every page
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      try {
        localStorage.setItem("gameplane-e2e-dataset", "screenshots");
      } catch {}
    });
  });

  test("dashboard", async ({ page }) => {
    await loginAsAdmin(page);

    // DASHBOARD (/): Fleet health overview
    await page.goto("/");
    // Wait for the sidebar navigation to render (Modules link is always visible after hydration)
    await expect(page.getByRole("link", { name: /modules/i }).first()).toBeVisible();
    await page.waitForTimeout(250); // Layout settle
    await shoot(page, "dashboard");
  });

  test("servers", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVERS LIST (/servers): Game server table
    await page.goto("/servers");
    // Wait for page heading or table to be visible
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    // Wait for server row to load before screenshot
    await expect(page.getByText("mc-survival").first()).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(250);
    await shoot(page, "servers");
  });

  test("console", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVER CONSOLE TAB (/servers/mc-survival)
    await page.goto("/servers/mc-survival");
    // Click Console tab to navigate to the correct tab
    await clickTab(page, "Console");
    // Wait for the connect line the terminal writes when the mocked WebSocket opens
    await expect(page.getByText("— connected —").first()).toBeVisible({
      timeout: 15_000,
    });
    await shoot(page, "console");
  });

  test("backups", async ({ page }) => {
    await loginAsAdmin(page);

    // BACKUPS INDEX PAGE (/backups)
    await page.goto("/backups");
    // Wait for a backup from the screenshot fixtures
    await expect(page.getByText("mc-survival-nightly-0713").first()).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(250);
    await shoot(page, "backups");
  });

  test("modules", async ({ page }) => {
    await loginAsAdmin(page);

    // MODULES CATALOG PAGE (/modules)
    await page.goto("/modules");
    // Wait for Minecraft (Vanilla) from catalog fixture (buildScreenshotHandlers, handlers.ts ~line 1324)
    await expect(page.getByText("Minecraft (Vanilla)").first()).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(250);
    await shoot(page, "modules");
  });
});
