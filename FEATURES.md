# Gameplane features

Maturity labels: **stable** (shipped, on by default, and exercised by a bucketed e2e test), **beta** (shipped with caveats or without a bucketed e2e test), **experimental** (shipped but the docs say so), **planned** (not started), **in progress** (started, not yet merged to master).

Default column: **on** (enabled by the chart or the product), **off** (opt-in, ships disabled), or **n/a** (planned, in progress, or tooling with no install default).

Notification rows take effect once a notification sink is configured.

Status date: 2026-10-14 (v0.3.0, pre-v1).

See [docs/roadmap.md](docs/roadmap.md) for what is planned and in progress.

## Servers and lifecycle

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Create a game server from a module template | stable | on | Creates a GameServer from an installed module template through the create wizard or the API. | [README](README.md#features) |
| Start, stop and restart a server | stable | on | Start clears spec.suspend so the operator scales the pod back up, stop sets it so the pod scales to zero after the stop sequence, and restart stamps an annotation that restarts the pod without changing desired state. | [architecture](docs/architecture.md#start-a-server) |
| Show server phase and conditions | stable | on | Shows the server phase, such as Running or Suspended, with the reason conditions behind each. | [architecture](docs/architecture.md#start-a-server) |
| Clone a server into a new GameServer | stable | on | Copies the spec of a server into a new GameServer. | [README](README.md#features) |
| Switch game version on a running server | stable | on | Changes the template version of an existing server so the operator rolls it out. | [module-authoring](docs/module-authoring.md) |
| Delete a server | stable | on | Deletes a GameServer and its owned child objects. | [README](README.md#features) |
| Hand off server ownership and manage collaborators | beta | on | The owner or an admin can transfer a server to another user, and the owner can set collaborators who may use start, stop, restart, wake and clone. | [security](docs/security.md) |
| Graceful stop with the game's native shutdown sequence | beta | on | Runs the template stop sequence over RCON, telnet or stdin before the pod scales to zero so world saves finish. | [architecture](docs/architecture.md#sleep-an-idle-server) |
| Wipe world data with typed confirmation | beta | on | Suspends the server, erases the world volume and resumes, for owners or admins only. | [architecture](docs/architecture.md) |
| Browse and filter the server list across namespaces | beta | on | The Servers page lists servers from every namespace the viewer may read and filters them with All, Running and Stopped tabs. | [unified-dashboard](docs/unified-dashboard.md) |
| Keep game data on a PVC with clear storage errors | beta | on | Game data stays on its PVC when the game pod is deleted, and a missing storage class sets Ready to False with reason PVCProvisioningFailed; an install-time default storage class is opt-in. | [architecture](docs/architecture.md#start-a-server) |
| Edit game configuration from the Settings tab | in progress | n/a | Lets an operator change a server game configuration from its Settings tab, with validation. | [roadmap](docs/roadmap.md#in-progress) |

## Console and admin actions

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Live console stream for PTY-mode games | stable | on | Attaches the browser to the container stdin and stdout through the pod attach API for templates with consoleMode pty. | [install](docs/install.md#pod-logs-and-pty-console-permissions) |
| Container startup output stream | beta | on | Streams the game container stdout through the pod log API before the game log file exists. | [install](docs/install.md#pod-logs-and-pty-console-permissions) |
| Run template quick actions | stable | on | Runs template-defined admin actions such as save, broadcast, time, weather and reload from buttons. | [module-authoring](docs/module-authoring.md#actions) |
| Validate action input and run actions on PTY-only games | beta | on | Validates action parameters against the template schema and escapes them before they reach the game console, and runs actions on PTY-console games by writing lines to the container stdin. | [dependencies](docs/dependencies.md#gameaction) |
| Live game log tail | stable | on | Streams the game log file over a WebSocket from the agent sidecar. | [architecture](docs/architecture.md#tail-logs) |
| Replay and download the full game log | beta | on | Replays up to the last 500 game-log lines when a tab opens, capped by the agent at 20,000 lines and 4 MiB, and downloads the whole log file without interrupting the live stream. | [architecture](docs/architecture.md#tail-logs) |
| Send commands over RCON and ten console protocols | beta | on | Sends line-based commands over RCON or any of the ten console protocols a template selects, including REST and CLI; running without a password (rcon.authentication false) is opt-in. | [module-authoring](docs/module-authoring.md#rcon) |
| Console toolbar with history, clear, download and fullscreen | beta | on | Recalls the last 100 sent commands with the arrow keys, kept only while the Console tab stays open, and toolbar buttons clear the terminal, download its buffer and toggle fullscreen without stopping the stream. | [README](README.md#features) |
| Kubernetes events timeline for a server | beta | on | Shows the 50 most recent Kubernetes events for the server pod and its parent objects. | [architecture](docs/architecture.md#start-a-server) |

## Files

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Browse, edit, create, download and delete files | stable | on | Lists directories under the exposed data root, saves and creates text files through the agent write endpoint, and downloads or deletes one file, including large downloads that stream past the request timeout. | [security](docs/security.md#agent-file-browser-path-confinement) |
| Upload files and manage folders | beta | on | Uploads files into the open directory, writing them group-readable for the game process, creates folders, and removes a folder and its contents after confirmation, refusing folders that hold dot-prefixed entries. | [security](docs/security.md#agent-file-browser-path-confinement) |
| Confine file access to the exposed root | beta | on | Rejects path traversal and symlink escapes outside the exposed data root. | [security](docs/security.md#agent-file-browser-path-confinement) |

## Players

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| View online players and capacity | stable | on | Shows the online count, the maximum and the player list, refreshed every 5 seconds, for templates that declare a player source. | [README](README.md#features) |
| Manage the whitelist, kick, ban and banned list | beta | on | Edits whitelist entries, kicks online players, adds or removes persistent bans and lists the banned players the game reports, through the template commands; kick returns HTTP 501 when the template does not declare it. | [README](README.md#features) |
| Steam display names on player lists | beta | off | Resolves Steam IDs to display names through the Steam Web API once a Secret is configured. | [install](docs/install.md#steam-display-names-optional) |

## Mods

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Install, upgrade and upload mods | stable | on | Installs a mod file from a download URL with registry metadata in the per-volume mod manifest, replaces or removes an installed mod in place, and installs a multipart file upload as a file-drop mod. | [README](README.md#mod-management) |
| Reject path-traversal and symlink-escape mod archives | stable | on | Extracts valid mod archives while rejecting zip-slip paths and symlinks that escape the target directory. | [README](README.md#mod-management) |
| Install mods by launch-parameter ID | beta | on | Projects selected mod IDs into the launch environment for games that fetch mods at startup. | [module-authoring](docs/module-authoring.md#mods) |
| Browse and install from seven public registries | beta | on | Searches and installs mods from Modrinth, Thunderstore, Hangar, the Factorio Mod Portal, SpigotMC, GitHub Releases and uMod with no API key; Factorio asks for the user's factorio.com login in the install form, and Thunderstore and GitHub Releases need the template to name a community or an owner and repository. | [README](README.md#supported-registries) |
| Browse CurseForge, Steam Workshop and Nexus Mods | beta | off | Searches and installs CurseForge mods and Steam Workshop items once an API key is set under Settings, Mod registries; Nexus Mods is browse-only, and downloads are not supported. | [README](README.md#supported-registries) |
| Store and replace registry API keys from Settings | beta | on | Saves, replaces and removes registry keys under Settings, Mod registries, kept in Kubernetes Secrets or the standalone database. | [README](README.md#supported-registries) |
| Check installed mods for newer versions | beta | on | Reports available updates for mods installed from a provider that has a resolvable version. | [README](README.md#mod-management) |
| Install a modpack from a provider | beta | on | Installs a modpack either by pinning it into a server env (env-mode) or by installing its dependency mods (deps-mode). | [module-authoring](docs/module-authoring.md#modpacks-providersmodpacks) |

## Backups and restore

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Run on-demand and scheduled restic backups | stable | on | Starts a Backup that runs restic against the server data volume and records the snapshot ID; a BackupSchedule creates Backups on its cron expression, records last and next times, can be suspended, and with a Forbid concurrency policy does not start a backup while one is active. | [architecture](docs/architecture.md#back-up) |
| Trim backups with keep-last retention and prune on delete | stable | on | Applies keepLast retention after each scheduled backup, where an empty policy keeps everything, and deleting a Backup runs restic forget with prune so its snapshot is removed. | [architecture](docs/architecture.md#backup-deletion-and-snapshot-cleanup) |
| Connect a restic repository through a labelled Secret | beta | on | Reads the restic repository and credentials from a labelled Kubernetes Secret referenced by repoRef, and points it at S3-compatible storage through the repository URL in that Secret. | [architecture](docs/architecture.md#back-up) |
| Report failed backups and rejected restores | stable | on | Sets a Backup to Failed when its data volume is missing or its repository credentials are rejected, and sets a Restore to Failed when its Backup is missing or has no successful snapshot. | [architecture](docs/architecture.md#back-up) |
| Restore a restic snapshot into an existing server | stable | on | Suspends the target GameServer, overwrites its data volume from a snapshot, and resumes it. | [architecture](docs/architecture.md#restore-from-backup) |
| Quiesce game writes before each backup | beta | on | Asks the game to flush and pause writes before the snapshot when quiesce is true, which is the default on Backups. | [architecture](docs/architecture.md#back-up) |
| Tune backup and restore Job retry limit and deadline | beta | on | Sets the retry count and wall-clock deadline for restic backup and restore Jobs through Helm values. | [architecture](docs/architecture.md#back-up) |
| Browse backups and restores on the dashboard Backups page | beta | on | Lists per-server and global backups and restores with location, server and phase filters and a detail drawer. | [architecture](docs/architecture.md#back-up) |
| Create and restore CSI volume-snapshot backups | beta | off | Captures a CSI VolumeSnapshot of the data volume instead of running restic when a volume snapshot class is set, and restores one into a new GameServer without touching the original server. | [architecture](docs/architecture.md#back-up) |
| Backup and restore drill runbook | planned | n/a | A documented runbook for periodic restore drills; the restore test exists but the runbook does not. | [roadmap](docs/roadmap.md#production-readiness-hardening-planned) |

## Game modules and catalog

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Discover modules from OCI and upload sources | stable | on | The operator indexes an OCI module source and lists its modules and versions in ModuleSource status, and bundles uploaded through the API are stored in an upload source and indexed the same way. | [module-authoring](docs/module-authoring.md#module-sources) |
| Install modules and block deleting ones a server uses | stable | on | Applying a Module CR makes the operator create the cluster-scoped GameTemplate for that module, and a finalizer keeps a Module or GameTemplate in place while a GameServer still uses its template. | [module-authoring](docs/module-authoring.md#installing-a-module) |
| Reject unsafe or unreachable module sources | stable | on | Module source fetches go through the SSRF dial guard, which refuses unsafe destinations, and a Module reports a failure when its source registry cannot be reached. | [module-authoring](docs/module-authoring.md#module-sources) |
| Switch a running server to another module version | stable | on | Changes a GameServer to another catalog version and loader, keeping each version and loader on its own data volume. | [module-authoring](docs/module-authoring.md#version-catalog-specversions) |
| Browse the official catalog and merged module states | beta | on | The official OCI catalog holds 29 game modules and the chart enables 16 by default, and the Modules page shows available, installed, verified, failed and update states across all configured sources. | [module-authoring](docs/module-authoring.md#installing-a-module) |
| Pin or verify module bundles with digests and signatures | beta | off | Sets a Module digest so only that exact bundle installs, and checks bundle signatures against a cosign public key or a keyless Sigstore identity before installing. | [module-authoring](docs/module-authoring.md#verifying-and-pinning-bundles) |
| Use a Git repository, HTTP archive or local directory as a module source | beta | off | Reads module directories from a Git repository at a pinned ref, from a tar.gz or zip archive served over HTTP or HTTPS, or from a path mounted into the operator pod. | [module-authoring](docs/module-authoring.md#module-sources) |
| Complete specs.md for all 29 game modules | planned | n/a | Each game module carries a specs.md documenting its template configuration; 26 of 29 have one today. | [roadmap](docs/roadmap.md#game-module-template-specifications-planned) |
| Nuclear Option join through a load-balancer IP pool override | in progress | n/a | Lets Nuclear Option clients join through a load-balancer IP pool override; blocked until the server binding on UDP 7777 is documented. | [roadmap](docs/roadmap.md#in-progress) |

## Module authoring

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Declare a mods capability in a GameTemplate | stable | on | A template declares file-drop paths or an ID list so the mod install flow knows where mods go. | [module-authoring](docs/module-authoring.md#mods) |
| Render an install wizard and declare typed admin actions | beta | on | A template config schema generates the install and settings form for a server, and admin actions with typed input definitions are rendered and validated by the console and API. | [module-authoring](docs/module-authoring.md#config-schema--wizard) |
| Author modules in the dashboard Modules builder | beta | on | The dashboard offers a builder that creates module content without the CLI. | [module-authoring](docs/module-authoring.md#5-web-dashboard-module-builder) |
| Scaffold, validate, preview and package modules with gp-module | beta | n/a | gp-module init generates a module directory for a chosen archetype, validate checks it offline with JSON output for CI, preview dry-runs the runtime config for a memory limit and user configuration, and package builds a local tar.gz bundle with size limits. | [module-authoring](docs/module-authoring.md#1-scaffolding-a-new-module-init) |
| Push and sign module bundles for OCI sources | beta | n/a | Publishes a packaged module bundle as an OCI artifact for a module source to index, and signs official bundles with a cosign key held in CI secrets. | [module-authoring](docs/module-authoring.md#pushing-a-bundle) |
| Editor JSON schemas for module.yaml and template.yaml | beta | n/a | Provides yaml-language-server schemas so editors validate and autocomplete module and template files. | [module-authoring](docs/module-authoring.md#editor-schemas) |

## Networking and tunnels

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Expose a server through a ClusterIP Service | stable | on | The default expose mode fronts the game pod with a Service reachable only inside the cluster. | [networking](docs/networking.md) |
| Apply a per-server egress NetworkPolicy for the tunnel pod | beta | on | Creates an egress policy for a tunnel-enabled server that admits DNS, advertised ports and provider relay ports. | [tunnels](docs/tunnels.md#troubleshooting) |
| Expose a server through NodePort, LoadBalancer or Hostport | beta | off | Opt-in expose modes publish game ports on node ports with an optional pinned number, ask the cluster load-balancer address manager for an external address, or bind game ports on the node host, which the wake sentinel cannot hold. | [networking](docs/networking.md#address-pool-overview) |
| Request or change a named address pool or specific external IP | beta | off | Writes a MetalLB or Cilium pool preference or a specific address onto a LoadBalancer Service, recorded as unhonored when no address manager is set, and updates pool metadata on a running server, while whether the IP changes depends on the load balancer. | [networking](docs/networking.md#setting-an-address-pool-or-specific-address) |
| Report address assignment, conflicts and pool errors | beta | off | Shows Assigned, AssignmentPending, ServiceNotReady and other reasons on the AddressAssignment condition, reports AddressInUse when another GameServer holds the address, and reports PoolNotFound or AllocationFailed when the pool is absent or exhausted. | [networking](docs/networking.md#troubleshooting-address-assignment) |
| Relay players through frp, Tailscale or playit.gg | beta | off | Runs an frpc tunnel to your own frps server, joins the game pod to your tailnet with an auth key, or runs a playit tunnel whose public address is reported in status, with relay credentials stored as owned Secrets. | [tunnels](docs/tunnels.md#frp) |
| Keep the tunnel pod running while the game sleeps | beta | off | The tunnel Deployment keeps the relay connection and public address up while the game pod is asleep. | [tunnels](docs/tunnels.md#interaction-with-idle-auto-sleep-and-wake-on-connect) |
| Restrict game ingress with a sourceRanges allowlist | beta | off | Limits Service-port traffic to listed source ranges, and does not gate traffic arriving through a tunnel. | [tunnels](docs/tunnels.md#interaction-with-other-networking-fields) |
| Report conflicting networking options as a status condition | planned | n/a | Would flag redundant settings such as a public LoadBalancer plus a tunnel as an informational condition. | [tunnels](docs/tunnels.md#interaction-with-other-networking-fields) |

## Idle sleep and wake

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Wake a sleeping server from the dashboard or wake verb | beta | on | POST /servers/{name}:wake clears the operator sleep marker so the game pod comes back, and the dashboard labels a slept server Asleep and offers Wake instead of Start. | [architecture](docs/architecture.md#sleep-an-idle-server) |
| Sleep idle servers after empty minutes | beta | off | Opt-in spec.idle lets the operator scale a server to zero after it reports no players for afterMinutes, with a 5-minute floor, running the module stop sequence first. | [architecture](docs/architecture.md#sleep-an-idle-server) |
| Explain and monitor idle sleep | beta | off | status.idle.reason and the dashboard explain why idle time is not accruing, a stale heartbeat or missing player count freezes the idle clock, and the gameplane_gameservers_idle gauge reports asleep or awake. | [architecture](docs/architecture.md#sleep-an-idle-server) |
| Wake a sleeping server on a cron wake window | beta | off | spec.idle.wakeWindows cron expressions in the cluster timezone clear the sleep marker at each tick. | [architecture](docs/architecture.md#sleep-an-idle-server) |
| Arm wake-on-connect and hold advertised ports while asleep | beta | off | spec.idle.wakeOnConnect makes the operator run a sentinel pod that holds advertised ports by switching Service endpoints during sleep, and Hostport servers bounce clients and exit on wake instead of holding them. | [architecture](docs/architecture.md#sleep-an-idle-server) |
| Wake on game joins with per-port protocols and try-again disconnects | beta | off | Minecraft server-list pings are answered without waking, Terraria joins wake through the Terraria parser, other games wake on traffic and drop the first client, and held joins past the 25-second deadline get a protocol-native try-again disconnect, with the protocol chosen per port. | [architecture](docs/architecture.md#sleep-an-idle-server) |

## Multi-cluster

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Register, rotate and remove remote clusters | beta | on | Registering stores a remote Kubernetes target with its kubeconfig through the Clusters page or POST /clusters, PUT /clusters/{name}/kubeconfig rotates the credential in place, and removing a registration deletes the kubeconfig Secret the API created for it. | [multicluster-ui](docs/multicluster-ui.md#registered-clusters) |
| Create and read servers on a chosen registered cluster | stable | on | A create or read request with ?cluster=<id> targets that cluster's own Kubernetes API, rejects unknown cluster ids with 400, and never falls back to a same-named local server when a remote target is missing. | [unified-dashboard](docs/unified-dashboard.md#data-and-authorization-contract) |
| Enforce per-cluster RBAC grants on remote servers | stable | on | A viewer bound only to the local cluster is forbidden from reading remote servers. | [multicluster-ui](docs/multicluster-ui.md#user-access) |
| Route module, registry and mod-ID writes to the selected cluster | beta | on | Mod and modpack configuration writes go to the selected cluster with resource-version and UID protection, and remote module catalog reads and writes need a cluster-wide modules:read or modules:manage grant there. | [remote-parity](docs/remote-parity.md#routing-and-identity) |
| Keep node join tokens and kubeconfig issuance local to the central cluster | stable | on | Node join token and kubeconfig endpoints are available only for the local cluster. | [multicluster-ui](docs/multicluster-ui.md#node-inventory) |
| Add nodes and download kubeconfig from the Cluster page | beta | off | With clusterOps enabled, the Cluster page mints a node join command and a downloaded kubeconfig, which grants kube-system and CSR-approval RBAC. | [install](docs/install.md#values-reference) |
| Show API health and node inventory per registered cluster | beta | on | The Clusters page shows Kubernetes API health, version and last check, and the Cluster page lists nodes and capacity for the chosen cluster, showing an error rather than local data when a remote read fails. | [multicluster-ui](docs/multicluster-ui.md#registered-clusters) |
| Combined counts, search and location filters across sites | beta | on | The dashboard totals authorized sites and labels partial totals, one search covers authorized servers from every site without identity collisions, and location filters narrow servers, backups and schedules. | [unified-dashboard](docs/unified-dashboard.md#user-experience) |
| Open canonical server links that carry cluster and namespace | beta | on | Direct links and fresh tabs target the named cluster and namespace, ignoring stored cluster selections. | [unified-dashboard](docs/unified-dashboard.md#acceptance) |
| Block unsafe remote dial addresses, with an optional CIDR allowlist | beta | on | Dial-time checks reject loopback, link-local, metadata, unspecified and multicast destinations for remote Kubernetes and gateway dials; limiting destinations to listed networks with GAMEPLANE_REMOTE_ALLOWED_CIDRS is opt-in. | [standalone-panel](docs/standalone-panel.md#install-and-register-remote-game-clusters) |
| Run remote agent operations through the mTLS gateway | beta | off | The optional private gateway carries files, mods, player data and capture files to a registered cluster over mTLS, stores the gateway URL and labelled credentials per cluster, and refuses agents too old for the UID route. | [multicluster-agent-gateway](docs/multicluster-agent-gateway.md#routing-and-identity) |
| Rotate gateway trust and revoke gateway access | beta | off | Repeated PUT rotates gateway credentials, and DELETE removes gateway access while keeping the cluster registration. | [standalone-panel](docs/standalone-panel.md#configure-remote-agent-access) |
| Keep games running when the gateway is down | beta | off | A gateway outage stops remote agent access but not games or the local operator. | [multicluster-agent-gateway](docs/multicluster-agent-gateway.md#failure-and-rotation-behavior) |
| Run the central panel without a local Kubernetes cluster | beta | off | api.standalone runs the dashboard and API with no operator and the central host never runs games; Docker Compose installs it bound to 127.0.0.1:8080, and module installs act on a selected registered cluster. | [standalone-panel](docs/standalone-panel.md#central-panel-on-kubernetes) |
| Manage the standalone panel encryption key | beta | off | Remote credentials are encrypted in SQL with a key file that must be backed up with the database; rotate-panel-key re-encrypts them offline, and a provisioned-key option requires an existing 32-byte key and never generates one. | [standalone-panel](docs/standalone-panel.md#rotate-the-master-encryption-key) |

## Users, auth and access

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Sign in with local accounts and disable or re-enable local login | stable | on | Local accounts sign in with argon2id-hashed passwords and receive an HttpOnly, Secure, SameSite=Lax session cookie, logging out or a dashboard password reset ends the sessions on the server, and admins can disable local login from Authentication settings until it is re-enabled. | [security](docs/security.md#authentication) |
| Throttle login attempts and show generic login errors | stable | on | Wrong-password attempts are rate limited per source IP and return 429 once the bucket is exhausted, and the login page and other unauthenticated views show only generic messages such as invalid credentials. | [security](docs/security.md#client-ip-extraction-from-forwarded-headers) |
| Enforce a fixed permission catalog with built-in and custom roles | stable | on | Each API route maps to one permission from a fixed catalog and unmatched routes fail closed, the admin, operator and viewer roles are seeded, admins build custom roles from catalog subsets, and built-in roles are immutable. | [security](docs/security.md#authorization) |
| Bind roles per namespace and require cluster-wide grants for cluster permissions | beta | on | Per-namespace role bindings grant a role in one namespace while the wildcard namespace gives cluster-wide access, and cluster-scoped permissions such as users:manage need a cluster-wide binding. | [security](docs/security.md#authorization) |
| Restrict destructive server actions to the owner or an admin | stable | on | Delete, wipe, ownership transfer and collaborator edits require the server's owner or an admin. | [security](docs/security.md#per-gameserver-access-owner--collaborators) |
| Manage dashboard user accounts and remove users with their linked data | beta | on | Admins create users, change a primary role and manage per-namespace bindings from the Users page, and deleting a user removes SSO links, preferences, sessions, API tokens and role bindings and revokes the share links that account created. | [security](docs/security.md#authorization) |
| Refuse changes that would lock out every admin | beta | on | The API refuses an auth config save that leaves no sign-in provider enabled, counting the Helm provider as always enabled, and refuses to demote or delete the last user who can manage users. | [security](docs/security.md#lockout-guard-and-break-glass) |
| Save and customize dashboard theme preferences | beta | on | Any signed-in user can save personal theme preferences, and Settings, Theme and Appearance offers preset themes, custom colors with a contrast check, a CSS overlay, safe mode, and import and export. | [roadmap](docs/roadmap.md#shipped-in-v030) |
| Require the CSRF header on mutating requests | beta | on | Mutating requests must echo the CSRF cookie value in the X-Gameplane-CSRF header (double-submit). | [security](docs/security.md#authentication) |
| Share a read-only status link for one GameServer | beta | on | A token in a share link gives unauthenticated access to one GameServer's status and connection address, optionally with permission to wake it. | [security](docs/security.md#share-links) |
| Recover an admin with bootstrap-admin | beta | n/a | Running bootstrap-admin with --force resets a named account's password, promotes it to admin and ends its existing sessions, and --enable-local-login force-enables the local provider so a locked-out admin can sign in. | [security](docs/security.md#lockout-guard-and-break-glass) |
| Sign in with dashboard-managed or Helm-configured OIDC providers | beta | off | Admins add OIDC providers from Authentication settings with the client secret in a labelled Secret, applied without restart, and the api.oidc Helm values add a read-only provider that the dashboard cannot edit, disable or delete. | [security](docs/security.md#dashboard-managed-providers) |
| Map IdP group claims to dashboard roles | beta | off | Helm-seeded group mappings assign the most privileged matching role at install time, the role is recomputed at every OIDC login, and admins can override a role's group list from Authentication settings, effective at the next login. | [oidc](docs/oidc.md#role-mapping-at-install-time-no-bootstrap-admin-required) |
| Follow OIDC setup guides for five identity providers | beta | n/a | Step-by-step guides cover registering Gameplane as an OIDC client in Keycloak, Authentik, Google, Okta and Entra ID. | [oidc](docs/oidc.md#provider-guides) |
| Service accounts on the Users page | planned | n/a | A Users page tab for service accounts is planned for v1.1. | [roadmap](docs/roadmap.md) |

## Audit and security

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Record and page through the audit log | stable | on | Each mutating API request writes an audit event to the audit_events table that the dashboard serves, and GET /admin/audit pages and filters the events. | [install](docs/install.md#audit-log) |
| Export the audit trail and verify its hash chain | beta | on | GET /admin/audit/export downloads the full trail as CSV or JSON, and GET /admin/audit/verify re-walks the SHA-256 chain and reports the first broken link. | [security](docs/security.md#audit-log-integrity) |
| Forward audit events to stdout or a webhook | beta | off | Mirrors each event as a structured JSON log line when api.audit.stdout is set, and POSTs each event as JSON from a bounded best-effort buffer when api.audit.webhook.url is set, with an optional Authorization header read from a Secret. | [install](docs/install.md#audit-log) |
| Ship audit events to syslog or S3-compatible storage | beta | off | The bundled audit-syslog-bridge turns events into RFC 5424 records sent over TCP, TCP with TLS or UDP, with TCP recommended, and api.audit.s3 batches NDJSON objects flushed at 100 events, 1 MiB or 5 seconds. | [audit-syslog-bridge](audit-syslog-bridge/README.md#transport-prefer-tcp) |
| Prune audit events past a retention age | beta | off | api.audit.retentionDays removes events older than N days, and the default of 0 keeps events forever. | [install](docs/install.md#audit-log) |
| Resolve client IPs through trusted proxies | beta | on | The API reads X-Forwarded-For only from peers inside api.trustedProxies, so audit records and per-IP limits use the real client address. | [security](docs/security.md#client-ip-extraction-from-forwarded-headers) |
| Run Gameplane pods hardened and optionally enforce restricted Pod Security | beta | on | Operator, API, agent and optional bridge pods run as uid 65532 with read-only root filesystems, RuntimeDefault seccomp and all capabilities dropped, and podSecurity.enforceRestricted, off by default, applies the restricted Pod Security Standard to the games namespace. | [security](docs/security.md#pod-security) |
| Redact password values and confine credentials to labelled Secrets | beta | on | API responses replace non-empty GameServer password values with a redaction marker and fail closed if the template cannot be read, and the API reads and deletes only Secrets carrying its own labels for auth providers and notification sinks. | [security](docs/security.md#gameserver-config-passwords) |
| Encrypt agent and capture control traffic with mTLS | stable | on | Operator-managed agent and capture-sidecar control connections use mutual TLS 1.2 or newer with a verified client certificate. | [security](docs/security.md#authentication) |
| Isolate pods with NetworkPolicies | stable | on | Applies NetworkPolicies that restrict ingress to game servers and give the API pod an egress policy that allows private ranges. | [security](docs/security.md#networkpolicies) |
| Audit OIDC role assignments and mapping overrides | beta | off | Each OIDC role assignment, override and reset writes an audit event naming the provider, matched group and role transition. | [security](docs/security.md#audit-trail-every-assignment-override-and-reset) |
| Verify release signatures with the current cosign key | beta | n/a | The ECDSA P-256 cosign key verifies releases from the July 2026 rotation onward, and the retired Ed25519 key verifies earlier releases. | [key-rotation](docs/key-rotation.md#verifying-artifacts) |
| Sign the audit chain with a keyed HMAC | planned | n/a | An HMAC-keyed audit chain with the key held outside the database is a candidate future hardening. | [roadmap](docs/roadmap.md) |

## Notifications

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Store sink credentials in API-created Secrets | beta | on | Entering a sink's URL, ntfy topic or SMTP settings stores them in a Secret the API creates and labels, linked through configRef. | [notifications](docs/notifications.md#sink-credentials-secrets) |
| Use pre-created labelled Secrets for sinks | beta | n/a | A Secret made with kubectl or GitOps works when it carries the gameplane.local/notification-sink label and a matching configRef. | [notifications](docs/notifications.md#sink-credentials-secrets) |
| Alert on unhealthy servers, failed backups and restores | beta | on | server.unhealthy fires when a GameServer fails or loses its heartbeat and server.recovered when it becomes healthy again, backup.failed and restore.failed fire when a Backup or Restore enters the Failed phase, and backup.succeeded and restore.succeeded are opt-in events that a sink must select. | [notifications](docs/notifications.md#events) |
| Deliver alerts through a bounded queue with retries, test sends and metrics | beta | on | Notifications are sent from a bounded in-memory queue so a slow sink never blocks reconciliation or a request, network errors and 5xx responses are retried twice with 2 and 8 second backoffs, a POST to the sink test endpoint delivers a synthetic event, and gameplane_notify_deliveries_total counts sent, failed, dropped and skipped deliveries. | [notifications](docs/notifications.md#delivery-semantics-and-observability) |
| Guard sink dials and keep sink URLs out of errors | beta | on | Every sink dial goes through the netguard IsAllowed policy and refuses link-local, unspecified, multicast and NAT64 destinations at dial time, and delivery errors are sanitized so a sink URL carrying a capability token never appears in them. | [security](docs/security.md#notifications) |
| Label alerts with the instance name and post JSON webhook payloads | beta | on | The Admin Settings instance name is sent as the instance field of each alert, and the generic webhook receives a JSON object with type, ts, kind, name, namespace, message, instance and test fields. | [notifications](docs/notifications.md#payloads) |
| Send alerts to Discord, Slack, ntfy, SMTP or a generic webhook | beta | off | Admins add sinks in Admin Settings, each with its own payload format (Discord embed, Slack text, ntfy headers, SMTP plain text, webhook JSON), and SMTP takes a TLS mode of starttls (default), implicit or none; none only works for unauthenticated relays. | [notifications](docs/notifications.md#sink-credentials-secrets) |

## Telemetry

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Send daily usage counts with admin and install-time off switches | stable | on | The API sends its version, GameServer count and GameTemplate count once a day with no names, namespaces or addresses, holds reports until the first admin sees the first-login notice, and stops when turned off in Admin Settings or hard-disabled at install time. | [install](docs/install.md#telemetry) |
| Send extended usage reports with a resettable, signed install ID | beta | on | Extended reports add a random install ID, environment bands, per-module counts, feature flags and UI language from fixed sets, are signed with an Ed25519 install key, and go to the project default receiver unless a custom endpoint is set; admins can reset the install ID. | [install](docs/install.md#telemetry) |
| Run a bundled telemetry receiver from Helm | beta | off | api.telemetry.receiver.enabled deploys a receiver beside the install with a PVC for its SQLite data, which decodes reports strictly and keeps only daily aggregates and expiring activity records, expires old data on a retention schedule, and pseudonymises install IDs with a pepper. | [telemetry-provider](docs/telemetry-provider.md#deploy-on-kubernetes) |
| Limit and proof-of-work gate report ingestion on the receiver | beta | off | INGEST_SOURCE_DAILY_LIMIT caps accepted reports per source per UTC day, with a default of 20, and INGEST_POW makes reporters solve a challenge whose difficulty rises with request rate. | [telemetry-provider](docs/telemetry-provider.md#configuration) |
| Expose receiver summary, dashboard and metrics | beta | off | GET /v1/summary returns five fixed counts without credentials when PUBLIC_SUMMARY is true, the receiver dashboard on port 8081 starts only when DASHBOARD_TOKEN is at least 32 characters, and /metrics accepts only the Bearer token. | [telemetry-provider](docs/telemetry-provider.md#public-summary) |
| Run a standalone telemetry provider | beta | n/a | A runbook covers deploying the receiver image, TLS, secrets, Cloudflare settings, metrics and backups for a provider serving other installs. | [telemetry-provider](docs/telemetry-provider.md) |
| Show a version-trend view in the receiver dashboard | in progress | n/a | A version-trend view in the telemetry dashboard is in progress under the spec 022 amendment T101 to T110. | [roadmap](docs/roadmap.md) |

## Observability and admin

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Serve API, operator and agent Prometheus metrics | beta | on | The API serves Prometheus metrics on a private listener the public port does not expose, the operator exports controller-runtime metrics with the gameplane_gameservers and gameplane_backups phase gauges, and each game pod agent exposes gameplane_agent gauges on a plain metrics port 9090. | [install](docs/install.md#metrics) |
| Admin system log viewer | beta | on | Admins follow API and operator logs in separate streams, filter them, and download raw records. | [install](docs/install.md#observability) |
| Ship ServiceMonitor, PrometheusRule and Grafana objects from Helm | beta | off | Helm can create Prometheus Operator ServiceMonitor and PodMonitor objects that scrape the operator, API, telemetry receiver and agents, ship operator reconcile, workqueue, GameServer Failed and Backup Failed alert rules, and ship a Grafana operator dashboard ConfigMap for sidecar auto-import. | [install](docs/install.md#alerts) |
| Capture game pod packets and expire captures automatically | beta | off | Admins start opt-in, BPF-filtered PCAPNG captures on a running game pod and download the files, and completed captures are deleted automatically after their retention window ends. | [install](docs/install.md#values-reference) |

## Localization

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Localized dashboard and community language packs | in progress | n/a | Spec 019 adds app localization and community language packs; the work is on open PR #668 and is not yet on master. | [roadmap](docs/roadmap.md#in-progress) |

## Developer and operator tooling

| Capability | Maturity | Default | Description | Docs |
|---|---|---|---|---|
| Install and upgrade the panel from the Helm chart | stable | on | One helm upgrade --install installs the operator, API and dashboard, and a pre-upgrade hook applies the Gameplane CRDs so every upgrade keeps the schema current. | [install](docs/install.md#one-shot-install) |
| Read-only MCP server | beta | off | An optional MCP server lets AI assistants read GameServers, pods, events and logs and propose fixes without writing. | [mcp-server](mcp-server/README.md#tools) |
| Helper image overrides for air-gapped mirrors | beta | n/a | Helm lets operators retag the config-init and restic helper images to a private registry mirror. | [install](docs/install.md#values-reference) |
| Rolling edge images | beta | off | Setting image.tag to edge runs the rolling build published on every master push. | [install](docs/install.md#edge-channel-latest-edge-build) |
| Local Kind development loop | beta | n/a | Make targets create a Kind cluster with a local OCI registry, load images and run the web dev server. | [contributing](docs/contributing.md#dev-loop) |
| Store API data in PostgreSQL | experimental | off | Runs the API against a PostgreSQL database instead of SQLite, but only in images built with the postgres build tag. | [install](docs/install.md#values-reference) |
| Reuse an existing PVC for the API database or standalone panel key | beta | off | Mounts a pre-existing PersistentVolumeClaim for the API SQLite database, or a pre-existing key-only PersistentVolumeClaim for generated standalone encryption keys, instead of creating one. | [install](docs/install.md#values-reference) |
