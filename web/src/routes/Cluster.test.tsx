import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { setCurrentCluster } from "@/lib/cluster";
import { ClusterPage } from "./Cluster";

vi.mock("@tanstack/react-router", () => ({
  Navigate: ({ search }: { search: { cluster?: string } }) => <div>redirect:{search.cluster || "all"}</div>,
}));

describe("legacy cluster bookmark", () => {
  it("preserves an authorized remembered cluster", async () => {
    setCurrentCluster("east");
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", canViewInventory: true }] })));
    renderWithQuery(<ClusterPage />);
    expect(await screen.findByText("redirect:east")).toBeInTheDocument();
  });

  it("opens all nodes when standalone has no local cluster", async () => {
    setCurrentCluster("local");
    server.use(
      http.get("/admin/installation", () => HttpResponse.json({ standalone: true, localCluster: false })),
      http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", canViewInventory: true }] })),
    );
    renderWithQuery(<ClusterPage />);
    expect(await screen.findByText("redirect:all")).toBeInTheDocument();
  });

  it("discards an inaccessible remembered inventory scope", async () => {
    setCurrentCluster("east");
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", canViewInventory: false }] })));
    renderWithQuery(<ClusterPage />);
    expect(await screen.findByText("redirect:all")).toBeInTheDocument();
  });
});
