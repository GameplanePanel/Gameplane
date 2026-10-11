import { APIError, api, csrfHeaders, withClusterParam, createRequestClient, Captures, Shares, type ResourceScope } from "@/lib/api";
import type { KeyedRegistryProvider } from "@/lib/config";
import type {
  AuditEvent,
  AuditVerifyResult,
  AppearanceMode,
  Backup,
  BackupDestination,
  BackupSchedule,
  BannedPlayer,
  CatalogEntry,
  ClusterInfo,
  ClusterRegistry,
  ClusterStats,
  ClusterView,
  CustomColorConfig,
  ExtendedUser,
  GameServer,
  GameTemplate,
  InstalledMod,
  ModID,
  ModMeta,
  ModUpdatesResponse,
  List,
  LoginProvidersResp,
  LoginResponse,
  Module,
  NodeJoinInfo,
  ModuleSource,
  ModuleSourceSpec,
  PermissionGroup,
  PlayersResp,
  PortOverride,
  RegistryFile,
  RegistryProject,
  RegistryProviderInfo,
  RegistryVersion,
  Restore,
  Role,
  RoleBinding,
  ServerEvent,
  StatusReading,
  ThemePresetId,
  ThemeType,
  User,
  UserThemePreferences,
} from "@/types";

// Typed wrappers around the generic api<T>() fetcher. Every URL and request
// shape is declared here once; callers reference these instead of restating
// path strings. The generic api<T>() remains available as an escape hatch.

export type LifecycleVerb = "start" | "stop" | "restart" | "wake";
export type ModerateAction = "kick" | "ban" | "unban";

// Helper to append namespace query param when provided.
function withNS(path: string, ns?: string): string {
  if (!ns) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}namespace=${encodeURIComponent(ns)}`;
}

// Helper to append cluster query param for multi-cluster support.
// Only appends when the current cluster is non-local (preserves back-compat).
export function withCluster(path: string, clusterId = "local"): string {
  return withClusterParam(path, clusterId);
}

export interface ServerCreate {
  name: string;
  description?: string;
  templateRef: { name: string };
  // Selects a GameTemplate.spec.versions[].id; flows into spec.version.
  version?: string;
  config?: Record<string, string>;
  storage?: { size?: string };
  networking?: {
    expose?: string;
    hostname?: string;
    sourceRanges?: string[];
    portOverrides?: PortOverride[];
    // Load-balancer address pool preference; only meaningful for
    // expose: LoadBalancer.
    addressPool?: string;
    address?: string;
  };
  resources?: unknown;
  nodeSelector?: Record<string, string>;
}

function gameServerEnvelope(input: ServerCreate) {
  const { name, description, ...spec } = input;
  return {
    apiVersion: "gameplane.local/v1alpha1",
    kind: "GameServer",
    metadata: {
      name,
      ...(description ? { annotations: { "gameplane.local/description": description } } : {}),
    },
    spec,
  };
}





export interface NamespacesResponse {
  namespaces: string[];
}

// Namespaces the caller may read servers in (F-263): the /servers page
// fans out per namespace instead of relying on scope.Resolve's single
// default, so GAMEPLANE_EXTRA_NAMESPACES installs show every namespace the
// viewer actually has servers:read on.




// Multi-cluster registry operations.
export const Clusters = {
  // list returns all registered clusters in the registry, including the local cluster.
  list: () => api<List<ClusterRegistry>>("/clusters"),
};

// envelope wraps a typed `spec` in the unstructured Kubernetes envelope the
// API expects for create/update. Either `name` or `generateName` is required.
function envelope<K extends string, S>(
  kind: K,
  ident: { name: string } | { generateName: string },
  spec: S,
) {
  return {
    apiVersion: "gameplane.local/v1alpha1",
    kind,
    metadata: ident,
    spec,
  };
}

export interface BackupCreate {
  serverRef: { name: string };
  repoRef?: { name: string; key: string };
  strategy?: "restic-snapshot" | "volume-snapshot";
  quiesce?: boolean;
  tags?: string[];
  /** Either name (explicit) or generateName (server-assigned) must be set. */
  name?: string;
  generateName?: string;
}



export interface ScheduleCreate {
  serverRef: { name: string };
  schedule: string;
  // Required for restic-snapshot; omitted for volume-snapshot.
  repoRef?: { name: string; key: string };
  strategy?: "restic-snapshot" | "volume-snapshot";
  retention?: BackupSchedule["spec"]["retention"];
  suspend?: boolean;
  name?: string;
  generateName?: string;
}



export interface RestoreCreate {
  backupRef: { name: string };
  serverRef: { name: string };
  name?: string;
  generateName?: string;
}



export interface BackupDestinationCreate {
  name: string;
  url: string;
  password: string;
}





export interface UserCreate {
  username: string;
  displayName?: string;
  email?: string;
  role: string;
  password?: string;
}

// All fields optional; omit to leave the column unchanged. The API
// distinguishes "absent" from "empty string" so a caller can clear an
// email by sending "".
export interface UserUpdate {
  displayName?: string;
  email?: string;
  role?: string;
}

// Theme preferences request bodies (contracts/user-preferences-api.md
// §1.2–1.3). PUT retention (FR-012): optional custom fields are omitted
// (never sent as null), so the server keeps stored values; only the reset
// endpoint clears customs.
export interface UserPreferencesUpdate {
  themeType: ThemeType;
  presetId: ThemePresetId;
  appearanceMode: AppearanceMode;
  customColors?: CustomColorConfig | null;
  customCssEnabled: boolean;
  customCss?: string | null;
}

export interface UserPreferencesReset {
  presetId?: ThemePresetId;
  appearanceMode?: AppearanceMode;
}

export const Users = {
  me: () => api<User>("/users/me"),
  list: () => api<ExtendedUser[]>("/users"),
  create: (body: UserCreate) =>
    api<ExtendedUser>("/users", { method: "POST", body }),
  update: (id: number, body: UserUpdate) =>
    api<ExtendedUser>(`/users/${id}`, { method: "PATCH", body }),
  remove: (id: number) =>
    api<void>(`/users/${id}`, { method: "DELETE" }),
  resetPassword: (id: number, password: string) =>
    api<void>(`/users/${id}/reset-password`, {
      method: "POST",
      body: { password },
    }),
  bindings: (id: number) => api<RoleBinding[]>(`/users/${id}/bindings`),
  addBinding: (id: number, body: RoleBinding) =>
    api<RoleBinding>(`/users/${id}/bindings`, { method: "POST", body }),
  removeBinding: (id: number, roleName: string, namespace: string, cluster = "local") =>
    api<void>(`/users/${id}/bindings/${encodeURIComponent(roleName)}/${encodeURIComponent(namespace)}?cluster=${encodeURIComponent(cluster)}`, {
      method: "DELETE",
    }),
  // Theme preferences (contracts/user-preferences-api.md §1.1–1.3): the
  // effective preferences (pink/system defaults when the user has no stored
  // row) arrive on the user profile itself; PUT applies an update without
  // clearing stored customs; POST reset is the only operation that deletes
  // them (FR-012).
  updatePreferences: (body: UserPreferencesUpdate) =>
    api<UserThemePreferences>("/users/me/preferences", { method: "PUT", body }),
  resetPreferences: (body?: UserPreferencesReset) =>
    api<UserThemePreferences>("/users/me/preferences/reset", { method: "POST", body }),
};

export interface RoleWrite {
  name?: string;
  description?: string;
  permissions: string[];
}

export const Roles = {
  list: () => api<Role[]>("/roles"),
  catalog: () => api<{ groups: PermissionGroup[] }>("/roles/permissions"),
  create: (body: RoleWrite & { name: string }) =>
    api<Role>("/roles", { method: "POST", body }),
  update: (name: string, body: Omit<RoleWrite, "name">) =>
    api<Role>(`/roles/${name}`, { method: "PATCH", body }),
  remove: (name: string) =>
    api<void>(`/roles/${name}`, { method: "DELETE" }),
};

export const Auth = {
  login: (body: { username: string; password: string }) =>
    api<LoginResponse>("/auth/login", { method: "POST", body }),
  logout: () => api<void>("/auth/logout", { method: "POST" }),
  // Per-provider start route. The Helm-flag provider ("helm", or an old
  // response without names) uses the legacy path — its state cookies and
  // IdP-registered callback live there.
  oidcStartURL: (name?: string) =>
    name && name !== "helm"
      ? `/auth/oidc/${encodeURIComponent(name)}/start`
      : "/auth/oidc/start",
  // Public, pre-auth: which login methods are enabled + their labels.
  providers: () => api<LoginProvidersResp>("/auth/providers"),
};

// Managed clientSecret Secrets behind dashboard-added identity providers
// (Admin Settings → Authentication). Mirrors the notification-sink
// secret flow: PUT the value, reference the returned name as configRef.
export const AuthProviders = {
  putSecret: (name: string, body: { clientSecret: string }) =>
    api<{ name: string; keys: string[] }>(
      `/admin/auth/providers/${encodeURIComponent(name)}/secret`,
      { method: "PUT", body },
    ),
  deleteSecret: (name: string) =>
    api<void>(`/admin/auth/providers/${encodeURIComponent(name)}/secret`, {
      method: "DELETE",
    }),
};

export interface AuditExportFilter {
  actor?: string;
  method?: string;
  status?: string;
}

export const Audit = {
  page: (limit: number, before: number) => {
    const qs = new URLSearchParams({ limit: String(limit) });
    if (before > 0) qs.set("before", String(before));
    return api<AuditEvent[]>(`/admin/audit?${qs.toString()}`);
  },
  verify: () => api<AuditVerifyResult>("/admin/audit/verify"),
  exportCsv: async (filter: AuditExportFilter = {}): Promise<Blob> => {
    const params = new URLSearchParams({ format: "csv" });
    if (filter.actor) params.set("actor", filter.actor);
    if (filter.method) params.set("method", filter.method);
    if (filter.status) params.set("status", filter.status);
    const res = await fetch(withCluster(`/admin/audit/export?${params.toString()}`), {
      method: "GET",
      credentials: "include",
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new APIError(res.status, text);
    }
    return res.blob();
  },
};

// SinkSecretBody carries a sink's credential material, keyed by kind:
// discord/slack/webhook take {url, authorization?}, ntfy {url, token?},
// smtp {host, port?, username?, password?, from, to, tls?}. The API
// stores it as the labelled Secret the sink's configRef points at.
export interface SinkSecretBody {
  kind: string;
  url?: string;
  authorization?: string;
  token?: string;
  host?: string;
  port?: string;
  username?: string;
  password?: string;
  from?: string;
  to?: string;
  tls?: string;
}

export const Notifications = {
  // Test-fires the *persisted* sink synchronously; the response carries the
  // real delivery outcome (502 body = the delivery error, URL-sanitized).
  test: (name: string) =>
    api<{ delivered: boolean }>(
      `/admin/notifications/sinks/${encodeURIComponent(name)}/test`,
      { method: "POST" },
    ),
  // Writes the sink's credential Secret (API-managed, labelled); returns
  // the Secret name to use as the sink's configRef. Values are never
  // echoed back.
  putSecret: (name: string, body: SinkSecretBody) =>
    api<{ name: string; keys: string[] }>(
      `/admin/notifications/sinks/${encodeURIComponent(name)}/secret`,
      { method: "PUT", body },
    ),
  // Best-effort removal of the API-managed Secret; user-created Secrets
  // are refused server-side.
  deleteSecret: (name: string) =>
    api<void>(`/admin/notifications/sinks/${encodeURIComponent(name)}/secret`, {
      method: "DELETE",
    }),
};

// Managed apiKey Secrets behind keyed mod-registry providers (curseforge,
// steam, nexus — Admin Settings → Mod registries). Mirrors the
// notification-sink / auth-provider secret flow: PUT the value, reference
// the returned name as the modRegistries row's configRef. The key is
// write-only — the server never echoes it back, so there is no `get`.
export const ModRegistries = {
  putSecret: (provider: KeyedRegistryProvider, apiKey: string) =>
    api<{ name: string; keys: string[] }>(
      `/admin/registries/${encodeURIComponent(provider)}/secret`,
      { method: "PUT", body: { apiKey } },
    ),
  deleteSecret: (provider: KeyedRegistryProvider) =>
    api<void>(`/admin/registries/${encodeURIComponent(provider)}/secret`, {
      method: "DELETE",
    }),
};

// FileEntry mirrors the agent's response shape (agent/openapi.yaml). The
// dashboard ignores `mode`, but it's typed here for completeness.
export interface FileEntry {
  name: string;
  path: string;
  size: number;
  dir: boolean;
  mode?: string;
  modTime?: string;
}

function filesBase(server: string): string {
  return `/servers/${encodeURIComponent(server)}/files`;
}

// Shared helper for the non-JSON file endpoints (read/write/mkdir/upload/
// delete). The agent speaks raw bytes and multipart for these, which the
// generic api<T>() helper can't express. Errors are still surfaced as
// APIError so callers and TanStack Query treat them uniformly.




// Historical + live log access. download fetches the whole current log
// file from the agent as an attachment; the two stream paths are the
// live WebSocket sources the Logs tab toggles between.


// Module catalog and install/uninstall surface. The dashboard reads
// the merged catalog from /modules/catalog and drives installs by
// creating Module CRs through /modules.

export interface InstallRequest {
  source: string;
  module: string;
  name?: string;
  version?: string;
}

export const Modules = {
  catalog: () => api<List<CatalogEntry>>("/modules/catalog"),
  list: () => api<List<Module>>("/modules"),
  install: (body: InstallRequest) =>
    api<Module>("/modules", { method: "POST", body }),
  upgrade: (name: string, version: string) =>
    api<Module>(`/modules/${name}`, { method: "PATCH", body: { version } }),
  uninstall: (name: string) =>
    api<void>(`/modules/${name}`, { method: "DELETE" }),
};

// UploadedModule is the parsed-metadata echo from a bundle upload
// (used as the dry-run preview before committing).
export interface UploadedModule {
  module: {
    name: string;
    displayName: string;
    version: string;
    game: string;
    summary?: string;
  };
  configMap?: string;
  dryRun?: boolean;
}

async function uploadBundle(
  source: string,
  file: Blob,
  opts: { dryRun?: boolean } = {},
): Promise<UploadedModule> {
  const path = `/modules/sources/${source}/upload${opts.dryRun ? "?dryRun=true" : ""}`;
  const res = await fetch(withCluster(path), {
    method: "POST",
    headers: csrfHeaders(),
    credentials: "include",
    body: file,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new APIError(res.status, text);
  }
  return res.json() as Promise<UploadedModule>;
}

export const ModuleSources = {
  list: () => api<List<ModuleSource>>("/modules/sources"),
  create: (name: string, spec: ModuleSourceSpec) =>
    api<ModuleSource>("/modules/sources", { method: "POST", body: { name, ...spec } }),
  update: (name: string, spec: ModuleSourceSpec) =>
    api<ModuleSource>(`/modules/sources/${name}`, { method: "PUT", body: spec }),
  remove: (name: string) =>
    api<void>(`/modules/sources/${name}`, { method: "DELETE" }),
  upload: (source: string, file: Blob, opts?: { dryRun?: boolean }) =>
    uploadBundle(source, file, opts),
  removeUpload: (source: string, module: string) =>
    api<void>(`/modules/sources/${source}/upload/${module}`, { method: "DELETE" }),
};

// Share link operations live in api.ts as `Shares` (it needs the api
// functions' clustering and auth-header handling), not here.

// Module Builder visual authoring, validation, preview, and export surface.

export interface BuilderPortDef {
  name: string;
  containerPort: number;
  protocol: string;
  advertise: boolean;
}

export interface BuilderScaffoldRequest {
  name: string;
  displayName?: string;
  archetype?: string;
  image?: string;
  ports?: BuilderPortDef[];
  categories?: string[];
  summary?: string;
  storageSize?: string;
  storageMountPath?: string;
}

export interface BuilderScaffoldResponse {
  moduleYaml: string;
  templateYaml: string;
  readmeMd: string;
  iconBase64?: string;
}

export interface BuilderValidationFinding {
  level: "ERROR" | "WARN";
  ruleId: string;
  file: string;
  line: number;
  field?: string;
  message: string;
  remediation?: string;
}

export interface BuilderValidateResponse {
  clean: boolean;
  errorCount: number;
  warningCount: number;
  findings: BuilderValidationFinding[];
}

export interface BuilderPreviewRequest {
  templateYaml: string;
  versionId?: string;
  memory?: string;
  config?: Record<string, string>;
}

export interface BuilderPreviewResponse {
  versionId?: string;
  resolvedImage: string;
  effectiveEnv: Array<{ name: string; value: string; source: string }>;
  computedConfig: Record<string, string>;
  ports: Array<{ name: string; containerPort: number; protocol: string; advertise?: boolean }>;
  storage: { size: string; mountPath: string };
}

export interface BuilderArchiveRequest {
  name: string;
  moduleYaml: string;
  templateYaml: string;
  readmeMd: string;
  iconBase64?: string;
}

export interface BuilderInstallRequest extends BuilderArchiveRequest {
  targetSource: string;
}

export type BuilderExportRequest = BuilderArchiveRequest | BuilderInstallRequest;

export interface BuilderExportInstallResponse {
  installed: boolean;
  moduleName: string;
}

export interface BuilderStorageDef {
  size: string;
  mountPath: string;
}

export interface BuilderEnvDef {
  name: string;
  value: string;
}

export interface BuilderConfigFieldDef {
  name: string;
  displayName?: string;
  description?: string;
  type: string;
  default?: string;
  required?: boolean;
  min?: number;
  max?: number;
}

export interface BuilderCapabilitiesDef {
  lifecycle?: {
    stop?: string[];
  };
}

export interface BuilderArchetypeDefinition {
  id: string;
  title: string;
  description: string;
  defaultImage: string;
  defaultPorts: BuilderPortDef[];
  defaultStorage: BuilderStorageDef;
  defaultEnv: BuilderEnvDef[];
  configSchema: BuilderConfigFieldDef[];
  capabilities: BuilderCapabilitiesDef;
  defaultCategories: string[];
}

export interface BuilderArchetypesResponse {
  archetypes: BuilderArchetypeDefinition[];
}

export const ModuleBuilder = {
  archetypes: () =>
    api<BuilderArchetypesResponse>("/modules/builder/archetypes", { method: "GET" }),
  scaffold: (body: BuilderScaffoldRequest) =>
    api<BuilderScaffoldResponse>("/modules/builder/scaffold", { method: "POST", body }),
  validate: (body: { moduleYaml: string; templateYaml: string }) =>
    api<BuilderValidateResponse>("/modules/builder/validate", { method: "POST", body }),
  preview: (body: BuilderPreviewRequest) =>
    api<BuilderPreviewResponse>("/modules/builder/preview", { method: "POST", body }),
  installToCluster: (body: BuilderInstallRequest) =>
    api<BuilderExportInstallResponse>("/modules/builder/export", { method: "POST", body }),
  downloadArchive: async (body: BuilderArchiveRequest): Promise<Blob> => {
    const res = await fetch(withCluster("/modules/builder/export"), {
      method: "POST",
      headers: { ...csrfHeaders(), "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new APIError(res.status, text);
    }
    return res.blob();
  },
};


type RequestClient = Pick<ReturnType<typeof createRequestClient>, "api" | "url" | "raw" | "Captures" | "Shares">;

function makeResourceEndpoints(request: RequestClient) {
  const api = request.api;
  const withCluster = (path: string, cluster?: string) => request.url(cluster ? withClusterParam(path, cluster) : path);
  const fetch = request.raw;
  const Servers = {
    // A bare list() call defaults to the server's default namespace scope
    // (scope.DefaultNamespace) exactly as before (F-263); pass ns to target
    // one of the namespaces from Namespaces.list() explicitly.
    list: (ns?: string, cluster?: string, signal?: AbortSignal) => api<List<GameServer>>(withNS("/servers", ns), { cluster, signal }),
    get: (name: string, ns?: string, cluster?: string, signal?: AbortSignal) => api<GameServer>(withNS(`/servers/${name}`, ns), { cluster, signal }),
    create: (body: ServerCreate, cluster?: string) =>
      api<GameServer>("/servers", { method: "POST", body: gameServerEnvelope(body), cluster }),
    update: (name: string, body: GameServer, ns?: string) =>
      api<GameServer>(withNS(`/servers/${name}`, ns), { method: "PUT", body }),
    remove: (name: string, ns?: string) =>
      api<void>(withNS(`/servers/${name}`, ns), { method: "DELETE" }),
    lifecycle: (name: string, verb: LifecycleVerb, ns?: string, cluster?: string) =>
      api<void>(withNS(`/servers/${name}:${verb}`, ns), { method: "POST", cluster }),
    clone: (name: string, newName: string, ns?: string) =>
      api<GameServer>(withNS(`/servers/${name}:clone`, ns), { method: "POST", body: { newName } }),
    // Suspends the server and asks the operator to wipe its data volume.
    // `confirm` must equal the server name.
    wipeData: (name: string, confirm: string, ns?: string) =>
      api<void>(withNS(`/servers/${name}:wipe-data`, ns), { method: "POST", body: { confirm } }),
    // Reassigns the server's (informational) owner to another user.
    transfer: (name: string, userId: number, ns?: string) =>
      api<void>(withNS(`/servers/${name}:transfer`, ns), { method: "POST", body: { userId } }),
    // Updates the collaborators list for a server. userIds and usernames are
    // merged; either or both can be provided. Empty list clears collaborators.
    setCollaborators: (name: string, ns: string, body: { userIds?: number[]; usernames?: string[] }) =>
      api<void>(withNS(`/servers/${name}:collaborators`, ns), { method: "PUT", body }),
    // Lists all servers where the caller is owner or collaborator (cluster-wide).
    getMyServers: (cluster?: string, signal?: AbortSignal) => api<List<GameServer>>("/users/me/servers", { cluster, signal }),
    // Live module-declared metrics for the Overview tab. The agent returns
    // [] when the game has no RCON, so the UI hides the panel.
    status: (name: string, ns?: string) => api<StatusReading[]>(withNS(`/servers/${name}/status`, ns)),
    // Recent Kubernetes events for the server's pod/StatefulSet/GameServer
    // (image pull, scheduling, crash-loop) — feeds the Overview events feed,
    // most useful while a new server is still provisioning.
    events: (name: string, ns?: string) => api<ServerEvent[]>(withNS(`/servers/${name}/events`, ns)),
    // Run a module-declared action (spec.capabilities.actions[]). params
    // are the user-supplied values for the action's declared inputs.
    runAction: (name: string, body: { id: string; params?: Record<string, string> }, ns?: string) =>
      api<{ ok: boolean; raw?: string }>(withNS(`/servers/${name}/actions/run`, ns), {
        method: "POST",
        body,
      }),
    // Mod/plugin management (spec.capabilities.mods). The agent enforces
    // the install policy (host allowlist, size cap); the dashboard just
    // lists, installs by URL, and removes by name.
    mods: (name: string, ns?: string) => api<InstalledMod[]>(withNS(`/servers/${name}/mods`, ns)),
    // meta records the registry identity in the agent's install manifest;
    // replaces performs an in-place upgrade (install new → remove old).
    installMod: (
      name: string,
      body: { url: string; name?: string; replaces?: string; meta?: ModMeta },
      ns?: string,
    ) => api<InstalledMod>(withNS(`/servers/${name}/mods/install`, ns), { method: "POST", body }),
    removeMod: (name: string, mod: string, ns?: string) =>
      api<void>(withNS(`/servers/${name}/mods?name=${encodeURIComponent(mod)}`, ns), {
        method: "DELETE",
      }),
    // Batch update check over the install manifest: every managed mod is
    // checked against its registry provider server-side in one call.
    modUpdates: (name: string, ns?: string) =>
      api<ModUpdatesResponse>(withNS(`/servers/${name}/mods/updates`, ns)),
    // Direct mod upload (multipart) — same name/extension/size checks as a
    // URL install; the manifest records provider "upload".
    uploadMod: async (name: string, file: File, ns?: string): Promise<InstalledMod> => {
      const fd = new FormData();
      fd.append("file", file, file.name);
      const res = await filesFetch(withNS(`/servers/${encodeURIComponent(name)}/mods/upload`, ns), {
        method: "POST",
        headers: csrfHeaders(),
        body: fd,
      });
      return (await res.json()) as InstalledMod;
    },
    // Registries the server's game declares, with availability (e.g.
    // CurseForge needs an API key) — drives the provider switch.
    registryProviders: (name: string, ns?: string) =>
      api<RegistryProviderInfo[]>(withNS(`/servers/${name}/mods/registry/providers`, ns)),
    // In-app registry browse (spec.capabilities.mods.registry). The API
    // resolves the active version's loader + game version, so the dashboard
    // sends only the browse params + which provider. type="modpack" drives
    // the Modpacks tab; an empty q is a valid browse. Returns 501 when the
    // game has no browsable registry / unknown provider.
    searchRegistry: (
      name: string,
      opts: {
        q?: string;
        provider?: string;
        type?: "mod" | "modpack";
        sort?: string;
        category?: string;
        limit?: number;
        offset?: number;
      } = {},
      ns?: string,
    ) => {
      const p = new URLSearchParams();
      if (opts.q) p.set("q", opts.q);
      if (opts.provider) p.set("provider", opts.provider);
      if (opts.type) p.set("type", opts.type);
      if (opts.sort) p.set("sort", opts.sort);
      if (opts.category) p.set("category", opts.category);
      p.set("limit", String(opts.limit ?? 24));
      if (opts.offset) p.set("offset", String(opts.offset));
      return api<RegistryProject[]>(withNS(`/servers/${name}/mods/registry/search?${p.toString()}`, ns));
    },
    modVersions: (name: string, project: string, provider?: string, ns?: string) =>
      api<RegistryVersion[]>(
        withNS(
          `/servers/${name}/mods/registry/projects/${encodeURIComponent(project)}/versions` +
            (provider ? `?provider=${encodeURIComponent(provider)}` : ""),
          ns,
        ),
      ),
    // Modpacks: resolve a pack's dependency files (deps-mode, e.g. Valheim) —
    // the dashboard installs each via installMod.
    modpackDeps: (name: string, project: string, provider?: string, ns?: string) =>
      api<RegistryFile[]>(
        withNS(
          `/servers/${name}/mods/registry/projects/${encodeURIComponent(project)}/modpack` +
            (provider ? `?provider=${encodeURIComponent(provider)}` : ""),
          ns,
        ),
      ),
    // Apply an env-mode modpack (e.g. Minecraft/itzg): pins the pack on the
    // server and restarts it.
    installModpack: (name: string, body: { ref: string }, provider?: string, ns?: string) =>
      api<{ ok: boolean }>(
        withNS(`/servers/${name}/modpack` + (provider ? `?provider=${encodeURIComponent(provider)}` : ""), ns),
        { method: "POST", body },
      ),
    // Mods-by-id (spec.capabilities.mods.idList): games whose server
    // downloads its own mods given a list of ids (ARK CurseForge ids,
    // Project Zomboid MOD_IDS, generic Steam Workshop lists) rather than the
    // agent dropping files into a mods directory. PUT replaces the whole
    // list in one write — every write restarts the server (it changes the
    // game container's env), so the dashboard batches edits locally and
    // saves once (see api/internal/handlers/mod_ids.go).
    modIDs: (name: string, ns?: string) => api<ModID[]>(withNS(`/servers/${name}/mods/ids`, ns)),
    setModIDs: (name: string, ids: ModID[], ns?: string) =>
      api<ModID[]>(withNS(`/servers/${name}/mods/ids`, ns), { method: "PUT", body: ids }),
    // Tunnel credentials management. PUT creates/updates the Secret and sets
    // credentialsSecretRef on the tunnel config; GET returns configured status
    // and which Secret holds it (never the value); DELETE clears the ref and
    // removes the Secret.
    setTunnelCredentials: (
      name: string,
      provider: "frp" | "tailscale" | "playit",
      values: Record<string, string>,
      ns?: string,
      cluster?: string,
    ) =>
      api<void>(withNS(`/servers/${name}:tunnel-credentials`, ns), {
        method: "PUT",
        body: { provider, values },
        cluster,
      }),
    getTunnelCredentials: (name: string, ns?: string) =>
      api<{ configured: boolean; secretName: string; keys: string[] }>(
        withNS(`/servers/${name}:tunnel-credentials`, ns),
      ),
    removeTunnelCredentials: (name: string, ns?: string) =>
      api<void>(withNS(`/servers/${name}:tunnel-credentials`, ns), { method: "DELETE" }),
  };

  const Templates = {
    list: (cluster?: string, signal?: AbortSignal) => api<List<GameTemplate>>("/templates", { cluster, signal }),
    get: (name: string, cluster?: string, signal?: AbortSignal) => api<GameTemplate>(`/templates/${name}`, { cluster, signal }),
  };

  const Namespaces = {
    list: (cluster?: string, signal?: AbortSignal) => api<NamespacesResponse>("/namespaces", { cluster, signal }),
  };

  const Cluster = {
    info: (cluster?: string, signal?: AbortSignal) => api<ClusterInfo>("/cluster/info", { cluster, signal }),
    stats: (cluster?: string, signal?: AbortSignal) => api<ClusterStats>("/cluster/stats", { cluster, signal }),
    view: (cluster?: string, signal?: AbortSignal) => api<ClusterView>("/cluster", { cluster, signal }),
    // Credential-minting ops (admin-only; 501 unless clusterOps is enabled).
    addNode: (cluster?: string) => api<NodeJoinInfo>("/cluster/nodes:join", { method: "POST", cluster }),
    // kubeconfig is a file download, so it bypasses api()'s JSON handling.
    kubeconfig: async (cluster?: string): Promise<Blob> => {
      const res = await fetch(withCluster("/cluster/kubeconfig", cluster), {
        method: "POST",
        credentials: "include",
        headers: csrfHeaders(),
      });
      if (!res.ok) throw new APIError(res.status, await res.text().catch(() => ""));
      return res.blob();
    },
  };

  const Backups = {
    list: (ns?: string, cluster?: string, signal?: AbortSignal) => api<List<Backup>>(withNS("/backups", ns), { cluster, signal }),
    get: (name: string, ns?: string) => api<Backup>(withNS(`/backups/${name}`, ns)),
    create: (opts: BackupCreate, ns?: string) => {
      const { name, generateName, ...spec } = opts;
      const ident = name ? { name } : { generateName: generateName ?? `${spec.serverRef.name}-manual-` };
      return api<Backup>(withNS("/backups", ns), { method: "POST", body: envelope("Backup", ident, spec) });
    },
    remove: (name: string, ns?: string) =>
      api<void>(withNS(`/backups/${name}`, ns), { method: "DELETE" }),
  };

  const Schedules = {
    list: (ns?: string) => api<List<BackupSchedule>>(withNS("/schedules", ns)),
    create: (opts: ScheduleCreate, ns?: string) => {
      const { name, generateName, ...spec } = opts;
      const ident = name ? { name } : { generateName: generateName ?? `${spec.serverRef.name}-sched-` };
      return api<BackupSchedule>(withNS("/schedules", ns), {
        method: "POST",
        body: envelope("BackupSchedule", ident, spec),
      });
    },
    // Read-modify-write: fetches the current object, applies changes to its
    // spec, and PUTs the merged result. Used for the suspend toggle.
    patchSpec: async (name: string, patch: Partial<BackupSchedule["spec"]>, ns?: string) => {
      const current = await api<BackupSchedule>(withNS(`/schedules/${name}`, ns));
      const next = { ...current, spec: { ...current.spec, ...patch } };
      return api<BackupSchedule>(withNS(`/schedules/${name}`, ns), { method: "PUT", body: next });
    },
    remove: (name: string, ns?: string) =>
      api<void>(withNS(`/schedules/${name}`, ns), { method: "DELETE" }),
  };

  const Restores = {
    list: (ns?: string) => api<List<Restore>>(withNS("/restores", ns)),
    create: (opts: RestoreCreate, ns?: string) => {
      const { name, generateName, ...spec } = opts;
      const ident = name ? { name } : { generateName: generateName ?? "restore-" };
      return api<Restore>(withNS("/restores", ns), { method: "POST", body: envelope("Restore", ident, spec) });
    },
  };

  const BackupDestinations = {
    list: () => api<List<BackupDestination>>("/backup-destinations"),
    // POST is also used to rotate the password of an existing destination —
    // the server treats {name} as the upsert key.
    upsert: (body: BackupDestinationCreate) =>
      api<BackupDestination>("/backup-destinations", { method: "POST", body }),
    remove: (name: string) =>
      api<void>(`/backup-destinations/${name}`, { method: "DELETE" }),
  };

  const Players = {
    snapshot: (server: string, ns?: string) => api<PlayersResp>(withNS(`/servers/${server}/players`, ns)),
    banned: (server: string, ns?: string) =>
      api<BannedPlayer[]>(withNS(`/servers/${server}/players/banned`, ns)),
    moderate: (
      server: string,
      action: ModerateAction,
      body: { name: string; reason?: string },
      ns?: string,
    ) =>
      api<{ ok: boolean; raw?: string }>(
        withNS(`/servers/${server}/players/${action}`, ns),
        { method: "POST", body },
      ),
    whitelist: (server: string, ns?: string) =>
      api<string[]>(withNS(`/servers/${server}/players/whitelist`, ns)),
    whitelistAdd: (server: string, name: string, ns?: string) =>
      api<{ ok: boolean; raw?: string }>(
        withNS(`/servers/${server}/players/whitelist/add`, ns),
        { method: "POST", body: { name } },
      ),
    whitelistRemove: (server: string, name: string, ns?: string) =>
      api<{ ok: boolean; raw?: string }>(
        withNS(`/servers/${server}/players/whitelist/remove`, ns),
        { method: "POST", body: { name } },
      ),
  };

  async function filesFetch(input: string, init: RequestInit = {}): Promise<Response> {
    const res = await fetch(withCluster(input), { credentials: "include", ...init });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new APIError(res.status, text);
    }
    return res;
  }

  const Files = {
    list: (server: string, path: string, ns?: string) =>
      api<FileEntry[]>(withNS(`${filesBase(server)}/list?path=${encodeURIComponent(path)}`, ns)),
    read: async (server: string, path: string, ns?: string): Promise<string> => {
      const res = await filesFetch(
        withNS(`${filesBase(server)}/read?path=${encodeURIComponent(path)}`, ns),
      );
      return res.text();
    },
    write: async (
      server: string,
      path: string,
      content: string | Blob,
      ns?: string,
    ): Promise<void> => {
      await filesFetch(withNS(`${filesBase(server)}/write?path=${encodeURIComponent(path)}`, ns), {
        method: "POST",
        headers: { "Content-Type": "application/octet-stream", ...csrfHeaders() },
        body: content,
      });
    },
    create: async (
      server: string,
      path: string,
      content: string | Blob,
      ns?: string,
    ): Promise<void> => {
      await filesFetch(withNS(`${filesBase(server)}/create?path=${encodeURIComponent(path)}`, ns), {
        method: "POST",
        headers: { "Content-Type": "application/octet-stream", ...csrfHeaders() },
        body: content,
      });
    },
    mkdir: async (server: string, path: string, ns?: string): Promise<void> => {
      await filesFetch(withNS(`${filesBase(server)}/mkdir?path=${encodeURIComponent(path)}`, ns), {
        method: "POST",
        headers: csrfHeaders(),
      });
    },
    remove: async (
      server: string,
      path: string,
      recursive = false,
      ns?: string,
    ): Promise<void> => {
      const qs = `path=${encodeURIComponent(path)}${recursive ? "&recursive=true" : ""}`;
      await filesFetch(withNS(`${filesBase(server)}/delete?${qs}`, ns), {
        method: "DELETE",
        headers: csrfHeaders(),
      });
    },
    upload: async (
      server: string,
      dir: string,
      files: FileList | File[],
      ns?: string,
    ): Promise<void> => {
      const fd = new FormData();
      for (const f of Array.from(files)) fd.append("files", f, f.name);
      await filesFetch(withNS(`${filesBase(server)}/upload?path=${encodeURIComponent(dir)}`, ns), {
        method: "POST",
        headers: csrfHeaders(),
        body: fd,
      });
    },
    downloadURL: (server: string, path: string, ns?: string) =>
      withCluster(withNS(`${filesBase(server)}/download?path=${encodeURIComponent(path)}`, ns)),
  };

  const Logs = {
    downloadURL: (server: string, ns?: string) =>
      withCluster(withNS(`/servers/${encodeURIComponent(server)}/logs/download`, ns)),
    // Live tail of the configured game log file, via the agent (mTLS).
    // Requests the last 500 lines before following new output.
    fileStreamPath: (server: string, ns?: string) =>
      withCluster(withNS(`/ws/servers/${encodeURIComponent(server)}/logs?tail=500`, ns)),
    // Live stream of the game container's stdout via the pod-log API.
    // Shows download/config output during startup — before the game's own
    // log file exists — and works even when agent mTLS isn't configured.
    podStreamPath: (server: string, ns?: string) =>
      withCluster(withNS(`/ws/servers/${encodeURIComponent(server)}/logs/pod?from=start`, ns)),
  };
  return { Servers, Templates, Namespaces, Cluster, Backups, Schedules, Restores, BackupDestinations, Players, Files, Logs, Captures: request.Captures, Shares: request.Shares };
}

/** Capture cluster/namespace once before starting any asynchronous operation. */
export function createResourceClient(scope: ResourceScope, signal?: AbortSignal): ResourceClient {
  const captured = Object.freeze({ ...scope });
  return {
    ...makeResourceEndpoints(createRequestClient(captured, signal)),
    withSignal: (nextSignal: AbortSignal) => createResourceClient(captured, nextSignal),
  };
}

export type ResourceClient = ReturnType<typeof makeResourceEndpoints> & {
  withSignal: (signal: AbortSignal) => ResourceClient;
};

// Legacy standalone endpoints are explicitly local unless a method accepts an explicit cluster.
export const { Servers, Templates, Namespaces, Cluster, Backups, Schedules, Restores, BackupDestinations, Players, Files, Logs } = makeResourceEndpoints({
  api, url: (path) => withClusterParam(path), raw: (path, init) => fetch(path, init), Captures, Shares,
});
