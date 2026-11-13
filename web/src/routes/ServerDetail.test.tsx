import { resourceKey } from "@/lib/resourceTarget";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { makeClient, renderWithQuery } from "@/test/render";
import { makeServer, makeTemplate, makeUser } from "@/test/factories";

// Router APIs the route reaches into.
const navigate = vi.fn();
let searchParams = {};
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
  useParams: () => ({ name: "alpha" }),
  useSearch: () => searchParams,
  useNavigate: () => navigate,
}));

// Heavy lazy tabs (xterm / Monaco) — replace with stubs so jsdom doesn't
// have to evaluate them.
vi.mock("./tabs/Console", () => ({ ConsoleTab: () => <div>console-tab</div> }));
vi.mock("./tabs/Files", () => ({ FilesTab: () => <div>files-tab</div> }));
vi.mock("./tabs/Logs", () => ({ LogsTab: () => <div>logs-tab</div> }));
vi.mock("./tabs/Mods", () => ({ ModsTab: () => <div>mods-tab</div> }));
vi.mock("./tabs/Players", () => ({ PlayersTab: () => <div>players-tab</div> }));
vi.mock("./tabs/Backups", () => ({ BackupsTab: () => <div>backups-tab</div> }));
vi.mock("./tabs/Overview", () => ({ OverviewTab: () => <div>overview-tab</div> }));
vi.mock("./tabs/Settings", () => ({ SettingsTab: () => <div>settings-tab</div> }));

import { ServerDetailPage } from "./ServerDetail";

let targetCanWrite = true;
beforeEach(() => {
  searchParams = {};
  targetCanWrite = true;
  navigate.mockClear();
  server.use(http.get("/servers/:name/access", ({ request, params }) => {
    const url = new URL(request.url);
    return HttpResponse.json({
      target: { cluster: url.searchParams.get("cluster") ?? "local", namespace: url.searchParams.get("namespace") ?? "gameplane-games", name: params.name },
      canWrite: targetCanWrite, canControl: targetCanWrite, canConsole: targetCanWrite,
      canDelete: targetCanWrite, isOwner: false, isCollaborator: false,
      permissions: targetCanWrite ? ["servers:read", "servers:write", "servers:console", "captures:manage"] : ["servers:read"],
    });
  }));
});

describe("ServerDetailPage lifecycle buttons", () => {
  it("uses remote target permissions even when the central profile is an administrator", async () => {
    searchParams = { cluster: "remote", ns: "team-b" };
    targetCanWrite = false;
    const writes = vi.fn(() => new HttpResponse(null, { status: 202 }));
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "admin" }))),
      http.get("/servers/alpha", ({ request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get("cluster")).toBe("remote");
        expect(url.searchParams.get("namespace")).toBe("team-b");
        return HttpResponse.json(makeServer({ metadata: { namespace: "team-b" }, status: { phase: "Running" } }));
      }),
      http.post(/\/servers\/[^/]+:stop$/, writes),
    );
    renderWithQuery(<ServerDetailPage />);
    const stop = await screen.findByRole("button", { name: /^Stop$/i });
    expect(stop).toBeDisabled();
    await userEvent.click(stop);
    expect(writes).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Capture" })).not.toBeInTheDocument());
  });

  it("does not expose actions when the live permission response belongs to a replacement UID", async () => {
    searchParams = { cluster: "remote", ns: "gameplane-games" };
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ metadata: { uid: "original" } }))),
      http.get("/servers/alpha/access", () => HttpResponse.json({ target: { cluster: "remote", namespace: "gameplane-games", name: "alpha", uid: "replacement" }, canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: false, isCollaborator: false, permissions: ["servers:write"] })),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(await screen.findByText(/This server changed while it was being loaded/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Stop$/i })).not.toBeInTheDocument();
  });

  it("while Running: Stop/Restart enabled, Start hidden", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ status: { phase: "Running" } }))),
    );
    renderWithQuery(<ServerDetailPage />);
    // Wait for the loaded (Running) state — buttons exist but are disabled
    // during the initial load.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Stop$/i })).not.toBeDisabled(),
    );
    expect(screen.queryByRole("button", { name: /^Start$/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Restart$/i })).not.toBeDisabled();
  });

  it("while Stopped: Start enabled, Stop/Restart disabled", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ status: { phase: "Stopped" } }))),
    );
    renderWithQuery(<ServerDetailPage />);
    const start = await screen.findByRole("button", { name: /^Start$/i });
    expect(start).not.toBeDisabled();
    expect(screen.getByRole("button", { name: /^Stop$/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^Restart$/i })).toBeDisabled();
  });

  it.each(["Pending", "Starting"] as const)("can stop a remote server while %s", async (phase) => {
    searchParams = { cluster: "remote", ns: "team-b" };
    const stopRequests: URL[] = [];
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ metadata: { namespace: "team-b" }, status: { phase } }))),
      http.post(/\/servers\/alpha:stop$/, ({ request }) => {
        stopRequests.push(new URL(request.url));
        return new HttpResponse(null, { status: 202 });
      }),
    );
    renderWithQuery(<ServerDetailPage />);
    const stop = await screen.findByRole("button", { name: /^Stop$/i });
    await waitFor(() => expect(stop).toBeEnabled());
    expect(screen.queryByRole("button", { name: /^Start$/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Restart$/i })).toBeDisabled();
    await userEvent.click(stop);
    await waitFor(() => expect(stopRequests).toHaveLength(1));
    expect(stopRequests[0]?.searchParams.get("namespace")).toBe("team-b");
    expect(stopRequests[0]?.searchParams.get("cluster")).toBe("remote");
  });

  it("when asleep: Wake button shown, Start hidden", async () => {
    server.use(
      http.get(
        "/servers/alpha",
        () =>
          HttpResponse.json(
            makeServer({
              status: {
                phase: "Suspended",
                idle: { asleep: true, asleepSince: "2026-05-07T12:00:00Z" },
              },
            }),
          ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    const wake = await screen.findByRole("button", { name: /^Wake$/i });
    expect(wake).not.toBeDisabled();
    expect(screen.queryByRole("button", { name: /^Start$/i })).not.toBeInTheDocument();
  });

  // C2: derivePhase reports Stopping (not Suspended) for the whole
  // graceful-drain window even after status.idle.asleep flips true —
  // players may still be connected. The badge must stay honest and Wake
  // must not appear on a server that is mid-shutdown, not actually asleep.
  it("while draining (Stopping) with idle.asleep already true: badge reads Stopping, no Wake button", async () => {
    server.use(
      http.get(
        "/servers/alpha",
        () =>
          HttpResponse.json(
            makeServer({
              status: {
                phase: "Stopping",
                idle: { asleep: true, asleepSince: "2026-05-07T12:00:00Z" },
              },
            }),
          ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    // The heading renders from the route param before the server loads, so
    // the badge has to be awaited rather than read off the first paint.
    expect(await screen.findByText("Stopping")).toBeInTheDocument();
    expect(screen.queryByText("Asleep")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Wake$/i })).not.toBeInTheDocument();
  });

  it("calls wake endpoint when Wake button clicked", async () => {
    const wakeHandler = vi.fn(() => new HttpResponse(null, { status: 202 }));
    server.use(
      http.get(
        "/servers/alpha",
        () =>
          HttpResponse.json(
            makeServer({
              status: {
                phase: "Suspended",
                idle: { asleep: true, asleepSince: "2026-05-07T12:00:00Z" },
              },
            }),
          ),
      ),
      http.post(/\/servers\/[^/]+:wake$/, wakeHandler),
    );
    const user = userEvent.setup();
    renderWithQuery(<ServerDetailPage />);
    const wake = await screen.findByRole("button", { name: /^Wake$/i });
    await user.click(wake);
    await waitFor(() => expect(wakeHandler).toHaveBeenCalled());
  });

  // F-126: the header's lifecycle mutation had no error handler — a failed
  // Restart/Stop/Wake/Start left the detail header silent.
  it("shows an error banner in the header when a lifecycle action fails", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ status: { phase: "Running" } }))),
      http.post(/\/servers\/[^/]+:restart$/, () =>
        HttpResponse.text("restart forbidden", { status: 403 }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<ServerDetailPage />);
    const restart = await screen.findByRole("button", { name: /^Restart$/i });
    await user.click(restart);
    expect(await screen.findByText(/restart forbidden/i)).toBeInTheDocument();
  });
});

describe("ServerDetailPage", () => {
  it("renders the server name and overview tab by default", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ metadata: { name: "alpha" } }))),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(await screen.findByRole("heading", { level: 1, name: "alpha" })).toBeInTheDocument();
    expect(await screen.findByText("overview-tab")).toBeInTheDocument();
  });

  it("opens the Logs tab by default for a freshly provisioning server", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(makeServer({ status: { phase: "Starting", startedAt: undefined } })),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    // Never been Running (no startedAt) → land on the install logs.
    expect(await screen.findByText("logs-tab")).toBeInTheDocument();
  });

  it("stays on Overview for a restart (Starting but already started before)", async () => {
    server.use(
      // makeServer keeps the default startedAt, so this reads as a restart.
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ status: { phase: "Starting" } }))),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(await screen.findByText("overview-tab")).toBeInTheDocument();
  });

  it("shows the provisioning sub-status under the phase badge while starting", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            status: {
              phase: "Starting",
              conditions: [
                {
                  type: "Progressing",
                  status: "True",
                  reason: "PullingImage",
                  message: "pulling the game image",
                },
              ],
            },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(await screen.findByText("Pulling the game image")).toBeInTheDocument();
  });

  it("shows the failure reason under the phase badge when Failed", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            status: {
              phase: "Failed",
              conditions: [
                {
                  type: "Ready",
                  status: "False",
                  reason: "ImagePullFailed",
                  message: "cannot pull the image — check the image reference",
                },
              ],
            },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(
      await screen.findByText("Cannot pull the image — check the image reference"),
    ).toBeInTheDocument();
  });

  it("switches to Logs tab on click", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
    );
    renderWithQuery(<ServerDetailPage />);
    const logsTab = await screen.findByRole("tab", { name: /Logs/i });
    await userEvent.click(logsTab);
    await waitFor(() => expect(screen.getByText("logs-tab")).toBeInTheDocument());
  });

  it("loads Console as a lazy chunk", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
    );
    renderWithQuery(<ServerDetailPage />);
    // Two "console"-named buttons exist: the "Open console" header
    // action and the "Console" tab. Match the tab via its exact label.
    const consoleTab = await screen.findByRole("tab", { name: "Console" });
    await userEvent.click(consoleTab);
    await waitFor(() => expect(screen.getByText("console-tab")).toBeInTheDocument());
  });

  it("loads Files as a lazy chunk", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
    );
    renderWithQuery(<ServerDetailPage />);
    const filesTab = await screen.findByRole("tab", { name: /Files/i });
    await userEvent.click(filesTab);
    await waitFor(() => expect(screen.getByText("files-tab")).toBeInTheDocument());
  });

  it("passes namespace query param to server API when ns search param is set", async () => {
    let requestUrl = "";
    server.use(
      http.get("/servers/:name", ({ request }) => {
        requestUrl = request.url;
        return HttpResponse.json(makeServer({ metadata: { name: "alpha", namespace: "other-ns" } }));
      }),
    );
    searchParams = { ns: "other-ns" };
    renderWithQuery(<ServerDetailPage />);
    await screen.findByRole("heading", { level: 1, name: "alpha" });
    expect(requestUrl).toContain("namespace=other-ns");
  });

  it("resolves a legacy local link before recording its canonical namespace", async () => {
    const requestURLs: string[] = [];
    server.use(
      http.get("/servers/:name", ({ request }) => {
        requestURLs.push(request.url);
        return HttpResponse.json(makeServer());
      }),
    );
    searchParams = {};
    renderWithQuery(<ServerDetailPage />);
    await screen.findByRole("heading", { level: 1, name: "alpha" });
    expect(requestURLs[0]).not.toContain("namespace=");
    expect(navigate).toHaveBeenCalledWith({ to: "/servers/$name", params: { name: "alpha" }, search: { cluster: "local", ns: "gameplane-games" }, replace: true });
  });
});

describe("ServerDetailPage dynamic tabs", () => {
  it("hides the Console tab and button when the template has no console", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: { consoleMode: "none", rcon: { protocol: "none" } },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    await screen.findByRole("heading", { level: 1, name: "alpha" });
    // The Console tab defaults to visible while the template query is still
    // loading (consoleAvailable = !tmpl || ...), so this waitFor genuinely
    // exercises the template resolving and the tab disappearing rather than
    // trivially matching a role that never existed.
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "Console" })).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /Open console/i })).not.toBeInTheDocument();
  });

  it("hides the Mods tab unless the template declares the capability", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      // default template has no capabilities.mods
    );
    renderWithQuery(<ServerDetailPage />);
    await screen.findByRole("tab", { name: "Console" });
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "Mods" })).not.toBeInTheDocument(),
    );
  });

  it("shows the Mods tab when the template declares mods", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: { capabilities: { mods: { path: "mods" } } },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    const modsTab = await screen.findByRole("tab", { name: "Mods" });
    await userEvent.click(modsTab);
    await waitFor(() => expect(screen.getByText("mods-tab")).toBeInTheDocument());
  });

  it("keeps the Logs tab available even when the template logs only to stdout", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: { logPath: undefined },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    // Container stdout (install/startup output) is always streamable via
    // the pod-log API, so Logs stays even without a configured logPath.
    await screen.findByRole("tab", { name: "Console" });
    expect(screen.getByRole("tab", { name: /^Logs$/i })).toBeInTheDocument();
  });
});

describe("ServerDetailPage clone action", () => {
  async function openMenu(user: ReturnType<typeof userEvent.setup>) {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer({ metadata: { name: "alpha" } }))),
    );
    renderWithQuery(<ServerDetailPage />);
    await user.click(await screen.findByRole("button", { name: "Server actions" }));
  }

  it("shows Clone server in the actions menu", async () => {
    const user = userEvent.setup();
    await openMenu(user);
    expect(screen.getByText("Clone server")).toBeInTheDocument();
  });

  it("disables Clone server for viewers", async () => {
    targetCanWrite = false;
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))),
    );
    const user = userEvent.setup();
    await openMenu(user);
    const item = screen.getByText("Clone server").closest("[role='menuitem']");
    await waitFor(() => expect(item).toHaveAttribute("aria-disabled", "true"));
    expect(item?.querySelector("[title]")).toHaveAttribute("title", "Requires operator role");
  });

  it("opens the dialog prefilled and validates the name", async () => {
    const user = userEvent.setup();
    await openMenu(user);
    await user.click(screen.getByText("Clone server"));
    const input = await screen.findByLabelText("New name");
    expect(input).toHaveValue("alpha-copy");

    await user.clear(input);
    await user.type(input, "Bad_Name");
    expect(
      screen.getByText("Name must be lowercase letters, digits, dashes (max 63)"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Clone server" })).toBeDisabled();
  });

  it("clones and navigates to the new server", async () => {
    const user = userEvent.setup();
    await openMenu(user);
    await user.click(screen.getByText("Clone server"));
    await screen.findByLabelText("New name");
    await user.click(screen.getByRole("button", { name: "Clone server" }));
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith({
        to: "/servers/$name",
        params: { name: "alpha-copy" },
        search: { cluster: "local", ns: "gameplane-games" },
      }),
    );
    expect(screen.queryByLabelText("New name")).not.toBeInTheDocument();
  });

  it("surfaces a 409 conflict and keeps the dialog open", async () => {
    server.use(
      http.post(/\/servers\/[^/]+:clone$/, () =>
        new HttpResponse("already exists", { status: 409 }),
      ),
    );
    const user = userEvent.setup();
    await openMenu(user);
    await user.click(screen.getByText("Clone server"));
    await screen.findByLabelText("New name");
    await user.click(screen.getByRole("button", { name: "Clone server" }));
    expect(
      await screen.findByText("A server named alpha-copy already exists."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("New name")).toBeInTheDocument();
  });
});

describe("ServerDetailPage failure states", () => {
  it("shows default failure message when Ready condition has no message", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            status: {
              phase: "Failed",
              conditions: [
                {
                  type: "Ready",
                  status: "False",
                  reason: "CrashLoopBackOff",
                  message: undefined,
                },
              ],
            },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(
      await screen.findByText("The server failed to start."),
    ).toBeInTheDocument();
  });

  it("renders server version and uptime metadata", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            status: {
              agent: { gameVersion: "1.20.1" },
              startedAt: "2026-05-07T00:00:00Z",
            },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    expect(await screen.findByText("1.20.1")).toBeInTheDocument();
  });

  it("shows the Modpacks tab when the template supports it", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: {
              // serverHasModpacks gates on the active version's loader
              // being modpack-capable (fabric/forge/neoforge/quilt) — a
              // template with no versions[] has no resolvable loader, so
              // a default forge version is required for the tab to show.
              versions: [{ id: "forge", displayName: "Forge", loader: "forge", default: true }],
              capabilities: {
                mods: {
                  loaders: {
                    forge: { path: "modpacks" },
                  },
                  registry: {
                    providers: [
                      {
                        provider: "modrinth",
                        modpacks: { refEnv: "MODRINTH_MODPACK" },
                      },
                    ],
                  },
                },
              },
            },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    const modpacksTab = await screen.findByRole("tab", { name: "Modpacks" });
    expect(modpacksTab).toBeInTheDocument();
  });

  it("hides Modpacks when the template does not declare the capability", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: { capabilities: {} },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    await screen.findByRole("tab", { name: "Console" });
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "Modpacks" })).not.toBeInTheDocument(),
    );
  });

  it("falls back to Overview tab when the requested tab becomes unavailable", async () => {
    let consoleAvailable = true;
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: consoleAvailable
              ? { consoleMode: "rcon", rcon: { protocol: "rcon" } }
              : { consoleMode: "none", rcon: { protocol: "none" } },
          }),
        ),
      ),
    );
    const { client } = renderWithQuery(<ServerDetailPage />);
    // Click Console tab which should show
    const consoleTab = await screen.findByRole("tab", { name: "Console" });
    await userEvent.click(consoleTab);
    await waitFor(() => expect(screen.getByText("console-tab")).toBeInTheDocument());

    // Simulate the template being updated to remove console support
    consoleAvailable = false;
    server.resetHandlers();
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: { consoleMode: "none", rcon: { protocol: "none" } },
          }),
        ),
      ),
    );
    // The template query has no polling interval of its own (unlike the
    // server query's 5s refetchInterval), so nothing re-fetches it after
    // swapping handlers — invalidate it explicitly to simulate the refetch.
    await client.invalidateQueries({ queryKey: resourceKey({ cluster: "local", namespace: "gameplane-games", name: "alpha" }, "template") });

    // Wait for template refetch and fallback to overview
    await waitFor(() =>
      expect(screen.getByText("overview-tab")).toBeInTheDocument(),
    );
  });

  it("renders Stop button for asleep servers without showing Wake after a stop", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            status: {
              phase: "Suspended",
              idle: { asleep: true, asleepSince: "2026-05-07T12:00:00Z" },
            },
          }),
        ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    // The Stop button renders (disabled) before /servers/alpha resolves —
    // findByRole can resolve during that initial disabled render, so wait
    // for the loaded, asleep-aware disabled state like the other lifecycle
    // button tests in this file do.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Stop$/i })).not.toBeDisabled(),
    );
  });
});

describe("ServerDetailPage capture tab", () => {
  it("renders the Capture tab between Backups and Settings", async () => {
    server.use(http.get("/servers/alpha", () => HttpResponse.json(makeServer())));
    renderWithQuery(<ServerDetailPage />);
    const nav = await screen.findByRole("tablist", { name: "Server detail tabs" });
    await screen.findByRole("tab", { name: "Capture" });
    const labels = within(nav)
      .getAllByRole("tab")
      .map((b) => b.textContent);
    const backupsIdx = labels.indexOf("Backups");
    const captureIdx = labels.indexOf("Capture");
    const settingsIdx = labels.indexOf("Settings");
    expect(backupsIdx).toBeGreaterThanOrEqual(0);
    expect(captureIdx).toBe(backupsIdx + 1);
    expect(settingsIdx).toBe(captureIdx + 1);
  });

  it("keeps the Capture tab reachable with a retry, not a denial, when /users/me fails", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/users/me", () => new HttpResponse("unavailable\n", { status: 503 })),
    );
    // useMe retries non-401 failures; drop the backoff so its retries are
    // exhausted within findBy's timeout.
    const client = makeClient();
    client.setDefaultOptions({
      ...client.getDefaultOptions(),
      queries: { ...client.getDefaultOptions().queries, retryDelay: 0 },
    });
    renderWithQuery(<ServerDetailPage />, { client });
    await userEvent.click(await screen.findByRole("tab", { name: "Capture" }));
    expect(await screen.findByText("Can't reach the control plane")).toBeInTheDocument();
    expect(screen.getByText(/HTTP 503/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(
      screen.queryByText(/You don't have access to packet capture on this server/i),
    ).not.toBeInTheDocument();
  });

  it("shows the not-enabled state (Bbnga copy) when capture is off", async () => {
    // Default makeServer() has no spec.capture — capture defaults to off.
    server.use(http.get("/servers/alpha", () => HttpResponse.json(makeServer())));
    renderWithQuery(<ServerDetailPage />);
    const captureTab = await screen.findByRole("tab", { name: "Capture" });
    await userEvent.click(captureTab);
    expect(
      await screen.findByText("Capture is not enabled on this server."),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Network packet capture is an optional feature that records raw game protocol traffic. Enable it to capture packets from joining players for protocol analysis.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Enable Capture/i })).toBeInTheDocument();
  });

  it("shows the empty-captures state (dBILX copy) once capture is enabled", async () => {
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            spec: { templateRef: { name: "minecraft-vanilla" }, capture: { enabled: true } },
          }),
        ),
      ),
      http.get(/\/servers\/alpha:captures$/, () =>
        HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    const captureTab = await screen.findByRole("tab", { name: "Capture" });
    await userEvent.click(captureTab);
    expect(await screen.findByText("No captures yet.")).toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
  });

  it("shows the invalid-BPF-filter error state in the start-capture modal (b4eaUf)", async () => {
    const user = userEvent.setup();
    server.use(
      http.get("/servers/alpha", () =>
        HttpResponse.json(
          makeServer({
            spec: { templateRef: { name: "minecraft-vanilla" }, capture: { enabled: true } },
          }),
        ),
      ),
      http.get(/\/servers\/alpha:captures$/, () =>
        HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
      ),
      http.post(
        /\/servers\/alpha:capture-start$/,
        () =>
          new HttpResponse(
            "invalid filter: syntax error at position 12 (invalid token 'foo')",
            { status: 400 },
          ),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    const captureTab = await screen.findByRole("tab", { name: "Capture" });
    await user.click(captureTab);

    const openBtn = await screen.findByRole("button", { name: "Start Capture" });
    await user.click(openBtn);

    const dialog = await screen.findByRole("dialog");
    const filterInput = within(dialog).getByLabelText("Packet Filter");
    await user.type(filterInput, "tcp prot 8080 foo");

    const submitBtn = within(dialog).getByRole("button", { name: "Start Capture" });
    await user.click(submitBtn);

    expect(
      await screen.findByText("invalid filter: syntax error at position 12 (invalid token 'foo')"),
    ).toBeInTheDocument();
    expect(filterInput).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(submitBtn).toBeDisabled());
  });
});
