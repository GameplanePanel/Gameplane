import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ModpackActive, ModpackPhase, ModpackView } from "@/types";
import { ModpackActiveCard } from "./modpack-active-card";

const active: ModpackActive = {
  provider: "curseforge",
  project: "1234",
  version: "5678",
  title: "All The Mods 9",
  versionName: "0.2.1",
  pinned: true,
};

function view(over: Partial<ModpackView> = {}, act: Partial<ModpackActive> | null = {}): ModpackView {
  return {
    active: act === null ? null : { ...active, ...act },
    phase: "Applied" as ModpackPhase,
    message: "",
    backupName: "",
    keyShared: true,
    missingFiles: [],
    missingFilesUnknown: false,
    downloadsPath: "/data/mods-downloads",
    backupAvailable: true,
    ...over,
  };
}

function renderCard(v: ModpackView, opts: { canManage?: boolean; onChangeVersion?: () => void; onRemove?: () => void } = {}) {
  const onChangeVersion = opts.onChangeVersion ?? vi.fn();
  const onRemove = opts.onRemove ?? vi.fn();
  render(
    <ModpackActiveCard
      view={v}
      mcLabel="Minecraft 1.21.1 · Neoforge"
      canManage={opts.canManage ?? true}
      onChangeVersion={onChangeVersion}
      onRemove={onRemove}
    />,
  );
  return { onChangeVersion, onRemove };
}

describe("ModpackActiveCard", () => {
  it("renders nothing when no pack is active", () => {
    const { container } = render(
      <ModpackActiveCard view={view({}, null)} mcLabel="x" canManage onChangeVersion={vi.fn()} onRemove={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the applied state with version, provider and Minecraft label", () => {
    renderCard(view());
    expect(screen.getByText("Active modpack")).toBeInTheDocument();
    expect(screen.getByText("All The Mods 9")).toBeInTheDocument();
    expect(screen.getByText("Applied")).toBeInTheDocument();
    expect(screen.getByText("Version 0.2.1 · pinned · Minecraft 1.21.1 · Neoforge")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /change version/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /remove/i })).toBeEnabled();
  });

  it("shows the backing up state with its default message and disables actions", () => {
    renderCard(view({ phase: "BackingUp" }));
    expect(screen.getByText("Backing up")).toBeInTheDocument();
    expect(screen.getByText("Backing up before switching. The server keeps running.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /change version/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /remove/i })).toBeDisabled();
  });

  it("shows the applying state with the restart message and disables actions", () => {
    renderCard(view({ phase: "Applying" }));
    expect(screen.getByText("Applying")).toBeInTheDocument();
    expect(screen.getByText("Restarting with the new version…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /change version/i })).toBeDisabled();
  });

  it("prefers the server message over the default when one is set", () => {
    renderCard(view({ phase: "Applying", message: "Custom server message" }));
    expect(screen.getByText("Custom server message")).toBeInTheDocument();
    expect(screen.queryByText("Restarting with the new version…")).not.toBeInTheDocument();
  });

  it("shows backup failure in danger text and keeps remove enabled for managers", () => {
    renderCard(view({ phase: "BackupFailed", message: "Backup could not be written" }));
    expect(screen.getByText("Backup failed")).toBeInTheDocument();
    expect(screen.getByText("Backup could not be written")).toHaveClass("text-danger");
    expect(screen.getByRole("button", { name: /change version/i })).toBeEnabled();
  });

  it("offers Pick version when the installed version is not pinned", () => {
    const onChangeVersion = vi.fn();
    renderCard(view({}, { pinned: false }), { onChangeVersion });
    expect(screen.getByText("Not pinned")).toBeInTheDocument();
    expect(screen.getByText(/The installed version could not be read/)).toBeInTheDocument();
    expect(screen.getByText("Version unknown · Minecraft 1.21.1 · Neoforge")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /pick version/i }));
    expect(onChangeVersion).toHaveBeenCalledTimes(1);
  });

  it("shows the removing state and disables actions until the removal fails", () => {
    renderCard(view({}, { pendingRemoval: true }));
    expect(screen.getByText("Removing")).toBeInTheDocument();
    expect(screen.getByText("Removing the pack and emptying the mods folder at the next start…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /change version/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /remove/i })).toBeDisabled();
  });

  it("turns a removal whose backup failed into Retry remove", () => {
    const onRemove = vi.fn();
    renderCard(view({ phase: "BackupFailed", message: "Disk full" }, { pendingRemoval: true }), { onRemove });
    expect(screen.getByText("Backup failed")).toBeInTheDocument();
    expect(screen.getByText("Disk full")).toHaveClass("text-danger");
    const retry = screen.getByRole("button", { name: /retry remove/i });
    expect(retry).toBeEnabled();
    fireEvent.click(retry);
    expect(onRemove).toHaveBeenCalledTimes(1);
  });

  it("renders an FTB pack with its provider label", () => {
    renderCard(view({}, { provider: "ftb", title: "Vault Hunters", versionName: "3.0" }));
    expect(screen.getByText("Vault Hunters")).toBeInTheDocument();
    expect(screen.getByText("FTB")).toBeInTheDocument();
  });

  it("disables both actions for read-only users and explains why", () => {
    renderCard(view(), { canManage: false });
    expect(screen.getByRole("button", { name: /change version/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /remove/i })).toBeDisabled();
    expect(screen.getAllByTitle("Requires operator role").length).toBeGreaterThan(0);
  });

  it("calls onRemove when Remove is pressed", () => {
    const { onRemove, onChangeVersion } = renderCard(view());
    fireEvent.click(screen.getByRole("button", { name: /remove/i }));
    expect(onRemove).toHaveBeenCalledTimes(1);
    expect(onChangeVersion).not.toHaveBeenCalled();
  });

  it("calls onChangeVersion when Change version is pressed", () => {
    const { onChangeVersion, onRemove } = renderCard(view());
    fireEvent.click(screen.getByRole("button", { name: /change version/i }));
    expect(onChangeVersion).toHaveBeenCalledTimes(1);
    expect(onRemove).not.toHaveBeenCalled();
  });
});
