import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse, delay } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import {
  makeServer,
  makeBackup,
  makeAudit,
  makeClusterView,
  makeClusterStats,
  makeUser,
} from "@/test/factories";

// TanStack Router's Link needs a router context the test doesn't supply.
// Replace it with a plain anchor — the attention/feed links keep the same
// DOM contract for what we assert.
const mockNavigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useLocation: () => ({ search: {} }),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
  useNavigate: () => mockNavigate,
}));

import { DashboardPage } from "./Dashboard";

describe("DashboardPage", () => {
  beforeEach(() => {
    mockNavigate.mockClear();
  });
  it("renders the overview shell: KPIs and section cards", async () => {
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Dashboard");
    // KPI tiles ("Storage" also appears as a cluster-resources meter label,
    // so assert at least one match rather than a unique one). Queries settle
    // asynchronously behind the loading gate, so wait for the first one.
    expect(await screen.findByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Players online")).toBeInTheDocument();
    expect(screen.getAllByText("Storage").length).toBeGreaterThan(0);
    expect(screen.getByText("Nodes ready")).toBeInTheDocument();
    // Section cards
    expect(screen.getByText("Fleet status")).toBeInTheDocument();
    expect(screen.getByText("Cluster resources")).toBeInTheDocument();
    expect(screen.getByText("Recent backups")).toBeInTheDocument();
    // Admin (default /users/me) has audit:read → activity card shown.
    expect(await screen.findByText("Recent activity")).toBeInTheDocument();
  });

  it("shows a loading state while queries are pending", () => {
    server.use(http.get("/servers", async () => { await delay("infinite"); }));
    renderWithQuery(<DashboardPage />);
    expect(screen.getByText("Loading dashboard…")).toBeInTheDocument();
  });

  it("surfaces servers needing attention and links to their detail", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "ok-srv" }, status: { phase: "Running" } }),
            makeServer({ metadata: { name: "broken-srv" }, status: { phase: "Failed" } }),
            makeServer({
              metadata: { name: "stale-srv" },
              status: { phase: "Running", agent: { stale: true } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<DashboardPage />);
    // "Needs attention" is a static header that renders before /servers
    // resolves, so wait on a data-dependent element (the attention-row link)
    // before the synchronous assertions, otherwise the fleet is still empty.
    const failedLink = await screen.findByRole("link", { name: /broken-srv/i });
    expect(failedLink).toHaveAttribute("href", "/servers/$name");
    expect(screen.getByText("Failed 1")).toBeInTheDocument();
    expect(screen.getByText(/Failed — check logs$/)).toBeInTheDocument();
    // Stale agent is flagged even though the phase is Running.
    expect(screen.getByRole("link", { name: /stale-srv/i })).toBeInTheDocument();
    expect(screen.getByText(/Agent heartbeat stale$/)).toBeInTheDocument();
  });

  it("shows a healthy state when nothing needs attention", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [makeServer({ metadata: { name: "happy" }, status: { phase: "Running" } })],
        }),
      ),
    );
    renderWithQuery(<DashboardPage />);
    expect(await screen.findByText(/Everything looks healthy/i)).toBeInTheDocument();
  });

  it("renders cluster resource meters and the node list", async () => {
    server.use(
      http.get("/cluster", () =>
        HttpResponse.json(
          makeClusterView({
            ready: 1,
            total: 1,
            nodes: [
              {
                name: "node-7",
                status: "Ready",
                cpu: { used: 4, capacity: 8 },
                memory: { used: 8_000_000_000, capacity: 16_000_000_000 },
                pods: { used: 9, capacity: 110 },
              },
            ],
          }),
        ),
      ),
      http.get("/cluster/stats", () =>
        HttpResponse.json(makeClusterStats({ usedStorageBytes: 780_000_000_000, totalStorageBytes: 1_000_000_000_000 })),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Cluster resources");
    expect(screen.getByText("CPU")).toBeInTheDocument();
    expect(screen.getByText("Memory")).toBeInTheDocument();
    // "Storage" appears as both a KPI tile and a meter — both are expected.
    expect(screen.getAllByText("Storage").length).toBeGreaterThan(0);
    expect(await screen.findByText("local / node-7")).toBeInTheDocument();
  });

  // Regression: nodes with no `used` reading (no metrics-server) used to
  // sum to a false 0%, indistinguishable from a genuinely idle cluster.
  it("shows unknown (—) cluster CPU/memory meters when no node reports `used`", async () => {
    server.use(
      http.get("/cluster", () =>
        HttpResponse.json(
          makeClusterView({
            ready: 1,
            total: 1,
            nodes: [{ name: "no-metrics-node", status: "Ready", cpu: { capacity: 8 }, memory: { capacity: 16_000_000_000 } }],
          }),
        ),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Cluster resources");
    await screen.findByText("local / no-metrics-node");
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(2);
  });

  // Regression: usedStorageBytes/totalStorageBytes are provisioned-vs-physical,
  // not used-vs-total — a networked-storage cluster can legitimately read
  // >100%, which must present as an explicit overcommit state, not a broken
  // meter.
  it("flags storage as overcommitted when provisioned exceeds physical capacity", async () => {
    server.use(
      http.get("/cluster/stats", () =>
        HttpResponse.json(makeClusterStats({ usedStorageBytes: 102_000_000_000, totalStorageBytes: 86_000_000_000 })),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Storage provisioned");
    expect(await screen.findAllByText(/overcommitted/i)).not.toHaveLength(0);
  });

  it("lists recent backups with their phase", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({
              metadata: { name: "valheim-snap" },
              spec: { serverRef: { name: "valheim-01" } },
              status: { phase: "Succeeded", startTime: "2026-05-07T03:00:00Z", size: "2.1 GiB" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Recent backups");
    expect(await screen.findByText("valheim-01")).toBeInTheDocument();
    expect(screen.getByText("2.1 GiB")).toBeInTheDocument();
  });

  it("humanizes audit events across action types", async () => {
    server.use(
      http.get("/admin/audit", () =>
        HttpResponse.json([
          makeAudit({ id: 1, actor: "ada", method: "POST", path: "/api/v1/servers/mc:start", target: "mc" }),
          makeAudit({ id: 2, actor: "ada", method: "POST", path: "/api/v1/servers/mc:stop", target: "mc" }),
          makeAudit({ id: 3, actor: "ada", method: "POST", path: "/api/v1/servers/mc:restart", target: "mc" }),
          makeAudit({ id: 4, actor: "ada", method: "POST", path: "/api/v1/backups", target: "mc" }),
          makeAudit({ id: 5, actor: "ada", method: "POST", path: "/api/v1/users", target: "liam" }),
          makeAudit({ id: 6, actor: "ada", method: "DELETE", path: "/api/v1/servers/old", target: "old" }),
          makeAudit({ id: 7, actor: "ada", method: "POST", path: "/api/v1/servers", target: "new" }),
          makeAudit({ id: 8, actor: "ada", method: "PUT", path: "/api/v1/servers/mc", target: "mc" }),
        ]),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("ada started mc");
    for (const text of [
      "ada stopped mc",
      "ada restarted mc",
      "ada backed up mc",
      "ada updated a user liam",
      "ada deleted old",
      "ada created new",
      "ada updated mc",
    ]) {
      expect(screen.getByText(text)).toBeInTheDocument();
    }
  });

  it("describes login audit rows as sign-ins", async () => {
    server.use(
      http.get("/admin/audit", () =>
        HttpResponse.json([
          makeAudit({ id: 1, actor: "ada", method: "POST", path: "/auth/login", status: 200 }),
          makeAudit({ id: 2, actor: "anonymous", method: "POST", path: "/auth/login", status: 401 }),
        ]),
      ),
    );
    renderWithQuery(<DashboardPage />);
    expect(await screen.findByText("ada signed in")).toBeInTheDocument();
    expect(screen.getByText("anonymous failed to sign in")).toBeInTheDocument();
  });

  it("renders empty states for activity and backups", async () => {
    server.use(
      http.get("/admin/audit", () => HttpResponse.json([])),
      http.get("/backups", () => HttpResponse.json({ items: [] })),
    );
    renderWithQuery(<DashboardPage />);
    expect(await screen.findByText("No recent activity.")).toBeInTheDocument();
    expect(screen.getByText("No backups yet.")).toBeInTheDocument();
  });

  it("handles missing cluster data without nodes", async () => {
    server.use(
      http.get("/cluster", () => HttpResponse.json({})),
      http.get("/cluster/stats", () => HttpResponse.json({})),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Cluster resources");
    // Nodes KPI tile falls back to "no node data" and the node list is omitted.
    expect(screen.getByText("no node data")).toBeInTheDocument();
  });

  it("renders gracefully when cluster and backup endpoints fail", async () => {
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [] })),
      http.get("/cluster", () => HttpResponse.error()),
      http.get("/cluster/stats", () => HttpResponse.error()),
      http.get("/backups", () => HttpResponse.error()),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Dashboard");
    // A successful empty server response remains distinct from failed inventory/backups.
    expect(await screen.findByText(/Everything looks healthy/i)).toBeInTheDocument();
    expect(await screen.findByText(/Backups are partial/)).toBeInTheDocument();
    expect(screen.queryByText("No backups yet.")).not.toBeInTheDocument();
  });

  it("does not turn a failed server request into a healthy empty cluster", async () => {
    server.use(http.get("/servers", () => HttpResponse.error()));
    renderWithQuery(<DashboardPage />);
    expect(await screen.findByText(/Server totals are partial/)).toBeInTheDocument();
    expect(await screen.findByText("server status unavailable")).toBeInTheDocument();
    expect(screen.getByText("player status unavailable")).toBeInTheDocument();
    expect(screen.queryByText(/Everything looks healthy/i)).not.toBeInTheDocument();
  });

  it("hides recent activity from users without audit:read", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))),
      http.get("/clusters", () => HttpResponse.json({ items: [{ name: "local", displayName: "local", phase: "Healthy", canViewInventory: false }] })),
    );
    const { client } = renderWithQuery(<DashboardPage />);
    // Wait until the viewer identity has actually loaded before asserting
    // the audit-gated card is absent (otherwise we'd pass during loading).
    await waitFor(() => expect(client.getQueryData(["me"])).toBeTruthy());
    await screen.findByText("Recent backups");
    expect(screen.queryByText("Recent activity")).not.toBeInTheDocument();
    // The registry explicitly denies node inventory for this server-only viewer.
    expect(screen.queryByRole("link", { name: /view nodes/i })).not.toBeInTheDocument();
  });

  it("shows the View nodes link with the registry's inventory capability", async () => {
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "operator" }))));
    const { client } = renderWithQuery(<DashboardPage />);
    await waitFor(() => expect(client.getQueryData(["me"])).toBeTruthy());
    await screen.findByText("Cluster resources");
    const viewNodesLink = screen.getByRole("link", { name: /view nodes/i });
    expect(viewNodesLink).toHaveAttribute("href", "/nodes");
  });

  it("renders vcpus as — when no node data is available", async () => {
    server.use(http.get("/cluster", () => HttpResponse.json({})));
    renderWithQuery(<DashboardPage />);
    const label = await screen.findByText("vCPUs");
    // Several stat cards fall back to the same "—" placeholder when their
    // data is missing, so scope the assertion to the vCPUs card itself.
    const card = label.closest("div.card");
    expect(card).not.toBeNull();
    expect(within(card as HTMLElement).getByText("—")).toBeInTheDocument();
  });

  it("renders nodes section only when nodes exist", async () => {
    server.use(http.get("/cluster", () => HttpResponse.json(makeClusterView({ nodes: [] }))));
    renderWithQuery(<DashboardPage />);
    await screen.findByText("Cluster resources");
    expect(screen.queryByText("Nodes")).not.toBeInTheDocument();
  });

  it("renders node status indicators correctly", async () => {
    server.use(
      http.get("/cluster", () =>
        HttpResponse.json(
          makeClusterView({
            nodes: [
              { name: "ready-node", status: "Ready", cpu: { capacity: 8 }, memory: { capacity: 16000000000 } },
              { name: "not-ready-node", status: "NotReady", cpu: { capacity: 4 }, memory: { capacity: 8000000000 } },
            ],
          }),
        ),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("local / ready-node");
    expect(screen.getByText("local / not-ready-node")).toBeInTheDocument();
  });

  it("handles partially missing node metrics gracefully", async () => {
    server.use(
      http.get("/cluster", () =>
        HttpResponse.json(
          makeClusterView({
            nodes: [
              { name: "partial-node", status: "Ready" }, // no cpu/memory/pods
            ],
          }),
        ),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("local / partial-node");
    // Multiple placeholders can render "—" for missing metrics (vCPUs card,
    // per-node cpu/mem cells); scope to the vCPUs stat card specifically,
    // since a node with no cpu.capacity keeps the cluster-wide vcpus sum
    // at zero.
    const label = screen.getByText("vCPUs");
    const card = label.closest("div.card");
    expect(card).not.toBeNull();
    expect(within(card as HTMLElement).getByText("—")).toBeInTheDocument();
  });

  it("sorts backups by startTime descending", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({
              metadata: { name: "backup-1" },
              spec: { serverRef: { name: "srv-1" } },
              status: { startTime: "2026-05-07T01:00:00Z" },
            }),
            makeBackup({
              metadata: { name: "backup-2" },
              spec: { serverRef: { name: "srv-2" } },
              status: { startTime: "2026-05-07T03:00:00Z" },
            }),
            makeBackup({
              metadata: { name: "backup-3" },
              spec: { serverRef: { name: "srv-3" } },
              status: { startTime: "2026-05-07T02:00:00Z" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<DashboardPage />);
    const rows = await screen.findAllByText(/srv-/);
    expect(rows[0]).toHaveTextContent("srv-2");
    expect(rows[1]).toHaveTextContent("srv-3");
    expect(rows[2]).toHaveTextContent("srv-1");
  });

  it("renders backup without size gracefully", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({
              metadata: { name: "no-size-backup" },
              spec: { serverRef: { name: "srv" } },
              status: { phase: "Pending", startTime: "2026-05-07T03:00:00Z" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<DashboardPage />);
    const backupName = await screen.findByText("srv");
    // Should not crash; size just doesn't render. Scope to this backup's
    // row — the unrelated Storage provisioned stat card legitimately
    // renders a "233 GB" / "3.7 GB / 15 GB" string elsewhere on the page,
    // which would otherwise false-match the same regex.
    const row = backupName.closest("div.flex.items-center.gap-3");
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).queryByText(/GiB|MiB|B$/)).not.toBeInTheDocument();
  });

  it("describes audit events for PATCH requests", async () => {
    server.use(
      http.get("/admin/audit", () =>
        HttpResponse.json([
          makeAudit({ id: 1, actor: "alice", method: "PATCH", path: "/api/v1/servers/mc", target: "mc" }),
        ]),
      ),
    );
    renderWithQuery(<DashboardPage />);
    await screen.findByText("alice updated mc");
  });

  it("renders audit event without target gracefully", async () => {
    server.use(
      http.get("/admin/audit", () =>
        HttpResponse.json([
          makeAudit({ id: 1, actor: "bob", method: "POST", path: "/api/v1/servers", target: undefined }),
        ]),
      ),
    );
    renderWithQuery(<DashboardPage />);
    expect(await screen.findByText(/bob created/)).toBeInTheDocument();
  });

  it("renders the Create server call to action linking to /servers/new", async () => {
    renderWithQuery(<DashboardPage />);
    const cta = await screen.findByRole("button", { name: /create server/i });
    expect(cta).toBeInTheDocument();
    await userEvent.click(cta);
    expect(mockNavigate).toHaveBeenCalledWith({ to: "/servers/new" });
  });

  it("operator can access the dashboard", async () => {
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "operator" }))));
    const { client } = renderWithQuery(<DashboardPage />);
    await waitFor(() => expect(client.getQueryData(["me"])).toBeTruthy());
    expect(await screen.findByText("Dashboard")).toBeInTheDocument();
  });

  it("viewer can access the dashboard", async () => {
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))));
    const { client } = renderWithQuery(<DashboardPage />);
    await waitFor(() => expect(client.getQueryData(["me"])).toBeTruthy());
    expect(await screen.findByText("Dashboard")).toBeInTheDocument();
  });
});
