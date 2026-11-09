import { test, expect, type Page } from "@playwright/test";

const remoteNode = "gp-demo-remote-control-plane";
const localNode = "gp-demo-central-control-plane";

async function expectInventoryFits(page: Page, nodeName: string) {
  const main = page.getByRole("main");
  const heading = main.getByRole("heading", { name: "Nodes", exact: true });
  const viewport = page.viewportSize()!;
  for (const control of [
    heading,
    main.getByRole("button", { name: /Add node/i }),
    main.getByRole("button", { name: /Filter by cluster/ }),
  ]) {
    const bounds = await control.boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.width).toBeGreaterThan(0);
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.y).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height);
  }
  expect(await heading.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await main.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  const nodeBounds = await main.getByText(nodeName, { exact: true }).boundingBox();
  const inventory = main.getByRole(viewport.width < 768 ? "list" : "grid", { name: "Node inventory" });
  const statusBounds = await inventory.getByText("Ready", { exact: true }).boundingBox();
  expect(nodeBounds).not.toBeNull();
  expect(statusBounds).not.toBeNull();
  expect(nodeBounds!.x + nodeBounds!.width).toBeLessThanOrEqual(statusBounds!.x);
}

test.describe("multicluster administration", () => {
  test.skip(process.env.GAMEPLANE_E2E_TARGET === "live", "Deterministic independent node fixtures use the mock API.");

  test.beforeEach(async ({ context }) => {
    await context.addCookies([{ name: "e2e_multicluster", value: "1", url: "http://localhost:5173" }]);
  });

  for (const width of [1440, 375]) {
    test(`combines same-named servers and filters without changing their identity at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(() => localStorage.setItem("gameplane.cluster", "remote-demo"));
      await page.goto("/servers");
      const main = page.getByRole("main");
      if (width >= 768) await expect(main.getByRole("heading", { name: "Servers", exact: true })).toBeVisible();
      else await expect(main).toBeVisible();
      await expect(page.getByRole("button", { name: "Select cluster", exact: true })).toHaveCount(0);
      const links = main.getByRole("link", { name: "smoke-same", exact: true });
      await expect(links).toHaveCount(2);
      const targets = await links.evaluateAll((elements) => elements.map((element) => {
        const url = new URL((element as HTMLAnchorElement).href);
        return [url.pathname, url.searchParams.get("cluster"), url.searchParams.get("ns")];
      }));
      expect(targets).toEqual([
        ["/servers/smoke-same", "local", "gameplane-games"],
        ["/servers/smoke-same", "remote-demo", "gameplane-games"],
      ]);
      await expect(main.getByText("local / gameplane-games", { exact: true })).toBeVisible();
      await expect(main.getByText("remote-demo / gameplane-games", { exact: true })).toBeVisible();

      await expect(main.getByRole("button", { name: /Filter by cluster/ })).toHaveCount(0);
      const filter = main.getByRole("button", { name: /^Filter(?:\s*\d+)?$/ });
      await filter.click();
      await page.getByRole("button", { name: /Filter by cluster/ }).click();
      await page.getByRole("option", { name: "remote-demo", exact: true }).click();
      await Promise.all([
        page.waitForRequest((request) => {
          const url = new URL(request.url());
          return url.pathname === "/fleet/servers" && url.searchParams.get("cluster") === "remote-demo";
        }),
        page.getByRole("button", { name: "Apply", exact: true }).click(),
      ]);
      await expect(links).toHaveCount(1);
      await expect(links).toHaveAttribute("href", /cluster=remote-demo/);
      await expect(main.getByText("local / gameplane-games", { exact: true })).toHaveCount(0);
      expect(await page.evaluate(() => localStorage.getItem("gameplane.cluster"))).toBe("remote-demo");

      await filter.click();
      await page.getByRole("button", { name: /Filter by cluster/ }).click();
      await page.getByRole("option", { name: "All clusters", exact: true }).click();
      await page.getByRole("button", { name: "Apply", exact: true }).click();
      await expect(links).toHaveCount(2);
      expect(await main.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    });

    test(`shows both clusters and selected nodes at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(() => localStorage.setItem("gameplane.cluster", "remote-demo"));
      await page.goto("/nodes");
      await expect(page.getByText(localNode, { exact: true })).toBeVisible();
      await expect(page.getByText(remoteNode, { exact: true })).toBeVisible();
      await page.goto("/clusters");
      await expect(page.getByRole("heading", { name: "Clusters", exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "Select cluster", exact: true })).toHaveCount(0);
      await expect(page.getByRole("heading", { name: "Remote demo", exact: true })).toBeVisible();
      await page.getByRole("button", { name: "View nodes in Remote demo", exact: true }).click();
      await expect(page).toHaveURL(/\/nodes\?cluster=remote-demo$/);
      await expect(page.getByRole("main").getByRole("button", { name: /Filter by cluster/ })).toBeVisible();
      await expect(page.getByText(remoteNode, { exact: true })).toBeVisible();
      await expect(page.getByText(localNode, { exact: true })).toHaveCount(0);
      await expect(page.getByRole("button", { name: /Add node/i })).toBeDisabled();
      await expectInventoryFits(page, remoteNode);
      await page.getByRole("button", { name: /Filter by cluster/ }).click();
      await page.getByRole("option", { name: "local", exact: true }).click();
      await expect(page).toHaveURL(/\/nodes\?cluster=local$/);
      await expect(page.getByText(localNode, { exact: true })).toBeVisible();
      await expect(page.getByText(remoteNode, { exact: true })).toHaveCount(0);
      await expectInventoryFits(page, localNode);
      await page.goto("/clusters/remote-demo?tab=configuration");
      await expect(page.getByRole("button", { name: /Download kubeconfig/i })).toBeDisabled();
      await page.goto("/clusters");
      await page.getByRole("button", { name: "View servers in Remote demo", exact: true }).click();
      await expect(page).toHaveURL(/\/servers\?cluster=remote-demo$/);
      await expect(page.getByRole("main").getByRole("link", { name: "smoke-same", exact: true })).toHaveCount(1);
      await expect(page.getByRole("button", { name: "Select cluster", exact: true })).toHaveCount(0);
      expect(await page.evaluate(() => localStorage.getItem("gameplane.cluster"))).toBe("remote-demo");
    });
  }
});
