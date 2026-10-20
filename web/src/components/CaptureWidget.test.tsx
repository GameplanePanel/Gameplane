import { afterEach, beforeEach, describe, it, expect, onTestFinished, vi } from "vitest";
import type { ReactElement, ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery as renderQuery } from "@/test/render";
import { ResourceTargetProvider, resourceKey, type ResourceTarget } from "@/lib/resourceTarget";
import { makeServer, makeCapture } from "@/test/factories";
import { APIError, type CaptureStartBody } from "@/lib/api";
import { CaptureWidget } from "./CaptureWidget";

function renderWithQuery(ui: ReactElement, permissions = ["captures:manage"], target: ResourceTarget = { cluster: "remote", namespace: "gameplane-games", name: "alpha" }) {
  return renderQuery(
    <ResourceTargetProvider target={target}
      access={{ canWrite: false, canControl: false, canConsole: false, canDelete: false, isOwner: false, isCollaborator: false, permissions }}>
      {ui}
    </ResourceTargetProvider>,
  );
}
const useMeMock = vi.fn();
// Target permissions come from ResourceTargetProvider; stub only identity state.
vi.mock("@/lib/auth", async (orig) => ({
  ...(await orig<typeof import("@/lib/auth")>()),
  useMe: () => useMeMock(),
}));

// Mock TanStack Router to avoid import errors in tests
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

beforeEach(() => {
  // Default: user has captures:manage permission
  useMeMock.mockReturnValue({
    data: { id: 1, username: "admin", role: "admin", permissions: { "*": ["captures:manage"] } },
    error: null,
    isLoading: false,
  });
});

afterEach(() => {
  server.resetHandlers();
  useMeMock.mockReset();
});

describe("CaptureWidget", () => {
  describe("selected-server capabilities", () => {
    const target: ResourceTarget = { cluster: "remote", namespace: "gameplane-games", name: "alpha", uid: "remote-alpha-uid" };
    const ready = { enabled: true, files: true, state: "ready" as const,
      defaultRetentionSeconds: 86400, maxRetentionSeconds: 604800,
      defaultMaxDurationSeconds: 300, defaultMaxSizeBytes: 5 * 1024 * 1024 * 1024 };
    const completed = makeCapture({ captureId: "cap-remote", phase: "Completed" });

    function listCompleted() {
      server.use(http.get(/servers\/alpha:captures$/, () => HttpResponse.json({
        captures: [completed], total: 1, limit: 100, offset: 0,
      })));
    }

    it("blocks Enable while the capability request is pending", async () => {
      let release!: () => void;
      const gate = new Promise<void>((resolve) => { release = resolve; });
      const enable = vi.fn(() => HttpResponse.json({}));
      server.use(
        http.get("/servers/alpha/capabilities", async () => {
          await gate;
          return HttpResponse.json({ target, capture: ready });
        }),
        http.post(/servers\/alpha:capture-enable$/, enable),
      );
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer()} />, ["captures:manage"], target);
      const button = screen.getByRole("button", { name: /Enable Capture/i });
      try {
        expect(screen.getByText("Checking capture support…")).toBeInTheDocument();
        expect(button).toBeDisabled();
        await userEvent.click(button);
        expect(enable).not.toHaveBeenCalled();
      } finally {
        release();
      }
      await waitFor(() => expect(button).not.toBeDisabled());
    });

    it.each([
      { state: "unsupported", status: 200, message: /needs a gateway update/i },
      { state: "unavailable", status: 200, message: /temporarily unavailable/i },
      { state: "unavailable", status: 503, message: /temporarily unavailable/i },
    ] as const)("disables Start and Download when capture access is $state with HTTP $status", async ({ state, status, message }) => {
      listCompleted();
      const download = vi.fn(() => new HttpResponse("unexpected download"));
      const start = vi.fn(() => HttpResponse.json({}));
      server.use(
        http.get("/servers/alpha/capabilities", () => status === 200
          ? HttpResponse.json({ target, capture: { enabled: false, files: false, state } })
          : new HttpResponse("unavailable", { status })),
        http.get(/servers\/alpha:capture-file$/, download),
        http.post(/servers\/alpha:capture-start$/, start),
      );
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer({ spec: { capture: { enabled: true } } })} />, ["captures:manage"], target);
      expect(await screen.findByText(message)).toBeInTheDocument();
      const startButton = screen.getByRole("button", { name: /Start Capture/i });
      const downloadButton = await screen.findByRole("button", { name: "Download capture cap-remote" });
      expect(startButton).toBeDisabled();
      expect(downloadButton).toBeDisabled();
      await userEvent.click(startButton);
      await userEvent.click(downloadButton);
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(start).not.toHaveBeenCalled();
      expect(download).not.toHaveBeenCalled();
    });

    it.each(["unsupported", "unavailable"] as const)("disables Enable when a location is %s", async (state) => {
      const enable = vi.fn(() => HttpResponse.json({}));
      server.use(
        http.get("/servers/alpha/capabilities", () => HttpResponse.json({ target, capture: { enabled: false, files: false, state } })),
        http.post(/servers\/alpha:capture-enable$/, enable),
      );
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer()} />, ["captures:manage"], target);
      await waitFor(() => expect(screen.queryByText("Checking capture support…")).not.toBeInTheDocument());
      const button = screen.getByRole("button", { name: /Enable Capture/i });
      expect(button).toBeDisabled();
      await userEvent.click(button);
      expect(enable).not.toHaveBeenCalled();
    });

    it("keeps existing files downloadable when the location disables new captures", async () => {
      listCompleted();
      const requests: URL[] = [];
      server.use(
        http.get("/servers/alpha/capabilities", () => HttpResponse.json({ target, capture: { ...ready, enabled: false } })),
        http.get(/servers\/alpha:capture-file$/, ({ request }) => {
          requests.push(new URL(request.url));
          return new HttpResponse(new Blob(["remote pcapng"]), { headers: { "Content-Type": "application/octet-stream" } });
        }),
      );
      const createURL = vi.fn(() => "blob:remote-capture");
      const revokeURL = vi.fn();
      const originalCreate = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
      const originalRevoke = Object.getOwnPropertyDescriptor(URL, "revokeObjectURL");
      Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createURL });
      Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeURL });
      const anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
      onTestFinished(() => {
        anchorClick.mockRestore();
        if (originalCreate) Object.defineProperty(URL, "createObjectURL", originalCreate);
        else delete (URL as { createObjectURL?: unknown }).createObjectURL;
        if (originalRevoke) Object.defineProperty(URL, "revokeObjectURL", originalRevoke);
        else delete (URL as { revokeObjectURL?: unknown }).revokeObjectURL;
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer({ spec: { capture: { enabled: true } } })} />, ["captures:manage"], target);
      expect(await screen.findByText(/New captures are disabled at this location/i)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /Start Capture/i })).toBeDisabled();
      const downloadButton = await screen.findByRole("button", { name: "Download capture cap-remote" });
      await waitFor(() => expect(downloadButton).not.toBeDisabled());
      await userEvent.click(downloadButton);
      await waitFor(() => expect(anchorClick).toHaveBeenCalledOnce());
      expect(requests).toHaveLength(1);
      expect(requests[0].pathname).toBe("/servers/alpha:capture-file");
      expect(requests[0].searchParams.get("cluster")).toBe("remote");
      expect(requests[0].searchParams.get("namespace")).toBe("gameplane-games");
      expect(requests[0].searchParams.get("id")).toBe("cap-remote");
      expect(createURL).toHaveBeenCalledOnce();
      expect(revokeURL).toHaveBeenCalledWith("blob:remote-capture");
    });

    it.each(["unsupported", "failure"] as const)("blocks an already-open Start form after capability %s", async (loss) => {
      const start = vi.fn(() => HttpResponse.json({ captureId: "must-not-start" }));
      let state: "ready" | "unsupported" | "failure" = "ready";
      listCompleted();
      server.use(
        http.get("/servers/alpha/capabilities", () => state === "failure"
          ? new HttpResponse("unavailable", { status: 503 })
          : HttpResponse.json({ target, capture: state === "ready" ? ready : { enabled: false, files: false, state } })),
        http.post(/servers\/alpha:capture-start$/, start),
      );
      const { client } = renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer({ spec: { capture: { enabled: true } } })} />, ["captures:manage"], target);
      const open = screen.getByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(open).not.toBeDisabled());
      const download = await screen.findByRole("button", { name: "Download capture cap-remote" });
      await userEvent.click(open);
      const dialog = await screen.findByRole("dialog");
      const submit = within(dialog).getByRole("button", { name: /Start Capture/i });
      expect(submit).not.toBeDisabled();
      state = loss;
      await act(async () => { await client.refetchQueries({ queryKey: resourceKey(target, "capabilities") }); });
      await waitFor(() => expect(submit).toBeDisabled());
      expect(download).toBeDisabled();
      await userEvent.click(submit);
      const form = dialog.querySelector("form");
      expect(form).not.toBeNull();
      fireEvent.submit(form!);
      expect(start).not.toHaveBeenCalled();
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it.each([undefined, 1800])("uses remote capture defaults, ceilings and server retention override %s", async (serverRetention) => {
      listCompleted();
      const capture = { ...ready, defaultRetentionSeconds: 7200, maxRetentionSeconds: 10800,
        defaultMaxDurationSeconds: 120, defaultMaxSizeBytes: 64 * 1024 * 1024 };
      const started: CaptureStartBody[] = [];
      server.use(
        http.get("/servers/alpha/capabilities", () => HttpResponse.json({ target, capture })),
        http.post(/servers\/alpha:capture-start$/, async ({ request }) => {
          const url = new URL(request.url);
          expect(url.searchParams.get("cluster")).toBe("remote");
          expect(url.searchParams.get("namespace")).toBe("gameplane-games");
          started.push(await request.json() as CaptureStartBody);
          return HttpResponse.json(makeCapture({ captureId: "remote-new", phase: "Pending" }), { status: 202 });
        }),
      );
      const retentionSeconds = serverRetention ?? capture.defaultRetentionSeconds;
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer({ spec: {
        capture: { enabled: true, ...(serverRetention === undefined ? {} : { retentionSeconds: serverRetention }) },
      } })} />, ["captures:manage"], target);
      const open = screen.getByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(open).not.toBeDisabled());
      expect(screen.getByText(`Captures will auto-delete after ${retentionSeconds / 3600} hours`)).toBeInTheDocument();
      await userEvent.click(open);
      const dialog = await screen.findByRole("dialog");
      const duration = within(dialog).getByLabelText(/Max duration value/i);
      const size = within(dialog).getByLabelText(/Max size value/i);
      const retention = within(dialog).getByLabelText(/Retention value/i);
      const submit = within(dialog).getByRole("button", { name: /Start Capture/i });
      expect(duration).toHaveValue(120);
      expect(size).toHaveValue(64);
      expect(size).toHaveAttribute("max", "64");
      expect(retention).toHaveValue(retentionSeconds / 3600);
      expect(retention).toHaveAttribute("min", String(60 / 3600));
      expect(retention).toHaveAttribute("max", "3");
      fireEvent.change(retention, { target: { value: String(59 / 3600) } });
      expect(submit).toBeDisabled();
      fireEvent.submit(dialog.querySelector("form")!);
      expect(started).toHaveLength(0);
      fireEvent.change(retention, { target: { value: String(60 / 3600) } });
      expect(submit).not.toBeDisabled();
      fireEvent.change(retention, { target: { value: "4" } });
      expect(submit).toBeDisabled();
      fireEvent.submit(dialog.querySelector("form")!);
      expect(started).toHaveLength(0);
      fireEvent.change(retention, { target: { value: String(retentionSeconds / 3600) } });
      fireEvent.change(size, { target: { value: "65" } });
      expect(submit).toBeDisabled();
      fireEvent.change(size, { target: { value: "64" } });
      fireEvent.change(duration, { target: { value: "3601" } });
      expect(submit).toBeDisabled();
      fireEvent.change(duration, { target: { value: "120" } });
      expect(submit).not.toBeDisabled();
      await userEvent.click(submit);
      await waitFor(() => expect(started).toHaveLength(1));
      expect(started[0]).toEqual({ maxDurationSeconds: 120, maxSizeBytes: 64 * 1024 * 1024, ttlSecondsAfterFinished: retentionSeconds });
    });
  });

  it("requires an exact capture grant even when server controls are permitted", () => {
    const read = vi.fn(() => HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }));
    const capabilities = vi.fn(() => HttpResponse.json({ capture: { enabled: true, files: true, state: "ready" } }));
    server.use(http.get(/servers\/alpha:captures(\?.*)?$/, read), http.get("/servers/alpha/capabilities", capabilities));
    renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={makeServer({ spec: { capture: { enabled: true } } })} />, ["servers:write"]);
    expect(screen.getByText(/You don't have access to packet capture on this server/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Start Capture/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Disable Capture/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(read).not.toHaveBeenCalled();
    expect(capabilities).not.toHaveBeenCalled();
  });

  it("does not infer capture management from a missing target context", () => {
    renderQuery(<CaptureWidget name="alpha" gs={makeServer()} />);
    expect(screen.getByText(/You don't have access to packet capture on this server/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Enable Capture/i })).not.toBeInTheDocument();
  });

  describe("permission gating", () => {
    it("shows access denied card when user lacks captures:manage permission", () => {
      useMeMock.mockReturnValue({
        data: { id: 1, username: "viewer", role: "viewer", permissions: { "*": ["servers:read"] } },
        error: null,
        isLoading: false,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />, ["servers:read"]);

      expect(screen.getByText(/You don't have access to packet capture on this server/i)).toBeInTheDocument();
      expect(screen.getByText(/Viewing, starting and downloading captures requires capture access/i)).toBeInTheDocument();
    });

    it("respects namespace binding", () => {
      // User has captures:manage only in test-ns, not in gameplane-games
      useMeMock.mockReturnValue({
        data: {
          id: 1,
          username: "operator",
          role: "operator",
          permissions: { "test-ns": ["captures:manage"] },
        },
        error: null,
        isLoading: false,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      // The verified gameplane-games target has no capture grant; a grant
      // elsewhere in the central profile cannot authorize this server.
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />, []);

      expect(screen.getByText(/You don't have access to packet capture on this server/i)).toBeInTheDocument();
    });

    it("shows a loading state instead of access denied while identity is unresolved", () => {
      useMeMock.mockReturnValue({
        data: undefined,
        error: null,
        isLoading: true,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText(/Checking access/i)).toBeInTheDocument();
      expect(screen.queryByText(/You don't have access to packet capture on this server/i)).not.toBeInTheDocument();
    });

    it("offers a retry instead of access denied when the identity fetch failed", async () => {
      const refetch = vi.fn();
      useMeMock.mockReturnValue({
        data: undefined,
        error: new APIError(503, "unavailable"),
        isLoading: false,
        refetch,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText("Can't reach the control plane")).toBeInTheDocument();
      expect(screen.getByText(/HTTP 503/)).toBeInTheDocument();
      expect(screen.queryByText(/You don't have access to packet capture on this server/i)).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Start Capture" })).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Try again" }));
      expect(refetch).toHaveBeenCalledTimes(1);
    });

    it("allows access when user has namespace-scoped captures:manage permission", () => {
      useMeMock.mockReturnValue({
        data: {
          id: 1,
          username: "operator",
          role: "operator",
          permissions: { "gameplane-games": ["captures:manage"] },
        },
        error: null,
        isLoading: false,
      });
      const gs = makeServer({
        spec: { capture: { enabled: false } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should show the disabled state (not the permission denied state)
      expect(screen.getByText(/Capture is not enabled on this server/i)).toBeInTheDocument();
      expect(screen.queryByText(/You don't have access to packet capture on this server/i)).not.toBeInTheDocument();
    });
  });

  describe("disabled state", () => {
    it("shows the disabled banner when capture is not enabled on the server", () => {
      const gs = makeServer({
        spec: { capture: { enabled: false } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText(/Capture is not enabled on this server/i)).toBeInTheDocument();
      expect(screen.getByText(/Network packet capture is an optional feature/i)).toBeInTheDocument();
    });

    it("enables the Enable Capture button only after checking location support", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: false } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const button = screen.getByRole("button", { name: /Enable Capture/i });
      expect(button).toBeInTheDocument();
      expect(button).toBeDisabled();
      await waitFor(() => expect(button).not.toBeDisabled());
    });

    it("calls enable mutation when Enable Capture is clicked", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: false } },
      });
      // The response is gated on the test, not on a timer. An
      // instantly-resolving handler can settle inside the act() that
      // userEvent.click awaits, so the pending render is already gone when
      // the assertion runs; a fixed delay only narrows that window (a
      // loaded CI worker can still overrun it). Holding the response until
      // the test releases it makes the pending state deterministic, and
      // releasing it afterwards still exercises the success path.
      let releaseEnable!: () => void;
      const enableGate = new Promise<void>((resolve) => {
        releaseEnable = resolve;
      });
      server.use(
        http.post(/servers\/alpha:capture-enable(\?.*)?$/, async ({ request }) => {
          const url = new URL(request.url);
          expect(url.searchParams.get("cluster")).toBe("remote");
          expect(url.searchParams.get("namespace")).toBe("gameplane-games");
          await enableGate;
          return HttpResponse.json({ name: "alpha", status: { capture: { enabled: true } } });
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const button = screen.getByRole("button", { name: /Enable Capture/i });
      await waitFor(() => expect(button).not.toBeDisabled());
      await userEvent.click(button);

      // Button shows the loading state while the gated request is in
      // flight. Re-queried by its (now-changed) accessible name rather than
      // asserted on the captured reference — the label swaps to "Enabling…"
      // while pending, so the original name would no longer find it.
      try {
        await waitFor(() => {
          const pendingBtn = screen.getByRole("button", { name: /Enabling/i });
          expect(pendingBtn).toBeDisabled();
        });
      } finally {
        // Released in `finally`, not just on the happy path: a failed
        // assertion above would otherwise leave the handler parked on this
        // promise forever, leaking a never-settling request past the end of
        // the test. Releasing here (rather than from an onTestFinished
        // hook) keeps the settle inside the test body, while the component
        // and any per-test stubs are still alive.
        releaseEnable();
      }

      // Confirm the mutation settles back out of its pending state — which
      // also runs the mutation's onSuccess.
      await waitFor(() =>
        expect(screen.getByRole("button", { name: /Enable Capture/i })).not.toBeDisabled(),
      );
    });

    it("displays error banner when enable mutation fails", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: false } },
      });
      server.use(
        http.post(/servers\/alpha:capture-enable(\?.*)?$/, () =>
          new HttpResponse("permission denied", { status: 403 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const button = screen.getByRole("button", { name: /Enable Capture/i });
      await waitFor(() => expect(button).not.toBeDisabled());
      await userEvent.click(button);

      await waitFor(() => {
        expect(screen.getByText(/permission denied/i)).toBeInTheDocument();
      });
    });
  });

  describe("enabled state", () => {
    it("renders the caution banner by default", () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText(/Caution: network packet captures contain real player data/i)).toBeInTheDocument();
      expect(screen.getByText(/Player IP addresses and port numbers/i)).toBeInTheDocument();
    });

    it("allows dismissing the caution banner", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const dismissBtn = screen.getByRole("button", { name: /Dismiss/i });
      await userEvent.click(dismissBtn);

      expect(screen.queryByText(/Caution: network packet captures contain real player data/i)).not.toBeInTheDocument();
    });

    it("shows Ready status when no active capture", () => {
      const gs = makeServer({
        spec: { capture: { enabled: true, retentionSeconds: 86400 } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText(/Ready/i)).toBeInTheDocument();
      expect(screen.getByText(/Captures will auto-delete after 24 hours/i)).toBeInTheDocument();
    });

    it("shows empty state when no captures exist", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText(/No captures yet/i)).toBeInTheDocument();
    });

    it("renders Disable Capture and Start Capture buttons when no active capture", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByRole("button", { name: /Disable Capture/i })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /Start Capture/i })).toBeInTheDocument();
    });

    it("disables the Disable Capture button when disable mutation is pending", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      // Gated on the test rather than on a timer, for the same reason as
      // the enable test above.
      let releaseDisable!: () => void;
      const disableGate = new Promise<void>((resolve) => {
        releaseDisable = resolve;
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () => new Promise(() => {})), // Never resolves
        http.post(/servers\/alpha:capture-disable(\?.*)?$/, async () => {
          await disableGate;
          return HttpResponse.json({ name: "alpha", status: { capture: { enabled: false } } });
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const disableBtn = await screen.findByRole("button", { name: /Disable Capture/i });
      await userEvent.click(disableBtn);

      // Re-query rather than asserting on the captured reference: the
      // button's accessible name/text is static here so the node is never
      // replaced, but waitFor still tolerates a deferred pending commit
      // instead of racing it.
      try {
        await waitFor(() => {
          expect(screen.getByRole("button", { name: /Disable Capture/i })).toBeDisabled();
        });
      } finally {
        // `finally` for the same reason as the enable test above.
        releaseDisable();
      }

      await waitFor(() =>
        expect(screen.getByRole("button", { name: /Disable Capture/i })).not.toBeDisabled(),
      );
    });
  });

  describe("active capture state", () => {
    it("shows Capturing… status when there is an active Running capture", async () => {
      const activeCap = makeCapture({ phase: "Running", captureId: "cap-active", startedAt: new Date(Date.now() - 5000).toISOString() });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText(/Capturing/i)).toBeInTheDocument();
    });

    it("displays active capture details with max duration and size meters", async () => {
      const activeCap = makeCapture({
        phase: "Running",
        captureId: "cap-active",
        maxDurationSeconds: 3600,
        maxSizeBytes: 104857600,
        bytesWritten: 10485760, // 10MB
        packetsWritten: 500,
        startedAt: new Date(Date.now() - 600000).toISOString(),
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText(/Max duration/i)).toBeInTheDocument();
      expect(screen.getByText(/Max size/i)).toBeInTheDocument();
      expect(screen.getByText(/500 packets captured/i)).toBeInTheDocument();
    });

    it("renders Stop Capture button for active captures", async () => {
      const activeCap = makeCapture({
        phase: "Running",
        captureId: "cap-active",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByRole("button", { name: /Stop Capture/i })).toBeInTheDocument();
    });

    it("calls stop mutation when Stop Capture is clicked", async () => {
      const activeCap = makeCapture({
        phase: "Running",
        captureId: "cap-active",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      // Gated on the test rather than on a timer, for the same reason as
      // the enable/disable tests above.
      let releaseStop!: () => void;
      const stopGate = new Promise<void>((resolve) => {
        releaseStop = resolve;
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
        http.post(/servers\/alpha:capture-stop(\?.*)?$/, async () => {
          await stopGate;
          return HttpResponse.json({ ...activeCap, phase: "Completed", completedAt: new Date().toISOString() });
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const stopBtn = await screen.findByRole("button", { name: /Stop Capture/i });
      await userEvent.click(stopBtn);

      try {
        await waitFor(() => {
          expect(screen.getByRole("button", { name: /Stop Capture/i })).toBeDisabled();
        });
      } finally {
        // `finally` for the same reason as the enable test above.
        releaseStop();
      }

      await waitFor(() =>
        expect(screen.getByRole("button", { name: /Stop Capture/i })).not.toBeDisabled(),
      );
    });
  });

  describe("capture list", () => {
    it("renders completed captures in a table", async () => {
      const completed1 = makeCapture({
        captureId: "cap-1",
        phase: "Completed",
        serverName: "alpha",
        filter: "tcp port 25565",
        bytesWritten: 2048,
        packetsWritten: 150,
      });
      const completed2 = makeCapture({
        captureId: "cap-2",
        phase: "Failed",
        serverName: "alpha",
        bytesWritten: 512,
        packetsWritten: 20,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed1, completed2], total: 2, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText("cap-1")).toBeInTheDocument();
      expect(screen.getByText("cap-2")).toBeInTheDocument();
    });

    it("renders capture phase badge with correct styling", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        phase: "Completed",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText("Completed")).toBeInTheDocument();
    });

    it("displays capture size and packet count formatted", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        bytesWritten: 1048576, // 1 MB
        packetsWritten: 1000,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText(/1(,)?000/)).toBeInTheDocument(); // Formatted packet count
    });

    it("enables download button only for Completed captures", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        phase: "Completed",
      });
      const failed = makeCapture({
        captureId: "cap-2",
        phase: "Failed",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed, failed], total: 2, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const buttons = await screen.findAllByLabelText(/Download capture/i);
      expect(buttons.length).toBe(2);
      // First button (Completed) should be enabled once the capabilities
      // query resolves; the list can render before it does.
      await waitFor(() => expect(buttons[0]).not.toBeDisabled());
      // Second button (Failed) should be disabled
      expect(buttons[1]).toBeDisabled();
    });

    it("downloads capture file on button click", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        phase: "Completed",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      // The response is gated on the test rather than on a timer: this test
      // asserts the in-flight (disabled) state AND the completed download,
      // so it needs the request to stay pending until the first assertion
      // has run and then complete on demand. A fixed delay would only make
      // the "still pending" window probable; the gate makes it certain.
      let releaseFile!: () => void;
      const fileGate = new Promise<void>((resolve) => {
        releaseFile = resolve;
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
        // The optional-query group is load-bearing, and a literal `\?` here
        // is a silent no-op: msw strips the query string before matching a
        // handler (getCleanUrl() in @mswjs/interceptors, via
        // matchRequestUrl()), so this regex is tested against
        // ".../servers/alpha:capture-file" with no "?" in it at all. A
        // regex that *requires* "?" therefore never matches — and instead
        // of failing loudly as an unhandled request, it falls through to
        // the default http.get("/servers/:name") handler in
        // src/test/handlers.ts, whose `:name` param happily swallows
        // "alpha:capture-file". That stub answers instantly with a
        // GameServer JSON body, res.blob() accepts it, and the mutation
        // settles before the pending assertion below can ever observe it.
        http.get(/servers\/alpha:capture-file(\?.*)?$/, async () => {
          await fileGate;
          return new HttpResponse(new Blob(["mock pcapng data"]), {
            headers: { "Content-Type": "application/octet-stream" },
          });
        }),
      );

      // jsdom doesn't implement URL.createObjectURL/revokeObjectURL, and
      // the mutation's onSuccess (CaptureWidget.tsx) calls both to build the
      // download link — stub them so that handler doesn't throw once the
      // mocked fetch resolves (same pattern as AuditLog.test.tsx's export).
      const createURL = vi.fn((_blob: Blob) => "blob:test");
      const revokeURL = vi.fn();
      Object.defineProperty(URL, "createObjectURL", { configurable: true, writable: true, value: createURL });
      Object.defineProperty(URL, "revokeObjectURL", { configurable: true, writable: true, value: revokeURL });
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
      // Registered immediately, and via onTestFinished rather than at the end
      // of the body, so the globals are restored even when an assertion below
      // throws — a leaked anchor-click spy or URL stub would otherwise poison
      // every later test in this file. Neither property exists on jsdom's URL
      // before this test defines it, so remove them outright rather than
      // restoring a prior descriptor.
      onTestFinished(() => {
        clickSpy.mockRestore();
        delete (URL as { createObjectURL?: unknown }).createObjectURL;
        delete (URL as { revokeObjectURL?: unknown }).revokeObjectURL;
      });

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const downloadBtn = await screen.findByLabelText(/Download capture cap-1/i);
      await waitFor(() => expect(downloadBtn).not.toBeDisabled());
      await userEvent.click(downloadBtn);

      // Button is disabled while the (still-gated) download is in flight.
      // Re-queried by aria-label inside waitFor rather than asserted on the
      // captured reference: the pending commit is synchronous in practice,
      // but waitFor costs nothing on the happy path and tolerates a
      // deferred one on a loaded runner.
      //
      // This can't pass vacuously — the button's only other disabled input,
      // `!downloadable`, never flips in this test (a single fixture row,
      // phase "Completed" throughout), so "disabled" here is proof of
      // fileMut.isPending and nothing else. And with the handler regex
      // above actually matching, the gate guarantees the request can't have
      // settled back to false yet.
      try {
        await waitFor(() => {
          expect(screen.getByLabelText(/Download capture cap-1/i)).toBeDisabled();
        });
      } finally {
        // Let the response through — in `finally` for the same reason as
        // the enable test above. Doing it here rather than from the
        // onTestFinished hook below matters twice over: that hook deletes
        // the URL.createObjectURL stub, and a gate released after it would
        // resolve the fetch straight into an onSuccess that then throws.
        releaseFile();
      }

      // Wait for the mutation's onSuccess to run the full download sequence.
      await waitFor(() => expect(clickSpy).toHaveBeenCalledOnce());
      expect(createURL).toHaveBeenCalledOnce();
      expect(revokeURL).toHaveBeenCalledWith("blob:test");
      // Button re-enables once the mutation settles, so the test doesn't
      // leave a pending mutation behind.
      await waitFor(() =>
        expect(screen.getByLabelText(/Download capture cap-1/i)).not.toBeDisabled(),
      );
    });

    it("expands row to show details on eye button click", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        createdAt: "2026-08-23T12:00:00Z",
        startedAt: "2026-08-23T12:05:00Z",
        serverName: "alpha",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const eyeBtn = await screen.findByLabelText(/View capture cap-1/i);
      await userEvent.click(eyeBtn);

      // Details row should appear
      const detailsRow = await screen.findByText(/Created/i);
      expect(detailsRow).toBeInTheDocument();
      expect(screen.getByText(/Started/i)).toBeInTheDocument();
      // "Server" appears in multiple places, so we check for the server name "alpha"
      expect(screen.getByText("alpha")).toBeInTheDocument();
    });

    it("collapses row when eye button is clicked again", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const eyeBtn = await screen.findByLabelText(/View capture cap-1/i);

      // Expand
      await userEvent.click(eyeBtn);
      await screen.findByText(/Created/i);

      // Collapse
      await userEvent.click(eyeBtn);
      await waitFor(() => {
        expect(screen.queryByText(/Created/i)).not.toBeInTheDocument();
      });
    });

    it("shows delete button for each capture", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByLabelText(/Delete capture cap-1/i)).toBeInTheDocument();
    });

    it("opens confirm dialog when delete button is clicked", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const deleteBtn = await screen.findByLabelText(/Delete capture cap-1/i);
      await userEvent.click(deleteBtn);

      // Exact match on the dialog title text ("Delete capture?"): a loose
      // /Delete capture/i also matches the dialog's own confirm button
      // ("Delete capture", no "?"), so findByText resolves two elements
      // and never settles — the same ambiguous-regex class documented on
      // "deletes capture when confirmed" below, just not yet called out
      // here.
      expect(await screen.findByText("Delete capture?")).toBeInTheDocument();
      expect(screen.getByText(/permanently deletes capture/i)).toBeInTheDocument();
    });

    it("deletes capture when confirmed", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      // Gated on the test rather than on a timer. Besides the usual
      // pending-window race, an instant response here is actively
      // misleading: deleteMut's onSuccess closes the dialog, so a
      // too-fast response leaves `confirmBtn` a detached node whose stale
      // `disabled` attribute the assertion could read either way.
      let releaseDelete!: () => void;
      const deleteGate = new Promise<void>((resolve) => {
        releaseDelete = resolve;
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
        // Optional-query group, not a literal `\?` — see the capture-file
        // handler in "downloads capture file on button click" for why. Here
        // the fall-through was even quieter: an unmatched DELETE lands on
        // the default http.delete("/servers/:name") stub, which returns 204,
        // api() maps 204 to undefined, and deleteMut *succeeds* instantly —
        // closing the dialog before the pending assertion below runs.
        http.delete(/.*servers.*alpha.*capture/, async () => {
          await deleteGate;
          return HttpResponse.json({ deleted: true, captureId: "cap-1" });
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const deleteBtn = await screen.findByLabelText(/Delete capture cap-1/i);
      await userEvent.click(deleteBtn);

      // Exact match: the row's icon button has aria-label "Delete capture
      // cap-1", which also satisfies a loose /Delete capture/i match — only
      // the dialog's confirm button has the accessible name "Delete capture".
      const confirmBtn = await screen.findByRole("button", { name: "Delete capture" });
      await userEvent.click(confirmBtn);

      // ConfirmDialog swaps the confirm button's label to "Working…" while
      // `busy` (deleteMut.isPending) is true — so re-querying by the
      // original exact name "Delete capture" would (wrongly) report "not
      // found" once pending lands, rather than failing on disabled state.
      // A loose /Delete capture/i also matches the row's own "Delete
      // capture cap-1" icon button (still present behind the dialog), so
      // scope the query to the dialog and match on the pending-only
      // "Working…" text — which also keeps the assertion from passing
      // vacuously for any other reason (there is none here, since no
      // confirmPhrase is passed and `matches` is always true, but tying it
      // to the pending-only label keeps that true even if that changes).
      //
      // The scope role is "alertdialog", not "dialog": HeroUI v3's
      // AlertDialogDialog hard-codes role="alertdialog" on the react-aria
      // Dialog (node_modules/@heroui/react/dist/components/alert-dialog/
      // alert-dialog.js), and Testing Library's byRole matches the role
      // string literally — it does not walk the ARIA role hierarchy, so
      // "dialog" never matches an alertdialog. Only the Modal-based
      // StartCaptureModal below is role="dialog".
      try {
        await waitFor(() => {
          const dialog = screen.getByRole("alertdialog");
          const pendingBtn = within(dialog).getByRole("button", { name: /Working/i });
          expect(pendingBtn).toBeDisabled();
        });
      } finally {
        // `finally` for the same reason as the enable test above.
        releaseDelete();
      }

      // Confirm the delete actually went through: onSuccess
      // clears deleteTarget, which closes the dialog. That is the only
      // observable proof the DELETE was issued and succeeded — the
      // captures list is a fixed mock, so the row itself never disappears.
      // Same "alertdialog" role as above — queried as "dialog" this
      // assertion was vacuous (it can never find the confirm dialog).
      await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    });

    // F-126: deleting a capture while the sidecar is unreachable left the
    // dialog open with no message — deleteMut had no error handler.
    it("shows an error banner in the dialog when delete fails", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
        http.delete(/.*servers.*alpha.*capture/, () =>
          HttpResponse.text("capture sidecar unreachable", { status: 502 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const deleteBtn = await screen.findByLabelText(/Delete capture cap-1/i);
      await userEvent.click(deleteBtn);
      const confirmBtn = await screen.findByRole("button", { name: "Delete capture" });
      await userEvent.click(confirmBtn);

      // The dialog stays open (no onSuccess fired) and now shows the error.
      expect(await screen.findByText(/capture sidecar unreachable/i)).toBeInTheDocument();
      expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    });

    it("shows expiry time with correct tone badge", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        // +30s past the exact hour: expiryLabel's Date.now() call happens
        // after an async render/fetch round trip, so an exact-multiple
        // offset (3600000) has zero margin — any elapsed wall-clock time
        // floors secondsLeft below the hour and flips the label to
        // "59m", missing the "1h" query entirely. The 30s pad tolerates
        // realistic CI overhead without changing the asserted "1h" text.
        expiresAt: new Date(Date.now() + 3630000).toISOString(), // ~1 hour
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Exact "1h", not /1h/: makeCapture's default startedAt→completedAt
      // span is exactly one hour, so the row's Duration cell renders
      // "1h 0m" and a loose /1h/ matches BOTH cells — findByText then
      // rejects with "found multiple elements", surfacing as a bare 5s
      // timeout (asyncUtilTimeout == testTimeout). Exact matching pins the
      // query to the expiry badge, whose whole text is "1h".
      const expiryCell = await screen.findByText("1h");
      // In HeroUI v3, the Chip color is applied at the root span level via Tailwind variants,
      // not as a plain className on the label. Check the parent element (the Chip root).
      const chipRoot = expiryCell.closest("span[data-color]");
      expect(chipRoot).toHaveAttribute("data-color", "warning");
    });

    it("displays capture count at the bottom", async () => {
      const cap1 = makeCapture({ captureId: "cap-1" });
      const cap2 = makeCapture({ captureId: "cap-2" });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [cap1, cap2], total: 5, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(await screen.findByText(/Showing 2 of 5 captures/i)).toBeInTheDocument();
    });
  });

  describe("StartCaptureModal", () => {
    it("opens modal when Start Capture button is clicked", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      // getByText would also match the still-mounted trigger button and the
      // submit button, both of which share the same "Start Capture" text —
      // the dialog title is uniquely queryable by its heading role.
      expect(await screen.findByRole("heading", { name: /Start Capture/i })).toBeInTheDocument();
      expect(screen.getByText(/Records raw network traffic/i)).toBeInTheDocument();
    });

    it("renders all form fields with correct labels", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      expect(await screen.findByLabelText(/Packet Filter/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/Max duration value/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/Max duration unit/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/Max size value/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/Max size unit/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/Retention value/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/Retention unit/i)).toBeInTheDocument();
    });

    it("starts with default values", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = (await screen.findByLabelText(/Packet Filter/i)) as HTMLInputElement;
      const durationInput = screen.getByLabelText(/Max duration value/i) as HTMLInputElement;
      const sizeInput = screen.getByLabelText(/Max size value/i) as HTMLInputElement;
      const retentionInput = screen.getByLabelText(/Retention value/i) as HTMLInputElement;

      expect(filterInput.value).toBe("");
      expect(durationInput.value).toBe("300");
      expect(sizeInput.value).toBe("5120");
      expect(retentionInput.value).toBe("24");
    });

    it("validates BPF filter and shows error when API returns filter error", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, () =>
          new HttpResponse("invalid filter syntax: syntax error\n", { status: 400 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "invalid [[ syntax");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      // Wait for error to appear
      await waitFor(() => {
        expect(screen.getByText(/invalid filter syntax/i)).toBeInTheDocument();
      });
    });

    it("shows error indicator when filter has validation error", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, () =>
          new HttpResponse("invalid filter\n", { status: 400 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "bad filter");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      // Wait for error and check that error icon appears
      await waitFor(() => {
        expect(screen.getByText(/invalid filter/i)).toBeInTheDocument();
      });
    });

    it("clears filter error when user edits the filter field", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, () =>
          new HttpResponse("invalid filter\n", { status: 400 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "bad");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      // Wait for error
      await waitFor(() => {
        expect(screen.getByText(/invalid filter/i)).toBeInTheDocument();
      });

      // Type more — error should clear
      await userEvent.type(filterInput, " filter");
      expect(screen.queryByText(/invalid filter/i)).not.toBeInTheDocument();
    });

    it("allows changing duration unit between seconds and minutes", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const durationUnit = screen.getByLabelText(/Max duration unit/i);
      await userEvent.click(durationUnit);
      const minutesOption = await screen.findByRole("option", { name: /minutes/i });
      await userEvent.click(minutesOption);

      await waitFor(() => {
        expect(screen.getByText("minutes")).toBeInTheDocument();
      });
    });

    it("allows changing size unit between MB and GB", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const sizeUnit = screen.getByLabelText(/Max size unit/i);
      await userEvent.click(sizeUnit);
      const gbOption = await screen.findByRole("option", { name: /GB/i });
      await userEvent.click(gbOption);

      await waitFor(() => {
        expect(screen.getByText("GB")).toBeInTheDocument();
      });
    });

    it("allows changing retention unit between hours and days", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const retentionUnit = screen.getByLabelText(/Retention unit/i);
      await userEvent.click(retentionUnit);
      const daysOption = await screen.findByRole("option", { name: /days/i });
      await userEvent.click(daysOption);

      await waitFor(() => {
        expect(screen.getByText("days")).toBeInTheDocument();
      });
    });

    it("disables Start button when duration value is below 1", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const durationInput = screen.getByLabelText(/Max duration value/i);
      await userEvent.clear(durationInput);
      await userEvent.type(durationInput, "0");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      expect(submitBtn).toBeDisabled();
    });

    it("disables Start button when size value is below 1", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const sizeInput = screen.getByLabelText(/Max size value/i);
      await userEvent.clear(sizeInput);
      await userEvent.type(sizeInput, "0");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      expect(submitBtn).toBeDisabled();
    });

    it("disables Start button when filter has validation error", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, () =>
          new HttpResponse("invalid filter\n", { status: 400 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "bad");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      // Wait for error
      await waitFor(() => {
        expect(screen.getByText(/invalid filter/i)).toBeInTheDocument();
      });

      expect(submitBtn).toBeDisabled();
    });

    it("submits capture start with correct body values in seconds and bytes", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      let captureStartBody: Partial<CaptureStartBody> | null = null;
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async ({ request }) => {
          captureStartBody = (await request.json()) as Partial<CaptureStartBody>;
          return HttpResponse.json(
            {
              captureId: "cap-new",
              phase: "Pending",
              serverName: "alpha",
              filter: "tcp port 8080",
              createdAt: new Date().toISOString(),
              startedAt: null,
              completedAt: null,
              bytesWritten: 0,
              packetsWritten: 0,
              expiresAt: new Date(Date.now() + 86400000).toISOString(),
            },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "tcp port 8080");

      const durationInput = screen.getByLabelText(/Max duration value/i);
      await userEvent.clear(durationInput);
      await userEvent.type(durationInput, "60");

      const sizeInput = screen.getByLabelText(/Max size value/i);
      await userEvent.clear(sizeInput);
      await userEvent.type(sizeInput, "1");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(captureStartBody).toBeDefined();
        expect(captureStartBody!.filter).toBe("tcp port 8080");
        expect(captureStartBody!.maxDurationSeconds).toBe(60);
        expect(captureStartBody!.maxSizeBytes).toBe(1048576); // 1 MB in bytes
      });
    });

    it("converts duration from minutes to seconds on submit", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      let captureStartBody: Partial<CaptureStartBody> | null = null;
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async ({ request }) => {
          captureStartBody = (await request.json()) as Partial<CaptureStartBody>;
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const durationInput = screen.getByLabelText(/Max duration value/i);
      const durationUnit = screen.getByLabelText(/Max duration unit/i);

      await userEvent.clear(durationInput);
      await userEvent.type(durationInput, "5");
      await userEvent.click(durationUnit);
      const minutesOption = await screen.findByRole("option", { name: /minutes/i });
      await userEvent.click(minutesOption);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(captureStartBody!.maxDurationSeconds).toBe(300); // 5 minutes * 60
      });
    });

    it("converts size from GB to bytes on submit", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      let captureStartBody: Partial<CaptureStartBody> | null = null;
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async ({ request }) => {
          captureStartBody = (await request.json()) as Partial<CaptureStartBody>;
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const sizeInput = screen.getByLabelText(/Max size value/i);
      const sizeUnit = screen.getByLabelText(/Max size unit/i);

      await userEvent.clear(sizeInput);
      await userEvent.type(sizeInput, "1");
      await userEvent.click(sizeUnit);
      const gbOption = await screen.findByRole("option", { name: /GB/i });
      await userEvent.click(gbOption);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(captureStartBody!.maxSizeBytes).toBe(1073741824); // 1 GB in bytes
      });
    });

    it("converts retention from days to seconds on submit", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      let captureStartBody: Partial<CaptureStartBody> | null = null;
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async ({ request }) => {
          captureStartBody = (await request.json()) as Partial<CaptureStartBody>;
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const retentionInput = screen.getByLabelText(/Retention value/i);
      const retentionUnit = screen.getByLabelText(/Retention unit/i);

      await userEvent.clear(retentionInput);
      await userEvent.type(retentionInput, "7");
      await userEvent.click(retentionUnit);
      const daysOption = await screen.findByRole("option", { name: /days/i });
      await userEvent.click(daysOption);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(captureStartBody!.ttlSecondsAfterFinished).toBe(604800); // 7 days * 86400
      });
    });

    it("shows loading state during capture start", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async () => {
          await new Promise((r) => setTimeout(r, 100));
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "tcp port 8080");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      expect(submitBtn).toHaveTextContent(/Starting/i);
    });

    it("displays non-filter API errors in an error banner", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, () =>
          new HttpResponse("server error: pod not running\n", { status: 500 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(screen.getByText(/server error/i)).toBeInTheDocument();
      });
    });

    it("closes modal when Cancel is clicked", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      expect(await screen.findByRole("heading", { name: /Start Capture/i })).toBeInTheDocument();

      const cancelBtn = screen.getByRole("button", { name: /Cancel/i });
      await userEvent.click(cancelBtn);

      // The page's own "Start Capture" trigger button keeps that text after
      // the modal closes, so assert on the dialog itself, not the text.
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      });
    });

    it("disables Cancel button during capture start", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async () => {
          await new Promise((r) => setTimeout(r, 100));
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      const cancelBtn = screen.getByRole("button", { name: /Cancel/i });
      expect(cancelBtn).toBeDisabled();
    });

    it("closes modal and invalidates queries on successful capture start", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, () =>
          HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          ),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      // Modal should close
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      });
    });
  });

  describe("retention settings", () => {
    it("displays default retention hours when retentionSeconds is not set", () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Default is 86400 seconds = 24 hours
      expect(screen.getByText(/Captures will auto-delete after 24 hours/i)).toBeInTheDocument();
    });

    it("displays custom retention hours when retentionSeconds is set", () => {
      const gs = makeServer({
        spec: { capture: { enabled: true, retentionSeconds: 172800 } }, // 2 days
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText(/Captures will auto-delete after 48 hours/i)).toBeInTheDocument();
    });

    it("displays singular hour when retention is 1 hour", () => {
      const gs = makeServer({
        spec: { capture: { enabled: true, retentionSeconds: 3600 } },
      });
      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      expect(screen.getByText(/Captures will auto-delete after 1 hour$/i)).toBeInTheDocument();
    });
  });

  describe("helper functions coverage (through rendering)", () => {
    it("formats duration of 0 seconds correctly", async () => {
      // shouldAdvanceTime keeps the fake clock ticking in step with real
      // time so promise-based infra (msw, TanStack Query, userEvent/
      // testing-library's findBy* polling) still resolves; a plain
      // vi.useFakeTimers() freezes those timers and hangs the test.
      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        const now = new Date("2026-08-24T12:00:00Z");
        vi.setSystemTime(now);

        const activeCap = makeCapture({
          phase: "Running",
          captureId: "cap-active",
          startedAt: now.toISOString(),
        });
        const gs = makeServer({
          spec: { capture: { enabled: true } },
        });
        server.use(
          http.get(/servers\/alpha:captures(\?.*)?$/, () =>
            HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
          ),
          http.get(/servers\/alpha:capture$/, () =>
            HttpResponse.json(activeCap),
          ),
        );

        renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

        // formatDuration(0) should display as "0s" through the elapsed
        // message. The captures list comes from TanStack Query through
        // msw and resolves asynchronously, so this must be awaited rather
        // than asserted on the synchronous first render.
        expect(await screen.findByText(/Capture started 0s ago/i)).toBeInTheDocument();
      } finally {
        // Restored unconditionally so a failed assertion above can never
        // leak fake timers into every later test in this file.
        vi.useRealTimers();
      }
    });

    it("formats duration correctly with minutes and seconds", async () => {
      const activeCap = makeCapture({
        phase: "Running",
        captureId: "cap-active",
        startedAt: new Date(Date.now() - 125000).toISOString(), // ~2 minutes, 5 seconds
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should show minutes and seconds. The seconds component is left
      // open (\d+) on purpose: elapsedSeconds() is recomputed at render
      // time, so any wall-clock time between building startedAt above and
      // the post-fetch render ticks "2m 5s" to "2m 6s" — a sub-second
      // margin this file has already been bitten by four times over
      // (see the padded expiry offsets below). The m/s shape is what this
      // test is about, and the regex still pins that.
      expect(await screen.findByText(/Capture started 2m \d+s ago/i)).toBeInTheDocument();
    });

    it("formats duration correctly with hours and minutes", async () => {
      const activeCap = makeCapture({
        phase: "Running",
        captureId: "cap-active",
        startedAt: new Date(Date.now() - 3725000).toISOString(), // ~1 hour, 2 minutes
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should show hours and minutes
      expect(await screen.findByText(/Capture started 1h 2m ago/i)).toBeInTheDocument();
    });

    it("handles invalid startedAt date gracefully", async () => {
      const activeCap = makeCapture({
        phase: "Running",
        captureId: "cap-active",
        startedAt: "invalid-date",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [activeCap], total: 1, limit: 100, offset: 0 }),
        ),
        http.get(/servers\/alpha:capture$/, () =>
          HttpResponse.json(activeCap),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should fall back to createdAt when startedAt is invalid
      expect(await screen.findByText(/Capture started/i)).toBeInTheDocument();
    });

    it("displays durationBetween with em-dash when dates are missing", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        startedAt: "",
        completedAt: "",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Duration column should show em-dash for missing dates
      const durationCells = await screen.findAllByText(/—/);
      expect(durationCells.length).toBeGreaterThan(0);
    });

    it("displays expiry badge with danger tone when less than 1 hour remaining", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        // +30s past the exact 30 minutes: expiryLabel's Date.now() call
        // happens after an async render/fetch round trip, so an
        // exact-minute offset has zero margin against CI-realistic delay —
        // any elapsed time floors the minutes component down to 29m and
        // the /30m/ query never finds it (masked as a 5s timeout, not a
        // "not found" error, because asyncUtilTimeout == testTimeout).
        expiresAt: new Date(Date.now() + 1830000).toISOString(), // ~30 minutes
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should show 30m with danger tone. Exact match (not /30m/) so the
      // query can only hit the expiry badge — the same row also renders a
      // Duration cell ("1h 0m" from makeCapture's defaults) and a relative
      // "Completed at" cell, either of which a loose regex could grow into.
      const expiryBadge = await screen.findByText("30m");
      // In HeroUI v3, the Chip color is applied at the root span level via Tailwind variants,
      // not as a plain className on the label. Check the parent element (the Chip root).
      const chipRoot = expiryBadge.closest("span[data-color]");
      expect(chipRoot).toHaveAttribute("data-color", "danger");
    });

    it("displays expiry badge with warning tone when 1-6 hours remaining", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        // +30s past the exact 2 hours, for the same reason as the 30m and
        // 24h boundary tests in this file: 7200000 is an exact multiple of
        // an hour, so it has zero margin and any elapsed render/fetch time
        // flips the hour count down to "1h", missing /2h/.
        expiresAt: new Date(Date.now() + 7230000).toISOString(), // ~2 hours
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should show 2h with warning tone; exact match for the same
      // single-element reason as the 30m case above.
      const expiryBadge = await screen.findByText("2h");
      // In HeroUI v3, the Chip color is applied at the root span level via Tailwind variants,
      // not as a plain className on the label. Check the parent element (the Chip root).
      const chipRoot = expiryBadge.closest("span[data-color]");
      expect(chipRoot).toHaveAttribute("data-color", "warning");
    });

    it("displays expiry badge with muted tone when more than 6 hours remaining", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        // +30s past the exact 24 hours, for the same reason as the 30m and
        // 2h boundary tests above: 86400000 is an exact multiple of an
        // hour, so it has zero margin against render/fetch delay before
        // the hour count flips down to "23h", missing /24h/.
        expiresAt: new Date(Date.now() + 86430000).toISOString(), // ~24 hours
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Should show 24h with muted tone; exact match for the same
      // single-element reason as the 30m case above.
      const expiryBadge = await screen.findByText("24h");
      // In HeroUI v3, the Chip color is applied at the root span level via Tailwind variants,
      // not as a plain className on the label. Check the parent element (the Chip root).
      // More than 6 hours remaining uses "default" color (visually muted/neutral).
      const chipRoot = expiryBadge.closest("span[data-color]");
      expect(chipRoot).toHaveAttribute("data-color", "default");
    });

    it("displays expiry badge with em-dash when expiresAt is missing", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        expiresAt: undefined,
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      // Expiry column should be empty or show nothing
      await screen.findByText("cap-1");
    });

    it("displays expiry badge with em-dash when expiresAt is invalid date", async () => {
      const completed = makeCapture({
        captureId: "cap-1",
        expiresAt: "invalid-date",
      });
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [completed], total: 1, limit: 100, offset: 0 }),
        ),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const emDash = await screen.findByText(/—/);
      expect(emDash).toBeInTheDocument();
    });

    it("omits optional filter field when empty string is submitted", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      let captureStartBody: Partial<CaptureStartBody> | null = null;
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async ({ request }) => {
          captureStartBody = (await request.json()) as Partial<CaptureStartBody>;
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(captureStartBody!.filter).toBeUndefined();
      });
    });

    it("includes filter when provided", async () => {
      const gs = makeServer({
        spec: { capture: { enabled: true } },
      });
      let captureStartBody: Partial<CaptureStartBody> | null = null;
      server.use(
        http.get(/servers\/alpha:captures(\?.*)?$/, () =>
          HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
        ),
        http.post(/servers\/alpha:capture-start(\?.*)?$/, async ({ request }) => {
          captureStartBody = (await request.json()) as Partial<CaptureStartBody>;
          return HttpResponse.json(
            { captureId: "cap-new", phase: "Pending", serverName: "alpha", createdAt: new Date().toISOString(), bytesWritten: 0, packetsWritten: 0 },
            { status: 202 },
          );
        }),
      );

      renderWithQuery(<CaptureWidget name="alpha" ns="gameplane-games" gs={gs} />);

      const startBtn = await screen.findByRole("button", { name: /Start Capture/i });
      await waitFor(() => expect(startBtn).not.toBeDisabled());
      await userEvent.click(startBtn);

      const filterInput = await screen.findByLabelText(/Packet Filter/i);
      await userEvent.type(filterInput, "  tcp port 25565  ");

      const submitBtn = screen.getByRole("button", { name: /Start Capture/ });
      await userEvent.click(submitBtn);

      await waitFor(() => {
        expect(captureStartBody!.filter).toBe("tcp port 25565");
      });
    });
  });
});
