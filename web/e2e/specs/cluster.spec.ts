import { test, expect, type Page } from "@playwright/test";
import { LoginPage } from "../pages/LoginPage";

// Mirrors navigation.spec's helper: works in both mock (always
// authenticated) and live (redirects to /login until we sign in) modes.
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

test.describe("nodes and cluster configuration", () => {
  // Mock mode serves a deterministic 3-node view. Live mode's cluster data
  // depends on clusterOps being enabled on the e2e cluster, so the node
  // summary isn't guaranteed there — keep this spec deterministic.
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET === "live",
    "cluster view is only deterministic under mock mode",
  );

  test.beforeEach(async ({ page }) => {
    await loginIfNeeded(page);
  });

  test("renders the Nodes header and filtered node summary", async ({ page }) => {
    await page.goto("/nodes");
    await expect(page).toHaveURL(/\/nodes$/);
    await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
    await expect(page.getByText("Ready nodes", { exact: true })).toBeVisible();
    await expect(page.getByText("Reporting clusters", { exact: true })).toBeVisible();
    await expect(page.getByRole("grid", { name: "Node inventory" })).toBeVisible();
  });

  test("keeps enrollment in Nodes and kubeconfig in the owning cluster configuration", async ({ page }) => {
    await page.goto("/nodes?cluster=local");
    await expect(page.getByRole("button", { name: /add node/i })).toBeVisible();
    await page.goto("/clusters/local?tab=configuration");
    await expect(page.getByRole("button", { name: /download kubeconfig/i })).toBeVisible();
  });

  test("preserves a legacy inventory bookmark while the Nodes sidebar opens all clusters", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("gameplane.cluster", "local"));
    await page.goto("/cluster");
    await expect(page).toHaveURL(/\/nodes\?cluster=local$/);
    const sidebar = page.getByRole("navigation", { name: "Primary" });
    const nodes = sidebar.getByRole("link", { name: "Nodes", exact: true });
    await expect(nodes).toHaveAttribute("href", "/nodes");
    await nodes.click();
    await expect(page).toHaveURL(/\/nodes$/);
    await page.getByRole("button", { name: "Filter", exact: true }).click();
    await expect(page.getByRole("button", { name: /Filter by cluster/ })).toContainText("All clusters");
  });
});
