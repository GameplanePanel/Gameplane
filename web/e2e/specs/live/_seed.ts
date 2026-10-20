import type { APIRequestContext, APIResponse } from "@playwright/test";

// Shared helpers for the live-mode data-screen specs. These talk to the
// REAL Gameplane API (through vite's proxy onto the kubectl port-forward
// globalSetup spawns) using the admin session cookies in storageState.
//
// Why seed through the API instead of reusing the Go e2e suite's fixtures:
// the Go suite registers a t.Cleanup for everything it creates, so by the
// time the Playwright job runs those CRs are already gone. Each live spec
// therefore creates the real objects it asserts on and deletes them again,
// which keeps it deterministic and independent of Go-suite test ordering.

// Re-export loginIfNeeded for live specs. The live specs own the navigation step:
// each must navigate and settle the page before calling loginIfNeeded.
export { loginIfNeeded } from "../../pages/LoginPage";

// seedHeaders builds the headers a seed mutation needs:
//
//   - X-Gameplane-CSRF — the API enforces double-submit CSRF on every mutating
//     request (the header must equal the gameplane_csrf cookie; api/internal/
//     auth/sessions.go). The browser app echoes it automatically (lib/api.ts);
//     an APIRequestContext does not, so we read the token out of storageState
//     and set it ourselves. Without this every seed POST/DELETE would 403.
//   - Accept: application/json — vite's dev proxy serves the SPA shell for
//     text/html navigations on collision paths like /servers, and forwards
//     everything else to the API. Pinning Accept keeps the seed request on
//     the API side of that bypass regardless of Playwright's default.
export async function seedHeaders(request: APIRequestContext): Promise<Record<string, string>> {
  const state = await request.storageState();
  const token = state.cookies.find((c) => c.name === "gameplane_csrf")?.value ?? "";
  return { "X-Gameplane-CSRF": token, Accept: "application/json" };
}

// expectSeedOk throws (failing the spec) unless the seed request succeeded.
// 409 Conflict is tolerated: a previous run that died before cleanup may
// have leaked the object, and an existing object is just as usable here.
async function expectSeedOk(res: APIResponse, what: string): Promise<void> {
  if (!res.ok() && res.status() !== 409) {
    throw new Error(`${what} failed: ${res.status()} ${await res.text().catch(() => "")}`);
  }
}

export interface Seeded {
  name: string;
  // Best-effort teardown — safe to call even if the object is already gone.
  // Takes a live request context: Playwright disposes the beforeAll request
  // before afterAll runs, so the cleanup must use afterAll's own `request`
  // rather than closing over the (now-closed) one used to seed.
  cleanup: (request: APIRequestContext) => Promise<void>;
}

// seedTemplate creates a minimal, schema-valid busybox GameTemplate
// (mirrors test/e2e's applyBusyboxTemplate). `sleep` keeps the pod alive so
// agent-backed screens have a live sidecar to read from. Cluster-scoped, so
// no namespace is needed.
export async function seedTemplate(request: APIRequestContext, name: string): Promise<Seeded> {
  const res = await request.post("/templates", {
    headers: await seedHeaders(request),
    data: {
      apiVersion: "gameplane.local/v1alpha1",
      kind: "GameTemplate",
      metadata: { name },
      spec: {
        displayName: `E2E live ${name}`,
        game: "busybox",
        version: "1",
        image: "busybox:1.36",
        command: ["sh", "-c", "sleep 100000"],
        ports: [{ name: "noop", containerPort: 12345, advertise: true, protocol: "TCP" }],
        // Without a declared console mode, resolveConsoleMode() (lib/
        // capabilities.ts) defaults to "none" and ServerDetail hides the
        // Console tab entirely — servers-core.spec.ts's "all six core tabs
        // render" test clicks it, so the seed must opt in.
        consoleMode: "pty",
      },
    },
  });
  await expectSeedOk(res, `seed template ${name}`);
  return {
    name,
    cleanup: async (req: APIRequestContext) => {
      await req
        .delete(`/templates/${name}`, { headers: await seedHeaders(req) })
        .catch(() => undefined);
    },
  };
}

export interface SeedServerOpts {
  name: string;
  template: string;
  description?: string;
}

// seedServer creates a namespaced GameServer referencing `template`. The
// API defaults the namespace to scope.DefaultNamespace ("gameplane-games") —
// the same default the dashboard reads — so seeded and rendered data line up
// without passing ?namespace=. The description annotation mirrors what the
// create-server wizard writes (gameplane.local/description).
export async function seedServer(request: APIRequestContext, opts: SeedServerOpts): Promise<Seeded> {
  const res = await request.post("/servers", {
    headers: await seedHeaders(request),
    data: {
      apiVersion: "gameplane.local/v1alpha1",
      kind: "GameServer",
      metadata: {
        name: opts.name,
        ...(opts.description
          ? { annotations: { "gameplane.local/description": opts.description } }
          : {}),
      },
      spec: { templateRef: { name: opts.template } },
    },
  });
  await expectSeedOk(res, `seed server ${opts.name}`);
  return {
    name: opts.name,
    cleanup: async (req: APIRequestContext) => {
      await req
        .delete(`/servers/${opts.name}`, { headers: await seedHeaders(req) })
        .catch(() => undefined);
    },
  };
}

// Generic mutating call with the CSRF header. Path is relative to the request's baseURL.
export async function apiSend(
  request: APIRequestContext,
  method: "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  data?: unknown,
): Promise<APIResponse> {
  return request.fetch(path, {
    method,
    headers: await seedHeaders(request),
    ...(data === undefined ? {} : { data }),
  });
}

export interface SeededUser extends Seeded {
  id: number;
  username: string;
  password: string;
}

// POST /users (users.go:113). password must be >= 12 chars (auth.MinPasswordLen, password.go:61);
// username must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$ (validate.go:17); role must exist
// (default "viewer"). Response JSON includes numeric `id`.
export async function seedUser(
  request: APIRequestContext,
  opts: { username: string; password?: string; role?: string; displayName?: string },
): Promise<SeededUser> {
  const password = opts.password ?? `E2e-${Math.random().toString(36).slice(2, 12)}-Pw1`;
  const res = await apiSend(request, "POST", "/users", {
    username: opts.username,
    password,
    role: opts.role ?? "viewer",
    ...(opts.displayName ? { displayName: opts.displayName } : {}),
  });
  if (!res.ok()) {
    throw new Error(`seed user ${opts.username} failed: ${res.status()} ${await res.text().catch(() => "")}`);
  }
  const { id } = (await res.json()) as { id: number };
  return {
    name: opts.username,
    username: opts.username,
    password,
    id,
    cleanup: (req) => deleteUser(req, id),
  };
}

// DELETE /users/{id}; best-effort, safe if already gone. Also removes the user's bindings.
export async function deleteUser(request: APIRequestContext, id: number): Promise<void> {
  await apiSend(request, "DELETE", `/users/${id}`).catch(() => undefined);
}

// POST /roles {name, description, permissions} (roles.go:116). Permission keys must exist in
// api/internal/rbac/catalog.go (e.g. "servers:read"); "*" is rejected.
export async function seedRole(
  request: APIRequestContext,
  opts: { name: string; description?: string; permissions: string[] },
): Promise<Seeded> {
  const res = await apiSend(request, "POST", "/roles", {
    name: opts.name,
    description: opts.description ?? "",
    permissions: opts.permissions,
  });
  await expectSeedOk(res, `seed role ${opts.name}`);
  return {
    name: opts.name,
    cleanup: async (req) => {
      await apiSend(req, "DELETE", `/roles/${encodeURIComponent(opts.name)}`).catch(() => undefined);
    },
  };
}

export const RESTIC_REPO_URL = "rest:http://gameplane-test-restic.gameplane-system.svc:8000/";
export const RESTIC_REPO_PASSWORD = "e2e-test-password-not-secret";
// Same values as test/e2e/fixtures/backup-restic-secret.yaml (stringData repo/password), so the
// repository initialised by TestE2E_ResticRepoReady is reused.

// POST /backup-destinations {name,url,password} (destinations.go:97-). Creates a Secret in
// gameplane-games labelled gameplane.local/backup-destination=true with keys repo+password.
// 400 unless name is a DNS label.
export async function seedBackupDestination(
  request: APIRequestContext,
  name: string,
): Promise<Seeded> {
  const res = await apiSend(request, "POST", "/backup-destinations", {
    name,
    url: RESTIC_REPO_URL,
    password: RESTIC_REPO_PASSWORD,
  });
  await expectSeedOk(res, `seed backup destination ${name}`);
  return {
    name,
    cleanup: async (req) => {
      await apiSend(req, "DELETE", `/backup-destinations/${name}`).catch(() => undefined);
    },
  };
}

// Best-effort: delete every Backup and Restore (default namespace) whose spec.serverRef.name === serverName.
export async function cleanupServerBackups(request: APIRequestContext, serverName: string): Promise<void> {
  for (const kind of ["backups", "restores"] as const) {
    const res = await request.get(`/${kind}`, { headers: { Accept: "application/json" } }).catch(() => null);
    if (!res || !res.ok()) continue;
    const list = (await res.json()) as {
      items?: Array<{ metadata: { name: string }; spec: { serverRef: { name: string } } }>;
    };
    for (const it of list.items ?? []) {
      if (it.spec.serverRef.name === serverName) {
        await apiSend(request, "DELETE", `/${kind}/${it.metadata.name}`).catch(() => undefined);
      }
    }
  }
}
