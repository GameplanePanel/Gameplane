import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ModpackConfirmDialog } from "./modpack-confirm-dialog";

const VERSION_CHANGE_WITH_DESTINATION =
  "Takes a backup to the cluster’s backup destination (s3-backups). The server version changes right away; the pack is installed only after the backup succeeds.";
const OLD_HELP_WITH_DESTINATION =
  "Takes a backup to the cluster’s backup destination (s3-backups) before the change. If the backup fails, nothing changes.";

describe("ModpackConfirmDialog backup help during a server version change", () => {
  it("explains the version changes right away and the pack waits for the backup", () => {
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
    expect(screen.getByText(VERSION_CHANGE_WITH_DESTINATION)).toBeInTheDocument();
    expect(screen.queryByText(OLD_HELP_WITH_DESTINATION)).not.toBeInTheDocument();
  });

  it("keeps the original backup help when the server version stays the same", () => {
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
    expect(screen.getByText(OLD_HELP_WITH_DESTINATION)).toBeInTheDocument();
    expect(screen.queryByText(VERSION_CHANGE_WITH_DESTINATION)).not.toBeInTheDocument();
  });
});
