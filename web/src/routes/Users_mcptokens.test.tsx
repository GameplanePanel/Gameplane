import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { UsersPage } from "./Users";

const usersList = vi.fn();
vi.mock("@/lib/endpoints", () => ({
  Users: {
    list: () => usersList(),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
    resetPassword: vi.fn(),
  },
  Roles: { list: vi.fn(async () => []) },
  Clusters: { list: vi.fn(async () => []) },
}));

const listUserMcpTokens = vi.fn();
const revokeUserMcpToken = vi.fn();
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  listUserMcpTokens: (id: number) => listUserMcpTokens(id),
  revokeUserMcpToken: (userId: number, tokenId: number) => revokeUserMcpToken(userId, tokenId),
}));

const canMock = vi.fn((_me: unknown, perm: string) => perm !== "users:manage");
vi.mock("@/lib/auth", async (orig) => ({
  ...(await orig<typeof import("@/lib/auth")>()),
  useMe: () => ({
    data: { id: 1, username: "root", displayName: "Root", email: "r@x", role: "admin" },
    error: null,
    isLoading: false,
  }),
  can: (me: unknown, perm: string) => canMock(me, perm),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => <a {...rest}>{children}</a>,
}));

const collaborator = {
  id: 2,
  username: "smoke-collab",
  displayName: "",
  email: "",
  role: "viewer",
  provider: "local",
  createdAt: "2026-11-18T10:00:00Z",
  roleSource: "manual",
};

const tokens = [
  {
    id: 11,
    name: "collab-cli",
    prefix: "gpmcp_b7e2",
    createdAt: "2026-11-01T10:00:00Z",
    expiresAt: "2099-01-15T00:00:00Z",
    lastUsedAt: "2026-11-19T10:00:00Z",
    revokedAt: null,
  },
  {
    id: 12,
    name: "laptop",
    prefix: "gpmcp_5e1a",
    createdAt: "2026-11-02T10:00:00Z",
    expiresAt: "2000-12-12T00:00:00Z",
    lastUsedAt: null,
    revokedAt: null,
  },
  {
    id: 13,
    name: "retired",
    prefix: "gpmcp_0000",
    createdAt: "2026-11-02T10:00:00Z",
    expiresAt: "2099-12-12T00:00:00Z",
    lastUsedAt: null,
    revokedAt: "2026-11-05T10:00:00Z",
  },
];

async function openTokensDialog() {
  const user = userEvent.setup();
  renderWithQuery(<UsersPage />);
  await user.click(await screen.findByRole("button", { name: "Actions for smoke-collab" }));
  await user.click(await screen.findByRole("menuitem", { name: /MCP tokens/ }));
  return user;
}

beforeEach(() => {
  usersList.mockResolvedValue([collaborator]);
  listUserMcpTokens.mockResolvedValue({ items: tokens });
  revokeUserMcpToken.mockResolvedValue(undefined);
  canMock.mockImplementation((_me: unknown, perm: string) => perm !== "users:manage");
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("UsersPage MCP tokens dialog", () => {
  it("lists the user's tokens with their state", async () => {
    await openTokensDialog();
    const dialog = await screen.findByRole("dialog", { name: "MCP tokens for smoke-collab" });
    expect(listUserMcpTokens).toHaveBeenCalledWith(2);
    expect(within(dialog).getByText("collab-cli")).toBeInTheDocument();
    expect(within(dialog).getByText("gpmcp_b7e2…")).toBeInTheDocument();
    expect(within(dialog).getByText("Expired")).toBeInTheDocument();
    expect(within(dialog).getByText("Revoked")).toBeInTheDocument();
    // Tokens 12 and 13 were never used, so the text appears twice.
    expect(within(dialog).getAllByText("Last used never")).toHaveLength(2);
  });

  it("hides Revoke without users:manage", async () => {
    await openTokensDialog();
    const dialog = await screen.findByRole("dialog", { name: "MCP tokens for smoke-collab" });
    await within(dialog).findByText("collab-cli");
    expect(within(dialog).queryByRole("button", { name: "Revoke" })).not.toBeInTheDocument();
  });

  it("revokes an active token with users:manage and refreshes the list", async () => {
    canMock.mockImplementation(() => true);
    const user = await openTokensDialog();
    const dialog = await screen.findByRole("dialog", { name: "MCP tokens for smoke-collab" });
    const revokeButtons = await within(dialog).findAllByRole("button", { name: "Revoke" });
    expect(revokeButtons).toHaveLength(1);
    await user.click(revokeButtons[0]);
    await waitFor(() => expect(revokeUserMcpToken).toHaveBeenCalledWith(2, 11));
    await waitFor(() => expect(listUserMcpTokens.mock.calls.length).toBeGreaterThan(1));
  });

  it("shows the revoke error", async () => {
    canMock.mockImplementation(() => true);
    revokeUserMcpToken.mockRejectedValue(new Error("not allowed"));
    const user = await openTokensDialog();
    const dialog = await screen.findByRole("dialog", { name: "MCP tokens for smoke-collab" });
    await user.click(await within(dialog).findByRole("button", { name: "Revoke" }));
    expect(await within(dialog).findByText("not allowed")).toBeInTheDocument();
  });

  it("shows the empty text when the user has no tokens and closes", async () => {
    listUserMcpTokens.mockResolvedValue({ items: [] });
    const user = await openTokensDialog();
    const dialog = await screen.findByRole("dialog", { name: "MCP tokens for smoke-collab" });
    expect(await within(dialog).findByText("No MCP tokens.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "MCP tokens for smoke-collab" })).not.toBeInTheDocument());
  });
});
