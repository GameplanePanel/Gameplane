import { QueryClientProvider } from "@tanstack/react-query";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeServer, makeTemplate } from "@/test/factories";

// xterm + WebSocket integration is exercised by e2e; in jsdom we mock
// xterm with instance capture (so tests can drive onData/onMessage/onStatus
// and inspect writes) and openWS with a recording stub.
type WSStatus = "connecting" | "open" | "reconnecting" | "closed";
type WSOpts = {
  onMessage?: (data: unknown) => void;
  onOpen?: () => void;
  onClose?: () => void;
  onStatus?: (status: WSStatus, info: { attempt: number; nextRetryMs?: number }) => void;
};

const mocks = vi.hoisted(() => {
  const terms: Array<{
    open: ReturnType<typeof vi.fn>;
    write: ReturnType<typeof vi.fn>;
    writeln: ReturnType<typeof vi.fn>;
    clear: ReturnType<typeof vi.fn>;
    onData: ReturnType<typeof vi.fn>;
    onResize: ReturnType<typeof vi.fn>;
    dispose: ReturnType<typeof vi.fn>;
    loadAddon: ReturnType<typeof vi.fn>;
    element: { isConnected: boolean };
    buffer: { active: { length: number; getLine: (i: number) => { translateToString: () => string } } };
    cols: number;
    rows: number;
  }> = [];
  const fits: Array<{ fit: ReturnType<typeof vi.fn> }> = [];
  const wsCalls: Array<{ path: string; opts: unknown }> = [];
  const wsHandle = { send: vi.fn(), close: vi.fn() };
  return { terms, fits, wsCalls, wsHandle };
});

// Vitest 4 tightened `new`-validation on vi.fn() mocks: a plain
// `vi.fn().mockImplementation(...)` used via `new Terminal(...)` now throws
// "is not a constructor" instead of silently working (it inspects the
// function's own shape, and a factory arrow fails that check even though
// mockImplementation's return value is what actually gets used for `new`
// in most mocking libs). A real `class` is a legitimate constructor, so it
// passes the check; the constructor body pushes the same recorded instance
// shape the tests already key off via `mocks.terms`/`mocks.fits`.
vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open = vi.fn();
    write = vi.fn();
    writeln = vi.fn();
    clear = vi.fn();
    onData = vi.fn(() => ({ dispose: vi.fn() }));
    onResize = vi.fn(() => ({ dispose: vi.fn() }));
    dispose = vi.fn();
    loadAddon = vi.fn();
    element = { isConnected: true };
    buffer = {
      active: { length: 2, getLine: (i: number) => ({ translateToString: () => `line ${i}` }) },
    };
    cols = 100;
    rows = 30;

    constructor() {
      mocks.terms.push(this);
    }
  },
}));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit = vi.fn();

    constructor() {
      mocks.fits.push(this);
    }
  },
}));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));
vi.mock("@/lib/ws", () => ({
  openWS: vi.fn((path: string, opts: unknown) => {
    mocks.wsCalls.push({ path, opts });
    return mocks.wsHandle;
  }),
}));

import { setCurrentCluster } from "@/lib/cluster";
import { ConsoleTab } from "./Console";

// Captured ResizeObserver callback (the hook constructs it with safeFit) so
// tests can fire a "resize" on demand and assert the B1 guards.
let capturedRoCb: (() => void) | null = null;

beforeEach(() => {
  setCurrentCluster("local");
  mocks.terms.length = 0;
  mocks.fits.length = 0;
  mocks.wsCalls.length = 0;
  mocks.wsHandle.send.mockClear();
  mocks.wsHandle.close.mockClear();
  capturedRoCb = null;
  // Run the deferred initial fit synchronously and capture resize callbacks
  // so the safe-fit guard is exercised deterministically in jsdom.
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
    cb(0);
    return 1;
  });
  vi.stubGlobal("cancelAnimationFrame", () => {});
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(cb: () => void) {
        capturedRoCb = cb;
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function renderConsole(mode: "rcon" | "pty") {
  const tmplName = mode === "pty" ? "terraria" : "minecraft-vanilla";
  server.use(
    http.get("/servers/alpha", () =>
      HttpResponse.json(makeServer({ spec: { templateRef: { name: tmplName } } })),
    ),
    http.get(`/templates/${tmplName}`, () =>
      HttpResponse.json(
        makeTemplate({
          spec: {
            ...makeTemplate().spec,
            consoleMode: mode,
            rcon: mode === "pty" ? undefined : makeTemplate().spec.rcon,
          },
        }),
      ),
    ),
  );
  const utils = renderWithQuery(<ConsoleTab name="alpha" />);
  const path = mode === "pty" ? "/ws/servers/alpha/console-pty" : "/ws/servers/alpha/console";
  await waitFor(() => expect(mocks.wsCalls.map((c) => c.path)).toContain(path));
  const call = mocks.wsCalls.find((c) => c.path === path);
  const opts = call?.opts as WSOpts;
  const term = mocks.terms[mocks.terms.length - 1];
  const fit = mocks.fits[mocks.fits.length - 1];
  return { ...utils, opts, term, fit };
}

describe("ConsoleTab", () => {
  it("attaches the live console while a pod history request is pending", async () => {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    server.use(http.get("/servers/alpha/logs/pod/history", async () => {
      await pending;
      return HttpResponse.json({ text: "pod snapshot" });
    }));
    const { term } = await renderConsole("pty");
    expect(mocks.wsCalls.length).toBeGreaterThan(0);
    release();
    expect(await screen.findByText("pod snapshot")).toBeInTheDocument();
    expect(term.write).not.toHaveBeenCalledWith(expect.stringContaining("pod snapshot"));
    expect(screen.getByText(/output may overlap or have gaps/i)).toBeInTheDocument();
  });
  it("keeps filters independent and disposes the old terminal when its resource route changes", async () => {
    const { term, unmount, rerender, client } = await renderConsole("pty");
    const calls = mocks.wsCalls.length;
    act(() => setCurrentCluster("remote-1"));
    expect(mocks.wsCalls).toHaveLength(calls);
    expect(term.dispose).not.toHaveBeenCalled();
    rerender(<QueryClientProvider client={client}><ResourceTargetProvider target={{ cluster: "remote-1", namespace: "games", name: "alpha", uid: "remote-instance" }}><ConsoleTab name="alpha" ns="games" /></ResourceTargetProvider></QueryClientProvider>);
    await waitFor(() => expect(mocks.wsCalls.length).toBeGreaterThan(calls));
    const remote = new URL(mocks.wsCalls.at(-1)!.path, "http://test");
    expect(remote.searchParams.get("cluster")).toBe("remote-1");
    expect(remote.searchParams.get("namespace")).toBe("games");
    expect(term.dispose).toHaveBeenCalled();
    expect(mocks.wsHandle.close).toHaveBeenCalled();
    unmount();
    setCurrentCluster("local");
  });

  it("renders an empty-state when the template has no console", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({ spec: { templateRef: { name: "noop" } } }),
        ),
      ),
      http.get("/templates/noop", () =>
        HttpResponse.json(makeTemplate({ spec: { ...makeTemplate().spec, consoleMode: "none", rcon: undefined } })),
      ),
    );
    renderWithQuery(<ConsoleTab name="alpha" />);
    expect(await screen.findByText("No console available")).toBeInTheDocument();
    expect(screen.getByText(/doesn't expose a console/i)).toBeInTheDocument();
  });

  it("shows loading until both server + template arrive", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(makeServer({ spec: { templateRef: { name: "minecraft-vanilla" } } })),
      ),
      http.get("/templates/minecraft-vanilla", async () => {
        await new Promise((r) => setTimeout(r, 50));
        return HttpResponse.json(makeTemplate({ spec: { ...makeTemplate().spec, consoleMode: "rcon" } }));
      }),
    );
    renderWithQuery(<ConsoleTab name="alpha" />);
    expect(screen.getByText(/Loading console/i)).toBeInTheDocument();
  });

  it("opens the PTY bridge when consoleMode is pty and frames stdin/stdout as base64", async () => {
    const { opts, term } = await renderConsole("pty");

    // Attaching sends an initial resize with the terminal's geometry.
    opts.onOpen?.();
    expect(mocks.wsHandle.send).toHaveBeenCalledWith(
      JSON.stringify({ kind: "resize", cols: 100, rows: 30 }),
    );

    // A geometry change ships a resize frame too.
    const onResize = term.onResize.mock.calls[0][0] as () => void;
    onResize();
    expect(mocks.wsHandle.send).toHaveBeenCalledWith(
      JSON.stringify({ kind: "resize", cols: 100, rows: 30 }),
    );

    // Keystrokes ship immediately as base64 stdin frames.
    const onData = term.onData.mock.calls[0][0] as (d: string) => void;
    onData("ls\n");
    expect(mocks.wsHandle.send).toHaveBeenCalledWith(
      JSON.stringify({ kind: "stdin", body: "bHMK" }), // base64("ls\n")
    );

    // stdout frames are base64-decoded into the terminal.
    opts.onMessage?.(JSON.stringify({ kind: "stdout", body: btoa("hello") }));
    expect(term.write).toHaveBeenCalledWith("hello");

    // err frames render as red text lines; malformed / bodiless / non-string
    // frames are dropped.
    opts.onMessage?.(JSON.stringify({ kind: "err", body: "attach torn down" }));
    expect(term.writeln).toHaveBeenCalledWith(expect.stringContaining("attach torn down"));
    opts.onMessage?.("not json");
    opts.onMessage?.(JSON.stringify({ kind: "stdout" })); // no body
    opts.onMessage?.(new ArrayBuffer(2)); // non-string frame
    expect(term.write).toHaveBeenCalledTimes(1);
  });

  it("opens the RCON socket when consoleMode is rcon", async () => {
    await renderConsole("rcon");
    expect(mocks.wsCalls.map((c) => c.path)).toContain("/ws/servers/alpha/console");
  });

  it("echoes RCON keystrokes and sends a command on Enter", async () => {
    const { term } = await renderConsole("rcon");
    const onData = term.onData.mock.calls[0][0] as (d: string) => void;
    onData("h"); // a printable char is echoed
    expect(term.write).toHaveBeenCalledWith("h");
    onData("\x7f"); // backspace with a non-empty buffer erases
    expect(term.write).toHaveBeenCalledWith("\b \b");
    onData("\x7f"); // backspace on an empty buffer is a no-op
    onData("ab\r"); // type then Enter flushes the line as a command
    expect(mocks.wsHandle.send).toHaveBeenCalledWith(JSON.stringify({ kind: "cmd", body: "ab" }));
  });

  it("writes RCON out/err frames and drops the rest", async () => {
    const { opts, term } = await renderConsole("rcon");
    opts.onMessage?.(JSON.stringify({ kind: "out", body: "pong" }));
    expect(term.writeln).toHaveBeenCalledWith("pong");
    opts.onMessage?.(JSON.stringify({ kind: "err", body: "boom" }));
    // Errors render in red so they read as failures, not command output.
    expect(term.writeln).toHaveBeenCalledWith("\x1b[31mboom\x1b[0m");
    opts.onMessage?.(JSON.stringify({ kind: "other", body: "x" })); // ignored
    opts.onMessage?.("not json"); // dropped
    opts.onMessage?.(new ArrayBuffer(2)); // non-string ignored
    expect(term.writeln).toHaveBeenCalledTimes(2);
  });

  it("safe-fits on mount and never fits a disposed terminal (B1)", async () => {
    const { unmount, fit } = await renderConsole("rcon");
    // The deferred initial fit ran once (host connected, not disposed).
    expect(fit.fit).toHaveBeenCalledTimes(1);
    // A resize that fires after teardown must be guarded out.
    unmount();
    capturedRoCb?.();
    expect(fit.fit).toHaveBeenCalledTimes(1);
  });

  it("does not fit when the host element is detached (B1)", async () => {
    const { term, fit } = await renderConsole("rcon");
    expect(fit.fit).toHaveBeenCalledTimes(1);
    term.element.isConnected = false;
    capturedRoCb?.();
    expect(fit.fit).toHaveBeenCalledTimes(1);
  });

  it("writes a status line only on connection transitions (B2)", async () => {
    const { opts, term } = await renderConsole("rcon");

    act(() => opts.onStatus?.("open", { attempt: 0 }));
    expect(term.writeln).toHaveBeenCalledWith(expect.stringContaining("— connected —"));

    act(() => opts.onStatus?.("reconnecting", { attempt: 1, nextRetryMs: 1000 }));
    expect(term.writeln).toHaveBeenCalledWith(expect.stringContaining("— disconnected —"));

    // A further reconnect while already disconnected must not spam the buffer.
    term.writeln.mockClear();
    act(() => opts.onStatus?.("reconnecting", { attempt: 2, nextRetryMs: 2000 }));
    expect(term.writeln).not.toHaveBeenCalled();

    // Recovery is a real edge and writes the connect line again.
    act(() => opts.onStatus?.("open", { attempt: 0 }));
    expect(term.writeln).toHaveBeenCalledWith(expect.stringContaining("— connected —"));
  });

  it("ignores status changes after teardown (B2)", async () => {
    const { opts, term, unmount } = await renderConsole("rcon");
    unmount();
    term.writeln.mockClear();
    act(() => opts.onStatus?.("open", { attempt: 0 }));
    expect(term.writeln).not.toHaveBeenCalled();
  });

  it("reflects connection state in the toolbar indicator", async () => {
    const { opts } = await renderConsole("rcon");
    expect(screen.getByText("connecting…")).toBeInTheDocument();
    act(() => opts.onStatus?.("open", { attempt: 0 }));
    expect(screen.getByText("LIVE")).toBeInTheDocument();
    act(() => opts.onStatus?.("reconnecting", { attempt: 1, nextRetryMs: 1000 }));
    expect(screen.getByText("reconnecting…")).toBeInTheDocument();
    act(() => opts.onStatus?.("closed", { attempt: 1 }));
    expect(screen.getByText("offline")).toBeInTheDocument();
  });

  it("clears the terminal from the toolbar", async () => {
    const { term } = await renderConsole("rcon");
    await userEvent.click(screen.getByRole("button", { name: /clear terminal/i }));
    expect(term.clear).toHaveBeenCalled();
  });

  it("downloads the terminal buffer as a log file", async () => {
    const createURL = vi.fn(() => "blob:test");
    const revokeURL = vi.fn();
    Object.defineProperty(URL, "createObjectURL", { configurable: true, writable: true, value: createURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, writable: true, value: revokeURL });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    await renderConsole("rcon");
    await userEvent.click(screen.getByRole("button", { name: /download terminal buffer/i }));
    expect(createURL).toHaveBeenCalledOnce();
    expect(clickSpy).toHaveBeenCalledOnce();
    expect(revokeURL).toHaveBeenCalledWith("blob:test");
    clickSpy.mockRestore();
  });

  it("toggles fullscreen on and off", async () => {
    const reqFs = vi.fn(() => Promise.resolve());
    const exitFs = vi.fn(() => Promise.resolve());
    Object.defineProperty(HTMLElement.prototype, "requestFullscreen", {
      configurable: true,
      writable: true,
      value: reqFs,
    });
    Object.defineProperty(document, "exitFullscreen", { configurable: true, value: exitFs });
    Object.defineProperty(document, "fullscreenElement", { configurable: true, value: null });
    await renderConsole("rcon");
    await userEvent.click(screen.getByRole("button", { name: /toggle fullscreen/i }));
    expect(reqFs).toHaveBeenCalled();
    // While in fullscreen, toggling exits.
    Object.defineProperty(document, "fullscreenElement", { configurable: true, value: document.body });
    await userEvent.click(screen.getByRole("button", { name: /toggle fullscreen/i }));
    expect(exitFs).toHaveBeenCalled();
  });

  it("sends an RCON command from the input bar", async () => {
    const { term } = await renderConsole("rcon");
    await userEvent.type(screen.getByPlaceholderText(/Type a command…/), "say hi");
    await userEvent.click(screen.getByRole("button", { name: /send/i }));
    expect(mocks.wsHandle.send).toHaveBeenCalledWith(JSON.stringify({ kind: "cmd", body: "say hi" }));
    expect(term.writeln).toHaveBeenCalledWith("say hi");
  });

  it("frames a PTY command as stdin with a trailing newline", async () => {
    await renderConsole("pty");
    await userEvent.type(screen.getByPlaceholderText(/Type a command…/), "ls");
    await userEvent.click(screen.getByRole("button", { name: /send/i }));
    expect(mocks.wsHandle.send).toHaveBeenCalledWith(
      JSON.stringify({ kind: "stdin", body: btoa("ls\n") }),
    );
  });

  it("ignores an empty command", async () => {
    await renderConsole("rcon");
    await userEvent.type(screen.getByPlaceholderText(/Type a command…/), "   ");
    await userEvent.click(screen.getByRole("button", { name: /send/i }));
    expect(mocks.wsHandle.send).not.toHaveBeenCalled();
  });

  it("recalls sent commands with ArrowUp and ArrowDown", async () => {
    await renderConsole("rcon");
    const input = screen.getByPlaceholderText(/Type a command…/);
    const send = screen.getByRole("button", { name: /send/i });
    await userEvent.type(input, "first");
    await userEvent.click(send);
    await userEvent.type(input, "second");
    await userEvent.click(send);
    await userEvent.type(input, "dra");
    await userEvent.keyboard("{ArrowUp}");
    expect(input).toHaveValue("second");
    await userEvent.keyboard("{ArrowUp}");
    expect(input).toHaveValue("first");
    await userEvent.keyboard("{ArrowUp}");
    expect(input).toHaveValue("first");
    await userEvent.keyboard("{ArrowDown}");
    expect(input).toHaveValue("second");
    await userEvent.keyboard("{ArrowDown}");
    expect(input).toHaveValue("dra");
  });
});
