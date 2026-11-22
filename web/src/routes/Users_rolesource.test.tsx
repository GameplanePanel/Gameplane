import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import type { ExtendedUser } from "@/types";
import { UsersPage } from "./Users";

// Spec 034 T034/T046: role-source badges (design rJt1I), "Return to SSO
// control" in the row menu and the edit dialog with its confirm (pszxE).

const list = vi.fn();
const update = vi.fn();
vi.mock("@/lib/endpoints", () => ({
  Users: {
    list: () => list(),
    update: (id: number, body: unknown) => update(id, body),
    remove: vi.fn(),
    resetPassword: vi.fn(),
    create: vi.fn(),
    bindings: () => Promise.resolve([]),
    addBinding: vi.fn(),
    removeBinding: vi.fn(),
  },
  Clusters: { list: () => Promise.resolve({ items: [{ name: "local", displayName: "Local", phase: "Healthy", canViewInventory: true }] }) },
  Roles: {
    list: () =>
      Promise.resolve([
        { name: "admin", description: "Full access.", builtin: true, permissions: ["*"] },
        { name: "operator", description: "Manage servers.", builtin: true, permissions: ["servers:read", "servers:write"] },
        { name: "viewer", description: "Read-only.", builtin: true, permissions: ["servers:read"] },
        { name: "login-only", description: "Sign in only.", builtin: false, permissions: [] },
      ]),
    catalog: () => Promise.resolve({ groups: [] }),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
  },
}));
vi.mock("@/lib/auth", async (orig) => ({
  ...(await orig<typeof import("@/lib/auth")>()),
  useMe: () => ({
    data: {
      id: 1, username: "root", displayName: "Root", email: "r@x",
      role: "admin", permissions: { "*": ["*"] },
    },
    error: null,
    isLoading: false,
  }),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => (
    <a {...rest}>{children}</a>
  ),
}));

const user = (over: Partial<ExtendedUser>): ExtendedUser => ({
  id: 1,
  username: "root",
  displayName: "Root",
  email: "r@x",
  role: "admin",
  provider: "local",
  roleSource: "manual",
  createdAt: "2026-01-01T00:00:00Z",
  ...over,
});

const ROOT = user({});
const ALICE = user({ id: 2, username: "alice", displayName: "", email: "alice@corp.example", provider: "oidc", roleSource: "sso" });
const BOB = user({ id: 3, username: "bob", displayName: "", email: "bob@corp.example", role: "operator", provider: "oidc", roleSource: "manual" });
const CAROL = user({ id: 4, username: "carol", displayName: "", email: "carol@corp.example", role: "login-only", provider: "oidc", roleSource: "sso_pending" });

function rowOf(username: string): HTMLElement {
  const row = screen.getByText(username).closest<HTMLElement>('[role="row"]');
  if (!row) throw new Error(`no row for ${username}`);
  return row;
}

beforeEach(() => {
  list.mockReset();
  update.mockReset();
  list.mockResolvedValue([ROOT, ALICE, BOB, CAROL]);
});

describe("UsersPage role source", () => {
  it("shows a badge for each role source and none for local users", async () => {
    renderWithQuery(<UsersPage />);
    await screen.findByText("alice");
    expect(within(rowOf("alice")).getByText("From SSO")).toBeInTheDocument();
    expect(within(rowOf("bob")).getByText("Set by hand")).toBeInTheDocument();
    expect(within(rowOf("carol")).getByText("From SSO (applies at next sign-in)")).toBeInTheDocument();
    // root is a local account whose source is manual: no badge at all.
    const root = rowOf("root");
    expect(within(root).queryByText("Set by hand")).not.toBeInTheDocument();
    expect(within(root).queryByText(/From SSO/)).not.toBeInTheDocument();
  });

  it("offers Return to SSO control only for SSO users whose role was set by hand", async () => {
    const u = userEvent.setup();
    renderWithQuery(<UsersPage />);
    await screen.findByText("bob");
    for (const name of ["root", "alice", "carol"]) {
      await u.click(screen.getByLabelText(`Actions for ${name}`));
      expect(await screen.findByRole("menuitem", { name: /Edit user/ })).toBeInTheDocument();
      expect(screen.queryByRole("menuitem", { name: /Return to SSO control/ })).not.toBeInTheDocument();
      await u.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByRole("menuitem", { name: /Edit user/ })).not.toBeInTheDocument());
    }
    await u.click(screen.getByLabelText("Actions for bob"));
    expect(await screen.findByRole("menuitem", { name: /Return to SSO control/ })).toBeInTheDocument();
  });

  it("confirms, sends roleSource sso and refreshes the users list", async () => {
    const u = userEvent.setup();
    update.mockResolvedValue({ ...BOB, roleSource: "sso_pending" });
    renderWithQuery(<UsersPage />);
    await screen.findByText("bob");
    await u.click(screen.getByLabelText("Actions for bob"));
    await u.click(await screen.findByRole("menuitem", { name: /Return to SSO control/ }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Return bob to SSO control?")).toBeInTheDocument();
    expect(
      within(dialog).getByText("Their role will follow your SSO rules again from their next sign-in. Until then they keep operator."),
    ).toBeInTheDocument();
    list.mockResolvedValue([ROOT, ALICE, { ...BOB, roleSource: "sso_pending" }, CAROL]);
    await u.click(within(dialog).getByRole("button", { name: "Return to SSO control" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(3, { roleSource: "sso" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    // The users query was refreshed: bob's row now shows the pending badge.
    expect(await within(rowOf("bob")).findByText("From SSO (applies at next sign-in)")).toBeInTheDocument();
  });

  it("sends nothing when the confirm is cancelled", async () => {
    const u = userEvent.setup();
    renderWithQuery(<UsersPage />);
    await screen.findByText("bob");
    await u.click(screen.getByLabelText("Actions for bob"));
    await u.click(await screen.findByRole("menuitem", { name: /Return to SSO control/ }));
    const dialog = await screen.findByRole("alertdialog");
    await u.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(update).not.toHaveBeenCalled();
  });

  it("shows the role source, helper and return button in the edit dialog of a hand-set SSO user", async () => {
    const u = userEvent.setup();
    update.mockResolvedValue({ ...BOB, roleSource: "sso_pending" });
    renderWithQuery(<UsersPage />);
    await screen.findByText("bob");
    await u.click(screen.getByLabelText("Actions for bob"));
    await u.click(await screen.findByRole("menuitem", { name: /Edit user/ }));
    const dialog = await screen.findByRole("dialog", { name: /Edit user/ });
    expect(within(dialog).getByText("Role source")).toBeInTheDocument();
    expect(within(dialog).getByText("Set by hand")).toBeInTheDocument();
    expect(
      within(dialog).getByText("Changing the role here marks it as set by hand. Sign-in will no longer change it."),
    ).toBeInTheDocument();
    await u.click(within(dialog).getByRole("button", { name: "Return to SSO control" }));
    const confirm = await screen.findByRole("alertdialog");
    await u.click(within(confirm).getByRole("button", { name: "Return to SSO control" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(3, { roleSource: "sso" }));
    // The open edit dialog now shows the pending badge and no return button.
    expect(await within(dialog).findByText("From SSO (applies at next sign-in)")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Return to SSO control" })).not.toBeInTheDocument();
  });

  it("shows the helper without a return button for an SSO-managed user", async () => {
    const u = userEvent.setup();
    renderWithQuery(<UsersPage />);
    await screen.findByText("alice");
    await u.click(screen.getByLabelText("Actions for alice"));
    await u.click(await screen.findByRole("menuitem", { name: /Edit user/ }));
    const dialog = await screen.findByRole("dialog", { name: /Edit user/ });
    expect(within(dialog).getByText("From SSO")).toBeInTheDocument();
    expect(within(dialog).getByText(/Changing the role here marks it as set by hand/)).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Return to SSO control" })).not.toBeInTheDocument();
  });

  it("shows no role source in the edit dialog of a local user", async () => {
    const u = userEvent.setup();
    renderWithQuery(<UsersPage />);
    await screen.findByText("root");
    await u.click(screen.getByLabelText("Actions for root"));
    await u.click(await screen.findByRole("menuitem", { name: /Edit user/ }));
    const dialog = await screen.findByRole("dialog", { name: /Edit user/ });
    expect(within(dialog).queryByText("Role source")).not.toBeInTheDocument();
    expect(within(dialog).queryByText(/Changing the role here marks it/)).not.toBeInTheDocument();
  });
});
