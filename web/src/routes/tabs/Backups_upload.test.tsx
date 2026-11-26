import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { FakeXHR } from "@/test/fakeXHR";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { makeRestore, makeServer } from "@/test/factories";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { Restore } from "@/types";
import { BackupsTab } from "./Backups";

const ALL = ["backups:read", "backups:restore", "schedules:read", "destinations:read"];
const user = () => userEvent.setup({ applyAccept: false });

function renderTab(permissions: string[] = ALL) {
  const ui: ReactElement = (
    <ResourceTargetProvider
      target={{ cluster: "local", name: "alpha", namespace: "games" }}
      access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions }}
    >
      <BackupsTab name="alpha" ns="games" />
    </ResourceTargetProvider>
  );
  return baseRenderWithQuery(ui);
}

// The restore the polled GET returns; tests replace it to move the flow along.
let current: Restore;
let confirmed: unknown;

function uploadRestoreOf(over: { phase?: NonNullable<Restore["status"]>["phase"]; status?: Restore["status"]; spec?: Partial<Restore["spec"]>; generation?: number | null } = {}): Restore {
  const base = makeRestore({
    metadata: { name: "restore-up-1", namespace: "games", ...(over.generation === null ? {} : { generation: over.generation ?? 4 }) },
    spec: { serverRef: { name: "alpha" }, upload: { fileName: "survival.tar.gz", format: "tar.gz", declaredBytes: 10 }, ...(over.spec ?? {}) },
    status: { phase: over.phase ?? "Receiving", ...(over.status ?? {}) },
  });
  return base;
}

const gameplaneReview = uploadRestoreOf({
  phase: "AwaitingConfirmation",
  spec: { serverRef: { name: "mc-survival" }, upload: { fileName: "survival.zip", format: "zip", declaredBytes: 10 } },
  status: {
    phase: "AwaitingConfirmation",
    upload: {
      kind: "gameplane",
      manifest: { serverName: "mc-survival", templateName: "minecraft-vanilla", gameVersion: "1.20.6", capturedAt: "2026-11-25T03:00:00Z", volumes: ["data", "mods"] },
      warnings: ["archive was made with game version 1.20.6, older than this server's 1.21.4"],
    },
  },
});

beforeEach(() => {
  FakeXHR.instances = [];
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
  current = uploadRestoreOf();
  confirmed = undefined;
  server.use(
    http.get("/servers/alpha", () => HttpResponse.json(makeServer({ metadata: { name: "alpha" } }))),
    http.get("/restores/restore-up-1", () => HttpResponse.json(current)),
    http.post(/\/restores\/[^/]+:confirm(\?.*)?$/, async ({ request }) => {
      confirmed = await request.json();
      return new HttpResponse(null, { status: 204 });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function openDialog(u = user()) {
  await u.click(await screen.findByRole("button", { name: /^upload backup$/i }));
  return { u, dialog: await screen.findByRole("dialog") };
}

async function pick(u: ReturnType<typeof user>, name: string, size = 10) {
  await u.upload(screen.getByTestId("backup-archive-input"), new File([new Uint8Array(size)], name));
}

// Walks the first two steps and answers the upload so the restore is polled.
async function sendFile(name = "survival.tar.gz") {
  const { u, dialog } = await openDialog();
  await pick(u, name);
  await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
  const xhr = FakeXHR.instances[0];
  await act(async () => { xhr.finish(202, JSON.stringify({ restore: "restore-up-1" })); });
  return { u, dialog, xhr };
}

describe("BackupsTab upload backup", () => {
  it("offers Upload backup only with backups:restore", async () => {
    renderTab(["backups:read", "schedules:read"]);
    expect(await screen.findByRole("button", { name: /back up now/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^upload backup$/i })).not.toBeInTheDocument();
  });

  it("keeps Next disabled until a .tar.gz or .zip file is chosen", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    expect(dialog).toHaveTextContent("Step 1 of 4 · File");
    expect(within(dialog).getByRole("button", { name: /^next$/i })).toBeDisabled();
    await pick(u, "notes.txt");
    expect(await screen.findByRole("alert")).toHaveTextContent("Choose a .tar.gz or .zip file.");
    expect(within(dialog).getByRole("button", { name: /^next$/i })).toBeDisabled();
    await pick(u, "world.zip");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: /^next$/i })).toBeEnabled();
    expect(dialog).toHaveTextContent("world.zip");
  });

  it("closes on Escape while choosing a file", async () => {
    renderTab();
    const { u } = await openDialog();
    await u.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("ignores an empty file selection", async () => {
    renderTab();
    await openDialog();
    fireEvent.change(screen.getByTestId("backup-archive-input"), { target: { files: [] } });
    expect(screen.getByRole("button", { name: /^next$/i })).toBeDisabled();
  });

  it("uploads with progress, then shows the checking step", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "survival.tar.gz", 2048);
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    const xhr = FakeXHR.instances[0];
    expect(xhr.url).toBe("/restores:upload?server=alpha&format=tar.gz&fileName=survival.tar.gz&namespace=games");
    expect(dialog).toHaveTextContent("Step 2 of 4 · Upload");
    act(() => xhr.progress(1024, 2048));
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
    expect(dialog).toHaveTextContent("1.0 KB of 2.0 KB · 50%");
    expect(dialog).toHaveTextContent("The server keeps running while the file is uploaded and checked.");
    await act(async () => { xhr.finish(202, JSON.stringify({ restore: "restore-up-1" })); });
    expect(await screen.findByText("Checking archive…")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Step 3 of 4 · Check");
    await u.click(within(dialog).getByRole("button", { name: /^cancel$/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("shows zero progress for an empty total", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "empty.zip", 0);
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
  });

  it("cancels a running upload", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "survival.tar.gz");
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    await u.click(within(dialog).getByRole("button", { name: /^cancel$/i }));
    expect(FakeXHR.instances[0].aborted).toBe(true);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("asks for the game of a plain archive and sends it", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "data-folder.zip");
    await u.click(screen.getByRole("checkbox", { name: /not a gameplane backup/i }));
    expect(dialog).toHaveTextContent("Gameplane cannot check this archive");
    expect(dialog).toHaveTextContent("Only the safety checks run: paths, links, device files and size.");
    const game = screen.getByLabelText("Game");
    // The target server's own game is preselected once the lists have loaded.
    await waitFor(() => expect(game).toHaveValue("minecraft-vanilla"));
    await u.selectOptions(game, "valheim-default");
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    expect(FakeXHR.instances[0].url).toBe("/restores:upload?server=alpha&format=zip&fileName=data-folder.zip&template=valheim-default&namespace=games");
  });

  it("keeps Next disabled for a plain archive until a game is chosen", async () => {
    server.use(http.get("/servers/alpha", () => HttpResponse.json(makeServer({ metadata: { name: "alpha" }, spec: { templateRef: { name: "gone" } } }))));
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "data-folder.zip");
    await u.click(screen.getByRole("checkbox", { name: /not a gameplane backup/i }));
    const game = screen.getByLabelText("Game");
    await waitFor(() => expect(within(game).getAllByRole("option").length).toBeGreaterThan(1));
    expect(game).toHaveValue("");
    expect(within(dialog).getByRole("button", { name: /^next$/i })).toBeDisabled();
    await u.selectOptions(game, "minecraft-vanilla");
    expect(within(dialog).getByRole("button", { name: /^next$/i })).toBeEnabled();
  });

  it("shows the too-large screen for a 413", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "survival.tar.gz", 10);
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    await act(async () => { FakeXHR.instances[0].finish(413, "too big"); });
    expect(await screen.findByText("File is larger than the upload limit")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Upload not started");
    expect(dialog).toHaveTextContent("Nothing was uploaded");
    expect(dialog).toHaveTextContent("This file is 10 B.");
    await u.click(within(dialog).getByRole("button", { name: /^close$/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("recognises the too-large message id on another status", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "survival.tar.gz");
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    FakeXHR.instances[0].responseHeaders["Content-Type"] = "application/problem+json";
    await act(async () => { FakeXHR.instances[0].finish(400, JSON.stringify({ detail: "x", messageId: "server.restore.upload.too_large" })); });
    expect(await screen.findByText("File is larger than the upload limit")).toBeInTheDocument();
  });

  it("shows the no-storage-class screen", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "survival.tar.gz");
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    FakeXHR.instances[0].responseHeaders["Content-Type"] = "application/problem+json";
    await act(async () => { FakeXHR.instances[0].finish(409, JSON.stringify({ detail: "no class", messageId: "server.restore.upload.no_storage_class" })); });
    expect(await screen.findByText("No staging storage class is set")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("backups.uploadStagingStorageClass");
    expect(dialog).toHaveTextContent("Nothing was changed");
  });

  it("returns to the file step with the error for any other failure, so the user can retry", async () => {
    renderTab();
    const { u, dialog } = await openDialog();
    await pick(u, "survival.tar.gz");
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    await act(async () => { FakeXHR.instances[0].finish(409, "server busy"); });
    expect(await screen.findByRole("alert")).toHaveTextContent("server busy");
    await u.click(within(dialog).getByRole("button", { name: /^next$/i }));
    expect(FakeXHR.instances).toHaveLength(2);
    await act(async () => { FakeXHR.instances[1].onerror?.(); });
    expect(await screen.findByRole("alert")).toHaveTextContent("connection lost");
  });

  it("shows the review of a Gameplane archive with its warnings and confirms with the generation", async () => {
    current = gameplaneReview;
    renderTab();
    const { u, dialog } = await sendFile("survival.zip");
    expect(await screen.findByText("Source server")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Step 4 of 4 · Review");
    expect(dialog).toHaveTextContent("mc-survival");
    await waitFor(() => expect(dialog).toHaveTextContent("Minecraft (Vanilla)"));
    expect(dialog).toHaveTextContent("1.20.6");
    expect(dialog).toHaveTextContent("2026-11-25 03:00 UTC");
    expect(dialog).toHaveTextContent("data, mods");
    expect(dialog).toHaveTextContent("older than this server's 1.21.4");
    expect(dialog).toHaveTextContent("This replaces all data on mc-survival");
    expect(dialog).toHaveTextContent("If the copy fails partway, data may be partly replaced and the server stays stopped.");
    current = uploadRestoreOf({ phase: "Running", spec: { serverRef: { name: "mc-survival" } } });
    await u.click(within(dialog).getByRole("button", { name: /^confirm restore$/i }));
    expect(await screen.findByText("Restore running")).toBeInTheDocument();
    expect(confirmed).toEqual({ generation: 4 });
    expect(dialog).toHaveTextContent("Restoring mc-survival");
    expect(dialog).toHaveTextContent("Keep this window open until the restore finishes.");
    expect(within(dialog).queryByRole("button", { name: /^(cancel|close)$/i })).not.toBeInTheDocument();
    // The window stays open while the server is being restored.
    await u.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("falls back to the raw template name and empty manifest fields in the review", async () => {
    current = uploadRestoreOf({ phase: "AwaitingConfirmation", status: { phase: "AwaitingConfirmation", upload: { kind: "gameplane", manifest: { templateName: "unlisted-game" } } } });
    renderTab();
    const { dialog } = await sendFile();
    expect(await screen.findByText("Source server")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("unlisted-game");
    expect(dialog).toHaveTextContent("This replaces all data on alpha");
  });

  it("shows the review of a plain archive", async () => {
    current = uploadRestoreOf({
      phase: "AwaitingConfirmation",
      spec: { upload: { fileName: "folder.zip", format: "zip", declaredBytes: 10, confirmedTemplate: "minecraft-vanilla" } },
      status: { phase: "AwaitingConfirmation", upload: { kind: "plain" } },
    });
    renderTab();
    const { dialog } = await sendFile("folder.zip");
    expect(await screen.findByText("Plain archive, no Gameplane manifest")).toBeInTheDocument();
    await waitFor(() => expect(dialog).toHaveTextContent("Minecraft (Vanilla)"));
    expect(dialog).toHaveTextContent("Primary data volume");
    expect(dialog).toHaveTextContent("Gameplane cannot check the contents");
    expect(dialog).toHaveTextContent("The archive is written onto the primary data volume as-is.");
  });

  it("shows why a confirmation failed and lets the user try again", async () => {
    current = { ...gameplaneReview, metadata: { name: "restore-up-1", namespace: "games" } };
    server.use(http.post(/\/restores\/[^/]+:confirm(\?.*)?$/, async ({ request }) => {
      confirmed = await request.json();
      return new HttpResponse("not waiting", { status: 409 });
    }));
    renderTab();
    const { u, dialog } = await sendFile("survival.zip");
    await u.click(await within(dialog).findByRole("button", { name: /^confirm restore$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent("not waiting");
    // A restore without a generation is confirmed as generation 0, which the API refuses.
    expect(confirmed).toEqual({ generation: 0 });
    expect(within(dialog).getByRole("button", { name: /^confirm restore$/i })).toBeEnabled();
  });

  it("uses a fallback message when the confirm error has no body", async () => {
    current = gameplaneReview;
    server.use(http.post(/\/restores\/[^/]+:confirm(\?.*)?$/, () => HttpResponse.error()));
    renderTab();
    const { u, dialog } = await sendFile("survival.zip");
    await u.click(await within(dialog).findByRole("button", { name: /^confirm restore$/i }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });

  it("shows the result of a finished restore and closes with Done", async () => {
    current = uploadRestoreOf({ phase: "Succeeded", spec: { serverRef: { name: "mc-survival" } } });
    renderTab();
    const { u, dialog } = await sendFile();
    expect(await screen.findByText("mc-survival restored")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Restore finished");
    expect(dialog).toHaveTextContent("Data was replaced from survival.tar.gz. The server is running again.");
    await u.click(within(dialog).getByRole("button", { name: /^done$/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("explains that nothing changed when the archive was refused", async () => {
    current = uploadRestoreOf({ phase: "Failed", status: { phase: "Failed", message: "archive is for valheim-default, this server runs minecraft-vanilla" } });
    renderTab();
    const { dialog } = await sendFile();
    expect(await screen.findByText("Refused: archive is for valheim-default, this server runs minecraft-vanilla")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Upload refused");
    expect(dialog).toHaveTextContent("Nothing was written to alpha and the server was not stopped.");
  });

  it("uses a generic reason when a refusal carries no message", async () => {
    current = uploadRestoreOf({ phase: "Failed", status: { phase: "Failed" } });
    renderTab();
    await sendFile();
    expect(await screen.findByText("Refused: the archive failed its checks")).toBeInTheDocument();
  });

  it("says when the confirmation timed out", async () => {
    current = uploadRestoreOf({ phase: "Failed", status: { phase: "Failed", message: "confirmation timed out" } });
    renderTab();
    const { dialog } = await sendFile();
    expect(await within(dialog).findByText("Nothing was restored. The partial upload was deleted and the server is running as before.")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Restore not started");
  });

  it("warns that data may be partly replaced when the copy failed after confirmation", async () => {
    current = uploadRestoreOf({
      phase: "Failed",
      spec: { upload: { fileName: "survival.tar.gz", format: "tar.gz", declaredBytes: 10, confirmed: true } },
      status: { phase: "Failed", message: "copy failed" },
    });
    renderTab();
    const { dialog } = await sendFile();
    expect(await screen.findByText("copy failed")).toBeInTheDocument();
    expect(dialog).toHaveTextContent("Restore failed");
    expect(dialog).toHaveTextContent("The server's data may be partly replaced and the server stays stopped.");
  });

  it("uses a generic heading when a failed copy has no message", async () => {
    current = uploadRestoreOf({
      phase: "Failed",
      spec: { upload: { fileName: "survival.tar.gz", format: "tar.gz", declaredBytes: 10, confirmed: true } },
      status: { phase: "Failed" },
    });
    renderTab();
    await sendFile();
    expect(await screen.findByText("The restore failed")).toBeInTheDocument();
  });

  it("shows an error when the restore cannot be read", async () => {
    server.use(http.get("/restores/restore-up-1", () => new HttpResponse("restore gone", { status: 404 })));
    renderTab();
    await sendFile();
    expect(await screen.findByText("restore gone")).toBeInTheDocument();
  });

  it("keeps showing the checking step while the first read is pending", async () => {
    server.use(http.get("/restores/restore-up-1", async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
      return HttpResponse.json(current);
    }));
    renderTab();
    await sendFile();
    expect(screen.getByText("Checking archive…")).toBeInTheDocument();
  });
});
