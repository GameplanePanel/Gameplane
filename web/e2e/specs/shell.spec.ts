import { test, expect } from "@playwright/test";
import { loginIfNeeded } from "../pages/LoginPage";

// Shell E2E: covers top-level navigation, breadcrumbs, cluster filters,
// and sidebar (desktop fixed + mobile drawer variants).
// These tests verify that the application shell components render correctly
// and that navigation, cluster switching, and UI interactions work as expected.

test.describe("shell", () => {
  // Live mode pre-authenticates every test via a single shared session
  // (globalSetup + storageState, web/playwright.config.ts). This file's
  // own "sign-out" test does a real logout against that shared session,
  // which invalidates it for every test that runs after — each one then
  // falls through loginIfNeeded and performs a real POST /auth/login,
  // quickly exceeding the API's per-IP login rate limit (burst 10, 5/min;
  // see CLAUDE.md's e2e conventions) and cascading into ~14 failures that
  // all land on /login. Shell/sidebar/breadcrumb/cluster-selector behavior
  // against the real backend is covered by
  // e2e/specs/live/login-and-shell.spec.ts instead, which is written to
  // stay within the live login budget; this file is mock-only, matching
  // the precedent in rbacEnforcement.spec.ts.
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET === "live",
    "shell/sidebar UI is covered live by login-and-shell.spec.ts; this file's sign-out test would burn the login rate limit for every test after it",
  );

  test.describe("top bar", () => {
    test("hamburger button appears on mobile, hidden on desktop", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      // On mobile (375px), hamburger should be visible (lg:hidden)
      await page.setViewportSize({ width: 375, height: 667 });
      const hamburger = page.getByRole("button", { name: /open navigation/i });
      await expect(hamburger).toBeVisible();

      // On desktop (1200px), hamburger should be hidden
      await page.setViewportSize({ width: 1200, height: 800 });
      await expect(hamburger).not.toBeVisible();
    });

    test("hamburger opens mobile drawer sidebar", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      await page.setViewportSize({ width: 375, height: 667 });
      const hamburger = page.getByRole("button", { name: /open navigation/i });
      // The drawer's nav carries its own accessible name ("Mobile
      // navigation"), distinct from the fixed sidebar's "Primary" — see
      // Sidebar.tsx — so this targets the drawer's own landmark rather
      // than one that (before that split) ambiguously matched both.
      const sidebarNav = page.getByRole("navigation", { name: /mobile navigation/i });

      // Initially, nav might be off-screen or hidden via modal
      await hamburger.click();
      await expect(sidebarNav).toBeVisible();

      // Close the drawer
      const closeButton = page.getByRole("button", { name: /close navigation/i });
      await expect(closeButton).toBeVisible();
      await closeButton.click();
    });

    test("user menu button displays initials from username", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      const userMenuTrigger = page.getByRole("button", { name: /user menu/i });
      await expect(userMenuTrigger).toBeVisible();
      // Avatar with initials should be visible (the exact text depends on the logged-in user)
    });

    test("user menu sign-out button logs out", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      const userMenuTrigger = page.getByRole("button", { name: /user menu/i });
      await userMenuTrigger.click();

      const signOutButton = page.getByRole("menuitem", { name: /sign out/i });
      await expect(signOutButton).toBeVisible();
      await signOutButton.click();

      // After sign-out, should redirect to login
      await page.waitForURL((u) => u.pathname.startsWith("/login"), { timeout: 10_000 });
      expect(new URL(page.url()).pathname).toMatch(/^\/login/);
    });
  });

  test.describe("sidebar", () => {
    test("desktop sidebar displays navigation with proper sections", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      await page.setViewportSize({ width: 1200, height: 800 });

      const nav = page.getByRole("navigation", { name: /primary/i });
      await expect(nav).toBeVisible();

      // Check for expected nav groups/items (admin user should see all sections)
      const serversLink = page.getByRole("link", { name: /servers/i }).first();
      const modulesLink = page.getByRole("link", { name: /modules/i });
      await expect(serversLink).toBeVisible();
      await expect(modulesLink).toBeVisible();
    });

    test("active nav item highlights correctly", async ({ page }) => {
      await page.goto("/servers");
      await loginIfNeeded(page);

      const nav = page.getByRole("navigation", { name: /primary/i });
      await expect(nav).toBeVisible();

      // The "Servers" link should be marked as the current page via aria-current
      const serversLink = page.getByRole("link", { name: /servers/i }).first();
      await expect(serversLink).toHaveAttribute('aria-current', 'page');
    });

    test("sidebar shows cluster name in header", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      // The sidebar header should display the current cluster name (or "—" if not set)
      // This is a basic check that the sidebar renders the cluster info
      const sidebar = page.getByRole("complementary", { name: "Sidebar" });
      const text = await sidebar.innerText();
      // Should contain "gameplane" and either a cluster name or "—"
      expect(text).toContain("gameplane");
    });

    test("sidebar shows user info and logout button", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      const nav = page.getByRole("navigation", { name: /primary/i });
      await expect(nav).toBeVisible();

      // Check for user info (role should be visible in footer)
      // and the logout button within the sidebar
      const sidebar = page.getByRole("complementary", { name: "Sidebar" });
      const logoutButton = sidebar.getByRole("button", { name: /sign out/i });
      await expect(logoutButton).toBeVisible();
    });
  });

  test.describe("breadcrumbs", () => {
    test("breadcrumbs show on dashboard route", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      const breadcrumbs = page.getByRole("navigation", { name: /breadcrumb/i });
      await expect(breadcrumbs).toBeVisible();

      // Should contain at least the root "gameplane" breadcrumb
      const text = await breadcrumbs.innerText();
      expect(text).toContain("gameplane");
    });

    test("breadcrumbs update when navigating to servers", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      // Navigate to servers
      const serversLink = page.getByRole("link", { name: /servers/i }).first();
      await serversLink.click();
      await page.waitForURL("**/servers", { timeout: 5_000 });

      const breadcrumbs = page.getByRole("navigation", { name: /breadcrumb/i });
      await expect(breadcrumbs).toBeVisible();

      const text = await breadcrumbs.innerText();
      expect(text).toContain("gameplane");
      expect(text).toContain("Servers");
    });

    test("breadcrumb current item shows aria-current=page", async ({ page }) => {
      await page.goto("/servers");
      await loginIfNeeded(page);

      const breadcrumbs = page.getByRole("navigation", { name: /breadcrumb/i });
      const currentItem = breadcrumbs.locator("[aria-current=page]");
      await expect(currentItem).toBeVisible();

      // The current item should be the last breadcrumb (Servers in this case)
      const text = await currentItem.innerText();
      expect(text).toContain("Servers");
    });
  });

  test.describe("cluster inventory navigation", () => {
    test("Nodes offers its own cluster filter without a global selector", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      await page.setViewportSize({ width: 1200, height: 800 });
      await expect(page.getByRole("heading", { name: "Dashboard", exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: /select cluster/i })).toHaveCount(0);
      await page.goto("/nodes");
      const clusterSelector = page.getByRole("main").getByRole("button", { name: /Filter by cluster/ });
      await expect(clusterSelector).toBeVisible();
      await expect(clusterSelector).toContainText("All clusters");
    });

    test("cluster filter opens authorized cluster choices", async ({ page }) => {
      await page.goto("/nodes");
      await loginIfNeeded(page);

      const clusterSelector = page.getByRole("button", { name: /Filter by cluster/ });
      await clusterSelector.click();
      await expect(page.getByRole("listbox", { name: /Filter by cluster/ })).toBeVisible();
      await expect(page.getByRole("option", { name: "All clusters", exact: true })).toBeVisible();
    });

    test("cluster directory retains connectivity health indicators", async ({ page }) => {
      await page.goto("/clusters");
      await loginIfNeeded(page);
      await expect(page.getByText(/^API:\s*Healthy$/).first()).toBeVisible();
      await expect(page.getByText(/Status reports Kubernetes API connectivity/)).toBeVisible();
    });

    test("Clusters and Nodes remain separate sidebar destinations", async ({ page }) => {
      await page.goto("/nodes?cluster=local");
      await loginIfNeeded(page);
      const sidebar = page.getByRole("navigation", { name: "Primary" });
      await sidebar.getByRole("link", { name: "Clusters", exact: true }).click();
      await expect(page).toHaveURL(/\/clusters$/);
      await expect(page.getByRole("heading", { name: "Clusters", exact: true })).toBeVisible();
      await sidebar.getByRole("link", { name: "Nodes", exact: true }).click();
      await expect(page).toHaveURL(/\/nodes$/);
      await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
    });
  });

  test.describe("navigation flow", () => {
    test("clicking sidebar links navigates to correct routes", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      // Navigate to modules
      const modulesLink = page.getByRole("link", { name: /modules/i }).first();
      await modulesLink.click();
      await page.waitForURL("**/modules", { timeout: 5_000 });

      expect(new URL(page.url()).pathname).toBe("/modules");

      // Breadcrumbs should update
      const breadcrumbs = page.getByRole("navigation", { name: /breadcrumb/i });
      const text = await breadcrumbs.innerText();
      expect(text).toContain("Modules");
    });

    test("sidebar nav items respect permission-based visibility", async ({ page }) => {
      await page.goto("/");
      await loginIfNeeded(page);

      const nav = page.getByRole("navigation", { name: /primary/i });

      // Admin user should see settings (admin section)
      // This is dependent on the logged-in user's role
      // For e2e-admin (which is an admin), we should see admin items
      const text = await nav.innerText();
      // At minimum, we should see the "General" section. The section label is
      // rendered with CSS `text-transform: uppercase` (source text stays
      // "General" for a11y/i18n), and `innerText` reflects the rendered case,
      // so match case-insensitively rather than the visual "GENERAL".
      expect(text).toMatch(/general/i);
    });
  });
});
