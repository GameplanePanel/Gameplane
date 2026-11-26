import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ModpackConfirmDialog } from "./modpack-confirm-dialog";

const base = (over: Partial<Parameters<typeof ModpackConfirmDialog>[0]> = {}) => ({
  open: true,
  onOpenChange: vi.fn(),
  mode: "install" as const,
  packTitle: "FTB Evolution 2",
  providerLabel: "FTB",
  versionName: "1.11.0",
  serverName: "test-server-02",
  backupAvailable: true,
  backupDestination: "s3-backups",
  pending: false,
  onConfirm: vi.fn(),
  ...over,
});

const WARNING_TITLE = "The server version changes";
const WARNING_BODY = "The server version changes from Latest · Fabric to 26.1.2 · NeoForge.";

describe("ModpackConfirmDialog server version change", () => {
  it("shows the version change warning with the current and target versions", () => {
    render(<ModpackConfirmDialog {...base({ serverVersionChange: { from: "Latest · Fabric", to: "26.1.2 · NeoForge" } })} />);
    expect(screen.getByText(WARNING_TITLE)).toBeInTheDocument();
    expect(screen.getByText(WARNING_BODY)).toBeInTheDocument();
  });

  it("shows no warning when the server version stays the same", () => {
    render(<ModpackConfirmDialog {...base()} />);
    expect(screen.queryByText(WARNING_TITLE)).not.toBeInTheDocument();
  });

  it("keeps the warning alongside the downgrade warning", () => {
    render(
      <ModpackConfirmDialog
        {...base({ mode: "downgrade", versionName: "1.10.2", fromVersionName: "1.11.0", serverVersionChange: { from: "Latest · Fabric", to: "26.1.2 · NeoForge" } })}
      />,
    );
    expect(screen.getByText("This is an older version")).toBeInTheDocument();
    expect(screen.getByText(WARNING_TITLE)).toBeInTheDocument();
  });
});
