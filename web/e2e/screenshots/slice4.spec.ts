import { test, expect, type Page, type BrowserContext } from "@playwright/test";
import { capture, captureLocator } from "./capture";

// T159 (specs/done_014-heroui-web-rebuild/tasks.md): Slice 4 — Admin Settings,
// Users & RBAC, Audit Log, System Logs, and Cluster Settings. Screenshot
// verification tests for the HeroUI rebuild, capturing the design frames
// listed in specs/done_014-heroui-web-rebuild/contracts/component-map.md's
// "Slice 4" entry / assets/design-export/MANIFEST.md, at 1440px, for comparison
// against assets/design-export/screenshots/<id>.png per
// contracts/screen-verification.md.
//
// Mirrors slice2a.spec.ts's structure (viewport, capture() helper,
// id-named PNGs, the useScreenshotDataset() localStorage switch) and is
// selected the same way — only when GAMEPLANE_SCREENSHOTS=1 (via
// playwright.config.ts's grep/grepInvert on the @screenshots tag).
//
// Several ids on the manifest are component crops (removable group chip
// variants, provenance badge variants, and standalone dialogs) rather than
// distinct screens. Each of those is captured with captureLocator() (see
// ./capture) scoped to the rendered element itself — not the full page —
// and, because the corresponding assets/design-export/screenshots/<id>.png for
// every one of those component ids was exported from Pencil's light
// palette while the rest of this suite captures the app in dark theme (per
// test.use({ colorScheme: "dark" }) above), each of those tests calls
// setTheme(page, "light") before navigating. uMiwd's Authentication section renders
// the "Overridden" provenance badge (R65Xyx) with an orange chip (XL5ZU)
// on its admin row and the "From Helm" badge (Rwnu3) with violet/secondary
// chips (vStkb/uw0dB) on its operator/viewer rows, each captured in light
// theme via its own scoped locator despite sharing one page state.
//
// screenshotConfig() (web/src/test/screenshotData.ts) stays in the plain
// "not yet configured" shape shared by most of the suite. The OIDC-
// configured state (uMiwd/R65Xyx/Rwnu3/XL5ZU/vStkb/uw0dB), the "no OIDC
// mappings yet" empty state (nNGDX/BV5ei), the "save rejected" state
// (QgW58), the failed audit-chain banner (kIxaJ), and the empty default
// storage class (dxdEi) are one-off variants selected per-test via a
// context.addCookies() cookie read inside buildScreenshotHandlers()
// (src/test/handlers.ts) — see setAdminConfigVariant()'s comment below for
// why a cookie, not a page.route override: those endpoints are answered by
// MSW's Service Worker (msw/browser), and Playwright cannot intercept a
// request a Service Worker's own fetch handler has already resolved.

// Full-page capture for this spec's routed-screen ids. Delegates to the
// shared web/e2e/screenshots/capture.ts helper (viewport sized to match the
// reference design frame, animations disabled) — see slice-3.spec.ts's
// header note for why this replaced a local `fullPage: true` screenshot
// (mismatched sizing strategy vs. the reference frame's own dimensions).

// Forces the app's own light/dark toggle (AppLayout.tsx THEME_STORAGE_KEY),
// which takes priority over the context's prefers-color-scheme — used by
// component crop tests (Kp48V, OIDC provenance-badge variants) that need
// to be captured in light theme per the design frame.
async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.addInitScript((t: string) => {
    try {
      window.localStorage.setItem("gameplane-theme", t);
    } catch {
      // localStorage unavailable (sandboxed)
    }
  }, theme);
}

// Selects the enriched screenshot dataset before the app's first fetch.
async function useScreenshotDataset(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("gameplane-e2e-dataset", "screenshots");
    } catch {
      // localStorage unavailable (sandboxed) — falls back to default handlers.
    }
  });
}

// Scoped to the Admin Settings section <nav> (AdminSettings.tsx) so this
// never collides with the header's notification-bell button, which also
// has the accessible name "Notifications" (NotificationsPanel.tsx) but
// isn't inside a <nav> landmark.
async function clickSection(page: Page, name: string): Promise<void> {
  const btn = page.locator("nav").getByRole("button", { name, exact: true });
  await expect(btn).toBeVisible({ timeout: 10_000 });
  await btn.click();
}

// Selects a GET /admin/config variant via a cookie read by
// buildScreenshotHandlers() (src/test/handlers.ts), the same
// context.addCookies() + MSW-cookie pattern slice2a.spec.ts uses for
// e2e_server_variant. A page.route() override does NOT work for this
// endpoint (or /admin/audit/verify, or the /admin/config/:section PUT
// below): MSW's Service Worker (msw/browser, src/test/browser-msw.ts)
// answers those requests itself, and Playwright cannot intercept a
// request already resolved inside a Service Worker's fetch handler.
// screenshotConfig() itself stays in the plain "not yet configured" shape
// so the rest of the suite is unaffected; must be called before page.goto.
async function setAdminConfigVariant(
  context: BrowserContext,
  variant: "oidc" | "oidc-empty-mappings" | "empty-storage-class" | "registries-curseforge",
): Promise<void> {
  await context.addCookies([
    { name: "e2e_admin_config_variant", value: variant, domain: "localhost", path: "/" },
  ]);
}

test.describe("Slice 4: Admin, Users, Audit, System logs, Cluster (Desktop — 1440x900) @screenshots", () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
  });

  test.beforeEach(async ({ page }) => {
    await useScreenshotDataset(page);
  });

  test("WZdnw: Admin Settings — General", async ({ page }) => {
    await page.goto("/admin");
    await expect(page.getByRole("heading", { name: /admin settings/i })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText(/instance name/i)).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "WZdnw");
  });

  test("uMiwd: Admin Settings — Authentication", async ({ page, context }) => {
    await setAdminConfigVariant(context, "oidc");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    // Helm OIDC Provider Card (read-only, groups claim + default role).
    await expect(page.getByText("Helm-seeded OIDC provider")).toBeVisible({ timeout: 10_000 });
    // Role Mapping Overrides Card — admin row is dashboard-overridden.
    // Scoped to the "Admin role mapping" group (RoleMappingOverridesCard,
    // AdminSettings.tsx) since the Helm-seeded card above renders the same
    // group name as a plain read-only chip.
    await expect(page.getByText("Role mapping overrides")).toBeVisible();
    await expect(page.getByLabel("Admin role mapping").getByText("ops-leads")).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "uMiwd");
  });

  test("R65Xyx: Provenance Badge — Overridden", async ({ page, context }) => {
    // Design PNG is light theme (assets/design-export/screenshots/R65Xyx.png) — see
    // the header comment's note on element-captured component ids.
    await setTheme(page, "light");
    await setAdminConfigVariant(context, "oidc");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    await expect(page.getByText("Overridden in dashboard")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    // Design PNG (assets/design-export/screenshots/R65Xyx.png) is a 408x44 crop of a
    // single ProvenanceBadge, not a full screen — scope the capture to the
    // rendered chip via its data-type hook (ProvenanceBadge.tsx: `data-type={type}`).
    await captureLocator(page, "R65Xyx", page.locator('[data-type="overridden"]').first());
  });

  test("Rwnu3: Provenance Badge — From Helm", async ({ page, context }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await setAdminConfigVariant(context, "oidc");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    await expect(page.getByText("From Helm values").first()).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    // Design PNG is a 308x44 crop of a single ProvenanceBadge — see R65Xyx's note.
    await captureLocator(page, "Rwnu3", page.locator('[data-type="fromHelm"]').first());
  });

  test("XL5ZU: Removable Group Chip — Orange (admin)", async ({ page, context }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await setAdminConfigVariant(context, "oidc");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    // Scoped to the "Admin role mapping" group — see uMiwd's note.
    await expect(
      page.getByLabel("Admin role mapping", { exact: true }).getByText("ops-leads"),
    ).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    // Design PNG is a 252x64 crop of a single RemovableGroupChip (orange
    // variant) — scope via its data-variant hook (RemovableGroupChip.tsx:
    // `data-variant={variant}`), within the admin group so it doesn't match
    // an unrelated orange chip elsewhere on the page. .first() (as with the
    // ProvenanceBadge locators above) is fine here: every chip the admin
    // group renders is an identical orange RemovableGroupChip instance, so
    // which one resolves doesn't change the capture.
    await captureLocator(
      page,
      "XL5ZU",
      page.getByLabel("Admin role mapping", { exact: true }).locator('[data-variant="orange"]').first(),
    );
  });

  test("vStkb: Removable Group Chip — Violet (operator)", async ({ page, context }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await setAdminConfigVariant(context, "oidc");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    // Scoped to the "Operator role mapping" group — see uMiwd's note.
    await expect(
      page.getByLabel("Operator role mapping", { exact: true }).getByText("ops-team"),
    ).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    // Design PNG is a 252x64 crop of a single RemovableGroupChip (violet
    // variant) — see XL5ZU's note (.first() is fine — identical chips).
    await captureLocator(
      page,
      "vStkb",
      page.getByLabel("Operator role mapping", { exact: true }).locator('[data-variant="violet"]').first(),
    );
  });

  test("uw0dB: Removable Group Chip — Secondary (viewer)", async ({ page, context }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await setAdminConfigVariant(context, "oidc");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    // Scoped to the "Viewer role mapping" group — see uMiwd's note.
    await expect(
      page.getByLabel("Viewer role mapping", { exact: true }).getByText("everyone"),
    ).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    // Design PNG is a 252x64 crop of a single RemovableGroupChip (secondary
    // variant) — see XL5ZU's note (.first() is fine — identical chips).
    await captureLocator(
      page,
      "uw0dB",
      page.getByLabel("Viewer role mapping", { exact: true }).locator('[data-variant="secondary"]').first(),
    );
  });

  test("nNGDX: Admin Settings — Authentication (No OIDC mappings)", async ({ page, context }) => {
    // Empty-state variant: Helm-seeded provider exists but declares no
    // role mappings at all, and there's no dashboard override either.
    // See setAdminConfigVariant()'s note on why this is a cookie, not a
    // page.route override.
    await setAdminConfigVariant(context, "oidc-empty-mappings");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    await expect(page.getByText("No OIDC role mappings yet")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "nNGDX");
  });

  test("BV5ei: Provenance Badge — Not configured", async ({ page, context }) => {
    // Same empty-state config as nNGDX — the resulting provenance badges
    // read "Not configured" since there's no Helm mapping and no override.
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await setAdminConfigVariant(context, "oidc-empty-mappings");
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    await expect(page.getByText("Not configured").first()).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    // Design PNG is a 278x44 crop of a single ProvenanceBadge — see R65Xyx's note.
    await captureLocator(page, "BV5ei", page.locator('[data-type="notConfigured"]').first());
  });

  test("QgW58: Admin Settings — Authentication (Save rejected)", async ({ page, context }) => {
    // NOTE: the design frame additionally shows a field-level red-stroked
    // input for the blank group name; the current form has no client-side
    // blank-name validation to trigger that state; this test reproduces
    // the card-level error banner half of the frame (the 400 response
    // surfaced via SaveStatus) by rejecting the section's PUT outright.
    // Selected via a cookie (see setAdminConfigVariant()'s note) rather
    // than page.route, since MSW's Service Worker answers this PUT itself.
    await context.addCookies([
      { name: "e2e_admin_config_put_variant", value: "reject", domain: "localhost", path: "/" },
    ]);
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    await expect(page.getByText("Role mapping overrides")).toBeVisible({ timeout: 10_000 });
    await page.getByRole("button", { name: /save role mappings/i }).click();
    await expect(page.getByText(/must not contain blank group names/i)).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "QgW58");
  });

  test("zqzr4: Admin Settings — Authentication (Admin mapping warning)", async ({ page }) => {
    // NOTE: the design frame (assets/design-export/MANIFEST.md) describes an
    // inline AdminGroupsInlineWarning appearing under this field as soon
    // as an admin group is typed; the current AddProviderForm only warns
    // via ConfirmAdminMappingDialog on submit (see Kp48V) — the inline
    // banner isn't wired up yet. This capture reflects the shipped
    // markup (expanded form with an admin group entered) so the
    // screenshot gate has a real comparison point; the missing inline
    // warning is a content/behavior gap to flag separately, not something
    // this screenshot-only task adds.
    await page.goto("/admin");
    await clickSection(page, "Authentication");
    await page.getByRole("button", { name: /^add provider$/i }).click();
    await expect(page.getByText("Add identity provider")).toBeVisible({ timeout: 10_000 });
    await page.getByLabel("Admin groups").fill("ops-leads");
    await expect(page.getByLabel("Admin groups")).toHaveValue("ops-leads");
    await page.waitForTimeout(200);
    await capture(page, "zqzr4");
  });

  test("Kp48V: Confirm Admin Mapping dialog", async ({ page }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await page.goto("/admin");
    await expect(page.getByRole("heading", { name: /admin settings/i })).toBeVisible({
      timeout: 10_000,
    });
    await clickSection(page, "Authentication");
    await page.getByRole("button", { name: /^add provider$/i }).click();
    await expect(page.getByText("Add identity provider")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel("Name", { exact: true })).toBeVisible({ timeout: 10_000 });
    await page.getByLabel("Name", { exact: true }).fill("corp-sso");
    await page.getByLabel("Issuer URL").fill("https://idp.example.com");
    await page.getByLabel("Client ID").fill("abc123");
    await page.getByLabel("Client secret").fill("supersecret");
    await page.getByLabel("Admin groups").fill("ops-leads");
    await page.getByRole("button", { name: /^add provider$/i }).click();
    await expect(page.getByText("Confirm admin role mapping?")).toBeVisible({ timeout: 10_000 });
    // ConfirmAdminMappingDialog wraps ConfirmDialog, which renders HeroUI's
    // AlertDialog/AlertDialogDialog (role="alertdialog", not "dialog").
    const dialog = page.getByRole("alertdialog", { name: /confirm admin role mapping\?/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await captureLocator(page, "Kp48V", dialog);
  });

  test("RC3Kf: Admin Settings — Backup destinations", async ({ page }) => {
    await page.goto("/admin");
    await clickSection(page, "Backup destinations");
    await expect(page.getByText(/restic repositories/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "RC3Kf");
  });

  test("g5mEpx: Admin Settings — Module sources", async ({ page }) => {
    await page.goto("/admin");
    await clickSection(page, "Module sources");
    await expect(page.getByText("upstream")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "g5mEpx");
  });

  test("Wj0V4: Admin Settings — Mod registries", async ({ page, context }) => {
    await setAdminConfigVariant(context, "registries-curseforge");
    await page.goto("/admin");
    await clickSection(page, "Mod registries");
    await expect(page.getByText("CurseForge")).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "Wj0V4");
  });

  test("n6Xlo: Admin Settings — Notifications", async ({ page }) => {
    await page.goto("/admin");
    await expect(page.getByRole("heading", { name: /admin settings/i })).toBeVisible({
      timeout: 10_000,
    });
    await clickSection(page, "Notifications");
    await expect(page.getByText(/no notification sinks configured/i)).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "n6Xlo");
  });

  test("uoxQW: Admin Settings — Telemetry", async ({ page }) => {
    await page.goto("/admin");
    await clickSection(page, "Telemetry");
    await expect(page.getByText(/send basic usage metrics/i)).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "uoxQW");
  });

  test("M2sA4u: Admin Settings — Updates", async ({ page }) => {
    await page.goto("/admin");
    await clickSection(page, "Updates");
    await expect(page.getByText(/upgraded via helm/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "M2sA4u");
  });

  test("zM0VF: Admin Settings — About", async ({ page }) => {
    await page.goto("/admin");
    await clickSection(page, "About");
    await expect(page.getByText(/agpl-3\.0/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "zM0VF");
  });

  test("bYDHC: Users & RBAC", async ({ page }) => {
    await page.goto("/users");
    await expect(page.getByRole("heading", { name: /users.*rbac/i })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText("operator-01")).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "bYDHC");
  });

  test("e9lV4: Users & RBAC — Roles", async ({ page }) => {
    await page.goto("/users");
    await page.getByRole("tab", { name: /^roles/i }).click();
    await expect(page.getByText(/full access to all resources/i)).toBeVisible({
      timeout: 10_000,
    });
    await page.waitForTimeout(200);
    await capture(page, "e9lV4");
  });

  test("TBvTC: Users & RBAC — Service accounts", async ({ page }) => {
    await page.goto("/users");
    await page.getByRole("tab", { name: /^service accounts$/i }).click();
    await expect(page.getByText(/tracked for v1\.1/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "TBvTC");
  });

  test("Dpb9f: Users & RBAC — Identity providers", async ({ page }) => {
    await page.goto("/users");
    await page.getByRole("tab", { name: /^identity providers$/i }).click();
    await expect(page.getByText(/oidc identity providers/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    await capture(page, "Dpb9f");
  });

  test("CqaSq: Role Editor Modal", async ({ page }) => {
    // Design frame CqaSq is the *edit* form (RoleEditorModal.tsx's
    // `Edit role: ${role.name}` heading), not the blank "New role" create
    // form — open it via the operator role card's Edit button.
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await page.goto("/users");
    await page.getByRole("tab", { name: /^roles/i }).click();
    // Users.tsx's role Card ("flex flex-col gap-2 p-4" — unique to these
    // grid cards) containing the "operator" role-name span.
    const operatorCard = page.locator(".flex.flex-col.gap-2.p-4").filter({
      has: page.getByText("operator", { exact: true }),
    });
    await expect(operatorCard.getByRole("button", { name: /^edit$/i })).toBeVisible({
      timeout: 10_000,
    });
    await operatorCard.getByRole("button", { name: /^edit$/i }).click();
    const dialog = page.getByRole("dialog", { name: /edit role: operator/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog.getByText("Edit role: operator")).toBeVisible();
    await page.waitForTimeout(200);
    // Design PNG (1088x1154) is a crop of just the modal, not the full page.
    await captureLocator(page, "CqaSq", dialog);
  });

  test("NLDDv: Dialog — Invite User", async ({ page }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await page.goto("/users");
    await page.getByRole("button", { name: /invite user/i }).click();
    const dialog = page.getByRole("dialog", { name: /^invite user$/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog.getByText("Invite user")).toBeVisible();
    await page.waitForTimeout(200);
    // Design PNG (1088x922) is a crop of just the modal, not the full page.
    await captureLocator(page, "NLDDv", dialog);
  });

  test("t3IY3u: Dialog — Edit User", async ({ page }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await page.goto("/users");
    await expect(page.getByText("operator-01").first()).toBeVisible({ timeout: 10_000 });
    await page.getByRole("button", { name: /^actions for operator-01$/i }).click();
    await page.getByRole("menuitem", { name: /edit user/i }).click();
    // Named lookup: while the DropdownMenu popover closes it briefly
    // shares role="dialog" with the modal opening underneath it, so an
    // unnamed getByRole("dialog") resolves to both.
    const dialog = page.getByRole("dialog", { name: /edit user/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog.getByText("Edit user")).toBeVisible();
    await page.waitForTimeout(200);
    // Design PNG (1088x782) is a crop of just the modal, not the full page.
    await captureLocator(page, "t3IY3u", dialog);
  });

  test("MaoHP: Dialog — Reset Password", async ({ page }) => {
    // Design PNG is light theme — see the header comment.
    await setTheme(page, "light");
    await page.goto("/users");
    await expect(page.getByText("operator-01").first()).toBeVisible({ timeout: 10_000 });
    await page.getByRole("button", { name: /^actions for operator-01$/i }).click();
    await page.getByRole("menuitem", { name: /reset password/i }).click();
    // Named lookup — see t3IY3u's note on the closing DropdownMenu popover
    // sharing role="dialog" with the modal opening underneath it.
    const dialog = page.getByRole("dialog", { name: /reset password for/i });
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog.getByText(/reset password for/i)).toBeVisible();
    // Design input YgQBa shows the 2px $accent focus stroke, so focus the
    // field to match. captureLocator()'s blur guard (capture.ts) leaves focus
    // inside [role="dialog"] alone, so the field stays focused for the shot.
    await dialog.getByLabel("New password").focus();
    await page.waitForTimeout(200);
    // Design PNG (1088x542) is a crop of just the modal, not the full page.
    await captureLocator(page, "MaoHP", dialog);
  });

  test("DxKOh: Audit Log", async ({ page }) => {
    await page.goto("/admin/audit");
    await expect(page.getByRole("heading", { name: /audit log/i })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText("audit chain verified", { exact: false })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText("admin-demo").first()).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "DxKOh");
  });

  test("m1hP1j: Audit Integrity Banner (failed)", async ({ page, context }) => {
    // Design PNG is light theme (white) — see the header comment.
    await setTheme(page, "light");
    // Cookie-selected, not page.route — see setAdminConfigVariant()'s note;
    // MSW's Service Worker answers /admin/audit/verify itself.
    await context.addCookies([
      { name: "e2e_audit_verify_variant", value: "failed", domain: "localhost", path: "/" },
    ]);
    await page.goto("/admin/audit");
    await expect(page.getByRole("heading", { name: /audit log/i })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText(/chain breaks at event #286/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(200);
    // Bare-banner id: kIxaJ is the 700px caption composition around the
    // banner; the design reference now lives at m1hP1j (see
    // assets/design-export/MANIFEST.md's m1hP1j row). kIxaJ stays exported but is
    // no longer diffed.
    await captureLocator(page, "m1hP1j", page.getByRole("alert"));
  });

  test("Bq2Yg: Admin — System Logs", async ({ page }) => {
    await page.goto("/admin/logs");
    await expect(page.getByRole("heading", { name: /system logs/i })).toBeVisible({
      timeout: 10_000,
    });
    // "API server" is a Tabs.Tab (role="tab"), not a plain button —
    // AdminLogs.tsx COMPONENTS[0].label.
    await expect(page.getByRole("tab", { name: /^api server$/i })).toBeVisible();
    // Mocked plaintext stream from /admin/system-logs/:component
    // (screenshotSystemLogLines).
    await expect(page.getByText(/starting gameplane-api/i)).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(500);
    await capture(page, "Bq2Yg");
  });

  test("j9W8A: Cluster Settings", async ({ page }) => {
    await page.goto("/cluster");
    await expect(page.getByRole("heading", { name: /cluster/i }).first()).toBeVisible({
      timeout: 10_000,
    });
    // Cluster.tsx's <h3>Storage</h3> — a plain getByText("Storage") also
    // matches non-heading wrapper elements whose only visible child is
    // that h3, so scope to the heading role for a unique match.
    await expect(page.getByRole("heading", { name: "Storage" })).toBeVisible();
    await expect(page.getByText("fast-nvme")).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "j9W8A");
  });

  test("dxdEi: Cluster Settings — Default storage class", async ({ page, context }) => {
    await setAdminConfigVariant(context, "empty-storage-class");
    await page.goto("/cluster");
    // See j9W8A's note on scoping to the heading role.
    await expect(page.getByRole("heading", { name: "Storage" })).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText("Cluster default")).toBeVisible();
    await page.waitForTimeout(200);
    await capture(page, "dxdEi");
  });
});
