# Architecture

Gameplane is split across long-lived components — dashboard, API, operator,
and [optional] audit-syslog-bridge / telemetry-receiver satellites — plus a
short-lived per-pod agent sidecar.

The default Helm installation runs the panel and operator together. A
[standalone panel](standalone-panel.md) runs the dashboard and API without local
Kubernetes; registered remote clusters run the operators and game workloads.
The diagram and direct-agent examples below describe the combined installation.

> **For AI Agents & Developers:** This document provides a high-level human-readable overview. For deep technical details, specifications, and boundaries of individual components, read the respective `specs.md` files located in each component directory (e.g., `api/specs.md`, `operator/specs.md`). AI Agents should consult [`agent-architecture.md`](agent-architecture.md) as their primary index.

```
┌───────────────────────────────────────────────────────┐
│  Browser (web/)                                       │
│    React + TanStack Router/Query                      │
│    xterm.js · Monaco · Tailwind                       │
└───────────────────────────────────────────────────────┘
                    │ HTTPS / WSS (same-origin)
┌───────────────────────────────────────────────────────┐
│  API (api/)                                           │
│    REST + WebSocket gateway                           │
│    Local + OIDC auth · RBAC · audit log               │
│    SQLite (production) or Postgres [experimental] for user store │
│    mTLS to agent                                      │
└───────────────────────────────────────────────────────┘
                    │
       ┌────────────┴────────────┐
       ▼                         ▼
┌──────────────┐         ┌─────────────────────┐
│  K8s API     │         │  In-pod agent       │
│              │         │  (sidecar in every  │
│  CRD reads   │         │   GameServer pod)   │
│  CRD writes  │         │  RCON · files       │
└──────────────┘         │  logs · console     │
       │                 │  heartbeat          │
       ▼                 └─────────────────────┘
┌────────────────────────────────────────────┐
│  Operator (operator/)                      │
│    controller-runtime reconcilers          │
│    GameServer   → StatefulSet+Service+PVC  │
│    Backup       → Job (restic)             │
│    BackupSchedule → Backups + retention    │
│    Restore      → Job (restic restore)     │
│    GameTemplate → inUseCount bookkeeping   │
│    Module       → materializes GameTemplate│
│    ModuleSource → indexes bundle registry  │
│    Cluster      → remote cluster registry  │
└────────────────────────────────────────────┘
```

## Dashboard component layer

The dashboard (`web/`) builds its UI on **HeroUI** (`@heroui/react`, `@heroui/styles`) rather than hand-rolled or Radix-based primitives. Two families make up the visual layer:

- **HeroUI base components** — imported directly from `@heroui/react` (Button, Card, Modal, Table, Tabs, Chip, Alert, Dropdown, Popover, and the rest of the library) wherever a screen needs a stock form control, layout primitive, or overlay.
- **Gameplane compositions** (`web/src/components/ui/`) — HeroUI components pre-wired with Gameplane's brand tokens, copy, and behavior for patterns that recur across the dashboard, grouped by role:
  - *Authenticated shell*: `AppShell`, `Sidebar`, `TopBar`, `Breadcrumbs`, `NotificationsPanel`, `GlobalSearch`, `AppearanceToggle`, `AppLoadingSkeleton`
  - *Status & data display*: `StatCard`, `PhaseChip`, `Meter`, `Sparkline`, `GameIcon`, `SlackIcon`, `ResourceInput`, `ProvenanceBadge`
  - *Feedback surfaces*: `LoadingCard`, `ErrorCard`, `ErrorBanner`, `AuditIntegrityBanner`, `CaptureWarningBanner`
  - *Dialogs, menus & overlays*: `ConfirmDialog`, `ConfirmAdminMappingDialog`, `RoleEditorModal`, `DropdownMenu`, `FilterPopover`, `PageHeader`, `RemovableGroupChip`
  - *Admin dialogs* (`admin/`): `InviteUserDialog`, `EditUserDialog`, `ResetPasswordDialog`

Brand tokens (orange accent, dark-default with light mode supported) map onto HeroUI's semantic token layer — see `specs/done_014-heroui-web-rebuild/contracts/theme-tokens.md` for the mapping and `web/specs.md`'s "HeroUI Component Layer" section for the full component inventory. Design changes still originate in `design.pen` (CLAUDE.md rule 1) before either layer is touched in code.

## Why two control planes?

The operator handles everything a cluster admin expects from a K8s
controller: declarative reconciliation, owner refs, leader election,
event emission. The API handles everything the browser expects from a
SaaS dashboard: session auth, CSRF, per-user RBAC beyond K8s RBAC,
audit trails.

Keeping them separate lets advanced users bypass the API entirely and
manage Gameplane with `kubectl apply` — the operator is authoritative.

## Data flow examples

### Start a server

1. Dashboard → `POST /servers/foo:start`
2. API → `PATCH spec.suspend=false` on GameServer via k8s API
3. Operator observes GameServer update, scales StatefulSet to 1
4. StatefulSet starts the game pod (game container + agent sidecar)
5. Agent starts heartbeating → sets `status.agent.lastHeartbeat`
6. Operator observes heartbeat → sets `status.phase=Running`
7. Dashboard receives the status update through local SSE or resource polling and re-renders

> **What `Running` means.** The phase flips to `Running` once the pod is
> Ready *and* the agent heartbeat is fresh. That signals the pod and
> sidecar are healthy — **not** that the game protocol (RCON / server
> query) is responsive. `status.agent.playersOnline` stays unknown until
> the game answers a player-count query, and the Console is unavailable
> until RCON is provisioned. Games needing stricter, protocol-level
> readiness should express it through the template's readiness probe.
>
> The operator updates only the phase/conditions/endpoints it owns via a
> JSON merge patch, while the agent independently patches `status.agent`;
> the two never clobber each other.

### Sleep an idle server

Opt-in per server via `spec.idle`. Every reconcile the operator decides, from
the object alone, whether idle time is accruing:

1. Agent heartbeat sets `status.agent.playersOnline` (null when the game
   exposes no player source)
2. Operator sees a *fresh* heartbeat reporting exactly `0` while the phase is
   `Running` → stamps `status.idle.emptySince`
3. `emptySince` older than `spec.idle.afterMinutes` → operator writes the
   `gameplane.local/idle-asleep-since` annotation
4. `desiredReplicas` sees the marker and runs the same `softStop` as an explicit
   stop: module stop sequence over RCON/pty, wait for not-ready, scale to zero
5. Phase reads `Suspended` with condition reason `IdleAsleep`; the PVC is kept
6. A `wakeWindows` cron tick, or `POST /servers/foo:wake`, clears the marker and
   the pod comes back

> **Why not `spec.suspend`.** That field is the user's own power switch — the
> `:start`/`:stop` verbs patch it directly. If the operator wrote it too, an
> automatic sleep would be indistinguishable from a deliberate stop, and a wake
> window would resurrect a server its owner had turned off. The sleep marker is
> therefore an operator-owned annotation, and `spec.suspend` always wins over
> the idle policy.
>
> **Unknown is not zero.** A stale heartbeat or an absent `playersOnline` both
> freeze the idle clock rather than counting as empty — a dead agent must never
> look like an empty server, and a game with no player-count protocol must never
> sleep. Both cases record the reason on `status.idle` so the dashboard can say
> why a server will never sleep.
>
> **Unparseable wake window.** If a `wakeWindows` entry fails to parse, the
> operator surfaces it as its own `IdleScheduleInvalid` condition (reason
> `WakeWindowUnparseable`, message the parse error) rather than only folding it
> into `status.idle.reason` — visible whether or not the server happens to be
> asleep, and cleared once the schedule is fixed.

### Tail logs

1. Dashboard opens WS `/ws/servers/foo/logs`
2. API verifies session, RBAC → dials `wss://foo-agent.gameplane-games.svc.cluster.local:8090/logs/tail` using mTLS
3. Agent tails the game container's log file and streams each line as a text WS frame
4. API proxies frames back to the browser; xterm.js renders them

The Logs tab can also stream the game container's stdout directly via the
Kubernetes pod-log API (`/ws/servers/foo/logs/pod`, no agent mTLS needed).
This is the default source and surfaces download/config output during
startup, before the game's own log file exists.

### Back up

1. Dashboard → `POST /backups` with inline `spec`
2. API creates the Backup CRD
3. Operator creates a Job running `restic backup /data` against the PVC
4. Job completes; operator mirrors Job status into Backup.status
5. (Agent, during the Job, optionally issues an RCON `save-all` to quiesce)

A `Backup` is only usable once its `status.snapshotID` has been read out of
the restic Job's pod logs — a backup with no snapshot id cannot be restored,
so parking it at `Succeeded` would be misleading. The operator retries the
scrape, and if the id still can't be read a grace period after the Job
finished (the pod logs were rotated or garbage-collected, or the Job itself
is gone) it transitions the Backup to `Failed`.

It always releases the game world: a Backup with `spec.quiesce` left the game
with auto-save off waiting for the post-backup unquiesce, so the unquiesce is
attempted before — and, if it fails, retried after — the Backup goes
terminal, rather than one failed attempt being final. A terminal Backup
(`Succeeded` or `Failed`) is otherwise not reconciled further, but keeps
being requeued (every 30s) until its unquiesce lands or it never quiesced in
the first place. Deleting a quiesced Backup goes through the same release: a
finalizer holds the object until the unquiesce is sent, so `kubectl delete
backup` can't drop it silently.

### Backup deletion and snapshot cleanup

Deleting a restic-strategy `Backup` also removes its snapshot from the
repository, so retention actually reclaims space instead of orphaning one
snapshot per deleted Backup. The same path serves every way a Backup can go:
`kubectl delete`, the dashboard/API, a `BackupSchedule`'s retention trim, a
`concurrencyPolicy: Replace` schedule dropping an in-flight Backup, and
garbage collection of a deleted GameServer's auto-schedule.

- **Finalizer.** Every Backup that has a `repoRef` and is not a
  `volume-snapshot` Backup carries `gameplane.local/backup-snapshot-finalizer`
  (added by the operator on its first reconcile, including for Backups created
  before the finalizer existed). Volume-snapshot Backups need none: their
  `VolumeSnapshot` is garbage-collected through its owner reference. A Backup
  that is Failed, or has no recorded `status.snapshotID`, is released at once.
- **Forget Job.** On delete the operator runs a one-shot Job
  `<backup>-forget` that executes `restic forget <snapshotID> --prune` with
  the same repo Secret, restic image and locked-down security context as the
  backup Job. It needs no PVC, GameServer or GameTemplate, so it still works
  after the server is gone. The Job has a backoff limit of 3 and a 30-minute
  deadline, and waits up to 10 minutes for the repository lock
  (`--retry-lock`); backup and restore Jobs use the same `--retry-lock` so neither
  fails while a forget is pruning. A snapshot that is already gone counts as
  success. The Backup stays `Terminating` for the duration (seconds to minutes),
  then goes; the outcome is recorded as a `SnapshotForgotten` condition and a
  `SnapshotForgotten` event.
- **Ordering.** The unquiesce finalizer runs first (a frozen game world is
  worse than an orphaned snapshot). A Backup deleted while its own Job is
  running has that Job deleted, and its pods awaited, before anything is
  forgotten; if no snapshot id had been recorded by then, the Backup is released
  with a `SnapshotForgetSkipped` warning and any snapshot it wrote is left in
  place. The forget also waits while a non-terminal `Restore` uses the Backup (by
  reference or by pinned snapshot id), since `forget --prune` takes the
  repository's exclusive lock.
- **Never blocks forever.** The whole stage is capped at 45 minutes, measured
  from the `backup.gameplane.local/forget-started-at` annotation. When the repo
  Secret is gone, its keys are missing, the namespace is terminating, the Job
  fails, or the cap passes, the finalizer is released with a
  `SnapshotForgetAbandoned` warning naming the snapshot, and
  `SnapshotForgotten=False` is recorded. The snapshot then stays in the
  repository and can be removed by hand with `restic forget <id> --prune`.
- **Caveats.** A repository that cannot be pruned with the Secret's credentials
  (for example an append-only rest-server) cannot be cleaned up this way; every
  delete there ends in the abandoned path, so prune it externally. Snapshots of
  Backups deleted before this finalizer existed are not cleaned up retroactively.
  Rolling the operator back to a release without this finalizer leaves existing
  Backups with a finalizer the old operator does not remove; clear it with
  `kubectl patch backup <name> --type=merge -p '{"metadata":{"finalizers":null}}'`.

### Restore from backup

1. Dashboard → `POST /restores` with inline `spec` (Backup reference, target GameServer)
2. API creates the Restore CRD
3. Operator waits for source Backup.status.snapshotID to exist, then pins
   it into Restore.status
4. For restic-snapshot backups: Operator suspends the target GameServer,
   creates a restic-restore Job (`restic restore <id> --target / --delete`,
   so files created after the snapshot are removed rather than left
   alongside the restored data), and resumes on completion
5. For volume-snapshot backups: Operator provisions a new GameServer seeded
   from the CSI snapshot (no suspend/resume needed). If the original server's spec
   references Secrets or ConfigMaps that it owns (via OwnerReference), the operator
   copies them to new objects owned by the restored server (with rewritten names),
   because the restored server cannot inherit ownership of the originals. The references
   are rewritten in the new server's spec when it is created, and the copies are
   re-ensured on every reconcile pass. The restore fails before creating the new server
   if the original server does not own every referenced object; transient errors
   requeue the restore instead of failing it. The restored server must reach Running phase within 10 minutes;
   if it is still starting after that, the restore fails with a deadline message.

A Restore to an existing server (restic-snapshot strategy) requires the target
suspended for the Job's duration — the PVC cannot be overwritten while the game
is live. Volume-snapshot Restores skip the existing server and provision a fresh
one from a snapshot instead, preserving the original server's spec (and handling
its owned references as described above).

The operator pins the source Backup's `snapshotID` into `Restore.status` the
first time it observes it, so retention deleting the snapshot mid-restore
cannot pull the rug out from under the Job. Before the pin, a source Backup
that is missing, or already `Failed`, fails the Restore with a clear message;
a Backup that simply hasn't produced a `snapshotID` yet leaves the Restore
`Pending`, requeued every 15s until it does. After the pin the Backup's
`snapshotID` is never re-read — but a source Backup that disappears outright
still fails the Restore on the next pass.

## Module system

A game module is a bundle of `module.yaml` (catalog metadata) +
`template.yaml` (a GameTemplate spec) + optional README/icon. Modules
can be loaded from anywhere:

- **ModuleSource** (cluster-scoped CRD) declares one store via a typed
  spec: `oci` (registry artifacts, explicit module list), `git`
  (auto-discovered module dirs at a ref), `http` (a tar.gz/zip
  archive), `local` (a directory mounted into the operator —
  `--module-local-root`, Helm `operator.localModules`), or `upload`
  (ConfigMaps labeled `gameplane.local/module-upload=true` in the operator
  namespace, written by the dashboard's upload endpoint or applied by
  hand).
- The **ModuleSource controller** indexes each source through a
  `modsrc.Fetcher` (operator/internal/modsrc — one implementation per
  type over a shared fs scanner) and caches the catalog into
  `status.modules`, each entry carrying a content digest (OCI manifest
  digest, git commit, or sha256 over the module dir).
- A **Module** CR installs one entry: the controller pulls the bundle
  and materializes an owned **GameTemplate**; the digest comparison
  re-applies bundles whose content changed behind an unchanged version
  (moving git branch, re-uploaded ConfigMap). A finalizer blocks
  uninstall while GameServers reference the template.
- Templates can also be `kubectl apply`'d directly — module-managed
  ones are distinguished by the `gameplane.local/managed-by=Module` label.
- `template.yaml#spec.capabilities` declares per-game console commands
  (player moderation, backup quiesce); the operator serializes it onto
  the agent sidecar (`GAMEPLANE_CAPABILITIES`), which interprets the
  commands at runtime — new games get full feature support without
  agent code changes.

The API's `/modules` surface (catalog merge, install, source CRUD,
bundle upload) only reads and writes these CRs/ConfigMaps; the
operator owns all reconciliation, so the dashboard and kubectl always
converge on the same outcome. Format spec: `docs/module-authoring.md`.

Combined panels manage the local module catalog. Standalone panels require an
explicit registered cluster for module and source operations; that cluster's
operator reconciles the changes.

## Multi-cluster (federation)

Gameplane scales across multiple Kubernetes clusters through a
federation model: each target cluster runs its own operator and agents,
while the central API server holds a pool of
Kubernetes clients keyed by cluster ID and dispatches requests via a
`?cluster=` URL parameter. Combined installations have a built-in `local`
cluster targeting the same cluster as the API. Standalone panels have no local
workload client and start with an empty registry.

**Cluster registration and health:**

- In combined mode, a `Cluster` CRD (group `gameplane.local/v1alpha1`) is the source of
  truth for additional clusters. Each `Cluster` references a
  `kubeconfig` Secret via a selector, and the API watches `Cluster`
  updates to populate its client pool live.
- The kubeconfig Secret **must** be labelled
  `gameplane.local/cluster-kubeconfig=true` in the control-plane
  namespace — the label guard prevents pointing at arbitrary Secrets
  (see "Kubeconfig Secret handling" in `docs/security.md`).
- In combined mode, the operator health-checks each registered `Cluster` and reconciles
  `status.phase` (Unknown/Healthy/Unhealthy), so Kubernetes connectivity
  is visible in the dashboard. This status does not probe an optional gateway.
- Standalone panels persist registrations and labelled credentials in SQL.
  Credential payloads use AES-GCM with a persistent key outside the database.
  The API polls registrations and remote Kubernetes health every 30 seconds;
  it needs no central operator. See [storage and recovery](standalone-panel.md#storage-and-recovery).

**Deployment model:**

- The **target cluster** runs its own operator instance (deployed via
  Helm to manage GameServer, Backup, and other CRDs on that cluster).
  Optional remote agent access also runs a private gateway from the API image,
  with no user database or browser/admin routes. Upgrade the operator and agents
  for the UID-bound agent protocol before enabling this path.
- The **central panel** hosts the API and dashboard. A combined installation
  can also host local game pods. A standalone panel manages registered remote
  workloads and can run on a host without Kubernetes.
- A **resource request** made to the API with `?cluster=<name>` is dispatched to
  the named cluster's Kubernetes client. Omitting the selector targets
  the built-in `local` cluster in combined mode and fails in standalone mode.
  Collection reads under `/fleet/*` combine
  authorized scopes by default, with optional cluster and namespace filters.
  Each returned resource retains its target identity and permissions; partial
  results report unavailable or truncated scopes. See the
  [unified dashboard](unified-dashboard.md) for the collection contract.

**Interactive connections:**

- Pod startup/stdout logs and PTY console attach resolve a Kubernetes client
  from the registry for each connection. Both the API URL and attach credentials
  come from that client. Unknown or removed clusters fail closed.
- The API checks the GameServer → StatefulSet → Pod controller owner UIDs before
  accessing a workload. Pod-log polling stops when the Pod UID changes; reconnects
  validate the new workload. Kubernetes log/attach APIs address Pods by name and
  do not support UID preconditions, so this is a preflight check, not an atomic
  authorization guarantee across deletion/recreation.
- The browser binds a socket to the open server's cluster and namespace.
  Reconnects retain that target; leaving the server view closes its streams.
  Inventory selection and list filters cannot retarget an open server operation.
  Unsupported remote operations fail instead of reaching a local namesake.
- RCON, game-file logs, files, players, live status and agent-based mods use the
  selected cluster's optional gateway. It verifies a dedicated central mTLS peer,
  target cluster, GameServer UID and owned agent Service, then uses local DNS and
  agent mTLS. Versioned agent routes verify the UID on the final hop; older agents
  fail closed. RCON module actions use this path, while stdin actions use the
  selected Kubernetes client with the same preflight limitations as PTY attach.
- Existing local installations retain direct agent connections. Remote gateway
  access requires both private gateway reachability and direct Kubernetes API
  access; it does not tunnel Kubernetes operations or replicate game storage.
  Capture downloads and cleanup use a separate gateway route bound to both the
  GameServer and NetworkCapture UIDs; sidecars verify persisted file identity.
  Modpack and ID-list configuration use the selected Kubernetes client and
  template, with provider credentials retained centrally.
  See [remote agent access](multicluster-agent-gateway.md) and
  [gateway installation](gateway-install.md) for configuration and boundaries.

**RBAC and permissions:**

Registering a cluster grants no implicit workload access. The central API stores
user roles and grants by cluster and namespace in its database. Existing primary
roles remain pinned to `local`; in standalone mode they grant panel administration
without remote workload access. Add remote grants through **Users & RBAC** or the
user bindings API. The registered kubeconfig separately needs Kubernetes RBAC on
the target. See [registration and permissions](install.md#rbac-and-permissions).

## Security boundaries

- **Browser → API**: HTTPS, session cookie + CSRF header, OIDC or local login.
- **API → local Agent**: mTLS; client cert signed by operator-managed CA mounted into API pod.
- **API → remote Gateway**: dedicated mTLS trust and an exact enrolled central
  URI SAN. The central API remains the user-authorization authority.
- **Gateway → Agent**: local agent mTLS and versioned GameServer UID-bound routes;
  browser credentials are not forwarded. The gateway exposes only allowlisted
  operations in configured namespaces and bounds active stream lifetimes.
- **Agent → K8s**: in-pod ServiceAccount, scoped to updating its owning GameServer's status.
- **Operator → K8s**: The operator's Kubernetes RBAC enforces a principle of least privilege across two layers. **Cluster-wide:** The operator's ServiceAccount holds a ClusterRole granting get/list/watch on Gameplane CRDs, Secrets, ConfigMaps, Services, PVCs, Pods, ServiceAccounts, Roles/RoleBindings, VolumeSnapshots and NetworkPolicies; writes are restricted to the cluster-scoped CRDs (GameTemplate CRUD, Module update/patch and finalizers), to CRD `/status` subresources, and to Events. **Namespace-scoped:** A Role in each managed namespace (`gameplane-games` by default, and any user-defined namespace) grants the operator create/update/patch/delete on GameServers, Backups, BackupSchedules, Restores, NetworkCaptures, StatefulSets, Deployments, Jobs, Services, PVCs, ConfigMaps, Secrets, ServiceAccounts, Roles/RoleBindings, NetworkPolicies and VolumeSnapshots, `update` on the GameServer, Backup, BackupSchedule and Restore finalizers, plus `pods/attach create` and `pods/ephemeralcontainers` get/patch/update. This split confines the operator's writes to its managed namespaces; it does not limit cluster-wide reads, including Secrets.
- **Operator/Agent → external fetches**: a shared dial-time SSRF guard
  (`netguard/`) refuses cloud-metadata and other unroutable-for-the-caller
  addresses — permissive for the operator's admin-configured ModuleSource
  fetches, strict for the agent's user-triggered mod-install downloads.
- **Agent/API → game console (module actions)**: a shared console-injection
  guard and command-template renderer (`gameaction/`) validates every
  module-declared action's inputs before rendering — rejecting control
  characters and shell/RCON metacharacters (; & | $ ` \ " ') and enforcing types, enum membership, a length cap, and
  required-ness. Both importers (the agent's RCON path and the API's stdin
  pod-attach) call it independently; each is its own trust boundary, so
  validation is never skipped because the other side already checked. The agent's player-moderation reason (kick/ban) uses the same policy function (gameaction.CheckText).
- **Operator → sentinel [optional]**: holds advertised ports while a GameServer
  is asleep and wakes it on a genuine connection attempt (opt-in via
  `spec.idle.wakeOnConnect`). Runs as a small 1-replica Deployment per armed
  server; disabled by default. Works across all four expose modes
  (ClusterIP/NodePort/LoadBalancer/Hostport); Hostport has an asymmetric
  limitation documented in `docs/roadmap.md`. See `sentinel/`.
- **Sentinel → game protocol parsing (gameproto)**: shared Go module for
  Minecraft and Terraria handshake parsing, used by the sentinel to distinguish
  a genuine join from a server-list ping without corrupting the connection stream.
  UDP-only games (Valheim, Factorio, etc.) have no connection to hold, so the
  sentinel uses a generic packets-in-window heuristic instead. See `gameproto/`.
- **API → audit-syslog-bridge**: HTTP-JSON webhook; the bridge forwards plaintext or TLS syslog to the collector
  for the audit trail, enabled via `api.audit.webhook.syslogBridge.enabled`.
- **API → telemetry-receiver**: the daily usage report, in a basic tier
  (version, server and template counts) and an extended tier (adds a random
  install ID, environment, game and feature categories, and an Ed25519
  signature). New installs send both by default after a first-login notice;
  each tier has its own admin toggle, and `api.telemetry.enabled=false`
  disables sending entirely. An empty `api.telemetry.endpoint` means the
  project's default receiver (`telemetry.gameplane.net`); the bundled receiver
  (`api.telemetry.receiver.enabled`) and any other URL replace it. The
  receiver keeps daily aggregates in SQLite, serves a token-protected
  dashboard on a separate port, and can expose a public five-count summary.
  See [install.md](install.md#telemetry) and
  [telemetry-provider.md](telemetry-provider.md).
- **Operator → capture-sidecar [optional]**: network packet capture sidecar
  injected into game pods as an ephemeral container; captures AF_PACKET frames
  matching a BPF filter to PCAPNG output, exposed via mTLS on port `:9091`.
  Opt-in per GameServer via `spec.capture.enabled` and admin-only (`captures:manage`
  permission). Disabled by default; enabled cluster-wide via `capture.enabled`
  Helm value. See `capture-sidecar/`.
- **mcp-server [optional]**: a standalone, strictly read-only MCP server —
  its tool handlers only ever hold a `*kube.Client` (`mcp-server/internal/kube`),
  whose sole exported methods are List/Get-shaped; the underlying typed/
  dynamic Kubernetes clientsets are unexported fields in that package, so
  no handler can reach a mutating verb. The authoritative backstop is RBAC:
  a `get`/`list`/`watch` ClusterRole, no create/update/patch/delete anywhere
  in it — but note it is **cluster-wide** (all namespaces, including
  `kube-system`), and pod logs can surface secrets an app logs at startup.
  Talks directly to the Kubernetes API (not through the API server), gated
  via `mcpServer.enabled` and reachable only via `kubectl exec` (stdio
  transport, no network port). See `mcp-server/README.md`.

See `docs/security.md` for the threat model.
