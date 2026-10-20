import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { SharePage } from "./Share";
import { Shares, APIError } from "@/lib/api";

const mockUseParams = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", () => ({
  Shares: {
    resolve: vi.fn(),
    start: vi.fn(),
  },
  APIError: class APIError extends Error {
    status: number;
    body: string;
    retryAfter: string | null;
    constructor(status: number, body: string, _statusText?: string, _contentType?: string, retryAfter: string | null = null) {
      super(`${status}: ${body}`);
      this.status = status;
      this.body = body;
      this.retryAfter = retryAfter;
    }
  },
}));

vi.mock("@tanstack/react-router", async () => {
  const actual = await vi.importActual("@tanstack/react-router");
  return {
    ...actual,
    useParams: mockUseParams,
  };
});

// Helper to render component with a mocked token parameter
function renderWithRouter(token: string) {
  mockUseParams.mockReturnValue({ token });
  return render(<SharePage />);
}

describe("SharePage", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mockUseParams.mockReturnValue({ token: "test-token" });
    // Clear localStorage
    localStorage.clear();
  });

  afterEach(() => {
    vi.clearAllTimers();
    // Guard against a fake-timer test failing/timing out before it reaches
    // its own vi.useRealTimers() — without this, real timers stay swapped
    // out and every later test in the file hangs on waitFor's internal poll.
    vi.useRealTimers();
  });

  describe("Loading state", () => {
    it("cancels an initial retry when navigating to another token", async () => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockRejectedValueOnce(new APIError(429, "private detail", undefined, undefined, "30"))
        .mockResolvedValue({ serverName: "new-server", status: "Running" });
      const { rerender } = renderWithRouter("old-token");
      await act(async () => {});
      const oldSignal = vi.mocked(Shares.resolve).mock.calls[0][1];
      mockUseParams.mockReturnValue({ token: "new-token" });
      rerender(<SharePage />);
      await act(async () => {});
      expect(oldSignal?.aborted).toBe(true);
      expect(screen.getByText("new-server")).toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      expect(vi.mocked(Shares.resolve).mock.calls[1][0]).toBe("new-token");
    });

    it("T181: renders loading spinner initially", async () => {
      vi.mocked(Shares.resolve).mockImplementation(
        () => new Promise(() => {}) // Never resolves
      );

      renderWithRouter("test-token");
      expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
    });
  });

  describe("Up state (T182)", () => {
    it("renders server name and Online status", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
        address: { host: "play.gameplane.example", port: 25565 },
        playersOnline: 3,
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
        expect(screen.getByText("Online")).toBeInTheDocument();
      });
    });

    it("displays address and port if exposed", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
        address: { host: "example.com", port: 8080 },
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("example.com:8080")).toBeInTheDocument();
      });
    });

    it("shows player count if available", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
        address: { host: "example.com", port: 8080 },
        playersOnline: 5,
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("5 players online")).toBeInTheDocument();
      });
    });

    it("hides address section if not exposed", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
        address: undefined,
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("Not exposed")).toBeInTheDocument();
      });
    });

    it("T182: respects FR-005 privacy (no cluster/namespace/version in response)", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
        address: { host: "play.gameplane.example", port: 25565 },
        playersOnline: 3,
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.queryByText(/cluster/i)).not.toBeInTheDocument();
        expect(screen.queryByText(/namespace/i)).not.toBeInTheDocument();
        expect(screen.queryByText(/v0\.|v1\.|v2\.|beta|alpha/i)).not.toBeInTheDocument();
      });
    });

    it("T182: copy address button copies address to clipboard", async () => {
      const mockClipboard = vi.fn().mockResolvedValue(undefined);
      Object.defineProperty(navigator, "clipboard", {
        value: { writeText: mockClipboard },
        configurable: true,
      });

      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
        address: { host: "example.com", port: 8080 },
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("example.com:8080")).toBeInTheDocument();
      });

      const copyBtn = screen.getByRole("button", { name: /copy/i });
      fireEvent.click(copyBtn);

      expect(mockClipboard).toHaveBeenCalledWith("example.com:8080");
    });
  });

  describe("Asleep states (T183)", () => {
    it("renders Asleep with Start button by default", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Suspended",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
        expect(screen.getByText("Asleep")).toBeInTheDocument();
        expect(
          screen.getByText(/This server is asleep to save resources/)
        ).toBeInTheDocument();
      });

      expect(screen.getByRole("button", { name: /start server/i })).toBeInTheDocument();
    });

    it("keeps Start available when an accepted wake is still asleep on the next poll", async () => {
      vi.useFakeTimers();

      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({
          serverName: "mc-survival",
          status: "Suspended",
        })
        .mockResolvedValueOnce({
          serverName: "mc-survival",
          status: "Suspended",
        });

      vi.mocked(Shares.start).mockResolvedValue();

      renderWithRouter("test-token");
      await act(async () => {});

      const startBtn = screen.getByRole("button", { name: /start server/i });
      await act(async () => {
        fireEvent.click(startBtn);
      });

      expect(vi.mocked(Shares.start)).toHaveBeenCalledWith("test-token", expect.any(AbortSignal));

      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();

      // The first poll must wait the full five seconds after Start completes.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4999);
      });
      expect(Shares.resolve).toHaveBeenCalledTimes(1);
      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1);
      });

      // A phase cannot establish whether a link has permission to start.
      expect(screen.getByRole("button", { name: /start server/i })).toBeEnabled();
      expect(screen.queryByText(/ask the server owner/)).not.toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60000);
      });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
    });

    it("handles Stopped status the same as Suspended", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Stopped",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByRole("button", { name: /start server/i })).toBeInTheDocument();
      });
    });

    it("T183: Start button calls Shares.start and transitions to Starting", async () => {
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({
          serverName: "mc-survival",
          status: "Suspended",
        })
        .mockResolvedValueOnce({
          serverName: "mc-survival",
          status: "Starting",
        });

      vi.mocked(Shares.start).mockResolvedValue();

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByRole("button", { name: /start server/i })).toBeInTheDocument();
      });

      const startBtn = screen.getByRole("button", { name: /start server/i });
      fireEvent.click(startBtn);

      expect(vi.mocked(Shares.start)).toHaveBeenCalledWith("test-token", expect.any(AbortSignal));

      await waitFor(() => {
        expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();
      });
    });
  });

  describe("Starting state with polling (T184)", () => {
    it("never overlaps a slow poll and waits five seconds after it finishes", async () => {
      vi.useFakeTimers();
      let finishPoll!: (value: Awaited<ReturnType<typeof Shares.resolve>>) => void;
      const slowPoll = new Promise<Awaited<ReturnType<typeof Shares.resolve>>>((resolve) => { finishPoll = resolve; });
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockImplementationOnce(() => slowPoll)
        .mockResolvedValue({ serverName: "survival", status: "Running" });
      renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(4999); });
      expect(Shares.resolve).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(30000); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      await act(async () => finishPoll({ serverName: "survival", status: "Starting" }));
      await act(async () => { await vi.advanceTimersByTimeAsync(4999); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it.each([new APIError(503, "internal secret"), new Error("private network details")])("keeps Starting through a transient failure and retries with backoff", async (failure) => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockRejectedValueOnce(failure)
        .mockResolvedValue({ serverName: "survival", status: "Running" });
      renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();
      expect(screen.queryByText("Link not available")).not.toBeInTheDocument();
      expect(screen.queryByText(/internal secret|private network details/)).not.toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(9999); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it.each([["30", 30000], ["Wed, 01 Jan 2025 00:00:35 GMT", 30000], ["1", 10000]] as const)("honors Retry-After %s without changing Starting to invalid", async (retryAfter, delay) => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date("2025-01-01T00:00:00Z"));
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockRejectedValueOnce(new APIError(429, "rate limit details", undefined, undefined, retryAfter))
        .mockResolvedValue({ serverName: "survival", status: "Running" });
      renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();
      expect(screen.queryByText(/rate limit details/)).not.toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(delay - 1); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it("caps exponential backoff and resets it after a successful Starting response", async () => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockRejectedValueOnce(new APIError(503, "temporary"))
        .mockRejectedValueOnce(new APIError(503, "temporary"))
        .mockRejectedValueOnce(new APIError(503, "temporary"))
        .mockRejectedValueOnce(new APIError(503, "temporary"))
        .mockRejectedValueOnce(new APIError(503, "temporary"))
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockResolvedValue({ serverName: "survival", status: "Running" });
      renderWithRouter("test-token");
      await act(async () => {});
      for (const delay of [5000, 10000, 20000, 40000, 60000, 60000]) {
        await act(async () => { await vi.advanceTimersByTimeAsync(delay); });
      }
      expect(Shares.resolve).toHaveBeenCalledTimes(7);
      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it("stops retrying on a neutral invalid response while polling", async () => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockResolvedValue({ serverName: "", status: "Unknown" });
      renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText("Link not available")).toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
    });

    it("uses neutral unavailable copy for a permanent auth failure during polling", async () => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockRejectedValue(new APIError(401, "private auth detail"));
      renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText("Link not available")).toBeInTheDocument();
      expect(screen.queryByText(/private auth detail/)).not.toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
    });

    it("ignores and aborts a pending old-token poll after navigation", async () => {
      vi.useFakeTimers();
      let finishOldPoll!: (value: Awaited<ReturnType<typeof Shares.resolve>>) => void;
      const oldPoll = new Promise<Awaited<ReturnType<typeof Shares.resolve>>>((resolve) => { finishOldPoll = resolve; });
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "old-server", status: "Starting" })
        .mockImplementationOnce(() => oldPoll)
        .mockResolvedValue({ serverName: "new-server", status: "Running" });
      const { rerender } = renderWithRouter("old-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      const oldSignal = vi.mocked(Shares.resolve).mock.calls[1][1];
      mockUseParams.mockReturnValue({ token: "new-token" });
      rerender(<SharePage />);
      await act(async () => {});
      expect(oldSignal?.aborted).toBe(true);
      expect(screen.getByText("new-server")).toBeInTheDocument();
      await act(async () => finishOldPoll({ serverName: "old-server", status: "Running" }));
      expect(screen.getByText("new-server")).toBeInTheDocument();
      expect(screen.queryByText("old-server")).not.toBeInTheDocument();
    });

    it("aborts an in-flight poll and schedules no follow-up after unmount", async () => {
      vi.useFakeTimers();
      let finishPoll!: (value: Awaited<ReturnType<typeof Shares.resolve>>) => void;
      const slowPoll = new Promise<Awaited<ReturnType<typeof Shares.resolve>>>((resolve) => { finishPoll = resolve; });
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "survival", status: "Starting" })
        .mockImplementationOnce(() => slowPoll);
      const { unmount } = renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      const signal = vi.mocked(Shares.resolve).mock.calls[1][1];
      unmount();
      expect(signal?.aborted).toBe(true);
      await act(async () => finishPoll({ serverName: "survival", status: "Starting" }));
      await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
    });

    it("T184: renders Starting state with spinner and message", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Starting",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
        expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();
      });
    });

    it("T184: waits five seconds between successful resolve requests", async () => {
      // Pure fake timers, advanced with the async API: shouldAdvanceTime
      // plus waitFor raced real time and timed out on busy CI runners.
      vi.useFakeTimers();

      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Starting",
      });

      renderWithRouter("test-token");

      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();

      // First call on mount
      expect(vi.mocked(Shares.resolve)).toHaveBeenCalledTimes(1);

      // Advance time
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });

      expect(vi.mocked(Shares.resolve)).toHaveBeenCalledTimes(2);

      vi.useRealTimers();
    });

    it("T184: transitions to Up when server is Running", async () => {
      vi.useFakeTimers();

      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({
          serverName: "mc-survival",
          status: "Starting",
        })
        .mockResolvedValueOnce({
          serverName: "mc-survival",
          status: "Running",
          address: { host: "play.gameplane.example", port: 25565 },
        });

      renderWithRouter("test-token");

      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(screen.getByText(/The server is starting up/)).toBeInTheDocument();

      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });

      expect(screen.getByText("Online")).toBeInTheDocument();
      expect(screen.getByText("play.gameplane.example:25565")).toBeInTheDocument();

      vi.useRealTimers();
    });

    it("T184: cancels polling on unmount", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });

      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Starting",
      });

      const { unmount } = render(
        <div>
          <SharePage />
        </div>
      );

      await waitFor(() => {
        expect(vi.mocked(Shares.resolve)).toHaveBeenCalled();
      });

      unmount();

      const callsAtUnmount = vi.mocked(Shares.resolve).mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
      expect(vi.mocked(Shares.resolve)).toHaveBeenCalledTimes(callsAtUnmount);

      vi.useRealTimers();
    });
  });

  describe("Invalid/expired state (T185)", () => {
    it("T185: maps 404 error to invalid state with neutral copy", async () => {
      vi.mocked(Shares.resolve).mockRejectedValue(
        new APIError(404, "Not found")
      );

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("Link not available")).toBeInTheDocument();
        expect(
          screen.getByText(/This link may be invalid, expired, or revoked/)
        ).toBeInTheDocument();
      });
    });

    it("keeps loading after initial 429 and recovers after Retry-After without declaring the link invalid", async () => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockRejectedValueOnce(new APIError(429, "private rate limit detail", undefined, undefined, "30"))
        .mockResolvedValue({ serverName: "survival", status: "Running" });
      renderWithRouter("test-token");
      await act(async () => {});
      expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
      expect(screen.queryByText("Link not available")).not.toBeInTheDocument();
      expect(screen.queryByText(/private rate limit detail/)).not.toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(29999); });
      expect(Shares.resolve).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it("T185: maps auth errors to invalid state with neutral copy", async () => {
      vi.mocked(Shares.resolve).mockRejectedValue(
        new APIError(401, "Unauthorized")
      );

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("Link not available")).toBeInTheDocument();
      });
    });

    it("T185: does not reveal whether link was valid, revoked, or expired", async () => {
      // Test scenario 1: invalid token (404)
      vi.mocked(Shares.resolve).mockRejectedValueOnce(
        new APIError(404, "Not found")
      );
      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("Link not available")).toBeInTheDocument();
      });

      const invalidMsg = screen.getByText(/This link may be invalid/);
      expect(invalidMsg).toBeInTheDocument();

      // Verify no specific error detail (status code or raw error body) is
      // shown — only the neutral copy asserted above, which lists all three
      // possibilities without confirming which one applies.
      expect(screen.queryByText(/404|Not found/)).not.toBeInTheDocument();
    });
  });

  describe("Appearance preference (T186)", () => {
    it("T186: reads appearance preference from localStorage", async () => {
      localStorage.setItem("gameplane-theme", "dark");

      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
      });
    });

    it("T186: applies light theme when stored", async () => {
      localStorage.setItem("gameplane-theme", "light");

      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(document.documentElement.getAttribute("data-theme")).toBe("light");
      });
    });

    it("T186: defaults to system preference when not stored", async () => {
      localStorage.removeItem("gameplane-theme");

      const mockMatchMedia = vi.fn((query) => ({
        matches: query === "(prefers-color-scheme: dark)",
      }));
      window.matchMedia = mockMatchMedia as unknown as typeof window.matchMedia;

      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
      });
    });

    it("T186: does not expose an appearance toggle", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "test-server",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("test-server")).toBeInTheDocument();
      });

      // Verify no toggle or theme controls exist
      expect(
        screen.queryByRole("button", { name: /light|dark|appearance/i })
      ).not.toBeInTheDocument();
      expect(screen.queryByRole("group", { name: /appearance/i })).not.toBeInTheDocument();
    });
  });

  describe("Privacy compliance (FR-005)", () => {
    it("T186: never renders cluster name", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
        address: { host: "play.gameplane.example", port: 25565 },
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
      });

      expect(screen.queryByText(/prod-east|cluster-1|my-cluster/i)).not.toBeInTheDocument();
    });

    it("T186: never renders namespace", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
      });

      expect(screen.queryByText(/namespace|default|prod/i)).not.toBeInTheDocument();
    });

    it("T186: never renders version string", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
      });

      expect(screen.queryByText(/v\d+\.\d+|beta|alpha|rc\d+/i)).not.toBeInTheDocument();
    });

    it("T186: never renders user names", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
      });

      expect(screen.queryByText(/alice|bob|admin|owner|user/i)).not.toBeInTheDocument();
    });

    it("T186: never renders server counts", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "mc-survival",
        status: "Running",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("mc-survival")).toBeInTheDocument();
      });

      // Make sure "3 players online" is shown only if from the response
      // but not server counts like "5 servers" or "10 running"
      expect(screen.queryByText(/\d+ servers|servers? running/i)).not.toBeInTheDocument();
    });

    it("T186: handles empty/neutral response as invalid state", async () => {
      vi.mocked(Shares.resolve).mockResolvedValue({
        serverName: "", // Empty serverName indicates error/neutral response
        status: "Unknown",
      });

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByText("Link not available")).toBeInTheDocument();
      });
    });
  });

  describe("Error handling", () => {
    it("aborts and ignores a Start request for a token that was left behind", async () => {
      let finishStart!: () => void;
      const oldStart = new Promise<void>((resolve) => { finishStart = resolve; });
      vi.mocked(Shares.resolve)
        .mockResolvedValueOnce({ serverName: "old-server", status: "Suspended" })
        .mockResolvedValue({ serverName: "new-server", status: "Running" });
      vi.mocked(Shares.start).mockImplementation(() => oldStart);
      const { rerender } = renderWithRouter("old-token");
      fireEvent.click(await screen.findByRole("button", { name: /start server/i }));
      const signal = vi.mocked(Shares.start).mock.calls[0][1];
      mockUseParams.mockReturnValue({ token: "new-token" });
      rerender(<SharePage />);
      expect(await screen.findByText("new-server")).toBeInTheDocument();
      expect(signal?.aborted).toBe(true);
      await act(async () => finishStart());
      expect(screen.getByText("Online")).toBeInTheDocument();
      expect(screen.queryByText("old-server")).not.toBeInTheDocument();
    });

    it.each([new Error("private network details"), new APIError(503, "private service details", undefined, undefined, "malformed")])("retries a transient initial failure using only the loading UI", async (failure) => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve)
        .mockRejectedValueOnce(failure)
        .mockResolvedValue({ serverName: "survival", status: "Running" });
      renderWithRouter("test-token");
      await act(async () => {});
      expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
      expect(screen.queryByText(/private network details|private service details|Link not available/)).not.toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(9999); });
      expect(Shares.resolve).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it("handles Start call failures by showing invalid state", async () => {
      vi.mocked(Shares.resolve).mockResolvedValueOnce({
        serverName: "mc-survival",
        status: "Suspended",
      });

      vi.mocked(Shares.start).mockRejectedValue(
        new APIError(403, "private denial details")
      );

      renderWithRouter("test-token");

      await waitFor(() => {
        expect(screen.getByRole("button", { name: /start server/i })).toBeInTheDocument();
      });

      const startBtn = screen.getByRole("button", { name: /start server/i });
      fireEvent.click(startBtn);

      await waitFor(() => {
        expect(screen.getByText("Link not available")).toBeInTheDocument();
      });
      expect(screen.queryByText("private denial details")).not.toBeInTheDocument();
    });

    it.each([["30", 30000], ["Wed, 01 Jan 2025 00:00:30 GMT", 30000], ["1", 5000]] as const)("recovers a start 429 after Retry-After %s and an asleep poll without reloading", async (retryAfter, delay) => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date("2025-01-01T00:00:00Z"));
      vi.mocked(Shares.resolve).mockResolvedValue({ serverName: "mc-survival", status: "Suspended" });
      vi.mocked(Shares.start)
        .mockRejectedValueOnce(new APIError(429, "private rate limit detail", undefined, undefined, retryAfter))
        .mockResolvedValue();
      renderWithRouter("test-token");
      await act(async () => {});
      await act(async () => { fireEvent.click(screen.getByRole("button", { name: /start server/i })); });
      expect(screen.getByRole("button", { name: /try again shortly/i })).toBeDisabled();
      expect(screen.queryByText(/private rate limit detail|Link not available|The server is starting up/)).not.toBeInTheDocument();
      await act(async () => { await vi.advanceTimersByTimeAsync(delay - 1); });
      expect(screen.getByRole("button", { name: /try again shortly/i })).toBeDisabled();
      expect(Shares.start).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(screen.getByRole("button", { name: /start server/i })).toBeEnabled();
      await act(async () => { fireEvent.click(screen.getByRole("button", { name: /start server/i })); });
      expect(Shares.start).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(Shares.resolve).toHaveBeenCalledTimes(2);
      expect(screen.getByRole("button", { name: /start server/i })).toBeEnabled();
    });

    it("cancels a start cooldown when navigating to a different token", async () => {
      vi.useFakeTimers();
      vi.mocked(Shares.resolve).mockResolvedValue({ serverName: "mc-survival", status: "Suspended" });
      vi.mocked(Shares.start).mockRejectedValueOnce(new APIError(429, "private detail", undefined, undefined, "30"));
      const { rerender } = renderWithRouter("old-token");
      await act(async () => {});
      await act(async () => { fireEvent.click(screen.getByRole("button", { name: /start server/i })); });
      expect(screen.getByRole("button", { name: /try again shortly/i })).toBeDisabled();
      mockUseParams.mockReturnValue({ token: "new-token" });
      rerender(<SharePage />);
      await act(async () => {});
      expect(screen.getByRole("button", { name: /start server/i })).toBeEnabled();
      await act(async () => { await vi.advanceTimersByTimeAsync(30000); });
      expect(Shares.start).toHaveBeenCalledTimes(1);
    });
  });
});
