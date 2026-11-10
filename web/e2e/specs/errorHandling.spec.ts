import { test, expect, type Page } from "@playwright/test";
import { LoginPage } from "../pages/LoginPage";

// Error-path UI behavior. The SPA must:
//   - Redirect to /login on a 401 from a protected route.
//   - Surface a usable error UI on a 500 (no blank screen).
//   - Make NO authenticated GETs before the user submits the login form
//     (CLAUDE.md §3 pre-auth privacy contract).

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

test.describe("error handling", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET === "live",
    "error injection requires controlled responses; mock mode is the deterministic path",
  );

  test("401 on a protected route bounces to /login", async ({ page, context }) => {
    // First, log in normally so we have a session.
    await loginIfNeeded(page);

    // Arm the next /users/me to come back 401. page.route can't help in
    // mock mode — MSW's service worker answers before the request hits
    // the network — so we set the cookie the /users/me handler checks.
    // It survives the navigation below, so the refetch after reload sees
    // it and the SPA's AppLayout 401 hook redirects to /login.
    await context.addCookies([
      { name: "e2e_force_401", value: "1", url: "http://localhost:5173" },
    ]);

    await page.goto("/servers");
    await page.waitForURL((u) => u.pathname.startsWith("/login"), { timeout: 10_000 });
  });

  test("failed cluster inventory shows incomplete coverage on Nodes", async ({ page }) => {
    // Cookie-selected, not page.route: MSW's Service Worker answers
    // /cluster and /cluster/stats itself in mock mode (see handlers.ts's
    // e2e_cluster_500 branch).
    await page.context().addCookies([
      { name: "e2e_cluster_500", value: "1", url: "http://localhost:5173" },
    ]);
    await loginIfNeeded(page);
    await page.goto("/nodes");
    await page.waitForLoadState("domcontentloaded");

    await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
    await expect(page.getByText("Node results are partial.", { exact: true })).toBeVisible();
    await expect(page.getByText("No node data is available for this scope.", { exact: true })).toBeVisible();
    await expect(page.getByText("0 / 0", { exact: true })).toHaveCount(0);
  });

  test("pre-auth /login does not call /users or /cluster", async ({ page }) => {
    const forbidden: string[] = [];
    page.on("request", (req) => {
      const u = new URL(req.url());
      if (
        u.pathname === "/users" ||
        u.pathname === "/cluster" ||
        u.pathname === "/cluster/info" ||
        u.pathname === "/cluster/stats" ||
        u.pathname === "/fleet/inventory"
      ) {
        forbidden.push(`${req.method()} ${u.pathname}`);
      }
    });

    const login = new LoginPage(page);
    await login.goto();
    // Wait for any initial fetches to settle.
    await page.waitForLoadState("networkidle");

    expect(
      forbidden,
      "/login must not GET cluster/user data before the form is submitted",
    ).toEqual([]);
  });
});
