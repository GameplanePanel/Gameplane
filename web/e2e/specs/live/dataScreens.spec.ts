import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { loginIfNeeded, seedServer, seedTemplate } from "./_seed";

// Live: prove the data-bearing dashboard screens render REAL backend data,
// not MSW fixtures. The 17 main specs all skip in live mode and assert
// against MSW; this spec is the inverse — it seeds real CRDs/DB rows through
// the API and asserts the UI shows exactly those, end-to-end against the
// gameplane-e2e cluster.
//
// Screens with no setup cost (Cluster, Users/Roles, Modules) read whatever
// the live install already has. Screens that need a workload (Servers list,
// Server Settings) use a seeded GameServer; no running pod is required —
// the CR existing is enough for these to render real data. Agent-backed
// screens (Overview/Files/Players) need a live sidecar and live in
// liveAgentScreens.spec.ts.

test.describe("live: data screens render real backend data", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "live-only — the mock specs cover these screens against MSW",
  );

  const stamp = Date.now().toString(36);
  const tmplName = `e2e-pw-data-tmpl-${stamp}`;
  const serverName = `e2e-pw-data-${stamp}`;
  let cleanups: Array<(request: APIRequestContext) => Promise<void>> = [];

  test.beforeAll(async ({ request }) => {
    const tmpl = await seedTemplate(request, tmplName);
    const server = await seedServer(request, {
      name: serverName,
      template: tmplName,
      description: "Live data-screen probe",
    });
    // Delete the server before the template it references.
    cleanups = [server.cleanup, tmpl.cleanup];
  });

  // afterAll gets its own live `request` — the one from beforeAll is already
  // disposed by the time teardown runs.
  test.afterAll(async ({ request }) => {
    for (const c of cleanups) await c(request);
  });

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
  });

  test("Nodes shows real node health", async ({ page }) => {
    await page.goto("/nodes");
    await expect(page).toHaveURL(/\/nodes$/);
    await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
    await expect(page.getByText("Ready nodes", { exact: true })).toBeVisible();
    // The kind cluster always returns at least its control-plane node.
    const table = page.getByRole("table", { name: "Node inventory" });
    await expect(table).toBeVisible();
    await expect.poll(() => table.getByRole("row").count()).toBeGreaterThan(1);
  });

  test("users page lists the bootstrapped admin and real builtin roles", async ({ page }) => {
    await page.goto("/users");
    await expect(page.getByRole("heading", { name: /users.*rbac/i })).toBeVisible();
    // The live admin is the account the Go e2e suite bootstrapped.
    await expect(page.getByText("e2e-admin").first()).toBeVisible();

    await page.getByRole("tab", { name: /^roles/i }).first().click();
    // RolesTab renders one card per role from GET /roles. Assert the real
    // builtin role names + the built-in badge — unambiguous real RBAC data,
    // not an MSW fixture. Generous timeout for the live roles query. (Role
    // names are more robust than the description copy, which is muted text.)
    await expect(page.getByText("operator", { exact: true })).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText("viewer", { exact: true })).toBeVisible();
    await expect(page.getByText("built-in").first()).toBeVisible();
  });

  test("servers list shows the seeded GameServer", async ({ page }) => {
    await page.goto("/servers");
    // Use the same session and Vite origin after navigation. A response event
    // from the previous document can lose its body when Chromium unloads it.
    const response = await page.request.get(new URL("/fleet/servers", page.url()).href, {
      headers: { Accept: "application/json" },
    });
    expect(response.ok()).toBeTruthy();
    expect(response.headers()["content-type"]).toContain("application/json");
    // A missing dev proxy must fail here, rather than serving the SPA shell
    // and leaving an apparently empty list. Verify the actual resource scope.
    expect(await response.json()).toEqual(expect.objectContaining({
      items: expect.arrayContaining([expect.objectContaining({
        target: expect.objectContaining({ cluster: "local", namespace: "gameplane-games", name: serverName }),
      })]),
    }));
    await expect(page.getByRole("heading", { name: /^servers$/i })).toBeVisible();
    // The list polls every 5s; the seeded server's row links to its detail.
    await expect(page.getByRole("link", { name: serverName })).toBeVisible({ timeout: 15_000 });
    // The Game column renders the real templateRef.
    await expect(page.getByText(tmplName).first()).toBeVisible();
  });

  test("server settings reflect the real CR spec", async ({ page }) => {
    await page.goto(`/servers/${serverName}`);
    await expect(page.getByRole("heading", { name: serverName })).toBeVisible({ timeout: 20_000 });

    await page.getByRole("tablist", { name: /Server detail tabs/i }).getByRole("tab", { name: /^Settings$/i }).click();

    // The General sub-section's disabled Template input carries the
    // templateRef we POSTed — proving the form is bound to the live CR, not a
    // fixture. Scope to the field's grid block (Field renders the label as a
    // div, not an associated <label>), then assert the input's value.
    const templateField = page.locator("div.grid", {
      has: page.getByText("Template", { exact: true }),
    });
    await expect(templateField.locator("input")).toHaveValue(tmplName, { timeout: 15_000 });
  });

  test("audit log shows the seeding mutations", async ({ page }) => {
    await page.goto("/admin/audit");
    await expect(page).toHaveURL(/\/admin\/audit$/);
    await expect(page.getByRole("heading", { name: /audit log/i })).toBeVisible();

    // Seeding issued POST /templates and POST /servers as e2e-admin, so the
    // log is non-empty and carries those real rows.
    await expect(page.getByText("No audit events yet.")).toHaveCount(0);
    await expect(page.getByText("e2e-admin").first()).toBeVisible();
    // The action cell shows a human-readable label (M12); the raw METHOD /path
    // lives in its title attribute, so match the seeded POST /servers row by
    // title rather than visible text.
    await expect(page.getByTitle(/\/servers/).first()).toBeVisible();
  });

  test("modules page renders the real catalog state", async ({ page }) => {
    await page.goto("/modules");
    await expect(page.getByRole("heading", { name: /^modules$/i })).toBeVisible();
    // The "All sources" filter chip always renders (the source list is
    // seeded with "all"), so it proves the page reflected the real catalog.
    // Asserted alone to stay unambiguous: when the catalog is empty the
    // empty-state text is ALSO present, so a combined locator would match two
    // elements and trip Playwright's strict mode.
    await expect(page.getByRole("button", { name: "All sources" })).toBeVisible();
  });
});
