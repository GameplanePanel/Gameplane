import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithQuery } from "@/test/render";
import { OverviewTab } from "./Overview";
import type { GameServer, PlayersResp } from "@/types";

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});


function gs(overrides: Partial<GameServer["status"]> = {}): GameServer {
  return {
    metadata: { name: "s1" },
    spec: { templateRef: { name: "minecraft-java" } },
    status: { phase: "Running", ...overrides },
  };
}

function jsonRes(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

describe("OverviewTab recent events", () => {
  it("renders mapped Kubernetes events including a Warning", async () => {
    fetchMock.mockImplementation((path: string) => {
      if (String(path).includes("/events")) {
        return Promise.resolve(jsonRes([
          {
            id: "e1", time: "2026-01-01T00:00:00Z", type: "Warning",
            reason: "Failed", message: "Back-off pulling image",
            source: "kubelet", object: "Pod/s1-0", count: 3,
          },
          {
            id: "e2", time: "2026-01-01T00:00:00Z", type: "Normal",
            reason: "Pulling", message: "pulling image itzg/minecraft",
            source: "kubelet", object: "Pod/s1-0", count: 1,
          },
        ]));
      }
      return Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" }));
    });
    renderWithQuery(<OverviewTab gs={gs({ phase: "Starting" })} name="s1" />);
    expect(await screen.findByText("Failed: Back-off pulling image")).toBeInTheDocument();
    expect(screen.getByText("Pulling: pulling image itzg/minecraft")).toBeInTheDocument();
  });
});

describe("OverviewTab metric tiles", () => {
  it("shows measured bytes and the requested claim size when capacity is unknown", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonRes([])));
    const server = gs({ phase: "Starting", agent: { lastHeartbeat: "now", diskUsedBytes: 0, diskTotalBytes: null } });
    server.spec.storage = { size: "10Gi" };
    renderWithQuery(<OverviewTab name="s1" gs={server} />);
    expect(await screen.findByText("10Gi requested")).toBeInTheDocument();
    expect(screen.getByText("0 B")).toBeInTheDocument();
  });

  it("renders real CPU, memory and disk usage from the heartbeat", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" })));
    renderWithQuery(<OverviewTab name="s1" gs={gs({
      agent: {
        lastHeartbeat: "now",
        cpuMillicores: 500, cpuLimitMillicores: 2000, // 25%
        memoryBytes: 536870912, memoryLimitBytes: 1073741824, // 512 MB / 1.0 GB = 50%
        diskUsedBytes: 2147483648, diskTotalBytes: 10737418240, // 2.0 GB / 10 GB = 20%
      },
    })} />);
    expect(await screen.findByText("Disk")).toBeInTheDocument();
    expect(screen.getByText("25%")).toBeInTheDocument(); // CPU 500/2000
    expect(screen.getByText("0.5 / 2.0 cores")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    // formatBytes gives one decimal below 10 units: 1 GiB → "1.0 GB".
    expect(screen.getByText("512 MB / 1.0 GB")).toBeInTheDocument();
    expect(screen.getByText("20%")).toBeInTheDocument();
    expect(screen.getByText("2.0 GB / 10 GB")).toBeInTheDocument();
    // No Network tile (no stock-K8s per-pod source), and Players lives in
    // the sidebar card, not the metric row.
    expect(screen.queryByText("Network")).toBeNull();
  });

  it("renders '—' for usage when the agent heartbeat is stale/absent", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" })));
    // A stale heartbeat: the API drops the usage readings and flags stale,
    // so the tiles must render "—" rather than a frozen value or NaN.
    renderWithQuery(<OverviewTab name="s1" gs={gs({
      agent: { lastHeartbeat: "old", stale: true },
    })} />);
    expect(await screen.findByText("Disk")).toBeInTheDocument();
    // CPU, Memory and Disk all unknown → at least three em-dashes.
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(3);
  });

  it("shows cores/bytes and '—' limits when usage is known but limits are absent", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" })));
    // phase Starting disables the roster query, so the Players card falls
    // back to status.agent.playersOnline. Usage values are set but the
    // limits/total are absent/0, exercising the no-limit tile branches.
    renderWithQuery(<OverviewTab name="s1" gs={gs({
      phase: "Starting",
      agent: {
        lastHeartbeat: "now",
        cpuMillicores: 1500,                          // no limit → "1.50 cores", no bar
        memoryBytes: 536870912,                       // no limit → "512 MB" primary + "512 MB / —"
        diskUsedBytes: 1073741824, diskTotalBytes: null, // A directory scan observes usage only.
        playersOnline: 3,                             // exercises the players fallback branch
      },
    })} />);
    expect(await screen.findByText("1.50 cores")).toBeInTheDocument();
    expect(screen.getByText("512 MB")).toBeInTheDocument();
    expect(screen.getByText("512 MB / —")).toBeInTheDocument();
    expect(screen.getByText("1.0 GB")).toBeInTheDocument();
    expect(screen.queryByText("1.0 GB / —")).not.toBeInTheDocument();
    // Players fallback (phase != Running) feeds the sidebar card.
    expect(screen.getByText(/names not yet available/)).toBeInTheDocument();
  });
});

describe("OverviewTab players card", () => {
  it("shows 'No players connected.' when online is 0", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonRes({
      online: 0, max: 20, players: [], asOf: "now",
      capabilities: { kick: true, ban: true, unban: true },
    } satisfies PlayersResp)));
    renderWithQuery(<OverviewTab gs={gs()} name="s1" />);
    expect(await screen.findByText("No players connected.")).toBeInTheDocument();
  });

  it("shows '—' and 'Player count unknown.' when online is -1", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonRes({
      online: -1, max: -1, players: [], asOf: "now",
      capabilities: { kick: true, ban: true, unban: true },
    } satisfies PlayersResp)));
    renderWithQuery(<OverviewTab gs={gs()} name="s1" />);
    expect(await screen.findByText("Player count unknown.")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Players online · —/ }),
    ).toBeInTheDocument();
    expect(screen.queryByText("-1")).not.toBeInTheDocument();
  });

  it("renders real player names from the snapshot", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonRes({
      online: 2, max: 20, players: ["alice", "bob"], asOf: "now",
      capabilities: { kick: true, ban: true, unban: true },
    } satisfies PlayersResp)));
    renderWithQuery(<OverviewTab gs={gs()} name="s1" />);
    expect(await screen.findByText("alice")).toBeInTheDocument();
    expect(await screen.findByText("bob")).toBeInTheDocument();
    expect(screen.queryByText("player1")).toBeNull();
  });

  it("shows '+ N more' when there are more than 5 players", async () => {
    const players = ["a", "b", "c", "d", "e", "f", "g"];
    fetchMock.mockImplementation(() => Promise.resolve(jsonRes({
      online: players.length, max: 20, players, asOf: "now",
      capabilities: { kick: true, ban: true, unban: true },
    } satisfies PlayersResp)));
    renderWithQuery(<OverviewTab gs={gs()} name="s1" />);
    expect(await screen.findByText("+ 2 more")).toBeInTheDocument();
  });

  it("calls onViewAllEvents when the View all link is clicked", async () => {
    const onViewAllEvents = vi.fn();
    fetchMock.mockImplementation((path: string) => {
      if (String(path).includes("/events")) {
        return Promise.resolve(jsonRes([
          {
            id: "e1", time: "2026-01-01T00:00:00Z", type: "Normal",
            reason: "Created", message: "Pod created",
            source: "kubelet", object: "Pod/s1-0", count: 1,
          },
        ]));
      }
      return Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" }));
    });
    renderWithQuery(
      <OverviewTab gs={gs()} name="s1" onViewAllEvents={onViewAllEvents} />,
    );
    expect(await screen.findByText(/View all/i)).toBeInTheDocument();
  });
});

describe("OverviewTab endpoint rendering", () => {
  it("annotates the single address row with the pool it came from", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" })));
    renderWithQuery(<OverviewTab gs={gs({
      endpoints: [
        { name: "game", host: "172.18.255.203", port: 25565, pool: "pool-us-west" },
      ],
    })} name="s1" />);
    // The address appears exactly once — no duplicate external/cluster rows,
    // and no port suffix (design EZFW0: bare host + "pool: <name>" annotation).
    expect(await screen.findAllByText("172.18.255.203")).toHaveLength(1);
    expect(screen.getByText("pool: pool-us-west")).toBeInTheDocument();
    // No tunnel endpoint → no Tunnel row at all.
    expect(screen.queryByText("Tunnel")).not.toBeInTheDocument();
    // clusterEndpoint resolves to the same object as primary (no tunnel) →
    // Cluster Address is omitted rather than repeating the same value.
    expect(screen.queryByText("Cluster Address")).not.toBeInTheDocument();
  });

  it("renders the address cleanly when no pool is assigned", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" })));
    renderWithQuery(<OverviewTab gs={gs({
      endpoints: [
        { name: "game", host: "10.107.129.42", port: 30812 },
      ],
    })} name="s1" />);
    // SC-008: the address stays visible even without a pool…
    expect(await screen.findByText("10.107.129.42")).toBeInTheDocument();
    // …with no dangling pool label and no "undefined" leaking into the markup.
    expect(screen.queryByText(/pool:/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/undefined/i)).not.toBeInTheDocument();
    // Still no tunnel and no separate Cluster Address (same endpoint as primary).
    expect(screen.queryByText("Tunnel")).not.toBeInTheDocument();
    expect(screen.queryByText("Cluster Address")).not.toBeInTheDocument();
  });

  it("renders tunnel, external address, and cluster address rows for the bound endpoint", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonRes({ online: 0, max: 20, players: [], asOf: "now" })));
    // A tunnel endpoint plus a distinct cluster-native endpoint: primary is
    // the tunnel (operator prepends it), clusterEndpoint is the first
    // non-tunnel entry after it — a different object from primary, so all
    // three rows render (design EZFW0: separate Host/Port fields under
    // Cluster Address, not a combined "host:port" string).
    renderWithQuery(<OverviewTab gs={gs({
      endpoints: [
        { name: "tunnel", host: "mc.frp.gameplane.dev", port: 25565, tunnelProvider: "frp" },
        { name: "game", host: "10.107.129.42", port: 30812 },
      ],
    })} name="s1" />);
    expect(await screen.findByText("Tunnel")).toBeInTheDocument();
    expect(screen.getByText("mc.frp.gameplane.dev:25565")).toBeInTheDocument();
    expect(screen.getByText("External Address")).toBeInTheDocument();
    expect(screen.getByText("Cluster Address")).toBeInTheDocument();
    expect(screen.getByText("Host")).toBeInTheDocument();
    expect(screen.getByText("Port")).toBeInTheDocument();
    expect(screen.getByText("10.107.129.42")).toBeInTheDocument();
    expect(screen.getByText("30812")).toBeInTheDocument();
  });
});
