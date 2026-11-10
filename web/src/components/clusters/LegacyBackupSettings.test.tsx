import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { setCurrentCluster } from "@/lib/cluster";
vi.mock("@tanstack/react-router", () => ({ Link: ({ children, to, params, search }: { children: ReactNode; to: string; params?: { clusterId: string }; search?: { tab: string } }) => <a href={to.replace("$clusterId", params?.clusterId ?? "") + (search ? `?tab=${search.tab}` : "")}>{children}</a> }));
import { LegacyBackupSettings } from "./LegacyBackupSettings";

afterEach(() => setCurrentCluster("local"));
describe("LegacyBackupSettings", () => {
  it("preserves the local bookmark destination on combined installations despite a remembered remote", async () => {
    setCurrentCluster("east");
    const reads = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "local" }, { name: "east", displayName: "East" }] })), http.get("/backup-destinations", reads));
    renderWithQuery(<LegacyBackupSettings />);
    expect(await screen.findByRole("link", { name: "Open local configuration" })).toHaveAttribute("href", "/clusters/local?tab=configuration");
    expect(reads).not.toHaveBeenCalled();
  });

  it("uses a valid remembered remote on standalone installations", async () => {
    setCurrentCluster("east");
    const reads = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/admin/installation", () => HttpResponse.json({ standalone: true, localCluster: false })), http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", displayName: "East" }] })), http.get("/backup-destinations", reads));
    renderWithQuery(<LegacyBackupSettings />);
    expect(await screen.findByRole("link", { name: "Open East configuration" })).toHaveAttribute("href", "/clusters/east?tab=configuration");
    expect(reads).not.toHaveBeenCalled();
  });

  it("does not substitute a remembered remote when local is unavailable on a combined installation", async () => {
    setCurrentCluster("east");
    server.use(http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", displayName: "East" }] })));
    renderWithQuery(<LegacyBackupSettings />);
    expect(await screen.findByRole("link", { name: "Choose a cluster" })).toHaveAttribute("href", "/clusters");
  });

  it.each(["", "local", "deleted"])("requests no workload for invalid standalone selection %s", async (selection) => {
    setCurrentCluster(selection);
    const reads = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(http.get("/admin/installation", () => HttpResponse.json({ standalone: true, localCluster: false })), http.get("/clusters", () => HttpResponse.json({ items: [] })), http.get("/backup-destinations", reads));
    renderWithQuery(<LegacyBackupSettings />);
    expect(await screen.findByRole("link", { name: "Choose a cluster" })).toHaveAttribute("href", "/clusters");
    expect(reads).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Add destination" })).not.toBeInTheDocument();
  });
});
