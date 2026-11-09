import { describe, it, expect, vi, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import type { ClusterRegistry } from "@/types";
import { getCurrentCluster, setCurrentCluster } from "@/lib/cluster";
const navigate = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigate }));
import { ClusterConfiguration } from "./ClusterConfiguration";
const local: ClusterRegistry = { name: "local", displayName: "Local", phase: "Healthy", canViewInventory: true };
function ClusterPage() { return <ClusterConfiguration cluster={local} />; }
afterEach(() => { vi.restoreAllMocks(); navigate.mockReset(); setCurrentCluster("local"); });
describe("ClusterConfiguration", () => {
  it.each([false, true, undefined])("honors clusterOps=%s while retaining compatibility with an unavailable info endpoint", async (clusterOps) => {
    server.use(http.get("/cluster/info", () => clusterOps === undefined ? HttpResponse.error() : HttpResponse.json({ clusterOps })));
    const { client } = renderWithQuery(<ClusterPage />);
    await waitFor(() => expect(client.getQueryState(["cluster-info", "local"])?.fetchStatus).toBe("idle"));
    const button = await screen.findByRole("button", { name: /download kubeconfig/i });
    if (clusterOps === false) expect(button).toBeDisabled(); else expect(button).toBeEnabled();
  });

  it("shows the local operation-disabled error returned by the API", async () => {
    server.use(http.post("/cluster/kubeconfig", () => HttpResponse.text("disabled", { status: 501 })));
    renderWithQuery(<ClusterPage />);
    await userEvent.click(await screen.findByRole("button", { name: /download kubeconfig/i }));
    expect(await screen.findByText(/aren't enabled/)).toBeInTheDocument();
  });

  it("does not expose local configuration or downloadable credentials on a remote", async () => {
    const config = vi.fn(() => HttpResponse.json({ installTimeSettings: { gameDataStorageClass: "local-only" } }));
    server.use(http.get("/admin/config", config));
    renderWithQuery(<ClusterConfiguration cluster={{ ...local, name: "east", displayName: "East" }} />);
    expect(await screen.findByRole("button", { name: /download kubeconfig/i })).toBeDisabled();
    expect(screen.getByText(/managed at this remote cluster/)).toBeInTheDocument();
    expect(screen.queryByText("Game data storage class")).not.toBeInTheDocument();
    expect(config).not.toHaveBeenCalled();
  });

  it("does not mint credentials for a viewer", async () => {
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))));
    renderWithQuery(<ClusterPage />);
    expect(await screen.findByRole("button", { name: /download kubeconfig/i })).toBeDisabled();
  });

  it("discards a late local download after navigating to a remote cluster", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    let requested = false;
    server.use(http.post("/cluster/kubeconfig", async () => { requested = true; await pending; return HttpResponse.text("local-secret"); }));
    const blob = vi.fn(() => "blob:test");
    globalThis.URL.createObjectURL = blob;
    const view = renderWithQuery(<ClusterPage />);
    await userEvent.click(await screen.findByRole("button", { name: /download kubeconfig/i }));
    await waitFor(() => expect(requested).toBe(true));
    view.rerender(<QueryClientProvider client={view.client}><ClusterConfiguration cluster={{ ...local, name: "east" }} /></QueryClientProvider>);
    await act(async () => { release(); await pending; });
    await waitFor(() => expect(view.client.isMutating()).toBe(0));
    expect(blob).not.toHaveBeenCalled();
  });

  it("requires confirmation, retains removal errors in the dialog, and clears only the removed selection", async () => {
    setCurrentCluster("east");
    let fails = true;
    const remove = vi.fn(() => fails ? HttpResponse.text("failed", { status: 503 }) : new HttpResponse(null, { status: 204 }));
    server.use(http.delete("/clusters/east", remove));
    renderWithQuery(<ClusterConfiguration cluster={{ ...local, name: "east", displayName: "East" }} />);
    await userEvent.click(await screen.findByRole("button", { name: "Remove cluster East" }));
    expect(remove).not.toHaveBeenCalled();
    expect(within(screen.getByRole("alertdialog")).getByText(/Running workloads remain/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Remove cluster" }));
    const dialog = screen.getByRole("alertdialog");
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Couldn't remove the cluster registration.");
    expect(getCurrentCluster()).toBe("east");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await userEvent.click(screen.getByRole("button", { name: "Remove cluster East" }));
    expect(within(screen.getByRole("alertdialog")).queryByRole("alert")).not.toBeInTheDocument();
    fails = false;
    await userEvent.click(screen.getByRole("button", { name: "Remove cluster" }));
    await waitFor(() => expect(getCurrentCluster()).toBe(""));
    expect(localStorage.getItem("gameplane.cluster")).toBe("");
    expect(navigate).toHaveBeenCalledWith({ to: "/clusters" });
  });

  it("keeps another cluster selected after removing a registration", async () => {
    setCurrentCluster("west");
    server.use(http.delete("/clusters/east", () => new HttpResponse(null, { status: 204 })));
    renderWithQuery(<ClusterConfiguration cluster={{ ...local, name: "east", displayName: "East" }} />);
    await userEvent.click(await screen.findByRole("button", { name: "Remove cluster East" }));
    await userEvent.click(screen.getByRole("button", { name: "Remove cluster" }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/clusters" }));
    expect(getCurrentCluster()).toBe("west");
  });
  it("downloads kubeconfig blob", async () => {
    const createObjectURLMock = vi.fn(() => "blob:mock-url");
    const revokeObjectURLMock = vi.fn();
    const clickMock = vi.fn();

    globalThis.URL.createObjectURL = createObjectURLMock;
    globalThis.URL.revokeObjectURL = revokeObjectURLMock;

    server.use(
      http.post("/cluster/kubeconfig", () =>
        // A raw `Blob` body throws inside msw's Response construction on this
        // environment's jsdom (its Blob lacks `.stream()`, which msw's
        // Response proxy calls while building the mocked Response) — use a
        // string body instead. The client's `res.blob()` still round-trips it
        // into a real Blob regardless of the original body shape (same fix
        // as AuditLog.test.tsx's CSV export test).
        new HttpResponse("kubeconfig content", { headers: { "Content-Type": "text/plain" } })
      ),
    );

    renderWithQuery(<ClusterPage />);
    const downloadBtn = await screen.findByRole("button", { name: /download kubeconfig/i });

    // Mock createElement and click
    const originalCreateElement = document.createElement.bind(document);
    document.createElement = vi.fn((tag) => {
      if (tag === "a") {
        const el = originalCreateElement("a");
        el.click = clickMock;
        return el;
      }
      return originalCreateElement(tag);
    });

    await userEvent.click(downloadBtn);
    await waitFor(() => {
      expect(clickMock).toHaveBeenCalled();
      expect(createObjectURLMock).toHaveBeenCalledWith(expect.any(Blob));
      expect(revokeObjectURLMock).toHaveBeenCalledWith("blob:mock-url");
    });

    // Cleanup
    document.createElement = originalCreateElement;
  });

  it("shows error when kubeconfig download fails", async () => {
    server.use(
      http.post("/cluster/kubeconfig", () =>
        new HttpResponse("forbidden", { status: 403 })
      ),
    );
    renderWithQuery(<ClusterPage />);
    const downloadBtn = await screen.findByRole("button", { name: /download kubeconfig/i });
    await userEvent.click(downloadBtn);
    expect(await screen.findByText(/don't have permission/i, {}, { timeout: 10000 })).toBeInTheDocument();
  });

  it("shows generic error when kubeconfig download fails with 500", async () => {
    server.use(
      http.post("/cluster/kubeconfig", () =>
        new HttpResponse("internal error", { status: 500 })
      ),
    );
    renderWithQuery(<ClusterPage />);
    const downloadBtn = await screen.findByRole("button", { name: /download kubeconfig/i });
    await userEvent.click(downloadBtn);
    expect(await screen.findByText(/operation failed/i)).toBeInTheDocument();
  });

  describe("Storage section", () => {
    it("renders the storage card when installTimeSettings is present with a configured class", async () => {
      server.use(
        http.get("/admin/config", () =>
          HttpResponse.json({
            general: {
              instanceName: "Gameplane",
              externalURL: "https://gameplane.local",
              defaultNamespace: "gameplane-games",
            },
            installTimeSettings: {
              gameDataStorageClass: "fast-nvme",
            },
          }),
        ),
      );
      renderWithQuery(<ClusterPage />);
      // Wait for the page to render and the config to load.
      expect(await screen.findByText("Storage")).toBeInTheDocument();
      expect(screen.getByText("Game data storage class")).toBeInTheDocument();
      expect(screen.getByText("fast-nvme")).toBeInTheDocument();
      expect(screen.getByText(/StorageClass for new GameServers/)).toBeInTheDocument();
    });

    it("renders the storage card with 'Cluster default' when gameDataStorageClass is unset", async () => {
      server.use(
        http.get("/admin/config", () =>
          HttpResponse.json({
            general: {
              instanceName: "Gameplane",
              externalURL: "https://gameplane.local",
              defaultNamespace: "gameplane-games",
            },
            installTimeSettings: {
              gameDataStorageClass: "",
            },
          }),
        ),
      );
      renderWithQuery(<ClusterPage />);
      expect(await screen.findByText("Storage")).toBeInTheDocument();
      expect(screen.getByText("Game data storage class")).toBeInTheDocument();
      // Should render the "Cluster default" pill badge.
      expect(screen.getByText("Cluster default")).toBeInTheDocument();
      // Should NOT render the storage class as plain text.
      expect(screen.queryByText(/^fast-nvme$/)).not.toBeInTheDocument();
      // Should include the additional hint text about using cluster default.
      expect(screen.getByText(/Left unset, so new volumes use the cluster's default StorageClass/)).toBeInTheDocument();
    });

    it("does not render the storage card when installTimeSettings is absent", async () => {
      server.use(
        http.get("/admin/config", () =>
          HttpResponse.json({
            general: {
              instanceName: "Gameplane",
              externalURL: "https://gameplane.local",
              defaultNamespace: "gameplane-games",
            },
          }),
        ),
      );
      renderWithQuery(<ClusterPage />);
      // The page should still render (with node data).
      await screen.findByRole("button", { name: /download kubeconfig/i });
      // But the storage section should not be present.
      expect(screen.queryByText("Storage")).not.toBeInTheDocument();
      expect(screen.queryByText("Game data storage class")).not.toBeInTheDocument();
    });

    it("does not render storage card and does not fire admin-config query for users without config:read permission", async () => {
      const configHandler = vi.fn(() =>
        HttpResponse.json({
          general: {
            instanceName: "Gameplane",
            externalURL: "https://gameplane.local",
            defaultNamespace: "gameplane-games",
          },
          installTimeSettings: {
            gameDataStorageClass: "fast-nvme",
          },
        }),
      );
      server.use(
        http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))),
        http.get("/admin/config", configHandler),
      );
      const { client } = renderWithQuery(<ClusterPage />);
      // Wait for the viewer identity to load.
      await waitFor(() => expect(client.getQueryData(["me"])).toBeTruthy());
      // Wait for the page to render (the cluster data should load).
      await screen.findByRole("button", { name: /download kubeconfig/i });
      // The storage section should NOT be present.
      expect(screen.queryByText("Storage")).not.toBeInTheDocument();
      expect(screen.queryByText("Game data storage class")).not.toBeInTheDocument();
      // The /admin/config handler should NOT have been called.
      expect(configHandler).not.toHaveBeenCalled();
    });
  });
});
