# api — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** github.com/GameplanePanel/gameplane/api

## Purpose

The api module is the REST + WebSocket gateway for Gameplane, serving the web dashboard and external integrations. It exposes the Kubernetes game-server CRDs (GameServer, GameTemplate, Backup, BackupSchedule, Restore, Module, ModuleSource, Cluster) and operator-reconciled state through a type-safe HTTP surface with multi-cluster support, role-based access control, and audit logging.

## Responsibilities

- **REST gateway:** expose CRUD operations on game resources (servers, templates, backups, schedules, destinations, clusters) and administrative surfaces (users, roles, config, audit, notifications, mod registries)
- **WebSocket bridge:** streaming console (RCON/PTY), pod logs, system logs, and real-time events (SSE) — multiplexed per namespace and cluster
- **Authentication:** local argon2id + OIDC via `coreos/go-oidc/v3`; session-based with CSRF; per-IP + per-user rate limiting on login endpoints
- **Authorization:** three built-in roles (admin/operator/viewer) plus custom roles; granular per-GameServer owner/collaborator fallback; cluster and namespace dimensions on permissions
- **Audit:** structured audit log (database + external sinks: webhook, S3, syslog bridge, stdout) with hash-chain integrity; per-user and per-IP tracking
- **Notifications:** watch CRD status transitions (server health, backup outcomes) and dispatch to admin-configured sinks (Discord, Slack, SMTP, webhook)
- **Module registries:** pluggable providers (CurseForge, Modrinth, Spigot, Hangar, Nexus, Steam, Thunderstore, Factorio, GitHub, UMod); live lookup + caching
- **Multi-cluster dispatch:** `?cluster=` selector routes requests to remote clusters via registered kubeconfigs; home cluster is `local`
- **Unified reads:** `/fleet/*` aggregates only the caller's authorized resources across registered clusters, with explicit target identities, target permissions, partial failures and bounded work; it does not schedule or mutate resources.
- **Telemetry:** daily usage reports POSTed to the resolved destination (operator hard-off, bundled receiver, custom endpoint, or the project's default receiver). Two admin-consented tiers: basic (`version`, `servers`, `templates`) and extended (random install ID, environment categories, game and feature counts, signing key, send time; reports are signed). New installs share both tiers by default once the first-login notice has been shown to an admin; nothing is sent before then. The schedule and consent source live in `telemetry_state` (migration 015), so restarts never send early and replicas share one schedule. When the destination offers proof-of-work challenges (`GET /v1/challenge`), the reporter solves one per request (a challenge above 26 bits is refused)

## Non-goals / boundaries

The API is a **UX layer only** — it reads and writes CRDs but does **not** contain business logic that belongs in the operator-reconciler:
- Do NOT implement "when GameServer is created, also create a default Backup" as an API-side action; the operator owns that as a reconciler side-effect
- Do NOT compute derived state in the handler (e.g., "server is healthy if all containers are running"); the operator computes status and the API reads it
- Do NOT add custom storage to supplement CRDs; operator-authoritative means every mutation goes through K8s objects and the operator reconciles the outcome

## Directory & package layout

```
api/
├── cmd/
│   ├── main.go           # entry point; subcommands: serve (default) + bootstrap-admin
│   ├── bootstrap.go      # bootstrap-admin implementation
│   └── bootstrap_test.go
├── internal/
│   ├── handlers/         # REST + WebSocket route handlers (lifecycle, users, modules, destinations, config, audit, events, resources, etc.)
│   ├── auth/             # authentication (local argon2id, OIDC, sessions, rate limiting)
│   ├── rbac/             # authorization (role catalog, middleware, permission -> rule mapping)
│   ├── db/               # database driver (sqlite/postgres), schema migrations, query layer
│   ├── kube/             # Kubernetes client wrapper, registry (multi-cluster), server/template helpers
│   ├── audit/            # audit logger (database, webhook sink, S3 sink)
│   ├── notify/           # notification delivery (Discord/Slack/SMTP/webhook watch -> dispatch)
│   ├── ws/               # WebSocket (console RCON/PTY, pod logs, agent client)
│   ├── registry/         # mod registry providers (CurseForge, Modrinth, Spigot, Hangar, Nexus, Steam, Thunderstore, Factorio, GitHub, UMod)
│   ├── steam/            # Steam Workshop ID resolver: cache + singleflight-deduped upstream lookups
│   ├── scope/            # namespace + cluster resolution from request context
│   ├── telemetry/        # optional anonymous usage metrics collection
│   ├── httperr/          # error -> HTTP status classification (safe message for client, full error logged)
│   └── [test files]
└── go.mod, go.sum
```

**Key packages and responsibilities:**

- **handlers:** 23+ route groups (Audit, AuthProviderSecrets, Capture, Cluster, ClusterActions, Clusters, Config, Destinations, Events, Lifecycle, ModIDs, ModSources, Modules, ModUpdates, Notifications, Ownership, PodEvents, Registry, RegistrySecrets, Resources, Roles, SystemLogs, Users, WebSocket Mount)
- **auth:** SessionStore (CSRF + expiry), Local (argon2id password check), OIDC (provider registry + claim mapping), Registry (auth provider discovery per request)
- **rbac:** Middleware (namespace/cluster-scoped permission check + owner/collaborator fallback), rule table (method/path -> permission), catalog (permission definitions)
- **db:** driver-selectable (modernc.org/sqlite or pgx/v5 via postgres build tag), migrations (001-012 per dialect in `migrations/sqlite/` and `migrations/postgres/`; 013+ shared and portable in `migrations/common/`), Store (query interface); queries use `?` placeholders, rebound to `$n` by the Postgres connection (`db.Rebind`); migration 015 (`migrations/common/015_telemetry_state.sql`) adds `telemetry_state` (singleton row: consent source, install ID, notice shown time, report schedule `next_due_at`/`last_attempt_at`/`last_success_at`, `last_outcome`, `consecutive_failures`, extended-fallback fields `ext_unsupported_until`/`ext_unsupported_endpoint`, `signing_secret`, `last_id_rotation_at`; seeded in Go by `Store.Migrate` from the existing `telemetry` config value) and `telemetry_notice_acks` (one row per admin who dismissed the notice: `user_id`, `acked_at`, `action`; rows are removed in Go when the user is deleted)
- **kube:** Client (K8s API wrapper), Registry (per-cluster clients from Cluster CRDs), watch (cluster-config sync)
- **audit:** Auditor (insert to DB + distribute to sinks), webhook sink (POST JSON to URL), S3 sink (object storage), hash-chain (detect tampering)
- **notify:** Notifier (watch GameServer/Backup/Restore status, format + deliver to sinks), sinks (Discord, Slack, SMTP, webhook)
- **ws:** Mount (WebSocket router), agent-client (JSON agent reads), transport (shared HTTP/WebSocket agent connection), actions (RCON/PTY execution), attach (SPDY proxy), podlogs (live pod logs)
- **registry:** provider types (each implements Search, Details, Manifest, Download), Set (versioned provider pool with key fallback)
- **scope:** ResolveNamespace (extract from path or default), ResolveCluster (validate `?cluster=` against registry)
- **httperr:** classify error type to safe HTTP status + message; preserve full error server-side
- **telemetry:** destination resolution (`ResolveDestination`), consent, report collection and preview (`BuildReport`), the scheduled reporter (Ed25519 signing, proof-of-work solving, install-ID rotation on `409 id_claimed`)

## External interface / contracts

### Entry point: api/cmd/main.go

Two subcommands:

1. **`serve` (default)** — starts the HTTP server
   - **Core flags:** `--addr`, `--metrics-addr`, `--db-driver`, `--db-dsn`, `--log-level`
   - **OIDC flags** (install-time, Helm-seeded): `--oidc-issuer`, `--oidc-client-id`, `--oidc-client-secret`, `--oidc-redirect-url`, `--oidc-display-name` (login button label, no hostname — pre-auth surface), `--oidc-groups-claim` (configurable claim name, defaults to "groups"), `--oidc-default-role` (default for unmapped users: "", "viewer", "operator", "admin", or "deny"), `--oidc-role-mapping-admin` (comma-separated group names for admin role), `--oidc-role-mapping-operator`, `--oidc-role-mapping-viewer`. All have `GAMEPLANE_OIDC_*` env fallbacks (preferred over flags for credentials).
   - **Storage class (report-only):** `--game-data-storage-class` — echoed in `GET /admin/config`'s `installTimeSettings.gameDataStorageClass`, read-only, unaffected by overrides.
   - **Other flags:** `--audit-*`, `--agent-*`, `--namespace`, `--cluster-ops`, `--cluster-external-address` (node-routable API server address used in the join command and downloaded kubeconfig instead of the in-cluster ClusterIP; empty = in-cluster address), `--update-channel`, `--curseforge-api-key`, `--telemetry-endpoint` (empty = the project's default receiver), `--telemetry-disabled` (hard off, whatever the admin toggles say), `--telemetry-interval` (default 24h, minimum 1m), `--official-module-source` (name of the official ModuleSource, so telemetry can tell official modules from custom ones; empty = every module counts as custom), `--capture-enabled`, `--capture-default-max-duration` (only these two are CLI flags; default/max retention and default max size are `GAMEPLANE_CAPTURE_*`-env-only, no flag)
   - Env overrides via GAMEPLANE_* vars (credentials come from env only, never flags); telemetry also reads env-only `GAMEPLANE_TELEMETRY_AUTH` (ingest token sent verbatim as the Authorization header) and `GAMEPLANE_TELEMETRY_BUNDLED` (set only by the chart when it wires the bundled receiver)
   - Initialize: database + migrations, K8s client, auth (local + OIDC with Helm-seeded provider synthesis), audit (sinks), notifier, cluster watch, telemetry, session GC, capture config
   - Routes all mounted at startup; chi router with security middleware (secure headers, body limit, audit, session auth, RBAC, rate limiting); MountCapture wires capture endpoints

2. **`bootstrap-admin`** — seed or reset the initial admin user
   - Flags: `--db-driver`, `--db-dsn`, `--username`, `--password`, `--password-stdin`, `--email`, `--display-name`, `--force`, `--enable-local-login`
   - Runs schema migrations like the serve path; password hashed with argon2id
   - Break-glass: `--enable-local-login` alone re-enables local auth in the config row (for OIDC-lockout recovery); every other key of the row (other providers, `helmOverride`) is written back unchanged
   - `--force` on an existing user resets the password, promotes to admin, and deletes all of that user's sessions (same eviction as the dashboard password reset)

### REST surface (domain-level)

The HTTP server listens on `:8000` (configurable) with these route groups. Prometheus metrics are not on this listener: a separate metrics listener (`--metrics-addr`, env `GAMEPLANE_METRICS_ADDR`, default `:9090`, empty disables it; `cmd/metrics.go`) routes only `GET /metrics`, and the chart's ServiceMonitor scrapes it. `--metrics-addr` equal to `--addr` is rejected at startup.

**Public (pre-auth):**
- `/auth/providers` — GET: list enabled login methods (no version/host/count, login privacy)
- `/auth/login` — POST: argon2id auth + session creation (rate-limited per IP + user)
- `/auth/logout` — POST: session deletion
- `/auth/oidc/{provider}/start` — GET: IdP authorization flow start
- `/auth/oidc/{provider}/callback` — GET: IdP token exchange (rate-limited)
- `/auth/oidc/start` (legacy) — GET: single helm-provider start
- `/auth/oidc/callback` (legacy) — GET: single helm-provider callback
- `/healthz` — GET: liveness probe

**Protected (authenticated + RBAC):**
- All six `/fleet/*` reads share a per-authenticated-user token bucket: 60 requests/minute sustained, burst 10. Exhausted requests receive 429 with `Retry-After: 1` before cluster discovery or resource listing. Sessions and routes share the account's budget; users behind the same IP do not. This per-process limit bounds request frequency without removing owner/collaborator scans; multiple API replicas have independent budgets.
- `/namespaces` — GET: namespaces the caller may read servers in (scope.AllowedNamespaces filtered by servers:read on the resolved `?cluster=`); lets the dashboard fan out `/servers?namespace=` across every namespace it can see instead of only scope.Resolve's default (F-263)
- `/fleet/servers`, `/fleet/backups`, `/fleet/schedules`, `/fleet/restores` — GET, authenticated: unified reads across registered clients and persisted registrations, restricted to configured allowed namespaces. Optional exact `cluster` and `namespace` filters narrow the backend read; omission means all eligible scopes, independent of the old selected-cluster default. Duplicate filters, wildcard IDs and invalid namespaces are rejected. A valid unknown or unauthorized cluster filter returns an empty filtered result without confirming registration existence.
  - Resource envelope: `{items:[{target:{cluster,namespace,name,uid},resource,permissions,access?}],partial,issues,totalReturned,scopes?}`. `resource` is the original Kubernetes object; server heartbeat freshness is projected as on existing reads. `target.uid` distinguishes replacement objects in row/cache identity; it does not add mutation preconditions to existing CRUD. `permissions` is the sorted catalogue of namespaced permissions actually held at that target, never a union of other clusters' grants.
  - Servers include objects granted by exact target `servers:read`, or the current object's owner/collaborator fallback. `access` exposes `canWrite` (explicit `servers:write`, required for full-object PUT), `canControl` (write or owner/collaborator), `canConsole` (console permission or owner/collaborator), `canDelete` (target admin wildcard or owner), `isOwner` and `isCollaborator`. These are UI projections; existing mutation/stream authorization remains authoritative.
  - Backups and restores require exact target `backups:read`; schedules require `schedules:read`. Server ownership alone does not grant those collection reads. Unauthorized scopes are never queried for these resources. Owner-only server discovery scans configured namespaces but returns only matching objects; unrelated registration names never appear in issue metadata.
  - `scopes` supplies deduplicated, sorted filter choices: explicitly read-granted scopes are included even when empty, unavailable or their items are truncated; ownership-only scopes appear only after an owned/collaborator object is found. Scope metadata is capped at 2,000 and remains separate from the item limit. An exact backend filter can retrieve a scope omitted from an unfiltered result.
- `/fleet/inventory` — GET, authenticated: same status envelope, with items `{cluster,name,view,stats}` and cluster-only `scopes`. Only an exact cluster-wide `cluster:read` grant (or wildcard cluster) qualifies; server namespace permissions and ownership never qualify. The route reads that cluster's nodes, bound PVs and optional metrics. Node usage retains `used` as absent when unavailable; clients must sum measured used/capacity pairs and show sample coverage rather than treat unmeasured nodes as idle. Storage remains provisioned PV capacity, not disk usage. A node/PV read failure omits that cluster's entire inventory item and reports partial coverage. Optional Kubernetes version metadata is omitted to keep reads cancellable. Namespace filters are invalid here.
- `/fleet/placements` — GET, authenticated: same status envelope, with items `{cluster,namespace,templates:[GameTemplate]}` and eligible `scopes`. Requires existing global `templates:read` plus explicit target `servers:write`; ownership alone cannot create a placement. Reads templates once per eligible cluster. Unavailable/empty catalogs offer no valid placement. This is permission/template eligibility, not a scheduler or capacity guarantee. Creation remains an explicit existing per-target request; the client must retain its chosen cluster/namespace for every follow-up and must not retry on another cluster.
- Fleet bounds and failures: `limit` is 1–2,000 (default 2,000); four workers, at most 128 work scopes, 200 objects per page, 2,000 scanned per resource scope, 10,000 scanned objects per request, a ten-second overall deadline and five-second scope deadlines. Metrics have a one-second sub-deadline. Inventory caps node/PV lists at 2,000 each; placements cap emitted template copies at 2,000. Discovery lists at most 2,000 registrations, while an exact filter directly looks up its registration. Limits/deadlines never become silent empty totals: HTTP 200 retains successful reads with `partial:true` and safe issues (`unavailable`, `forbidden`, `limit`). Issue `cluster:""` denotes a collection-wide limit/discovery problem; raw Kubernetes errors and credentials are never included. Hidden ownership-only scan failures may set `partial` without revealing a scope. Optional Cluster-CRD absence alone permits local discovery fallback; other discovery failures are reported as partial coverage.
- `/servers/{name}/access` — GET: existing selected-server read/owner/collaborator authorization, rechecked against the live object. Returns the same server access flags, explicit `target` and exact namespaced `permissions` for deep links; no global administration permissions are inferred. This avoids finding a server in a capped list just to decide which detail actions to show.
- `/servers/{name}/capabilities` — GET: the same selected-server read authorization and live-UID check. Returns `{target,capture:{enabled,files,state,defaultRetentionSeconds,maxRetentionSeconds,defaultMaxDurationSeconds,defaultMaxSizeBytes}}`. State is `ready`, `unsupported` or `unavailable`; these are transport capabilities, not permissions. Local settings come from CaptureConfig; remote settings come from the authenticated selected gateway and never from central capture defaults.
- `/servers` — GET, POST: list and create GameServer CRDs
- `/servers/{name}` — GET, PUT, DELETE: manage GameServer CRDs; cluster-dispatch via `?cluster=`; multiplexed console/files. Every response that carries a GameServer (`/servers`, `/servers/{name}` incl. the POST/PUT replies, `:clone`, `/users/me/servers`, `/fleet/servers`, `/events`) replaces password-type `spec.config` values with the redaction marker; see "GameServer config password redaction".
- `/servers/{name}/console` — WebSocket: RCON/exec; cluster-dispatch
- `/servers/{name}:start`, `:stop`, `:restart` — actions (operator-handled)
- Start/stop pin the live GameServer UID and validate any ownership-bound authorization, then merge-patch only `spec.suspend` with UID/resourceVersion preconditions. Kubernetes write conflicts receive bounded, context-aware retries only while the UID, spec and owner/collaborator grants remain unchanged; controller status updates may advance resourceVersion. Replacement/deletion returns 404, concurrent configuration or grant changes and exhausted retries retain 409, and non-conflict errors are not retried. Accepted requests still return 202; the operator owns the workload transition.
- `/servers/{name}:collaborators`, `:transfer` — GameServer owner/collaborator management
- `/servers/{name}:tunnel-credentials` — PUT/GET/DELETE: the server's tunnel credential Secret (`<server>-tunnel-auth`, owned by the GameServer). PUT stores the given provider's key (`frp`: `token`, `tailscale`: `authKey`, `playit`: `secretKey`) and removes stale keys, keeping keys no provider uses. It keeps the key of the provider still named in `spec.networking.tunnel.provider` even when that differs from the key just saved, since the dashboard saves credentials and switches that field in separate requests and a pod still running the active provider must keep being able to read its credential until the switch completes; that key is cleared on the next PUT once the spec provider matches. GET reports the key names, never the values, and is deterministic: the GameServer's `spec.networking.tunnel.provider` wins, then the fixed order frp, tailscale, playit. `PUT /servers/{name}` (direct spec provider switch) also participates in credential cleanup: the handler prunes every other provider's key from `<name>-tunnel-auth` immediately after the successful write that changes `spec.networking.tunnel.provider` (skipping a missing or not-API-owned Secret; a prune failure is logged, not returned), so the superseded provider's credential is removed before the tunnel pod picks up the new provider. A later credential PUT is only the retry path if that prune failed. The operator further protects against stale credentials by mounting only the active provider's key via items projection (optional:true), ensuring that even if a key was not pruned, it is never exposed to the tunnel pod. Removing the tunnel (no provider) is not a switch and leaves the stored credential in place; DELETE removes it explicitly.
- `/servers/{name}/files/*` — file browser, upload, download (proxied to agent); cluster-dispatch
- `/servers/{name}:capture-enable`, `:capture-disable` — sidecar lifecycle actions (fully implemented; see "Network capture endpoints" below)
- `/servers/{name}:capture-start` — POST: start a network packet capture (creates NetworkCapture CR, transitions to Pending)
- `/servers/{name}:capture-stop` — POST: request a stop of an active capture (sets the `gameplane.local/stop-requested` annotation; the operator completes the capture, see "Network capture stop flow")
- `/servers/{name}:captures` — GET: list all NetworkCaptures (active and historical) for a server, excluding Expired captures
- `/servers/{name}:capture` — GET: fetch a single capture's metadata and status; query param `id={captureId}`; 404 if not found or Expired
- `/servers/{name}:capture-file` — GET: download completed PCAPNG file from the capture sidecar; query param `id={captureId}` (proxied to sidecar over mTLS; 409 if still running)
- `/templates` — GET, POST: list and create GameTemplate CRDs
- `/templates/{name}` — GET, PUT, DELETE: manage GameTemplate CRDs (cluster-scoped)
- `/backups` — GET, POST: list and create Backup CRDs
- `/backups/{name}` — GET, PUT, DELETE: manage Backup CRDs (namespaced, cluster-dispatch)
- `/schedules` — GET, POST: list and create BackupSchedule CRDs
- `/schedules/{name}` — GET, PUT, DELETE: manage BackupSchedule CRDs (namespaced, cluster-dispatch)
- `/restores` — GET, POST: list and create Restore CRDs
- `/restores/{name}` — GET, PUT, DELETE: manage Restore CRDs (namespaced, cluster-dispatch)
- `/backup-destinations` — GET, POST: list and create restic repo Secrets
- `/backup-destinations/{name}` — GET, PUT, DELETE: manage restic repo Secrets (namespaced, cluster-dispatch)
- `/modules` — GET, POST: list and install Module CRDs
- `/modules/{name}` — GET, PATCH, DELETE: manage Module CRDs (cluster-scoped)
- `/modules/{name}:uninstall` — action
- `/modules/sources` — GET, POST: list and create ModuleSource CRDs
- `/modules/sources/{name}` — GET, PUT, DELETE: manage ModuleSource CRDs (cluster-scoped)
- `/modules/sources/{name}/upload`, `/modules/sources/{name}/upload/{module}` — bundle upload/delete
- `/modules/catalog` — GET: merged catalog across sources, with installation state
- `/modules/builder/{archetypes,scaffold,validate,preview,export}` — module builder workflow
- `/registry/{provider}/search`, `/{id}` — live mod registry queries (CurseForge, Modrinth, Spigot, etc.)
- `/mod-updates/{name}` — GET: available updates for a mod
- `/mod-ids/{name}` — PATCH: ID-managed mods (ARK CurseForge IDs, Project Zomboid MOD_IDs, Steam Workshop lists)
- `/cluster`, `/cluster/info`, `/cluster/stats` — GET: selected-cluster version, nodes, storage and optional usage. Require cluster-wide `cluster:read` on the selected cluster (or wildcard cluster); namespace grants cannot read node inventory. Remote requests use only that registry client's nodes, PVs, metrics and version, never home-cluster inventory. A registered cluster whose client is unavailable returns 503; an authorized unknown selector returns 400. Kubernetes denials retain their status with generic messages. Metrics-server absence remains optional and omits usage rather than reporting zero. Remote info returns the registration ID as its name, `clusterOps: false`, no local update channel, and the central API's `gameplaneVersion`. Node pod counts are populated from a cluster-wide Pending+Running pod list (paginated in 500-pod chunks with server-side terminal-pod filtering); counts are omitted entirely (not zero) when the list fails. Per-cluster counts are cached for ~15 seconds.
- `/cluster/nodes:join`, `/cluster/kubeconfig` — POST: credential-minting ops (admin only, `--cluster-ops` flag gated; 501 when disabled or a remote cluster is selected, before credential operations)
- `/clusters` — multi-cluster: list remote Cluster CRDs; create/delete cluster registrations. POST labels the kubeconfig Secret `gameplane.local/cluster-kubeconfig=true` and `gameplane.local/managed-by=gameplane-api`. DELETE removes the cluster's client from the registry at once, and deletes the referenced Secret only when it is the one POST generates for that cluster (cluster-<name>-kubeconfig) and carries `gameplane.local/cluster-kubeconfig=true` (Secrets created before managed-by labelling included); any other Secret, including one named for a different cluster, is left in place
- `/events` — SSE: real-time K8s events (multiplexed per namespace + cluster). The route needs `servers:read`; the stream then carries only the kinds the caller may read in the resolved cluster and namespace, each gated by the permission its GET route needs (`rbac.ReadPermission`): servers → `servers:read`, templates → `templates:read`, backups and restores → `backups:read`, schedules → `schedules:read`. Tests: `TestEvents_StreamsOnlyReadableKinds` (`handlers/events_scope_test.go`); e2e `TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions` (bucket `operator`). Each watched kind is first listed with `limit=1` and then watched from the returned resourceVersion, so a new connection only sees changes made after it opened; it no longer receives a synthetic ADDED event for every existing object. If that list fails, the watch falls back to an empty resourceVersion. Test: `TestEvents_WatchesFromListResourceVersion` (`handlers/events_scope_test.go`).
- `/pod-events` — SSE: pod-level events
- `/users/me` — GET: own profile (embeds `preferences`, see below)
- `/users/me/servers` — GET: own GameServers (owner/collaborator)
- `/users/me/preferences` — GET/PUT: own theme/styling preferences (feature 016)
- `/users/me/preferences/reset` — POST: reset own theme preferences to defaults (feature 016)
- `/users` — GET, POST: list and create users
- `/users/{id}` — PATCH, DELETE: manage users (admin only). DELETE runs `db.Store.DeleteUser`: one transaction deletes the user's `oidc_links`, `user_preferences`, `sessions`, `api_tokens` and role bindings, revokes the share links the user created (sets `revoked_at`), then deletes the `users` row. It does not rely on FK cascades (off on SQLite). An SSO subject whose user was deleted is provisioned as a new user on its next login
- **User list, lookup and create (`POST /users`) responses** include a `createdAt` field: `db.NormalizeTimestamp` passes RFC3339 values through unchanged, converts the legacy "YYYY-MM-DD HH:MM:SS" format from older migrations to RFC3339 UTC, and passes through unrecognized stored values unchanged.
- `/users/{id}/bindings` — GET/POST supplemental role assignments; DELETE `/users/{id}/bindings/{role}/{namespace}?cluster={id}` removes an exact assignment. Namespace `*` remains protected for the local primary role. An explicit registered remote cluster may receive a supplemental `*` namespace binding only when every role permission is `cluster:read` or a catalogued namespaced permission; wildcard/global administration permissions are rejected. Cluster `*` is never accepted for supplemental grants. Binding changes revoke the target user's sessions, and permissions are resolved from bindings on every request.
- **Share links are bound to the GameServer incarnation they were minted for, by UID.** `POST /servers/{name}:shares` stores the live GameServer's `metadata.uid` in `share_links.server_uid` (`db.Store.CreateShareLinkForServer`; `CreateShareLink` leaves it empty). Both public handlers (`GET /shares/{token}`, `POST /shares/{token}/start`) answer the uniform 404 when the link has a `server_uid` and the live GameServer's UID differs (the server was deleted and recreated under the same name, by any means, including within the same second; the UID replaces the timestamp check for these links). Legacy links (`server_uid` empty, minted before migration 013) keep the previous check: 404 when the live server's `metadata.creationTimestamp` is later than the link's `created_at`. An empty UID is never a match by itself; there is no backfill and no revocation of legacy links. `DELETE /servers/{name}` additionally revokes (sets `revoked_at`) that server's links via `db.Store.RevokeShareLinksForServer`
- `/roles` — GET catalog and custom roles; POST/PATCH/DELETE custom roles. A PATCH whose permission list drops `users:manage` from a role that grants it is refused (400) when that role is the caller's own primary role, or when every user who can manage users holds that role — the same lockout guards `PATCH /users/{id}` applies to a role change. Tests: `TestRoles_UpdateKeepsCallersOwnUserManagement`, `TestRoles_UpdateKeepsAtLeastOneUserManager`, `TestRoles_UpdateRemovesUserManagementWhenAnotherManagerRemains` (`handlers/roles_guard_test.go`); e2e `TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions` (bucket `operator`)
  - A role used by any remote-wide binding cannot gain global/control-plane permissions until those bindings are removed; safe permission removal still takes effect on the next request. Safe remote binding validation/insertion, role permission edits, and the role deletion in-use check share the existing user-management lock. This prevents a concurrent edit or delete/recreate from widening a supplemental remote grant. Existing externally provisioned global remote-wide bindings retain their prior removal protection. Tests: `TestRemoteBindings_*` (`handlers/remote_bindings_test.go`).
- `/admin/audit` — GET: audit log (searchable, hash-chain verifiable)
- `/admin/audit/export` — GET: streams the full matching audit trail as a download, `?format=csv` (default) or `?format=json`; optional filters `since`/`until` (RFC3339 timestamps, inclusive), `actor` (case-insensitive substring), `method` (`GET`/`POST`/`PUT`/`PATCH`/`DELETE`), and `status` (class `2xx`/`4xx`/`5xx`); an unrecognised `format`, `method`, or `status` value returns 400. CSV header, in column order: `id, ts, actor, method, path, target, status, ip, reason`. JSON is a top-level array of the same `audit.Event` objects `/admin/audit` returns.
- `/admin/config` — GET/PATCH: global settings (OIDC, notifications, telemetry, module upload limits, etc.)
- **instanceName field:** The `instanceName` field on `/admin/config` is optional; when omitted or empty, notifications fall back to "Gameplane" as the instance identifier.
- `/admin/telemetry` — GET (`config:read`): destination (`kind` default|custom|bundled|disabled|none, `host` only, null for disabled/none), `operatorDisabled`, stored `consent` (`basic`, `extended`, `source`), `installId` (null while extended is off), `preview` (the exact report the reporter would send now, built by `telemetry.BuildReport`; null when basic is off or nothing would be sent) and delivery `status` (`lastAttemptAt`, `lastSuccessAt`, `lastOutcome` never|ok|failed, `lastIdRotationAt`). The signing secret and failure details are never returned
- `/admin/telemetry/install-id` — POST (`config:manage`): replace the install ID, keep the signing secret and schedule; returns `{"installId"}`; 409 when extended is off or the operator disabled telemetry
- `/admin/telemetry/notice` — GET (`config:read`; returns `{"pending":false}` unless the caller also holds `config:manage`): whether the first-login notice is pending for the caller, with destination host and the exact basic/extended field lists; POST (`config:manage`) `{"action": "seen"|"keep"|"extended-off"|"all-off"}` records the caller's dismissal (`seen` only sets the notice-shown time and opens the report schedule, recording no ack), 204 on success, 409 when not pending (except `seen`) or the operator disabled telemetry. RBAC entries sit before the `/admin/*` fallback in `rbac.go`
- The telemetry consent itself is saved through `PUT /admin/config/telemetry` (`{"sendMetrics": bool, "extended": bool}`; `sendMetrics:false` forces `extended:false`; 409 when the operator disabled telemetry). The save runs in one transaction with the consent source, install ID and schedule updates
- `/admin/notifications` — PATCH config + test-send to sinks
- `/admin/auth` — PATCH identity-provider secrets
- `/admin/registries/{provider}/secret` — PATCH mod-registry API keys
- `/admin/system-logs` — GET: control-plane pod logs
- `/ws/servers/{name}/console` — WebSocket: RCON command execution (write-capable)
- `/ws/servers/{name}/console-pty` — WebSocket: PTY command execution (write-capable)
- `/ws/servers/{name}/logs` — WebSocket: game/agent log file stream (read-only)
- `/ws/servers/{name}/logs/pod` — WebSocket: pod stdout stream (read-only)
- **Log stream query parameters:** The logs WebSocket proxy (`/ws/servers/{name}/logs` and related) forwards only allow-listed query keys (`from`, `tail`) to the agent to prevent unauthorized parameter injection (e.g., `cluster`, namespace).

All cluster-dispatch routes accept `?cluster={name}` (validates against registered Cluster CRDs; default is `local`).

### WebSocket bridge

- **`/ws/servers/{name}/console` (GET upgrade)** — RCON to game pod via agent; write-capable
- **`/ws/servers/{name}/console-pty` (GET upgrade)** — PTY attach to the selected cluster's game pod; write-capable
- **`/ws/servers/{name}/logs` (GET upgrade)** — game/agent log file stream via agent; read-only
- **`/ws/servers/{name}/logs/pod` (GET upgrade)** — selected cluster's init/game stdout via the Kubernetes Pod log API; read-only
- All authenticate via session and cluster/namespace RBAC. Pod logs and PTY use
  the selected registry client's Kubernetes credentials and verify the
  GameServer → StatefulSet → Pod owner UID chain before opening streams.
- Agent routes use local mTLS and reject non-local selectors. Pod logs and PTY
  accept registered `?cluster=` targets without requiring agent mTLS.
- A browser cluster switch closes old streams and cancels retries/queued input.
  Pod log polling stops if a Pod is replaced; reconnects validate ownership again.
- Kubernetes log/attach calls are name-addressed, without UID preconditions;
  the owner checks do not make deletion/recreation atomic with stream startup.

### Agent transport boundary

The browser-facing agent proxy and internal `AgentClient` use the same transport
interface for HTTP operations and WebSocket connections. Handlers supply a server
name and namespace plus a registered agent path; they never supply a destination
URL. The direct transport validates the target, constructs its cluster-local DNS
address, uses the configured agent mTLS credentials, and refuses redirects. HTTP
headers retain the existing allowlist, so browser cookies, authorization and CSRF
material do not reach agents. Proxy body limits and JSON response limits remain at
the caller boundary.

Registered clusters may additionally configure `spec.agentGateway.url` and a
labeled `tlsSecretRef` in the central API namespace. Each remote request resolves
its Kubernetes client, GameServer UID and gateway mTLS credentials independently;
unknown/missing routes never fall back locally. HTTP/WebSocket agent operations,
RCON module actions and internal mod-update reads use the versioned gateway
protocol. Stdin actions use the selected Kubernetes client with workload ownership
preflight. Existing installations retain their direct local adapter.

See [remote agent access](../docs/multicluster-agent-gateway.md) for registration,
trust assumptions, rotation behavior and surfaces outside this protocol.

### Network capture endpoints

**Mounting & configuration:**
- `MountCapture(r chi.Router, reg *kube.Registry, auditor *audit.Auditor, cfg CaptureConfig, agentCABundle, agentClientCert, agentClientKey string)` — wires capture endpoints on a chi.Router with cluster dispatch and authenticated local sidecar or remote gateway transport
- `type CaptureConfig struct { FeatureEnabled bool; GatewayNamespace string; DefaultRetentionSeconds, MaxRetentionSeconds int64; DefaultMaxDurationSecs int; DefaultMaxSizeBytes int64 }` — local capture settings plus the central namespace for remote gateway credential lookup; passed from `cmd/main.go`
- Registered in api/cmd/main.go's `run()` with CaptureConfig fields bound from: `--capture-enabled` and `--capture-default-max-duration` (CLI flags), plus `GAMEPLANE_CAPTURE_DEFAULT_RETENTION`, `GAMEPLANE_CAPTURE_MAX_RETENTION`, and `GAMEPLANE_CAPTURE_DEFAULT_MAX_SIZE` (env-only, no corresponding flag)

**Implemented endpoints (cluster-dispatch via `?cluster=`, all require `captures:manage` RBAC permission, all authenticate via session, audit all writes synchronously before response):**

- **POST `/servers/{name}:capture-start`** — Create a NetworkCapture CR and transition to Pending; request body: `{filter?: string, maxDurationSeconds: int, maxSizeBytes: int64, ttlSecondsAfterFinished?: int64}`; response: `{captureId, phase, serverName, filter, maxDurationSeconds, maxSizeBytes, ttlSecondsAfterFinished, createdAt, startedAt?, completedAt?, bytesWritten, packetsWritten}` (HTTP 202 Accepted)
  - The selected Kubernetes identity needs `create` on `networkcaptures` and `update` on `networkcaptures/status` for the initial Pending phase. A status-write denial can follow a successful CR create; callers must inspect existing captures instead of blindly repeating start.
  - Verifies server exists and `spec.capture.enabled = true`; returns 400 if capture not enabled on server
  - Validates pcap-filter expression before CRD creation (FR-003; character whitelist + length check, no full BPF compile); returns 400 on invalid filter (e.g., control chars, >1024 chars)
  - Enforces maximum one Pending/Running capture per server (rejects with 409 Conflict if one exists); checks both `status.capture.activeCapture` and scans all NetworkCaptures as a guard against eventual consistency lag
  - Validates duration (1..3600s) and size (1..cluster-max bytes); returns 400 if out of range
  - Clamps TTL to cluster maximum at request time; returns 400 if exceeds max (cluster maximum is 604800s / 7 days by default, storage-limitation-informed, not a legal requirement)
  - Creates NetworkCapture CR with ownerReference to GameServer (cascade delete on server deletion)
  - Audit: `WriteSync()` before response body is sent (FR-006); reason field records "server_not_found", "capture_not_enabled", "invalid_filter", "invalid_duration", "invalid_size", "capture_in_progress", "ttl_exceeded", "create_failed", or "" on success

- **POST `/servers/{name}:capture-stop`** — Request that a Pending/Running NetworkCapture be stopped; request body: `{captureId: string}`; response: `{captureId, phase, serverName, filter, createdAt, startedAt?, completedAt?, stoppingReason, bytesWritten, packetsWritten}` (HTTP 200 OK)
  - Returns 400 if `captureId` is missing or empty in request body
  - Returns 404 if capture not found or doesn't belong to this server
  - Returns 409 if capture is not in Pending or Running phase (already Completed/Failed/Expired)
  - **Network capture stop flow (F-259; maintainer decision 2026-09-25):** sets only the `gameplane.local/stop-requested` annotation (RFC3339 UTC time of the first request) via `kube.StopNetworkCapture`; it never writes status. The operator's NetworkCapture reconciler stops the sidecar (closing the PCAPNG) and only then sets `phase=Completed`, `completionTime`, `message="stopped by user request"` and `SidecarStopped=True` (see `operator/specs.md`, "Network capture stop flow"). The response therefore normally reports the pre-stop phase (Pending/Running); clients poll `:captures`/`:capture` until Completed before downloading.
  - Idempotent: a repeat stop while the capture is still Pending/Running (operator not yet caught up) is a 200 no-op that keeps the original annotation value; once Completed it is a 409 as above.
  - `:capture-disable` requests the stop of every Pending/Running capture the same way before clearing `spec.capture.enabled`.
  - `:capture-file` gates on `phase=Completed` only; since the API no longer writes Completed, Completed means the sidecar has stopped and the file is closed. (PR #449's interim download-side polling for `SidecarStopped` was removed in favour of this.)
  - Audit: `WriteSync()` before response; reason field records "missing_id", "not_found", "not_running", "stop_failed", or "" on success

- **GET `/servers/{name}:captures`** — List all NetworkCaptures for a server (active and historical); response: `{captures: [...], total: int, limit: 100, offset: 0}`
  - Filters to captures whose `spec.serverRef.name` matches the server
  - Excludes Expired captures (phase == Expired)
  - Includes Pending/Running/Completed/Failed; client can filter by phase
  - Each item includes metadata, phase, timestamps, packet/byte counts, and computed `expiresAt` field (when the capture will auto-delete if TTL is set)
  - Audit: `WriteSync()` before response (non-GET auditing, rare but required for capture list due to FR-006 gap on GET operations)

- **GET `/servers/{name}:capture`** — Fetch a single capture's metadata, status, and counts; query param `id={captureId}`; response: capture object with full details (HTTP 200)
  - Returns 400 if `id` is missing or empty
  - Returns 404 if capture not found, doesn't belong to this server, or phase == Expired
  - Response includes phase, timestamps (createdAt/startedAt/completedAt), filter, limits, byte/packet counts, and computed `expiresAt`
  - Audit: `WriteSync()` before response; reason "not_found", "expired", or ""

- **GET `/servers/{name}:capture-file`** — Download completed PCAPNG file from sidecar; query param `id={captureId}` (HTTP 200 on success)
  - Returns 400 if `id` is missing or empty
  - Returns 404 if capture not found or doesn't belong to this server
  - Returns 404 if capture phase == Expired (TTL window elapsed)
  - Returns 409 for any phase other than Completed (Pending, Running, or Failed — Expired is handled above as 404, not here)
  - Proxies from sidecar's `https://<gs>-agent.<ns>.svc.cluster.local:9091/captures/{id}/file` over mTLS
  - Remotely, the central API resolves the selected server and capture UIDs and calls the registered gateway's versioned capture-file route. The gateway independently validates live ownership, phase and TTL, then reaches the selected cluster's fixed sidecar Service on 9091. The sidecar validates persisted server/capture identity before serving bytes. No fallback to local data or an unbound legacy remote file is permitted.
  - **CRITICAL (FR-006):** Audit `WriteSync()` with status code BEFORE streaming starts (on both success and error paths); if audit write fails, returns 500 and stops download entirely (audit failure fails the operation)
  - Sets response headers: `Content-Type: application/vnd.tcpdump.pcap`, `Content-Disposition: attachment; filename="capture-{id}.pcapng"`
  - Streams file without buffering via `io.Copy(responseWriter, sidecarResponse.Body)` so large captures don't accumulate in memory
  - On sidecar error (non-2xx), classifies via `writeUpstreamError` (timeout→504, other transport error→502); error message is safe generic text to client, full error logged server-side
  - Audit reason field: "not_found", "not_running", "expired", "invalid_host", "download_failed", or "" on success; a second audit row (with "download_failed") is written post-stream if sidecar returned non-2xx (to correct the initial optimistic row)

- **DELETE `/servers/{name}:capture`** — Remove a completed or failed capture. Remote deletion confirms sidecar file cleanup before deleting the NetworkCapture with its UID precondition; gateway failure retains the resource for retry. A stale or replaced capture cannot delete another capture's bytes.

- **Error responses:** All errors are plain text via `httperr.WriteCode()`, no JSON envelope. Status codes include 400 (validation), 404 (not found or stale identity), 409 (conflict/wrong state), 501 (capture feature or required gateway capability unavailable), 502/504 (gateway transport failure), 503 (sidecar unavailable), and 500 (internal error).

**Fully implemented endpoints (all routing registered, RBAC gated, handlers complete):**

- **POST `/servers/{name}:capture-enable`** — Enable capture sidecar injection on a GameServer (sets `spec.capture.enabled = true`, triggers live injection)
  - Enable and start use the selected cluster's feature flag. For a remote cluster, authenticated gateway capabilities supply that flag plus retention/default duration/default size settings; an incompatible or unreachable gateway fails closed before mutation.
  - Patches GameServer spec directly; operator watches and injects sidecar as ephemeral container live into running pod (rule 10: operator is authoritative)
  - Gated by `captures:manage` permission
  - Audit: synchronous write before response with reason "feature_disabled", "server_not_found", "terminating", "patch_failed", or "" on success

- **POST `/servers/{name}:capture-disable`** — Disable capture on a GameServer (sets `spec.capture.enabled = false`)
  - First requests a stop of every Pending/Running capture (the `gameplane.local/stop-requested` annotation, see "Network capture stop flow" under `:capture-stop`), then patches GameServer spec directly; operator stops any running capture and rejects new ones, but the ephemeral container remains in place until the next pod recreation (Kubernetes design constraint: ephemeral containers cannot be removed without pod recreation)
  - **ASYMMETRY (US2 central design point):** Disabling stops accepting new capture-start requests and stops any running capture but the container lingers until the pod is next recreated (e.g., on node drain, replica restart, or manual delete)
  - Gated by `captures:manage` permission
  - Audit: synchronous write before response with reason "feature_disabled", "server_not_found", "terminating", "patch_failed", or "" on success

### OIDC role mapping and Helm-seeded provider (install-time configuration)

**Helm-seeded provider synthesis & coexistence:**
- **Startup:** API synthesizes a read-only "helm" provider when `--oidc-issuer` is set (Helm-seeded OIDC configuration exists). This synthetic provider coexists with:
  - Dashboard-managed OIDC providers (stored in the "auth" config row, can be created/edited/deleted via API)
  - Local login (always available unless explicitly disabled)
  - Legacy fallback paths for backward compat (legacy provider mirrors Helm config as identity)
- **Provenance:** The "helm" provider's groupsClaim, defaultRole, and roleMappings come from CLI flags (`--oidc-groups-claim`, `--oidc-default-role`, `--oidc-role-mapping-*`) only. It is not a database row; it is synthesized at runtime from flags and the optional `helmOverride.roleMappings` overlay (see below).
- **Per-login re-evaluation:** Role mappings are read at login time from the current "auth" config row, so override changes take effect immediately on the next login without restart or Helm upgrade.

**Group-claim extraction & role assignment:**
- **Claim name:** Defaults to "groups" when `--oidc-groups-claim` is empty or unset. Otherwise uses the configured name. Extraction honors the configured (or default) name on every login (not cached).
- **First-match precedence:** Roles are assigned in strict precedence order: admin → operator → viewer. A user whose IdP groups match any group in the admin mapping gets the admin role, even if they also match operator/viewer groups. If no tier matches, the policy's DefaultRole applies ("", "viewer", "operator", "admin", or "deny" to refuse login).
- **Per-login re-evaluation:** Role assignment is computed fresh on every successful OIDC login, so a user's role changes immediately if their IdP groups change (or if the group-claim name in config changes).
- **Two safety guards:**
  1. Role assignment only applies when mappings are explicitly configured (when overrides exist or when Helm seeded them). No automatic role assignment from bare group names without explicit mapping.
  2. Demotion guard: A user who is the **only user able to manage users** (sole admin, or sole admin-equivalent) cannot be demoted or removed from the admin role by the login-time role assignment flow. This prevents accidental lockout: if a user is currently the only admin and a role remapping would remove their admin status, the remapping is skipped (logged as warn), leaving them as admin. The guard applies only on login re-evaluation; the override API (PATCH /admin/config/auth) does not enforce it (an explicit admin action).
- **Re-evaluation trigger:** re-evaluation runs whenever the effective policy the role was computed from has role mappings — for the Helm provider that is the Helm seed merged with `helmOverride`, so an override alone (no Helm-seeded mappings) is enough.
- **Audit (FR-014):** every applied role assignment (first login, or a re-evaluation that changes the role) is audited with reason `oidc role assigned: provider=<name> matched=<group> from=<old> to=<new>`, for the Helm provider (`provider=helm`) and for every dashboard-managed provider (`provider=<provider name>`). A demotion skipped by the guard is logged, not audited.

**The helmOverride overlay:**
- **Storage:** Lives in the "auth" config row as `helmOverride.roleMappings.{admin, operator, viewer}`. No separate table, no migration beyond the existing config table. The entire overlay is optional.
- **Shape:** Each role key (admin/operator/viewer) is independently optional:
  - **Absent:** use the Helm-seeded value (from `--oidc-role-mapping-*` flags)
  - **Non-nil array (including `[]`):** override with this array; `[]` means "nobody maps to this role"
  - Provenance is **derived from key presence** — no explicit `source` field exists. A key's presence alone means DB-overridden; absence means Helm-seeded.
- **Merge semantics:** Non-nil replaces the Helm seed in full (not a merge); `[]` is distinct from absent.
- **Reset route:** `DELETE /admin/config/auth/role-mappings/{role}` removes a single role's key, restoring the Helm-seeded value for that role.
- **API:** Writes go through `PUT /admin/config` with the full auth config (including the helmOverride overlay). Reads via `GET /admin/config` return both the current override state (helmOverride) and the original Helm seed (installTimeSettings.oidcHelmProvider) as separate fields, never merged.
- **Audit:** Set/change operations are audited with reason "oidc role mapping override set: role=<role> groups=<groups>". Reset operations are audited with reason "oidc role mapping override reset: role=<role>".

**installTimeSettings response object:**
- **Returned by:** `GET /admin/config` (requires `config:read` permission)
- **Content:** Read-only snapshot of Helm-seeded values captured at startup:
  - `gameDataStorageClass` (string, the value of `--game-data-storage-class` flag)
  - `oidcHelmProvider` (object with `groupsClaim`, `defaultRole`, `roleMappings.{admin, operator, viewer}` — the original Helm seed, never merged with overrides)
- **Immutability:** Never changes after startup, unaffected by any override (helmOverride.roleMappings changes do not appear here). Allows clients to always see what was Helm-configured vs. what was overridden.
- **Presence:** Absent entirely when no install-time values are reportable (empty storage class, no Helm OIDC).

### User theme preferences (feature 016)

Per-user dashboard styling (theme preset, appearance mode, custom colors, custom CSS overlay) stored server-side so it follows the account across devices. Contract: `specs/done_016-user-theme-customization/contracts/user-preferences-api.md`.

**Storage (migration `011_user_theme_preferences.sql`, api/internal/db/migrations/):**

- `user_preferences` — 1:1 with `users`:
  - `user_id` INTEGER PRIMARY KEY, REFERENCES `users(id)` ON DELETE CASCADE (FK enforced only on Postgres; modernc-sqlite runs with FK OFF, matching the convention noted in 003_roles.sql)
  - `theme_type` TEXT NOT NULL DEFAULT 'preset' — base mode: `"preset"` | `"custom_colors"` (custom CSS is an independent overlay flag, not a base mode)
  - `preset_id` TEXT NOT NULL DEFAULT 'pink' — `"pink"` | `"legacy"`
  - `appearance_mode` TEXT NOT NULL DEFAULT 'system' — `"light"` | `"dark"` | `"system"`
  - `custom_accent`, `custom_surface` TEXT NULL — `#RRGGBB` hex colors
  - `custom_css_enabled` INTEGER NOT NULL DEFAULT 0 — overlay on/off flag
  - `custom_css` TEXT NULL — sanitized stylesheet (max 32 KiB, see FR-013 below)
  - `updated_at` TEXT NOT NULL (application-generated RFC3339 UTC in Go)
  - Index `idx_user_preferences_user` on `user_id`
- **Backfill (FR-003, SC-001):** every user existing at migration time is initialized with the legacy preset (`INSERT ... SELECT id, 'preset', 'legacy', 'system' FROM users`), preserving the pre-feature orange appearance on upgrade. Accounts created afterwards rely on column defaults plus the code-level `db.DefaultUserPreferences()` (pink preset, system appearance, overlay off) returned by `Store.GetPreferences` when no row exists.
- **Store layer (api/internal/db/preferences.go):** `GetPreferences` (returns `DefaultUserPreferences()` on no row), `UpsertPreferences` (full-row replace — the caller owns merge/retention semantics), `ResetPreferences` (the single operation that NULLs the custom columns). Enum validation and CSS sanitization live in the handlers, not the store.

**Endpoints (mounted in `MountUsers`, all require a valid session — any authenticated user manages their own styling; no RBAC permission beyond authentication):**

- **GET `/users/me/preferences`** — returns the effective preferences object `{themeType, presetId, appearanceMode, customColors, customCssEnabled, customCss, updatedAt}`; a user without a stored row gets the defaults (pink/system/nulls). 401 when unauthenticated.
- **PUT `/users/me/preferences`** — handler-level merge (FR-012). Required: `themeType` (enum), `presetId` (`"pink" | "legacy"`), `appearanceMode` (enum), `customCssEnabled` (bool). Optional: `customColors` (`accent`/`surface`, each validated against `^#([0-9a-fA-F]{6})$`), `customCss` (sanitized per FR-013). The handler reads the stored row and applies only the fields present in the body, so omitted custom fields keep their stored values — an ordinary update (preset switch, overlay toggle) can never null them. 400 names the offending field/rule; 401 unauthenticated; 403 CSRF mismatch.
- **POST `/users/me/preferences/reset`** — the **only** operation that deletes stored custom settings (FR-012): NULLs `custom_accent`/`custom_surface`/`custom_css`, sets `custom_css_enabled = 0` and `theme_type = 'preset'`. Optional body `{presetId?, appearanceMode?}` (each validated; omitted fields resolve to the user's current values). Returns the resulting preferences object.
- **GET `/users/me`** — embeds `preferences` in the profile payload (`userDTO.Preferences`) so the dashboard can apply the theme without a second round-trip on boot. Also embeds `permissionsByCluster` (cluster → namespace → sorted []string of permissions) so the frontend can determine cluster-specific access without triggering 403 errors on multi-cluster deployments. The existing `permissions` field (namespace → permissions, merged across clusters) is preserved for backward compatibility. Neither field is present on other user payloads.

**FR-013 custom CSS sanitization (authoritative enforcement gate, `sanitizeCustomCSS` in api/internal/handlers/users.go):**

A submitted stylesheet is rejected with 400 and a message naming the offending rule when it:
- exceeds 32,768 characters (32 KiB);
- contains an `@import` rule;
- contains an external `url()` reference (absolute `http://`/`https://` or protocol-relative `//host/...`, remote fonts included) — inline `data:` URIs are permitted;
- has unbalanced `{`/`}` braces;
- contains `<style`, `</style`, `<script`, or `</script` (DOM-escape defense against breaking out of the `<style>` element).

**FR-012 retention invariant:** `custom_accent`, `custom_surface`, and `custom_css` survive preset switches and overlay toggles; only the reset endpoint clears them. Client contract: ordinary updates omit optional custom fields rather than sending explicit nulls.

### Capture operation auditing

**Scope — which operations are audited** (per T010, specs/done_003-network-capture-sidecar/research.md):

- **REQUIRED (FR-006):** All capture write/delete operations are audited synchronously:
  - POST `:capture-enable` (enable) — recorded before response
  - POST `:capture-disable` (disable) — recorded before response
  - POST `:capture-start` (start) — recorded before response
  - POST `:capture-stop` (stop) — recorded before response
  - GET `:capture-file` (download) — recorded before response (despite being a GET, because FR-006 explicitly names "download" as a required audit event)
  - DELETE `:capture` (delete, future Phase 2 task) — recorded before response
  
- **RECOMMENDED (product decision, not FR-006-mandated):** GET `:captures` (list) audit is recommended but not required by FR-006

- **NOT REQUIRED:** GET `:capture` (get-status) audit is not required by FR-006 and no handler-side write is performed for this endpoint

**Audit implementation pattern** (api/internal/audit/audit.go and api/internal/handlers/capture.go):

- **Synchronous writes:** All capture handlers call `Auditor.WriteSync(ctx, method, path, target, reason, status)` directly before writing the HTTP response body to the client
- **Signature:** `WriteSync(ctx context.Context, method, path, target, reason string, status int) error` (api/internal/audit/audit.go:689)
- **Timing:** The audit row is inserted into the database **before WriteHeader is called**, so if the audit write fails, the handler returns HTTP 500 (internal error) without sending the response body — **a failed audit write fails the entire capture operation (FR-006)**
- **Error handling:** WriteSync returns an error if the database write fails; the handler must check the return value and bail if it's non-nil. This is enforced by a helper method `auditWriteOrFail` in capture.go, which returns false on write failure (the handler then returns 500 without proceeding)
- **One row per request:** after a successful WriteSync, `auditWriteOrFail` calls `audit.MarkRecorded(ctx)`, which makes the global `audit.Middleware` skip its generic row for that request, so each capture mutation is audited once (the explicit row also carries `reason` and the composite `target`). If WriteSync fails the flag stays unset and the middleware row is written as a fallback

**Audit reason field** (migration 007_audit_reason.sql and audit.go):

- **Column:** `audit_events.reason` (nullable TEXT, added by migration 007_audit_reason.sql, api/internal/db/migrations/)
- **Purpose:** Structured, machine-readable reason codes for operations that need fault reporting beyond HTTP status — e.g., capture start may fail with reasons "server_not_found", "capture_not_enabled", "invalid_filter", "invalid_duration", "invalid_size", "capture_in_progress", "ttl_exceeded", "create_failed", or "" on success
- **Hash chain compatibility** (api/internal/audit/audit.go:309-331):
  - The `reason` field **participates in the hash chain ONLY when non-empty** (api/internal/audit/audit.go computeHash function, lines 329-332)
  - Pre-migration rows (written before 007_audit_reason.sql shipped) have NULL/empty reason and were hashed without a reason field; their stored hash remains valid and is verified bit-for-bit
  - Post-migration rows with non-empty reason include it in their content hash; two rows differing only in reason hash differently
  - The Verify function (api/internal/audit/audit.go:455+) correctly reconstructs this at each row: pre-migration rows are hashed without reason (as originally computed), post-migration rows with non-empty reason include it
  - This preserves backward compatibility: the hash chain is never invalidated by the migration, and pre-existing audit rows remain verifiable

**Fan-out to external sinks:**

- WriteSync passes the reason field to all configured external sinks (webhook, S3, stdout) via the Event struct (api/internal/audit/audit.go:722-731), enabling external audit systems to capture structured failure reasons alongside the HTTP status code

**Captures:manage permission model** (api/internal/rbac/ and api/internal/db/migrations/):

- **Permission key:** `captures:manage` (defined in api/internal/rbac/catalog.go:71, namespaced)
- **Label:** "Enable, start, stop, download, and delete packet captures"
- **Seeding:** Only the **admin role** has this permission, via the wildcard admin permission `*` (admin holds `perm: "*"` in 003_roles.sql:48, which covers all permissions including captures:manage)
- **Migration 008 (008_captures_rbac.sql):** This migration deliberately does NOT explicitly INSERT captures:manage into role_permissions for the admin role, because admin's permission set must remain exactly `['*']` as verified by the test TestRoles_ListIncludesBuiltins
- **Grantability:** captures:manage is a normal catalog permission that *could technically* be granted to custom roles via the roles API (it has no structural restriction like the `*` wildcard does). However, the default seeding keeps it admin-only; future phases may decide to allow operator or custom-role grantability (T012 in research.md left that as an open decision)
- **Ownership fallback limitation:** The owner/collaborator fallback in rbac.go (lines 111-127) is explicitly restricted to `servers:read`, `servers:write`, and `servers:console` permissions. Even a GameServer owner without the `captures:manage` permission will get HTTP 403 Forbidden on all capture endpoints

**RBAC rule-table ordering (critical security constraint)**:

- **Constraint:** All 8 capture permission checks in `api/internal/rbac/rbac.go` (lines 189-196) MUST precede the `servers:write` catch-all rule (line 201)
- **Why it matters:** Path matching is done on the first segment, stripped of any verb suffix ("servers"). So `/servers/{name}:capture-start` matches segment "servers" just like `/servers/{name}` (ordinary CRUD). An unordered insertion of capture rules after the `servers:write` catch-all (which the operator role holds via 003_roles.sql:48) would silently grant all 8 capture endpoints to the operator role, **breaking the security requirement that only admin has capture permissions (FR-005/SC-005) with no test failure, no log line, no observable error — just silent escalation**
- **What breaks if reordered:**
  - Any HTTP verb on any path matching `/servers/{name}:capture-*` would resolve to `servers:write` permission instead of `captures:manage`
  - The operator role holds `servers:write`, so operators would gain full capture access, undermining admin-only isolation
  - Capture audit entries would still flow through the audit middleware, but the RBAC denial that should have fired never happens
  - No CI test currently detects this regression
- **Regression test guards** (api/internal/handlers/capture_test.go and rbac_test.go):
  - Test TestCapture_OnlyAdminCanAccess (or similar) verifies that operator role gets 403 on all 8 capture endpoints
  - Test TestRBACRuleOrdering (or similar) validates that capture rules appear before servers:write in the rule table (structural assertion on rule slice ordering)
  - These tests prevent accidental reordering in future edits

**Security & invariants:**

- **Asymmetry clarity:** Enable is live and restart-free. Disable is similarly live but not truly "off"  — the container remains in the pod until recreation. Spec.md documents this precisely; dashboa will surface it to users (e.g., "Capture disabled but sidecar remains until pod restart").
- **RBAC ordering (critical):** All 8 capture permission checks in `api/internal/rbac/rbac.go` (lines 189-196 in rbac.go) MUST precede the `servers:write` catch-all. Path matching is done on segment "servers" (via chi's `{name}` segment stripping), so a /servers/{name}:verb verb routes as "servers" segment. An unordered insertion after servers:write would silently grant capture access to the operator role (which holds servers:write), breaking the security requirement that only admin has capture permissions (FR-005/SC-005). CI does not currently detect this regression; future edits must preserve order or add a structural test.
- **Concurrent capture limit (API + operator):** API enforces at creation time (rejects 409); operator enforces again at reconciliation time. Two independent gates prevent race conditions and eventual-consistency lag.
- **Sidecar authentication:** mTLS only; both client cert+key and server CA are passed from cmd/main.go to MountCapture and built into an http.Client. No sidecar auth tokens in URLs (SSRF risk), no secrets echoed in logs.

### RBAC roles (three built-in + custom)

**Built-in roles:**
- **admin:** wildcard permission `*`; full access to all resources and config
- **operator:** read/write servers, backups, schedules, templates; read modules, destinations, cluster, roles (modules install/upgrade/uninstall requires `modules:manage`, seeded only to admin — see `api/internal/db/migrations/sqlite/003_roles.sql`)
- **viewer:** read-only across servers, backups, schedules, templates, modules, destinations, cluster, roles

**Permissions (granular, per resource and action):**
```
servers:read, servers:write, servers:console (namespaced)
backups:read, backups:write, backups:restore (namespaced)
schedules:read, schedules:write (namespaced)
templates:read, templates:write (cluster-scoped)
modules:read, modules:manage (cluster-scoped)
destinations:read, destinations:manage (namespaced)
captures:manage (namespaced)
cluster:read (selected-cluster inventory), cluster:manage (control-plane)
users:read, users:manage (cluster-scoped)
roles:read, roles:manage (cluster-scoped)
audit:read, config:read, config:manage (cluster-scoped)
```

**Network capture permissions:**
- `captures:manage` — enables, starts, stops, downloads, and (future) deletes packet captures (namespaced, scoped to GameServer within namespace)
  - Required for all 7 implemented capture endpoints (`:capture-enable`, `:capture-disable`, `:capture-start`, `:capture-stop`, `:captures`, `:capture`, `:capture-file`)
  - Future Phase 2 task: DELETE `:capture` endpoint for explicit capture deletion (8th rule, also gated by captures:manage)
  - Seeded to **admin role only** (migration 008 and admin's `*` wildcard; no explicit custom-role or operator-role grant)
  - RBAC rule table ordering: All 8 capture rules (including the future DELETE rule) MUST precede the `servers:write` catch-all to prevent silent operator-role escalation (see invariant 12)

**Binding dimensions:**
- Per-user + per-role (many-to-many)
- Per-namespace (namespaced permissions; `*` = cluster-wide)
- Per-cluster (multi-cluster; `*` = all clusters, but typically scoped to specific cluster)

**Owner/collaborator fallback:**
- Generic GameServer POST/PUT reserve annotations under `gameplane.local` and its subdomains for authorized API operations and controllers, except the editable `gameplane.local/description` and unused legacy `gameplane.local/grace-period-seconds` hints: POST strips protected supplied values before stamping the creator; PUT restores their live values, including omitted keys. This protects ownership, wipe/restart/wake requests, acknowledgements and lifecycle guards without restricting ordinary settings or other annotation domains. Dedicated lifecycle/ownership routes retain their existing authorization and confirmation checks.
- When namespace permission is denied and request targets a GameServer, check if caller is owner or collaborator
- Owner-only operations (`:transfer`, `:collaborators`, `:wipe-data`, bare DELETE) deny collaborators
- Owner-only operations need the server's owner or an admin (a role holding `*` in the resolved cluster and namespace), whatever namespace permission the caller holds: the namespace `servers:write` permission alone does not grant them, and a server with no owner annotation (for example one created with kubectl or GitOps) is admin-only for them. `rbac.Middleware` enforces the rule for all four; the `:transfer`, `:collaborators` and `:wipe-data` handlers repeat the check (`requireOwnerOrAdmin` in `handlers/ownership.go`). Those three handlers pin their merge patch to the `resourceVersion` the check read (`patchServerAsOwner`); on a 409 Conflict they re-read the server and repeat the check (a caller who lost ownership meanwhile gets 403), retrying up to three times before returning 409. Other server writes keep their namespace permission rules.
  - Tests: `TestMiddleware_OwnerOnlyOperationsNeedOwnerOrAdmin`, `TestMiddleware_ServerWithoutOwnerIsAdminOnlyForOwnerOnlyOperations`, `TestMiddleware_OtherServerWritesUnchangedForServersWriteHolders`, `TestMiddleware_OwnerOnlyOperationOnMissingServer`, `TestMiddleware_OwnerOnlyOperationWithoutFetcherFailsClosed` (`rbac/owner_only_test.go`); `TestOwnership_TransferRequiresOwnerOrAdmin`, `TestOwnership_SetCollaboratorsRequiresOwnerOrAdmin`, `TestLifecycle_WipeDataRequiresOwnerOrAdmin` (`handlers/owner_only_ops_test.go`); e2e `TestAPI_OwnerOnlyServerOperations_RequireOwnerOrAdmin` (bucket `api-mods`)
- Fetch GameServer from `?cluster=` (cluster-gated in middleware)

## Key invariants

1. **Operator-authoritative:** every mutating request goes through K8s API (Create/Patch/Delete on CRDs); API waits for operator to reconcile status
2. **Every mutating request audited:** audit middleware logs actor, method, path, target, status, IP to database + external sinks
3. **Three-role baseline RBAC:** admin/operator/viewer roles reproduce historical permission matrix exactly
4. **Multi-dimensional RBAC:** namespace + cluster + owner/collaborator dimensions; cluster gating prevents cross-cluster privilege escalation
5. **Append-only migrations:** database schema mutations are irreversible (migrations 001-014); no down-migrations
6. **Login rate limiting:** `/auth/login` is per-IP (burst 10, 5/min, `LoginLimiter`) plus a per-username throttle layered on top (burst 6, 3/min, `LoginUserLimiter` in `auth/local.go`); the OIDC callback routes (`/auth/oidc/{provider}/callback`, legacy `/auth/oidc/callback`) are per-IP only (burst 10, 10/min via `OIDCCallbackLimiter`), no per-user dimension
7. **Audit hash-chain:** each audit_events row includes hash of previous row (prev_hash) + its own content hash (hash); detects DB-level UPDATE/DELETE tampering
8. **Audit pagination is bounded:** The `Auditor.Page(ctx, limit)` method clamps the untrusted `limit` parameter to a maximum of 500 entries (`MaxAuditPageSize`). Clamping occurs at both the API handler layer (api/internal/handlers/audit.go line 25) and the store layer (api/internal/audit/audit.go lines 820–822) so untrusted input is bounded at the earliest opportunity and again at use time. The allocated slice is always created with capacity within the bound (`make([]Event, 0, limit)` after clamping), guaranteeing that no untrusted client input can cause unbounded memory allocation regardless of how the limit value flows through the system.
9. **Session CSRF protection:** CSRF token paired with session token; validated on state-changing requests. The CSRF cookie is deliberately **not** `HttpOnly` — the SPA reads it via JS and echoes it back as the `X-Gameplane-CSRF` header (double-submit pattern); making it `HttpOnly` would break the protection it's providing. The session cookie itself stays `HttpOnly`. Cookie *clearing* (logout) always sends `HttpOnly` regardless of the original cookie, since a delete carries no value for a script to read and the browser matches the clear on Name/Domain/Path alone. This is why the `.golangci.yml` gosec G124 exclusion is scoped narrowly to `api/internal/auth/sessions.go`.
10. **Cluster-watch logging is privacy-preserving:** The `kube.Watch` goroutine logs cluster synchronization events using only the cluster name and error details, never secret material. While cluster credentials are held in memory and used to construct clients, logging never includes kubeconfig bytes, API tokens, or other sensitive data. The taint-source variable naming (`kubeconfigField` for field names like "kubeconfig") is distinct from the actual credential bytes flowing through the client, preserving clarity for future readers and static analysis.
11. **Secure error handling:** internal errors (DB failures, K8s API errors) logged in full; safe generic messages sent to clients
12. **Cluster dispatch validation:** `?cluster=` matched against registered Cluster CRDs via registry; unknown cluster is a 400
13. **WebSocket/HTTP proxy path validation:** `api/internal/ws/dialer.go` takes the namespace and pod name from the request path before building the agent's upstream URL. Both are validated as DNS-1123 labels (`isDNS1123Label`) and rejected with a 400 before any URL is constructed — gosec's taint analysis doesn't model a custom validator as a sanitizer, hence the scoped G704 exclusion on that file.
14. **Capture rule-table ordering:** All 8 capture permission checks (POST `:capture-enable`, `:capture-disable`, `:capture-start`, `:capture-stop`; GET `:captures`, `:capture`, `:capture-file`; DELETE `:capture`) **MUST precede** the `servers:write` catch-all rule in `api/internal/rbac/rbac.go` lines 189-196 before line 211. Because all `/servers/{name}:verb` paths match the segment "servers" (chi's `{name}` segment strips the verb suffix), an unordered insertion after `servers:write` (which the operator role holds) would **silently grant all 8 capture endpoints to the operator role**, breaking the security requirement that only admin has capture permissions (FR-005/SC-005). This regression is a structural bug CI does not currently catch — moving the capture rules after servers:write is a one-line security break. Any future RBAC edits must preserve this order; consider a structural test to prevent silent reordering.
15. **Helm-seeded OIDC role mappings:** The "helm" provider is synthesized from CLI flags and the optional helmOverride overlay. No database migration carries the Helm seed; it lives only in flags. The helmOverride.roleMappings lives on the existing "auth" config row, per-role independently optional (key presence = overridden, absence = use Helm seed). Re-evaluated at login time so changes take effect without restart. Demotion guard prevents removing the last user able to manage users.
16. **Home-cluster-only routes:** Handlers built on the API's home-cluster client, and routes that reach agent or sidecar Services, serve the home cluster only and answer 501 not implemented (no cross-cluster agent yet) for a `?cluster=` naming any other registered cluster, so a permission granted on one cluster is never applied to another. The route list and its tests are under Authorization → Home-cluster-only routes.
17. **GameServer config passwords never leave the API in clear:** password-type `spec.config` values are replaced by a fixed marker in every response that carries a GameServer, through the single helper in `api/internal/handlers/config_redact.go`; a PUT that echoes the marker keeps the stored value. See Security considerations → GameServer config password redaction.

## Dependencies

**Internal (same workspace via go.work):**
- `github.com/GameplanePanel/gameplane/netguard` — dial-time address guard for outbound HTTP: mod-registry browse/search and the Steam name resolver use `IsPublic`; notification sinks use `IsAllowed`
- `github.com/GameplanePanel/gameplane/gameaction` — console-injection guard + command-template renderer

**External (go.mod):**
- `github.com/go-chi/chi/v5` v5.1.0 — HTTP router, middleware
- `github.com/coder/websocket` v1.8.12 — WebSocket upgrade + streaming
- `github.com/coreos/go-oidc/v3` v3.11.0 — OIDC provider discovery + token validation
- `github.com/go-jose/go-jose/v4` v4.0.2 — OIDC JWT parsing (transitive via go-oidc)
- `github.com/jackc/pgx/v5` v5.11.0 — PostgreSQL driver (build tag: postgres, experimental)
- `github.com/minio/minio-go/v7` v7.2.1 — S3-compatible client (audit sink)
- `github.com/prometheus/client_golang` v1.23.2 — Prometheus metrics
- `modernc.org/sqlite` v1.34.1 — SQLite driver (production, tested)
- `golang.org/x/crypto` v0.53.0 — argon2id password hashing
- `golang.org/x/oauth2` v0.30.0 — OAuth2 token exchange (OIDC flow)
- `k8s.io/api` v0.35.0, `k8s.io/apimachinery` v0.35.0, `k8s.io/client-go` v0.35.0 — Kubernetes API
- `sigs.k8s.io/controller-runtime` v0.23.3 — K8s client (dynamic unstructured access)

Verify from `/api/go.mod`.

## Data & persistence

### Database driver selection

- **Production (default):** `modernc.org/sqlite` — file-based, WAL mode, tested
- **Experimental:** PostgreSQL via `jackc/pgx/v5` — compile with `-tags=postgres`. Works end to end; `api/internal/db`'s tests run against PostgreSQL in the `api (postgres)` CI job, but there is no Postgres e2e/upgrade coverage yet
- Driver selected at startup via `--db-driver` (sqlite|postgres) + `--db-dsn`
- SQLite DSNs automatically receive a `busy_timeout(5000)` pragma if one is not already specified, allowing multiple processes (e.g., the API and bootstrap-admin) sharing the same file to wait for locks instead of failing immediately
- Migrations run automatically on startup (`store.Migrate(ctx)`): the driver's legacy set (`migrations/sqlite/` or `migrations/postgres/`, 001-012, same filenames and resulting schema) then the shared set (`migrations/common/`, 013+, one portable file per migration), in version order; a version present in both sets is a startup error. Versions are recorded in `schema_migrations` by bare filename, so SQLite installs see the same versions as before the split
- Runtime SQL is written once with `?` placeholders; the Postgres connector rewrites them to `$n` (`db.Rebind`). Timestamps that used SQLite's `datetime('now')` are generated in Go (`db.NowTimestamp()`, same `YYYY-MM-DD HH:MM:SS` UTC text) and bound as parameters; inserted ids come from `RETURNING id` (pgx has no `LastInsertId`)
- Postgres legacy migrations declare the text columns the API sorts or range-compares (`roles.name`, role-binding scope columns, `audit_events.ts`, `sessions.expires_at`, `share_links.created_at`/`expires_at`) `COLLATE "C"` so ordering matches SQLite's byte-wise collation

### Schema (migrations 001-014)

**001_init.sql:**
- `users` — username (unique), email, display_name, pw_hash (argon2id), role (legacy, now via role_bindings), created_at, updated_at
- `sessions` — token (PK; since 014 holds the hex SHA-256 digest of the session cookie value, never the value), user_id (FK), csrf_token, expires_at
- `oidc_links` — (issuer, subject) -> user_id (many-to-one); email claim
- `audit_events` — ts, actor, method, path, target, status, ip
- `api_tokens` — token (PK), user_id, name, last_used

**002_config.sql:**
- `config` — key (PK), value, updated_at

**003_roles.sql:** (custom roles + granular permissions)
- `roles` — name (PK), description, builtin flag
- `role_permissions` — (role_name, permission) junction
- `user_role_bindings` — (user_id, role_name, namespace) junction; seeded with pre-existing user roles on '*' namespace

**004_cluster_rbac.sql:** (multi-cluster support)
- Alters `user_role_bindings` primary key to (user_id, role_name, cluster, namespace); backfill to 'local' cluster

**005_audit_chain.sql:** (hash-chain integrity)
- Adds `prev_hash` and `hash` columns to `audit_events` for tamper detection

**006_share_links.sql:** (unauthenticated server access tokens)
- Creates `share_links` table: signed, revocable tokens for unauthenticated access to a single GameServer's status and connection address, optionally with start capability
- Token never stored; only SHA-256 hash persisted and indexed for O(1) lookup
- Revocation (`DELETE /servers/{name}/shares/{id}`, `db.Store.RevokeShareLink`) matches the link's cluster, namespace, server name and id; no match returns `db.ErrShareLinkNotFound`, which the handler answers with 404
- Pre-existing; not part of the Phase 2 Foundational feature scope
- `created_by` has no foreign key on Postgres (the SQLite file's `ON DELETE CASCADE` never fires there), so a deleted user's links stay behind, revoked, on both drivers
- **revokedAt** field: The response to `POST /servers/{name}:shares` (create) and `GET /servers/{name}:shares` (list) includes a nullable `revokedAt` field: an RFC3339 string when the link was revoked, or JSON `null` while the link remains active.

**007_audit_reason.sql:** (Phase 2 Foundational: capture operation auditing)
- Adds nullable `reason TEXT` column to `audit_events`
- Used by synchronous audit writes (`Auditor.WriteSync`) to record machine-readable failure reasons for operations like network captures
- Existing rows retain NULL `reason`; the audit hash-chain boundary preserves backward compatibility (see below)

**008_captures_rbac.sql:** (Phase 2 Foundational: capture permissions)
- Seeds `captures:manage` permission to admin role via `INSERT INTO role_permissions(role_name, permission) VALUES ('admin', 'captures:manage')`
- Only admin role grants capture access in Phase 2 Foundational; future phases determine operator role grantability

**009_share_links_cluster.sql:** (multi-cluster share links)
- Adds `cluster TEXT NOT NULL DEFAULT 'local'` to `share_links`; binds a link to the cluster it was created on

**010_share_links_expiry_nullable.sql:** (configurable share link expiry, feature 017)
- Rebuilds `share_links` (create `_new`, copy, drop, rename — SQLite has no `ALTER COLUMN`) so `expires_at` is nullable; every existing non-null `expires_at` is copied verbatim, never recomputed
- NULL `expires_at` means "never expires"; `LookupShareLink`/`ListShareLinks` treat it as always-valid for expiry purposes (revocation is unaffected and still checked independently)
- Removes the previous fixed 90-day maximum lifetime (`MaxShareLinkExpiryDays`) from both the store (`CreateShareLink`) and the handler; the only remaining store-side check is that a non-nil `expiresAt` must be strictly in the future
- `POST /servers/{name}:shares` request body now carries exactly one of `expiresAt` (RFC3339 instant, client-computed) or `neverExpires: true`; a request with neither or both is rejected 400. The response's `expiresAt` is an RFC3339 string, or JSON `null` for a never-expiring link.
- `expiresIn` (a Go duration string) is **deprecated** and kept working for one release only: it is honored only when neither `expiresAt` nor `neverExpires` is present, and its 7-day default for an omitted/empty value applies only on this legacy path

**011_user_theme_preferences.sql:** (per-user theme customization, feature 016)
- Creates `user_preferences` (1:1 with `users`): base mode (`theme_type`), preset (`preset_id`), appearance mode, nullable custom colors (`custom_accent`/`custom_surface`), custom CSS overlay flag + sanitized text, `updated_at`; index `idx_user_preferences_user`
- Backfills every pre-existing user with the legacy preset (`preset_id = 'legacy'`, dark-preserving upgrade per FR-003); accounts created later default to pink via column defaults + `db.DefaultUserPreferences()`
- Retention rule (FR-012): the custom columns are nulled only by the reset endpoint, never by ordinary updates (see "User theme preferences" under External interface / contracts)

**012_account_removal_cleanup.sql:** (account removal cleanup)
- One-off pass that deletes `oidc_links`, `user_preferences`, `sessions`, `api_tokens` and `user_role_bindings` rows whose user no longer exists, and revokes (sets `revoked_at`, RFC3339 UTC) active `share_links` whose creator no longer exists. Clears rows left by user deletes made before `db.Store.DeleteUser` removed them explicitly; forward-only, so a rollback needs the pre-upgrade DB snapshot

**013_share_links_server_uid.sql:** (share link UID binding; first shared migration in `migrations/common/`)
- Adds `server_uid TEXT NOT NULL DEFAULT ''` to `share_links`; portable, runs unchanged on SQLite and PostgreSQL. Existing rows get `''` (legacy links, validated by creationTimestamp only); no backfill
- New links store the GameServer `metadata.uid`; public share handlers require the live UID to equal it when non-empty (see "Share links are bound to the GameServer incarnation")

**014_sessions_digest_reset.sql:** (session digest storage)
- `DELETE FROM sessions` once. Sessions switched to storing a SHA-256 digest of the cookie value (`auth.sessionDigest`); pre-existing rows hold raw values and are deliberately invalidated, so every user signs in again once after upgrade. Numbered 014 because 013 is used by another shared migration. The runner applies pending files in version (filename) order and skips recorded ones, so 013 and 014 pending together run 013 first; a 013 added after 014 was recorded on an earlier startup still runs on the next startup, after it. Forward-only: a rollback to a pre-digest binary also invalidates digest-era sessions.

Foreign keys are enforced only on Postgres (modernc-sqlite runs with FK OFF); the API layer is authoritative and deletes dependent rows itself, so the Postgres cascades never change the outcome. The one exception is `share_links.created_by`: the Postgres schema declares it as a plain column with no foreign key, so deleting a user keeps that user's share links (revoked) in the audit trail on both drivers, instead of cascading them away.

## Security considerations

### Authentication
- **Local:** argon2id (Argon2id13, m=64MiB, t=3, p=2) with per-user random salt; ~200ms per check; operations are bounded by a 1-slot weighted semaphore (`argonConcurrency`) that honours request context, so client disconnects abort pending argon2 operations and prevent stalling under concurrent login attempts. Each operation allocates a 64 MiB block that is garbage on return, so at startup `api/cmd/memlimit.go` also sets the Go soft memory limit to 80% of the cgroup memory limit (256Mi chart default -> ~204 MiB) unless `GOMEMLIMIT` is set, making the GC reclaim those blocks before a burst of concurrent failed logins can OOM-kill the container
- **OIDC:** `coreos/go-oidc/v3` discovery + `go-jose/v4` JWT validation; claims mapping (email, groups, roles)
- **Sessions:** cryptographically random token + paired CSRF token; only the SHA-256 digest (hex) of the token is persisted (the cookie carries the raw value, so a database copy cannot be replayed as a cookie; lookup, logout and cleanup hash the presented value first); DB-only persistence (no in-memory store), 12-hour TTL from creation, garbage-collected on an interval (`SessionStore.StartGC`)
- **CSRF cookie is JS-readable by design:** unlike the session cookie (`HttpOnly`), the CSRF cookie is set `HttpOnly: false` so the SPA can read its value and echo it back as `X-Gameplane-CSRF` on mutating requests — the standard double-submit pattern (see `docs/security.md`). Logout's cookie-clear always sends `HttpOnly: true` regardless, since a MaxAge<0 delete carries no value and the browser matches it on Name/Domain/Path alone.
- **Bootstrap:** `bootstrap-admin` subcommand hashes password same way as API
- **Client IP:** `auth.ClientIPFromTrustedProxies` (`internal/auth/clientip.go`) records the client IP that the rate limiters and audit key on. A TCP peer outside `--trusted-proxies` is the client and its `X-Forwarded-For` is ignored. Behind a trusted peer the header is read right to left up to the first address outside the list, or to the leftmost address when every hop is trusted; an entry that isn't an IP address ends the walk at the last address reached. An empty list makes the peer the client. Tests: `internal/auth/clientip_test.go`.
- **Login audit attribution:** a successful local login records the signed-in username as the audit actor (`SetActor` in `internal/auth/local.go`), so the `/auth/login` audit row names who signed in; a failed login stays anonymous. Tests: `TestLogin_Success_AttributesAuditActor`, `TestLogin_WrongPassword_LeavesAuditActorAnonymous` (`internal/auth/local_test.go`).

### Authorization
- **RBAC middleware:** intercepts all protected routes; namespace + cluster gating
- **Owner/collaborator fallback:** fallback only when RBAC denies AND GameServer is explicitly named; fail-closed on malformed paths
- **Cluster dispatch validation:** `?cluster=` against registry; unknown cluster is a 400 (malformed request, not 403 forbidden)
- **Inventory authorization and discovery:** inventory routes authorize the selected cluster before looking up its registration, so an unauthorized selection returns 403 without probing Kubernetes. GET `/clusters` accepts authenticated callers and filters persisted registrations: cluster-wide inventory readers and namespace server readers discover their own targets; existing user/cluster managers can discover registration metadata for administration. Each entry reports `canViewInventory` from the selected-cluster grant, independently of discovery permission. Detailed version/health text is omitted for discovery-only callers, and disconnected registrations remain visible with an unavailable status. No discovery path grants inventory or server permissions.
- **Selected-cluster mod operations:** production uses `MountRegistryWithRegistry` and `MountModIDsWithRegistry`. Registry providers/search/versions/modpack dependencies and GET/PUT `/servers/{name}/mods/ids` resolve the selected server and template, validating any owner/collaborator UID grant before provider access. POST `/servers/{name}/modpack` and mod-ID updates retain UID/resourceVersion and write only that cluster. Update conflicts receive bounded, context-aware retries for status changes on the same UID with unchanged spec and owner/collaborator grants; concurrent configuration or ownership edits retain HTTP 409. Provider engines and credentials remain centrally configured. Legacy bare-client mounts keep their remote refusal.
- **Selected-cluster capture files:** GET `/servers/{name}:capture-file` and file cleanup during DELETE `:capture` use the private gateway for remote targets. Both server and capture UIDs are checked at the gateway and sidecar. Remote deletion accepts only a 204 bound cleanup acknowledgement or a 410 confirming both identity and PCAP absence before UID-guarded record deletion; ambiguous errors retain the record for retry. Cleanup may be skipped only for Completed captures with current-generation SidecarStopped=True, reason never_started, and no recorded gameplane.local/capture-pod-uid annotation, after validating server ownership. Pending/Running captures still require stop first; Failed phase or missing completion time is not absence proof. Existing local transport and cleanup behavior remain supported. Capture permissions and synchronous audit requirements do not change. Missing/old gateways fail closed, with no local fallback.
- **Server capabilities:** GET `/servers/{name}/capabilities` requires the normal target-scoped server read permission and returns the exact target plus `capture.enabled`, `capture.files` and `capture.state`. Local enablement uses central configuration; remote enablement comes from the authenticated selected gateway. States are `ready`, `unsupported` or `unavailable`. Capabilities describe transport support and never grant capture permission.
- **Central administration:** cluster node-join/kubeconfig, users, provider credentials, module catalog and installation management remain central administrative operations. `TestHomeClientMounts_ServeHomeClusterOnly` and `TestHomeClientMounts_MatchMain` retain their local-client boundary checks. Remote routing, identity and isolation tests cover the registry-backed mounts separately.
- **Gateway-enabled agent routes:** the production mount enables selected-cluster RCON, game-file logs/downloads, files, players, status and agent-based mods through the optional gateway. RCON module actions use that gateway; stdin actions, Pod logs and PTY attach use the selected Kubernetes client. GET `/servers/{name}/mods/updates` uses `MountModUpdatesWithRegistry` so its template and agent reads target the same selected cluster. Consumers mounted without the remote resolver retain the legacy 501 guard; missing or invalid gateway configuration fails closed rather than using a local agent. Tests: `TestModUpdatesRoutesAgentAndTemplateToRemoteCluster`, `TestModUpdatesRemoteCannotUseLocalOnlyAgent` and `ws/gateway_client_test.go` cover remote routing and fail-closed behavior.

### GameServer config password redaction
- Every covered response omits `metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]`: this historical manifest can contain passwords absent from the current config or template. Omission applies even when current config is absent, empty, or malformed; stored Kubernetes metadata and other annotations remain unchanged.
`GameServer.spec.config` stores wizard values in clear, including those of `type: password` fields in the template's `configSchema`. Anyone holding `servers:read` (or the owner/collaborator fallback) could therefore read every password. The API now redacts them; implementation and tests are in `api/internal/handlers/config_redact.go` and `config_redact_test.go`.
- **Marker:** `__gameplane_redacted__`. On reads, each non-empty password-type value is replaced by the marker (an empty value stays empty so clients can show "not set"). The password-field set comes from the server's `GameTemplate.spec.configSchema` (read live and memoised only within one request, or within one SSE event, so a template edit applies to the very next response and event).
- **Covered paths (one helper):** GET/POST/PUT `/servers` and `/servers/{name}` (including the create and update replies), `POST /servers/{name}:clone` reply (the clone itself stores the source's values verbatim), `GET /users/me/servers`, `GET /fleet/servers`, and the `/events` SSE stream (each watch event is redacted on a copy). Share links expose only name/status/address/player count; capture, mod, tunnel-credential, notification and WebSocket paths never carry `spec.config`; the audit log and its sinks record actor/method/path/target/status/ip/reason only and never request bodies.
- **Fail closed:** when the template cannot be read (no `templateRef`, not found, API error, malformed `configSchema`) every non-empty config value is redacted.
- **Update rule (PUT `/servers/{name}`)**, applied before the write (markers: every key; the other rules: per password-type key, every key when failing closed): a value equal to the marker keeps the stored value for ANY key, whether or not the current template marks it a password and whatever the stored value's type (the marker is API-emitted, and a fail-closed read marks every key); if the key is absent from the stored object the marker is dropped, never stored; a different non-empty value replaces it; an explicit empty string clears it only when the field is optional, for a `required` field (or when failing closed, where optionality is unknown) the stored value is kept; a key omitted from the body is removed as in any PUT, except a `required` password field, which is restored. This is what makes the dashboard's whole-`spec` PUT (`mergeDraftOntoLatest`) safe. On POST, marker values are dropped.
- **Not covered:** Kubernetes API readers (kubectl, GitOps, the optional read-only `mcp-server`) see the stored object; protect it with cluster RBAC.

### Audit
- **Scope:** every mutating request (POST/PATCH/DELETE); reads excluded
- **Sinks:** database (table audit_events) + webhook (POST JSON) + S3 (object storage) + syslog bridge (HTTP->syslog relay) + stdout (structured logs)
- **Hash-chain:** prev_hash + hash computed at insert time; Verify tool detects tampering
- **Retention:** optional daily prune (--audit-retention-days)
- **Webhook sink context threading:** `WebhookSink.Start` runs the delivery worker until its context is cancelled; on cancellation it calls `drain`, which best-effort-ships whatever's still buffered within a short (2s) deadline so a wedged endpoint can't stall process exit. Both `drain` and the normal per-event `post` path detach from the worker's context via `context.WithoutCancel` before making the HTTP call — the `Start` select loop can still pick a buffered event right after cancellation, and a cancelled context would fail that delivery even though the event could still be shipped. Because `WithoutCancel` strips the deadline along with the cancellation, `post` re-applies the caller's own deadline (if any) onto the detached context, so `drain`'s 2s budget survives the trip through `post` instead of being silently discarded.

### Audit Reason field (Phase 2 Foundational)

- **Column:** `audit_events.reason` (nullable TEXT, added by migration `007_audit_reason.sql`)
- **Purpose:** Machine-readable failure reason for operations that need structured fault reporting beyond HTTP status codes (e.g., network capture start/stop operations)
- **Middleware (generic requests):** Always passes empty string `""` as reason, so pre-migration rows have NULL reason
- **Synchronous writes (handler-direct):** Routes that need immediate audit writes before sending the response call `Auditor.WriteSync(ctx, method, path, target, reason, status)` directly, providing the reason
  - Method signature: `WriteSync(ctx context.Context, method, path, target, reason string, status int) error`
  - Extracts actor from context (set by auth middleware)
  - Extracts client IP from context (set by the `auth.ClientIPFromTrustedProxies` middleware)
  - Generates RFC3339 timestamp
  - Returns error if DB write fails. Most synchronous callers treat this as **non-fatal** (log it, don't fail the request — e.g. `config.go`'s role-mapping override/reset audit). The network capture handlers are the deliberate exception (FR-006): they check the return value via `auditWriteOrFail` and bail with 500 without proceeding, so a failed audit write does fail those operations (see "Network capture endpoints" above and `WriteSync` at capture.go)
  - Fan-outs to external sinks (webhook, S3, stdout) with reason included
- **Hash-chain boundary (backward compatibility):** The `reason` field is included in the canonical hash **only when non-empty**. This preserves the hash computation of pre-migration rows (which have NULL/empty reason) bit-for-bit identical. A post-migration row with a non-empty reason hashes differently, providing integrity protection. Rows differing only in reason will hash differently; Verify still walks the chain correctly across both pre- and post-migration rows.

### Outbound safety
- **netguard dial guard:** mod-registry browse/search (`api/internal/registry`) shares one client built with `netguard.HTTPClient(…, netguard.IsPublic)`, so every registry fetch, redirects included, connects only to a globally routable address and never through a proxy from the pod environment. Notification sinks use the permissive `netguard.IsAllowed` policy, so self-hosted endpoints on private addresses stay reachable.
- **mTLS to agent:** client cert + key validate console operations
- **Agent proxy URL construction:** `api/internal/ws/dialer.go`'s ws and http proxy handlers build the upstream agent URL from the namespace and pod name taken off the request path. In both handlers the namespace and pod name are validated as DNS-1123 labels via `isDNS1123Label` first, and a request with an invalid namespace or pod name is rejected with `400 Bad Request` before any URL is built. Only after that validation do the two paths diverge in how they construct the URL: the HTTP proxy path assembles it with `url.URL`, while the WebSocket proxy path concatenates the already-validated host onto a fixed `wss://` scheme and the fixed agent path (`upstream := "wss://" + host + agentPath`). The load-bearing safety property — validation strictly precedes URL construction — holds for both paths regardless of which one then uses string concatenation.

### Upload limits
- **Module bundle upload** (`POST /modules/sources/{name}/upload`): the compressed body is capped at 900 KiB (413 above it). Extraction caps each member at 900 KiB, the archive at 256 distinct paths, the running total of extracted bytes at 4 MiB (`maxUploadExtractedBytes`, repeated member names included), and the whole decompressed tar stream (including headers and entries extraction skips) at 5 MiB (`maxUploadDecompressedBytes`, covering tar framing). Extraction stops at the first member that crosses a limit, and the request gets 400. The four kept bundle files must together stay under 900 KiB.

### Error handling
- **httperr package:** internal errors (K8s 404, DB constraint, FS path) mapped to safe HTTP status + generic message
- **500+ errors:** never echoed to client; full error logged at Error level server-side
- **4xx errors:** hand-crafted safe messages by handlers (e.g., "ref is required", already classified)

### Login privacy (rule 3)
- `/auth/providers` omits version, cluster name, server count, hostnames
- `/login` error is always "invalid credentials" (never "wrong password" vs "unknown user")
- No internal metrics visible pre-auth; Prometheus metrics are served only on the separate metrics listener, never on the public port (tests: `cmd/metrics_test.go`, e2e `TestHelmInstall_MetricsNotOnPublicPort`)

## Testing & coverage

### Test tiers

1. **Unit tests** (localhost, no K8s)
   - Mocked auth, DB (in-memory sqlite)
   - Package-level tests (`*_test.go` in each internal package)
   - Fast (<1 min total)

2. **Envtest integration** (K8s envtest assets 1.31)
   - Real K8s API server (in-memory)
   - Real database (file-backed sqlite)
   - Coverage merged with unit tests
   - Handlers + CRD reconciliation flows
   - ~2 min

3. **E2E (kind + Helm)**
   - Real kind cluster (1.31)
   - Real Helm chart installation
   - Real agents + console/file operations
   - Bucket sharding (api-auth, api-rbac, api-agent, ratelimit, bot, multicluster)
   - ~10-20 min total (parallel buckets on CI)

### Coverage gate

**Target:** 80% (api/.testcoverage.yml)

Excluded:
- `cmd/` (main.go + flag/signal wiring)
- `internal/db/db_postgres.go` (Postgres driver, build-tag gated; excluded from the coverage gate because coverage runs without `-tags postgres`. The `api (postgres)` CI job builds it and runs `internal/db`'s tests, including `db_postgres_test.go`, against a PostgreSQL service container)

Final 20% gap concentrated in:
- `ws/attach.go` handle() (SPDY exec proxy, exercised by e2e)
- `auth/oidc.go` last-mile error paths (id_token claim parse failures)
- `events.go` SSE pump (live kube-watch, also e2e)

## References

- **`docs/architecture.md`** — components, data flow, security boundaries, operator-authoritative rationale
- **`docs/security.md`** — detailed threat model, auth/RBAC/audit design, pre-auth privacy
- **`docs/notifications.md`** — notification sink formats + test delivery
- **`docs/oidc.md`** — OIDC provider setup, claim mapping, role inference
- **`docs/module-authoring.md`** — module registry bundle format, ModuleSource CRD
- **`CLAUDE.md`** rule 10 — "The operator is authoritative" principle (API is UX layer only)
- **`api/go.mod`** — dependency versions (source of truth for go.mod)
- **Makefile** — `make test-go`, `make cover`, `make lint-go`, `make images`; CI runs via GitHub Actions


## Drift Fixes
### Removed Endpoints
<!-- REMOVED: DELETE /module-sources — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: DELETE /roles — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /admin/cluster/{op} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /admin/system-logs — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /cluster/actions — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /login — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /mod-updates/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /module-sources — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /modules/{name}:uninstall — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /pod-events — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /registry/{provider}/search — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /servers/{name}/console — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /servers/{name}/files — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /servers/{name}:collaborators — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /servers/{name}:start — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /users/{id} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: GET /{id} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /admin/auth — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /admin/config — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /admin/notifications — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /admin/registries/{provider}/secret — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /mod-ids/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /roles — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PATCH /users/{id}/role-bindings — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /backup-destinations/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /backups/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /cluster — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /module-sources — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /modules/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /restores/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /schedules/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /servers/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /templates/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: POST /users/{id} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PUT /backup-destinations/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PUT /module-sources — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PUT /modules/{name} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->
<!-- REMOVED: PUT /users/{id} — removed in commit 1749bb6396d7d4d53f8fc57ad6214164eee299b8 Mon Sep 21 00:16:43 2026 +0200 -->


### Auto-Discovered Endpoints (Drift Detected)
### Removed Endpoints
<!-- REMOVED: /modules/sources — DELETE removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /roles — DELETE removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /admin/system-logs — GET removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /auth/oidc/callback — GET removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /auth/oidc/{provider}/callback — GET removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /mod-updates/{name} — GET removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /users/{id} — GET removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /admin/auth — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /admin/config — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /admin/notifications — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /admin/registries/{provider}/secret — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /backup-destinations/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /backups/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /mod-ids/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /modules/sources — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /restores/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /roles — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /schedules/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /servers/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /templates/{name} — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /users/{id}/role-bindings — PATCH removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /admin/notifications/sinks/{name}/test — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /auth/login — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /backup-destinations/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /backups/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /modules/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /restores/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /schedules/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /servers/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /shares/{token} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /templates/{name} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /users/{id} — POST removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /backup-destinations/{name} — PUT removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /modules/sources — PUT removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /modules/{name} — PUT removed in commit <sha> 2026-09-28 -->
<!-- REMOVED: /users/{id} — PUT removed in commit <sha> 2026-09-28 -->

## Optional private agent gateway

The `gateway` subcommand starts an mTLS-only listener without the application
database, browser sessions, or administrative API routes. It defaults to
`127.0.0.1:8443` and requires an explicit registered cluster ID, namespace
allowlist, dedicated central-client CA, and exact allowed central URI SAN.
The central API remains the user-authorization authority.

`gatewayprotocol` defines versioned target routes and the shared exact
method/path allowlist. `gateway` validates the addressed GameServer UID and
controller-owned agent Service, then forwards to the fixed cluster-local
port-8090 agent using `/v1/targets/{uid}`. Final agent UID enforcement prevents
a name-reuse race from reaching a replacement server. The gateway never falls
back to unversioned agent routes. Separate capture-file routes validate both
GameServer and NetworkCapture UIDs, ownership, completion and expiry before
using the fixed capture-sidecar port 9091 and its identity-bound route.

Authenticated GET `/v1/capabilities` advertises protocol `v1`, target UID support,
capture-file transport support and this site's capture feature switch. Central
capability reads use fresh registration/credential resolution, bounded response
size and timeout, and reject unsupported protocol/identity combinations.

Mounted gateway trust is reloaded on each TLS handshake and revalidated on
every request, including reused connections. Invalid/removed trust rejects
new requests; TLS session resumption is disabled. Agent credentials reload on
every operation. Streams, file/log/capture downloads, file/mod uploads and mod
installs have no fixed total-duration cap by default. `--max-request-duration`
(`gateway.maxRequestDuration` in Helm) accepts zero to disable the cap or any
positive duration to impose one; negative values are rejected. Central peer
certificate expiry and parent cancellation always terminate an operation.
Ordinary operations and Kubernetes identity lookups have 30-second timeouts.
Both HTTP hops waive the response-header timeout for long agent operations,
including mod install and upload processing before the first response byte.
Ordinary requests retain their header limit; dial and TLS handshake limits,
parent cancellation, certificate expiry and configured total lifetimes still apply.
Existing streams may remain active until certificate expiry, an explicitly
configured maximum lifetime, caller disconnect or gateway shutdown following
trust or permission removal. Reconnection performs fresh authorization.
No ambiguous mutation is automatically retried.

Kubernetes operations still use the central API's independently scoped remote
Kubernetes clients. Gateway loss does not inherently disable those clients.
The protocol does not provide storage replication, workload migration, or
central database availability. Deployment guidance is in
[`docs/gateway-install.md`](../docs/gateway-install.md).

## Standalone management mode

`--standalone` / `GAMEPLANE_STANDALONE=true` skips Kubernetes credential discovery and does not register `local`. Management secrets and remote registrations persist in the API database through narrow `kube.Client.Secrets`/`Clusters` interfaces; workloads still use explicit Kubernetes registry clients. Credential payloads use AES-GCM with a durable 32-byte key selected by `--panel-key-file` / `GAMEPLANE_PANEL_KEY_FILE` (legacy default `/data/panel.key`; supplied deployment profiles use separate storage at `/keys/panel.key`). `--panel-key-provisioned` / `GAMEPLANE_PANEL_KEY_PROVISIONED=true` requires an existing protected key and performs no key-file writes. Startup validates ownership, permissions, file type, size and existing ciphertext; unsafe/missing/wrong keys fail closed. Database and key backups need separate protection. Existing Kubernetes management objects are not imported automatically.

Offline `rotate-panel-key --old-key-file PATH --new-key-file PATH` durably creates or reuses a separate key and re-encrypts management secrets atomically, retaining both files. `--new-key-provisioned` requires an existing operator-durable read-only replacement. Versioned `gpk1` ciphertext authenticates its key identity and object identity/version; legacy ciphertext remains readable and is upgraded by rotation. A singleton database key state serializes rotation with all credential mutations, including atomic registration, rejecting stale writers. Exact retries of the immediately preceding rotation authenticate the new state and succeed. Stop every API process first and retain each historical key with separately protected matching backups.

`GET /admin/installation` is authenticated and reports `{standalone,localCluster}`. Empty standalone `/clusters` and `/fleet/*` responses contain no synthesized local cluster. A bounded API poller loads registrations and maintains safe connectivity status, independent of an operator on the panel host. Notifications observe registered remote workloads while credentials remain central.

Standalone remote clients require verified HTTPS and reject kubeconfig proxies and redirects. Direct dial-time resolution rejects every unsafe address before connecting to a pinned literal IP; optional `--remote-allowed-cidrs` / `GAMEPLANE_REMOTE_ALLOWED_CIDRS` further restricts destinations for both Kubernetes and gateway connections. Private workload networks remain supported. Finite decompressed responses are bounded to 8 MiB (`/version`: 64 KiB), JSON watch frames to 1 MiB, and notification snapshots to 1,024 objects per resource. Notification polling every five seconds is enabled only with a configured enabled sink; failed/oversized snapshots do not manufacture transitions. User log and interactive streams are not subject to the finite-response cap.

`POST /clusters` accepts a raw, self-contained kubeconfig string; existing kubeconfig validation rejects executable/file-backed credential references. `PUT /clusters/{name}/gateway` accepts `{url,caCert,clientCert,clientKey}` with an HTTPS origin and PEM TLS material. It persists only labelled management credentials and returns 204 without exposing secrets. `DELETE /clusters/{name}/gateway` clears the reference and only removes API-managed credentials. Existing central `cluster:manage` authorization applies to registration and gateway mutations.

`PUT /clusters/{name}/kubeconfig` accepts `{kubeconfig:string}` and requires central `cluster:manage`. It publishes an immutable labelled credential, updates the existing registration with UID and previous-pointer conflict checks, invalidates the previous workload client and attempts immediate refresh. Registration UID, gateway settings and grants are preserved. The 204 response contains no credentials and does not imply remote connectivity; conflicting rotations return 409. Cleanup uses secret identity/version preconditions and preserves externally managed credentials. Standalone initial registration commits the credential and registration atomically; an exact matching API-owned legacy orphan can be adopted without changing it.

Standalone `/modules/*` requests require one explicit registered remote `cluster` selector and cluster-wide module permission on that target. The selected remote operator owns module reconciliation; upload ConfigMaps use the configured operator namespace. Combined installs retain local module ownership and reject remote selectors on this surface. Local node credential minting is incompatible with standalone startup; Kubernetes system-log endpoints return 501 and the dashboard directs administrators to container logs.

See [standalone installation](../docs/standalone-panel.md) and [design](../specs/023-standalone-panel/spec.md).
