import { test, expect, type Page } from "@playwright/test";
import { LoginPage } from "../pages/LoginPage";
import { ServerDetailPage } from "../pages/ServerDetailPage";

// Files tab e2e. The tab proxies through the API to the agent's
// /files/{list,read,write,mkdir,delete}. MSW seeds the listing with
// "server.properties" and "world/"; this spec proves the wire-up
// without exercising the real agent.

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

test.describe("file manager tab", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET === "live",
    "file write/delete on a live agent mutates real volume contents; mock mode is the safe path",
  );

  test.beforeEach(async ({ page }) => {
    await loginIfNeeded(page);
  });

  test("renders the listing from /files/list", async ({ page }) => {
    const serverDetail = new ServerDetailPage(page);
    await serverDetail.goto("alpha");
    await page.waitForLoadState("domcontentloaded");

    const tabNav = page.getByRole("tablist", { name: /Server detail tabs/i });
    await tabNav.getByRole("tab", { name: /^files$/i }).click();

    // Wait for the listing fetch to complete, then assert seeded entries
    // are visible. The MSW handler returns server.properties + world/.
    await expect(page.getByText("server.properties")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/^world$/)).toBeVisible();
  });

  test("clicking a file fires a /files/read request", async ({ page }) => {
    const serverDetail = new ServerDetailPage(page);
    await serverDetail.goto("alpha");
    await page.waitForLoadState("domcontentloaded");

    const tabNav = page.getByRole("tablist", { name: /Server detail tabs/i });
    await tabNav.getByRole("tab", { name: /^files$/i }).click();
    await expect(page.getByText("server.properties")).toBeVisible({ timeout: 10_000 });

    const readReq = page.waitForRequest(
      (req) => /\/files\/read/.test(req.url()) && req.method() === "GET",
    );
    await page.getByText("server.properties").click();
    await readReq;
  });

  test("loads TypeScript on opening a script and provides diagnostics, completions, and definitions", async ({ page, context }) => {
    test.setTimeout(90_000);
    await page.addInitScript(() => localStorage.setItem("gameplane-e2e-dataset", "typescript-file"));
    const wasmUrls: URL[] = [];
    context.on("response", (response) => {
      const url = new URL(response.url());
      if (/\/tsc(?:-[\w-]+)?\.wasm$/.test(url.pathname) &&
        response.ok() && response.headers()["content-type"]?.startsWith("application/wasm")) {
        wasmUrls.push(url);
      }
    });

    const server = new ServerDetailPage(page);
    await server.goto("alpha");
    await server.clickTab("Settings");
    await page.getByRole("tab", { name: "Placement", exact: true }).click();
    await expect(page.locator(".monaco-editor .view-line").first()).toBeVisible();
    await server.clickTab("Files");
    const file = page.getByText("server.ts", { exact: true });
    await expect(file).toBeVisible();
    expect(wasmUrls).toHaveLength(0);
    const openedAt = performance.now();
    await file.click();

    const editor = page.locator(".monaco-editor");
    await expect(editor.locator(".squiggly-error").first()).toBeVisible({ timeout: 60_000 });
    const firstDiagnosticMs = Math.round(performance.now() - openedAt);
    expect(wasmUrls.map((url) => url.origin)).toContain(new URL(page.url()).origin);
    await editor.locator(".view-line").first().click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.insertText("const port: number = 25565;");
    await expect(editor.locator(".squiggly-error")).toHaveCount(0);
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await page.keyboard.insertText("port.toF");
    const completionRequestedAt = performance.now();
    await page.keyboard.press("Control+Space");
    const suggestion = editor.getByRole("listbox", { name: "Suggest" }).getByText("toFixed", { exact: true });
    await expect(suggestion).toBeVisible();
    const completionMs = Math.round(performance.now() - completionRequestedAt);
    console.log(`Monaco TS7: file click to first error ${firstDiagnosticMs} ms, completion shortcut to suggestion ${completionMs} ms`);

    await suggestion.click();
    await expect(editor.locator(".view-line").last()).toContainText("port.toFixed");
    await page.keyboard.press("Home");
    for (let i = 0; i < "port.to".length; i++) await page.keyboard.press("ArrowRight");
    // Monaco binds Peek Definition to a different shortcut on Linux.
    const linux = await page.evaluate(() => navigator.userAgent.includes("Linux"));
    await page.keyboard.press(linux ? "Control+Shift+F10" : "Alt+F12");
    const peek = page.locator(".reference-zone-widget");
    await expect(peek).toContainText("lib.es5.d.ts");
    await expect(peek.locator(".preview .view-lines")).toContainText(/toFixed\s*\(fractionDigits\?:\s*number\):\s*string/);
  });
});
