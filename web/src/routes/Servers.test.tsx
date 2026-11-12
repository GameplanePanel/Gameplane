import { afterEach, describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeServer, makeClusterStats } from "@/test/factories";

// TanStack Router's Link needs a router context the test doesn't supply.
// Replace it with a plain anchor — same DOM contract for what we assert.
// Extract search params and build the full href so route-parameter assertions work.
vi.mock("@tanstack/react-router", () => ({
  useLocation: () => ({ search: {} }),
  Link: ({ children, to, search, ...rest }: { children: ReactNode; to: string; search?: Record<string, unknown> } & Record<string, unknown>) => {
    let href = to;
    if (search && Object.keys(search).length > 0) {
      const params = new URLSearchParams();
      Object.entries(search).forEach(([key, value]) => {
        if (value !== undefined && value !== null) {
          params.set(key, String(value));
        }
      });
      href = `${to}?${params.toString()}`;
    }
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  },
  useNavigate: () => vi.fn(),
  useSearch: () => ({}),
  useParams: () => ({}),
}));

import { ServersPage } from "./Servers";

describe("ServersPage", () => {
  it("renders the server list and cluster stats", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "alpha", namespace: "gameplane-games" }, status: { phase: "Running" } }),
            makeServer({ metadata: { name: "beta", namespace: "gameplane-games" }, status: { phase: "Stopped" } }),
          ],
        }),
      ),
      http.get("/cluster/stats", () => HttpResponse.json(makeClusterStats())),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");
    expect(screen.getByText("beta")).toBeInTheDocument();
  });

  it("shows the correct count badge on each status filter tab", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "r1", namespace: "gameplane-games" }, status: { phase: "Running" } }),
            makeServer({ metadata: { name: "r2", namespace: "gameplane-games" }, status: { phase: "Running" } }),
            makeServer({ metadata: { name: "s1", namespace: "gameplane-games" }, status: { phase: "Stopped" } }),
            makeServer({ metadata: { name: "p1", namespace: "gameplane-games" }, status: { phase: "Pending" } }),
          ],
        }),
      ),
      http.get("/cluster/stats", () => HttpResponse.json(makeClusterStats())),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("r1");

    // All = every server (4), Running = phase Running (2), Stopped = Stopped
    // + Suspended + Failed folded together by countByState (1). Pending
    // counts toward All but neither Running nor Stopped.
    const allTab = screen.getByRole("tab", { name: /All/i });
    expect(within(allTab).getByText("4")).toBeInTheDocument();
    const runningTab = screen.getByRole("tab", { name: /Running/i });
    expect(within(runningTab).getByText("2")).toBeInTheDocument();
    const stoppedTab = screen.getByRole("tab", { name: /Stopped/i });
    expect(within(stoppedTab).getByText("1")).toBeInTheDocument();
  });

  // Regression: usedStorageBytes/totalStorageBytes are provisioned-vs-physical,
  // not used-vs-total, so networked storage can legitimately read >100% — that
  // must present as an explicit overcommit state, not a silently broken meter.
  it("flags storage as overcommitted when provisioned exceeds physical capacity", async () => {
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [] })),
      http.get("/cluster/stats", () =>
        HttpResponse.json(makeClusterStats({ usedStorageBytes: 102_000_000_000, totalStorageBytes: 86_000_000_000 })),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("Storage provisioned");
    expect(await screen.findAllByText(/overcommitted/i)).not.toHaveLength(0);
  });

  it("filters by name via the search box", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "alpha", namespace: "gameplane-games" } }),
            makeServer({ metadata: { name: "beta", namespace: "gameplane-games" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");
    const search = screen.getByPlaceholderText(/Search/i);
    await userEvent.type(search, "alpha");
    await waitFor(() => expect(screen.queryByText("beta")).not.toBeInTheDocument());
    expect(screen.getByText("alpha")).toBeInTheDocument();
  });

  it("never sums unknown player counts into a negative total", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            // legacy -1 sentinel and the new null "unknown" — neither may
            // drag the aggregate below zero.
            makeServer({ metadata: { name: "a", namespace: "gameplane-games" }, status: { phase: "Running", agent: { playersOnline: -1 } } }),
            makeServer({ metadata: { name: "b", namespace: "gameplane-games" }, status: { phase: "Running", agent: { playersOnline: null } } }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("a");
    expect(screen.queryByText("-1")).not.toBeInTheDocument();
    expect(screen.queryByText("-2")).not.toBeInTheDocument();
  });

  it("shows CPU and memory usage from the agent heartbeat", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            // Telemetry lives under status.agent (cgroup + statfs); the table
            // shows it as a percent of the limit when a limit is reported.
            makeServer({
              metadata: { name: "metrics-on", namespace: "gameplane-games" },
              status: {
                phase: "Running",
                agent: {
                  cpuMillicores: 500,
                  cpuLimitMillicores: 2000, // 25%
                  memoryBytes: 536870912,
                  memoryLimitBytes: 1073741824, // 50%
                },
              },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("metrics-on");
    expect(screen.getByText("25%")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
  });

  it("falls back to absolute cores when CPU has no limit", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "no-limit", namespace: "gameplane-games" },
              status: { phase: "Running", agent: { cpuMillicores: 1500 } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("no-limit");
    expect(screen.getByText("1.50 cores")).toBeInTheDocument();
  });

  it("renders dashes for CPU and memory when the heartbeat omits them", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "metrics-off", namespace: "gameplane-games" },
              status: { phase: "Running", agent: { playersOnline: 0 } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    const nameCell = await screen.findByText("metrics-off");
    const row = nameCell.closest("tr") as HTMLElement;
    // Both the CPU and Memory cells fall back to "—" (so does Node).
    expect(within(row).getAllByText("—").length).toBeGreaterThanOrEqual(2);
  });

  it("renders empty stats gracefully when /cluster/stats fails", async () => {
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [] })),
      http.get("/cluster/stats", () => HttpResponse.error()),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByRole("heading", { name: "Servers" });
    expect(await screen.findByText(/Inventory are partial/)).toBeInTheDocument();
  });

  it("includes shared servers in the unified list", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "owned" }, status: { phase: "Running" } }),
          ],
        }),
      ),
      http.get("/users/me/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "owned" }, status: { phase: "Running" } }),
            makeServer({ metadata: { name: "shared" }, status: { phase: "Stopped" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("owned");
    expect(screen.queryByText(/Shared with you/i)).not.toBeInTheDocument();
    expect(screen.getByText("shared")).toBeInTheDocument();
  });

  it("does not show 'Shared with you' header when no shared servers", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "owned" }, status: { phase: "Running" } }),
          ],
        }),
      ),
      http.get("/users/me/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "owned" }, status: { phase: "Running" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("owned");
    expect(screen.queryByText(/Shared with you/i)).not.toBeInTheDocument();
  });

  it("filters shared servers by search and phase", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [],
        }),
      ),
      http.get("/users/me/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "shared-alpha" }, status: { phase: "Running" } }),
            makeServer({ metadata: { name: "shared-beta" }, status: { phase: "Stopped" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("shared-alpha");
    const search = screen.getByPlaceholderText(/Search/i);
    await userEvent.type(search, "alpha");
    await waitFor(() => expect(screen.queryByText("shared-beta")).not.toBeInTheDocument());
    expect(screen.getByText("shared-alpha")).toBeInTheDocument();
  });

  it("deduplicates servers by namespace and name", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "dup", namespace: "gameplane-games" },
              status: { phase: "Running" },
            }),
          ],
        }),
      ),
      http.get("/users/me/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "dup", namespace: "gameplane-games" },
              status: { phase: "Running" },
            }),
            makeServer({
              metadata: { name: "shared", namespace: "gameplane-games" },
              status: { phase: "Running" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("dup");
    // "dup" should appear only once in the document (in the main list, not shared)
    const dupElements = screen.getAllByText("dup");
    expect(dupElements).toHaveLength(1);
    // "shared" should appear once in the shared section
    expect(screen.getByText("shared")).toBeInTheDocument();
  });

  it("renders shared servers from non-default namespaces as enabled links with namespace param", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "owned", namespace: "gameplane-games" },
              status: { phase: "Running" },
            }),
          ],
        }),
      ),
      http.get("/users/me/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "owned", namespace: "gameplane-games" },
              status: { phase: "Running" },
            }),
            makeServer({
              metadata: { name: "other-ns-server", namespace: "other-namespace" },
              status: { phase: "Running" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("owned");
    const sharedLink = screen.getByText("other-ns-server").closest("a") as HTMLAnchorElement;
    expect(sharedLink).toBeInTheDocument();
    expect(sharedLink.href).toContain("ns=other-namespace");
  });

  it("renders the Filter button with no badge when no facets are applied", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({ metadata: { name: "alpha", namespace: "gameplane-games" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");
    const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
    expect(filterButton).toBeInTheDocument();
    // Badge should not contain a number when no facets are applied
    expect(within(filterButton).queryByText(/\d/)).not.toBeInTheDocument();
  });

  it("opens the popover and lists distinct games and namespaces", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "alpha", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
            }),
            makeServer({
              metadata: { name: "beta", namespace: "other-ns" },
              spec: { templateRef: { name: "valheim" } },
            }),
            makeServer({
              metadata: { name: "gamma", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");

    const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
    await userEvent.click(filterButton);

    // HeroUI Popover contains checkboxes with the game and namespace names
    expect(await screen.findByRole("checkbox", { name: "minecraft-java" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "valheim" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "gameplane-games" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "other-ns" })).toBeInTheDocument();
  });

  it("filters servers by game when a game is selected and Apply is clicked", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "alpha", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
            }),
            makeServer({
              metadata: { name: "beta", namespace: "gameplane-games" },
              spec: { templateRef: { name: "valheim" } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");

    // Open filter
    const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
    await userEvent.click(filterButton);

    // Select minecraft-java
    const minecraftCheckbox = screen.getByRole("checkbox", { name: "minecraft-java" });
    await userEvent.click(minecraftCheckbox);

    // Click Apply
    const applyButton = screen.getByRole("button", { name: /^Apply$/i });
    await userEvent.click(applyButton);

    // Only alpha should be visible, beta should not
    await waitFor(() => expect(screen.queryByText("beta")).not.toBeInTheDocument());
    expect(screen.getByText("alpha")).toBeInTheDocument();
  });

  it("clears selected facets when Clear is clicked", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "alpha", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
            }),
            makeServer({
              metadata: { name: "beta", namespace: "gameplane-games" },
              spec: { templateRef: { name: "valheim" } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");

    // Open filter
    const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
    await userEvent.click(filterButton);

    // Select minecraft-java
    const minecraftCheckbox = screen.getByRole("checkbox", { name: "minecraft-java" });
    await userEvent.click(minecraftCheckbox);

    // Click Clear (should keep popover open)
    const clearButton = screen.getByRole("button", { name: /^Clear$/i });
    await userEvent.click(clearButton);

    // minecraft-java should no longer be checked
    expect(minecraftCheckbox).not.toBeChecked();
  });

  it("shows count badge when facets are applied", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "alpha", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
            }),
            makeServer({
              metadata: { name: "beta", namespace: "other-ns" },
              spec: { templateRef: { name: "valheim" } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("alpha");

    // Open filter
    const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
    await userEvent.click(filterButton);

    // Select one game
    const minecraftCheckbox = screen.getByRole("checkbox", { name: "minecraft-java" });
    await userEvent.click(minecraftCheckbox);

    // Select one namespace
    const otherNsCheckbox = screen.getByRole("checkbox", { name: "other-ns" });
    await userEvent.click(otherNsCheckbox);

    // Click Apply
    const applyButton = screen.getByRole("button", { name: /^Apply$/i });
    await userEvent.click(applyButton);

    // Badge should show "2" (1 game + 1 namespace) — scope to the Filter
    // button so it doesn't collide with other "2"s (stat tiles, tab counts).
    await waitFor(() => {
      expect(within(filterButton).getByText("2")).toBeInTheDocument();
    });
  });

  it("composes status filter and game facet filter together", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "mc-running", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
              status: { phase: "Running" },
            }),
            makeServer({
              metadata: { name: "mc-stopped", namespace: "gameplane-games" },
              spec: { templateRef: { name: "minecraft-java" } },
              status: { phase: "Stopped" },
            }),
            makeServer({
              metadata: { name: "val-running", namespace: "gameplane-games" },
              spec: { templateRef: { name: "valheim" } },
              status: { phase: "Running" },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("mc-running");

    // Apply game filter for minecraft-java
    const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
    await userEvent.click(filterButton);
    const minecraftCheckbox = screen.getByRole("checkbox", { name: "minecraft-java" });
    await userEvent.click(minecraftCheckbox);
    const applyButton = screen.getByRole("button", { name: /^Apply$/i });
    await userEvent.click(applyButton);

    // Now apply status filter to "Running" (HeroUI Tab)
    const runningTab = screen.getByRole("tab", { name: /Running/i });
    await userEvent.click(runningTab);

    // Only mc-running should be visible
    expect(screen.getByText("mc-running")).toBeInTheDocument();
    expect(screen.queryByText("mc-stopped")).not.toBeInTheDocument();
    expect(screen.queryByText("val-running")).not.toBeInTheDocument();
  });

  // The asleep flag threads through serverRowData -> ServerRow ->
  // ServerLifecycleActions untested before this — cover the Wake/Start
  // swap and the :wake call the same way ServerDetail.test.tsx does.
  it("shows Wake (not Start) on an asleep row and calls the :wake endpoint when clicked", async () => {
    const wakeHandler = vi.fn(() => new HttpResponse(null, { status: 202 }));
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "sleepy", namespace: "gameplane-games" },
              status: {
                phase: "Suspended",
                idle: { asleep: true, asleepSince: "2026-05-07T12:00:00Z" },
              },
            }),
          ],
        }),
      ),
      http.post(/\/servers\/[^/]+:wake$/, wakeHandler),
    );
    renderWithQuery(<ServersPage />);
    const row = (await screen.findByText("sleepy")).closest("tr") as HTMLElement;
    expect(within(row).getByTitle("Wake")).toBeInTheDocument();
    expect(within(row).queryByTitle("Start")).not.toBeInTheDocument();
    await userEvent.click(within(row).getByTitle("Wake"));
    await waitFor(() => expect(wakeHandler).toHaveBeenCalled());
  });

  // F-263: /servers page fans out per allowed namespace and merges results.
  describe("namespace fan-out (F-263)", () => {
    it("fans out /servers per namespace from /namespaces and merges the results", async () => {
      server.use(
        http.get("/namespaces", () =>
          HttpResponse.json({ namespaces: ["gameplane-games", "extra-ns"] }),
        ),
        http.get("/servers", ({ request }) => {
          const url = new URL(request.url);
          const ns = url.searchParams.get("namespace") ?? "gameplane-games";
          if (ns === "extra-ns") {
            return HttpResponse.json({
              items: [makeServer({ metadata: { name: "extra-server", namespace: "extra-ns" } })],
            });
          }
          return HttpResponse.json({
            items: [makeServer({ metadata: { name: "default-server", namespace: "gameplane-games" } })],
          });
        }),
      );
      renderWithQuery(<ServersPage />);
      // Both namespace queries start once /namespaces resolves; wait for
      // each rather than relying on the order MSW answers them in.
      await screen.findByText("extra-server");
      // Merged: both namespaces' servers show up in one list.
      expect(await screen.findByText("default-server")).toBeInTheDocument();
    });

    it("builds the namespace filter from the merged, fanned-out list", async () => {
      server.use(
        http.get("/namespaces", () =>
          HttpResponse.json({ namespaces: ["gameplane-games", "extra-ns"] }),
        ),
        http.get("/servers", ({ request }) => {
          const url = new URL(request.url);
          const ns = url.searchParams.get("namespace") ?? "gameplane-games";
          if (ns === "extra-ns") {
            return HttpResponse.json({
              items: [makeServer({ metadata: { name: "extra-server", namespace: "extra-ns" } })],
            });
          }
          return HttpResponse.json({
            items: [makeServer({ metadata: { name: "default-server", namespace: "gameplane-games" } })],
          });
        }),
      );
      renderWithQuery(<ServersPage />);
      // extra-ns's server (and thus its facet) only appears after the
      // second-wave query resolves — wait for it before opening the popover.
      await screen.findByText("extra-server");

      const filterButton = screen.getByRole("button", { name: /^Filter(?:\s*\d+)?$/i });
      await userEvent.click(filterButton);
      expect(await screen.findByRole("checkbox", { name: "gameplane-games" })).toBeInTheDocument();
      expect(screen.getByRole("checkbox", { name: "extra-ns" })).toBeInTheDocument();
    });

    it("does not blank the page when one namespace's request fails", async () => {
      const brokenNsHandler = vi.fn(() => HttpResponse.error());
      server.use(
        http.get("/namespaces", () =>
          HttpResponse.json({ namespaces: ["gameplane-games", "broken-ns"] }),
        ),
        http.get("/servers", ({ request }) => {
          const url = new URL(request.url);
          const ns = url.searchParams.get("namespace") ?? "gameplane-games";
          if (ns === "broken-ns") {
            return brokenNsHandler();
          }
          return HttpResponse.json({
            items: [makeServer({ metadata: { name: "healthy-server", namespace: "gameplane-games" } })],
          });
        }),
      );
      renderWithQuery(<ServersPage />);
      // The failing namespace must not blank the whole page.
      await screen.findByText("healthy-server");
      // ...and it must actually have been fanned out to, not silently
      // skipped.
      await waitFor(() => expect(brokenNsHandler).toHaveBeenCalled());
      // ...and the partial failure must surface, naming the broken namespace.
      expect(await screen.findByText(/broken-ns.*servers unavailable/i)).toBeInTheDocument();
    });

    // Review finding (F-263 follow-up): {"namespaces": []} is an
    // authoritative "servers:read in no namespace" answer, the normal case
    // for a user with no role binding who only owns/collaborates on
    // servers — not an error. The page must fan out over nothing (no
    // /servers request at all) and show only the Shared with you list, with
    // no error banner and no stuck "Loading…" state.
    it("includes owner-only servers with no error when namespace access is empty", async () => {
      const serversHandler = vi.fn(() => HttpResponse.json({ items: [] }));
      server.use(
        http.get("/namespaces", () => HttpResponse.json({ namespaces: [] })),
        http.get("/servers", serversHandler),
        http.get("/users/me/servers", () =>
          HttpResponse.json({
            items: [makeServer({ metadata: { name: "shared-only" }, status: { phase: "Running" } })],
          }),
        ),
      );
      renderWithQuery(<ServersPage />);
      await screen.findByText("shared-only");
      expect(screen.queryByText(/Shared with you/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/Couldn't load servers in/i)).not.toBeInTheDocument();
      expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
      // Empty namespace list fans out over nothing — no /servers call at all.
      expect(serversHandler).not.toHaveBeenCalled();
    });

    it("behaves like a single-namespace install when /namespaces returns one namespace", async () => {
      const serversHandler = vi.fn(() =>
        HttpResponse.json({
          items: [makeServer({ metadata: { name: "solo-server", namespace: "gameplane-games" } })],
        }),
      );
      server.use(
        http.get("/namespaces", () => HttpResponse.json({ namespaces: ["gameplane-games"] })),
        http.get("/servers", ({ request }) => {
          const url = new URL(request.url);
          expect(url.searchParams.get("namespace")).toBe("gameplane-games");
          return serversHandler();
        }),
      );
      renderWithQuery(<ServersPage />);
      await screen.findByText("solo-server");
      // Exactly one /servers request, for the default namespace — same
      // shape as the pre-fan-out single-namespace behavior.
      expect(serversHandler).toHaveBeenCalledTimes(1);
    });

    it("falls back to the default namespace's servers when /namespaces errors", async () => {
      const serversHandler = vi.fn(({ request }: { request: Request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get("namespace")).toBe("gameplane-games");
        return HttpResponse.json({
          items: [makeServer({ metadata: { name: "fallback-server", namespace: "gameplane-games" } })],
        });
      });
      server.use(
        http.get("/namespaces", () => HttpResponse.json({ error: "boom" }, { status: 500 })),
        http.get("/servers", serversHandler),
      );
      renderWithQuery(<ServersPage />);
      // The /namespaces query retries twice (1s + 2s backoff) before
      // isError, so allow well past the default 5s find timeout.
      await screen.findByText("fallback-server", {}, { timeout: 8000 });
      expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
      expect(serversHandler).toHaveBeenCalledTimes(1);
    }, 10_000);
  });

  it.each(["Pending", "Starting"] as const)("allows stopping a %s server from its fleet row", async (phase) => {
    server.use(http.get("/servers", () => HttpResponse.json({
      items: [makeServer({ metadata: { name: "booting", namespace: "gameplane-games" }, status: { phase } })],
    })));
    renderWithQuery(<ServersPage />);
    const row = (await screen.findByText("booting")).closest("tr") as HTMLElement;
    expect(within(row).getByTitle("Stop")).toBeEnabled();
  });

  // C1: an asleep server is phase Suspended, but :stop is still a real
  // action (it patches spec.suspend=true) — Stop must not be dead here.
  it("keeps Stop enabled for an asleep server", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "sleepy", namespace: "gameplane-games" },
              status: {
                phase: "Suspended",
                idle: { asleep: true, asleepSince: "2026-05-07T12:00:00Z" },
              },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    const row = (await screen.findByText("sleepy")).closest("tr") as HTMLElement;
    expect(within(row).getByTitle("Stop")).not.toBeDisabled();
  });
});

// The page renders either a <table> (desktop) or a stacked card list
// (mobile), decided by useMediaQuery("(max-width: 767px)"). Everywhere else
// in this suite window.matchMedia is left at the global jsdom stub (always
// `matches: false`, see src/test/setup.ts), which is why the table has been
// what every test above exercises. These cases override matchMedia to force
// the mobile branch so ServerCard/StatChip get real coverage too.
describe("ServersPage mobile layout", () => {
  const ORIGINAL_MATCH_MEDIA = window.matchMedia;

  function setMobileViewport() {
    window.matchMedia = ((query: string) => ({
      matches: query === "(max-width: 767px)",
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as typeof window.matchMedia;
  }

  afterEach(() => {
    window.matchMedia = ORIGINAL_MATCH_MEDIA;
  });

  it("renders a stacked card list (with players and memory stats, no lifecycle actions) instead of the table", async () => {
    setMobileViewport();
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "mobile-alpha", namespace: "gameplane-games" },
              status: {
                phase: "Running",
                agent: {
                  cpuMillicores: 500,
                  cpuLimitMillicores: 2000,
                  memoryBytes: 536870912,
                  memoryLimitBytes: 1073741824,
                  playersOnline: 3,
                  playersMax: 10,
                },
              },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    await screen.findByText("mobile-alpha");

    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getByText("3/10")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    // CPU chip removed from mobile card
    expect(screen.queryByText("25%")).not.toBeInTheDocument();
    // Lifecycle action buttons removed from mobile card
    expect(screen.queryByTitle("Start")).not.toBeInTheDocument();
    expect(screen.queryByTitle("Stop")).not.toBeInTheDocument();
    expect(screen.queryByTitle("Restart")).not.toBeInTheDocument();
  });

  it("includes owner-only servers in the mobile list", async () => {
    setMobileViewport();
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [] })),
      http.get("/users/me/servers", () =>
        HttpResponse.json({
          items: [makeServer({ metadata: { name: "mobile-shared" }, status: { phase: "Stopped" } })],
        }),
      ),
    );
    renderWithQuery(<ServersPage />);
    expect(await screen.findByText("mobile-shared")).toBeInTheDocument();
    expect(screen.getByText("mobile-shared")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("shows the 'No servers match' card when the list is empty", async () => {
    setMobileViewport();
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [] })),
      http.get("/users/me/servers", () => HttpResponse.json({ items: [] })),
    );
    renderWithQuery(<ServersPage />);
    expect(await screen.findByText("No servers match.")).toBeInTheDocument();
  });

  // F-126: a viewer clicking Start on a row got a silent 403 — the mutation
  // had no error handler at all, so the failure never reached the page.
  it("shows an error banner when a row action is forbidden (viewer 403)", async () => {
    server.use(
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            makeServer({
              metadata: { name: "locked", namespace: "gameplane-games" },
              status: { phase: "Stopped" },
            }),
          ],
        }),
      ),
      http.post(/\/servers\/[^/]+:start$/, () =>
        HttpResponse.text("forbidden: viewers cannot start servers", { status: 403 }),
      ),
    );
    renderWithQuery(<ServersPage />);
    const row = (await screen.findByText("locked")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByTitle("Start"));
    expect(await screen.findByText(/forbidden: viewers cannot start servers/i)).toBeInTheDocument();
  });
});
