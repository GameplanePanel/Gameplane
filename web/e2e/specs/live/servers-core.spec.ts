import { test, expect } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";
import { loginIfNeeded, seedServer, seedTemplate } from "./_seed";
import { ServersPage } from "../../pages/ServersPage";
import { ServerDetailPage } from "../../pages/ServerDetailPage";

// T085 (specs/done_014-heroui-web-rebuild/tasks.md): live Playwright coverage for
// the Slice 2a flows (Servers list + Server Detail core tabs) against a real
// cluster — this is the feature's E2E tier per OD-3 (Settled 2026-09-03); no
// corresponding test/e2e/ Go test is added for Slice 2a.
//
// Mirrors login-and-shell.spec.ts's structure: one describe block, storageState
// pre-authenticates every test (globalSetup), and loginIfNeeded is called only
// as the documented no-op safety net (page.goto then waitForLoadState, THEN
// loginIfNeeded) — never a second explicit login. One seeded template + one
// seeded GameServer covers every flow below; nothing here spends more than
// that one login budget-wise.

test.describe("live: servers core (list + detail)", () => {
  test.skip(
    process.env.GAMEPLANE_E2E_TARGET !== "live",
    "live-only — the mock specs (serverDetail.spec.ts, servers *.spec.ts) cover these flows against MSW",
  );

  const stamp = Date.now().toString(36);
  const tmplName = `e2e-pw-core-tmpl-${stamp}`;
  const serverName = `e2e-pw-core-${stamp}`;
  let cleanups: Array<(request: APIRequestContext) => Promise<void>> = [];

  test.beforeAll(async ({ request }) => {
    const tmpl = await seedTemplate(request, tmplName);
    const server = await seedServer(request, {
      name: serverName,
      template: tmplName,
      description: "Live servers-core probe",
    });
    // Delete the server before the template it references.
    cleanups = [server.cleanup, tmpl.cleanup];
  });

  // afterAll gets its own live `request` — beforeAll's is already disposed.
  test.afterAll(async ({ request }) => {
    for (const c of cleanups) await c(request);
  });

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
  });

  test("servers list: phase/template/namespace filters narrow the seeded server", async ({
    page,
  }) => {
    const servers = new ServersPage(page);
    await servers.goto();
    await expect(page.getByRole("heading", { name: /^servers$/i })).toBeVisible();

    const row = servers.getServerRow(serverName);
    // The list polls every 5s — give the seeded CR time to appear.
    await expect(row).toBeVisible({ timeout: 15_000 });

    // Phase filter (status Tabs: All/Running/Stopped): a freshly-created
    // GameServer with no running pod is neither Running nor Stopped
    // (Pending/Starting), so the "Running" tab hides it and "All" restores it.
    await servers.clickStatusTab("Running");
    await expect(row).toHaveCount(0);
    await servers.clickStatusTab("All");
    await expect(row).toBeVisible({ timeout: 10_000 });

    // Template + namespace facet filters (FilterPopover).
    await servers.filterButton.click();
    const gameCheckbox = page.getByRole("checkbox", { name: tmplName });
    const nsCheckbox = page.getByRole("checkbox", { name: "gameplane-games" });
    await expect(gameCheckbox).toBeVisible({ timeout: 10_000 });
    // HeroUI's Checkbox renders its own <label> over the (visually tiny)
    // native input, which Playwright's actionability check treats as the
    // input being covered — force through it, same as a real click on the
    // label does natively (clicking a label toggles its associated input).
    await gameCheckbox.check({ force: true });
    await expect(nsCheckbox).toBeVisible();
    await nsCheckbox.check({ force: true });
    // Apply/Clear's accessible name is just their visible text.
    await page.getByRole("button", { name: /^apply$/i }).click();
    // Both facets include the seeded server's own template/namespace, so it
    // stays visible with the filter applied.
    await expect(row).toBeVisible({ timeout: 10_000 });

    // Reopen the facet filter, whose label includes the applied count.
    // The separate "Filter by cluster" control must not match this locator.
    await page.getByRole("button", { name: /^filter(?:\s+\d+)?$/i }).click();
    await page.getByRole("button", { name: /^clear$/i }).click();
    await page.getByRole("button", { name: /^apply$/i }).click();
    await expect(row).toBeVisible({ timeout: 10_000 });
  });

  test("servers list: row actions open the server actions menu and one lifecycle action fires", async ({
    page,
  }) => {
    const servers = new ServersPage(page);
    await servers.goto();
    const row = servers.getServerRow(serverName);
    await expect(row).toBeVisible({ timeout: 15_000 });

    // Lifecycle action: Stop is enabled for a Pending server (it is only
    // disabled for an already-Stopped/Suspended one) — exercise it directly
    // from the row rather than opening Detail, mirroring the "one server
    // action" scope of this task.
    const stopButton = row.getByRole("button", { name: /^stop$/i });
    await expect(stopButton).toBeVisible();
    await stopButton.click();

    // Row actions menu: Clone / Transfer / Wipe / Delete.
    const menuTrigger = row.getByRole("button", { name: /server actions/i });
    await menuTrigger.click();
    await expect(page.getByRole("menuitem", { name: /clone server/i })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: /transfer ownership/i })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: /wipe world data/i })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: /delete server/i })).toBeVisible();
    // Close the menu without acting (Escape) — the full dialog open/cancel
    // flows are covered from Server Detail below.
    await page.keyboard.press("Escape");
  });

  test("server detail: all six core tabs render", async ({ page }) => {
    const detail = new ServerDetailPage(page);
    await detail.goto(serverName);
    await expect(page.getByRole("heading", { name: serverName })).toBeVisible({ timeout: 20_000 });

    // Not necessarily the landing tab: ServerDetail switches a provisioning
    // server (Pending/Starting, never started — exactly the seeded one) to
    // Logs the first time the GameServer query resolves. Select Overview
    // explicitly, retrying so a click that lands before that switch isn't
    // silently undone.
    await expect(async () => {
      await detail.clickTab("Overview");
      await expect(page.getByText("Connection")).toBeVisible({ timeout: 2_000 });
    }).toPass({ timeout: 20_000 });

    await detail.clickTab("Events");
    await expect(detail.tablist.getByRole("tab", { name: /^events$/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    await detail.clickTab("Console");
    await expect(detail.tablist.getByRole("tab", { name: /^console$/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    await detail.clickTab("Logs");
    await expect(detail.tablist.getByRole("tab", { name: /^logs$/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    await detail.clickTab("Files");
    await expect(detail.tablist.getByRole("tab", { name: /^files$/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    // No live agent sidecar is running (no seeded pod) — Files.tsx still
    // renders its tree pane's root breadcrumb without crashing.
    await expect(page.getByRole("button", { name: /new folder|refresh/i }).first()).toBeVisible({
      timeout: 10_000,
    });

    await detail.clickTab("Players");
    await expect(detail.tablist.getByRole("tab", { name: /^players$/i })).toHaveAttribute("aria-selected", "true");
    // No agent sidecar is running (no seeded pod), so the players snapshot
    // query never resolves and the header stays "Loading…". The "Online"
    // StatCard label is rendered unconditionally (value "—" until data arrives).
    await expect(page.getByText("Online", { exact: true }).first()).toBeVisible({ timeout: 10_000 });
  });

  test("server detail: clone/transfer/wipe/delete dialogs open and cancel without mutating the server", async ({
    page,
  }) => {
    const detail = new ServerDetailPage(page);
    await detail.goto(serverName);
    await expect(page.getByRole("heading", { name: serverName })).toBeVisible({ timeout: 20_000 });

    // The header's own actions menu (not a row menu) hosts the same four
    // dialogs on Server Detail.
    //
    // Root cause found via a captured CI trace (five prior fix attempts —
    // dialog toBeHidden, backdrop count 0, backdrop opacity 0 via
    // waitForFunction — all hung identically; see PR #351 review notes):
    // HeroUI v3's `ModalBackdrop`/`ModalContainer` (and the equivalent
    // `AlertDialogBackdrop`/`AlertDialogContainer` used by the wipe/delete
    // AlertDialogs) are meant to be composed with the container NESTED
    // inside the backdrop, not as siblings under `<Modal>` — react-aria-
    // components' underlying `ModalOverlay` (what `ModalBackdrop` wraps)
    // tracks its OWN exit animation AND a *second* one on an internal
    // `modalRef` that only gets attached when the modal content
    // (`ModalContainer`/`AlertDialogContainer`) is rendered as a descendant,
    // via `InternalModalContext`
    // (react-aria-components/dist/private/Modal.mjs,
    // `ModalOverlayWithForwardRef`: `isExiting = isOverlayExiting ||
    // isModalExiting`). Every one of this app's six dialog components used
    // to compose them as siblings instead, so that `modalRef` was never
    // attached to anything and `useAnimation`'s `if (isActive &&
    // ref.current)` guard (react-aria/dist/private/utils/animation.mjs)
    // never fired for it — `isModalExiting` latched `true` the very first
    // time any dialog closed and never reset, permanently disabling the
    // backdrop's "should I unmount" check for the rest of that component
    // instance's life. Fixed by nesting `ModalContainer`/
    // `AlertDialogContainer` inside `ModalBackdrop`/`AlertDialogBackdrop` in
    // all six components (matches the HeroUI type composition:
    // `ModalContainerProps`/`AlertDialogContainerProps` wrap
    // `react-aria-components/Modal`'s `Modal`, and `ModalBackdrop`/
    // `AlertDialogBackdrop` wrap its `ModalOverlay`). With the fix, the
    // backdrop element (`[data-slot="modal-backdrop"]` /
    // `[data-slot="alert-dialog-backdrop"]`) actually unmounts once its
    // exit animation completes, so all four dialogs can be exercised on a
    // single page load without a stuck backdrop blocking the next trigger
    // click.
    const openMenuAndDialog = async (itemName: RegExp, dialogHeading: RegExp, role: "dialog" | "alertdialog" = "dialog") => {
      await page.getByRole("button", { name: /server actions/i }).click();
      await page.getByRole("menuitem", { name: itemName }).click();
      // The dropdown's own popover is role="dialog" too and can still be
      // mid-exit-animation (data-exiting) when the modal opens, so a bare
      // getByRole("dialog") is a strict-mode violation — scope by the
      // modal's own accessible name (its heading) to pick the right one.
      const dialog = page.getByRole(role, { name: dialogHeading });
      await expect(dialog).toBeVisible({ timeout: 10_000 });
      await expect(dialog.getByRole("heading", { name: dialogHeading })).toBeVisible();
      await dialog.getByRole("button", { name: /^cancel$/i }).click();
      await expect(dialog).toBeHidden({ timeout: 5_000 });

      // The backdrop must actually unmount once its exit animation
      // completes — this is the assertion that proves the nested
      // composition fixed the exit-state latch (see the note above). It
      // must hold before the next dialog is opened, or a stuck backdrop
      // from this dialog would block the next trigger click.
      await expect(
        page.locator('[data-slot="modal-backdrop"], [data-slot="alert-dialog-backdrop"], [data-slot="drawer-backdrop"]'),
      ).toHaveCount(0);

      // The server must still exist — this dialog was cancelled, not
      // confirmed.
      await expect(page.getByRole("heading", { name: serverName })).toBeVisible();
    };

    await openMenuAndDialog(/clone server/i, /^clone server$/i);
    await openMenuAndDialog(/transfer ownership/i, new RegExp(`transfer ${serverName}`, "i"));
    await openMenuAndDialog(/wipe world data/i, /^wipe world\?$/i, "alertdialog");
    // Delete's confirm dialog title is "Delete <name>?" (ConfirmDialog).
    await openMenuAndDialog(/delete server/i, new RegExp(`delete ${serverName}\\?`, "i"), "alertdialog");
  });
});
