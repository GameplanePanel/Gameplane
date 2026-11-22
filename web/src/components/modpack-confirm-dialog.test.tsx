import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ModpackConfirmDialog } from "./modpack-confirm-dialog";

type Props = Parameters<typeof ModpackConfirmDialog>[0];

function base(overrides: Partial<Props> = {}): Props {
  return {
    open: true,
    onOpenChange: vi.fn(),
    mode: "install",
    packTitle: "All the Mods",
    providerLabel: "CurseForge",
    versionName: "2.0.0",
    serverName: "survival",
    backupAvailable: true,
    pending: false,
    onConfirm: vi.fn(),
    ...overrides,
  };
}

describe("ModpackConfirmDialog", () => {
  it.each([
    ["install", "Install All the Mods?", "Install and restart"],
    ["change", "Switch to 2.0.0?", "Change version and restart"],
    ["downgrade", "Go back to 2.0.0?", "Downgrade and restart"],
    ["remove", "Remove All the Mods?", "Remove modpack"],
  ] as const)("shows the %s title and primary label", (mode, title, label) => {
    render(<ModpackConfirmDialog {...base({ mode })} />);
    expect(screen.getByText(title)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: label })).toBeEnabled();
  });

  it("shows the downgrade warning only in downgrade mode", () => {
    const { unmount } = render(<ModpackConfirmDialog {...base({ mode: "downgrade" })} />);
    expect(screen.getByText("This is an older version")).toBeInTheDocument();
    unmount();
    for (const mode of ["install", "change", "remove"] as const) {
      const r = render(<ModpackConfirmDialog {...base({ mode })} />);
      expect(screen.queryByText("This is an older version")).toBeNull();
      r.unmount();
    }
  });

  it("names the version it replaces in change mode", () => {
    render(<ModpackConfirmDialog {...base({ mode: "change", fromVersionName: "1.0.0" })} />);
    expect(screen.getByText("survival restarts on All the Mods 2.0.0 instead of 1.0.0.")).toBeInTheDocument();
  });

  it("explains the restart without a from version", () => {
    render(<ModpackConfirmDialog {...base({ mode: "change" })} />);
    expect(screen.getByText("survival restarts on All the Mods 2.0.0.")).toBeInTheDocument();
  });

  it("omits the pack summary version in remove mode and warns about hand-added mods", () => {
    render(<ModpackConfirmDialog {...base({ mode: "remove" })} />);
    expect(screen.queryByText("CurseForge · 2.0.0")).toBeNull();
    expect(screen.getByText(/including ones added by hand/)).toBeInTheDocument();
    expect(screen.getByText("The pack is removed and survival restarts as a plain server.")).toBeInTheDocument();
  });

  it("shows the pack summary with the provider and version outside remove mode", () => {
    render(<ModpackConfirmDialog {...base({ mode: "install" })} />);
    expect(screen.getByText("CurseForge · 2.0.0")).toBeInTheDocument();
  });

  it("ticks back up first and passes backupFirst true when a destination exists", async () => {
    const onConfirm = vi.fn();
    render(<ModpackConfirmDialog {...base({ onConfirm })} />);
    expect(screen.getByRole("checkbox")).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    expect(onConfirm).toHaveBeenCalledWith({ backupFirst: true });
  });

  it("passes backupFirst false once the box is unticked", async () => {
    const onConfirm = vi.fn();
    render(<ModpackConfirmDialog {...base({ onConfirm })} />);
    await userEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    expect(onConfirm).toHaveBeenCalledWith({ backupFirst: false });
  });

  it("re-ticks the backup box when the dialog reopens", () => {
    const { rerender } = render(<ModpackConfirmDialog {...base({ open: true })} />);
    rerender(<ModpackConfirmDialog {...base({ open: false })} />);
    rerender(<ModpackConfirmDialog {...base({ open: true })} />);
    expect(screen.getByRole("checkbox")).toBeChecked();
  });

  it("disables the backup box with a reason and passes false when no destination exists", async () => {
    const onConfirm = vi.fn();
    render(<ModpackConfirmDialog {...base({ backupAvailable: false, onConfirm })} />);
    const box = screen.getByRole("checkbox");
    expect(box).toBeDisabled();
    expect(box).not.toBeChecked();
    expect(screen.getByText("No backup destination is set for this server's cluster.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    expect(onConfirm).toHaveBeenCalledWith({ backupFirst: false });
  });

  it("disables both buttons and shows working while pending", () => {
    render(<ModpackConfirmDialog {...base({ pending: true })} />);
    expect(screen.getByRole("button", { name: "Working…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  });

  it("closes through Cancel when not pending", async () => {
    const onOpenChange = vi.fn();
    render(<ModpackConfirmDialog {...base({ onOpenChange })} />);
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("shows the error text", () => {
    render(<ModpackConfirmDialog {...base({ error: "Backup failed" })} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Backup failed");
  });
});
