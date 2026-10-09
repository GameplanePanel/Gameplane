import { test, expect } from "@playwright/test";
import type { APIRequestContext, Locator, Page } from "@playwright/test";
import { loginIfNeeded, seedServer, seedTemplate } from "./_seed";
import { ServerDetailPage } from "../../pages/ServerDetailPage";

// T190 (specs/done_014-heroui-web-rebuild/tasks.md): live share-link coverage —
// this is the feature's E2E tier per OD-3 (Settled 2026-09-03); no
// corresponding test/e2e/ Go test is added for share links.
//
// ShareLinksSection (web/src/routes/tabs/settings/ShareLinks.tsx) was, at
// the time the first test below was written, built but not yet mounted
// into Settings.tsx's SECTIONS, so that test drives the authenticated
// create/revoke calls directly via page.request (same-session cookies, so
// no extra login) against the real API — the same technique _seed.ts uses
// for template/server fixtures. See the T030 note below: the dialog is
// now mounted and is driven through real UI instead. The public
// /share/$token route (registered in web/src/router/tree.tsx) IS real UI
// and is exercised as such, in a separate, unauthenticated browser context
// to genuinely test "signed out" rather than merely a page an admin
// session happens to be able to see.
//
// One seeded template + one seeded GameServer, one real login (via
// storageState / loginIfNeeded's no-op safety net) covers the whole file,
// mirroring servers-core.spec.ts's login-budget discipline.
//
// T030 (specs/done_017-share-link-expiry/tasks.md): ShareLinksSection is now
// mounted into Settings.tsx's SECTIONS (Phase 5 landed), so the redesigned
// create dialog (six expiry choices, "No expiry", "Custom") has real UI to
// drive. The tests below reach it the way a user would — ServerDetailPage's
// Settings tab, then the "Share links" sub-tab — and drive the HeroUI
// Select the same way ShareLinks.test.tsx's component tests do (open the
// trigger button showing the current selection, click the target
// `option`), rather than posting to the API directly. The original test
// above is kept as-is (it predates the redesign and still documents the
// deprecated `expiresIn` path against the raw endpoint).

test.describe("live: share links", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "the mock spec (web/e2e/specs/slice5.spec.ts) covers share-link states against a scripted fetch shim",
  );

  const stamp = Date.now().toString(36);
  const tmplName = `e2e-pw-share-tmpl-${stamp}`;
  const serverName = `e2e-pw-share-${stamp}`;
  let cleanups: Array<(request: APIRequestContext) => Promise<void>> = [];

  test.beforeAll(async ({ request }) => {
    const tmpl = await seedTemplate(request, tmplName);
    const server = await seedServer(request, {
      name: serverName,
      template: tmplName,
      description: "Live share-link probe",
    });
    cleanups = [server.cleanup, tmpl.cleanup];
  });

  test.afterAll(async ({ request }) => {
    for (const c of cleanups) await c(request);
  });

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
  });

  // page.request shares the authenticated browser context's cookies, but
  // mutating calls still need the CSRF header the SPA's own fetch wrapper
  // adds automatically (api/internal/auth/sessions.go's double-submit
  // check) — read it out of the same cookie jar, mirroring _seed.ts's
  // seedHeaders (not exported, so reimplemented locally here).
  async function csrfHeaders(p: Page): Promise<Record<string, string>> {
    const cookies = await p.context().cookies();
    const token = cookies.find((c) => c.name === "gameplane_csrf")?.value ?? "";
    return { "X-Gameplane-CSRF": token, Accept: "application/json" };
  }

  test("create a link, resolve it signed out, revoke it, and see Invalid signed out", async ({
    page,
    browser,
  }) => {
    // Create the link as the authenticated admin (server owner) —
    // api/internal/handlers/shares.go: "only the server owner can manage
    // shares", and seedServer created this one as this same admin.
    const createRes = await page.request.post(`/servers/${serverName}:shares`, {
      headers: await csrfHeaders(page),
      data: { canStart: true, expiresIn: "24h" },
    });
    expect(createRes.ok(), await createRes.text().catch(() => "")).toBeTruthy();
    const created = (await createRes.json()) as { id: string; token: string };
    expect(created.token).toBeTruthy();
    expect(created.id).toBeTruthy();

    // Resolve signed out: a genuinely separate, storageState-free context
    // — no admin session cookie ever exists here — so this is the same
    // request an anonymous link recipient would make.
    const anonContext = await browser.newContext();
    const anonPage = await anonContext.newPage();
    try {
      await anonPage.goto(`/share/${created.token}`);
      await anonPage.waitForLoadState("domcontentloaded");

      // Every valid state (Up / Asleep-can-start / Starting) renders the
      // server's own name as the page's <h1>; only Invalid does not. A
      // freshly-created GameServer may be in any of those three phases
      // depending on how far the operator has gotten, so assert on the
      // name rather than a specific status chip.
      const nameHeading = anonPage.getByRole("heading", { name: serverName });
      await expect(nameHeading).toBeVisible({ timeout: 20_000 });
      await expect(anonPage.getByRole("heading", { name: /link not available/i })).toHaveCount(0);

      // Best-effort start action: only meaningful if the server happened
      // to resolve into the Asleep-can-start state during the window
      // above. Not asserted as required — this is a bonus check per
      // T190, not the spec's core flow.
      const startButton = anonPage.getByRole("button", { name: /start server/i });
      if (await startButton.isVisible().catch(() => false)) {
        await startButton.click();
        await expect(anonPage.getByText(/starting|online/i).first()).toBeVisible({
          timeout: 15_000,
        });
      }

      // Revoke as the admin, in the original authenticated page.
      const revokeRes = await page.request.delete(`/servers/${serverName}/shares/${created.id}`, {
        headers: await csrfHeaders(page),
      });
      expect(revokeRes.ok(), await revokeRes.text().catch(() => "")).toBeTruthy();

      // Reopen signed out: the same token now resolves to Invalid. FR-005:
      // the copy must not hint that the link ever existed.
      await anonPage.goto(`/share/${created.token}`);
      await expect(
        anonPage.getByRole("heading", { name: /link not available/i }),
      ).toBeVisible({ timeout: 20_000 });
      await expect(
        anonPage.getByText(/this link may be invalid, expired, or revoked/i),
      ).toBeVisible();
    } finally {
      await anonContext.close();
    }
  });

  // Formats a Date the same way ShareLinks.tsx's formatDate() does, so
  // assertions below match the list's rendered text exactly.
  function formatExpiry(date: Date): string {
    return date.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
  }

  // Opens the Settings tab's "Share links" sub-section and clicks "Create
  // link", leaving the dialog open for the caller to drive. Returns the
  // dialog's own Locator so callers can scope every subsequent interaction
  // to it — mirrors slice5.spec.ts's `page.getByRole("dialog", { name })`
  // pattern for the same three dialogs (atqRh/VM7ro/S7SCDc).
  //
  // The opener button itself is scoped to the app's <main> landmark
  // (AppShell.tsx renders it; AppLayout.tsx's Sidebar-drawer comment notes
  // HeroUI portals dialog content to document.body, i.e. outside <main>),
  // not because a dialog could be open yet here (none is), but so this
  // stays correct even if a previous dialog's DOM lingers — the button text
  // "Create link" is shared with the create dialog's own submit button.
  async function openCreateDialog(page: Page): Promise<Locator> {
    const detail = new ServerDetailPage(page);
    await detail.goto(serverName);
    await expect(page.getByRole("heading", { name: serverName })).toBeVisible({ timeout: 20_000 });
    await detail.clickTab("Settings");
    await page.getByRole("tab", { name: /^share links$/i }).click();
    // The persistent header opener comes first; an empty list also renders
    // the same action in its empty state (including on a fresh retry worker).
    await page.getByRole("main").getByRole("button", { name: "Create link", exact: true }).first().click();
    const dialog = page.getByRole("dialog", {
      name: new RegExp(`^create share link for ${serverName}$`, "i"),
    });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    return dialog;
  }

  // Confirms the create dialog, closes the follow-up "Share link created"
  // dialog, and waits for the list to refresh. Every locator here is scoped
  // to its own dialog so it can't collide with the page's "Create link"
  // button (shared name) or with the other dialog's "Done"/"Copy link"
  // controls.
  async function submitAndCloseCreatedDialog(createDialog: Locator, page: Page): Promise<void> {
    await createDialog.getByRole("button", { name: "Create link" }).click();
    const createdDialog = page.getByRole("dialog", { name: /^share link created$/i });
    await expect(createdDialog).toBeVisible({ timeout: 10_000 });
    await createdDialog.getByRole("button", { name: "Done" }).click();
    await expect(createdDialog).toHaveCount(0);
  }

  test("create a link via a preset expiry choice and see it in the list", async ({ page }) => {
    const dialog = await openCreateDialog(page);

    // Drive the HeroUI Select: open it (default is "30 days") and pick
    // "60 days", a genuine selection change (mirrors
    // ShareLinks.test.tsx's component-test pattern). Scoped to the create
    // dialog so it can't match a same-named control elsewhere.
    await dialog.getByRole("button", { name: /30 days/ }).click();
    await page.getByRole("option", { name: "60 days" }).click();

    const expectedDate = formatExpiry(new Date(Date.now() + 60 * 24 * 60 * 60 * 1000));
    await submitAndCloseCreatedDialog(dialog, page);

    const grid = page.getByRole("grid", { name: "Share links" });
    await expect(grid.getByText(expectedDate).first()).toBeVisible({ timeout: 10_000 });
  });

  test("create a 'No expiry' link and see 'Never' in the list", async ({ page }) => {
    const dialog = await openCreateDialog(page);

    await dialog.getByRole("button", { name: /30 days/ }).click();
    await page.getByRole("option", { name: "No expiry" }).click();

    // FR-002: the warning shows once "No expiry" is selected.
    await expect(dialog.getByText(/this link works until you revoke it/i)).toBeVisible();

    await submitAndCloseCreatedDialog(dialog, page);

    const grid = page.getByRole("grid", { name: "Share links" });
    await expect(grid.getByText("Never", { exact: true }).first()).toBeVisible({
      timeout: 10_000,
    });
    // FR-007/SC-004: a null-expiry link is never reported "Expired". Scoped
    // to this link's own row, not the whole grid, so an unrelated expired
    // link elsewhere in the grid can't fail this assertion.
    const neverRow = grid.getByRole("row").filter({ hasText: "Never" });
    await expect(neverRow.getByText(/^expired$/i)).toHaveCount(0);
  });

  test("create a link with a custom expiry date and see it in the list", async ({ page }) => {
    const dialog = await openCreateDialog(page);

    await dialog.getByRole("button", { name: /30 days/ }).click();
    await page.getByRole("option", { name: "Custom" }).click();

    // FR-003: a date strictly after today. Pick 20 days out — comfortably
    // valid and well under the 365-day long-lived threshold (OD-6), so this
    // test stays focused on the plain custom-date path.
    const customDate = new Date();
    customDate.setDate(customDate.getDate() + 20);
    const y = customDate.getFullYear();
    const m = String(customDate.getMonth() + 1).padStart(2, "0");
    const d = String(customDate.getDate()).padStart(2, "0");
    await dialog.getByLabel("Expires on").fill(`${y}-${m}-${d}`);

    const expectedDate = formatExpiry(customDate);
    await submitAndCloseCreatedDialog(dialog, page);

    const grid = page.getByRole("grid", { name: "Share links" });
    await expect(grid.getByText(expectedDate).first()).toBeVisible({ timeout: 10_000 });
  });
});
