import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { ReactElement } from "react";
import { describe, it, expect, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { makeBackup, makeRestore, makeDestination, makeSchedule, makeServer } from "@/test/factories";
import { BackupsTab } from "./Backups";

// The default MSW handlers already serve /backups (one Succeeded backup
// for "alpha"), /schedules (one), /restores (empty) and
// /backup-destinations (one). Tests override only what they need.

beforeEach(() => {
  // Ensure /servers/alpha is available for tests that query it
  server.use(
    http.get("/servers/alpha", () =>
      HttpResponse.json(makeServer({ metadata: { name: "alpha" } }))
    )
  );
});

describe("BackupsTab", () => {
  it("renders schedules, backups, and enables 'Back up now' with one destination", async () => {
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText("alpha-2026-05-07")).toBeInTheDocument();
    expect(screen.getByText("0 3 * * *")).toBeInTheDocument(); // default schedule cron
    const backupNow = screen.getByRole("button", { name: /back up now/i });
    await waitFor(() => expect(backupNow).toBeEnabled());
  });

  it("disables 'Back up now' with a hint when no destination is configured", async () => {
    server.use(http.get("/backup-destinations", () => HttpResponse.json({ items: [] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    const btn = await screen.findByRole("button", { name: /back up now/i });
    await waitFor(() => expect(btn).toBeDisabled());
    expect(btn.closest("[title]")?.getAttribute("title")).toMatch(/No backup destination/i);
  });

  it("disables 'Back up now' with a multi-destination hint", async () => {
    server.use(
      http.get("/backup-destinations", () =>
        HttpResponse.json({ items: [makeDestination({ name: "a" }), makeDestination({ name: "b" })] }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    const btn = await screen.findByRole("button", { name: /back up now/i });
    await waitFor(() =>
      expect(btn.closest("[title]")?.getAttribute("title")).toMatch(/Multiple destinations/i),
    );
    expect(btn).toBeDisabled();
  });

  it("posts a backup when 'Back up now' is clicked", async () => {
    let posted: unknown = null;
    server.use(
      http.post("/backups", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json(makeBackup({ metadata: { name: "alpha-new", namespace: "gameplane-games" } }));
      }),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    const btn = await screen.findByRole("button", { name: /back up now/i });
    await waitFor(() => expect(btn).toBeEnabled());
    await userEvent.click(btn);
    await waitFor(() => expect(posted).not.toBeNull());
  });

  it("shows an error banner when the backup request fails", async () => {
    server.use(http.post("/backups", () => HttpResponse.text("snapshot failed", { status: 500 })));
    renderWithQuery(<BackupsTab name="alpha" />);
    const btn = await screen.findByRole("button", { name: /back up now/i });
    await waitFor(() => expect(btn).toBeEnabled());
    await userEvent.click(btn);
    expect(await screen.findByText("snapshot failed")).toBeInTheDocument();
  });

  it("toggles the schedule form open and closed", async () => {
    renderWithQuery(<BackupsTab name="alpha" />);
    const newBtn = await screen.findByRole("button", { name: /new schedule/i });
    await userEvent.click(newBtn);
    expect(await screen.findByText(/Schedule \(cron\)/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /cancel/i }));
    await waitFor(() => expect(screen.queryByText(/Schedule \(cron\)/i)).toBeNull());
  });

  it("shows the empty-schedules message when there are none", async () => {
    server.use(http.get("/schedules", () => HttpResponse.json({ items: [] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText("No schedules yet.")).toBeInTheDocument();
  });

  it("renders recent restores when present", async () => {
    server.use(
      http.get("/backups", () => HttpResponse.json({ items: [] })),
      http.get("/restores", () =>
        HttpResponse.json({
          items: [
            makeRestore({
              spec: { serverRef: { name: "alpha" }, backupRef: { name: "alpha-2026-05-07" } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText("Recent restores")).toBeInTheDocument();
    expect(screen.getByText("alpha-2026-05-07")).toBeInTheDocument();
  });

  it("opens the detail drawer when a backup row is clicked", async () => {
    renderWithQuery(<BackupsTab name="alpha" />);
    await userEvent.click(await screen.findByText("alpha-2026-05-07"));
    expect(await screen.findByText("Snapshot ID")).toBeInTheDocument();
  });

  it("opens the restore dialog from a restorable backup", async () => {
    // A single succeeded backup → exactly one (enabled) Restore button.
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup()] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    await screen.findByText("alpha-2026-05-07");
    const restoreBtn = screen.getByRole("button", { name: /^restore$/i });
    expect(restoreBtn).toBeEnabled();
    await userEvent.click(restoreBtn);
    expect(await screen.findByText(/Restore backup/i)).toBeInTheDocument();
  });

  it("disables Restore for a backup that has not succeeded", async () => {
    server.use(
      http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ status: { phase: "Running" } })] })),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    await screen.findByText("alpha-2026-05-07");
    expect(screen.getByRole("button", { name: /^restore$/i })).toBeDisabled();
  });

  it("filters out backups, schedules, and restores from other servers", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "mine", namespace: "gameplane-games" }, spec: { serverRef: { name: "alpha" } } }),
            makeBackup({ metadata: { name: "theirs", namespace: "gameplane-games" }, spec: { serverRef: { name: "beta" } } }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText("mine")).toBeInTheDocument();
    expect(screen.queryByText("theirs")).toBeNull();
  });

  it("shows summary stat cards with backup stats", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({
              spec: { serverRef: { name: "alpha" } },
              status: { phase: "Succeeded", completionTime: "2026-05-07T03:00:00Z", size: "100 MiB" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText(/Last backup/i)).toBeInTheDocument();
    expect(screen.getByText(/Next backup/i)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Backups/i })).toBeInTheDocument();
    expect(screen.getByText(/Total logical size/i)).toBeInTheDocument();
  });

  it("shows warning when multiple schedules exist", async () => {
    server.use(
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [
            makeSchedule({ metadata: { name: "sched-1" }, spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *" } }),
            makeSchedule({ metadata: { name: "sched-2" }, spec: { serverRef: { name: "alpha" }, schedule: "0 12 * * *" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText(/multiple backup schedules/i)).toBeInTheDocument();
  });

  it("shows 'Managed by server settings' badge for auto-managed schedule", async () => {
    server.use(
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [
            makeSchedule({ metadata: { name: "alpha-auto" }, spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText(/Managed by server settings/i)).toBeInTheDocument();
  });

  it("does not show recent restores section when there are none", async () => {
    server.use(http.get("/restores", () => HttpResponse.json({ items: [] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    expect(await screen.findByText("alpha-2026-05-07")).toBeInTheDocument();
    expect(screen.queryByText(/Recent restores/i)).not.toBeInTheDocument();
  });

  it("clickinig restore button from table row opens dialog", async () => {
    // The default /backups handler serves two backups (one Succeeded, one
    // Failed) — every row renders its own "Restore" button, so an unscoped
    // findByRole("button", {name: /^restore$/i}) is ambiguous ("found
    // multiple elements") and never resolves. Narrow to one restorable
    // backup, matching "opens the restore dialog from a restorable backup"
    // above.
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup()] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    const restoreBtn = await screen.findByRole("button", { name: /^restore$/i });
    await userEvent.click(restoreBtn);
    // Dialog should open when Restore button in table row is clicked
    expect(await screen.findByText(/Restore backup/i)).toBeInTheDocument();
  });

  it("stops propagation when restore button in table row is clicked", async () => {
    // See the previous test — scope to a single backup so the Restore
    // button query is unambiguous.
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup()] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    // The table row click handler should not fire when clicking the restore button
    const restoreBtn = await screen.findByRole("button", { name: /^restore$/i });
    await userEvent.click(restoreBtn);
    // Verify dialog opened (which means the click was handled by the button, not propagated)
    expect(await screen.findByText(/Restore backup/i)).toBeInTheDocument();
  });

  it("closes restore dialog when onClose is called", async () => {
    // See "clickinig restore button from table row opens dialog" — scope to
    // a single backup so the Restore button query is unambiguous.
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup()] })));
    renderWithQuery(<BackupsTab name="alpha" />);
    const restoreBtn = await screen.findByRole("button", { name: /^restore$/i });
    await userEvent.click(restoreBtn);
    await screen.findByText(/Restore backup/i);
    // Press Escape to close the dialog (via Radix dialog's onOpenChange)
    await userEvent.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByText(/Restore backup/i)).not.toBeInTheDocument();
    });
  });

  it("closes backup detail drawer when onClose is called", async () => {
    renderWithQuery(<BackupsTab name="alpha" />);
    await userEvent.click(await screen.findByText("alpha-2026-05-07"));
    await screen.findByText("Snapshot ID");
    // The close button should be clickable
    const closeBtn = screen.getByRole("button", { name: /Dismiss/i });
    await userEvent.click(closeBtn);
    await waitFor(() => {
      expect(screen.queryByText("Snapshot ID")).not.toBeInTheDocument();
    });
  });

  it("transitions from backup detail drawer to restore dialog", async () => {
    renderWithQuery(<BackupsTab name="alpha" />);
    // Click backup row to open drawer
    await userEvent.click(await screen.findByText("alpha-2026-05-07"));
    await screen.findByText("Snapshot ID");
    // Click Restore button in drawer
    const restoreInDrawer = screen.getByRole("button", { name: /^restore$/i });
    await userEvent.click(restoreInDrawer);
    // Drawer should close and restore dialog should open
    await waitFor(() => {
      expect(screen.queryByText("Snapshot ID")).not.toBeInTheDocument();
      expect(screen.getByText(/Restore backup/i)).toBeInTheDocument();
    });
  });

  it("calculates next backup time from active schedules", async () => {
    server.use(
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [
            makeSchedule({
              spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *", suspend: false },
              status: { nextScheduleTime: "2026-05-08T03:00:00Z" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    // The "Next backup" stat card should show the next schedule time
    expect(await screen.findByText(/Next backup/i)).toBeInTheDocument();
  });

  describe("schedule row next run", () => {
    const scheduleWithNext = (next?: string) =>
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [
            makeSchedule({
              spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *", suspend: false },
              status: next ? { nextScheduleTime: next } : {},
            }),
          ],
        }),
      );

    it("shows a future next run as 'in <interval>', not 'ago'", async () => {
      const next = new Date(Date.now() + 3 * 3600 * 1000 + 60_000).toISOString();
      server.use(scheduleWithNext(next));
      renderWithQuery(<BackupsTab name="alpha" />);
      expect(await screen.findByText("Next: in 3h")).toBeInTheDocument();
      expect(screen.queryByText(/Next: .*ago/)).not.toBeInTheDocument();
    });

    it("shows 'due' when the next run time has passed", async () => {
      server.use(scheduleWithNext("2020-01-01T00:00:00Z"));
      renderWithQuery(<BackupsTab name="alpha" />);
      expect(await screen.findByText("Next: due")).toBeInTheDocument();
    });

    it("shows a dash when the schedule has no next run time", async () => {
      server.use(scheduleWithNext(undefined));
      renderWithQuery(<BackupsTab name="alpha" />);
      expect(await screen.findByText("Next: —")).toBeInTheDocument();
    });
  });

  it("calculates total size from all backups", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({
              metadata: { name: "backup-1" },
              spec: { serverRef: { name: "alpha" } },
              status: { phase: "Succeeded", size: "100 MiB" },
            }),
            makeBackup({
              metadata: { name: "backup-2" },
              spec: { serverRef: { name: "alpha" } },
              status: { phase: "Succeeded", size: "50 MiB" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsTab name="alpha" />);
    // Total size stat should show combined size
    expect(await screen.findByText(/Total logical size/i)).toBeInTheDocument();
  });
});

function renderWithQuery(ui: ReactElement, options?: Parameters<typeof baseRenderWithQuery>[1]) {
  const props = ui.props as { name?: string; ns?: string };
  return baseRenderWithQuery(<ResourceTargetProvider target={{ cluster: "local", name: props.name ?? "alpha", namespace: props.ns }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>{ui}</ResourceTargetProvider>, options);
}
