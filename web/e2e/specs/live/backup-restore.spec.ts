import { test, expect } from "@playwright/test";
import type { APIRequestContext, Locator, Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import {
  loginIfNeeded,
  seedTemplate,
  seedServer,
  seedBackupDestination,
  cleanupServerBackups,
  apiSend,
} from "./_seed";
import type { Seeded } from "./_seed";
import { ServerDetailPage } from "../../pages/ServerDetailPage";

// Live: backup and restore a real GameServer through the dashboard against the
// shared restic repository. The happy path backs up a running busybox server,
// writes a marker file, restores the backup and asserts the marker is gone.
// The error path deletes the target server while the Restore dialog is open and
// asserts the operator marks the Restore Failed with "target server ... not found".
//
// PREMISE NOTE (destination): the GameServer does not reference the restic Secret.
// The Backup references it through spec.repoRef, and the Backups tab only enables
// "Back up now" when exactly one labelled backup destination exists. The fixture
// Secret e2e-restic-creds is not labelled, so this spec seeds its own destination
// pointing at the same restic repository.
//
// PREMISE NOTE (error path): restoring onto a deleted server shows no toast. POST
// /restores succeeds and the operator reports the failure on the Restore status,
// visible on /backups?tab=restores.
//
// The shared restic repository and its warm-up Job are created by
// TestE2E_ResticRepoReady, which the e2e-web-live CI job runs before Playwright.
// When the warm-up Job has not succeeded, the whole spec is skipped.

const LIVE = process.env.GAMEPLANE_E2E_TARGET === "live";

// Reads the warm-up Job status from the cluster. Returns "" when kubectl or the
// Job is unavailable, which makes the spec skip rather than fail.
function resticWarmupSucceeded(): string {
  const ctxName = `kind-${process.env.GAMEPLANE_E2E_CLUSTER ?? "gameplane-e2e"}`;
  try {
    return execFileSync(
      "kubectl",
      [
        "--context",
        ctxName,
        "get",
        "job",
        "e2e-restic-warmup",
        "-n",
        "gameplane-games",
        "-o",
        "jsonpath={.status.succeeded}",
      ],
      { encoding: "utf8" },
    ).trim();
  } catch {
    return "";
  }
}

const warm = LIVE ? resticWarmupSucceeded() : "";

test.describe.configure({ mode: "serial" });

test.describe("live: backup and restore a running server", () => {
  test.skip(!LIVE, "live-only — needs a real pod, agent sidecar and restic repository");
  test.skip(LIVE && warm !== "1", "restic repo not warmed (run TestE2E_ResticRepoReady first)");

  const stamp = Date.now().toString(36);
  const tmplName = `e2e-pw-br-tmpl-${stamp}`;
  const serverName = `e2e-pw-br-${stamp}`;
  const destName = `e2e-pw-restic-${stamp}`;
  const markerName = `e2e-marker-${stamp}.txt`;

  let cleanups: Array<(request: APIRequestContext) => Promise<void>> = [];

  test.beforeAll(async ({ request }) => {
    const tmpl: Seeded = await seedTemplate(request, tmplName);
    const server: Seeded = await seedServer(request, { name: serverName, template: tmplName });
    const dest: Seeded = await seedBackupDestination(request, destName);
    cleanups = [server.cleanup, dest.cleanup, tmpl.cleanup];

    // The Backups tab only enables "Back up now" for exactly one destination, so a
    // second destination already in the cluster would make the spec silently wrong.
    const res = await request.get("/backup-destinations", { headers: { Accept: "application/json" } });
    if (!res.ok()) {
      throw new Error(`list backup destinations failed: ${res.status()}`);
    }
    const body = (await res.json()) as { items?: unknown[] };
    const count = body.items?.length ?? 0;
    if (count !== 1) {
      throw new Error(`expected exactly 1 backup destination, found ${count}`);
    }
  });

  // afterAll gets its own live `request`; beforeAll's is disposed by now.
  test.afterAll(async ({ request }) => {
    await cleanupServerBackups(request, serverName);
    for (const c of cleanups) await c(request);
  });

  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    await loginIfNeeded(page);
  });

  // Waits for the server header to show a running phase. Returns false when the
  // pod does not reach Running within budget on this runner.
  async function waitForRunning(page: Page): Promise<boolean> {
    try {
      await expect(page.locator("header").getByText(/running/i).first()).toBeVisible({
        timeout: 150_000,
      });
      return true;
    } catch {
      return false;
    }
  }

  // The Backups tab on the server page holds the backup and restore controls.
  function backupsGrid(page: Page): Locator {
    return page.getByRole("grid", { name: "Backups table" });
  }

  test("backs up a running server, then restores it and the marker file is gone", async ({
    page,
  }) => {
    test.setTimeout(480_000);

    const detail = new ServerDetailPage(page);
    await detail.goto(serverName);
    await expect(page.getByRole("heading", { name: serverName })).toBeVisible({ timeout: 30_000 });

    const running = await waitForRunning(page);
    test.skip(!running, "pod did not reach Running within budget on this runner");

    // Back up now: the server must be running and the lone destination must be
    // selected, which makes the button enabled.
    await detail.clickTab("Backups");
    const backUp = page.getByRole("button", { name: "Back up now", exact: true });
    await expect(backUp).toBeEnabled({ timeout: 20_000 });
    await backUp.click();

    // The backup phase text is "Succeeded". The list polls every 5 s.
    await expect(backupsGrid(page).getByRole("row").filter({ hasText: "Succeeded" }).first()).toBeVisible({
      timeout: 240_000,
    });

    // Positive control: create a marker file after the backup was taken.
    await detail.clickTab("Files");
    await page.getByRole("button", { name: "New file", exact: true }).click();
    const newFileDialog = page.getByRole("dialog");
    await newFileDialog.getByPlaceholder("config.yaml").fill(markerName);
    await newFileDialog.getByRole("button", { name: "Create", exact: true }).click();
    await expect(page.locator("aside li").filter({ hasText: markerName })).toBeVisible({
      timeout: 30_000,
    });

    // Restore the Succeeded backup onto this server. The operator suspends the
    // server, runs the restic restore Job and resumes it.
    await detail.clickTab("Backups");
    const succeededRow = backupsGrid(page).getByRole("row").filter({ hasText: "Succeeded" }).first();
    await succeededRow.getByRole("button", { name: "Restore", exact: true }).click();

    const restoreDialog = page.getByRole("dialog");
    await expect(restoreDialog.getByRole("heading", { name: "Restore backup" })).toBeVisible();
    await restoreDialog.getByRole("button", { name: "Restore", exact: true }).click();
    await expect(restoreDialog).toBeHidden({ timeout: 30_000 });

    const recentRestores = page
      .locator("section")
      .filter({ has: page.getByRole("heading", { name: "Recent restores" }) });
    await expect(recentRestores.getByText("Succeeded", { exact: true })).toBeVisible({
      timeout: 240_000,
    });

    // The restore suspends and resumes the pod, so reload and wait for it to run again.
    await page.reload();
    const runningAgain = await waitForRunning(page);
    // The pod was Running before the backup, so not coming back after a
    // successful restore is a regression, not a slow runner: fail, don't skip.
    expect(runningAgain, "pod did not return to Running after restore within budget").toBe(true);

    // The marker file was created after the backup, so the restore must remove it.
    // The file list can error while the agent restarts, so retry the refresh.
    await detail.clickTab("Files");
    await expect(async () => {
      await page.getByRole("button", { name: "Refresh" }).click();
      await expect(page.locator("aside li").filter({ hasText: markerName })).toHaveCount(0, {
        timeout: 5_000,
      });
    }).toPass({ timeout: 60_000 });
  });

  test("restoring onto a server deleted mid-dialog is marked Failed on the Restores tab", async ({
    page,
    request,
  }) => {
    test.setTimeout(360_000);

    // This test needs the Succeeded backup from the happy path. Skip when that
    // backup does not exist, which means the happy path was skipped.
    const backupsRes = await request.get("/backups", { headers: { Accept: "application/json" } });
    const backups = backupsRes.ok()
      ? ((await backupsRes.json()) as {
          items?: Array<{ spec: { serverRef: { name: string } }; status?: { phase?: string } }>;
        })
      : { items: [] };
    const hasSucceeded = (backups.items ?? []).some(
      (b) => b.spec.serverRef.name === serverName && b.status?.phase === "Succeeded",
    );
    test.skip(!hasSucceeded, "no Succeeded backup for this server (happy path did not run)");

    // Open the Restore dialog from the global Backups page and pick the target
    // server explicitly, since the dialog has no default target outside a server tab.
    await page.goto("/backups");
    const backupsTable = page.getByRole("grid", { name: "Backups" });
    const row = backupsTable.getByRole("row").filter({ hasText: serverName }).filter({ hasText: "Succeeded" }).first();
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.getByRole("button", { name: "Restore", exact: true }).click();

    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("heading", { name: "Restore backup" })).toBeVisible();
    await dialog.locator("#target-server").click();
    await page
      .getByRole("listbox", { name: "Select target server" })
      .getByRole("option")
      .filter({ hasText: serverName })
      .click();

    // Delete the target while the dialog is still open. Restoring onto a server
    // that is being deleted reports a different message, so wait until the server
    // is fully gone.
    const del = await apiSend(request, "DELETE", `/servers/${serverName}`);
    expect(del.ok()).toBeTruthy();
    await expect
      .poll(
        async () =>
          (await request.get(`/servers/${serverName}`, { headers: { Accept: "application/json" } })).status(),
        { timeout: 120_000 },
      )
      .toBe(404);

    // POST /restores succeeds even though the target is gone. The operator fails it later.
    await dialog.getByRole("button", { name: "Restore", exact: true }).click();
    await expect(dialog).toBeHidden({ timeout: 30_000 });

    await page.goto("/backups?tab=restores");
    const restoresTable = page.getByRole("grid", { name: "Restores" });
    // The happy path's Succeeded restore targets the same server, so match the Failed row itself.
    const failedRow = restoresTable
      .getByRole("row")
      .filter({ hasText: serverName })
      .filter({ has: page.getByText("Failed", { exact: true }) });
    await expect(failedRow).toBeVisible({ timeout: 90_000 });
    await expect(failedRow.getByText(/target server .* not found/)).toBeVisible({ timeout: 10_000 });
  });
});
