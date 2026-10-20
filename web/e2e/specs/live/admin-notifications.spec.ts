import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { loginIfNeeded, apiSend, seedHeaders } from "./_seed";

// Live coverage for the admin Notifications section (web/src/routes/AdminSettings.tsx,
// NotificationsSection). Drives the real UI against the real API: sink add/edit/delete
// persist through PUT /admin/config/notifications and the labelled Secret, and the SSRF
// guard is observed at delivery time through "Send test".
//
// PREMISE NOTE (edit step): no UI edits a sink's URL, and the URL is never returned to the
// browser (it is written only into a labelled Secret). The "edit" step therefore toggles the
// sink's enabled switch, saves, reloads and asserts the persisted state flipped.
//
// PREMISE NOTE (error path): saving does NOT reject http://169.254.169.254/. The API only
// checks scheme and host on save (api/internal/handlers/notifications.go requireURL). The
// SSRF block happens at delivery (netguard.IsAllowed in notify.HTTPClient), so the error
// surfaces only after "Send test", as the row's inline result text.

test.describe("live: admin notifications", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "the mock spec covers notification states against a scripted fetch shim",
  );

  let sink = "";

  test.beforeEach(async ({ page }) => {
    // Lowercase DNS label, well under the 46-character limit.
    sink = `e2e-notif-${Date.now().toString(36)}`;
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
    await page.goto("/admin?section=notifications");
    await expect(page.getByText("Notifications", { exact: true }).first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test.afterEach(async ({ request }) => {
    if (!sink) return;
    // Best-effort cleanup: drop only this test's sink from the list, then its Secret.
    const res = await request
      .get("/admin/config", { headers: await seedHeaders(request) })
      .catch(() => undefined);
    if (res?.ok()) {
      const cfg = (await res.json()) as { notifications?: { sinks?: { name: string }[] } };
      const sinks = (cfg.notifications?.sinks ?? []).filter((s) => s.name !== sink);
      await apiSend(request, "PUT", "/admin/config/notifications", {
        ...cfg.notifications,
        sinks,
      }).catch(() => undefined);
    }
    await apiSend(request, "DELETE", `/admin/notifications/sinks/${sink}/secret`).catch(
      () => undefined,
    );
  });

  async function addWebhookSink(page: Page, url: string) {
    await page.getByRole("button", { name: "Add sink", exact: true }).click();
    await page.getByPlaceholder("team-alerts").fill(sink);
    await page.getByRole("button", { name: /Sink kind/i }).click();
    await page.getByRole("option", { name: /webhook/i }).click();
    await page.getByPlaceholder("https://example.com/hook").fill(url);
    // Form submit. The header "Add sink" button is replaced by the form while adding.
    await page.getByRole("button", { name: "Add sink", exact: true }).click();
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 15_000 });
  }

  test("webhook sink create, edit and delete persist across reload", async ({ page }) => {
    await addWebhookSink(page, "http://gameplane-api.gameplane-system.svc/healthz");

    await page.reload();
    const row = page.getByRole("listitem").filter({ hasText: sink });
    await expect(row).toBeVisible({ timeout: 15_000 });
    await expect(row).toContainText(`webhook · Secret: gameplane-notify-${sink}`);

    // Edit substitute: disable the sink (see PREMISE NOTE above), save, reload.
    // HeroUI's Switch hides the role=switch input under decorative spans, so a pointer
    // click is intercepted; toggle it the way a keyboard user does (as theme-customization does).
    await row.getByRole("switch", { name: `Disable sink ${sink}` }).press("Space");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 15_000 });

    await page.reload();
    await expect(page.getByRole("switch", { name: `Enable sink ${sink}` })).toBeVisible({
      timeout: 15_000,
    });

    // Delete, save, reload.
    await page.getByRole("button", { name: `Delete sink ${sink}` }).click();
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 15_000 });

    await page.reload();
    await expect(page.getByText("No notification sinks configured.")).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByRole("listitem").filter({ hasText: sink })).toHaveCount(0);
  });

  test("SSRF-blocked webhook URL is rejected at delivery and rendered inline", async ({ page }) => {
    // Save accepts the link-local address; the block happens when the test is delivered.
    await addWebhookSink(page, "http://169.254.169.254/");

    const row = page.getByRole("listitem").filter({ hasText: sink });
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.getByRole("button", { name: "Send test" }).click();
    await expect(row.getByText(/not an allowed destination/i)).toBeVisible({ timeout: 20_000 });
  });
});
