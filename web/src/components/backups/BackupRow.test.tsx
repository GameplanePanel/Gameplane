import type React from "react";
import { describe, it, expect, vi } from "vitest";
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
            <Table.Column key="name" isRowHeader>
              Name
            </Table.Column>
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

describe("BackupRow", () => {
  it("clicking the row calls onSelect", async () => {
    const onSelect = vi.fn();
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({ metadata: { name: "alpha-1" } })}
          showServer={true}
          onSelect={onSelect}
          onRestore={() => {}}
        />,
      ),
    );
    await userEvent.click(screen.getByText("alpha-1"));
    expect(onSelect).toHaveBeenCalled();
  });

  it("renders the backup name and server", async () => {
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({
            metadata: { name: "alpha-1" },
            spec: { serverRef: { name: "alpha" } }
          })}
          showServer={true}
          onSelect={() => {}}
          onRestore={() => {}}
        />,
      ),
    );
    // Verify that the backup row renders the backup name
    expect(screen.getByText("alpha-1")).toBeInTheDocument();
    // Verify that the server name is displayed when showServer=true
    expect(screen.getByText("alpha")).toBeInTheDocument();
  });

  it.each([[undefined, undefined], ["0", "0 B"], ["13827", "14 KB"], ["317Ki", "317 KB"]])("keeps logical size separate from packed bytes %s", (added, formatted) => {
    const backup = makeBackup({ status: { size: "4Gi", bytesAdded: added } });
    render(tableWrap(<BackupRow backup={backup} showServer={false} onSelect={() => {}} onRestore={() => {}} />));
    expect(screen.getByText("4Gi")).toBeInTheDocument();
    if (added == null) expect(screen.queryByText(/ added$/)).not.toBeInTheDocument();
    else expect(screen.getByText(formatted + " added")).toBeInTheDocument();
  });

  it("Restore button is enabled when phase=Succeeded with snapshotID", async () => {
    const onRestore = vi.fn();
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({
            metadata: { name: "alpha-1" },
            status: { phase: "Succeeded", snapshotID: "abc123" },
          })}
          showServer={false}
          onSelect={() => {}}
          onRestore={onRestore}
        />,
      ),
    );
    const btn = screen.getByRole("button", { name: "Restore" });
    expect(btn).toBeEnabled();
    await userEvent.click(btn);
    expect(onRestore).toHaveBeenCalled();
  });

  it("Restore button is disabled when no snapshotID", () => {
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({ status: { phase: "Succeeded" } })}
          showServer={false}
          onSelect={() => {}}
          onRestore={() => {}}
        />,
      ),
    );
    expect(screen.getByRole("button", { name: "Restore" })).toBeDisabled();
  });

  it("Restore button is disabled when phase is not Succeeded", () => {
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({ status: { phase: "Running", snapshotID: "x" } })}
          showServer={false}
          onSelect={() => {}}
          onRestore={() => {}}
        />,
      ),
    );
    expect(screen.getByRole("button", { name: "Restore" })).toBeDisabled();
  });

  it("hides server column when showServer=false", () => {
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({ spec: { serverRef: { name: "srv-x" } } })}
          showServer={false}
          onSelect={() => {}}
          onRestore={() => {}}
        />,
      ),
    );
    expect(screen.queryByText("srv-x")).not.toBeInTheDocument();
  });

  it("shows the server name when showServer=true", () => {
    render(
      tableWrap(
        <BackupRow permissions={["backups:restore"]}
          backup={makeBackup({ spec: { serverRef: { name: "srv-x" } } })}
          showServer={true}
          onSelect={() => {}}
          onRestore={() => {}}
        />,
      ),
    );
    expect(screen.getByText("srv-x")).toBeInTheDocument();
  });
});
