import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { render, screen, within } from "@testing-library/react";
import { SettingsNav } from "./SettingsNav";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

describe("SettingsNav groups", () => {
  it("lists Account (Theme, SFTP) before Server, with the admin sections under Server", () => {
    render(<SettingsNav active="sftp" onSelect={() => undefined} showAdminSections />);
    const account = screen.getByRole("group", { name: "Account" });
    const server = screen.getByRole("group", { name: "Server" });
    expect(account.compareDocumentPosition(server) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    expect(within(account).getByRole("link", { name: /Theme/ })).toHaveAttribute("href", "/settings/theme");
    const sftp = within(account).getByRole("link", { name: /SFTP/ });
    expect(sftp).toHaveAttribute("href", "/settings/sftp");
    expect(sftp).toHaveAttribute("aria-current", "page");

    for (const label of ["General", "Authentication", "Module sources", "Mod registries", "Notifications", "Telemetry", "Updates", "About"]) {
      expect(within(server).getByRole("button", { name: label })).toBeInTheDocument();
    }
  });

  it("shows only the Account group to users without admin sections", () => {
    render(<SettingsNav active="theme" onSelect={() => undefined} showAdminSections={false} />);
    expect(screen.getByRole("group", { name: "Account" })).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Server" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /SFTP/ })).toHaveAttribute("href", "/settings/sftp");
    expect(screen.queryByRole("button", { name: "General" })).not.toBeInTheDocument();
  });
});
