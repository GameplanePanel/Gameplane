import { expect, test } from "@playwright/test";

test("opens an unlisted file directly and autosaves it in the local editor", async ({ page }) => {
  test.skip(process.env.GAMEPLANE_E2E_TARGET === "live", "Uses the mock server volume.");
  await page.goto("/servers/alpha?cluster=local&ns=gameplane-games&file=%2Fconfig%2Funlisted.txt");
  await expect(page.getByRole("tab", { name: "Files", exact: true })).toHaveAttribute("aria-selected", "true");
  const editor = page.locator(".monaco-editor");
  await expect(editor.locator(".view-line").first()).toContainText("# mock file body");
  const saved = page.waitForRequest(req => new URL(req.url()).pathname === "/servers/alpha/files/write");
  await editor.locator(".view-line").first().click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.insertText("motd=hello");
  const write = await saved;
  expect(new URL(write.url()).searchParams.get("path")).toBe("/config/unlisted.txt");
  expect(new URL(write.url()).searchParams.get("namespace")).toBe("gameplane-games");
  expect(write.postData()).toBe("motd=hello");
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Close /config/unlisted.txt" }).click();
  await expect(page.getByText("Select a file to edit.")).toBeVisible();
  expect(new URL(page.url()).searchParams.get("file")).toBe("/config/");
});
