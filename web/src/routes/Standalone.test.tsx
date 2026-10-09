import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { setCurrentCluster } from "@/lib/cluster";
import { ClusterPage } from "./Cluster";
import { ClustersPage } from "./Clusters";
import { ModulesPage } from "./Modules";
import { AdminLogsPage } from "./AdminLogs";
import { AdminSettingsPage } from "./AdminSettings";
import { ModuleSourcesPanel } from "@/components/modules/ModuleSourcesPanel";
import { makeCatalog, makeModuleSource, makeUser } from "@/test/factories";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}));

function standalone(clusters: Array<{ name: string; displayName?: string; canViewInventory?: boolean }> = []) {
  server.use(
    http.get("/admin/installation", () => HttpResponse.json({ standalone: true, localCluster: false })),
    http.get("/clusters", () => HttpResponse.json({ items: clusters })),
  );
}

afterEach(() => setCurrentCluster("local"));

describe("standalone panel", () => {
  it.each(["catalog", "sources"])("shows and retries an unavailable remote module %s", async (kind) => {
    standalone([{ name: "east" }]);
    setCurrentCluster("east");
    let unavailable = true;
    const paths: string[] = [];
    server.use(http.get(`/modules/${kind}`, ({ request }) => {
      paths.push(request.url);
      return unavailable ? HttpResponse.text("Cluster unavailable", { status: 503 }) : HttpResponse.json({ items: [] });
    }));
    renderWithQuery(<ModulesPage />);
    expect(await screen.findByText(kind === "catalog" ? "Failed to load module catalog." : "Failed to load module sources.")).toBeInTheDocument();
    if (kind === "catalog") expect(screen.queryByText(/No modules in any catalog/)).not.toBeInTheDocument();
    unavailable = false;
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(screen.queryByText(/Failed to load module/)).not.toBeInTheDocument());
    expect(await screen.findByRole("heading", { name: "Modules" })).toBeInTheDocument();
    expect(paths.length).toBeGreaterThanOrEqual(2);
    expect(paths.every((path) => new URL(path).searchParams.get("cluster") === "east")).toBe(true);
  });

  it("does not request backup destinations until a registered workload is selected", async () => {
    standalone();
    const destinations = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/backup-destinations", destinations));
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Backup destinations" }));
    expect(await screen.findByText("Select a workload cluster")).toBeInTheDocument();
    expect(destinations).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Add destination" })).not.toBeInTheDocument();
  });

  it("lists, creates and deletes backup destinations on the selected remote", async () => {
    standalone([{ name: "east" }]);
    setCurrentCluster("east");
    const requests: Array<{ method: string; cluster: string | null }> = [];
    let exists = false;
    server.use(http.all("/backup-destinations", ({ request }) => {
      requests.push({ method: request.method, cluster: new URL(request.url).searchParams.get("cluster") });
      if (request.method === "POST") exists = true;
      return HttpResponse.json({ items: exists ? [{ name: "repo", url: "s3:example/repo", hasPassword: true }] : [] });
    }), http.delete("/backup-destinations/repo", ({ request }) => {
      requests.push({ method: request.method, cluster: new URL(request.url).searchParams.get("cluster") });
      exists = false;
      return new HttpResponse(null, { status: 204 });
    }));
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Backup destinations" }));
    await userEvent.click(await screen.findByRole("button", { name: "Add destination" }));
    await userEvent.type(screen.getByPlaceholderText("gameplane-backup-repo"), "repo");
    await userEvent.type(screen.getByPlaceholderText("s3:s3.example.com/gameplane-bucket"), "s3:example/repo");
    await userEvent.type(screen.getByPlaceholderText("Strong, unique passphrase"), "password");
    await userEvent.click(screen.getByRole("button", { name: "Save destination" }));
    await userEvent.click(await screen.findByRole("button", { name: "Delete repo" }));
    await userEvent.type(screen.getByPlaceholderText("Type to confirm"), "repo");
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Delete repo" })).not.toBeInTheDocument());
    expect(requests).toEqual(expect.arrayContaining([
      { method: "GET", cluster: "east" }, { method: "POST", cluster: "east" }, { method: "DELETE", cluster: "east" },
    ]));
    expect(requests.every((request) => request.cluster === "east")).toBe(true);
  });

  it("lets remote module managers edit sources without panel configuration access", async () => {
    standalone([{ name: "east" }]);
    setCurrentCluster("east");
    const remove = vi.fn(({ request }: { request: Request }) => {
      expect(new URL(request.url).searchParams.get("cluster")).toBe("east");
      return new HttpResponse(null, { status: 204 });
    });
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({
      permissions: {}, permissionsByCluster: { east: { "*": ["modules:read", "modules:manage"] } },
    }))), http.get("/modules/catalog", () => HttpResponse.json({ items: [] })),
    http.get("/modules/sources", () => HttpResponse.json({ items: [makeModuleSource({ metadata: { name: "upstream" } })] })),
    http.delete("/modules/sources/upstream", remove));
    renderWithQuery(<ModulesPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Manage sources" }));
    expect(await screen.findByRole("button", { name: "Add source" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Delete upstream" }));
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
  });

  it("discards a backup destination draft when switching workload clusters", async () => {
    standalone([{ name: "east" }, { name: "west" }]);
    setCurrentCluster("east");
    const creates = vi.fn(() => HttpResponse.json({}));
    const reads: string[] = [];
    server.use(http.get("/backup-destinations", ({ request }) => {
      reads.push(new URL(request.url).searchParams.get("cluster") ?? "local");
      return HttpResponse.json({ items: [] });
    }), http.post("/backup-destinations", creates));
    renderWithQuery(<AdminSettingsPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Backup destinations" }));
    await userEvent.click(await screen.findByRole("button", { name: "Add destination" }));
    await userEvent.type(screen.getByPlaceholderText("gameplane-backup-repo"), "east-draft");
    await userEvent.click(screen.getByRole("button", { name: "Select cluster" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "west" }));
    await waitFor(() => expect(reads).toContain("west"));
    expect(screen.queryByDisplayValue("east-draft")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save destination" })).not.toBeInTheDocument();
    expect(creates).not.toHaveBeenCalled();
    expect(reads).not.toContain("local");
  });

  it("shows container runtime log guidance without opening a local log stream", async () => {
    standalone();
    const logs = vi.fn(() => HttpResponse.text("unexpected local logs"));
    server.use(http.get("/admin/system-logs/:component", logs));
    renderWithQuery(<AdminLogsPage />);
    expect(await screen.findByText("View logs through your container runtime")).toBeInTheDocument();
    expect(screen.getByText("docker compose logs --follow")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Download" })).not.toBeInTheDocument();
    expect(logs).not.toHaveBeenCalled();
  });

  it("does not grant module mutations from a different cluster's permissions", async () => {
    standalone([{ name: "east" }]);
    setCurrentCluster("east");
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({
      permissionsByCluster: { west: { "*": ["modules:read", "modules:manage"] }, east: { "*": ["modules:read"] } },
    }))),
    http.get("/modules/catalog", () => HttpResponse.json({ items: [makeCatalog({ installed: false })] })),
    http.get("/modules/sources", () => HttpResponse.json({ items: [makeModuleSource({ metadata: { name: "upstream" } })] })));
    const catalog = renderWithQuery(<ModulesPage />);
    expect(await screen.findByRole("button", { name: "Create module" })).toBeDisabled();
    expect(await screen.findByRole("button", { name: "Install" })).toBeDisabled();
    catalog.unmount();
    renderWithQuery(<ModuleSourcesPanel />);
    expect(await screen.findByRole("button", { name: "Add source" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Edit upstream" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete upstream" })).toBeDisabled();
  });

  it("does not request module data without read permission on the selected target", async () => {
    standalone([{ name: "east" }]);
    setCurrentCluster("east");
    const catalog = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ permissionsByCluster: { west: { "*": ["modules:read", "modules:manage"] } } }))),
      http.get("/modules/catalog", catalog));
    renderWithQuery(<ModulesPage />);
    expect(await screen.findByText("You don't have permission to view modules on this cluster.")).toBeInTheDocument();
    expect(catalog).not.toHaveBeenCalled();
  });
  it("does not assume a local module catalog when capabilities cannot be loaded", async () => {
    const catalog = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/admin/installation", () => HttpResponse.text("unavailable", { status: 503 })),
      http.get("/modules/catalog", catalog));
    renderWithQuery(<ModulesPage />);
    expect(await screen.findByText("Couldn't load installation capabilities.")).toBeInTheDocument();
    expect(catalog).not.toHaveBeenCalled();
  });
  it("does not request local node inventory or module resources on an empty installation", async () => {
    standalone();
    setCurrentCluster("local");
    const workload = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/cluster", workload), http.get("/cluster/info", workload),
      http.get("/modules/catalog", workload), http.get("/modules/sources", workload), http.get("/templates", workload));
    const inventory = renderWithQuery(<ClusterPage />);
    expect(await screen.findByText("Select a workload cluster")).toBeInTheDocument();
    expect(workload).not.toHaveBeenCalled();
    inventory.unmount();
    renderWithQuery(<ModulesPage />);
    expect(await screen.findByText("Select a workload cluster")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Manage clusters" })).toHaveAttribute("href", "/clusters");
    expect(workload).not.toHaveBeenCalled();
  });

  it("requires a registered selection and scopes module queries to that remote", async () => {
    standalone([{ name: "east", displayName: "East" }]);
    setCurrentCluster("deleted");
    const paths: string[] = [];
    server.use(http.get("/modules/catalog", ({ request }) => { paths.push(request.url); return HttpResponse.json({ items: [] }); }),
      http.get("/modules/sources", ({ request }) => { paths.push(request.url); return HttpResponse.json({ items: [] }); }));
    renderWithQuery(<ModulesPage />);
    expect(await screen.findByText("Select a workload cluster")).toBeInTheDocument();
    expect(paths).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: "Select cluster" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "East" }));
    expect(await screen.findByRole("heading", { name: "Modules" })).toBeInTheDocument();
    await waitFor(() => expect(paths.length).toBeGreaterThanOrEqual(2));
    expect(paths.every((path) => new URL(path).searchParams.get("cluster") === "east")).toBe(true);
  });

  it("registers a remote from an empty panel and does not synthesize local", async () => {
    standalone();
    let registered = false;
    const body = vi.fn();
    server.use(http.get("/clusters", () => HttpResponse.json({ items: registered ? [{ name: "east", displayName: "East", phase: "Unknown" }] : [] })),
      http.post("/clusters", async ({ request }) => {
        body(await request.json()); registered = true;
        return HttpResponse.json({ name: "east" }, { status: 201 });
      }));
    renderWithQuery(<ClustersPage />);
    expect(await screen.findByText("No clusters available")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Register cluster" }));
    await userEvent.type(screen.getByLabelText("Name"), "east");
    await userEvent.type(screen.getByLabelText("Display name"), "East");
    await userEvent.type(screen.getByLabelText("Kubeconfig"), "apiVersion: v1");
    await userEvent.click(screen.getByRole("button", { name: "Register" }));
    expect(await screen.findByRole("heading", { name: "East" })).toBeInTheDocument();
    expect(body).toHaveBeenCalledWith({ name: "east", displayName: "East", kubeconfig: "apiVersion: v1" });
    expect(screen.queryByText("Central cluster")).not.toBeInTheDocument();
  });

  it("keeps registration validation failures visible for correction", async () => {
    standalone();
    server.use(http.post("/clusters", () => HttpResponse.text("invalid kubeconfig", { status: 400 })));
    renderWithQuery(<ClustersPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Register cluster" }));
    expect(screen.getByRole("button", { name: "Register" })).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Name"), "east");
    await userEvent.type(screen.getByLabelText("Kubeconfig"), "invalid");
    await userEvent.click(screen.getByRole("button", { name: "Register" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("invalid kubeconfig");
    expect(screen.getByLabelText("Kubeconfig")).toHaveValue("invalid");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Kubeconfig")).not.toBeInTheDocument();
  });

  it("requires confirmation before removing a remote registration", async () => {
    standalone([{ name: "east", displayName: "East" }]);
    const remove = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(http.delete("/clusters/east", remove));
    renderWithQuery(<ClustersPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Remove cluster East" }));
    expect(remove).not.toHaveBeenCalled();
    expect(await screen.findByText(/Running workloads remain/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Remove cluster" }));
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
  });
});
