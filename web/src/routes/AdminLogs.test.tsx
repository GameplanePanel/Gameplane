import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AdminLogsPage, capBuffer } from "./AdminLogs";

// Stream behavior tests use the combined-installation capability; standalone
// capability loading and absence of log requests are covered in Standalone.test.
vi.mock("@/lib/useInstallation", () => ({
  useInstallation: () => ({ data: { standalone: false, localCluster: true }, isPending: false, isError: false }),
}));

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

// Each call needs a fresh Response: a body stream can only be read once.
function logRes(body: string, pod: string): Response {
  return new Response(body, {
    status: 200,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "X-Gameplane-Pod": pod,
    },
  });
}

// pushableRes returns a Response whose body stays open so a test can feed
// chunks over time — needed to observe behavior between chunks (the
// single-string logRes stream ends before the first assertion runs).
function pushableRes(pod: string): {
  res: Response;
  push: (s: string) => void;
  close: () => void;
} {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c;
    },
  });
  const enc = new TextEncoder();
  return {
    res: new Response(stream, {
      status: 200,
      headers: {
        "Content-Type": "text/plain; charset=utf-8",
        "X-Gameplane-Pod": pod,
      },
    }),
    push: (s: string) => controller.enqueue(enc.encode(s)),
    close: () => controller.close(),
  };
}

function calledURLs(): string[] {
  return fetchMock.mock.calls.map((c) => String(c[0]));
}

describe("AdminLogsPage", () => {
  it("renders controls and streams the api log by default", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(logRes("api line one\napi line two\n", "gameplane-api-0")),
    );
    render(<AdminLogsPage />);

    expect(screen.getByRole("tab", { name: /api server/i })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: /operator/i })).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Follow" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /download/i })).toBeInTheDocument();

    expect(await screen.findByText(/api line one/)).toBeInTheDocument();
    expect(screen.getByText("gameplane-api-0")).toBeInTheDocument();

    const url = calledURLs()[0];
    expect(url).toContain("/admin/system-logs/api?");
    expect(url).toContain("tailLines=500");
    expect(url).toContain("follow=false");
  });

  it("switching component starts a new stream", async () => {
    fetchMock.mockImplementation((input: RequestInfo | URL) =>
      Promise.resolve(
        String(input).includes("/operator")
          ? logRes("operator says hi\n", "gameplane-operator-0")
          : logRes("api says hi\n", "gameplane-api-0"),
      ),
    );
    render(<AdminLogsPage />);
    await screen.findByText(/api says hi/);

    fireEvent.click(screen.getByText("Operator"));

    expect(await screen.findByText(/operator says hi/)).toBeInTheDocument();
    expect(screen.getByText("gameplane-operator-0")).toBeInTheDocument();
    expect(calledURLs().some((u) => u.includes("/admin/system-logs/operator?"))).toBe(true);
    // The old scrollback is cleared on switch.
    expect(screen.queryByText(/api says hi/)).toBeNull();
  });

  it("changing tail refetches with the new tailLines", async () => {
    const user = userEvent.setup();
    fetchMock.mockImplementation(() =>
      Promise.resolve(logRes("some output\n", "gameplane-api-0")),
    );
    render(<AdminLogsPage />);
    await screen.findByText(/some output/);

    // Find and click the Select trigger (initially showing "500 lines")
    await user.click(screen.getByRole("button", { name: /tail lines/i }));

    // Find and click the 1000 lines option
    await user.click(await screen.findByRole("option", { name: "1000 lines" }));

    await waitFor(() =>
      expect(calledURLs().some((u) => u.includes("tailLines=1000"))).toBe(true),
    );
  });

  it("toggling follow reconnects with follow=true", async () => {
    const user = userEvent.setup();
    fetchMock.mockImplementation(() =>
      Promise.resolve(logRes("streamed line\n", "gameplane-api-0")),
    );
    render(<AdminLogsPage />);
    await screen.findByText(/streamed line/);

    await user.click(screen.getByRole("switch", { name: "Follow" }));

    await waitFor(() =>
      expect(calledURLs().some((u) => u.includes("follow=true"))).toBe(true),
    );
  });

  it("shows an error when the stream fails", async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(new Response("no pods found", { status: 404 })),
    );
    render(<AdminLogsPage />);
    expect(await screen.findByText(/404: no pods found/)).toBeInTheDocument();
    expect(screen.getByText("No output.")).toBeInTheDocument();
  });

  it("downloads the current tail as a .log file", async () => {
    const createURL = vi.fn(() => "blob:test");
    const revokeURL = vi.fn();
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true, writable: true, value: createURL,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true, writable: true, value: revokeURL,
    });
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);

    fetchMock.mockImplementation(() =>
      Promise.resolve(logRes("dump line\n", "gameplane-api-0")),
    );
    render(<AdminLogsPage />);
    await screen.findByText(/dump line/);

    fireEvent.click(screen.getByRole("button", { name: /download/i }));

    await waitFor(() => expect(createURL).toHaveBeenCalledOnce());
    expect(clickSpy).toHaveBeenCalledOnce();
    expect(revokeURL).toHaveBeenCalledWith("blob:test");
    // The download re-fetches without follow, pinned to the streamed pod.
    const url = calledURLs()[fetchMock.mock.calls.length - 1];
    expect(url).toContain("follow=false");
    expect(url).toContain("pod=gameplane-api-0");
    clickSpy.mockRestore();
  });

  it("shows an error when the download fetch fails", async () => {
    fetchMock
      .mockImplementationOnce(() =>
        Promise.resolve(logRes("ok line\n", "gameplane-api-0")),
      )
      .mockImplementationOnce(() => Promise.reject(new Error("network down")));
    render(<AdminLogsPage />);
    await screen.findByText(/ok line/);

    fireEvent.click(screen.getByRole("button", { name: /download/i }));

    expect(await screen.findByText("network down")).toBeInTheDocument();
  });

  it("parses the timestamp from slog's `time` key", async () => {
    const line = JSON.stringify({
      time: "2026-09-26T00:00:00Z",
      level: "INFO",
      msg: "api started",
    });
    fetchMock.mockImplementation(() =>
      Promise.resolve(logRes(`${line}\n`, "gameplane-api-0")),
    );
    render(<AdminLogsPage />);

    expect(await screen.findByText(/api started/)).toBeInTheDocument();
    expect(screen.getByText("2026-09-26T00:00:00Z")).toBeInTheDocument();
    // The timestamp is not also rendered among the trailing structured fields.
    expect(screen.queryByText(/time=/)).toBeNull();
  });

  it("stops auto-scrolling when the user scrolls up and resumes at the bottom", async () => {
    const stream = pushableRes("gameplane-api-0");
    fetchMock.mockImplementation(() => Promise.resolve(stream.res));
    render(<AdminLogsPage />);

    stream.push("first chunk\n");
    await screen.findByText(/first chunk/);
    const scroller = screen.getByTestId("log-scroller") as HTMLDivElement;

    // Give the panel scrollable geometry (jsdom does no layout) and a
    // plain-value scrollTop so the component's writes are observable.
    Object.defineProperty(scroller, "scrollHeight", { configurable: true, value: 1000 });
    Object.defineProperty(scroller, "clientHeight", { configurable: true, value: 100 });
    Object.defineProperty(scroller, "scrollTop", {
      configurable: true, writable: true, value: 0,
    });

    // Scroll far above the bottom: auto-scroll disengages, so new output
    // must not move the viewport.
    scroller.scrollTop = 100;
    fireEvent.scroll(scroller);
    stream.push("second chunk\n");
    await screen.findByText(/second chunk/);
    expect(scroller.scrollTop).toBe(100);

    // Return to the bottom: auto-scroll re-engages and pins to the end.
    scroller.scrollTop = 900;
    fireEvent.scroll(scroller);
    stream.push("third chunk\n");
    await screen.findByText(/third chunk/);
    expect(scroller.scrollTop).toBe(1000);

    stream.close();
  });
});

describe("capBuffer", () => {
  it("returns strings under the cap unchanged", () => {
    expect(capBuffer("abc", 10)).toBe("abc");
  });

  it("trims the head to the cap at a line boundary", () => {
    expect(capBuffer("aaa\nbbb\nccc", 8)).toBe("bbb\nccc");
  });

  it("falls back to a plain cut when no newline is in range", () => {
    expect(capBuffer("abcdefghij", 4)).toBe("ghij");
  });
});
