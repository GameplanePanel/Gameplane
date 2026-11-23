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
describe("ClusterDetailPage control-plane namespace", () => {
  it("shows the stored control-plane namespace of a remote cluster", async () => {
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", displayName: "East", phase: "Healthy", canViewInventory: false, controlPlaneNamespace: "platform-ops" }] })));
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByText("Registration ID")).toBeInTheDocument();
    const label = screen.getByText("Control-plane namespace");
    expect(label.nextElementSibling).toHaveTextContent("platform-ops");
  });

  it("falls back to gameplane when the registry entry has no namespace", async () => {
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", displayName: "East", phase: "Healthy", canViewInventory: false }] })));
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByText("Registration ID")).toBeInTheDocument();
    expect(screen.getByText("Control-plane namespace").nextElementSibling).toHaveTextContent("gameplane");
  });

  it("hides the control-plane namespace for the local cluster", async () => {
    route.clusterId = "local";
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "local", displayName: "local", phase: "Healthy", canViewInventory: false, controlPlaneNamespace: "gameplane" }] })));
    renderWithQuery(<ClusterDetailPage />);
    expect(await screen.findByText("Central cluster")).toBeInTheDocument();
    expect(screen.getByText("Registration ID")).toBeInTheDocument();
    expect(screen.queryByText("Control-plane namespace")).not.toBeInTheDocument();
  });
});
