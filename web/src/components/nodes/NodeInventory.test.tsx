import { describe, expect, it } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import type { FleetInventory, FleetResult } from "@/lib/fleet";
import { NodeInventory } from "./NodeInventory";

function result(items: FleetInventory[], extra: Partial<FleetResult<FleetInventory>> = {}) {
  return { items, scopes: [{ cluster: "east" }, { cluster: "west" }], partial: false, issues: [], totalReturned: items.length, ...extra };
}
const items: FleetInventory[] = [
  { cluster: "east", name: "East", stats: {}, view: { nodes: [{ name: "same", status: "Ready", cpu: { used: 0, capacity: 4 } }] } },
  { cluster: "west", name: "West", stats: {}, view: { nodes: [{ name: "same", status: "NotReady", cpu: { used: 3, capacity: 4 } }, { name: "unknown" }] } },
];

describe("NodeInventory", () => {
  it("requests all nodes by default and keeps duplicate names in separate rows", async () => {
    const requests: Array<string | null> = [];
    server.use(http.get("/fleet/inventory", ({ request }) => { requests.push(new URL(request.url).searchParams.get("cluster")); return HttpResponse.json(result(items)); }));
    renderWithQuery(<NodeInventory />);
    expect(await screen.findAllByText("same")).toHaveLength(2);
    expect(requests).toEqual([null]);
    const rows = screen.getAllByRole("row");
    expect(rows.some((row) => row.textContent?.includes("East") && row.textContent.includes("same"))).toBe(true);
    expect(rows.some((row) => row.textContent?.includes("West") && row.textContent.includes("same"))).toBe(true);
  });
  it("requests the selected cluster and retains global scopes in the filter", async () => {
    const requests: Array<string | null> = [];
    server.use(http.get("/fleet/inventory", ({ request }) => { const cluster = new URL(request.url).searchParams.get("cluster"); requests.push(cluster); return HttpResponse.json(result(cluster ? [items[1]] : [], { partial: !cluster })); }));
    renderWithQuery(<NodeInventory clusterId="west" onClusterChange={() => {}} />);
    await screen.findByText("same");
    expect(requests).toContain("west");
    await userEvent.click(screen.getByRole("button", { name: "Filter by cluster" }));
    expect(await screen.findByRole("option", { name: "east" })).toBeInTheDocument();
  });
  it("combines search and status filters and sorts the table", async () => {
    server.use(http.get("/fleet/inventory", () => HttpResponse.json(result(items))));
    renderWithQuery(<NodeInventory />);
    await screen.findAllByText("same");
    await userEvent.click(screen.getByRole("button", { name: "Sort by CPU" }));
    await userEvent.click(screen.getByRole("button", { name: "Sort by CPU" }));
    expect(within(screen.getAllByRole("row")[1]).getByText("West")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Search nodes" }), "east");
    expect(screen.getAllByText("same")).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "Filter by status" }));
    await userEvent.click(await screen.findByRole("option", { name: "Not ready" }));
    expect(await screen.findByText("No nodes match these filters.")).toBeInTheDocument();
  });
  it("does not report unavailable inventory as a healthy empty fleet", async () => {
    server.use(http.get("/fleet/inventory", () => HttpResponse.json(result([], { partial: true, issues: [{ cluster: "west", code: "unavailable", message: "Inventory unavailable." }] }))));
    renderWithQuery(<NodeInventory />);
    expect(await screen.findByText("Node results are partial.")).toBeInTheDocument();
    expect(screen.getByText("No node data is available for this scope.")).toBeInTheDocument();
    expect(screen.queryByText("0 / 0")).not.toBeInTheDocument();
  });
  it("shows unknown measurements separately from measured zero", async () => {
    server.use(http.get("/fleet/inventory", () => HttpResponse.json(result(items))));
    renderWithQuery(<NodeInventory embedded clusterId="east" />);
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(2));
    const row = screen.getAllByRole("row")[1];
    expect(within(row).getByText("0%")).toBeInTheDocument();
    expect(within(row).getAllByText("—").length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByRole("button", { name: "Filter by cluster" })).not.toBeInTheDocument();
  });
  it("does not paint a late local response over explicitly selected remote inventory", async () => {
    let release: () => void = () => {};
    let localRequested = false;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    server.use(http.get("/fleet/inventory", async ({ request }) => {
      const cluster = new URL(request.url).searchParams.get("cluster") || "local";
      if (cluster === "local") { localRequested = true; await pending; }
      return HttpResponse.json(result([{ cluster, name: cluster, stats: {}, view: { nodes: [{ name: `${cluster}-node`, status: "Ready" }] } }]));
    }));
    const { client, rerender } = renderWithQuery(<NodeInventory embedded clusterId="local" />);
    await waitFor(() => expect(localRequested).toBe(true));
    rerender(<QueryClientProvider client={client}><NodeInventory embedded clusterId="remote" /></QueryClientProvider>);
    await screen.findByText("remote-node");
    await act(async () => { release(); await pending; });
    expect(screen.queryByText("local-node")).not.toBeInTheDocument();
    expect(client.getQueryData(["fleet", "inventory", "remote"])).toMatchObject({ items: [{ cluster: "remote" }] });
  });
  it("preserves node roles, labels, uptime and status without requiring optional fields", async () => {
    server.use(http.get("/fleet/inventory", () => HttpResponse.json(result([{ cluster: "east", name: "East", stats: {}, view: { nodes: [
      { name: "master-1", status: "Ready", roles: ["master", "control-plane"], labels: ["zone=north"], uptime: "45d 3h 22m" },
      { name: "worker-1", status: "NotReady" }, { name: "unknown-node" },
    ] } }]))));
    renderWithQuery(<NodeInventory embedded clusterId="east" />);
    await screen.findByText("master-1");
    expect(screen.getByText("master, control-plane")).toBeInTheDocument();
    expect(screen.getByText("zone=north")).toBeInTheDocument();
    expect(screen.getByText("45d 3h 22m")).toBeInTheDocument();
    expect(screen.getByText("Not ready")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(4);
  });
  it("reports complete emptiness separately from errors and retries inventory loading", async () => {
    let failed = true;
    server.use(http.get("/fleet/inventory", () => failed ? new HttpResponse("failed", { status: 500 }) : HttpResponse.json(result([]))));
    renderWithQuery(<NodeInventory embedded clusterId="east" />);
    await screen.findByText("Node results are unavailable. Try again.");
    expect(screen.queryByText("No nodes are reporting in this scope.")).not.toBeInTheDocument();
    failed = false;
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No nodes are reporting in this scope.")).toBeInTheDocument();
  });
  it("summarizes the visible rows instead of trusting unfiltered cluster counts", async () => {
    server.use(http.get("/fleet/inventory", () => HttpResponse.json(result(items))));
    renderWithQuery(<NodeInventory />);
    await screen.findAllByText("same");
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Search nodes" }), "east");
    expect(screen.getByText("1 / 1")).toBeInTheDocument();
  });
});
