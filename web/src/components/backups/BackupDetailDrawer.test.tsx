import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeBackup } from "@/test/factories";
import { BackupDetailDrawer } from "./BackupDetailDrawer";

describe("BackupDetailDrawer", () => {
  it.each([
    { policy: "legacy/default primary", volumePolicy: undefined },
    { policy: "explicit primary-only", volumePolicy: "primary-only" as const },
  ])("shows captured and excluded claims for $policy independently of copy success", async ({ volumePolicy }) => {
    server.use(http.get("/backups/coverage", () => HttpResponse.json(makeBackup({
      metadata: { name: "coverage" },
      status: {
        phase: "Succeeded", snapshotID: "copy",
        conditions: [{ type: "Consistency", status: "False", reason: "UnsupportedRawCopy", message: "The game does not support quiesce." }],
        captureManifest: { state: "Recorded", volumePolicy, recordedAt: "2026-10-21T12:00:00Z", volumes: [
          { claimName: "game-data", claimUID: "world-uid", mountPaths: ["/world"], included: true, classification: "data", reason: "PrimaryDataClaim" },
          { claimName: "game-extra-data", mountPaths: ["/extra-data"], included: false, classification: "data", reason: "NotCovered" },
          { claimName: "game-extra-install", mountPaths: ["/serverfiles"], included: false, classification: "rebuildable", reason: "NotCovered" },
          { claimName: "game-mods", mountPaths: ["/world/mods"], included: false, classification: "unclassified", reason: "NotCovered" },
        ] },
      },
    }))));
    renderWithQuery(<BackupDetailDrawer permissions={["*"]} name="coverage" onClose={() => {}} onRestore={() => {}} />);
    expect(await screen.findByText("The game does not support quiesce.")).toBeInTheDocument();
    expect(screen.getByText("Included claims are copied at the recorded archive roots. Primary-only backups exclude extra data volumes and separate mod claims. Rebuildable volumes are excluded. Check recovery prerequisites before restarting.")).toBeInTheDocument();
    expect(screen.getByText("game-data")).toBeInTheDocument();
    expect(screen.getByText("game-extra-data")).toBeInTheDocument();
    expect(screen.getByText("/world/mods")).toBeInTheDocument();
    expect(screen.getAllByText("Not covered")).toHaveLength(3);
    expect(screen.getByText("Rebuildable")).toBeInTheDocument();
  });

  it("shows named archive roots and the saved prerequisites for missing and intact excluded claims", async () => {
    server.use(http.get("/backups/recovery", () => HttpResponse.json(makeBackup({
      metadata: { name: "recovery" },
      status: { phase: "Succeeded", snapshotID: "snapshot", captureManifest: {
        state: "Recorded", formatVersion: 2, volumePolicy: "all-data", volumes: [
          { claimName: "world", claimUID: "world-uid", logicalName: "primary", snapshotRoot: "/volumes/primary", included: true, classification: "data", reason: "IncludedData" },
          { claimName: "missing-install", included: false, classification: "rebuildable", reason: "RebuildRequired", recoveryPrerequisite: "Missing install files must be reconstructed from the module." },
          { claimName: "intact-cache", claimUID: "cache-uid", included: false, classification: "rebuildable", reason: "RebuildRequired", recoveryPrerequisite: "Keep compatible existing cache content; reconstruct only if needed." },
        ],
      } },
    }))));
    renderWithQuery(<BackupDetailDrawer permissions={["*"]} name="recovery" onClose={() => {}} onRestore={() => {}} />);
    expect(await screen.findByText("/volumes/primary")).toBeInTheDocument();
    expect(screen.getByText("Missing install files must be reconstructed from the module.")).toBeInTheDocument();
    expect(screen.getByText("Keep compatible existing cache content; reconstruct only if needed.")).toBeInTheDocument();
    expect(screen.getAllByText("Not covered")).toHaveLength(2);
    expect(screen.queryByText(/must be reconstructed before recovery can run/)).not.toBeInTheDocument();
  });

  it("shows legacy coverage as unknown without inventing a current layout", async () => {
    server.use(http.get("/backups/legacy", () => HttpResponse.json(makeBackup({ metadata: { name: "legacy" }, status: { phase: "Succeeded" } }))));
    renderWithQuery(<BackupDetailDrawer permissions={["*"]} name="legacy" onClose={() => {}} onRestore={() => {}} />);
    expect(await screen.findByText(/Capture-time volume metadata is unavailable/)).toBeInTheDocument();
    expect(screen.queryByText("Included")).not.toBeInTheDocument();
  });

  it("does not render content when name is null", () => {
    renderWithQuery(<BackupDetailDrawer permissions={["*"]} name={null} onClose={() => {}} onRestore={() => {}} />);
    expect(screen.queryByText(/Backup details/i)).not.toBeInTheDocument();
  });

  it("dialog has accessible name when open", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(makeBackup({ metadata: { name: "alpha-1" } })),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={() => {}} />,
    );
    await screen.findByText(/Backup details/i);
    expect(screen.getByRole("dialog", { name: /backup details/i })).toBeInTheDocument();
  });

  it("renders details when open", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(
          makeBackup({
            metadata: { name: "alpha-1" },
            status: {
              phase: "Succeeded",
              snapshotID: "abc123",
              completionTime: "2026-05-07T03:00:00Z",
              size: "120 MiB",
            },
          }),
        ),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={() => {}} />,
    );
    expect(await screen.findByText(/Backup details/i)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("Succeeded")).toBeInTheDocument());
  });

  it("Close button calls onClose", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(makeBackup({ metadata: { name: "alpha-1" } })),
      ),
    );
    const onClose = vi.fn();
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={onClose} onRestore={() => {}} />,
    );
    await screen.findByText(/Backup details/i);
    await userEvent.click(screen.getByRole("button", { name: /Dismiss/i }));
    expect(onClose).toHaveBeenCalled();
  });

  it("renders an error banner on fetch failure", async () => {
    server.use(
      http.get("/backups/x", () => HttpResponse.text("not found", { status: 404 })),
    );
    renderWithQuery(<BackupDetailDrawer permissions={["*"]} name="x" onClose={() => {}} onRestore={() => {}} />);
    await screen.findByText(/Backup details/i);
    await waitFor(() => expect(screen.getByText(/not found/)).toBeInTheDocument());
  });

  it("calls onRestore when Restore button clicked", async () => {
    const onRestore = vi.fn();
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(
          makeBackup({
            metadata: { name: "alpha-1" },
            status: {
              phase: "Succeeded",
              snapshotID: "abc123",
            },
          }),
        ),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={onRestore} />,
    );
    const restoreBtn = await screen.findByRole("button", { name: /^Restore$/i });
    await userEvent.click(restoreBtn);
    await waitFor(() => {
      expect(onRestore).toHaveBeenCalled();
    });
  });

  it("disables Restore button when backup is not Succeeded or missing snapshotID", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(
          makeBackup({
            metadata: { name: "alpha-1" },
            status: {
              phase: "Running",
              snapshotID: "abc123",
            },
          }),
        ),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={() => {}} />,
    );
    const restoreBtn = await screen.findByRole("button", { name: /^Restore$/i });
    expect(restoreBtn).toBeDisabled();
  });

  it("disables Restore button when snapshotID is missing", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(
          makeBackup({
            metadata: { name: "alpha-1" },
            status: {
              phase: "Succeeded",
            },
          }),
        ),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={() => {}} />,
    );
    const restoreBtn = await screen.findByRole("button", { name: /^Restore$/i });
    expect(restoreBtn).toBeDisabled();
  });

  it("deletes a backup", async () => {
    const deleteHandler = vi.fn(() =>
      HttpResponse.json({})
    );
    const onClose = vi.fn();
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(makeBackup({ metadata: { name: "alpha-1" } })),
      ),
      http.delete("/backups/alpha-1", deleteHandler),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={onClose} onRestore={() => {}} />,
    );
    await screen.findByText(/Backup details/i);
    const deleteBtn = screen.getByRole("button", { name: /Delete/i });
    await userEvent.click(deleteBtn);
    await waitFor(() => {
      expect(deleteHandler).toHaveBeenCalled();
      expect(onClose).toHaveBeenCalled();
    });
  });

  it("shows error when delete fails", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(makeBackup({ metadata: { name: "alpha-1" } })),
      ),
      http.delete("/backups/alpha-1", () =>
        HttpResponse.text("delete failed", { status: 500 }),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={() => {}} />,
    );
    await screen.findByText(/Backup details/i);
    const deleteBtn = screen.getByRole("button", { name: /Delete/i });
    await userEvent.click(deleteBtn);
    await waitFor(() => {
      expect(screen.getByText(/delete failed/)).toBeInTheDocument();
    });
  });

  it("renders backup timestamps", async () => {
    server.use(
      http.get("/backups/alpha-1", () =>
        HttpResponse.json(
          makeBackup({
            metadata: { name: "alpha-1" },
            status: {
              phase: "Succeeded",
              startTime: "2026-05-07T01:00:00Z",
              completionTime: "2026-05-07T03:00:00Z",
            },
          }),
        ),
      ),
    );
    renderWithQuery(
      <BackupDetailDrawer permissions={["*"]} name="alpha-1" onClose={() => {}} onRestore={() => {}} />,
    );
    await screen.findByText(/Backup details/i);
    await waitFor(() => {
      expect(screen.getByText(/2026-05-07T01:00:00Z/)).toBeInTheDocument();
      expect(screen.getByText(/2026-05-07T03:00:00Z/)).toBeInTheDocument();
    });
  });
});
