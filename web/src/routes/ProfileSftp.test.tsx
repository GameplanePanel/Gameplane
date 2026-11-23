import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { ProfileSftpPage } from "./ProfileSftp";

const endpoint = {
  address: "sftp.gameplane.example",
  port: 2222,
  hostKeyFingerprint: "SHA256:4mQ2xR7vK9pL0wZ3sB8nT1cY6hJ5gF2dA0eU",
};

const laptopKey = {
  id: 7,
  name: "laptop",
  publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIN7d laptop",
  fingerprint: "SHA256:9dK1vT7qLm2Xr8ZcW4bN0eH6sJ3yU5pA1fG",
  createdAt: "2026-10-03T10:00:00Z",
  lastUsedAt: null,
};

const passwordSet = { length: 32, createdAt: "2026-10-03T10:00:00Z", lastUsedAt: null };

function selfBody(over: Partial<{ endpoint: unknown; keys: unknown[]; password: unknown }> = {}) {
  return {
    username: "alice",
    endpoint,
    keys: [laptopKey],
    password: passwordSet,
    ...over,
  };
}

function useSelf(body: ReturnType<typeof selfBody>) {
  server.use(http.get("/users/me/sftp", () => HttpResponse.json(body)));
}

beforeEach(() => {
  server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))));
});

afterEach(() => {
  server.resetHandlers();
});

describe("ProfileSftpPage", () => {
  it("shows the address, the username forms, the keys and the password state", async () => {
    useSelf(selfBody());
    renderWithQuery(<ProfileSftpPage />);
    await screen.findByRole("heading", { name: "SFTP access" });
    expect(await screen.findByText("sftp.gameplane.example")).toBeInTheDocument();
    expect(screen.getByText("2222")).toBeInTheDocument();
    expect(screen.getByText("alice+<server>")).toBeInTheDocument();
    expect(screen.getByText("laptop")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Regenerate/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Revoke/ })).toBeInTheDocument();
  });

  it("shows the not-enabled note and no address rows when the cluster has no endpoint", async () => {
    useSelf(selfBody({ endpoint: null }));
    renderWithQuery(<ProfileSftpPage />);
    expect(await screen.findByText(/SFTP is not enabled on this cluster/)).toBeInTheDocument();
    expect(screen.queryByText("sftp.gameplane.example")).not.toBeInTheDocument();
  });

  it("offers Generate password when none is set", async () => {
    useSelf(selfBody({ password: null, keys: [] }));
    renderWithQuery(<ProfileSftpPage />);
    expect(await screen.findByRole("button", { name: /Generate password/ })).toBeInTheDocument();
    expect(screen.getByText("No SSH keys yet.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Revoke/ })).not.toBeInTheDocument();
  });

  it("shows the inline server error when a key is refused", async () => {
    useSelf(selfBody({ password: null, keys: [] }));
    server.use(
      http.post("/users/me/sftp/keys", () =>
        HttpResponse.json(
          { detail: "This is not a valid OpenSSH public key." },
          { status: 400, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    renderWithQuery(<ProfileSftpPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Add key/ }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Name"), "laptop");
    await userEvent.type(within(dialog).getByLabelText("Public key"), "not a key");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add key" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("This is not a valid OpenSSH public key.");
  });

  it("generates a password with the default length of 32 and shows it once", async () => {
    useSelf(selfBody({ password: null, keys: [] }));
    let requestedLength: number | undefined;
    server.use(
      http.post("/users/me/sftp/password", async ({ request }) => {
        const body = (await request.json()) as { length: number };
        requestedLength = body.length;
        return HttpResponse.json({ password: "k7#Qp2vR9xW4mT8nL1zF5dH0bY3sC6g", length: body.length });
      }),
    );
    renderWithQuery(<ProfileSftpPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Generate password/ }));
    const gen = await screen.findByRole("dialog");
    expect(within(gen).getByText("32 characters")).toBeInTheDocument();
    await userEvent.click(within(gen).getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(requestedLength).toBe(32));
    expect(await screen.findByTestId("sftp-password-value")).toHaveTextContent("k7#Qp2vR9xW4mT8nL1zF5dH0bY3sC6g");
    expect(screen.getByText(/You will not see this again/)).toBeInTheDocument();
  });

  it("confirms before revoking the password", async () => {
    useSelf(selfBody());
    let revoked = false;
    server.use(
      http.delete("/users/me/sftp/password", () => {
        revoked = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<ProfileSftpPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Revoke/ }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Revoke SFTP password?")).toBeInTheDocument();
    expect(revoked).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(revoked).toBe(true));
  });

  it("confirms before removing a key", async () => {
    useSelf(selfBody());
    let removedId: string | undefined;
    server.use(
      http.delete("/users/me/sftp/keys/:id", ({ params }) => {
        removedId = String(params.id);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<ProfileSftpPage />);
    await userEvent.click(await screen.findByRole("button", { name: /Remove/ }));
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(removedId).toBe("7"));
  });
});
