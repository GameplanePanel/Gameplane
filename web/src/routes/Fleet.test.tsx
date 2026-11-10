import type { ReactNode } from "react";
import { useTestLocation, navigateTestSearch, resetTestSearch } from "@/test/routerSearch";
import { afterEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeServer, makeSchedule } from "@/test/factories";
import { setCurrentCluster } from "@/lib/cluster";
import type { FleetItem, FleetResult } from "@/lib/fleet";
import type { GameServer } from "@/types";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, params, search }: { children: ReactNode; to: string; params?: { name: string }; search?: Record<string, string> }) =>
    <a href={`${params ? to.replace("$name", params.name) : to}${search ? `?${new URLSearchParams(search)}` : ""}`}>{children}</a>,
  useLocation: () => useTestLocation(),
  useNavigate: () => navigateTestSearch,
  useSearch: () => ({}), useParams: () => ({}),
}));

import { ServersPage } from "./Servers";
import { DashboardPage } from "./Dashboard";
import { BackupsPage } from "./Backups";

function item(cluster: string, namespace: string, canControl = false): FleetItem<GameServer> {
  return {
    target: { cluster, namespace, name: "same", uid: `${cluster}-${namespace}` },
    resource: makeServer({ metadata: { name: "same", namespace }, status: { phase: "Stopped" } }),
    permissions: canControl ? ["servers:read", "servers:write"] : ["servers:read"],
    access: { canWrite: canControl, canControl, canConsole: false, canDelete: canControl, isOwner: false, isCollaborator: false },
  };
}

function result<T>(items: T[], extra: Partial<FleetResult<T>> = {}): FleetResult<T> {
  return { items, partial: false, issues: [], totalReturned: items.length, ...extra };
}

afterEach(() => { resetTestSearch(); setCurrentCluster("local"); window.history.replaceState(null, "", "/"); });

describe("unified fleet pages", () => {
  it("keeps duplicate names distinct and binds row actions to their originating site", async () => {
    const requests: URL[] = [];
    server.use(
      http.get("/fleet/servers", () => HttpResponse.json(result([item("local", "games"), item("remote", "games", true), item("remote", "other")]))),
      http.post("/servers/same:start", ({ request }) => { requests.push(new URL(request.url)); return new HttpResponse(null, { status: 202 }); }),
    );
    setCurrentCluster("unrelated-stored-selection");
    renderWithQuery(<ServersPage />);
    const links = await screen.findAllByRole("link", { name: "same" });
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "/servers/same?cluster=local&ns=games", "/servers/same?cluster=remote&ns=games", "/servers/same?cluster=remote&ns=other",
    ]);
    const rows = screen.getAllByRole("row");
    const local = rows.find((row) => row.textContent?.includes("local / games"))!;
    const remote = rows.find((row) => row.textContent?.includes("remote / games"))!;
    expect(within(local).getByRole("button", { name: "Start" })).toBeDisabled();
    await userEvent.click(within(remote).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0].searchParams.get("cluster")).toBe("remote");
    expect(requests[0].searchParams.get("namespace")).toBe("games");
  });

  it("sends the selected location to the API so a truncated global list can be narrowed", async () => {
    const requested: Array<string | null> = [];
    server.use(http.get("/fleet/servers", ({ request }) => {
      const cluster = new URL(request.url).searchParams.get("cluster");
      requested.push(cluster);
      return HttpResponse.json(cluster === "remote" ? result([item("remote", "games")]) : result([item("local", "games")], {
        partial: true, issues: [{ cluster: "remote", code: "limit", message: "Result limit reached. Narrow the filter." }],
        scopes: [{ cluster: "local", namespace: "games" }, { cluster: "remote", namespace: "games" }],
      }));
    }));
    renderWithQuery(<ServersPage />);
    await screen.findByText(/Server results are partial/);
    expect(screen.queryByRole("button", { name: /Filter by cluster/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    await userEvent.click(screen.getByRole("button", { name: /Filter by cluster/ }));
    await userEvent.click(await screen.findByRole("option", { name: "remote" }));
    expect(requested).not.toContain("remote");
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(requested).toContain("remote"));
    await waitFor(() => expect(screen.getByRole("link", { name: "same" })).toHaveAttribute("href", "/servers/same?cluster=remote&ns=games"));
  });

  it("does not report an unavailable fleet as zero healthy servers", async () => {
    server.use(
      http.get("/fleet/servers", () => HttpResponse.json(result([], { partial: true, issues: [{ cluster: "remote", code: "unavailable", message: "Location unavailable." }] }))),
      http.get("/fleet/inventory", () => HttpResponse.json(result([]))),
    );
    renderWithQuery(<DashboardPage />);
    expect(await screen.findByText("server status unavailable")).toBeInTheDocument();
    expect(screen.getByText("player status unavailable")).toBeInTheDocument();
    expect(screen.queryByText(/Everything looks healthy/)).not.toBeInTheDocument();
    const running = screen.getByText("Running").closest("div.card")!;
    expect(within(running as HTMLElement).getByText("—")).toBeInTheDocument();
  });
  it("keeps schedule mutations in the originating scope and disables read-only rows", async () => {
    const requests: URL[] = [];
    const schedule = makeSchedule({ metadata: { name: "same-schedule", namespace: "games" } });
    server.use(
      http.get("/fleet/schedules", () => HttpResponse.json(result(["local", "remote"].map((cluster) => ({
        target: { cluster, namespace: "games", name: "same-schedule", uid: `${cluster}-schedule` }, resource: schedule,
        permissions: cluster === "remote" ? ["schedules:read", "schedules:write"] : ["schedules:read"],
      }))))),
      http.get("/schedules/same-schedule", ({ request }) => { requests.push(new URL(request.url)); return HttpResponse.json(schedule); }),
      http.put("/schedules/same-schedule", async ({ request }) => { requests.push(new URL(request.url)); return HttpResponse.json(await request.json()); }),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("tab", { name: "Schedules" }));
    await screen.findAllByText("same-schedule");
    const rows = screen.getAllByRole("row");
    const local = rows.find((row) => row.textContent?.includes("local / games"))!;
    const remote = rows.find((row) => row.textContent?.includes("remote / games"))!;
    expect(within(local).getByRole("switch", { name: "Schedule active" })).toBeDisabled();
    await userEvent.click(within(remote).getByRole("switch", { name: "Schedule active" }));
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests.every((url) => url.searchParams.get("cluster") === "remote" && url.searchParams.get("namespace") === "games")).toBe(true);
  });

});
