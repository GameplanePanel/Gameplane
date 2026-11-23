import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { UsersPage } from "./Users";

const list = vi.fn();
vi.mock("@/lib/endpoints", () => ({
  Users: {
    list: () => list(),
    update: vi.fn(),
    remove: vi.fn(),
    resetPassword: vi.fn(),
    create: vi.fn(),
    bindings: () => Promise.resolve([]),
    addBinding: vi.fn(),
    removeBinding: vi.fn(),
  },
  Roles: {
    list: () => Promise.resolve([]),
    catalog: () => Promise.resolve({ groups: [] }),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
  },
}));
vi.mock("@/lib/auth", async (orig) => ({
  ...(await orig<typeof import("@/lib/auth")>()),
  useMe: () => ({
    data: { id: 1, username: "root", displayName: "Root", email: "r@x", role: "admin", permissions: { "*": ["*"] } },
    error: null,
    isLoading: false,
  }),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => (
    <a {...rest}>{children}</a>
  ),
}));

const adminView = {
  userId: 2,
  username: "smoke",
  keys: [
    {
      id: 7,
      name: "laptop",
      type: "ssh-ed25519",
      fingerprint: "SHA256:9dK1vT7qLm2Xr8ZcW4bN0eH6sJ3yU5pA1fG",
      createdAt: "2026-10-03T10:00:00Z",
      lastUsedAt: null,
    },
  ],
  password: { length: 32, createdAt: "2026-10-03T10:00:00Z", lastUsedAt: null },
};

beforeEach(() => {
  list.mockResolvedValue([
    { id: 1, username: "root", displayName: "Root", email: "r@x", role: "admin", provider: "local" },
    { id: 2, username: "smoke", displayName: "", email: "", role: "viewer", provider: "local" },
  ]);
  server.use(http.get("/users/2/sftp", () => HttpResponse.json(adminView)));
});

afterEach(() => {
  server.resetHandlers();
});

async function openSftpDrawer() {
  renderWithQuery(<UsersPage />);
  await userEvent.click(await screen.findByRole("button", { name: "Actions for smoke" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: /SFTP access/ }));
  return screen.findByRole("dialog");
}

describe("Users SFTP drawer", () => {
  it("opens from the row menu and lists the user's keys and password", async () => {
    const drawer = await openSftpDrawer();
    expect(await within(drawer).findByText("SFTP access · smoke")).toBeInTheDocument();
    expect(within(drawer).getByText(/laptop · ssh-ed25519/)).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Revoke" })).toBeInTheDocument();
  });

  it("removes one key of another user", async () => {
    let removed: string | undefined;
    server.use(
      http.delete("/users/:id/sftp/keys/:keyId", ({ params }) => {
        removed = `${params.id}/${params.keyId}`;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const drawer = await openSftpDrawer();
    await userEvent.click(await within(drawer).findByRole("button", { name: "Remove" }));
    await waitFor(() => expect(removed).toBe("2/7"));
  });

  it("revokes another user's password", async () => {
    let revokedFor: string | undefined;
    server.use(
      http.delete("/users/:id/sftp/password", ({ params }) => {
        revokedFor = String(params.id);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const drawer = await openSftpDrawer();
    await userEvent.click(await within(drawer).findByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(revokedFor).toBe("2"));
  });
});
