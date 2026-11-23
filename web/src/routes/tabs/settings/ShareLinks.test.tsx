import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type { ReactNode } from "react";
import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { ShareLinksSection } from "./ShareLinks";
import type { ShareLink } from "@/types";

const mockLinks: ShareLink[] = [
  {
    id: "link-1",
    createdAt: "2026-07-28T00:00:00Z",
    expiresAt: null,
    canStart: true,
  },
  {
    id: "link-2",
    createdAt: "2026-06-01T00:00:00Z",
    expiresAt: "2026-06-08T00:00:00Z",
    canStart: false,
  },
  {
    id: "link-3",
    createdAt: "2026-05-12T00:00:00Z",
    expiresAt: "2026-05-19T00:00:00Z",
    canStart: true,
  },
];

const ONE_DAY_MS = 24 * 60 * 60 * 1000;

// Local-time "YYYY-MM-DD" for `days` days from now (negative/zero allowed),
// matching the format the create dialog's native date input expects. Always
// computed relative to the real clock (never a hardcoded calendar date) so
// the custom-date tests (c)/(d) don't rot as time passes.
function isoDateNDaysFromNow(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

// Mirrors ShareLinks.tsx's own customDateToExpiresAt: end of the local
// calendar day (23:59:59.999 local time) for a "YYYY-MM-DD" date, converted
// to the UTC instant a test can compare against the captured request body.
function endOfLocalDayISO(isoDate: string): string {
  const [year, month, day] = isoDate.split("-").map(Number);
  return new Date(year, month - 1, day, 23, 59, 59, 999).toISOString();
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false },
    mutations: { retry: false },
  },
});

const Wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={queryClient}><ResourceTargetProvider target={{ cluster: "local", name: "mc-survival" }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>{children}</ResourceTargetProvider></QueryClientProvider>
);

const server = setupServer(
  http.get(/\/servers\/[^/]+:shares$/, () =>
    HttpResponse.json(mockLinks),
  ),
  http.post(/\/servers\/[^/]+:shares$/, async ({ request }) => {
    const body = (await request.json()) as { expiresAt?: string; neverExpires?: boolean; canStart: boolean };
    const newLink: ShareLink = {
      id: `link-${Date.now()}`,
      createdAt: new Date().toISOString(),
      expiresAt: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
      canStart: body.canStart,
      token: "test-token-" + Math.random().toString(36).slice(2),
    };
    return HttpResponse.json(newLink);
  }),
  http.delete("/servers/:name/shares/:id", () =>
    new HttpResponse(null, { status: 204 }),
  ),
);

describe("ShareLinksSection", () => {
  beforeEach(() => {
    server.listen();
    queryClient.clear();
    vi.clearAllMocks();
  });

  afterEach(() => {
    // Per-test server.use() overrides (e.g. the empty-state and error-case
    // handlers below) must not leak into later tests — server.close() alone
    // doesn't clear them.
    server.resetHandlers();
    server.close();
  });

  // List state tests
  it("renders title and description", async () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    expect(screen.getByText("Share links")).toBeInTheDocument();
    expect(
      screen.getByText(/Let people without a Gameplane account/),
    ).toBeInTheDocument();
  });

  it("renders Create link button in header", () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const btn = screen.getByRole("button", { name: /Create link/ });
    expect(btn).toBeInTheDocument();
  });

  // Empty state test
  it("shows empty state when no links exist", async () => {
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json([]),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("No share links yet")).toBeInTheDocument();
    });
    expect(
      screen.getByText(/Create a link so a friend without a Gameplane/),
    ).toBeInTheDocument();
  });

  // Table rendering tests
  it("renders table with share links when they exist", async () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByRole("grid")).toBeInTheDocument();
      // Table should contain date strings
      expect(screen.getByText("Jul 28, 2026")).toBeInTheDocument();
      expect(screen.getByText("Jun 1, 2026")).toBeInTheDocument();
    });
  });

  it("renders column headers", async () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("Created")).toBeInTheDocument();
      expect(screen.getByText("Expires")).toBeInTheDocument();
    });
  });

  it("displays can-start capability as a chip on inactive links", async () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    expect(await screen.findByRole("gridcell", { name: "Can start" })).toBeInTheDocument();
  });

  it("displays view-only capability as 'View only' chip", async () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("View only")).toBeInTheDocument();
    });
  });

  it("renders Revoke only for active links", async () => {
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      const revokeButtons = screen.getAllByRole("button", { name: "Revoke" });
      expect(revokeButtons).toHaveLength(1);
    });
    // The two expired links show a muted dash instead of a Revoke button.
    expect(screen.getAllByText("—")).toHaveLength(2);
  });

  it("changes only the selected link when active links have the same dates and permission", async () => {
    const user = userEvent.setup();
    let canStart = true;
    const updates: boolean[] = [];
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json([mockLinks[0], { ...mockLinks[0], id: "link-other", canStart }, ...mockLinks.slice(1)])),
      http.patch("/servers/mc-survival/shares/link-other", async ({ request }) => {
        const body = await request.json() as { canStart: boolean };
        canStart = body.canStart;
        updates.push(canStart);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const other = await screen.findByRole("switch", { name: "Allow anyone with link link-1 to wake the server" });
    const toggle = screen.getByRole("switch", { name: "Allow anyone with link link-other to wake the server" });
    const row = screen.getByRole("row", { name: /link-other/ });
    expect(within(row).getByText("link-other")).toBeVisible();
    expect(within(row).getByRole("switch")).toBe(toggle);
    expect(toggle).toBeChecked();
    expect(screen.getAllByRole("switch")).toHaveLength(2);
    for (const enabled of [false, true]) {
      await user.click(toggle);
      await waitFor(() => {
        expect(toggle).toBeEnabled();
        expect(toggle).toHaveProperty("checked", enabled);
      });
      expect(other).toBeChecked();
    }
    expect(updates).toEqual([false, true]);
  });

  it("keeps the current permission visible when the API rejects an edit", async () => {
    server.use(http.patch("/servers/:name/shares/:id", () =>
      HttpResponse.json({ error: "Permission denied" }, { status: 403 })));
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const toggle = await screen.findByRole("switch", { name: "Allow anyone with link link-1 to wake the server" });
    await userEvent.setup().click(toggle);
    expect(await screen.findByRole("alert")).toHaveTextContent("Permission denied");
    expect(toggle).toBeChecked();
  });

  it("keeps the capability read-only for a non-owner", async () => {
    render(
      <QueryClientProvider client={queryClient}>
        <ResourceTargetProvider target={{ cluster: "local", name: "mc-survival" }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: false, isCollaborator: false }}>
          <ShareLinksSection name="mc-survival" />
        </ResourceTargetProvider>
      </QueryClientProvider>
    );
    expect(await screen.findByText("Active")).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  // Create dialog tests
  it("opens create dialog when Create link button is clicked", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
  });

  it("shows expiry options in create dialog", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Expires in")).toBeInTheDocument();
    });
  });

  it("includes allow-start switch in create dialog", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Allow starting the server")).toBeInTheDocument();
    });
  });

  it("closes create dialog on Cancel", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
    const cancelBtn = screen.getByRole("button", { name: "Cancel" });
    await user.click(cancelBtn);
    await waitFor(() => {
      expect(screen.queryByText("Create share link for mc-survival")).not.toBeInTheDocument();
    });
  });

  it("creates link with selected expiry and canStart", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);

    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });

    // Toggle allow-start switch (should enable canStart capability)
    const allowStartSwitch = screen.getByRole("switch", {
      name: "Allow starting the server",
    });
    await user.click(allowStartSwitch);

    // Drive the HeroUI Select: open it (default is "30 days") and pick a
    // different option, "90 days", so the test exercises an actual
    // selection change rather than re-picking the already-selected value.
    const expiryTrigger = screen.getByRole("button", { name: /30 days/ });
    await user.click(expiryTrigger);
    const ninetyDaysOption = await screen.findByRole("option", { name: "90 days" });
    await user.click(ninetyDaysOption);

    // Submit with the selected expiry
    const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
    await user.click(createConfirmBtn);

    // Should open created dialog
    await waitFor(() => {
      expect(screen.getByText("Share link created")).toBeInTheDocument();
    });
  });

  it("shows error on failed create", async () => {
    const user = userEvent.setup();
    server.use(
      http.post(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json({ error: "Permission denied" }, { status: 403 }),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
    const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
    await user.click(createConfirmBtn);
    await waitFor(() => {
      // errorText() surfaces the API's JSON {error} body verbatim rather
      // than the "Failed to create link" fallback when one is present.
      expect(screen.getByText("Permission denied")).toBeInTheDocument();
    });
  });

  // Created dialog tests
  it("shows the URL once and the same management ID in the created dialog and the table", async () => {
    const user = userEvent.setup();
    const created = { ...mockLinks[0], id: "friends-link", token: "friends-token" };
    let rows = mockLinks;
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () => HttpResponse.json(rows)),
      http.post(/\/servers\/[^/]+:shares$/, () => {
        rows = [{ ...mockLinks[0], id: created.id }, ...mockLinks];
        return HttpResponse.json(created);
      }),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
    const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
    await user.click(createConfirmBtn);
    await waitFor(() => {
      expect(screen.getByText("Share link created")).toBeInTheDocument();
      expect(screen.getByText(/You will not see this link again/)).toBeInTheDocument();
    });
    const dialog = screen.getByRole("dialog", { name: "Share link created" });
    expect(within(dialog).getByText(/\/share\/friends-token$/)).toBeVisible();
    expect(within(dialog).getByText("friends-link")).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(await screen.findByRole("switch", { name: "Allow anyone with link friends-link to wake the server" })).toBeInTheDocument();
    expect(screen.getByText("friends-link")).toBeVisible();
    expect(screen.queryByText(/friends-token/)).not.toBeInTheDocument();
  });

  it("copies link URL to clipboard", async () => {
    const user = userEvent.setup();
    const clipboardSpy = vi.spyOn(navigator.clipboard, "writeText");

    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
    const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
    await user.click(createConfirmBtn);

    await waitFor(() => {
      expect(screen.getByText("Share link created")).toBeInTheDocument();
    });

    const copyBtn = screen.getByRole("button", { name: /Copy link/ });
    await user.click(copyBtn);

    await waitFor(() => {
      expect(clipboardSpy).toHaveBeenCalled();
    });
    clipboardSpy.mockRestore();
  });

  it("closes created dialog on Done", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const createBtn = screen.getByRole("button", { name: /Create link/ });
    await user.click(createBtn);
    await waitFor(() => {
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
    const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
    await user.click(createConfirmBtn);

    await waitFor(() => {
      expect(screen.getByText("Share link created")).toBeInTheDocument();
    });

    const doneBtn = screen.getByRole("button", { name: "Done" });
    await user.click(doneBtn);

    await waitFor(() => {
      expect(screen.queryByText("Share link created")).not.toBeInTheDocument();
    });
  });

  // Revoke dialog tests
  it("opens revoke dialog when Revoke button is clicked", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const revokeButtons = await waitFor(() =>
      screen.getAllByRole("button", { name: "Revoke" }),
    );
    await user.click(revokeButtons[0]);
    await waitFor(() => {
      expect(screen.getByText("Revoke this share link?")).toBeInTheDocument();
    });
  });

  it("shows confirmation message in revoke dialog", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const revokeButtons = await waitFor(() =>
      screen.getAllByRole("button", { name: "Revoke" }),
    );
    await user.click(revokeButtons[0]);
    await waitFor(() => {
      expect(
        screen.getByText(/will immediately lose access to/),
      ).toBeInTheDocument();
    });
  });

  it("closes revoke dialog on Cancel", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const revokeButtons = await waitFor(() =>
      screen.getAllByRole("button", { name: "Revoke" }),
    );
    await user.click(revokeButtons[0]);
    await waitFor(() => {
      expect(screen.getByText("Revoke this share link?")).toBeInTheDocument();
    });
    const cancelBtn = screen.getAllByRole("button", { name: "Cancel" })[0];
    await user.click(cancelBtn);
    await waitFor(() => {
      expect(screen.queryByText("Revoke this share link?")).not.toBeInTheDocument();
    });
  });

  it("revokes link on confirmation", async () => {
    const user = userEvent.setup();
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const revokeButtons = await waitFor(() =>
      screen.getAllByRole("button", { name: "Revoke" }),
    );
    await user.click(revokeButtons[0]);
    await waitFor(() => {
      expect(screen.getByText("Revoke this share link?")).toBeInTheDocument();
    });
    const revokeConfirmBtn = screen.getByRole("button", { name: "Revoke link" });
    await user.click(revokeConfirmBtn);
    await waitFor(() => {
      expect(screen.queryByText("Revoke this share link?")).not.toBeInTheDocument();
    });
  });

  it("shows error on failed revoke", async () => {
    const user = userEvent.setup();
    server.use(
      http.delete("/servers/:name/shares/:id", () =>
        HttpResponse.json({ error: "Permission denied" }, { status: 403 }),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    const revokeButtons = await waitFor(() =>
      screen.getAllByRole("button", { name: "Revoke" }),
    );
    await user.click(revokeButtons[0]);
    await waitFor(() => {
      expect(screen.getByText("Revoke this share link?")).toBeInTheDocument();
    });
    const revokeConfirmBtn = screen.getByRole("button", { name: "Revoke link" });
    await user.click(revokeConfirmBtn);
    await waitFor(() => {
      // errorText() surfaces the API's JSON {error} body verbatim rather
      // than the "Failed to revoke link" fallback when one is present.
      expect(screen.getByText("Permission denied")).toBeInTheDocument();
    });
  });

  // Multi-cluster tests
  it("accepts namespace prop", () => {
    render(<ShareLinksSection name="mc-survival" ns="custom-ns" />, { wrapper: Wrapper });
    expect(screen.getByText("Share links")).toBeInTheDocument();
  });

  // Status determination tests
  it("shows Active status for non-expired links", async () => {
    // The fixture links' expiry dates are fixed calendar dates, so they
    // eventually fall into the past relative to the real clock. Override
    // with a link that expires relative to "now" so this test keeps
    // asserting the intended behavior (status derived from expiresAt vs.
    // the current time) instead of drifting into a stale-fixture failure.
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json([
          {
            id: "active-link",
            createdAt: new Date().toISOString(),
            expiresAt: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
            canStart: true,
          },
        ]),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("Active")).toBeInTheDocument();
    });
  });

  it("shows Expired status for past-expiry links", async () => {
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json([
          {
            id: "expired-link",
            createdAt: "2026-01-01T00:00:00Z",
            expiresAt: "2024-01-01T00:00:00Z",
            canStart: false,
          },
        ]),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("Expired")).toBeInTheDocument();
    });
  });

  it("renders Revoked status for revoked links", async () => {
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json([
          {
            id: "revoked-link",
            createdAt: "2026-05-12T00:00:00Z",
            expiresAt: "2026-05-19T00:00:00Z",
            revokedAt: "2026-05-13T10:30:00Z",
            canStart: true,
          },
        ]),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("Revoked")).toBeInTheDocument();
    });
    expect(screen.queryByText("Active")).not.toBeInTheDocument();
    expect(screen.queryByText("Expired")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Revoke" })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  // T029: expiry-choice request shapes, warnings, and client-side blocking.
  describe("expiry choice request shapes (T029)", () => {
    // (a) The four day-count presets: each must send an absolute `expiresAt`
    // computed client-side, with no `neverExpires` field, and no other
    // preset's day count. Captures the outgoing request body the same way
    // AdminSettings.test.tsx does (a `vi.fn()` POST handler stashing
    // `request.json()` into a variable this test then asserts on).
    it.each([
      ["15 days", 15],
      ["30 days", 30],
      ["60 days", 60],
      ["90 days", 90],
    ])("preset %s sends an absolute expiresAt ~%d days out", async (label, days) => {
      const user = userEvent.setup();
      let capturedBody: Record<string, unknown> | null = null;
      const postHandler = vi.fn(async ({ request }: { request: Request }) => {
        capturedBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          id: "link-new",
          createdAt: new Date().toISOString(),
          expiresAt: new Date(Date.now() + 7 * ONE_DAY_MS).toISOString(),
          canStart: false,
          token: "test-token",
        });
      });
      server.use(http.post(/\/servers\/[^/]+:shares$/, postHandler));

      render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
      await user.click(screen.getByRole("button", { name: /Create link/ }));
      await waitFor(() => {
        expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
      });

      // 30 days is already the default selection; only drive the Select for
      // the other presets so this exercises a real selection change there.
      if (label !== "30 days") {
        const expiryTrigger = screen.getByRole("button", { name: /30 days/ });
        await user.click(expiryTrigger);
        const option = await screen.findByRole("option", { name: label });
        await user.click(option);
      }

      await user.click(screen.getByRole("button", { name: "Create link" }));
      await waitFor(() => expect(postHandler).toHaveBeenCalled());

      expect(capturedBody).not.toBeNull();
      const body = capturedBody as unknown as {
        expiresAt?: string;
        neverExpires?: boolean;
      };
      expect(body.neverExpires).toBeUndefined();
      // OD-1 (settled 2026-09-20): a preset of N days is a calendar date,
      // end-of-day local, exactly like a custom date (OD-2) — deterministic,
      // so this is an exact match, not a tolerance window.
      expect(body.expiresAt).toBe(endOfLocalDayISO(isoDateNDaysFromNow(days)));
    });

    // (a)+(b) "No expiry" shows the FR-002 warning and sends
    // `neverExpires: true` with no `expiresAt` field at all.
    it('"No expiry" shows the FR-002 warning and sends neverExpires: true', async () => {
      const user = userEvent.setup();
      let capturedBody: Record<string, unknown> | null = null;
      const postHandler = vi.fn(async ({ request }: { request: Request }) => {
        capturedBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          id: "link-new",
          createdAt: new Date().toISOString(),
          expiresAt: null,
          canStart: false,
          token: "test-token",
        });
      });
      server.use(http.post(/\/servers\/[^/]+:shares$/, postHandler));

      render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
      await user.click(screen.getByRole("button", { name: /Create link/ }));
      await waitFor(() => {
        expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
      });

      const expiryTrigger = screen.getByRole("button", { name: /30 days/ });
      await user.click(expiryTrigger);
      const neverOption = await screen.findByRole("option", { name: "No expiry" });
      await user.click(neverOption);

      await waitFor(() => {
        expect(
          screen.getByText("This link works until you revoke it."),
        ).toBeInTheDocument();
      });

      await user.click(screen.getByRole("button", { name: "Create link" }));
      await waitFor(() => expect(postHandler).toHaveBeenCalled());

      expect(capturedBody).not.toBeNull();
      const body = capturedBody as unknown as {
        expiresAt?: string;
        neverExpires?: boolean;
      };
      expect(body.neverExpires).toBe(true);
      expect(body.expiresAt).toBeUndefined();
    });

    // (a) "Custom": a normal (non-long-lived) future date sends an absolute
    // expiresAt at the end of that local calendar day (OD-2), with no
    // neverExpires field and no long-lived (OD-6) warning.
    it('"Custom" with a normal future date sends end-of-day expiresAt', async () => {
      const user = userEvent.setup();
      let capturedBody: Record<string, unknown> | null = null;
      const postHandler = vi.fn(async ({ request }: { request: Request }) => {
        capturedBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          id: "link-new",
          createdAt: new Date().toISOString(),
          expiresAt: new Date(Date.now() + 10 * ONE_DAY_MS).toISOString(),
          canStart: false,
          token: "test-token",
        });
      });
      server.use(http.post(/\/servers\/[^/]+:shares$/, postHandler));

      const customDate = isoDateNDaysFromNow(10);

      render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
      await user.click(screen.getByRole("button", { name: /Create link/ }));
      await waitFor(() => {
        expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
      });

      const expiryTrigger = screen.getByRole("button", { name: /30 days/ });
      await user.click(expiryTrigger);
      const customOption = await screen.findByRole("option", { name: "Custom" });
      await user.click(customOption);

      const dateInput = await screen.findByLabelText("Expires on");
      fireEvent.change(dateInput, { target: { value: customDate } });

      expect(
        screen.queryByText(/Long-lived link/),
      ).not.toBeInTheDocument();

      const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
      await waitFor(() => expect(createConfirmBtn).not.toBeDisabled());
      await user.click(createConfirmBtn);
      await waitFor(() => expect(postHandler).toHaveBeenCalled());

      expect(capturedBody).not.toBeNull();
      const body = capturedBody as unknown as {
        expiresAt?: string;
        neverExpires?: boolean;
      };
      expect(body.neverExpires).toBeUndefined();
      expect(body.expiresAt).toBe(endOfLocalDayISO(customDate));
    });

    // (c) A custom date 400 days out shows the OD-6 long-lived warning and
    // the request still submits successfully with the correct expiresAt.
    it("custom date 400 days out shows the OD-6 long-lived warning and still submits", async () => {
      const user = userEvent.setup();
      let capturedBody: Record<string, unknown> | null = null;
      const postHandler = vi.fn(async ({ request }: { request: Request }) => {
        capturedBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          id: "link-new",
          createdAt: new Date().toISOString(),
          expiresAt: new Date(Date.now() + 400 * ONE_DAY_MS).toISOString(),
          canStart: false,
          token: "test-token",
        });
      });
      server.use(http.post(/\/servers\/[^/]+:shares$/, postHandler));

      const farDate = isoDateNDaysFromNow(400);

      render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
      await user.click(screen.getByRole("button", { name: /Create link/ }));
      await waitFor(() => {
        expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
      });

      const expiryTrigger = screen.getByRole("button", { name: /30 days/ });
      await user.click(expiryTrigger);
      const customOption = await screen.findByRole("option", { name: "Custom" });
      await user.click(customOption);

      const dateInput = await screen.findByLabelText("Expires on");
      fireEvent.change(dateInput, { target: { value: farDate } });

      await waitFor(() => {
        expect(
          screen.getByText(
            "Long-lived link — it stays valid for over a year unless you revoke it.",
          ),
        ).toBeInTheDocument();
      });

      const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
      await waitFor(() => expect(createConfirmBtn).not.toBeDisabled());
      await user.click(createConfirmBtn);
      await waitFor(() => expect(postHandler).toHaveBeenCalled());

      expect(capturedBody).not.toBeNull();
      const body = capturedBody as unknown as { expiresAt?: string };
      expect(body.expiresAt).toBe(endOfLocalDayISO(farDate));
    });

    // (d) A custom date of today or earlier is blocked client-side: the
    // submit button stays disabled and no request is ever sent — not merely
    // that an error later appears.
    it("custom date of today is blocked client-side and never sends a request", async () => {
      const user = userEvent.setup();
      const postHandler = vi.fn(async () =>
        HttpResponse.json({
          id: "link-new",
          createdAt: new Date().toISOString(),
          expiresAt: new Date().toISOString(),
          canStart: false,
          token: "test-token",
        }),
      );
      server.use(http.post(/\/servers\/[^/]+:shares$/, postHandler));

      const todayDate = isoDateNDaysFromNow(0);

      render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
      await user.click(screen.getByRole("button", { name: /Create link/ }));
      await waitFor(() => {
        expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
      });

      const expiryTrigger = screen.getByRole("button", { name: /30 days/ });
      await user.click(expiryTrigger);
      const customOption = await screen.findByRole("option", { name: "Custom" });
      await user.click(customOption);

      const dateInput = await screen.findByLabelText("Expires on");
      fireEvent.change(dateInput, { target: { value: todayDate } });

      const createConfirmBtn = screen.getByRole("button", { name: "Create link" });
      await waitFor(() => expect(createConfirmBtn).toBeDisabled());

      // Attempting the click on a genuinely disabled button is a no-op (no
      // click event is dispatched), which is exactly what this test needs
      // to prove: not just that an error shows, but that the request is
      // never made at all.
      await user.click(createConfirmBtn);

      expect(postHandler).not.toHaveBeenCalled();
      expect(screen.getByText("Create share link for mc-survival")).toBeInTheDocument();
    });
  });

  // (e) A NULL-expiry mock row renders "Never" in the Expires column and is
  // never reported "Expired", regardless of how much time passes.
  it('renders "Never" and never "Expired" for a null-expiry link', async () => {
    server.use(
      http.get(/\/servers\/[^/]+:shares$/, () =>
        HttpResponse.json([
          {
            id: "never-link",
            createdAt: "2026-01-01T00:00:00Z",
            expiresAt: null,
            canStart: false,
          },
        ]),
      ),
    );
    render(<ShareLinksSection name="mc-survival" />, { wrapper: Wrapper });
    await waitFor(() => {
      expect(screen.getByText("Never")).toBeInTheDocument();
    });
    expect(screen.queryByText("Expired")).not.toBeInTheDocument();
    // The status chip for a never-expiring link is "Active", never "Expired".
    expect(screen.getByText("Active")).toBeInTheDocument();
  });
});
