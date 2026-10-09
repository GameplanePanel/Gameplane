// Default MSW handlers — the baseline API surface a route test can rely
// on to render its happy-path UI without each test having to declare
// every endpoint. Tests override individual routes via `server.use(...)`.

import { http, HttpResponse, ws } from "msw";
import { fleetHandlers } from "./fleetHandlers";
import {
  makeAudit,
  makeBackup,
  makeBannedPlayer,
  makeCatalog,
  makeClusterInfo,
  makeClusterStats,
  makeClusterView,
  makeConfig,
  makeDestination,
  makeFileEntry,
  makeModule,
  makeModuleSource,
  makePlayers,
  makeRestore,
  makeSchedule,
  makeServer,
  makeTemplate,
  makeUser,
} from "./factories";
import {
  getScreenshotData,
  screenshotConsoleOutput,
  screenshotSystemLogLines,
  screenshotConfigWithOidc,
  screenshotConfigOidcEmptyMappings,
  screenshotConfigEmptyStorageClass,
  screenshotConfigCurseforgeOnly,
  screenshotEmptyMods,
  screenshotModrinthProjects,
} from "./screenshotData";

export const INVALID_BPF_FILTER_FIXTURE = "tcp prot 8080 foo";

export const handlers = [
  ...fleetHandlers,
  // Auth
  http.get("/users/me", ({ cookies }) => {
    // e2e affordance: a 401 on /users/me must bounce the SPA to /login,
    // but the browser-mode service worker answers before Playwright's
    // page.route can inject a status. A cookie the test sets (and which
    // survives the navigation) lets it force the unauthorized path
    // deterministically. No-op for the unit suite (no cookie set).
    if (cookies.e2e_force_401 === "1") {
      return new HttpResponse("unauthorized\n", { status: 401 });
    }
    // e2e affordance: rbacEnforcement.spec.ts needs /users/me to report a
    // specific role from the moment the SPA loads. MSW's Service Worker
    // answers this route itself, so a page.route() override can't inject
    // the role — a cookie the test sets (and which survives navigation)
    // selects one deterministically. No-op for the unit suite.
    if (
      cookies.e2e_me_role === "admin" ||
      cookies.e2e_me_role === "operator" ||
      cookies.e2e_me_role === "viewer"
    ) {
      return HttpResponse.json(makeUser({ role: cookies.e2e_me_role }));
    }
    return HttpResponse.json(makeUser());
  }),
  // Mock-mode Playwright login: accept any non-empty credentials and set
  // the CSRF cookie the SPA's mutation path reads on every later POST.
  // Real backend rate-limiting and argon2id verification are out of
  // scope for browser-side mock — those are exercised in api_auth_e2e.
  http.post("/auth/login", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      username?: string;
      password?: string;
    } | null;
    if (!body?.username || !body?.password || body.password.startsWith("wrong") || body.password === "invalid") {
      return new HttpResponse("invalid credentials\n", { status: 401 });
    }
    return HttpResponse.json(
      { user: makeUser({ username: body.username }), csrf: "mock-csrf-token" },
      {
        headers: {
          // Mirror the real cookie shape: csrf cookie is JS-readable so
          // the SPA can echo it back in X-Gameplane-CSRF.
          "Set-Cookie":
            "gameplane_csrf=mock-csrf-token; Path=/; SameSite=Lax",
        },
      },
    );
  }),
  http.post("/auth/logout", () => new HttpResponse(null, { status: 204 })),
  // Pre-auth login providers. Mock mode advertises both local + OIDC so
  // the full login UI is exercised; real installs gate OIDC on config.
  http.get("/auth/providers", () =>
    HttpResponse.json({
      providers: [
        { kind: "local", label: "Local account" },
        { kind: "oidc", label: "Keycloak" },
      ],
    }),
  ),

  // Cluster
  // Server-sent events stream the layout opens. Browser mock mode has a
  // real EventSource, so without this it falls through to the dev proxy
  // and logs a 500; keep an open, idle stream instead. (jsdom has no
  // EventSource, so the vitest suite never reaches this handler.)
  http.get("/events", () => {
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(": connected\n\n"));
        // Intentionally left open — an SSE connection stays alive.
      },
    });
    return new HttpResponse(stream, {
      headers: {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
    });
  }),

  http.get("/cluster", ({ cookies, request }) => {
    // e2e affordance: errorHandling.spec.ts needs /cluster to return 500
    // to verify the error UI. MSW's Service Worker answers this route
    // itself, so a page.route() override can't inject the status — a
    // cookie the test sets forces the failure deterministically.
    if (cookies.e2e_cluster_500 === "1") {
      return new HttpResponse("boom\n", { status: 500 });
    }
    if (cookies.e2e_multicluster === "1") {
      const remote = new URL(request.url).searchParams.get("cluster") === "remote-demo";
      return HttpResponse.json(makeClusterView({
        name: remote ? "remote-demo" : "local",
        ready: 1, total: 1,
        nodes: [{ name: remote ? "gp-demo-remote-control-plane" : "gp-demo-central-control-plane", status: "Ready" }],
      }));
    }
    return HttpResponse.json(makeClusterView());
  }),
  http.get("/cluster/info", () => HttpResponse.json(makeClusterInfo())),
  http.get("/cluster/stats", ({ cookies }) => {
    if (cookies.e2e_cluster_500 === "1") {
      return new HttpResponse("boom\n", { status: 500 });
    }
    return HttpResponse.json(makeClusterStats());
  }),
  http.get("/clusters", ({ cookies }) => HttpResponse.json({
    items: [
      { name: "local", displayName: "local", phase: "Healthy", canViewInventory: true },
      ...(cookies.e2e_multicluster === "1" ? [{ name: "remote-demo", displayName: "Remote demo", phase: "Healthy", canViewInventory: true }] : []),
    ],
  })),

  // Namespaces (F-263): default single-namespace install so existing
  // /servers-only mocks keep behaving exactly as before the fan-out.
  http.get("/namespaces", () => HttpResponse.json({ namespaces: ["gameplane-games"] })),

  // Servers
  http.get("/servers", () =>
    HttpResponse.json({
      items: [
        makeServer(),
        makeServer({ metadata: { name: "beta", namespace: "gameplane-games" } }),
      ],
    }),
  ),
  // `:name([^:/]+)` excludes ':' so this never swallows a `name:verb` colon-action
  // URL (e.g. `alpha:capture`) meant for one of the regex handlers below — a
  // dead/misordered colon-action handler should 404 as unhandled, not silently
  // return a plausible-looking server here.
  http.get("/servers/:name([^:/]+)", ({ params, request, cookies }) => {
    const cluster = new URL(request.url).searchParams.get("cluster") || "local";
    const namespace = new URL(request.url).searchParams.get("namespace") || "gameplane-games";
    return HttpResponse.json(makeServer({ metadata: { name: String(params.name), namespace,
      ...(cookies.e2e_multicluster === "1" ? { uid: `${cluster}-servers-uid` } : {}),
    } }));
  }),
  // Ownership fixtures are explicit in the tests that exercise owner/collaborator access.
  http.get("/users/me/servers", ({ cookies }) => HttpResponse.json({ items: cookies.e2e_shared_server === "1"
    ? [makeServer({ metadata: { name: "team-a-shared", namespace: "team-a" } })]
    : [] })),
  http.post("/servers", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      metadata?: { name?: string };
    } | null;
    return HttpResponse.json(
      makeServer({
        metadata: { name: body?.metadata?.name ?? "new-server", namespace: "gameplane-games" },
      }),
    );
  }),
  // See the GET /servers/:name comment above — same colon-exclusion reasoning.
  http.put("/servers/:name([^:/]+)", ({ params }) =>
    HttpResponse.json(makeServer({ metadata: { name: String(params.name) } })),
  ),
  http.delete("/servers/:name([^:/]+)", () => new HttpResponse(null, { status: 204 })),

  // Lifecycle: chi uses `:verb` literal-colon URL syntax, which standard
  // URL pattern matchers don't parse — fall back to regex per verb.
  http.post(/\/servers\/[^/]+:start(\?.*)?$/, () => new HttpResponse(null, { status: 202 })),
  http.post(/\/servers\/[^/]+:stop(\?.*)?$/, () => new HttpResponse(null, { status: 202 })),
  http.post(/\/servers\/[^/]+:restart(\?.*)?$/, () => new HttpResponse(null, { status: 202 })),
  http.post(/\/servers\/[^/]+:wake(\?.*)?$/, () => new HttpResponse(null, { status: 202 })),
  http.post(/\/servers\/[^/]+:clone(\?.*)?$/, async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      newName?: string;
    } | null;
    return HttpResponse.json(
      makeServer({
        metadata: { name: body?.newName ?? "cloned-server", namespace: "gameplane-games" },
      }),
    );
  }),
  http.post(/\/servers\/[^/]+:wipe-data$/, () => new HttpResponse(null, { status: 202 })),
  http.post(/\/servers\/[^/]+:transfer$/, () => new HttpResponse(null, { status: 204 })),
  http.put(/\/servers\/[^/]+:collaborators$/, () => new HttpResponse(null, { status: 204 })),

  // Tunnel credentials: GET returns configured status, PUT sets credentials, DELETE removes.
  http.get(/\/servers\/[^/]+:tunnel-credentials$/, () =>
    HttpResponse.json({ configured: false, secretName: "", keys: [] }),
  ),
  http.put(/\/servers\/[^/]+:tunnel-credentials$/, () => new HttpResponse(null, { status: 204 })),
  http.delete(/\/servers\/[^/]+:tunnel-credentials$/, () => new HttpResponse(null, { status: 204 })),

  // Capture: enable/disable, start/stop, list, get, delete, and file download
  http.post(/\/servers\/[^/]+:capture-enable(\?.*)?$/, () =>
    HttpResponse.json({
      name: "server-name",
      status: { capture: { enabled: true } },
    }),
  ),
  http.post(/\/servers\/[^/]+:capture-disable(\?.*)?$/, () =>
    HttpResponse.json({
      name: "server-name",
      status: { capture: { enabled: false } },
    }),
  ),
  http.get(/\/servers\/[^/]+:captures(\?.*)?$/, () =>
    HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 }),
  ),
  http.get(/\/servers\/[^/]+:capture$/, () =>
    HttpResponse.json({
      captureId: "cap-12345",
      serverName: "server-name",
      phase: "Completed",
      startedAt: "2026-08-23T00:00:00Z",
      completedAt: "2026-08-23T01:00:00Z",
      createdAt: "2026-08-23T00:00:00Z",
      expiresAt: "2026-08-30T00:00:00Z",
      bytesWritten: 1024,
      packetsWritten: 100,
      filter: "tcp port 25565",
    }),
  ),
  http.post(/\/servers\/[^/]+:capture-start(\?.*)?$/, async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      filter?: string;
      maxDurationSeconds?: number;
      maxSizeBytes?: number;
      ttlSecondsAfterFinished?: number;
    } | null;

    // Validate BPF filter if provided
    if (body?.filter) {
      // Check for invalid syntax patterns
      if (body.filter === INVALID_BPF_FILTER_FIXTURE || body.filter.includes("{{{{")) {
        return new HttpResponse("Invalid BPF filter syntax: syntax error at position 12 (invalid token 'foo')\n", { status: 400 });
      }
    }

    return HttpResponse.json(
      {
        captureId: "cap-12345",
        serverName: "server-name",
        phase: "Pending",
        startedAt: null,
        completedAt: null,
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 0,
        packetsWritten: 0,
        filter: body?.filter || "tcp port 25565",
      },
      { status: 202 },
    );
  }),
  http.post(/\/servers\/[^/]+:capture-stop(\?.*)?$/, () =>
    HttpResponse.json({
      captureId: "cap-12345",
      serverName: "server-name",
      phase: "Completed",
      startedAt: "2026-08-23T00:00:00Z",
      completedAt: "2026-08-23T01:00:00Z",
      createdAt: "2026-08-23T00:00:00Z",
      expiresAt: "2026-08-30T00:00:00Z",
      bytesWritten: 1024,
      packetsWritten: 100,
      filter: "tcp port 25565",
    }),
  ),
  http.delete(/\/servers\/[^/]+:capture$/, () =>
    HttpResponse.json({ deleted: true, captureId: "cap-12345" }),
  ),
  http.get(/\/servers\/[^/]+:capture-file$/, () =>
    new HttpResponse(new Blob(["mock pcapng data"]), {
      headers: { "Content-Type": "application/octet-stream" },
    }),
  ),

  // Templates
  http.get("/templates", () =>
    HttpResponse.json({
      items: [
        makeTemplate(),
        makeTemplate({
          metadata: { name: "valheim-default" },
          spec: {
            displayName: "Valheim",
            game: "valheim",
            version: "0.218",
            image: "ghcr.io/gameplanepanel/gameplane/valheim:0.218",
          },
        }),
      ],
    }),
  ),
  http.get("/templates/:name", ({ params, cookies }) => {
    const name = String(params.name);
    // e2e affordance: serverDetail.spec.ts's mods-install-carries-namespace
    // case needs a template whose capabilities.mods.install is set, so the
    // Mods tab's URL-install form renders. `path` must also be set —
    // serverHasMods()/resolveModVolume() (src/lib/capabilities.ts) only
    // show the Mods tab when the template declares a mod directory (or
    // idList); `install` alone isn't enough. No-op for every other test.
    if (cookies.e2e_mods_install === "1") {
      return HttpResponse.json(
        makeTemplate({
          metadata: { name },
          spec: { capabilities: { mods: { path: "/data/mods", install: { allowedHosts: ["example.com"] } } } },
        }),
      );
    }
    return HttpResponse.json(makeTemplate({ metadata: { name } }));
  }),

  // Backups
  http.get("/backups", () =>
    HttpResponse.json({
      items: [
        makeBackup(),
        makeBackup({
          metadata: { name: "alpha-2026-05-06", namespace: "gameplane-games" },
          status: {
            phase: "Failed",
            startTime: "2026-05-06T03:00:00Z",
            completionTime: "2026-05-06T03:00:30Z",
          },
        }),
      ],
    }),
  ),
  http.get("/backups/:name", ({ params }) =>
    HttpResponse.json(makeBackup({ metadata: { name: String(params.name) } })),
  ),
  http.post("/backups", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      metadata?: { name?: string; generateName?: string };
      spec?: { serverRef?: { name?: string } };
    } | null;
    const name =
      body?.metadata?.name ??
      `${body?.spec?.serverRef?.name ?? "alpha"}-manual-${Date.now()}`;
    return HttpResponse.json(makeBackup({ metadata: { name, namespace: "gameplane-games" } }));
  }),
  http.delete("/backups/:name", () => new HttpResponse(null, { status: 204 })),

  http.get("/schedules", () => HttpResponse.json({ items: [makeSchedule()] })),
  http.get("/schedules/:name", ({ params }) =>
    HttpResponse.json(makeSchedule({ metadata: { name: String(params.name) } })),
  ),
  http.post("/schedules", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      metadata?: { name?: string; generateName?: string };
      spec?: { serverRef?: { name?: string }; schedule?: string };
    } | null;
    return HttpResponse.json(
      makeSchedule({
        metadata: {
          name: body?.metadata?.name ?? `${body?.spec?.serverRef?.name ?? "alpha"}-sched-1`,
          namespace: "gameplane-games",
        },
      }),
    );
  }),
  http.put("/schedules/:name", ({ params }) =>
    HttpResponse.json(makeSchedule({ metadata: { name: String(params.name) } })),
  ),
  http.delete("/schedules/:name", () => new HttpResponse(null, { status: 204 })),

  // Restores
  http.get("/restores", () => HttpResponse.json({ items: [] })),
  http.post("/restores", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      metadata?: { name?: string; generateName?: string };
      spec?: { backupRef?: { name?: string }; serverRef?: { name?: string } };
    } | null;
    return HttpResponse.json(
      makeRestore({
        metadata: {
          name: body?.metadata?.name ?? `restore-${Date.now()}`,
          namespace: "gameplane-games",
        },
        spec: {
          backupRef: { name: body?.spec?.backupRef?.name ?? "alpha-2026-05-07" },
          serverRef: { name: body?.spec?.serverRef?.name ?? "alpha" },
        },
      }),
    );
  }),
  http.delete("/restores/:name", () => new HttpResponse(null, { status: 204 })),

  // Backup destinations
  http.get("/backup-destinations", () =>
    HttpResponse.json({ items: [makeDestination()] }),
  ),
  http.post("/backup-destinations", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      name?: string;
      url?: string;
    } | null;
    return HttpResponse.json(
      makeDestination({ name: body?.name ?? "default", url: body?.url ?? "s3://x" }),
    );
  }),
  http.delete("/backup-destinations/:name", () => new HttpResponse(null, { status: 204 })),

  // Modules: legacy /modules handler is preserved (some existing tests
  // hit it); the dashboard's actual paths are /modules/catalog and
  // /modules/sources, served below.
  http.get("/modules", () => HttpResponse.json({ items: [makeModule()] })),
  http.get("/modules/catalog", () =>
    HttpResponse.json({
      items: [
        makeCatalog(),
        makeCatalog({
          name: "valheim-default",
          displayName: "Valheim",
          game: "valheim",
          installed: true,
          installedVersion: "0.218",
          moduleName: "valheim-default",
          phase: "Ready",
          versions: ["0.218", "0.217"],
          latestVersion: "0.218",
        }),
      ],
    }),
  ),
  http.get("/modules/sources", () =>
    HttpResponse.json({ items: [makeModuleSource()] }),
  ),
  // Module builder archetypes catalog
  http.get("/modules/builder/archetypes", () =>
    HttpResponse.json({
      archetypes: [
        {
          id: "steamcmd",
          title: "SteamCMD Dedicated Server",
          description: "Dedicated game server installed and managed via SteamCMD (Valve UDP ports, save volume, non-root user)",
          defaultImage: "cm2network/steamcmd:root@sha256:4d830b0475b8719f96b9978ba57404434bb3da3f260388d75cfb373cf5889ea8",
          defaultPorts: [
            { name: "game", containerPort: 27015, protocol: "UDP", advertise: true },
            { name: "query", containerPort: 27016, protocol: "UDP", advertise: true },
          ],
          defaultStorage: {
            size: "20Gi",
            mountPath: "/serverdata",
          },
          defaultEnv: [
            { name: "STEAMAPPID", value: "0" },
            { name: "SERVER_NAME", value: "Game Server" },
          ],
          configSchema: [
            {
              name: "SERVER_PASSWORD",
              displayName: "Server Password",
              description: "Password required for players to join the server",
              type: "password",
              required: false,
            },
            {
              name: "MAX_PLAYERS",
              displayName: "Maximum Players",
              description: "Maximum allowed concurrent players",
              type: "int",
              default: "16",
              min: 1,
              max: 128,
            },
          ],
          capabilities: {
            lifecycle: {
              stop: ["quit"],
            },
          },
          defaultCategories: ["Survival", "Co-op"],
        },
        {
          id: "java",
          title: "Java Application Server",
          description: "JVM-based game server (Minecraft, etc.) with automatic memory heap calculation and RCON support",
          defaultImage: "eclipse-temurin:21-jre-jammy@sha256:0d5bba8111956f2f01fbf9c054238e55e09f58ea9fc3a3b5c6e838ebdcbb636d",
          defaultPorts: [
            { name: "game", containerPort: 25565, protocol: "TCP", advertise: true },
            { name: "rcon", containerPort: 25575, protocol: "TCP", advertise: false },
          ],
          defaultStorage: {
            size: "10Gi",
            mountPath: "/data",
          },
          defaultEnv: [
            { name: "EULA", value: "TRUE" },
            { name: "ENABLE_RCON", value: "true" },
            { name: "RCON_PORT", value: "25575" },
          ],
          configSchema: [
            {
              name: "MAX_MEMORY",
              displayName: "Maximum JVM Heap Memory",
              description: "Maximum memory allocated to JVM, dynamically calculated from container limit",
              type: "string",
              autoFromMemoryLimit: {
                percent: 75,
              },
            },
            {
              name: "RCON_PASSWORD",
              displayName: "RCON Password",
              description: "Remote console management password",
              type: "password",
              required: false,
            },
          ],
          capabilities: {
            lifecycle: {
              stop: ["stop"],
            },
          },
          defaultCategories: ["Sandbox", "Survival"],
        },
        {
          id: "generic",
          title: "Generic Container Server",
          description: "General-purpose containerized game server with configurable TCP/UDP ports and persistent storage",
          defaultImage: "alpine:3.20@sha256:b89d9c10e96b8da1b4874828d0a479b157d60b49364fae0db5122e1b5056f918",
          defaultPorts: [
            { name: "game", containerPort: 8080, protocol: "TCP", advertise: true },
          ],
          defaultStorage: {
            size: "5Gi",
            mountPath: "/data",
          },
          defaultEnv: [],
          configSchema: [],
          capabilities: {},
          defaultCategories: ["Co-op"],
        },
      ],
    }),
  ),
  http.get("/modules/:name", ({ params }) =>
    HttpResponse.json(makeModule({ metadata: { name: String(params.name) } })),
  ),
  http.post("/modules", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      module?: string;
      name?: string;
    } | null;
    return HttpResponse.json(
      makeModule({ metadata: { name: body?.name ?? body?.module ?? "minecraft-vanilla" } }),
    );
  }),
  http.patch("/modules/:name", ({ params }) =>
    HttpResponse.json(makeModule({ metadata: { name: String(params.name) } })),
  ),
  http.delete("/modules/:name", () => new HttpResponse(null, { status: 204 })),

  // Pod/StatefulSet/GameServer Kubernetes events (Overview events feed).
  http.get("/servers/:name/events", () => HttpResponse.json([])),
  http.get("/servers/:name/mods", () => HttpResponse.json([])),
  http.get("/servers/:name/mods/updates", () => HttpResponse.json({ updates: [], checkedAt: new Date().toISOString() })),
  http.post("/servers/:name/mods/install", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as { url?: string; name?: string } | null;
    return HttpResponse.json({ name: body?.name ?? "mock-mod.jar", size: 1024 });
  }),

  // Players
  http.get("/servers/:name/players", () => HttpResponse.json(makePlayers())),
  http.get("/servers/:name/players/banned", () =>
    HttpResponse.json([makeBannedPlayer()]),
  ),
  http.post(/\/servers\/[^/]+\/players\/(kick|ban|unban)$/, () =>
    HttpResponse.json({ ok: true }),
  ),
  http.get("/servers/:name/players/whitelist", () =>
    HttpResponse.json(["alice", "carol"]),
  ),
  http.post(/\/servers\/[^/]+\/players\/whitelist\/(add|remove)$/, () =>
    HttpResponse.json({ ok: true }),
  ),

  // Files (agent proxy)
  http.get("/servers/:name/files/list", () =>
    HttpResponse.json([
      makeFileEntry({ name: "server.properties", path: "/data/server.properties", size: 412 }),
      makeFileEntry({ name: "world", path: "/data/world", size: 0, dir: true }),
    ]),
  ),
  http.get("/servers/:name/files/read", () => new HttpResponse("# mock file body\n", { status: 200 })),
  http.post("/servers/:name/files/write", () => new HttpResponse(null, { status: 204 })),
  http.post("/servers/:name/files/mkdir", () => new HttpResponse(null, { status: 204 })),
  http.delete("/servers/:name/files/delete", () => new HttpResponse(null, { status: 204 })),

  // Users (canonical path per endpoints.ts; /admin/users below is legacy).
  http.get("/users", () =>
    HttpResponse.json([
      makeUser(),
      makeUser({ id: 2, username: "operator-bob", role: "operator" }),
      makeUser({ id: 3, username: "viewer-carol", role: "viewer" }),
    ]),
  ),
  http.post("/users", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      username?: string;
      role?: "admin" | "operator" | "viewer";
    } | null;
    return HttpResponse.json(
      makeUser({
        id: 99,
        username: body?.username ?? "new-user",
        role: body?.role ?? "viewer",
      }),
    );
  }),
  http.patch("/users/:id", ({ params }) =>
    HttpResponse.json(makeUser({ id: Number(params.id) })),
  ),
  http.delete("/users/:id", () => new HttpResponse(null, { status: 204 })),
  http.post("/users/:id/reset-password", () => new HttpResponse(null, { status: 204 })),

  // Roles + permission catalog (custom RBAC).
  http.get("/roles", () =>
    HttpResponse.json([
      {
        name: "admin",
        description: "Full access to all resources, including users, roles, and global config.",
        builtin: true,
        permissions: ["*"],
      },
      {
        name: "operator",
        description: "Manage game servers, backups, templates, and schedules.",
        builtin: true,
        permissions: ["servers:read", "servers:write"],
      },
      {
        name: "viewer",
        description: "Read-only access across the control panel.",
        builtin: true,
        permissions: ["servers:read"],
      },
    ]),
  ),
  http.get("/roles/permissions", () =>
    HttpResponse.json({
      groups: [
        {
          resource: "servers",
          label: "Game servers",
          permissions: [
            { key: "servers:read", label: "View servers", namespaced: true },
            { key: "servers:write", label: "Manage servers", namespaced: true },
          ],
        },
        {
          resource: "captures",
          label: "Network captures",
          permissions: [
            { key: "captures:manage", label: "Enable, start, stop, download, and delete packet captures", namespaced: true },
          ],
        },
        {
          resource: "users",
          label: "Users",
          permissions: [{ key: "users:manage", label: "Manage users", namespaced: false }],
        },
      ],
    }),
  ),
  http.post("/roles", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      name?: string;
      description?: string;
      permissions?: string[];
    } | null;
    return HttpResponse.json(
      {
        name: body?.name ?? "new-role",
        description: body?.description ?? "",
        builtin: false,
        permissions: body?.permissions ?? [],
      },
      { status: 201 },
    );
  }),
  http.patch("/roles/:name", async ({ params, request }) => {
    const body = (await request.json().catch(() => null)) as {
      description?: string;
      permissions?: string[];
    } | null;
    return HttpResponse.json({
      name: String(params.name),
      description: body?.description ?? "",
      builtin: false,
      permissions: body?.permissions ?? [],
    });
  }),
  http.delete("/roles/:name", () => new HttpResponse(null, { status: 204 })),
  http.get("/users/:id/bindings", () => HttpResponse.json([])),
  http.post("/users/:id/bindings", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as {
      roleName?: string;
      namespace?: string;
    } | null;
    return HttpResponse.json(
      { roleName: body?.roleName ?? "viewer", namespace: body?.namespace ?? "team-a" },
      { status: 201 },
    );
  }),
  http.delete("/users/:id/bindings/:role/:namespace", () => new HttpResponse(null, { status: 204 })),

  // Admin
  http.get("/admin/audit", ({ request }) => {
    const url = new URL(request.url);
    const limit = Number(url.searchParams.get("limit") ?? "50");
    const out: ReturnType<typeof makeAudit>[] = [];
    for (let i = 0; i < Math.min(limit, 5); i++) {
      out.push(
        makeAudit({
          id: i + 1,
          method: i % 2 === 0 ? "POST" : "GET",
          path: `/api/v1/servers/alpha`,
          status: i === 4 ? 403 : 200,
        }),
      );
    }
    return HttpResponse.json(out);
  }),
  // Audit chain integrity check — AuditLogPage renders the success/failure
  // banner from this. No dedicated test exercises the failure path against
  // this default handler (see AuditLog.test.tsx's own fetch mocks); this
  // exists so the endpoint isn't unhandled when a test lands on /admin/audit.
  http.get("/admin/audit/verify", () =>
    HttpResponse.json({ ok: true, checked: 5, message: "audit chain intact" }),
  ),
  http.get("/admin/config", () => HttpResponse.json(makeConfig())),
  http.put("/admin/config/:section", () => new HttpResponse(null, { status: 204 })),
  // First-login telemetry notice (spec 022): not pending by default, so
  // tests that render the app shell as an admin see no banner.
  http.get("/admin/telemetry/notice", () => HttpResponse.json({ pending: false })),
  http.post("/admin/telemetry/notice", () => new HttpResponse(null, { status: 204 })),
  // Admin Settings -> Telemetry (spec 022): matches the default config
  // (basic off, extended off), so the section renders the default destination.
  http.get("/admin/telemetry", () =>
    HttpResponse.json({
      destination: { kind: "default", host: "telemetry.example.org" },
      operatorDisabled: false,
      consent: { basic: false, extended: false, source: "default" },
      installId: null,
      preview: null,
      status: { lastAttemptAt: null, lastSuccessAt: null, lastOutcome: "never", lastIdRotationAt: null },
    }),
  ),
  http.post("/admin/notifications/sinks/:name/test", () =>
    HttpResponse.json({ delivered: true }),
  ),

  // Legacy admin/users path retained in case any existing test still
  // points at it; canonical /users handler above takes precedence for
  // paths the dashboard actually fetches.
  http.get("/admin/users", () => HttpResponse.json({ items: [makeUser()] })),
];

/**
 * SSO-only dataset handlers — overrides just `/auth/providers` to report
 * no local-login provider, so Login.tsx renders its SSO-only branch (no
 * username/password form, just the provider button). MSW matches array
 * order, so this override must precede the default `/auth/providers`
 * handler. Served when localStorage.getItem("gameplane-e2e-dataset") ===
 * "sso-only".
 */
export function buildSsoOnlyHandlers() {
  return [
    http.get("/auth/providers", () =>
      HttpResponse.json({
        providers: [
          { kind: "oidc", label: "Keycloak" },
          { kind: "oidc", label: "Google" },
        ],
      }),
    ),
    ...handlers,
  ];
}

/**
 * Screenshot dataset handlers — a richer, more diverse set of test data
 * for demo/screenshot purposes. Reuses the existing handler functions and
 * factories so shapes stay in sync. Served when
 * localStorage.getItem("gameplane-e2e-dataset") === "screenshots".
 */
const wsOrigin = typeof window !== "undefined" ? window.location.origin.replace(/^http/, "ws") : "ws://localhost:5173";

export function buildScreenshotHandlers() {
  const data = getScreenshotData();
  return [
    ...fleetHandlers,
    // The Servers page asks which namespaces to list before listing servers.
    http.get("/namespaces", () => HttpResponse.json({ namespaces: ["gameplane-games"] })),
    // Auth: reuse default login/logout (screenshot demos don't test auth edge cases)
    http.get("/users/me", ({ cookies }) => {
      if (cookies.e2e_force_401 === "1") {
        return new HttpResponse("unauthorized\n", { status: 401 });
      }
      // Screenshot mode: return admin-demo by default
      return HttpResponse.json(data.users[0]);
    }),
    http.post("/auth/login", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        username?: string;
        password?: string;
      } | null;
      if (!body?.username || !body?.password || body.password.startsWith("wrong") || body.password === "invalid") {
        return new HttpResponse("invalid credentials\n", { status: 401 });
      }
      // Return matching user from screenshot set, or admin-demo fallback
      const user = data.users.find((u) => u.username === body.username) ?? data.users[0];
      return HttpResponse.json(
        { user, csrf: "mock-csrf-token" },
        {
          headers: {
            "Set-Cookie": "gameplane_csrf=mock-csrf-token; Path=/; SameSite=Lax",
          },
        },
      );
    }),
    http.post("/auth/logout", () => new HttpResponse(null, { status: 204 })),
    http.get("/auth/providers", () =>
      HttpResponse.json({
        providers: [
          { kind: "local", label: "Local account" },
          { kind: "oidc", label: "Keycloak" },
          { kind: "oidc", label: "Google" },
        ],
      }),
    ),

    // Cluster
    http.get("/events", () => {
      const stream = new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode(": connected\n\n"));
        },
      });
      return new HttpResponse(stream, {
        headers: {
          "Content-Type": "text/event-stream",
          "Cache-Control": "no-cache",
          Connection: "keep-alive",
        },
      });
    }),
    http.get("/cluster", () => HttpResponse.json(data.clusterView())),
    http.get("/cluster/info", () => HttpResponse.json(makeClusterInfo())),
    http.get("/cluster/stats", () => HttpResponse.json(data.clusterStats())),
    http.get("/clusters", () =>
      HttpResponse.json({
        items: [{ name: "local", displayName: "local", phase: "Healthy" as const, canViewInventory: true }],
      }),
    ),

    // Templates: all 8 screenshot templates
    http.get("/templates", () =>
      HttpResponse.json({
        items: data.templates,
      }),
    ),
    http.get("/templates/:name", ({ params }) => {
      const name = String(params.name);
      const template = data.templates.find((t) => t.metadata.name === name);
      // Only the single-get carries a configSchema: Settings > Game
      // configuration reads it, the wizard's list view must stay unchanged.
      const configSchema = data.templateConfigSchemas[name];
      if (template && configSchema) {
        return HttpResponse.json({ ...template, spec: { ...template.spec, configSchema } });
      }
      return HttpResponse.json(template ?? makeTemplate({ metadata: { name } }));
    }),

    // Servers: all 5 screenshot servers
    http.get("/servers", () =>
      HttpResponse.json({
        items: data.servers,
      }),
    ),
    http.get("/servers/:name([^:/]+)", ({ params, cookies }) => {
      const name = String(params.name);
      const server = data.servers.find((s) => s.metadata.name === name);
      if (!server) {
        return HttpResponse.json(makeServer({ metadata: { name, ...(name === "test-server-no-shares" ? { annotations: { "gameplane.local/owner-id": "1", "gameplane.local/owner": "admin" } } : {}) } }));
      }
      if (name === "mc-survival" && cookies.e2e_server_variant) {
        const variant = cookies.e2e_server_variant;
        if (variant === "idle") {
          return HttpResponse.json({
            ...server,
            spec: {
              ...server.spec,
              idle: { enabled: true, afterMinutes: 15 },
            },
            status: {
              ...server.status,
              idle: {
                reason: "counting down",
                emptySince: new Date(Date.now() - 150 * 1000).toISOString(),
              },
            },
          });
        }
        if (variant === "asleep") {
          return HttpResponse.json({
            ...server,
            spec: {
              ...server.spec,
              suspend: true,
            },
            status: {
              ...server.status,
              phase: "Suspended",
              idle: {
                asleep: true,
                asleepSince: new Date(Date.now() - 21600 * 1000).toISOString(),
              },
              agent: {
                ...server.status?.agent,
                playersOnline: null,
                playersMax: 0,
              },
              startedAt: undefined,
            },
          });
        }
        if (variant === "never-sleeps") {
          return HttpResponse.json({
            ...server,
            spec: {
              ...server.spec,
              idle: { enabled: true, afterMinutes: 60 },
            },
            status: {
              ...server.status,
              idle: { reason: "this game reports no player count" },
            },
          });
        }
        if (variant === "pvc-failed") {
          return HttpResponse.json({
            ...server,
            status: {
              ...server.status,
              phase: "Pending",
              conditions: [
                {
                  type: "Ready",
                  status: "False",
                  reason: "PVCProvisioningFailed",
                  message: 'PVC "mc-survival-data": StorageClass \'fast-nvme\' not found on cluster.',
                  lastTransitionTime: new Date(Date.now() - 1800 * 1000).toISOString(),
                },
              ],
              agent: {
                playersOnline: null,
                playersMax: 0,
                cpuMillicores: 0,
                cpuLimitMillicores: 2000,
                memoryBytes: 0,
                memoryLimitBytes: 4_000_000_000,
              },
              startedAt: undefined,
            },
          });
        }
        if (variant === "failed") {
          return HttpResponse.json({
            ...server,
            status: {
              ...server.status,
              phase: "Failed",
              conditions: [
                {
                  type: "Ready",
                  status: "False",
                  reason: "ImagePullBackOff",
                  message: 'Back-off pulling image "itzg/minecraft-server:bad-tag"',
                  lastTransitionTime: new Date(Date.now() - 300 * 1000).toISOString(),
                },
              ],
              agent: {
                playersOnline: null,
                playersMax: 0,
                cpuMillicores: 0,
                cpuLimitMillicores: 2000,
                memoryBytes: 0,
                memoryLimitBytes: 4_000_000_000,
              },
              startedAt: undefined,
            },
          });
        }
      }
      return HttpResponse.json(server);
    }),
    http.post("/servers", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        metadata?: { name?: string };
      } | null;
      return HttpResponse.json(
        makeServer({
          metadata: { name: body?.metadata?.name ?? "new-server", namespace: "default" },
        }),
      );
    }),
    http.put("/servers/:name([^:/]+)", ({ params }) =>
      HttpResponse.json(makeServer({ metadata: { name: String(params.name) } })),
    ),
    http.delete("/servers/:name([^:/]+)", () => new HttpResponse(null, { status: 204 })),

    // Lifecycle actions
    http.post(/\/servers\/[^/]+:start$/, () => new HttpResponse(null, { status: 202 })),
    http.post(/\/servers\/[^/]+:stop$/, () => new HttpResponse(null, { status: 202 })),
    http.post(/\/servers\/[^/]+:restart$/, () => new HttpResponse(null, { status: 202 })),
    http.post(/\/servers\/[^/]+:wake$/, () => new HttpResponse(null, { status: 202 })),
    http.post(/\/servers\/[^/]+:clone$/, async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        newName?: string;
      } | null;
      return HttpResponse.json(
        makeServer({
          metadata: { name: body?.newName ?? "cloned-server", namespace: "default" },
        }),
      );
    }),
    http.post(/\/servers\/[^/]+:wipe-data$/, () => new HttpResponse(null, { status: 202 })),
    http.post(/\/servers\/[^/]+:transfer$/, () => new HttpResponse(null, { status: 204 })),
    http.put(/\/servers\/[^/]+:collaborators$/, () => new HttpResponse(null, { status: 204 })),

    // Tunnel credentials
    http.get(/\/servers\/[^/]+:tunnel-credentials$/, () =>
      HttpResponse.json({ configured: false, secretName: "", keys: [] }),
    ),
    http.put(/\/servers\/[^/]+:tunnel-credentials$/, () => new HttpResponse(null, { status: 204 })),
    http.delete(/\/servers\/[^/]+:tunnel-credentials$/, () => new HttpResponse(null, { status: 204 })),

    // Capture
    http.post(/\/servers\/[^/]+:capture-enable(\?.*)?$/, () =>
      HttpResponse.json({
        name: "server-name",
        status: { capture: { enabled: true } },
      }),
    ),
    http.post(/\/servers\/[^/]+:capture-disable(\?.*)?$/, () =>
      HttpResponse.json({
        name: "server-name",
        status: { capture: { enabled: false } },
      }),
    ),
    http.get(/\/servers\/[^/]+:captures(\?.*)?$/, ({ cookies }) => {
      if (cookies.e2e_capture_variant === "list") {
        return HttpResponse.json({
          captures: [
            {
              captureId: "cap-001",
              serverName: "mc-survival",
              phase: "Completed",
              startedAt: "2026-09-12T14:00:00Z",
              completedAt: "2026-09-12T14:15:00Z",
              createdAt: "2026-09-12T14:00:00Z",
              expiresAt: "2026-09-19T14:00:00Z",
              bytesWritten: 2048,
              packetsWritten: 250,
              filter: "tcp port 25565",
            },
            {
              captureId: "cap-002",
              serverName: "mc-survival",
              phase: "Completed",
              startedAt: "2026-09-11T10:30:00Z",
              completedAt: "2026-09-11T10:45:00Z",
              createdAt: "2026-09-11T10:30:00Z",
              expiresAt: "2026-09-18T10:30:00Z",
              bytesWritten: 1536,
              packetsWritten: 180,
              filter: "tcp port 25565",
            },
          ],
          total: 2,
          limit: 100,
          offset: 0,
        });
      }
      return HttpResponse.json({ captures: [], total: 0, limit: 100, offset: 0 });
    }),
    http.get(/\/servers\/[^/]+:capture$/, ({ cookies }) => {
      if (cookies.e2e_capture_variant === "running") {
        return HttpResponse.json({
          captureId: "cap-running",
          serverName: "mc-survival",
          phase: "Running",
          startedAt: "2026-09-13T12:30:00Z",
          completedAt: null,
          createdAt: "2026-09-13T12:30:00Z",
          expiresAt: "2026-09-20T12:30:00Z",
          bytesWritten: 512,
          packetsWritten: 45,
          filter: "tcp port 25565",
        });
      }
      return HttpResponse.json({
        captureId: "cap-12345",
        serverName: "server-name",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      });
    }),
    http.post(/\/servers\/[^/]+:capture-start(\?.*)?$/, async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        filter?: string;
        maxDurationSeconds?: number;
        maxSizeBytes?: number;
        ttlSecondsAfterFinished?: number;
      } | null;

      // Validate BPF filter if provided
      if (body?.filter) {
        // Check for invalid syntax patterns
        if (body.filter === INVALID_BPF_FILTER_FIXTURE || body.filter.includes("{{{{")) {
          return new HttpResponse("Invalid BPF filter syntax: syntax error at position 12 (invalid token 'foo')\n", { status: 400 });
        }
      }

      return HttpResponse.json(
        {
          captureId: "cap-12345",
          serverName: "server-name",
          phase: "Pending",
          startedAt: null,
          completedAt: null,
          createdAt: "2026-08-23T00:00:00Z",
          expiresAt: "2026-08-30T00:00:00Z",
          bytesWritten: 0,
          packetsWritten: 0,
          filter: body?.filter || "tcp port 25565",
        },
        { status: 202 },
      );
    }),
    http.post(/\/servers\/[^/]+:capture-stop(\?.*)?$/, () =>
      HttpResponse.json({
        captureId: "cap-12345",
        serverName: "server-name",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      }),
    ),
    http.delete(/\/servers\/[^/]+:capture$/, () =>
      HttpResponse.json({ deleted: true, captureId: "cap-12345" }),
    ),
    http.get(/\/servers\/[^/]+:capture-file$/, () =>
      new HttpResponse(new Blob(["mock pcapng data"]), {
        headers: { "Content-Type": "application/octet-stream" },
      }),
    ),

    // Backups
    // T118 (specs/done_014-heroui-web-rebuild/tasks.md): both entries' spec.serverRef
    // is pinned to "mc-survival" (previously left at makeBackup()'s "alpha"
    // default despite the metadata.name already reading "test-server-01-…") so
    // ServerDetail's per-server Backups tab (which filters by
    // spec.serverRef.name) actually has rows to render for the screenshot.
    http.get("/backups", () =>
      HttpResponse.json({
        items: [
          makeBackup({
            metadata: { name: "mc-survival-nightly-0713", namespace: "default" },
            spec: { serverRef: { name: "mc-survival" } },
          }),
          makeBackup({
            metadata: { name: "mc-survival-nightly-0712", namespace: "default" },
            spec: { serverRef: { name: "mc-survival" } },
            status: {
              phase: "Failed",
              startTime: "2026-05-06T03:00:00Z",
              completionTime: "2026-05-06T03:00:30Z",
            },
          }),
        ],
      }),
    ),
    http.get("/backups/:name", ({ params }) => {
      const name = String(params.name);
      if (name === "mc-survival-nightly-0713") {
        return HttpResponse.json(
          makeBackup({
            metadata: { name, namespace: "default" },
            spec: { serverRef: { name: "mc-survival" } },
            status: {
              phase: "Succeeded",
              startTime: "2026-07-13T00:12:04Z",
              completionTime: "2026-07-13T00:14:41Z",
              size: "1.4 GiB",
              snapshotID: "a1b2c3d4e5f6",
            },
          }),
        );
      }
      if (name === "mc-survival-nightly-0712") {
        return HttpResponse.json(
          makeBackup({
            metadata: { name, namespace: "default" },
            spec: { serverRef: { name: "mc-survival" } },
            status: {
              phase: "Failed",
              startTime: "2026-05-06T03:00:00Z",
              completionTime: "2026-05-06T03:00:30Z",
            },
          }),
        );
      }
      return HttpResponse.json(makeBackup({ metadata: { name } }));
    }),
    http.post("/backups", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        metadata?: { name?: string; generateName?: string };
        spec?: { serverRef?: { name?: string } };
      } | null;
      const name =
        body?.metadata?.name ??
        `${body?.spec?.serverRef?.name ?? "alpha"}-manual-${Date.now()}`;
      return HttpResponse.json(makeBackup({ metadata: { name, namespace: "default" } }));
    }),
    http.delete("/backups/:name", () => new HttpResponse(null, { status: 204 })),

    // Schedules: use screenshot schedules
    http.get("/schedules", () => HttpResponse.json({ items: data.schedules })),
    http.get("/schedules/:name", ({ params }) =>
      HttpResponse.json(makeSchedule({ metadata: { name: String(params.name) } })),
    ),
    http.post("/schedules", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        metadata?: { name?: string; generateName?: string };
        spec?: { serverRef?: { name?: string }; schedule?: string };
      } | null;
      return HttpResponse.json(
        makeSchedule({
          metadata: {
            name: body?.metadata?.name ?? `${body?.spec?.serverRef?.name ?? "alpha"}-sched-1`,
            namespace: "default",
          },
        }),
      );
    }),
    http.put("/schedules/:name", ({ params }) =>
      HttpResponse.json(makeSchedule({ metadata: { name: String(params.name) } })),
    ),
    http.delete("/schedules/:name", () => new HttpResponse(null, { status: 204 })),

    // Restores
    http.get("/restores", () => HttpResponse.json({ items: data.restores })),
    http.post("/restores", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        metadata?: { name?: string; generateName?: string };
        spec?: { backupRef?: { name?: string }; serverRef?: { name?: string } };
      } | null;
      return HttpResponse.json(
        makeRestore({
          metadata: {
            name: body?.metadata?.name ?? `restore-${Date.now()}`,
            namespace: "default",
          },
          spec: {
            backupRef: { name: body?.spec?.backupRef?.name ?? "alpha-2026-05-07" },
            serverRef: { name: body?.spec?.serverRef?.name ?? "alpha" },
          },
        }),
      );
    }),
    http.delete("/restores/:name", () => new HttpResponse(null, { status: 204 })),

    // Backup destinations
    http.get("/backup-destinations", () =>
      HttpResponse.json({ items: [makeDestination()] }),
    ),
    http.post("/backup-destinations", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        name?: string;
        url?: string;
      } | null;
      return HttpResponse.json(
        makeDestination({ name: body?.name ?? "default", url: body?.url ?? "s3://x" }),
      );
    }),
    http.delete("/backup-destinations/:name", () => new HttpResponse(null, { status: 204 })),

    // Modules
    http.get("/modules", () => HttpResponse.json({ items: [makeModule()] })),
    http.get("/modules/catalog", () =>
      HttpResponse.json({
        items: [
          makeCatalog(),
          makeCatalog({
            name: "valheim-default",
            displayName: "Valheim",
            game: "valheim",
            installed: true,
            installedVersion: "0.218",
            moduleName: "valheim-default",
            phase: "Ready",
            versions: ["0.218", "0.217"],
            latestVersion: "0.218",
          }),
        ],
      }),
    ),
    http.get("/modules/sources", () =>
      HttpResponse.json({ items: [makeModuleSource()] }),
    ),
    http.get("/modules/:name", ({ params }) =>
      HttpResponse.json(makeModule({ metadata: { name: String(params.name) } })),
    ),
    http.post("/modules", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        module?: string;
        name?: string;
      } | null;
      return HttpResponse.json(
        makeModule({ metadata: { name: body?.name ?? body?.module ?? "minecraft-vanilla" } }),
      );
    }),
    http.patch("/modules/:name", ({ params }) =>
      HttpResponse.json(makeModule({ metadata: { name: String(params.name) } })),
    ),
    http.delete("/modules/:name", () => new HttpResponse(null, { status: 204 })),

    // Server events: return events for the requested server
    http.get("/servers/:name/events", ({ params }) => {
      const name = String(params.name);
      const serverEvents = data.events.filter((e) => e.object === name);
      return HttpResponse.json(serverEvents);
    }),

    // Servers (additional endpoints)
    http.get("/users/me/servers", () => HttpResponse.json({ items: data.servers })),
    http.get("/servers/:name/status", () =>
      HttpResponse.json([
        { id: "world-seed", value: "3825002917346286791" },
        { id: "difficulty", value: "Easy" },
      ]),
    ),

    // Mods
    // T118 (specs/done_014-heroui-web-rebuild/tasks.md): modpacks flipped true so
    // the Modpacks tab (screenshot slice2b) has a provider to browse —
    // matches valheim-default's registry.providers[].modpacks declaration
    // added to screenshotData.ts for the same task.
    http.get("/servers/:name/mods/registry/providers", ({ params }) =>
      // test-server-09 (template minecraft-modded) declares two registries
      // — modrinth + hangar — so the Mods browse screen (design GayoL) can
      // capture the provider tabs and category pills (specs/014h).
      // test-server-02 (template minecraft-modded) uses modrinth for the
      // mods-tab screenshot test (design tY6RD).
      // Every other server keeps the pre-existing single-provider default.
      HttpResponse.json(
        String(params.name) === "test-server-09"
          ? [
              { provider: "modrinth", available: true, modpacks: true },
              { provider: "hangar", available: true, modpacks: false },
            ]
          : String(params.name) === "test-server-02"
            ? [{ provider: "modrinth", available: true, modpacks: true }]
            : [{ provider: "thunderstore", available: true, modpacks: true }],
      ),
    ),
    http.get("/servers/:name/mods/registry/search", ({ params }) =>
      HttpResponse.json(
        String(params.name) === "test-server-02"
          ? screenshotModrinthProjects
          : data.registryProjects,
      ),
    ),
    http.get("/servers/:name/mods/registry/projects/:project/versions", () =>
      HttpResponse.json([]),
    ),
    http.get("/servers/:name/mods/updates", () =>
      HttpResponse.json({ checkedAt: "2026-09-02T15:40:00Z", updates: [] }),
    ),
    http.get("/servers/:name/mods/ids", () => HttpResponse.json([])),
    http.get("/servers/:name/mods", ({ params }) =>
      // test-server-02 (template minecraft-modded) is used by the sZtDi
      // screenshot test to capture the empty mods state (matching design
      // frame sZtDi which shows "0 installed" / "No mods installed.").
      // Every other server keeps the pre-existing fixture with 3 Fabric mods.
      HttpResponse.json(String(params.name) === "test-server-02" ? screenshotEmptyMods : data.installedMods)
    ),

    // Players
    http.get("/servers/:name/players", () =>
      HttpResponse.json(makePlayers({ online: 0, players: [] })),
    ),
    http.get("/servers/:name/players/banned", () => HttpResponse.json([])),
    http.post(/\/servers\/[^/]+\/players\/(kick|ban|unban)$/, () =>
      HttpResponse.json({ ok: true }),
    ),
    http.get("/servers/:name/players/whitelist", () => HttpResponse.json(["alice"])),
    http.post(/\/servers\/[^/]+\/players\/whitelist\/(add|remove)$/, () =>
      HttpResponse.json({ ok: true }),
    ),

    // Files
    http.get("/servers/:name/files/list", () =>
      HttpResponse.json([
        makeFileEntry({ name: ".cache", path: "/data/.cache", size: 0, dir: true }),
        makeFileEntry({ name: ".fabric", path: "/data/.fabric", size: 0, dir: true }),
        makeFileEntry({ name: "libraries", path: "/data/libraries", size: 0, dir: true }),
        makeFileEntry({ name: "logs", path: "/data/logs", size: 0, dir: true }),
        makeFileEntry({ name: "mods", path: "/data/mods", size: 0, dir: true }),
        makeFileEntry({ name: "versions", path: "/data/versions", size: 0, dir: true }),
        makeFileEntry({ name: "world", path: "/data/world", size: 0, dir: true }),
        makeFileEntry({ name: ".fabric-manifest.json", path: "/data/.fabric-manifest.json", size: 1024 }),
        makeFileEntry({ name: ".rcon-cli.yaml", path: "/data/.rcon-cli.yaml", size: 128 }),
        makeFileEntry({ name: "banned-ips.json", path: "/data/banned-ips.json", size: 2 }),
        makeFileEntry({ name: "eula.txt", path: "/data/eula.txt", size: 68 }),
        makeFileEntry({ name: "fabric-server-mc.1.21.4-loader.jar", path: "/data/fabric-server-mc.1.21.4-loader.jar", size: 4_500_000 }),
        makeFileEntry({ name: "ops.json", path: "/data/ops.json", size: 2 }),
        makeFileEntry({ name: "server.properties", path: "/data/server.properties", size: 1024 }),
        makeFileEntry({ name: "usercache.json", path: "/data/usercache.json", size: 512 }),
        makeFileEntry({ name: "whitelist.json", path: "/data/whitelist.json", size: 2 }),
      ]),
    ),
    http.get("/servers/:name/files/read", () => new HttpResponse("# mock file body\n", { status: 200 })),
    http.post("/servers/:name/files/write", () => new HttpResponse(null, { status: 204 })),
    http.post("/servers/:name/files/mkdir", () => new HttpResponse(null, { status: 204 })),
    http.delete("/servers/:name/files/delete", () => new HttpResponse(null, { status: 204 })),

    // Users: use screenshot users
    http.get("/users", () =>
      HttpResponse.json(data.users),
    ),
    http.post("/users", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        username?: string;
        role?: "admin" | "operator" | "viewer";
      } | null;
      return HttpResponse.json(
        makeUser({
          id: 99,
          username: body?.username ?? "new-user",
          role: body?.role ?? "viewer",
        }),
      );
    }),
    http.patch("/users/:id", ({ params }) =>
      HttpResponse.json(makeUser({ id: Number(params.id) })),
    ),
    http.delete("/users/:id", () => new HttpResponse(null, { status: 204 })),
    http.post("/users/:id/reset-password", () => new HttpResponse(null, { status: 204 })),

    // Roles + permissions
    http.get("/roles", () =>
      HttpResponse.json([
        {
          name: "admin",
          description: "Full access to all resources, including users, roles, and global config.",
          builtin: true,
          permissions: ["*"],
        },
        {
          name: "operator",
          description: "Manage game servers, backups, templates, and schedules.",
          builtin: true,
          permissions: ["servers:read", "servers:write"],
        },
        {
          name: "viewer",
          description: "Read-only access across the control panel.",
          builtin: true,
          permissions: ["servers:read"],
        },
      ]),
    ),
    http.get("/roles/permissions", () =>
      HttpResponse.json({
        groups: [
          {
            resource: "servers",
            label: "Game servers",
            permissions: [
              { key: "servers:read", label: "View servers", namespaced: true },
              { key: "servers:write", label: "Manage servers", namespaced: true },
            ],
          },
          {
            resource: "captures",
            label: "Network captures",
            permissions: [
              { key: "captures:manage", label: "Enable, start, stop, download, and delete packet captures", namespaced: true },
            ],
          },
          {
            resource: "users",
            label: "Users",
            permissions: [{ key: "users:manage", label: "Manage users", namespaced: false }],
          },
        ],
      }),
    ),
    http.post("/roles", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        name?: string;
        description?: string;
        permissions?: string[];
      } | null;
      return HttpResponse.json(
        {
          name: body?.name ?? "new-role",
          description: body?.description ?? "",
          builtin: false,
          permissions: body?.permissions ?? [],
        },
        { status: 201 },
      );
    }),
    http.patch("/roles/:name", async ({ params, request }) => {
      const body = (await request.json().catch(() => null)) as {
        description?: string;
        permissions?: string[];
      } | null;
      return HttpResponse.json({
        name: String(params.name),
        description: body?.description ?? "",
        builtin: false,
        permissions: body?.permissions ?? [],
      });
    }),
    http.delete("/roles/:name", () => new HttpResponse(null, { status: 204 })),
    http.get("/users/:id/bindings", () => HttpResponse.json([])),
    http.post("/users/:id/bindings", async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        roleName?: string;
        namespace?: string;
      } | null;
      return HttpResponse.json(
        { roleName: body?.roleName ?? "viewer", namespace: body?.namespace ?? "team-a" },
        { status: 201 },
      );
    }),
    http.delete("/users/:id/bindings/:role/:namespace", () => new HttpResponse(null, { status: 204 })),

    // Admin: audit events and config
    http.get("/admin/audit", ({ request }) => {
      const url = new URL(request.url);
      const limit = Number(url.searchParams.get("limit") ?? "50");
      // Return screenshot audit events up to limit
      return HttpResponse.json(data.auditEvents.slice(0, limit));
    }),
    // Audit chain integrity — default "verified" state (DxKOh). The
    // failure banner state (kIxaJ) is a one-off variant selected via the
    // "e2e_audit_verify_variant=failed" cookie (see slice4.spec.ts) rather
    // than a page.route override: MSW's Service Worker (msw/browser)
    // answers this route itself, and Playwright can't intercept a request
    // already resolved inside a Service Worker's fetch handler.
    http.get("/admin/audit/verify", ({ cookies }) => {
      if (cookies.e2e_audit_verify_variant === "failed") {
        return HttpResponse.json({
          ok: false,
          checked: 42,
          firstBadId: 286,
          message: "Integrity check failed — chain breaks at event #286",
        });
      }
      return HttpResponse.json({ ok: true, checked: data.auditEvents.length, message: "audit chain intact" });
    }),
    // GET /admin/config variants, selected via the "e2e_admin_config_variant"
    // cookie (see slice4.spec.ts) rather than a page.route override — same
    // Service Worker interception reason as /admin/audit/verify above.
    http.get("/admin/config", ({ cookies }) => {
      switch (cookies.e2e_admin_config_variant) {
        case "oidc":
          return HttpResponse.json(screenshotConfigWithOidc());
        case "oidc-empty-mappings":
          return HttpResponse.json(screenshotConfigOidcEmptyMappings());
        case "empty-storage-class":
          return HttpResponse.json(screenshotConfigEmptyStorageClass());
        case "registries-curseforge":
          return HttpResponse.json(screenshotConfigCurseforgeOnly());
        default:
          return HttpResponse.json(data.config());
      }
    }),
    // PUT /admin/config/:section — the "save rejected" variant (QgW58) is
    // selected via the "e2e_admin_config_put_variant=reject" cookie, same
    // reason as above.
    http.put("/admin/config/:section", ({ cookies }) => {
      if (cookies.e2e_admin_config_put_variant === "reject") {
        return HttpResponse.json(
          {
            error:
              "helmOverride.roleMappings.admin must not contain blank group names. No changes were saved.",
          },
          { status: 400 },
        );
      }
      return new HttpResponse(null, { status: 204 });
    }),
    http.post("/admin/notifications/sinks/:name/test", () =>
      HttpResponse.json({ delivered: true }),
    ),
    http.get("/admin/users", () => HttpResponse.json({ items: data.users })),

    // Admin — System Logs (Bq2Yg, slice 4). AdminLogsPage fetches this
    // plaintext stream directly (not through lib/api.ts), so it's mocked
    // as a plain HttpResponse rather than HttpResponse.json.
    http.get("/admin/system-logs/:component", () =>
      new HttpResponse(screenshotSystemLogLines.join("\n") + "\n", {
        status: 200,
        headers: {
          "Content-Type": "text/plain",
          "X-Gameplane-Pod": "gameplane-api-6f9c8d5b7-x2k9p",
        },
      }),
    ),
    // Share links: list, create, revoke, resolve (public), start (public)
    http.get(/\/servers\/[^/]+:shares$/, ({ request }) => {
      // Extract server name from URL
      const match = new URL(request.url).pathname.match(/\/servers\/([^/:]+):shares$/);
      const serverName = match?.[1];

      // Return empty list for test-server-no-shares (empty state)
      if (serverName === "test-server-no-shares") {
        return HttpResponse.json([]);
      }

      // Return mock share links for testing
      return HttpResponse.json([
        {
          id: "share-1",
          createdAt: "2026-07-28T00:00:00Z",
          expiresAt: null,
          canStart: true,
        },
        {
          id: "share-2",
          createdAt: "2026-06-01T00:00:00Z",
          expiresAt: "2026-06-08T00:00:00Z",
          canStart: false,
        },
      ]);
    }),
    http.post(/\/servers\/[^/]+:shares$/, async ({ request }) => {
      const body = (await request.json().catch(() => null)) as {
        expiresIn?: string;
        expiresAt?: string;
        neverExpires?: boolean;
        canStart?: boolean;
      } | null;
      // neverExpires wins over any expiresAt/expiresIn also present, and
      // returns expiresAt: null so component tests can assert on it.
      // Otherwise an explicit absolute expiresAt is echoed back, and finally
      // the deprecated expiresIn keeps its existing 7-day-default behavior
      // so currently-passing tests on that path are unaffected.
      const expiresAt: string | null = body?.neverExpires
        ? null
        : (body?.expiresAt ?? new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString());
      return HttpResponse.json({
        id: `share-${Math.random().toString(36).slice(2)}`,
        createdAt: new Date().toISOString(),
        expiresAt,
        canStart: body?.canStart ?? false,
        token: `token_${Math.random().toString(36).slice(2)}`,
      });
    }),
    http.delete("/servers/:name/shares/:id", () =>
      new HttpResponse(null, { status: 204 }),
    ),
    // Public share endpoints (no auth required)
    http.get("/shares/:token", () => {
      // Return public share info
      return HttpResponse.json({
        serverName: "mc-survival",
        status: "Running",
        address: {
          host: "game.example.com",
          port: 25565,
        },
        playersOnline: 3,
      });
    }),
    http.post("/shares/:token/start", () =>
      new HttpResponse(null, { status: 202 }),
    ),

    // WebSocket: PTY Console (registered before RCON console to match narrower pattern first)
    ws.link(`${wsOrigin}/ws/servers/*/console-pty*`).addEventListener("connection", ({ client }) => {
      // Send initial attachment message
      setTimeout(() => {
        const attachMsg = "[PTY] Attached to terminal\r\n";
        client.send(JSON.stringify({ kind: "stdout", body: btoa(attachMsg) }));
      }, 100);

      // Send sample output lines as base64-encoded stdout
      screenshotConsoleOutput.forEach((line, index) => {
        setTimeout(() => {
          const lineWithNewline = line + "\r\n";
          client.send(JSON.stringify({ kind: "stdout", body: btoa(lineWithNewline) }));
        }, 150 + index * 40);
      });

      // Echo stdin back
      client.addEventListener("message", (event: MessageEvent) => {
        try {
          const msg = JSON.parse(String(event.data)) as { kind: string; body?: string };
          if (msg.kind === "stdin" && msg.body) {
            // Echo the input
            const decoded = atob(msg.body);
            client.send(JSON.stringify({ kind: "stdout", body: btoa(decoded) }));
            // Send a simulated response
            const response = "Command executed\r\n";
            client.send(JSON.stringify({ kind: "stdout", body: btoa(response) }));
          } else if (msg.kind === "resize") {
            // Silently accept resize requests
          }
        } catch {
          // Ignore malformed messages
        }
      });
    }),

    // WebSocket: RCON Console
    ws.link(`${wsOrigin}/ws/servers/*/console*`).addEventListener("connection", ({ client }) => {
      // Wildcard /* matches across path segments, so /console-pty also matches /console*; skip if more specific handler should handle this
      const url = new URL(client.url);
      if (url.pathname.endsWith("/console-pty")) return;

      // Send initial connection message via RCON envelope
      setTimeout(() => {
        client.send(JSON.stringify({ kind: "out", body: "[Console] Connected to RCON" }));
      }, 100);

      // Send sample output lines at intervals
      screenshotConsoleOutput.forEach((line, index) => {
        setTimeout(() => {
          client.send(JSON.stringify({ kind: "out", body: line }));
        }, 150 + index * 40);
      });

      // Echo commands back to the client
      client.addEventListener("message", (event: MessageEvent) => {
        try {
          const msg = JSON.parse(String(event.data)) as { kind: string; body?: string };
          if (msg.kind === "cmd" && msg.body) {
            // Echo the command
            client.send(JSON.stringify({ kind: "out", body: `> ${msg.body}` }));
            // Send a simulated response
            client.send(JSON.stringify({ kind: "out", body: "Command executed" }));
          }
        } catch {
          // Ignore malformed messages
        }
      });
    }),

    // WebSocket: Pod Logs
    ws.link(`${wsOrigin}/ws/servers/*/logs/pod*`).addEventListener("connection", ({ client }) => {
      if (typeof document !== "undefined" && document.cookie.includes("e2e_server_variant=failed")) return;
      // Send log lines at intervals
      data.logLines.forEach((line, index) => {
        setTimeout(() => {
          client.send(line);
        }, 50 + index * 40);
      });
    }),

    // WebSocket: File Logs
    ws.link(`${wsOrigin}/ws/servers/*/logs*`).addEventListener("connection", ({ client }) => {
      // Wildcard /* matches across path segments, so /logs/pod also matches /logs*; skip if more specific handler should handle this
      const url = new URL(client.url);
      if (url.pathname.endsWith("/logs/pod")) return;
      if (typeof document !== "undefined" && document.cookie.includes("e2e_server_variant=failed")) return;

      // Send log lines at intervals (same as pod logs for demo)
      data.logLines.forEach((line, index) => {
        setTimeout(() => {
          client.send(line);
        }, 50 + index * 40);
      });
    }),
  ];
}
