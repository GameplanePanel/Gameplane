// Thin fetch wrapper for the Gameplane API. Handles:
//   - base URL (relative; vite proxy forwards in dev, same-origin in prod)
//   - CSRF header injection for mutating requests (cookie → header)
//   - uniform error throwing so TanStack Query .error works consistently
//   - cluster query param threading for multi-cluster support

import type {
  CaptureStatus,
  NetworkCapture,
  NetworkCaptureList,
  ShareLink,
  ShareLinkPublic,
} from "@/types";

const CSRF_COOKIE = "gameplane_csrf";
const CSRF_HEADER = "X-Gameplane-CSRF";

function csrfToken(): string {
  const match = document.cookie.match(new RegExp("(?:^|; )" + CSRF_COOKIE + "=([^;]+)"));
  return match ? decodeURIComponent(match[1]) : "";
}

// withNS appends the namespace query param when provided. Local to this
// file so the capture client calls below don't need a dependency on
// web/src/lib/endpoints.ts (which itself depends on this file — importing
// back would be circular). Mirrors endpoints.ts's withNS exactly.
function withNS(path: string, ns?: string): string {
  if (!ns) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}namespace=${encodeURIComponent(ns)}`;
}

// These APIs administer the central installation. The similarly named
// /users/me/servers endpoint is a selected-cluster workload query.
export function isCentralAPI(path: string): boolean {
  const pathname = path.split("?")[0];
  if (pathname === "/users/me/servers") return false;
  return ["/auth", "/users", "/roles", "/admin", "/clusters", "/fleet", "/shares"].some(
    (prefix) => pathname === prefix || pathname.startsWith(prefix + "/"),
  );
}

export function withClusterParam(path: string, clusterId = "local"): string {
  if (isCentralAPI(path)) return path;
  // Explicit resource URLs are already scoped; never append a second selector.
  if (new URL(path, "http://gameplane.invalid").searchParams.has("cluster")) return path;
  if (clusterId === "local") return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}cluster=${encodeURIComponent(clusterId)}`;
}

// csrfHeaders returns the CSRF header for callers that bypass api() —
// raw fetch with a non-JSON body (multipart upload, plaintext write) still
// needs the same token the API enforces on all mutating requests.
export function csrfHeaders(): Record<string, string> {
  return { [CSRF_HEADER]: csrfToken() };
}

// ProblemFields is the subset of an RFC 9457 problem+json body the dashboard
// reads: the English detail plus the stable message identifier and its args.
interface ProblemFields {
  detail: string;
  messageId?: string;
  args?: Record<string, string | number>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// parseProblem returns the problem fields when the response is an
// application/problem+json body with a string detail; anything else (other
// content types, unparsable JSON, missing detail) yields null.
function parseProblem(body: string, contentType?: string): ProblemFields | null {
  if (!contentType?.toLowerCase().includes("application/problem+json")) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return null;
  }
  if (!isRecord(parsed) || typeof parsed.detail !== "string") return null;
  const fields: ProblemFields = { detail: parsed.detail };
  if (typeof parsed.messageId === "string") fields.messageId = parsed.messageId;
  if (isRecord(parsed.args)) {
    const args: Record<string, string | number> = {};
    for (const [key, value] of Object.entries(parsed.args)) {
      if (typeof value === "string" || typeof value === "number") args[key] = value;
    }
    fields.args = args;
  }
  return fields;
}

export class APIError extends Error {
  status: number;
  body: string;
  isHTML: boolean;
  retryAfter: string | null;
  /** English detail from a problem+json body; undefined for other responses. */
  detail?: string;
  /** Stable message identifier (problem+json `messageId`), used for translation. */
  messageId?: string;
  /** Interpolation arguments for `messageId` (problem+json `args`). */
  args?: Record<string, string | number>;
  constructor(status: number, body: string, statusText?: string, contentType?: string, retryAfter: string | null = null) {
    const isHTML = body.trimStart().startsWith("<") || (contentType?.includes("text/html") ?? false);
    const problem = parseProblem(body, contentType);
    const message = problem
      ? `${status}: ${problem.detail}`
      : isHTML ? `${status} ${statusText || ""}`.trim() : `${status}: ${body}`;
    super(message);
    this.status = status;
    this.body = body;
    this.isHTML = isHTML;
    this.retryAfter = retryAfter;
    if (problem) {
      // body carries the English sentence so existing callers that display
      // err.body keep showing the same text they showed before problem+json.
      this.body = problem.detail;
      this.detail = problem.detail;
      this.messageId = problem.messageId;
      this.args = problem.args;
    }
  }
}

export interface Options {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  /** Capture the intended cluster when a query or operation is created. */
  cluster?: string;
}

export async function api<T>(path: string, opts: Options = {}): Promise<T> {
  const method = opts.method ?? "GET";
  const headers: Record<string, string> = {
    Accept: "application/json, application/problem+json",
    "Content-Type": "application/json",
    ...(opts.headers ?? {}),
  };
  const mutating = method !== "GET" && method !== "HEAD" && method !== "OPTIONS";
  if (mutating) headers[CSRF_HEADER] = csrfToken();

  // Thread cluster query param for multi-cluster support.
  // Only append when non-local to preserve back-compat (local = default, omit param).
  const requestPath = withClusterParam(path, opts.cluster ?? "local");

  const res = await fetch(requestPath, {
    method,
    headers,
    credentials: "include",
    // Older web deployments cached HTML at these same URLs without Vary.
    // Bypass those entries; TanStack Query owns application data caching.
    cache: "no-store",
    signal: opts.signal,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new APIError(res.status, text, res.statusText, res.headers.get("content-type") ?? undefined, res.headers.get("retry-after"));
  }
  if (res.status === 204) return undefined as T;
  // Some 2xx responses (e.g. 202 Accepted from fire-and-forget actions) carry
  // no body at all — res.json() throws on an empty string, which would look
  // like a request failure to callers. Treat an empty body as success with
  // no payload instead of attempting to parse it.
  const text = await res.text();
  if (text === "") return undefined as T;
  return JSON.parse(text) as T;
}

// ---------------------------------------------------------------------
// Capture (network-capture sidecar) REST client calls — contracts/
// rest-api.md, verified against the response structs in
// api/internal/handlers/capture.go (the code wins where they disagree;
// see the T086 report for the two places that happened).
// ---------------------------------------------------------------------

// captureToggleResponse mirrors captureHandler's captureToggleResp
// (api/internal/handlers/capture.go) — the shape returned by both
// capture-enable and capture-disable. Not exported from web/src/types.ts,
// since it nests CaptureStatus under {name, status.capture} only for
// these two endpoints.
export interface CaptureToggleResponse {
  name: string;
  status: {
    capture: CaptureStatus;
  };
}

export interface CaptureStartBody {
  filter?: string;
  maxDurationSeconds: number;
  maxSizeBytes: number;
  ttlSecondsAfterFinished?: number;
}

// captureDeleteResponse mirrors captureDeleteResp (capture.go).
export interface CaptureDeleteResponse {
  deleted: boolean;
  captureId: string;
}

function makeCaptures(request: typeof api, scopedURL: (path: string) => string, raw: (path: string, init?: RequestInit) => Promise<Response> = (path, init) => fetch(path, init)) {
  const api = request;
  const withClusterParam = scopedURL;
  return {
  // POST /servers/{name}:capture-enable
  enable: (name: string, ns?: string) =>
    api<CaptureToggleResponse>(withNS(`/servers/${name}:capture-enable`, ns), { method: "POST" }),
  // POST /servers/{name}:capture-disable
  disable: (name: string, ns?: string) =>
    api<CaptureToggleResponse>(withNS(`/servers/${name}:capture-disable`, ns), { method: "POST" }),
  // POST /servers/{name}:capture-start — 202 Accepted; response fields
  // beyond captureStartResp's (ttlSecondsAfterFinished is present here,
  // stoppingReason is not) are typed optional on NetworkCapture.
  start: (name: string, body: CaptureStartBody, ns?: string) =>
    api<NetworkCapture>(withNS(`/servers/${name}:capture-start`, ns), { method: "POST", body }),
  // POST /servers/{name}:capture-stop — captureId goes in the JSON body,
  // not a query param (captureStopReq, capture.go).
  stop: (name: string, captureId: string, ns?: string) =>
    api<NetworkCapture>(withNS(`/servers/${name}:capture-stop`, ns), {
      method: "POST",
      body: { captureId },
    }),
  // GET /servers/{name}:captures
  list: (name: string, ns?: string) =>
    api<NetworkCaptureList>(withNS(`/servers/${name}:captures`, ns)),
  // GET /servers/{name}:capture?id={id}
  get: (name: string, id: string, ns?: string) =>
    api<NetworkCapture>(withNS(`/servers/${name}:capture?id=${encodeURIComponent(id)}`, ns)),
  // DELETE /servers/{name}:capture?id={id}
  remove: (name: string, id: string, ns?: string) =>
    api<CaptureDeleteResponse>(
      withNS(`/servers/${name}:capture?id=${encodeURIComponent(id)}`, ns),
      { method: "DELETE" },
    ),
  // GET /servers/{name}:capture-file?id={id} — binary PCAPNG (research.md
  // Decision 1: streamed straight through from the sidecar). Bypasses
  // api<T>()'s JSON handling, matching Cluster.kubeconfig /
  // Audit.exportCsv's Blob-download pattern (web/src/lib/endpoints.ts).
  // A GET carries no CSRF header, consistent with api<T>()'s own rule
  // (mutating = method !== GET/HEAD/OPTIONS).
  download: async (name: string, id: string, ns?: string): Promise<Blob> => {
    const path = withClusterParam(
      withNS(`/servers/${name}:capture-file?id=${encodeURIComponent(id)}`, ns),
    );
    const res = await raw(path, { method: "GET", credentials: "include" });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new APIError(res.status, text);
    }
    return res.blob();
  },
};
}

export const Captures = makeCaptures(api, (path) => withClusterParam(path));

// ConfigUpdateResponse mirrors the envelope returned by PUT /admin/config/{section}
// and DELETE /admin/config/auth/role-mappings/{role}, containing the updated section
// and its new value (as a JSON object, not raw message — api<T>() parses it).
export interface ConfigUpdateResponse {
  section: string;
  value: Record<string, unknown>;
}

export const Config = {
  // DELETE /admin/config/auth/role-mappings/{role} — resets one role's override
  // to its Helm-seeded value, returning the updated auth section.
  deleteRoleMapping: (role: string) =>
    api<ConfigUpdateResponse>(
      `/admin/config/auth/role-mappings/${encodeURIComponent(role)}`,
      { method: "DELETE" },
    ),
};

// Telemetry notice types mirror contracts/api-telemetry-http.md (spec 022).
export type TelemetryNoticeAction = "seen" | "keep" | "extended-off" | "all-off";

export interface TelemetryDestination {
  kind: "default" | "custom" | "bundled" | "disabled" | "none";
  // The URL host only; null for the disabled and none kinds.
  host: string | null;
}

// GET /admin/telemetry/notice. When pending is false the other keys are
// omitted, so a caller without config:manage gets exactly {"pending":false}.
export interface TelemetryNotice {
  pending: boolean;
  destination?: TelemetryDestination;
  fields?: { basic: string[]; extended: string[] };
}

// GET /admin/telemetry (contracts/api-telemetry-http.md; api/internal/handlers/telemetry.go).
export interface TelemetryConsent {
  basic: boolean;
  extended: boolean;
  source: "default" | "legacy" | "admin";
}

export interface TelemetryStatus {
  lastAttemptAt: string | null;
  lastSuccessAt: string | null;
  lastOutcome: "never" | "ok" | "failed";
  lastIdRotationAt: string | null;
}

export interface TelemetryPreviewExt {
  schema: number;
  installId: string;
  env: { k8s: string[]; distro: string[]; arch: string[]; nodes: string; deployment: string };
  games: { official: Record<string, number>; custom: number };
  features: {
    wakeOnConnect: boolean;
    tunnels: string[];
    capture: boolean;
    backups: boolean;
    sso: boolean;
    auditForwarding: boolean;
    clusters: string;
    db: string;
    language: string;
  };
  key: string;
  sentAt: string;
}

// The exact report the reporter would send now (telemetryschema.Report).
export interface TelemetryPreview {
  version: string;
  servers: number;
  templates: number;
  ext?: TelemetryPreviewExt;
}

export interface TelemetryInfo {
  destination: TelemetryDestination;
  // When true the stored consent is reported but has no effect.
  operatorDisabled: boolean;
  consent: TelemetryConsent;
  // null whenever extended is off.
  installId: string | null;
  // null when basic is off or the destination is disabled/none.
  preview: TelemetryPreview | null;
  status: TelemetryStatus;
}

export const Telemetry = {
  // GET /admin/telemetry (config:read).
  get: () => api<TelemetryInfo>("/admin/telemetry"),
  // POST /admin/telemetry/install-id (config:manage) — rotates the install ID.
  // 409 when extended is off or the operator disabled telemetry.
  resetInstallId: () =>
    api<{ installId: string }>("/admin/telemetry/install-id", { method: "POST" }),
  // GET /admin/telemetry/notice — the first-login notice for the caller.
  notice: () => api<TelemetryNotice>("/admin/telemetry/notice"),
  // POST /admin/telemetry/notice — "seen" records that the notice was rendered
  // (no ack, idempotent); the other actions dismiss it. Every action answers
  // 204, and a 409 means the notice was not pending.
  ack: (action: TelemetryNoticeAction) =>
    api<void>("/admin/telemetry/notice", { method: "POST", body: { action } }),
};

// ShareLinkCreateBody is the discriminated create-request shape callers
// build: exactly one of an absolute `expiresAt` instant or `neverExpires:
// true` (OD-1/OD-5), or the deprecated-for-one-release relative `expiresIn`
// duration string. `canStart` is always required. This narrows
// ShareLinkCreateRequest's flat optional fields into a shape the compiler
// can enforce at call sites, while the wire type (and the deprecated path)
// stay defined in `types.ts`.
export type ShareLinkCreateBody = { canStart: boolean } & (
  | { expiresAt: string; neverExpires?: never; expiresIn?: never }
  | { neverExpires: true; expiresAt?: never; expiresIn?: never }
  | { expiresIn: string; expiresAt?: never; neverExpires?: never }
  // All three omitted: legacy callers relying on the server's existing
  // omitted-expiry default (OD-7), e.g. api.test.ts's URL-encoding test.
  | { expiresAt?: never; neverExpires?: never; expiresIn?: never }
);

// Share link management. Authenticated operations (create, list, edit, revoke) require
// an active session and server ownership; public operations (resolve, start) are
// rate-limited but require no auth. Invalid links remain neutral; resolve keeps
// transient failures available for retry without exposing details in public UI.
function makeShares(request: typeof api) {
  const api = request;
  return {
  // POST /servers/{name}:shares (authenticated, owner-only).
  // Creates a new share link with an absolute expiry, no expiry, or (deprecated
  // for one release) a relative expiry, plus the start permission.
  create: (server: string, body: ShareLinkCreateBody, ns?: string) =>
    api<ShareLink>(withNS(`/servers/${encodeURIComponent(server)}:shares`, ns), {
      method: "POST",
      body,
    }),

  // GET /servers/{name}:shares (authenticated, owner-only).
  // Lists all share links for a server. The list never includes raw tokens.
  list: (server: string, ns?: string) =>
    api<ShareLink[]>(withNS(`/servers/${encodeURIComponent(server)}:shares`, ns)),

  setCanStart: (server: string, id: string, canStart: boolean, ns?: string) =>
    api<void>(
      withNS(`/servers/${encodeURIComponent(server)}/shares/${encodeURIComponent(id)}`, ns),
      { method: "PATCH", body: { canStart } }
    ),

  // DELETE /servers/{name}/shares/{id} (authenticated, owner-only).
  // Revokes a share link, rendering its token invalid immediately.
  revoke: (server: string, id: string, ns?: string) =>
    api<void>(
      withNS(`/servers/${encodeURIComponent(server)}/shares/${encodeURIComponent(id)}`, ns),
      { method: "DELETE" }
    ),

  // GET /shares/{token} (public, no auth, rate-limited).
  // Resolves a share link token to its public view: server name, status, address,
  // player count (if exposed). Returns the same response for invalid, expired, and
  // revoked tokens — callers cannot distinguish (FR-005 privacy rule). Transient
  // errors propagate so polling can retain its last public state and back off.
  resolve: async (token: string, signal?: AbortSignal): Promise<ShareLinkPublic> => {
    try {
      return await api<ShareLinkPublic>(`/shares/${encodeURIComponent(token)}`, { signal });
    } catch (err) {
      if (err instanceof APIError && err.status === 404) {
        // Invalid, expired and revoked tokens have the same neutral response.
        return {
          serverName: "",
          status: "Unknown",
        };
      }
      throw err;
    }
  },

  // POST /shares/{token}/start (public, no auth, rate-limited, only if canStart=true).
  // Wakes a sleeping server if the link permits it. Returns 202 Accepted on success.
  // Returns the same response for invalid/expired/revoked/no-permission states (FR-005).
  // Rejections propagate: a neutral 404 is unavailable, not an accepted wake.
  // The public page renders fixed copy without exposing error response details.
  start: (token: string, signal?: AbortSignal): Promise<void> =>
    api<void>(`/shares/${encodeURIComponent(token)}/start`, {
      method: "POST",
      signal,
    }),
};
}

export const Shares = makeShares(api);


export interface ResourceScope {
  readonly cluster: string;
  readonly namespace?: string;
}

/** Immutable request scope for a resource, independent of list filters and storage. */
export function createRequestClient(input: ResourceScope, signal?: AbortSignal) {
  const scope = Object.freeze({ cluster: input.cluster, namespace: input.namespace });
  if (!scope.cluster) throw new Error("A resource cluster is required.");
  const url = (path: string): string => {
    if (!path.startsWith("/") || path.startsWith("//")) throw new Error("Expected an API path.");
    if (isCentralAPI(path)) return path;
    const parsed = new URL(path, "http://gameplane.invalid");
    const clusters = parsed.searchParams.getAll("cluster");
    if (clusters.length > 1 || (clusters.length === 1 && clusters[0] !== scope.cluster)) {
      throw new Error("The request cluster does not match its resource.");
    }
    const namespaced = ["/servers", "/backups", "/schedules", "/restores", "/backup-destinations", "/ws/servers"].some(
      (prefix) => parsed.pathname === prefix || parsed.pathname.startsWith(prefix + "/"),
    );
    if (namespaced && scope.namespace) {
      const namespaces = parsed.searchParams.getAll("namespace");
      if (namespaces.length > 1 || (namespaces.length === 1 && namespaces[0] !== scope.namespace)) {
        throw new Error("The request namespace does not match its resource.");
      }
      parsed.searchParams.set("namespace", scope.namespace);
    }
    if (scope.cluster !== "local") parsed.searchParams.set("cluster", scope.cluster);
    return parsed.pathname + parsed.search;
  };
  const request = <T>(path: string, options: Options = {}): Promise<T> => {
    if (!isCentralAPI(path) && options.cluster && options.cluster !== scope.cluster) {
      throw new Error("The request cluster does not match its resource.");
    }
    const requestSignal = signal && options.signal ? AbortSignal.any([signal, options.signal]) : options.signal ?? signal;
    return api<T>(url(path), { ...options, cluster: scope.cluster, signal: requestSignal });
  };
  const raw = (path: string, init: RequestInit = {}): Promise<Response> => {
    const requestSignal = signal && init.signal ? AbortSignal.any([signal, init.signal]) : init.signal ?? signal;
    return fetch(url(path), { credentials: "include", cache: "no-store", ...init, signal: requestSignal });
  };
  return { scope, api: request, url, raw, Captures: makeCaptures(request, url, raw), Shares: makeShares(request) };
}

// ---------------------------------------------------------------------
// Personal MCP tokens (spec 037). Shapes mirror the JSON written by
// api/internal/auth/mcptokens.go (MCPToken) and the POST response of
// api/internal/handlers/mcp_tokens.go. The raw token is returned only by
// createMcpToken, once; it is never listed again.
// ---------------------------------------------------------------------

export interface McpToken {
  id: number;
  name: string;
  prefix: string;
  createdAt: string;
  expiresAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

export type CreatedMcpToken = McpToken & { token: string };

export type McpTokenLifetimeDays = 7 | 30 | 90 | 365;

// GET /users/me/mcp-tokens — the caller's tokens, newest first.
export const listMcpTokens = () => api<{ items: McpToken[] }>("/users/me/mcp-tokens");

// POST /users/me/mcp-tokens — the only response that carries the secret.
export const createMcpToken = (name: string, expiresInDays: McpTokenLifetimeDays) =>
  api<CreatedMcpToken>("/users/me/mcp-tokens", {
    method: "POST",
    body: { name, expiresInDays },
  });

// DELETE /users/me/mcp-tokens/{id} — 204.
export const revokeMcpToken = (id: number) =>
  api<void>(`/users/me/mcp-tokens/${encodeURIComponent(String(id))}`, { method: "DELETE" });

// GET /users/{id}/mcp-tokens (users:read) — another user's tokens.
export const listUserMcpTokens = (userId: number) =>
  api<{ items: McpToken[] }>(`/users/${encodeURIComponent(String(userId))}/mcp-tokens`);

// DELETE /users/{id}/mcp-tokens/{tokenId} (users:manage) — 204.
export const revokeUserMcpToken = (userId: number, tokenId: number) =>
  api<void>(
    `/users/${encodeURIComponent(String(userId))}/mcp-tokens/${encodeURIComponent(String(tokenId))}`,
    { method: "DELETE" },
  );

// ---------------------------------------------------------------------
// SFTP access (spec 040). Shapes mirror api/internal/auth/sftp_store.go
// (SFTPKey, SFTPPasswordInfo) and api/internal/handlers/sftp.go. The
// generated password is returned only by generateSftpPassword, once.
// ---------------------------------------------------------------------

export interface SftpKey {
  id: number;
  name: string;
  publicKey: string;
  fingerprint: string;
  createdAt: string;
  lastUsedAt: string | null;
}

export interface SftpPasswordInfo {
  length: number;
  createdAt: string;
  lastUsedAt: string | null;
}

export interface SftpEndpoint {
  address: string;
  port: number;
  hostKeyFingerprint: string;
}

// GET /users/me/sftp — the caller's username, endpoint, keys and password metadata.
export interface SftpSelf {
  username: string;
  endpoint: SftpEndpoint | null;
  keys: SftpKey[];
  password: SftpPasswordInfo | null;
}

// GET /users/{id}/sftp (users:manage) — another user's keys and password metadata.
export interface SftpAdminView {
  userId: number;
  username: string;
  keys: Array<Omit<SftpKey, "publicKey"> & { type: string }>;
  password: SftpPasswordInfo | null;
}

export type SftpPasswordLength = number;

export const SFTP_PASSWORD_MIN_LENGTH = 20;
export const SFTP_PASSWORD_MAX_LENGTH = 40;
export const SFTP_PASSWORD_DEFAULT_LENGTH = 32;

// GET /users/me/sftp
export const getSftpSelf = () => api<SftpSelf>("/users/me/sftp");

// POST /users/me/sftp/keys — 201 with the stored key.
export const addSftpKey = (name: string, publicKey: string) =>
  api<SftpKey>("/users/me/sftp/keys", { method: "POST", body: { name, publicKey } });

// DELETE /users/me/sftp/keys/{id} — 204.
export const deleteSftpKey = (id: number) =>
  api<void>(`/users/me/sftp/keys/${encodeURIComponent(String(id))}`, { method: "DELETE" });

// POST /users/me/sftp/password — the only response that carries the password.
export const generateSftpPassword = (length: SftpPasswordLength) =>
  api<{ password: string; length: number }>("/users/me/sftp/password", {
    method: "POST",
    body: { length },
  });

// DELETE /users/me/sftp/password — 204.
export const revokeSftpPassword = () =>
  api<void>("/users/me/sftp/password", { method: "DELETE" });

// GET /users/{id}/sftp (users:manage)
export const getUserSftp = (userId: number) =>
  api<SftpAdminView>(`/users/${encodeURIComponent(String(userId))}/sftp`);

// DELETE /users/{id}/sftp/keys/{keyId} (users:manage) — 204.
export const removeUserSftpKey = (userId: number, keyId: number) =>
  api<void>(
    `/users/${encodeURIComponent(String(userId))}/sftp/keys/${encodeURIComponent(String(keyId))}`,
    { method: "DELETE" },
  );

// DELETE /users/{id}/sftp/password (users:manage) — 204.
export const revokeUserSftpPassword = (userId: number) =>
  api<void>(`/users/${encodeURIComponent(String(userId))}/sftp/password`, { method: "DELETE" });

// Cluster SFTP endpoint (spec 040 US4; design Screen/Clusters — SFTP (remote-lab) 309702331-255).
// Shapes mirror api/internal/handlers/sftp_cluster.go (sftpClusterView, sftpTokenCreated) and
// auth.SFTPServiceToken. The token secret is returned only by createClusterSftpToken, once.
export interface ClusterSftpToken {
  id: number;
  cluster: string;
  prefix: string;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

export interface ClusterSftpView {
  cluster: string;
  local: boolean;
  address: string | null;
  hostKeyFingerprint: string | null;
  tokens: ClusterSftpToken[];
}

export interface ClusterSftpEndpoint {
  cluster: string;
  address: string;
  hostKeyFingerprint: string;
  updatedAt: string;
}

export interface ClusterSftpTokenCreated extends ClusterSftpToken {
  token: string;
}

// The API allows two active service tokens per cluster (auth.SFTPServiceToken limit).
export const CLUSTER_SFTP_TOKEN_LIMIT = 2;

const clusterSftpPath = (cluster: string) => `/clusters/${encodeURIComponent(cluster)}/sftp`;

// GET /clusters/{name}/sftp (cluster:manage) — endpoint and active tokens.
export const getClusterSftp = (cluster: string) => api<ClusterSftpView>(clusterSftpPath(cluster));

// PUT /clusters/{name}/sftp (cluster:manage) — refused for the panel's own cluster.
export const putClusterSftp = (cluster: string, body: { address: string; hostKeyFingerprint: string }) =>
  api<ClusterSftpEndpoint>(clusterSftpPath(cluster), { method: "PUT", body });

// POST /clusters/{name}/sftp-token (cluster:manage) — 201; the only response that carries the token.
export const createClusterSftpToken = (cluster: string) =>
  api<ClusterSftpTokenCreated>(`${clusterSftpPath(cluster)}-token`, { method: "POST" });

// DELETE /clusters/{name}/sftp-token/{id} (cluster:manage) — 204.
export const revokeClusterSftpToken = (cluster: string, tokenId: number) =>
  api<void>(`${clusterSftpPath(cluster)}-token/${encodeURIComponent(String(tokenId))}`, { method: "DELETE" });
