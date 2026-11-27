import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { makeBackup, makeRestore } from "@/test/factories";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { DOWNLOAD_HANDOFF_MS } from "@/components/backups/BackupDownloadDialog";
import { BackupsTab } from "./Backups";

function renderTab(permissions: string[], props: { cluster?: string; ns?: string } = {}): ReturnType<typeof baseRenderWithQuery> {
  const ui: ReactElement = (
    <ResourceTargetProvider
      target={{ cluster: props.cluster ?? "local", name: "alpha", namespace: props.ns }}
      access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions }}
    >
      <BackupsTab name="alpha" ns={props.ns} />
    </ResourceTargetProvider>
  );
  return baseRenderWithQuery(ui);
}

const READ = ["backups:read", "schedules:read", "destinations:read"];

// Records the links the dialog clicks instead of letting jsdom navigate.
function captureDownloads(): string[] {
  const clicked: string[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push(`${this.getAttribute("href")}|${this.hasAttribute("download")}`);
  });
  return clicked;
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("BackupsTab download action", () => {
  it("downloads a restic backup as .tar.gz by default and shows the preparing state", async () => {
    const clicked = captureDownloads();
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ status: { phase: "Succeeded", snapshotID: "s1", size: "1434Mi" } })] })));
    renderTab(READ);
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Download backup");
    expect(dialog).toHaveTextContent("alpha-2026-05-07 · alpha");
    expect(dialog).toHaveTextContent("This archive contains every server file");
    expect(dialog).toHaveTextContent("configs that may hold passwords");
    expect(dialog).toHaveTextContent("1.4 GB");
    expect(screen.getByRole("radio", { name: /^\.tar\.gz/ })).toBeChecked();
    await userEvent.click(within(dialog).getByRole("button", { name: /^download$/i }));
    expect(clicked).toEqual(["/backups/alpha-2026-05-07:download?format=tar.gz&namespace=gameplane-games|true"]);
    expect(await screen.findByText("Preparing download…")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    await waitFor(() => expect(screen.queryByText("Preparing download…")).not.toBeInTheDocument());
  });

  it("downloads a .zip when chosen, with the cluster and namespace of the server", async () => {
    const clicked = captureDownloads();
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ status: { phase: "Succeeded", snapshotID: "s1" } })] })));
    renderTab(READ, { cluster: "east", ns: "games" });
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    await userEvent.click(await screen.findByRole("radio", { name: /^\.zip/ }));
    expect(screen.getByRole("radio", { name: /^\.zip/ })).toBeChecked();
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: /^download$/i }));
    expect(clicked).toEqual(["/backups/alpha-2026-05-07:download?format=zip&namespace=games&cluster=east|true"]);
  });

  it("shows a dash for the estimated size when the backup has none", async () => {
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ status: { phase: "Succeeded", snapshotID: "s1" } })] })));
    renderTab(READ);
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    expect(await screen.findByText("Estimated size")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toHaveTextContent(/Estimated size\s*—/);
  });

  it("closes the dialog by itself once the handoff window has passed", async () => {
    captureDownloads();
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ status: { phase: "Succeeded", snapshotID: "s1" } })] })));
    renderTab(READ);
    const timers = vi.spyOn(globalThis, "setTimeout");
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: /^download$/i }));
    expect(await screen.findByText("Preparing download…")).toBeInTheDocument();
    const handoff = timers.mock.calls.find(([, delay]) => delay === DOWNLOAD_HANDOFF_MS);
    expect(handoff).toBeDefined();
    act(() => { (handoff?.[0] as () => void)(); });
    await waitFor(() => expect(screen.queryByText("Preparing download…")).not.toBeInTheDocument());
  });

  it("starts over with the default format when the dialog is opened again", async () => {
    captureDownloads();
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ status: { phase: "Succeeded", snapshotID: "s1" } })] })));
    renderTab(READ);
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    await userEvent.click(await screen.findByRole("radio", { name: /^\.zip/ }));
    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: /^download$/i }));
    expect(await screen.findByRole("radio", { name: /^\.tar\.gz/ })).toBeChecked();
  });

  it("offers no download for a volume-snapshot backup and explains why", async () => {
    server.use(http.get("/backups", () => HttpResponse.json({ items: [makeBackup({ spec: { serverRef: { name: "alpha" }, strategy: "volume-snapshot" } })] })));
    renderTab(READ);
    expect(await screen.findByText("alpha-2026-05-07")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^download$/i })).not.toBeInTheDocument();
    expect(screen.getByTitle("Snapshot backups stay inside the storage system.")).toHaveTextContent("—");
  });

  it("offers no download before the backup finished or recorded its snapshot", async () => {
    server.use(http.get("/backups", () => HttpResponse.json({ items: [
      makeBackup({ metadata: { name: "pending-1" }, status: { phase: "Running" } }),
      makeBackup({ metadata: { name: "awaiting-1" }, status: { phase: "Succeeded" } }),
    ] })));
    renderTab(READ);
    expect(await screen.findByText("pending-1")).toBeInTheDocument();
    expect(screen.getByText("awaiting-1")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^download$/i })).not.toBeInTheDocument();
  });

  it("lists an upload restore by its file name", async () => {
    server.use(http.get("/restores", () => HttpResponse.json({ items: [makeRestore({
      metadata: { name: "restore-up-1" },
      spec: { serverRef: { name: "alpha" }, upload: { fileName: "survival.tar.gz", format: "tar.gz", declaredBytes: 10 } },
    })] })));
    renderTab(READ);
    expect(await screen.findByText("survival.tar.gz")).toBeInTheDocument();
  });
});
