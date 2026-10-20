import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { loginIfNeeded, seedUser, deleteUser } from "./_seed";
import { LoginPage } from "../../pages/LoginPage";

// Live users-and-roles coverage: the Users & RBAC page (web/src/routes/Users.tsx)
// driven against the REAL API. Covers invite -> role change -> persisted
// reload -> sign-in as the new user with the operator permission set, plus the
// role-change error path (404) for a user that no longer exists.
//
// Login budget: loginIfNeeded (the shared admin session from storageState)
// covers the page under test; the only extra login is the fresh context that
// signs in as the newly created user (see the first test).

test.describe("live: users and roles", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "live-only: the mock suite covers the Users page against a scripted fetch shim",
  );

  const stamp = Date.now().toString(36);
  const pw = `E2e-Users-Pw-${stamp}`;
  let uname = "";
  // Ids of users created by a test; removed in afterEach (best-effort).
  let cleanupUsernames: string[] = [];

  test.beforeEach(async ({ page }) => {
    uname = `e2e-users-${stamp}-${Math.random().toString(36).slice(2, 6)}`;
    cleanupUsernames = [uname];
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
    await page.goto("/users");
    await expect(page.getByRole("heading", { name: "Users & RBAC" })).toBeVisible({ timeout: 15_000 });
  });

  test.afterEach(async ({ request }) => {
    // Best-effort teardown: resolve each leaked username to an id and delete it.
    // Deleting an already-removed user is a no-op for deleteUser.
    const res = await request
      .get("/users", { headers: { Accept: "application/json" } })
      .catch(() => null);
    if (!res || !res.ok()) return;
    const users = (await res.json().catch(() => [])) as Array<{ id: number; username: string }>;
    for (const u of users) {
      if (cleanupUsernames.includes(u.username)) {
        await deleteUser(request, u.id);
      }
    }
  });

  // Opens the Edit dialog for the given username via its row actions menu.
  async function openEditDialog(page: Page, username: string): Promise<void> {
    await page.getByRole("button", { name: `Actions for ${username}` }).click();
    await page.getByRole("menuitem", { name: "Edit user" }).click();
    await expect(page.getByRole("heading", { name: "Edit user" })).toBeVisible();
  }

  test("invite, promote to operator, persist, and sign in with the new role", async ({ page, browser }) => {
    // 1. Invite a user with the default role (viewer).
    await page.getByRole("button", { name: "Invite user", exact: true }).click();
    await page.locator("#invite-username").fill(uname);
    await page.locator("#invite-password").fill(pw);
    await page.getByRole("button", { name: "Create user", exact: true }).click();

    const grid = page.getByRole("grid", { name: "Users list" });
    const row = grid.getByRole("row").filter({ hasText: uname });
    await expect(row).toBeVisible({ timeout: 15_000 });
    await expect(row.getByText("viewer", { exact: true })).toBeVisible();

    // 2. Promote to operator through the Edit dialog's primary role.
    await openEditDialog(page, uname);
    await page.locator("#edit-role").click();
    await page.getByRole("option", { name: "operator", exact: true }).click();
    await page.getByRole("button", { name: "Save changes", exact: true }).click();
    await expect(row.getByText("operator", { exact: true })).toBeVisible({ timeout: 15_000 });
    // A successful save closes the dialog (Users.tsx save.onSuccess -> setEditing(null)).
    await expect(page.getByRole("dialog")).toBeHidden({ timeout: 15_000 });

    // 3. The change survives a reload.
    await page.reload();
    await expect(grid.getByRole("row").filter({ hasText: uname }).getByText("operator", { exact: true })).toBeVisible({
      timeout: 15_000,
    });

    // 4. Sign in as the new user in a clean context. An operator has no
    //    users:manage, audit:read, config:manage, or "*", so those links must be
    //    absent from the primary navigation while the workload links remain.
    const ctx = await browser.newContext({
      baseURL: "http://localhost:5173",
      storageState: { cookies: [], origins: [] },
    });
    try {
      const p = await ctx.newPage();
      await p.goto("/login");
      await p.waitForLoadState("domcontentloaded");
      await new LoginPage(p).login(uname, pw);
      await p.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 15_000 });

      const nav = p.getByRole("navigation", { name: "Primary" });
      await expect(nav).toBeVisible({ timeout: 15_000 });
      for (const l of ["Servers", "Modules", "Backups"]) {
        await expect(nav.getByRole("link", { name: l, exact: true })).toBeVisible();
      }
      for (const l of ["Users & RBAC", "Audit log", "System logs", "Settings"]) {
        await expect(nav.getByRole("link", { name: l, exact: true })).toHaveCount(0);
      }
    } finally {
      await ctx.close();
    }
  });

  test("changing the role of a deleted user surfaces the API 404 inline", async ({ page, request }) => {
    // Error path (T040). PATCH /users/{id} with a role on a deleted user
    // returns 404 "user not found" (users.go update, #699), which the Edit
    // dialog renders as its inline alert.
    const ghostName = `e2e-ghost-${stamp}-${Math.random().toString(36).slice(2, 6)}`;
    const ghost = await seedUser(request, { username: ghostName, password: pw });
    cleanupUsernames.push(ghostName);

    // The Edit dialog is opened while the user still exists, so the list
    // already contains the row when the dialog loads.
    await page.reload();
    await expect(page.getByRole("grid", { name: "Users list" }).getByRole("row").filter({ hasText: ghostName })).toBeVisible({
      timeout: 15_000,
    });
    await openEditDialog(page, ghostName);

    // Delete the user behind the open dialog, then try to change its role.
    await deleteUser(request, ghost.id);

    await page.locator("#edit-role").click();
    await page.getByRole("option", { name: "operator", exact: true }).click();
    await page.getByRole("button", { name: "Save changes", exact: true }).click();

    await expect(page.getByRole("dialog").getByRole("alert").filter({ hasText: "user not found" })).toBeVisible({
      timeout: 15_000,
    });
  });
});
