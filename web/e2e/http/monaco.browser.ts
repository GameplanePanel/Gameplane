import { expect, test } from "@playwright/test";
import { makeFileEntry, makeServer, makeTemplate, makeUser } from "../../src/test/factories";

function browserCapabilities() {
  return {
    secure: self.isSecureContext,
    isolated: self.crossOriginIsolated,
    sharedMemory: typeof SharedArrayBuffer !== "undefined",
    jspi: "Suspending" in WebAssembly && "promising" in WebAssembly,
  };
}

test("checks and completes a TypeScript file over plain LAN HTTP", async ({ page }) => {
  const target = { cluster: "local", namespace: "gameplane-games", name: "alpha", uid: "alpha-uid" };
  const content = 'const port: number = "25565";';
  const file = makeFileEntry({ name: "server.ts", path: "/data/server.ts", size: content.length });
  const fixtures: Record<string, unknown> = {
    "/users/me": makeUser(),
    "/users/me/servers": { items: [] },
    "/clusters": { items: [] },
    "/admin/installation": { standalone: false, localCluster: false },
    "/admin/telemetry/notice": { pending: false },
    "/servers/alpha": makeServer({ metadata: { uid: target.uid } }),
    "/servers/alpha/access": {
      target, permissions: ["*"], canWrite: true, canControl: true, canConsole: true,
      canDelete: true, isOwner: true, isCollaborator: false,
    },
    "/servers/alpha/events": [],
    "/servers/alpha/files/list": [file],
    "/templates/minecraft-vanilla": makeTemplate(),
    "/modules": { items: [] },
  };
  await page.route(url => url.pathname in fixtures || url.pathname === "/servers/alpha/files/read", async route => {
    if (route.request().isNavigationRequest()) return route.continue();
    const path = new URL(route.request().url()).pathname;
    if (path === "/servers/alpha/files/read") return route.fulfill({ contentType: "text/plain", body: content });
    return route.fulfill({ json: fixtures[path] });
  });
  await page.goto("/servers/alpha?cluster=local&ns=gameplane-games");
  const expected = { secure: false, isolated: false, sharedMemory: false, jspi: true };
  expect(await page.evaluate(browserCapabilities)).toEqual(expected);
  await page.getByRole("tab", { name: "Files", exact: true }).click();
  const workerStarted = page.waitForEvent("worker", worker => /\/monaco-ts-worker-[^/]+\.js$/.test(new URL(worker.url()).pathname));
  const openedAt = performance.now();
  await page.getByText("server.ts", { exact: true }).click();
  const worker = await workerStarted;
  expect(await worker.evaluate(browserCapabilities)).toEqual(expected);

  const editor = page.locator(".monaco-editor");
  await expect(editor.locator(".squiggly-error").first()).toBeVisible({ timeout: 60_000 });
  const startupMs = Math.round(performance.now() - openedAt);
  await editor.locator(".view-line").first().click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.insertText("const port: number = 25565;");
  await expect(editor.locator(".squiggly-error")).toHaveCount(0);
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await page.keyboard.insertText("port.toF");
  const requestedAt = performance.now();
  await page.keyboard.press("Control+Space");
  await expect(editor.getByRole("listbox", { name: "Suggest" }).getByText("toFixed", { exact: true })).toBeVisible();
  console.log(`Plain HTTP TS7: file click to first error ${startupMs} ms, completion shortcut to suggestion ${Math.round(performance.now() - requestedAt)} ms`);
});
