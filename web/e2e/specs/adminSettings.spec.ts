import { test, expect, type Page } from "@playwright/test";
import { LoginPage } from "../pages/LoginPage";

// AdminSettings page e2e. Covers the General section (the simplest
// useSectionForm consumer) submitting a PUT to /admin/config/{section}
// and the section navigation between General/Auth/Telemetry/Updates.
//
// We don't walk every section's form — useSectionForm is shared, so
// proving it on General is enough; the per-section render is exercised
// in component tests.

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

test.describe("admin settings page", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET === "live",
    "config edits in live mode persist to the API DB; mock mode is the safe path",
  );

  test.beforeEach(async ({ page }) => {
    await loginIfNeeded(page);
  });

  test("renders the page header and General section", async ({ page }) => {
    await page.goto("/admin");
    await page.waitForLoadState("domcontentloaded");

    await expect(page.getByRole("heading", { name: /admin settings/i })).toBeVisible();
    // General tab is selected by default — its inputs are visible.
    await expect(page.getByText(/instance name/i)).toBeVisible();
  });

  test("saving General section sends PUT /admin/config/general", async ({ page }) => {
    await page.goto("/admin");
    await page.waitForLoadState("domcontentloaded");

    // Update the Instance name input. Target it by its field label —
    // page.locator("input").first() would grab the global header search
    // box, which renders before the General form.
    const instanceInput = page.getByLabel(/instance name/i);
    await instanceInput.fill("e2e-mock-instance");

    const saved = page.waitForRequest(
      (req) => /\/admin\/config\/general$/.test(req.url()) && req.method() === "PUT",
    );
    await page.getByRole("button", { name: /save changes/i }).first().click();
    const req = await saved;
    const body = req.postDataJSON() as { instanceName?: string };
    expect(body.instanceName).toBe("e2e-mock-instance");
  });

  test("navigates between sections", async ({ page }) => {
    await page.goto("/admin");
    await page.waitForLoadState("domcontentloaded");

    // Click Telemetry — its toggle row appears.
    await page.getByRole("button", { name: /^telemetry$/i }).click();
    await expect(page.getByText(/send basic usage metrics/i)).toBeVisible();

    // Click About — version metadata appears.
    await page.getByRole("button", { name: /^about$/i }).click();
    await expect(page.getByText(/agpl-3\.0/i)).toBeVisible();
  });

  test("a sink added but never saved stores no Secret", async ({ page }) => {
    const secretRequests: string[] = [];
    page.on("request", (req) => {
      if (/\/admin\/(notifications\/sinks|auth\/providers|registries)\/[^/]+\/secret$/.test(req.url())) {
        secretRequests.push(`${req.method()} ${req.url()}`);
      }
    });
    await page.goto("/admin");
    await page.waitForLoadState("domcontentloaded");

    const nav = page.getByRole("navigation", { name: "Settings sections" });
    await nav.getByRole("button", { name: /^notifications$/i }).click();
    await page.getByRole("button", { name: /^add sink$/i }).click();
    await page.getByPlaceholder("team-alerts").fill("e2e-unsaved");
    await page.getByPlaceholder(/discord\.com/i).fill("https://discord.com/api/webhooks/1/x");
    await page.getByRole("button", { name: /^add sink$/i }).click();
    await expect(page.getByText(/Secret: gameplane-notify-e2e-unsaved/i)).toBeVisible();

    // Leave the section without saving: the draft row is discarded.
    await nav.getByRole("button", { name: /^general$/i }).click();
    await nav.getByRole("button", { name: /^notifications$/i }).click();
    await expect(page.getByText(/Secret: gameplane-notify-e2e-unsaved/i)).toHaveCount(0);
    expect(secretRequests).toEqual([]);
  });
});
