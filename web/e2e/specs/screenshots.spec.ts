import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import fs from "fs";
import { LoginPage } from "../pages/LoginPage";

const __filename = fileURLToPath(import.meta.url);
const OUT = path.resolve(path.dirname(__filename), "../../../docs/img");

test.describe("@screenshots dashboard gallery", () => {
  test.use({
    viewport: { width: 1920, height: 1080 },
    deviceScaleFactor: 1,
    timezoneId: "UTC",
    colorScheme: "dark",
  });

  // Helper to capture screenshot at 1920×1080 JPEG
  async function shoot(page: Page, name: string, quality = 80): Promise<void> {
    await page.waitForLoadState("load");
    await page.waitForTimeout(400);
    // Text-dense screens (Logs) need a lower quality to stay under the contract's 150 KB.
    await page.screenshot({
      path: path.join(OUT, name + ".jpg"),
      type: "jpeg",
      quality,
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

  test("login", async ({ page }) => {
    // Force 401 on /users/me so the SPA doesn't redirect to dashboard before
    // we can screenshot the login page. The cookie survives the navigation and
    // is needed before visiting /login because mock mode answers /users/me
    // immediately upon page load.
    await page.context().addCookies([
      { name: "e2e_force_401", value: "1", url: "http://localhost:5173" },
    ]);
    await page.goto("/login");
    await expect(
      page.getByRole("textbox", { name: /Email or username/i })
    ).toBeVisible();
    await shoot(page, "login");
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

  test("servers-list", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVERS LIST (/servers): Game server table
    await page.goto("/servers");
    // Wait for page heading or table to be visible
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.waitForTimeout(250);
    await shoot(page, "servers-list");
  });

  test("server-overview", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVER OVERVIEW (/servers/mc-survival)
    await page.goto("/servers/mc-survival");
    // Click Overview tab to ensure we're on the right tab
    await clickTab(page, "Overview");
    // Wait for overview tab panel or a distinctive metric/heading
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.waitForTimeout(250);
    await shoot(page, "server-overview");
  });

  test("mods-registry-browse", async ({ page }) => {
    await loginAsAdmin(page);

    // MODS REGISTRY BROWSER (/servers/test-server-02 — Minecraft (Fabric), Modrinth)
    // test-server-02's template (minecraft-modded) declares capabilities.mods
    // with a Modrinth registry, which is what makes the Mods tab visible
    // (ServerDetail hides it otherwise). "Install mod" opens the install page,
    // whose default mode is "Browse registry" when the template declares one.
    await page.goto("/servers/test-server-02");
    await clickTab(page, "Mods");
    // Installed-mods header ("0 installed") proves the tab body loaded.
    await expect(page.getByText(/\d+ installed/)).toBeVisible();
    await clickTab(page, "Install mod");
    // First registry card from the mocked Modrinth search.
    await expect(page.getByText("Fabulously Optimized").first()).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(250);
    await shoot(page, "mods-registry-browse");
  });

  test("server-console", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVER CONSOLE TAB (/servers/mc-survival)
    await page.goto("/servers/mc-survival");
    // Click Console tab to navigate to the correct tab
    await clickTab(page, "Console");
    // Wait for the connect line the terminal writes when the mocked WebSocket opens
    await expect(page.getByText("— connected —").first()).toBeVisible({
      timeout: 15_000,
    });
    await shoot(page, "server-console");
  });

  test("create-server-template-select", async ({ page }) => {
    await loginAsAdmin(page);

    // CREATE SERVER WIZARD - TEMPLATE SELECTION (/servers/new)
    await page.goto("/servers/new");
    // Wait for the template picker cards (step 1 of the wizard)
    await expect(page.getByText("Minecraft Java Edition").first()).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(250);
    await shoot(page, "create-server-template-select");
  });

  test("admin-mod-registries", async ({ page }) => {
    await loginAsAdmin(page);

    // ADMIN SETTINGS - MOD REGISTRIES (/admin)
    await page.goto("/admin");
    // Click Mod registries button to navigate to the correct section
    await clickTab(page, "Mod registries");
    // Wait for admin settings section to load
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.waitForTimeout(250);
    await shoot(page, "admin-mod-registries");
  });

  test("server-detail-events", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVER EVENTS TAB (test-server-04 with FAILED phase)
    await page.goto("/servers/test-server-04");
    // Click Events tab to navigate to the correct tab
    await clickTab(page, "Events");
    // Wait for the mocked Kubernetes events to render
    await expect(page.getByText(/CrashLoopBackOff/).first()).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(250);
    await shoot(page, "server-detail-events");
  });

  test("admin-settings-general", async ({ page }) => {
    await loginAsAdmin(page);

    // ADMIN SETTINGS - GENERAL (/admin)
    await page.goto("/admin");
    // Wait for admin general form or heading (General is the default section)
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.waitForTimeout(250);
    await shoot(page, "admin-settings-general");
  });

  test("cluster-nodes", async ({ page }) => {
    await loginAsAdmin(page);

    // CLUSTER PAGE (/cluster): Node list and stats
    await page.goto("/cluster");
    // Wait for cluster heading or node list
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.waitForTimeout(250);
    await shoot(page, "cluster-nodes");
  });

  test("server-detail-logs", async ({ page }) => {
    await loginAsAdmin(page);

    // SERVER LOGS TAB (/servers/mc-survival)
    await page.goto("/servers/mc-survival");
    // Click Logs tab to navigate to the correct tab
    await clickTab(page, "Logs");
    // Wait for a line from the mocked log stream
    await expect(page.getByText(/Done \(14\.618s\)/).first()).toBeVisible({
      timeout: 15_000,
    });
    await shoot(page, "server-detail-logs", 65);
  });
});
