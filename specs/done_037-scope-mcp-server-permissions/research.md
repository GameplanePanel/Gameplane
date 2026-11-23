# Research: Scope MCP server permissions (spec 037)

Facts below were read from `master` at `73389d7a` (2026-11-22) and from the `mcp-server` submodule at `3e070f85`.

## Current state

- **Chart:** `charts/gameplane/templates/mcp-server.yaml` renders one ClusterRole `<release>-mcp-server-read` (get/list/watch on 7 Gameplane CRDs, pods, events; get on pods/log) bound cluster-wide to ServiceAccount `gameplane-mcp-server`. Skipped when `api.standalone`.
- **Managed game namespaces:** the chart helper `gameplane.operatorGameNamespaces` (`_helpers.tpl:182`) already returns `gamesNamespace` + `operator.gameNamespaces`, uniq, DNS-label-validated, as a JSON list. Seven templates use it.
- **MCP server:** `mcp-server/tools.go` registers 7 tools on a `*kube.Client` (`internal/kube/client.go`) built from `ctrl.GetConfig()`. "All namespaces" is a Kubernetes-wide list (`NamespaceAll`).
- **API auth:** `auth.SessionStore.Authenticate` (`api/internal/auth/sessions.go:166`) accepts only the session cookie; CSRF on mutating methods. Sessions store a SHA-256 digest of a 256-bit random value and carry `session_epoch`, which only a password reset bumps (`api/internal/handlers/users.go:837`). `lookup` reloads permissions (`LoadPerms`) on every request, so role and grant changes apply on the next request.
- **Legacy `api_tokens` table:** created in the frozen 001 migrations, stores raw tokens, no expiry; only referenced by a cleanup `DELETE` (`api/internal/db/users.go:45`). Unused.
- **Next common migration:** `023` (last is `022_user_role_source.sql`).
- **Cluster and namespace selection:** `?cluster=` (`scope.ResolveCluster`, unknown or unregistered → `ErrForbiddenCluster`) and `?namespace=` (`scope.Resolve`, allow-list `scope.AllowedNamespaces` = default games namespace + `GAMEPLANE_EXTRA_NAMESPACES`). `rbac.Middleware` authorizes the route permission against that cluster and namespace, with owner/collaborator fallback on `/servers/{name}...` paths (`api/internal/rbac/rbac.go:121-140`).
- **Reads the API already has:** CRD list/get on `/servers`, `/templates`, `/backups`, `/schedules`, `/restores` (`handlers/resources.go`); `/modules`, `/modules/sources` (`handlers/modules.go`); per-server events `/servers/{name}/events` (newest 50, `handlers/pod_events.go`); paged pod log history `/servers/{name}/logs/pod/history` (100-line pages, `ws/pod_history.go`); `/users/me/servers` (owned and collaborated servers); `/clusters` (filtered by the caller's grants); `/me`; admin `/admin/system-logs/{api|operator}` (local cluster only).
- **Missing for the MCP tools:** namespace pod list, pod get, namespace-wide raw events, a bounded plain pod log tail by pod and container.
- **Audit:** `audit.Middleware` records requests that `shouldLog` selects, with the actor set by `Authenticate` through `auth.SetActor`.
- **Dashboard:** personal settings live at `/settings/theme` (`Screen/Theme Settings` in `design.fig`). The Users page has a "Service accounts" placeholder tab for v1.1 machine tokens (`web/src/routes/Users.tsx:920`), which is a separate feature.

## Decisions

### R1. Token format and storage

- **Decision:** token = `gpmcp_` + 43 base64url characters (256 bits from `crypto/rand`). Stored as SHA-256 hex digest only, like sessions. New table `mcp_tokens` in `common/023_mcp_tokens.sql`. The legacy `api_tokens` table is left alone.
- **Rationale:** reuses the proven session digest scheme; the prefix lets secret scanners and the API recognise the token; a new table avoids the legacy raw-token column.
- **Alternatives:** reuse `api_tokens` (raw tokens at rest, no expiry; rejected); JWTs (cannot be revoked without a lookup anyway; rejected).

### R2. How the API accepts a token

- **Decision:** `Authenticate` keeps the cookie path unchanged. When there is no session cookie and the request carries `Authorization: Bearer gpmcp_…`, it looks the token up (digest, not expired, not revoked, `session_epoch` still matches the user), loads permissions with `LoadPerms`, marks the request context as token-authenticated, and sets the audit actor to `<username> (mcp)`. A new `MCPTokenGuard` middleware, mounted right after `Authenticate`, rejects every token-authenticated request whose method is not GET/HEAD or whose route is not in the allow-list (contracts/api-mcp-token-auth.md). WebSocket upgrades are never on the list.
- **Rationale:** permissions come from the same `rbac.Middleware`, owner fallback and handlers as the dashboard (FR-011, FR-020). The allow-list makes "read-only" a property of the API, not of the MCP client. Loading permissions per request gives FR-013 for free.
- **Alternatives:** method-only guard (some GETs are sensitive, such as capture file downloads or console WebSocket upgrades; rejected); a separate `/mcp/*` API surface (duplicates every permission rule; rejected by OD-2).

### R3. Which events end a token

- **Decision:** expiry, revoke by the user, revoke by an admin, user deletion (FK cascade), and password reset (stored `session_epoch` no longer matches). Role and grant changes do not end a token; they change what it can read on the next call.
- **Rationale:** matches how sessions behave, and FR-013 is met by the per-request permission load.

### R4. New API reads

- **Decision:** add four GET routes, all under the normal cluster/namespace scope and RBAC (contracts/api-reads.md):
  - `/pods?cluster&namespace&labelSelector` and `/pods/{name}?cluster&namespace`: RBAC segment `pods` → `servers:read` (namespaced).
  - `/kube-events?cluster&namespace&fieldSelector&labelSelector`: segment `kube-events` → `servers:read`. Newest 100 first, with the same truncation note the MCP server uses today. (`/events` is already the SSE feed.)
  - `/servers/{name}/pods` and `/servers/{name}/logs/pod/tail?pod&container&tailLines&previous`: under `/servers/{name}` so owners and collaborators without namespace `servers:read` reach their own servers through the existing fallback. The tail returns plain text, default 200 lines, cap 5000 lines and the newest 256 KiB.
  - Main-namespace reads: `namespace=<control-plane namespace>` on `/pods`, `/pods/{name}` and `/kube-events` is accepted only for callers with `*` on the local cluster (FR-012). Logs of the API and operator keep coming from the existing admin route `/admin/system-logs/{api|operator}?tailLines`. Remote clusters never expose their control-plane namespace through these routes.
- **Rationale:** smallest set that covers every MCP tool; the dashboard can adopt them later.

### R5. MCP server per-user mode

- **Decision:** new flags on `serve`: `--panel-url` (or `GAMEPLANE_PANEL_URL`) and the token from `GAMEPLANE_MCP_TOKEN` (env only, never a flag, so it does not land in process lists or shell history). With a panel URL, `serve` builds a `panel.Client` (HTTP, GET only, TLS verified, optional `--panel-ca` file) instead of the `kube.Client`. Tool handlers take a `Reader` interface with the existing six read methods plus cluster selection; both clients implement it. Tools gain an optional `cluster` argument; without it, the panel client lists `/clusters` and fans out, labelling each item with its cluster and reporting unreachable ones (FR-022). Each `serve` process sends a random `X-Gameplane-MCP-Session` header so the API can audit one session start per process (FR-015).
- **Rationale:** keeps stdio-only transport (FR-031); the handler boundary invariant extends to the new client (its exported methods are reads, and its only HTTP method constant is GET, checked by a tripwire test); runs on the user's own machine with the existing image (`docker run -i … serve`).
- **Alternatives:** an HTTP MCP transport hosted by the API (new network surface; rejected by FR-031).

### R6. Break-glass mode (OD-3)

- **Decision:** `serve` without `--panel-url` keeps today's in-cluster behaviour, limited to the namespaces in `GAMEPLANE_MCP_NAMESPACES` (comma-separated, rendered by the chart from the release namespace + `gameplane.operatorGameNamespaces`). "All namespaces" loops over that list; a named namespace outside it returns "namespace not in scope" before any Kubernetes call (FR-007). With the variable unset (standalone kubeconfig runs), behaviour is unchanged and the README says so.
- **Chart:** replace the ClusterRole with (a) a ClusterRole `<release>-mcp-server-catalog-read` for gametemplates, modules, modulesources only, (b) a Role `<release>-mcp-server-read` in the release namespace for pods, events, pods/log, and (c) the same Role plus gameservers, backups, backupschedules, restores in each managed game namespace, each with a RoleBinding. The old ClusterRole and ClusterRoleBinding names are not rendered any more, so Helm deletes them on upgrade (FR-006).
- **Rationale:** Kubernetes RBAC is the authoritative layer (constitution IX: primitives first); the in-process check gives clear errors.

### R7. Dashboard

- **Decision:** a new personal settings page `/settings/mcp-tokens` ("MCP tokens"), linked next to Theme settings in the user menu: list (name, created, expires, last used, Revoke), "Create token" dialog (name, expiry), one-time reveal with copy button and a ready-to-paste client config snippet. Admins see and can revoke any user's tokens from that user's row in Users & RBAC. Designed in `design.fig` first.
- **Rationale:** tokens are personal; the v1.1 "Service accounts" tab is for machine identities and stays separate.

### R8. Testing

- API: unit tests for token parse/lookup/guard; `handlers/mcp_tokens_test.go` and `handlers/pods_test.go` (fake clientset) for the token routes and new reads (namespace scoping, owner fallback, admin-only main namespace, remote cluster deny). MCP repo: `panel.Client` tests against `httptest`, tripwire for GET-only, namespace allow-list tests. E2E (constitution I): new tests in the `api-rbac` bucket (token user sees only its namespace; revoked token refused) and `multicluster` bucket (remote grant). Chart: `helm template` checks in the existing chart test job that no ClusterRole grants pods/events/logs.
