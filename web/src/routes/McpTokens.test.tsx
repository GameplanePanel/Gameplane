import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";

const navigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

const listMcpTokens = vi.fn();
const createMcpToken = vi.fn();
const revokeMcpToken = vi.fn();
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  listMcpTokens: () => listMcpTokens(),
  createMcpToken: (name: string, days: number) => createMcpToken(name, days),
  revokeMcpToken: (id: number) => revokeMcpToken(id),
}));

const canMock = vi.fn(() => false);
vi.mock("@/lib/auth", async (orig) => ({
  ...(await orig<typeof import("@/lib/auth")>()),
  useMe: () => ({ data: makeUser({ id: 1, username: "root", role: "viewer" }), error: null, isLoading: false }),
  can: (...args: unknown[]) => canMock(...(args as [])),
}));

import { McpTokensPage } from "./McpTokens";

const FUTURE = "2099-01-01T00:00:00Z";
const PAST = "2000-01-01T00:00:00Z";
const SECRET = "gpmcp_Zm9vYmFyYmF6cXV4SECRETTAILtoken9";

const activeToken = {
  id: 1,
  name: "claude-desktop",
  prefix: "gpmcp_a1b2",
  createdAt: "2026-11-02T10:00:00Z",
  expiresAt: FUTURE,
  lastUsedAt: "2026-11-22T10:00:00Z",
  revokedAt: null,
};
const expiredToken = { ...activeToken, id: 2, name: "old-token", prefix: "gpmcp_7d0e", expiresAt: PAST, lastUsedAt: null };
const revokedToken = { ...activeToken, id: 3, name: "test", prefix: "gpmcp_c4b2", revokedAt: "2026-11-10T10:00:00Z" };

beforeEach(() => {
  listMcpTokens.mockResolvedValue({ items: [activeToken, expiredToken, revokedToken] });
  createMcpToken.mockResolvedValue({ ...activeToken, id: 9, name: "claude-desktop", prefix: "gpmcp_Zm9v", token: SECRET });
  revokeMcpToken.mockResolvedValue(undefined);
  canMock.mockReturnValue(false);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("McpTokensPage list", () => {
  it("shows active rows with Revoke and expired or revoked rows with badges and no Revoke", async () => {
    renderWithQuery(<McpTokensPage />);
    expect(await screen.findByText("claude-desktop")).toBeInTheDocument();
    expect(screen.getByText("Expired")).toBeInTheDocument();
    expect(screen.getByText("Revoked")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Revoke token / })).toHaveLength(1);
    expect(screen.getByText("Never")).toBeInTheDocument();
  });

  it("shows the empty state when the user has no tokens", async () => {
    listMcpTokens.mockResolvedValue({ items: [] });
    renderWithQuery(<McpTokensPage />);
    expect(await screen.findByText("No MCP tokens yet")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("shows the load error", async () => {
    listMcpTokens.mockRejectedValue(new Error("tokens unavailable"));
    renderWithQuery(<McpTokensPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent("tokens unavailable");
    expect(screen.queryByText("No MCP tokens yet")).not.toBeInTheDocument();
  });

  it("renders the settings nav with MCP tokens active and admin sections hidden without config:manage", async () => {
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");
    const current = screen.getByRole("link", { current: "page" });
    expect(current).toHaveTextContent("MCP tokens");
    expect(screen.queryByRole("button", { name: "General" })).not.toBeInTheDocument();
  });
});

describe("McpTokensPage create flow", () => {
  it("creates a token, shows the secret once and drops it on Done", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");

    await user.click(screen.getByRole("button", { name: "Create token" }));
    const create = screen.getByRole("button", { name: "Create" });
    expect(create).toBeDisabled();
    await user.type(screen.getByLabelText("Name"), "  claude-desktop  ");
    expect(create).toBeEnabled();
    await user.click(create);

    expect(await screen.findByRole("heading", { name: "Token created" })).toBeInTheDocument();
    expect(createMcpToken).toHaveBeenCalledWith("claude-desktop", 30);
    expect(screen.getByText("Copy this token now. It will not be shown again.")).toBeInTheDocument();
    expect(document.querySelector("pre")?.textContent).toContain(SECRET);
    expect(document.querySelector("pre")?.textContent).toContain('"GAMEPLANE_MCP_TOKEN"');

    await user.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Token created" })).not.toBeInTheDocument());
    expect(document.body.textContent).not.toContain(SECRET);
  });

  it("copies the full token and the client configuration to the clipboard", async () => {
    // userEvent.setup() installs its own clipboard stub, so mock after it.
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");

    await user.click(screen.getByRole("button", { name: "Create token" }));
    await user.type(screen.getByLabelText("Name"), "claude-desktop");
    await user.click(screen.getByRole("button", { name: "Create" }));
    await screen.findByRole("heading", { name: "Token created" });

    const copies = screen.getAllByRole("button", { name: "Copy" });
    expect(copies).toHaveLength(2);
    await user.click(copies[0]);
    expect(writeText).toHaveBeenLastCalledWith(SECRET);
    await user.click(copies[1]);
    expect(writeText).toHaveBeenLastCalledWith(expect.stringContaining(SECRET));
  });

  it("does not throw when the clipboard is refused", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");
    await user.click(screen.getByRole("button", { name: "Create token" }));
    await user.type(screen.getByLabelText("Name"), "x");
    await user.click(screen.getByRole("button", { name: "Create" }));
    await screen.findByRole("heading", { name: "Token created" });
    await user.click(screen.getAllByRole("button", { name: "Copy" })[0]);
    expect(writeText).toHaveBeenCalled();
    expect(await screen.findByText("Copying failed. Select the text and copy it by hand.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Token created" })).toBeInTheDocument();
  });

  it("shows the create error and keeps the dialog open", async () => {
    createMcpToken.mockRejectedValue(new Error("name already used"));
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");
    await user.click(screen.getByRole("button", { name: "Create token" }));
    await user.type(screen.getByLabelText("Name"), "dup");
    await user.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("name already used")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Token created" })).not.toBeInTheDocument();
  });

  it("closes the create dialog on Cancel without creating a token", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");
    await user.click(screen.getByRole("button", { name: "Create token" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument());
    expect(createMcpToken).not.toHaveBeenCalled();
  });
});

describe("McpTokensPage revoke", () => {
  it("asks for confirmation and revokes the token", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke token claude-desktop" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Revoke token claude-desktop?")).toBeInTheDocument();
    expect(within(dialog).getByText("Any MCP client using it stops working at once.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revokeMcpToken).toHaveBeenCalledWith(1));
  });

  it("closes the revoke confirmation on Cancel without revoking", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke token claude-desktop" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(revokeMcpToken).not.toHaveBeenCalled();
  });

  it("shows the revoke error inside the confirmation and clears it when the dialog closes", async () => {
    revokeMcpToken.mockRejectedValue(new Error("revoke refused"));
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke token claude-desktop" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));
    expect(await within(dialog).findByText("revoke refused")).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());

    await user.click(await screen.findByRole("button", { name: "Revoke token claude-desktop" }));
    const reopened = await screen.findByRole("alertdialog");
    expect(within(reopened).queryByText("revoke refused")).not.toBeInTheDocument();
  });

  it("closes the confirmation and refetches the list after a successful revoke", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke token claude-desktop" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revokeMcpToken).toHaveBeenCalledWith(1));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    await waitFor(() => expect(listMcpTokens.mock.calls.length).toBeGreaterThan(1));
  });
});

describe("McpTokensPage create options and dismissal", () => {
  it("creates the token with the lifetime picked in the select", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");
    await user.click(screen.getByRole("button", { name: "Create token" }));
    await user.click(screen.getByRole("button", { name: /Expires in/ }));
    await user.click(await screen.findByRole("option", { name: "90 days" }));
    await user.type(screen.getByLabelText("Name"), "long-lived");
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(createMcpToken).toHaveBeenCalledWith("long-lived", 90));
  });

  it("dismisses the create dialog with Escape without creating a token", async () => {
    const user = userEvent.setup();
    renderWithQuery(<McpTokensPage />);
    await screen.findByText("claude-desktop");
    await user.click(screen.getByRole("button", { name: "Create token" }));
    await user.type(screen.getByLabelText("Name"), "discard me");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument());
    expect(createMcpToken).not.toHaveBeenCalled();
  });
});
