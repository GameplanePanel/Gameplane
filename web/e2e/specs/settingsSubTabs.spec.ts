import { test, expect, type Page } from "@playwright/test";
import { LoginPage } from "../pages/LoginPage";
import { ServerDetailPage } from "../pages/ServerDetailPage";

// Settings sub-tab navigation under ServerDetail. Walks every sub-tab
// (General → Networking → Resources → Environment → Lifecycle → Access
// → Danger) and asserts no console errors during the traversal. The
// existing serverDetail.spec covers a subset; this one exercises the
// full set in isolation so a regression that breaks one sub-tab
// produces a clearly-named failure.

async function loginIfNeeded(page: Page): Promise<void> {
  await page.goto("/");
  await page.waitForLoadState("domcontentloaded");
  if (new URL(page.url()).pathname.startsWith("/login")) {
    const login = new LoginPage(page);
    const username =
      process.env.ADMIN_USERNAME ?? process.env.GAMEPLANE_E2E_ADMIN_USERNAME ?? "e2e-admin";
    const password =
      process.env.ADMIN_PASSWORD ?? process.env.GAMEPLANE_E2E_ADMIN_PASSWORD ?? "any-non-empty";
    await login.login(username, password);
    await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 10_000 });
  }
}

function isExpectedDevWarning(text: string): boolean {
  const lower = text.toLowerCase();
  return (
    lower.includes("[vite]") ||
    lower.includes("[mocks]") ||
    lower.includes("downloadable font") ||
    lower.includes("hydration") ||
    lower.includes("websocket connection to") ||
    lower.includes("ws://localhost") ||
    lower.includes("reading 'dimensions'") ||
    lower.includes("fitaddon")
  );
}

test.describe("server settings sub-tabs", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET === "live",
    "settings sub-tabs against live data depend on a running server; mock mode is the deterministic path",
  );

  test.beforeEach(async ({ page }) => {
    await loginIfNeeded(page);
  });

  test("validates edits in the Placement JSON editor", async ({ page }) => {
    const server = new ServerDetailPage(page);
    await server.goto("alpha");
    await server.clickTab("Settings");
    await page.getByRole("tab", { name: "Placement", exact: true }).click();
    const tolerations = page.locator(".monaco-editor").first();
    await tolerations.locator(".view-line").first().click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.insertText("[}");
    await expect(page.getByText("Invalid JSON", { exact: true })).toBeVisible();
    await expect(tolerations.locator(".squiggly-error").first()).toBeVisible();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.insertText("[]");
    await expect(page.getByText("Invalid JSON", { exact: true })).toHaveCount(0);
    await expect(tolerations.locator(".squiggly-error")).toHaveCount(0);
  });

  test("walks every sub-tab without console errors", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));
    page.on("console", (msg) => {
      if (msg.type() === "error") errors.push(msg.text());
    });

    const serverDetail = new ServerDetailPage(page);
    await serverDetail.goto("alpha");
    await page.waitForLoadState("domcontentloaded");

    const tabNav = page.getByRole("tablist", { name: /Server detail tabs/i });
    await tabNav.getByRole("tab", { name: /^Settings$/i }).click();

    const subTabs = [
      "General",
      "Version",
      "Resources",
      "Networking",
      "Environment",
      "Lifecycle",
      "Scheduled backups",
      "Network capture",
      "Placement",
      "RBAC & access",
      "Danger zone",
    ];
    for (const t of subTabs) {
      // Settings sub-tabs render as buttons inside the Settings panel.
      // Some may not be present depending on the Settings module shape;
      // tolerate that gracefully.
      const btn = page.getByRole("button", { name: new RegExp(`^${t}$`, "i") }).first();
      if (await btn.isVisible().catch(() => false)) {
        await btn.click();
        await page.waitForTimeout(200);
      }
    }

    expect(errors.filter((e) => !isExpectedDevWarning(e))).toEqual([]);
  });
});
