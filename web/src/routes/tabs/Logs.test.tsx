import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Each call to openWS pushes a new fake socket onto sockets[]; tests
// reach into the latest one to drive messages/lifecycle and assert cleanup.
type StatusInfo = { attempt: number; nextRetryMs?: number };
type FakeSock = {
  path: string;
  close: ReturnType<typeof vi.fn>;
  send: ReturnType<typeof vi.fn>;
  sendMsg: (s: string) => void;
  open: () => void;
  closeEv: () => void;
  statusEv: (status: string, info: StatusInfo) => void;
};
const sockets: FakeSock[] = [];

vi.mock("@/lib/ws", () => ({
  openWS: vi.fn(
    (
      path: string,
      opts: {
        onMessage: (d: string) => void;
        onOpen?: () => void;
        onClose?: () => void;
        onStatus?: (status: string, info: StatusInfo) => void;
      },
    ) => {
      const sock: FakeSock = {
        path,
        close: vi.fn(),
        send: vi.fn(),
        sendMsg: opts.onMessage,
        open: () => opts.onOpen?.(),
        closeEv: () => opts.onClose?.(),
        statusEv: (status, info) => opts.onStatus?.(status, info),
      };
      sockets.push(sock);
      return { send: sock.send, close: sock.close };
    },
  ),
}));

import { setCurrentCluster } from "@/lib/cluster";
import { LogsTab, parseLogLevel } from "./Logs";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";

beforeEach(() => {
  setCurrentCluster("local");
  sockets.length = 0;
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("LogsTab", () => {
  it("opens earlier pages on a short viewport and keeps the live socket attached", async () => {
    const requests: string[] = [];
    server.use(http.get("/servers/alpha/logs/pod/history", ({ request }) => {
      const cursor = new URL(request.url).searchParams.get("before") ?? "";
      requests.push(cursor);
      return HttpResponse.json(cursor ? { text: "oldest row", source: "setup" } : { text: "recent row", source: "game", before: "stable-boundary" });
    }));
    render(<LogsTab name="alpha" />);
    await userEvent.click(screen.getByRole("button", { name: "Earlier output" }));
    expect(await screen.findByText("recent row")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    expect(await screen.findByText("oldest row")).toBeInTheDocument();
    expect(requests).toEqual(["", "stable-boundary"]);
    expect(sockets[0].close).not.toHaveBeenCalled();
  });
  it("resets file history on a source change and reports a rotated file", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    const queries: URL[] = [];
    server.use(
      http.get("/servers/alpha/logs/pod/history", async () => {
        await pending;
        return HttpResponse.json({ text: "stale pod response" });
      }),
      http.get("/servers/alpha/logs/history", ({ request }) => {
        const url = new URL(request.url);
        queries.push(url);
        return url.searchParams.get("before") ? new HttpResponse(null, { status: 409 }) :
          HttpResponse.json({ text: "file row", before: "file-cursor" });
      }),
    );
    render(<ResourceTargetProvider target={{ cluster: "remote", namespace: "games", name: "alpha", uid: "target" }}>
      <LogsTab name="alpha" ns="games" logPath="/data/server.log" />
    </ResourceTargetProvider>);
    await userEvent.click(screen.getByRole("button", { name: "Earlier output" }));
    await userEvent.click(screen.getByRole("tab", { name: /game log/i }));
    release();
    await userEvent.click(screen.getByRole("button", { name: "Earlier output" }));
    expect(await screen.findByText("file row")).toBeInTheDocument();
    expect(screen.queryByText("stale pod response")).not.toBeInTheDocument();
    expect(queries[0].searchParams.get("cluster")).toBe("remote");
    expect(queries[0].searchParams.get("namespace")).toBe("games");
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    expect(await screen.findByText(/earlier output expired/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Hide history" }));
    expect(screen.queryByText("file row")).not.toBeInTheDocument();
  });
  it("keeps filters independent and clears output when its resource route changes", async () => {
    const { unmount, rerender } = render(<LogsTab name="alpha" />);
    const old = sockets[0];
    act(() => old.sendMsg("old cluster line"));
    await waitFor(() => expect(screen.getByText(/1 lines/)).toBeInTheDocument());
    act(() => setCurrentCluster("remote-1"));
    expect(sockets).toHaveLength(1);
    expect(old.close).not.toHaveBeenCalled();
    rerender(<ResourceTargetProvider target={{ cluster: "remote-1", namespace: "games", name: "alpha", uid: "remote-instance" }}><LogsTab name="alpha" ns="games" /></ResourceTargetProvider>);
    await waitFor(() => expect(sockets).toHaveLength(2));
    const remote = new URL(sockets[1].path, "http://test");
    expect(remote.searchParams.get("cluster")).toBe("remote-1");
    expect(remote.searchParams.get("namespace")).toBe("games");
    expect(old.close).toHaveBeenCalled();
    expect(screen.queryByText(/1 lines/)).not.toBeInTheDocument();
    unmount();
    setCurrentCluster("local");
  });

  it("counts incoming lines", async () => {
    // jsdom doesn't lay out the virtualized rows so we assert against
    // the line counter rather than the row text content. The rendering
    // path (via useVirtualizer) is exercised in real browsers and e2e.
    render(<LogsTab name="alpha" />);
    sockets[0].sendMsg("first line\nsecond line\n");
    // Two newlines produce three split entries (first, second, "").
    await waitFor(() => expect(screen.getByText(/3 lines/)).toBeInTheDocument());
  });

  it("filters lines by user-typed substring", async () => {
    render(<LogsTab name="alpha" />);
    sockets[0].sendMsg("error: bad config\ninfo: starting up");
    await waitFor(() => expect(screen.getByText(/2 lines/)).toBeInTheDocument());
    const filter = screen.getByPlaceholderText(/filter/i);
    await userEvent.type(filter, "error");
    await waitFor(() => expect(screen.getByText(/1 lines/)).toBeInTheDocument());
  });

  it("filters lines by log level via the level pills", async () => {
    render(<LogsTab name="alpha" />);
    sockets[0].sendMsg(
      "[Server thread/INFO]: ok\n[Server thread/WARN]: hmm\n[Server thread/ERROR]: boom",
    );
    await waitFor(() => expect(screen.getByText(/3 lines/)).toBeInTheDocument());
    // The Error tab advertises its count and filters to just that level.
    const errorTab = screen.getByRole("tab", { name: /Error 1/ });
    await userEvent.click(errorTab);
    await waitFor(() => expect(screen.getByText(/1 lines/)).toBeInTheDocument());
  });

  it("download button navigates to the log download URL", async () => {
    render(<LogsTab name="alpha" />);
    // jsdom can't navigate; swap in a plain object so the href
    // assignment is observable instead of throwing "not implemented".
    const original = window.location;
    Object.defineProperty(window, "location", {
      writable: true,
      value: { href: "" },
    });
    try {
      await userEvent.click(screen.getByRole("button", { name: /download/i }));
      expect(window.location.href).toBe("/servers/alpha/logs/download");
    } finally {
      Object.defineProperty(window, "location", {
        writable: true,
        value: original,
      });
    }
  });

  it("closes the socket on unmount", async () => {
    const { unmount } = render(<LogsTab name="alpha" />);
    unmount();
    expect(sockets[0].close).toHaveBeenCalled();
  });

  it("reconnects when the server name changes", async () => {
    const { rerender } = render(<LogsTab name="alpha" />);
    rerender(<LogsTab name="beta" />);
    expect(sockets[0].close).toHaveBeenCalled();
    expect(sockets).toHaveLength(2);
  });

  it("defaults to the container-output (pod) stream so startup logs show", () => {
    render(<LogsTab name="alpha" />);
    expect(sockets[0].path).toBe("/ws/servers/alpha/logs/pod?from=start");
  });

  it("switches to the game-log file stream when toggled (template has a logPath)", async () => {
    render(<LogsTab name="alpha" logPath="/data/logs/latest.log" />);
    await userEvent.click(screen.getByRole("tab", { name: /game log/i }));
    expect(sockets[0].close).toHaveBeenCalled();
    expect(sockets[sockets.length - 1].path).toBe("/ws/servers/alpha/logs?tail=500");
  });

  it("offers only container output (no toggle) when the template has no logPath", () => {
    render(<LogsTab name="alpha" />);
    // The "Game log" tab is absent and only the pod stream is opened.
    expect(screen.queryByRole("tab", { name: /game log/i })).not.toBeInTheDocument();
    expect(sockets).toHaveLength(1);
    expect(sockets[0].path).toBe("/ws/servers/alpha/logs/pod?from=start");
  });

  it("shows the provisioning placeholder with the operator message while starting", () => {
    render(<LogsTab name="alpha" phase="Starting" progressMessage="pulling the game image" />);
    // Lowercase operator message is capitalized for display.
    expect(screen.getByText("Pulling the game image")).toBeInTheDocument();
    expect(screen.getByText(/install output appears here/i)).toBeInTheDocument();
  });

  it("shows a failure placeholder (no spinner) when the server has Failed", () => {
    render(
      <LogsTab name="alpha" phase="Failed" progressMessage="cannot pull the image — check the image reference" />,
    );
    expect(screen.getByText("Cannot pull the image — check the image reference")).toBeInTheDocument();
    expect(screen.getByText(/check the overview events/i)).toBeInTheDocument();
    // Not the "starting" install copy.
    expect(screen.queryByText(/install output appears here/i)).not.toBeInTheDocument();
  });

  it("hides the placeholder once log lines arrive", async () => {
    render(<LogsTab name="alpha" phase="Starting" progressMessage="installing server files" />);
    expect(screen.getByText("Installing server files")).toBeInTheDocument();
    sockets[0].sendMsg("Downloading server-1.21.jar…\n");
    await waitFor(() =>
      expect(screen.queryByText("Installing server files")).not.toBeInTheDocument(),
    );
  });

  it("shows a connecting message when running with no output yet", () => {
    render(<LogsTab name="alpha" phase="Running" />);
    expect(screen.getByText(/connecting to the log stream/i)).toBeInTheDocument();
  });

  it("switches to waiting-for-output once the socket opens while running", async () => {
    render(<LogsTab name="alpha" phase="Running" />);
    sockets[0].open();
    await waitFor(() => expect(screen.getByText(/waiting for output/i)).toBeInTheDocument());
  });

  it("offers container output when the game-log stream keeps failing", async () => {
    render(<LogsTab name="alpha" logPath="/data/logs/latest.log" phase="Running" />);
    // Switch from container output to the agent-backed game-log file stream.
    await userEvent.click(screen.getByRole("tab", { name: /game log/i }));
    const sock = sockets[sockets.length - 1];
    expect(sock.path).toBe("/ws/servers/alpha/logs?tail=500");
    // Repeated reconnect attempts mean the agent is unreachable: show the
    // actionable notice instead of an endless spinner.
    sock.statusEv("reconnecting", { attempt: 2, nextRetryMs: 2000 });
    await waitFor(() => expect(screen.getByText(/game log unavailable/i)).toBeInTheDocument());
    // The fallback returns to the always-available container output stream.
    await userEvent.click(screen.getByRole("button", { name: /use container output/i }));
    await waitFor(() =>
      expect(sockets[sockets.length - 1].path).toBe("/ws/servers/alpha/logs/pod?from=start"),
    );
  });
});

describe("parseLogLevel", () => {
  it.each([
    ["[Server thread/INFO]: started", "INFO"],
    ["[Server thread/WARN]: deprecated", "WARN"],
    ["[Server thread/ERROR]: crash", "ERROR"],
    ["[main/DEBUG]: verbose", "DEBUG"],
    ["2026-01-01 SEVERE something", "ERROR"],
    ["plain line with no level", null],
  ])("maps %s -> %s", (line, want) => {
    expect(parseLogLevel(line)).toBe(want);
  });
});
