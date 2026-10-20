import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { loginIfNeeded, apiSend } from "./_seed";

// Spec 013 US2 (T043): live coverage of the Admin > Mod registries section.
//
// The section lists three fixed keyed providers (AdminSettings.tsx
// keyedRegistryProviders): CurseForge, Steam Workshop and Nexus Mods. There is
// no free-form add, so every test drives the "Nexus Mods" row. Its API key is
// written to the labelled Secret gameplane-modreg-nexus in the control
// namespace (registry_secret.go, registry.DefaultKeySecretName).
//
// The tests share one registry row, so they run serially.

test.describe.configure({ mode: "serial" });

const NEXUS_SECRET = "gameplane-modreg-nexus";
const CONTROL_NS = "gameplane-system";
const CTX = `kind-${process.env.GAMEPLANE_E2E_CLUSTER ?? "gameplane-e2e"}`;

test.describe("live: mod registries", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "the mock spec (web/e2e/specs/adminSettings.spec.ts) covers the registries section against a scripted fetch shim",
  );

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
    await page.goto("/admin?section=modRegistries");
    // The settings nav also carries a "Mod registries" label, so wait on the section's own row.
    await expect(page.getByRole("listitem").filter({ hasText: "Nexus Mods" })).toBeVisible({
      timeout: 15_000,
    });
  });

  test.afterEach(async ({ request }) => {
    // Best-effort teardown: clear the config, drop the managed key Secret, then
    // remove any unmanaged Secret the error-path test seeded. Runs last so the
    // kubectl delete also removes a leftover unmanaged Secret.
    await apiSend(request, "PUT", "/admin/config/modRegistries", { registries: [] }).catch(() => undefined);
    await apiSend(request, "DELETE", "/admin/registries/nexus/secret").catch(() => undefined);
    try {
      execFileSync("kubectl", [
        "--context", CTX,
        "delete", "secret", NEXUS_SECRET,
        "-n", CONTROL_NS,
        "--ignore-not-found",
      ]);
    } catch {
      // best effort: kubectl may be unavailable in this runner
    }
  });

  test("set, persist, then remove the Nexus Mods API key", async ({ page }) => {
    const stamp = Date.now().toString(36);
    const row = page.getByRole("listitem").filter({ hasText: "Nexus Mods" });

    await expect(row.getByText("Not configured", { exact: true })).toBeVisible();
    await row.getByRole("button", { name: "Set API key" }).click();
    await page.getByLabel(/API key/i).fill(`e2e-nexus-key-${stamp}`);
    await page.getByRole("button", { name: /^Save$/ }).click();
    await expect(row.getByText("Configured", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: /^Save changes$/ }).click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 15_000 });

    // Persistence: a reload shows the saved state from the API.
    await page.reload();
    await expect(row.getByText("Configured", { exact: true })).toBeVisible();
    await expect(row.getByRole("button", { name: "Replace" })).toBeVisible();
    await expect(row.getByRole("button", { name: "Remove" })).toBeVisible();

    // Remove the key and save; the reload shows it gone again.
    await row.getByRole("button", { name: "Remove" }).click();
    await page.getByRole("button", { name: /^Save changes$/ }).click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 15_000 });

    await page.reload();
    await expect(row.getByText("Not configured", { exact: true })).toBeVisible();
    await expect(row.getByRole("button", { name: "Set API key" })).toBeVisible();
  });

  test("an unmanaged Secret with the same name fails the save inline", async ({ page }) => {
    // Seed a Secret with the name the API will write, but without the
    // Gameplane managed label. The API refuses to overwrite it (409).
    execFileSync("kubectl", [
      "--context", CTX,
      "delete", "secret", NEXUS_SECRET,
      "-n", CONTROL_NS,
      "--ignore-not-found",
    ]);
    execFileSync("kubectl", [
      "--context", CTX,
      "create", "secret", "generic", NEXUS_SECRET,
      "-n", CONTROL_NS,
      "--from-literal=apiKey=preexisting",
    ]);

    const stamp = Date.now().toString(36);
    const row = page.getByRole("listitem").filter({ hasText: "Nexus Mods" });

    await row.getByRole("button", { name: "Set API key" }).click();
    await page.getByLabel(/API key/i).fill(`e2e-nexus-key-${stamp}`);
    await page.getByRole("button", { name: /^Save$/ }).click();
    await page.getByRole("button", { name: /^Save changes$/ }).click();

    await expect(
      page.getByText(/a secret with that name exists but is not managed by Gameplane/i),
    ).toBeVisible();

    // The config row is written only after the Secret write succeeds, so a
    // reload must still show the registry as not configured.
    await page.reload();
    await expect(row.getByText("Not configured", { exact: true })).toBeVisible();
    await expect(row.getByRole("button", { name: "Set API key" })).toBeVisible();
  });
});
