import { describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import { BackupDestinationsPanel } from "./BackupDestinationsPanel";

describe("BackupDestinationsPanel", () => {
  it("binds list, create and confirmed deletion to the cluster and namespace", async () => {
    const requests: string[] = [];
    let exists = false;
    server.use(http.all("/backup-destinations", ({ request }) => {
      requests.push(request.url);
      if (request.method === "POST") exists = true;
      return HttpResponse.json({ items: exists ? [{ name: "repo", url: "s3:example/repo", hasPassword: true }] : [] });
    }), http.delete("/backup-destinations/repo", ({ request }) => {
      requests.push(request.url);
      exists = false;
      return new HttpResponse(null, { status: 204 });
    }));
    renderWithQuery(<BackupDestinationsPanel clusterId="east" />);
    await userEvent.click(await screen.findByRole("button", { name: "Add destination" }));
    await userEvent.type(screen.getByLabelText("Name (DNS label)"), "repo");
    await userEvent.type(screen.getByLabelText("Restic URL"), "s3:example/repo");
    await userEvent.type(screen.getByLabelText("Repository password"), "password");
    await userEvent.click(screen.getByRole("button", { name: "Save destination" }));
    await userEvent.click(await screen.findByRole("button", { name: "Delete repo" }));
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
    await userEvent.type(screen.getByPlaceholderText("Type to confirm"), "repo");
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Delete repo" })).not.toBeInTheDocument());
    expect(requests.length).toBeGreaterThanOrEqual(3);
    for (const request of requests) {
      const params = new URL(request).searchParams;
      expect(params.get("cluster")).toBe("east");
      expect(params.get("namespace")).toBe("gameplane-games");
    }
  });

  it("honors a namespace grant and does not borrow mutation permission from another cluster", async () => {
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ permissions: {}, permissionsByCluster: {
      east: { "gameplane-games": ["destinations:read"] }, west: { "*": ["destinations:manage"] },
    } }))));
    renderWithQuery(<BackupDestinationsPanel clusterId="east" />);
    expect(await screen.findByText("default")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add destination" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete default" })).not.toBeInTheDocument();
  });

  it("does not request destinations without read access to this cluster", async () => {
    const reads = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ permissions: {}, permissionsByCluster: {
      west: { "*": ["destinations:read", "destinations:manage"] },
    } }))), http.get("/backup-destinations", reads));
    renderWithQuery(<BackupDestinationsPanel clusterId="east" />);
    expect(await screen.findByText(/permission to view backup destinations/)).toBeInTheDocument();
    expect(reads).not.toHaveBeenCalled();
  });

  it("discards password drafts and mutation errors when the route target changes", async () => {
    const creates = vi.fn(() => HttpResponse.text("Cannot save", { status: 503 }));
    server.use(http.post("/backup-destinations", creates));
    const view = renderWithQuery(<BackupDestinationsPanel clusterId="east" />);
    await userEvent.click(await screen.findByRole("button", { name: "Add destination" }));
    await userEvent.type(screen.getByLabelText("Name (DNS label)"), "east-draft");
    await userEvent.type(screen.getByLabelText("Restic URL"), "s3:east/repo");
    await userEvent.type(screen.getByLabelText("Repository password"), "east-password");
    await userEvent.click(screen.getByRole("button", { name: "Save destination" }));
    expect(await screen.findByText(/Cannot save/)).toBeInTheDocument();
    view.rerender(<QueryClientProvider client={view.client}><BackupDestinationsPanel clusterId="west" /></QueryClientProvider>);
    expect(await screen.findByRole("button", { name: "Add destination" })).toBeInTheDocument();
    expect(screen.queryByDisplayValue("east-password")).not.toBeInTheDocument();
    expect(screen.queryByText(/Cannot save/)).not.toBeInTheDocument();
    expect(creates).toHaveBeenCalledTimes(1);
  });

  it("keeps a pending destination write on its captured target after the route changes", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const requests: string[] = [];
    server.use(http.post("/backup-destinations", async ({ request }) => {
      requests.push(request.url);
      await pending;
      return HttpResponse.json({ name: "repo", url: "s3:example/repo", hasPassword: true });
    }));
    const view = renderWithQuery(<BackupDestinationsPanel clusterId="east" />);
    await userEvent.click(await screen.findByRole("button", { name: "Add destination" }));
    await userEvent.type(screen.getByLabelText("Name (DNS label)"), "repo");
    await userEvent.type(screen.getByLabelText("Restic URL"), "s3:example/repo");
    await userEvent.type(screen.getByLabelText("Repository password"), "east-password");
    await userEvent.click(screen.getByRole("button", { name: "Save destination" }));
    await waitFor(() => expect(requests).toHaveLength(1));
    view.rerender(<QueryClientProvider client={view.client}><BackupDestinationsPanel clusterId="west" /></QueryClientProvider>);
    expect(await screen.findByRole("button", { name: "Add destination" })).toBeInTheDocument();
    release();
    await waitFor(() => expect(view.client.isMutating()).toBe(0));
    expect(new URL(requests[0]).searchParams.get("cluster")).toBe("east");
    expect(screen.queryByDisplayValue("east-password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save destination" })).not.toBeInTheDocument();
  });

  it("keeps failed deletion in its dialog and resets the error on dismissal", async () => {
    server.use(http.delete("/backup-destinations/default", () => HttpResponse.text("Deletion failed", { status: 503 })));
    renderWithQuery(<BackupDestinationsPanel clusterId="east" />);
    await userEvent.click(await screen.findByRole("button", { name: "Delete default" }));
    await userEvent.type(screen.getByPlaceholderText("Type to confirm"), "default");
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await within(screen.getByRole("alertdialog")).findByText(/Deletion failed/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete default" }));
    expect(screen.queryByText(/Deletion failed/)).not.toBeInTheDocument();
  });
  it("lists the cluster backup destinations", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({}),
      ),
      // BackupDestinations.list() hits /backup-destinations, not the
      // /admin-prefixed path — that prefix is only used for the auth
      // provider / mod registry secret write endpoints.
      http.get("/backup-destinations", () =>
        HttpResponse.json({
          items: [
            {
              name: "primary-backup",
              url: "s3:s3.example.com/bucket",
              hasPassword: true,
              createdAt: "2026-01-01T00:00:00Z",
            },
          ],
        }),
      ),
    );
    renderWithQuery(<BackupDestinationsPanel clusterId="local" />);
    expect(await screen.findByText("primary-backup")).toBeInTheDocument();
  });

  it("shows error when backup destinations fail to load", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({}),
      ),
      http.get("/backup-destinations", () =>
        new HttpResponse("Server error", { status: 500 }),
      ),
    );
    renderWithQuery(<BackupDestinationsPanel clusterId="local" />);
    expect(await screen.findByText(/error/i)).toBeInTheDocument();
  });

  it("shows empty state for backup destinations", async () => {
    server.use(
      http.get("/admin/config", () =>
        HttpResponse.json({}),
      ),
      http.get("/backup-destinations", () =>
        HttpResponse.json({ items: [] }),
      ),
    );
    renderWithQuery(<BackupDestinationsPanel clusterId="local" />);
    expect(
      await screen.findByText(/No backup destinations configured/i),
    ).toBeInTheDocument();
  });

  it("lists backup destinations and opens the add form", async () => {
    renderWithQuery(<BackupDestinationsPanel clusterId="local" />);
    // The default handler returns one destination ("default").
    await userEvent.click(await screen.findByRole("button", { name: /Add destination/i }));
    const nameField = await screen.findByPlaceholderText("gameplane-backup-repo");
    await userEvent.type(nameField, "repo1");
    await userEvent.type(screen.getByPlaceholderText(/s3:s3.example.com/i), "s3:host/bucket");
    await userEvent.type(screen.getByPlaceholderText(/passphrase/i), "a-strong-passphrase");
    const save = screen.getByRole("button", { name: /Save destination/i });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);
    // On success the form closes; the Add button returns.
    expect(await screen.findByRole("button", { name: /Add destination/i })).toBeInTheDocument();
  });

  it("prompts before deleting a backup destination", async () => {
    renderWithQuery(<BackupDestinationsPanel clusterId="local" />);
    await userEvent.click(await screen.findByRole("button", { name: /Delete default/i }));
    expect(await screen.findByText(/Delete backup destination\?/i)).toBeInTheDocument();
  });


});
