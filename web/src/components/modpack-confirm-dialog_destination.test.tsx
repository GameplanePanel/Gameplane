import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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

describe("ModpackConfirmDialog backup destination", () => {
  it("names the backup destination when one is known", () => {
    render(<ModpackConfirmDialog {...base({ backupDestination: "s3-backups" })} />);
    expect(screen.getByText(/\(s3-backups\)/)).toBeInTheDocument();
    expect(screen.getByText(/backup destination \(s3-backups\) before the change/)).toBeInTheDocument();
  });

  it("keeps the generic backup text when no name is given", () => {
    render(<ModpackConfirmDialog {...base()} />);
    expect(screen.getByText("Takes a backup to this server's backup destination before the change. If the backup fails, nothing changes.")).toBeInTheDocument();
    expect(screen.queryByText(/s3-backups/)).not.toBeInTheDocument();
  });
});
