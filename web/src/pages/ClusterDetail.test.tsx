import { describe, expect, it, vi, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { setCurrentCluster } from "@/lib/cluster";
const route = vi.hoisted(() => ({ clusterId: "east", tab: "overview" }));
vi.mock("@tanstack/react-router", () => ({ useParams: () => ({ clusterId: route.clusterId }), useSearch: () => ({ tab: route.tab }), useNavigate: () => vi.fn() }));
vi.mock("@/components/nodes/NodeInventory", () => ({ NodeInventory: ({ clusterId }: { clusterId?: string }) => <div>Inventory for {clusterId}</div> }));
import { ClusterDetailPage } from "./ClusterDetail";

afterEach(() => { route.clusterId = "east"; route.tab = "overview"; setCurrentCluster("local"); });
describe("ClusterDetailPage", () => {
  it("uses its route registration independently of remembered selection", async () => {
    setCurrentCluster("west");
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", displayName: "East", phase: "Healthy", canViewInventory: true }] })));
    route.tab = "nodes";
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByRole("heading", { name: "East", level: 1 })).toBeInTheDocument();
    expect(screen.getByText("Inventory for east")).toBeInTheDocument();
  });

  it("does not mount inventory for a registry entry without its explicit capability", async () => {
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", displayName: "East", phase: "Healthy", canViewInventory: false }] })));
    route.tab = "nodes";
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByText(/permission to view node inventory/)).toBeInTheDocument();
    expect(screen.queryByText("Inventory for east")).not.toBeInTheDocument();
  });

  it("never invents a local registration on an empty standalone installation", async () => {
    route.clusterId = "local";
    route.tab = "configuration";
    const destinations = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/admin/installation", () => HttpResponse.json({ standalone: true, localCluster: false })), http.get("/clusters", () => HttpResponse.json({ items: [] })), http.get("/backup-destinations", destinations));
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByText("Cluster unavailable")).toBeInTheDocument();
    expect(destinations).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Download kubeconfig" })).not.toBeInTheDocument();
  });

  it("shows a retryable registry error instead of treating a failed registry as an absent cluster", async () => {
    server.use(http.get("/clusters", () => HttpResponse.text("unavailable", { status: 503 })));
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByText(/Couldn't load registered clusters/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByText("Cluster unavailable")).not.toBeInTheDocument();
  });
});
