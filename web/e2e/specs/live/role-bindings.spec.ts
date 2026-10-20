import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { loginIfNeeded, seedUser, deleteUser, apiSend } from "./_seed";
import { LoginPage } from "../../pages/LoginPage";

// Live role-bindings coverage: custom roles and namespace grants on the Users &
// RBAC page (web/src/routes/Users.tsx) driven against the REAL API. Covers
// creating a namespaced custom role in the Roles tab, binding it to a user in
// gameplane-games, proving the binding persists across reload and gates the
// user's access (the signed-in user sees "Access denied" on /users and the API
// answers 403), then revoking the binding through the UI.
//
// Login budget: loginIfNeeded (the shared admin session from storageState)
// covers the page under test; the only extra login is the fresh context that
// signs in as the bound user.

test.describe("live: role bindings", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "live-only: the mock suite covers the Users page against a scripted fetch shim",
  );

  const stamp = Date.now().toString(36);
  // Names are derived per test so retries never reuse leaked objects.
  let uname = "";
  let roleName = "";
  // Id of the user seeded by the test; deleted in afterEach (also removes its bindings).
  let userId: number | undefined;

  test.beforeEach(async ({ page }) => {
    const suffix = Math.random().toString(36).slice(2, 6);
    uname = `e2e-rb-${stamp}-${suffix}`;
    roleName = `e2e-rb-role-${stamp}-${suffix}`;
    userId = undefined;
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
    await page.goto("/users");
    await expect(page.getByRole("heading", { name: "Users & RBAC" })).toBeVisible({ timeout: 15_000 });
  });

  test.afterEach(async ({ request }) => {
    // Order matters: the user goes first (its bindings are removed with it),
    // because a role that is still assigned to a user cannot be deleted (409).
    if (userId !== undefined) {
      await deleteUser(request, userId);
    }
    await apiSend(request, "DELETE", `/roles/${encodeURIComponent(roleName)}`).catch(() => undefined);
  });

  // Opens the Edit dialog for the given username via its row actions menu.
  async function openEditDialog(page: Page, username: string): Promise<void> {
    await page.getByRole("button", { name: `Actions for ${username}` }).click();
    await page.getByRole("menuitem", { name: "Edit user" }).click();
    await expect(page.getByRole("heading", { name: "Edit user" })).toBeVisible();
  }

  test("bind a namespaced custom role, prove it gates access, then revoke it", async ({ page, browser, request }) => {
    // Seed the target user with the viewer primary role: viewer is cluster-wide
    // read-only and has no users:manage, so /users is forbidden for it.
    const u = await seedUser(request, { username: uname, role: "viewer" });
    userId = u.id;

    // 1. Create a custom role with one namespaced permission in the Roles tab.
    await page.getByRole("tab", { name: /^Roles/ }).click();
    await page.getByRole("button", { name: "New role", exact: true }).click();
    await page.locator("#role-name").fill(roleName);
    // HeroUI hides the checkbox input under its own spans; force it, as servers-core does.
    await page.getByRole("checkbox", { name: /servers:read/ }).check({ force: true });
    await page.getByRole("button", { name: "Create role", exact: true }).click();

    await expect(page.getByText(roleName, { exact: true })).toBeVisible({ timeout: 15_000 });
    const roleCard = page
      .getByText(roleName, { exact: true })
      .locator("xpath=ancestor::div[contains(concat(' ', normalize-space(@class), ' '), ' flex-col ')][1]");
    await expect(roleCard.getByText("1 permission", { exact: true })).toBeVisible();

    // 2. Grant the role to the user in gameplane-games from the Edit dialog. The user
    // was seeded through the API after the list loaded, so reload to refetch it.
    await page.reload();
    await page.getByRole("tab", { name: /^Users/ }).click();
    const grid = page.getByRole("grid", { name: "Users list" });
    await expect(grid.getByRole("row").filter({ hasText: uname })).toBeVisible({ timeout: 15_000 });
    await openEditDialog(page, uname);

    await page.getByRole("button", { name: /Grant role/ }).click();
    await page.getByRole("option", { name: roleName, exact: true }).click();
    await page.getByPlaceholder("namespace").fill("gameplane-games");
    await page.getByRole("button", { name: "Add", exact: true }).click();

    const removeButton = () =>
      page.getByRole("button", { name: `Remove ${roleName} in gameplane-games on local` });
    await expect(removeButton()).toBeVisible({ timeout: 15_000 });
    await page.getByRole("button", { name: "Cancel", exact: true }).click();

    // 3. The binding survives a reload.
    await page.reload();
    await openEditDialog(page, uname);
    await expect(removeButton()).toBeVisible({ timeout: 15_000 });
    await page.getByRole("button", { name: "Cancel", exact: true }).click();

    // 4. Signed in as the bound user, /users is forbidden client-side
    //    (RequirePermission renders "Access denied" without users:manage), and
    //    the API refuses the same request with 403 from the same session.
    const ctx = await browser.newContext({
      baseURL: "http://localhost:5173",
      storageState: { cookies: [], origins: [] },
    });
    try {
      const p = await ctx.newPage();
      await p.goto("/login");
      await p.waitForLoadState("domcontentloaded");
      await new LoginPage(p).login(uname, u.password);
      await p.waitForURL((x) => !x.pathname.startsWith("/login"), { timeout: 15_000 });

      await p.goto("/users");
      await expect(p.getByText("Access denied", { exact: true })).toBeVisible({ timeout: 15_000 });
      await expect(p.getByText(/doesn.t permit access to this page/)).toBeVisible();

      const denied = await ctx.request.get("/users", { headers: { Accept: "application/json" } });
      expect(denied.status()).toBe(403);
    } finally {
      await ctx.close();
    }

    // 5. Revoke the binding from the admin session; it disappears and stays gone after reload.
    await openEditDialog(page, uname);
    await removeButton().click();
    await expect(removeButton()).toHaveCount(0, { timeout: 15_000 });
    await page.getByRole("button", { name: "Cancel", exact: true }).click();

    await page.reload();
    await openEditDialog(page, uname);
    await expect(removeButton()).toHaveCount(0);
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
  });
});
