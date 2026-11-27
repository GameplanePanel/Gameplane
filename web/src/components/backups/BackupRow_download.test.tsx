import type React from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Table } from "@heroui/react";
import { BackupRow } from "./BackupRow";
import { makeBackup } from "@/test/factories";

function tableWrap(children: React.ReactNode) {
  return (
    <Table.Root>
      <Table.ScrollContainer>
        <Table.Content aria-label="Backups">
          <Table.Header>
            <Table.Column key="name" isRowHeader>Name</Table.Column>
            <Table.Column key="server">Server</Table.Column>
            <Table.Column key="phase">Phase</Table.Column>
            <Table.Column key="size">Size</Table.Column>
            <Table.Column key="completed">Completed</Table.Column>
            <Table.Column key="actions" />
          </Table.Header>
          <Table.Body>{children}</Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table.Root>
  );
}

function renderRow(permissions: string[], backup = makeBackup(), onDownload?: (b: unknown) => void) {
  return render(tableWrap(
    <BackupRow permissions={permissions} backup={backup} showServer onSelect={() => {}} onRestore={() => {}} onDownload={onDownload} />,
  ));
}

describe("BackupRow download action", () => {
  it("calls onDownload for a finished restic backup", async () => {
    const onDownload = vi.fn();
    const backup = makeBackup();
    renderRow(["backups:read"], backup, onDownload);
    await userEvent.click(screen.getByRole("button", { name: "Download" }));
    expect(onDownload).toHaveBeenCalledWith(backup);
  });

  it("hides Download without backups:read", () => {
    renderRow(["backups:restore"], makeBackup(), vi.fn());
    expect(screen.queryByRole("button", { name: "Download" })).not.toBeInTheDocument();
    expect(screen.queryByTitle("Snapshot backups stay inside the storage system.")).not.toBeInTheDocument();
  });

  it("shows a dash instead of Download for a volume-snapshot backup", () => {
    renderRow(["backups:read"], makeBackup({ spec: { serverRef: { name: "alpha" }, strategy: "volume-snapshot" } }), vi.fn());
    expect(screen.queryByRole("button", { name: "Download" })).not.toBeInTheDocument();
    expect(screen.getByTitle("Snapshot backups stay inside the storage system.")).toBeInTheDocument();
  });

  it("shows nothing for a backup that is not downloadable yet", () => {
    renderRow(["backups:read"], makeBackup({ status: { phase: "Running" } }), vi.fn());
    expect(screen.queryByRole("button", { name: "Download" })).not.toBeInTheDocument();
  });

  it("has no Download control when the page does not wire downloads", () => {
    renderRow(["backups:read"]);
    expect(screen.queryByRole("button", { name: "Download" })).not.toBeInTheDocument();
  });
});
