import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { QueryClientProvider } from "@tanstack/react-query";

// Store callbacks for triggering events in tests
let sseCallback: ((ev: unknown) => void) | null = null;

vi.mock("@/lib/sse", () => ({
  openEventStream: (opts: { onEvent: (ev: unknown) => void }) => {
    sseCallback = opts.onEvent;
    return () => {
      sseCallback = null;
    };
  },
  queryFilterForKind: (kind: string) => {
    const keyMap: Record<string, string[] | null> = {
      servers: ["servers"],
      templates: ["templates"],
      backups: ["backups"],
      schedules: ["schedules"],
      restores: ["restores"],
    };
    const key = keyMap[kind];
    return key ? { queryKey: key } : null;
  },
}));

import { NotificationsPanel } from "./NotificationsPanel";

describe("NotificationsPanel", () => {
  beforeEach(() => {
    sseCallback = null;
  });

  it("closes its subscription when local event streaming is disabled", () => {
    const view = renderWithQuery(<NotificationsPanel />);
    expect(sseCallback).not.toBeNull();
    view.rerender(<QueryClientProvider client={view.client}><NotificationsPanel enabled={false} /></QueryClientProvider>);
    expect(sseCallback).toBeNull();
    expect(screen.getByRole("button", { name: /notifications/i })).toBeInTheDocument();
  });

  it("renders bell button with aria label", () => {
    renderWithQuery(<NotificationsPanel />);
    const bell = screen.getByRole("button", { name: /notifications/i });
    expect(bell).toBeInTheDocument();
  });

  it("shows no recent activity when there are no notices", async () => {
    renderWithQuery(<NotificationsPanel />);
    const bell = screen.getByRole("button", { name: /notifications/i });
    await userEvent.click(bell);
    expect(await screen.findByText("Recent activity")).toBeInTheDocument();
    expect(screen.getByText("No recent activity.")).toBeInTheDocument();
  });

  it("bell toggles the notifications panel open and closed", async () => {
    renderWithQuery(<NotificationsPanel />);
    const bell = screen.getByRole("button", { name: /notifications/i });

    // Initially closed — no "Recent activity" text
    expect(screen.queryByText("Recent activity")).not.toBeInTheDocument();

    // Click to open
    await userEvent.click(bell);
    expect(
      await screen.findByText("Recent activity")
    ).toBeInTheDocument();

    // Click to close
    await userEvent.click(bell);
    await waitFor(() => {
      expect(screen.queryByText("Recent activity")).not.toBeInTheDocument();
    });
  });

  it("displays notice events with text and time", async () => {
    renderWithQuery(<NotificationsPanel />);

    // Simulate an SSE event
    expect(sseCallback).not.toBeNull();
    await act(() => {
      sseCallback!({
        kind: "servers",
        eventType: "ADDED",
        object: { metadata: { name: "test-server" } },
      });
    });

    const bell = screen.getByRole("button", { name: /notifications/i });
    await userEvent.click(bell);

    // Wait for the notice to appear
    expect(
      await screen.findByText(/added server test-server/)
    ).toBeInTheDocument();
  });

  it("badge shows unread count", async () => {
    renderWithQuery(<NotificationsPanel />);

    // Simulate multiple SSE events
    expect(sseCallback).not.toBeNull();
    await act(() => {
      sseCallback!({
        kind: "servers",
        eventType: "ADDED",
        object: { metadata: { name: "server-1" } },
      });
    });
    await act(() => {
      sseCallback!({
        kind: "backups",
        eventType: "MODIFIED",
        object: { metadata: { name: "backup-1" } },
      });
    });

    // Badge should show 2 unread
    expect(await screen.findByText("2")).toBeInTheDocument();
  });

  it("badge shows 9+ when unread count exceeds 9", async () => {
    renderWithQuery(<NotificationsPanel />);

    expect(sseCallback).not.toBeNull();
    // Simulate 11 events
    for (let i = 1; i <= 11; i++) {
      await act(() => {
        sseCallback!({
          kind: "servers",
          eventType: "ADDED",
          object: { metadata: { name: `server-${i}` } },
        });
      });
    }

    expect(await screen.findByText("9+")).toBeInTheDocument();
  });

  it("resets unread count to 0 when panel is opened", async () => {
    renderWithQuery(<NotificationsPanel />);

    await act(() => {
      sseCallback!({
        kind: "servers",
        eventType: "ADDED",
        object: { metadata: { name: "server-1" } },
      });
    });

    // Badge shows 1
    expect(await screen.findByText("1")).toBeInTheDocument();

    // Click bell to open panel
    const bell = screen.getByRole("button", { name: /notifications/i });
    await userEvent.click(bell);

    // Wait for panel to render
    await screen.findByText("Recent activity");

    // Badge should be gone (unread count reset to 0)
    await waitFor(() => {
      expect(screen.queryByText("1")).not.toBeInTheDocument();
    });
  });

  it("caps notices at 50 items", async () => {
    renderWithQuery(<NotificationsPanel />);

    // Simulate 60 events
    for (let i = 1; i <= 60; i++) {
      await act(() => {
        sseCallback!({
          kind: "servers",
          eventType: "ADDED",
          object: { metadata: { name: `server-${i}` } },
        });
      });
    }

    const bell = screen.getByRole("button", { name: /notifications/i });
    await userEvent.click(bell);

    // Wait for panel to appear
    await screen.findByText("Recent activity");

    const noticeItems = screen.getAllByRole("listitem");
    // Should have exactly 50 items (capped)
    expect(noticeItems).toHaveLength(50);
  });

  it("removes trailing 's' from kind in notice text", async () => {
    renderWithQuery(<NotificationsPanel />);

    await act(() => {
      sseCallback!({
        kind: "servers",
        eventType: "MODIFIED",
        object: { metadata: { name: "my-server" } },
      });
    });

    const bell = screen.getByRole("button", { name: /notifications/i });
    await userEvent.click(bell);

    // Should say "modified server" (not "modified servers")
    expect(
      await screen.findByText(/modified server my-server/)
    ).toBeInTheDocument();
  });

  it("coalesces a burst of events for one kind into a single invalidation", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { client } = renderWithQuery(<NotificationsPanel />);
      const spy = vi.spyOn(client, "invalidateQueries");
      for (let i = 1; i <= 30; i++) {
        await act(() => {
          sseCallback!({ kind: "templates", eventType: "MODIFIED", object: { metadata: { name: `t-${i}` } } });
        });
      }
      await act(() => {
        sseCallback!({ kind: "servers", eventType: "MODIFIED", object: { metadata: { name: "s-1" } } });
      });
      expect(spy).not.toHaveBeenCalled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(500);
      });
      expect(spy).toHaveBeenCalledTimes(2);
      expect(spy).toHaveBeenCalledWith({ queryKey: ["templates"] }, { cancelRefetch: false });
      expect(spy).toHaveBeenCalledWith({ queryKey: ["servers"] }, { cancelRefetch: false });
    } finally {
      vi.useRealTimers();
    }
  });

  it("defers the invalidation until an in-flight fetch for that key settles", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { client } = renderWithQuery(<NotificationsPanel />);
      const spy = vi.spyOn(client, "invalidateQueries");
      const fetching = vi.spyOn(client, "isFetching").mockReturnValueOnce(1).mockReturnValue(0);
      await act(() => {
        sseCallback!({ kind: "servers", eventType: "MODIFIED", object: { metadata: { name: "s-1" } } });
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(500);
      });
      expect(fetching).toHaveBeenCalledWith({ queryKey: ["servers"] });
      expect(spy).not.toHaveBeenCalled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(500);
      });
      expect(spy).toHaveBeenCalledTimes(1);
      expect(spy).toHaveBeenCalledWith({ queryKey: ["servers"] }, { cancelRefetch: false });
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops pending invalidations on unmount", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { client, unmount } = renderWithQuery(<NotificationsPanel />);
      const spy = vi.spyOn(client, "invalidateQueries");
      await act(() => {
        sseCallback!({ kind: "servers", eventType: "MODIFIED", object: { metadata: { name: "s-1" } } });
      });
      unmount();
      await vi.advanceTimersByTimeAsync(500);
      expect(spy).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  describe("heartbeat noise filter", () => {
    const send = async (eventType: string, object?: Record<string, unknown>) => {
      await act(() => {
        sseCallback!({ kind: "servers", eventType, object });
      });
    };
    const server = (generation: number, phase: string) => ({
      metadata: { name: "s1", namespace: "games", generation },
      status: { phase },
    });
    // Opens the panel and returns how many notices it lists.
    const noticeCount = async () => {
      await userEvent.click(screen.getByRole("button", { name: /notifications/i }));
      await screen.findByText("Recent activity");
      return screen.queryAllByRole("listitem").length;
    };

    it("drops MODIFIED events that change neither generation nor phase", async () => {
      renderWithQuery(<NotificationsPanel />);
      await send("ADDED", server(1, "Running"));
      await send("MODIFIED", server(1, "Running"));
      await send("MODIFIED", server(1, "Running"));
      await send("MODIFIED", server(1, "Running"));
      expect(await noticeCount()).toBe(1);
      expect(screen.getByText(/added server s1/)).toBeInTheDocument();
    });

    it("records a MODIFIED when the phase changes", async () => {
      renderWithQuery(<NotificationsPanel />);
      await send("ADDED", server(1, "Running"));
      await send("MODIFIED", server(1, "Stopped"));
      await send("MODIFIED", server(1, "Stopped"));
      expect(await noticeCount()).toBe(2);
      expect(screen.getByText(/modified server s1/)).toBeInTheDocument();
    });

    it("records a MODIFIED when the generation (spec) changes", async () => {
      renderWithQuery(<NotificationsPanel />);
      await send("ADDED", server(1, "Running"));
      await send("MODIFIED", server(2, "Running"));
      expect(await noticeCount()).toBe(2);
    });

    it("records the first MODIFIED of an unseen object once, even without generation or status", async () => {
      renderWithQuery(<NotificationsPanel />);
      await send("MODIFIED", { metadata: { name: "bare" } });
      await send("MODIFIED", { metadata: { name: "bare" } });
      await send("MODIFIED", { metadata: { name: "bare" } });
      // An event with no object at all is handled too (recorded once).
      await send("MODIFIED");
      expect(await noticeCount()).toBe(2);
    });

    it("forgets the baseline on DELETED so a later MODIFIED is recorded", async () => {
      renderWithQuery(<NotificationsPanel />);
      await send("ADDED", server(1, "Running"));
      await send("DELETED", server(1, "Running"));
      await send("MODIFIED", server(1, "Running"));
      expect(await noticeCount()).toBe(3);
    });

    it("does not invalidate caches for a suppressed MODIFIED", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        const { client } = renderWithQuery(<NotificationsPanel />);
        const spy = vi.spyOn(client, "invalidateQueries");
        await send("ADDED", server(1, "Running"));
        await act(async () => {
          await vi.advanceTimersByTimeAsync(500);
        });
        expect(spy).toHaveBeenCalledTimes(1);
        await send("MODIFIED", server(1, "Running"));
        await act(async () => {
          await vi.advanceTimersByTimeAsync(1000);
        });
        expect(spy).toHaveBeenCalledTimes(1);
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
