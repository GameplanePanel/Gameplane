import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ModpackConfirmDialog } from "./modpack-confirm-dialog";

const RESTART_SENTENCE = "The server restarts on the new version right away, before any backup finishes.";

describe("ModpackConfirmDialog server version restart notice", () => {
  it("says the server restarts on the new version before any backup finishes", () => {
    render(
      <ModpackConfirmDialog
        open
        onOpenChange={vi.fn()}
        mode="install"
        packTitle="FTB Evolution 2"
        providerLabel="FTB"
        versionName="1.11.0"
        serverName="test-server-02"
        backupAvailable
        backupDestination="s3-backups"
        pending={false}
        onConfirm={vi.fn()}
        serverVersionChange={{ from: "Latest · Fabric", to: "26.1.2 · NeoForge" }}
      />,
    );
    expect(screen.getByText("The server version changes from Latest · Fabric to 26.1.2 · NeoForge.")).toBeInTheDocument();
    expect(screen.getByText(RESTART_SENTENCE)).toBeInTheDocument();
  });

  it("shows no restart notice when the server version stays the same", () => {
    render(
      <ModpackConfirmDialog
        open
        onOpenChange={vi.fn()}
        mode="install"
        packTitle="FTB Evolution 2"
        providerLabel="FTB"
        versionName="1.11.0"
        serverName="test-server-02"
        backupAvailable
        backupDestination="s3-backups"
        pending={false}
        onConfirm={vi.fn()}
      />,
    );
    expect(screen.queryByText(RESTART_SENTENCE)).not.toBeInTheDocument();
  });
});
