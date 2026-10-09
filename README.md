# Gameplane

A Kubernetes-native game server control panel. Open-source alternative to
[CubeCoders AMP](https://cubecoders.com/AMP) with a K8s backend instead of
Docker — scales from a single-node k3s homelab to multi-node production
clusters without changing the operational model.

The dashboard and API can also run on a host without Kubernetes using
[standalone Docker Compose](docs/standalone-panel.md). Game servers still run
on registered Kubernetes clusters, each with its own operator and agents.

> Status: **pre-v1 release** (`v0.3.0`). The operator, API, agent, and dashboard
> are feature-complete for the v1 scope and stabilized for external testing.
> See [Pre-v1 status & known limitations](#pre-v1-status--limitations) before
> running it for anything you can't afford to lose.

**Website:** <https://gameplanepanel.github.io/website/> — features,
docs, and comparisons. Source lives in
[`GameplanePanel/website`](https://github.com/GameplanePanel/website),
mounted here as the `website/` submodule.

## Screenshots

Screenshots are captured against mocked data for consistency and reproducibility; all UI layouts and components reflect the current dashboard.

| | |
|---|---|
| ![Sign-in form with username and password fields, an OIDC sign-in button, the Gameplane logo, and a feature sidebar](docs/img/login.jpg) | ![Create Server wizard step 1 showing the game template grid with icons, names, versions, and descriptions beside a YAML preview panel](docs/img/create-server-template-select.jpg) |
| Login — local account or OIDC | Create server — pick a game template |
| ![Server Events tab listing Kubernetes events for a failed server, from scheduling and image pull to CrashLoopBackOff, with All, Info, and Warnings filters](docs/img/server-detail-events.jpg) | ![Admin Settings General section with instance name, external URL, and default namespace fields beside the settings navigation](docs/img/admin-settings-general.jpg) |
| Server detail — Events (diagnosing a failed start) | Admin Settings — General |
| ![Cluster page showing three node cards with readiness, uptime, pod counts, CPU cores, and CPU and memory usage bars](docs/img/cluster-nodes.jpg) | ![Server Logs tab streaming container output with timestamps, log-level filters, a text filter, and a download button](docs/img/server-detail-logs.jpg) |
| Cluster — nodes at a glance | Server detail — Logs |
| ![Dashboard with running-server, players-online, vCPU, storage, and node tiles, a fleet status bar, cluster resource meters, recent activity, and recent backups](docs/img/dashboard.jpg) | ![Servers page listing every game server with game, status badge, players, and node placement columns beside summary tiles and filters](docs/img/servers-list.jpg) |
| Dashboard — fleet health at a glance | Servers — every game server, one list |
| ![Server detail Overview tab with CPU, memory, and disk usage tiles, recent events, connection host and port, and players online](docs/img/server-overview.jpg) | ![Mods tab registry browser showing a grid of Thunderstore mods for a Valheim server with authors and download counts](docs/img/mods-registry-browse.jpg) |
| Server detail — Overview | Mods — browsing a registry (Thunderstore) |
| ![Live console for a Minecraft server streaming startup, player join, and auto-save lines over WebSocket with Clear, Download, and Fullscreen controls](docs/img/server-console.jpg) | ![Admin Settings Mod registries section showing CurseForge and Steam Workshop configured and Nexus Mods not configured](docs/img/admin-mod-registries.jpg) |
| Console — live output over WebSocket | Admin Settings — Mod registries |

## Pre-v1 Status & Limitations

Gameplane is a **pre-v1 release** (`v0.3.0`). Core workflows — server deployment, live consoles, file management, backups/restores, game modules, multi-cluster management, and RBAC — work end-to-end and are verified by automated unit, integration, upgrade, and end-to-end test suites on every commit.

Here are a few items to keep in mind:

- **Multi-cluster access**: Pod stdout/startup logs and PTY attach use the selected Kubernetes API. An [optional private gateway](docs/multicluster-agent-gateway.md), updated operator and UID-aware agents add remote RCON, game log files, files, players, module actions and agent-based mods. The central API still needs direct Kubernetes connectivity. Capture downloads and cleanup require an upgraded gateway and sidecar with persisted file identity. Modpack and ID-list configuration use the selected Kubernetes client.
- **Idle auto-sleep & wake-on-connect**: Sleeping servers require normal game boot time when waking up. Minecraft Java and Terraria support full protocol handshake parsing to hold client connections while waking; other games use packet heuristics where players reconnect once the server is ready.
- **Relay Tunnels**: Integrated `frp`, `Tailscale`, and `playit` relays run as supervised sidecar pods. For `playit`, port-forward mappings are managed directly through your playit.gg account.
- **Production readiness**: Automated release upgrade testing runs on every PR. Disaster-recovery runbooks and fine-tuned workload resource guidance are actively being finalized (see [`docs/roadmap.md`](docs/roadmap.md)).

[`docs/roadmap.md`](docs/roadmap.md) tracks all items leading to a v1 GA release.

## Why Gameplane?

Popular panels like AMP or Pterodactyl work well for single Docker hosts. However, if you want to scale from a single homelab machine running one server to a multi-node cluster hosting dozens of game servers across a community or hosting service, traditional panels force you to change your infrastructure setup.

Gameplane uses standard Kubernetes primitives (CRDs, operators, StatefulSets, PVCs) so the exact same control plane handles everything from single-node k3s installs to large multi-node clusters seamlessly.

Gameplane is compared to Pterodactyl and CubeCoders AMP (both control panels)
and Agones (a Kubernetes operator library). While not direct competitors,
Agones is included as a reference point for teams building on Kubernetes
primitives.

Status: **pre-v1 release** (`v0.3.0`). The operator, API, agent, and dashboard
are feature-complete for the v1 scope and stabilized for external testing.

| Dimension | Gameplane | Pterodactyl | CubeCoders AMP | Agones |
|-----------|-----------|-------------|----------------|--------|
| Deployment/runtime model | Combined Kubernetes install or standalone panel on Docker Compose; game workloads use Kubernetes CRDs and a controller-runtime operator, from k3s to multi-node clusters. [BETA] [G-a](docs/comparison-sources.md#gameplane-row-a) | Self-hosted Panel with PHP/MySQL/Redis dependencies; Wings backend manages Docker containers. [P-a](docs/comparison-sources.md#pterodactyl-row-a) | Web-based control panel supporting Windows (native) and Linux (Debian 10+). [C-a](docs/comparison-sources.md#cubecoders-row-a) | Kubernetes-native library extending K8s with GameServer/Fleet CRDs. [A-a](docs/comparison-sources.md#agones-row-a) |
| Scaling & auto-sleep | Opt-in idle auto-sleep with configurable wake windows, manual wake button, or wake-on-connect. Minecraft/Terraria full protocol support; others use packet heuristics. [optional] [G-b](docs/comparison-sources.md#gameplane-row-b) | Multi-node support with cron-based power scheduling; no dedicated idle/auto-sleep feature. [P-b](docs/comparison-sources.md#pterodactyl-row-b) | Instance Automatic Sleep feature; multi-server architecture with controller managing instances. [C-b](docs/comparison-sources.md#cubecoders-row-b) | Fleet autoscaling via buffer/webhook strategies; no idle/sleep state. [A-b](docs/comparison-sources.md#agones-row-b) |
| Inbound connectivity (NAT traversal, relay) | Integrated frp, Tailscale, playit relay sidecars; playit mappings user-managed via playit.gg account. [optional; disabled by default] [G-c](docs/comparison-sources.md#gameplane-row-c) | No integrated relay sidecars; manual proxy/port forwarding configuration required. [P-c](docs/comparison-sources.md#pterodactyl-row-c) | not publicly documented (checked 2026-09-02) [C-c](docs/comparison-sources.md#cubecoders-row-c) | No relay or NAT traversal features documented. [A-c](docs/comparison-sources.md#agones-row-c) |
| Backup and restore | Restic snapshots to S3-compatible storage; on-demand or cron-scheduled via BackupSchedule; one-click restore. [G-d](docs/comparison-sources.md#gameplane-row-d) | Wings (local, default) or S3-compatible backup drivers; cron scheduling and on-demand backups. [P-d](docs/comparison-sources.md#pterodactyl-row-d) | not publicly documented (checked 2026-09-02) [C-d](docs/comparison-sources.md#cubecoders-row-d) | not applicable (Agones is a Kubernetes operator library) [A-d](docs/comparison-sources.md#agones-row-d) |
| Access control & authentication | Local argon2id + OIDC (Keycloak/Google/GitHub); three built-in roles (admin/operator/viewer); custom roles supported. [G-e](docs/comparison-sources.md#gameplane-row-e) | 2FA configurable per account or admin-only; subuser management via Artisan CLI. [P-e](docs/comparison-sources.md#pterodactyl-row-e) | Role-based access control; OIDC single-sign-on in Advanced Edition. [C-e](docs/comparison-sources.md#cubecoders-row-e) | not applicable (Agones is a Kubernetes operator library) [A-e](docs/comparison-sources.md#agones-row-e) |
| Game template distribution | OCI bundles via ModuleSource (git/http/oci/local/upload); optional cosign signature verification per source. 30 ready-to-use templates shipped. [G-f](docs/comparison-sources.md#gameplane-row-f) | Community eggs repository (eggs.pterodactyl.io) with nests and custom egg creation support. [P-f](docs/comparison-sources.md#pterodactyl-row-f) | Customizable templates framework; community-contributed templates available via external repositories. [C-f](docs/comparison-sources.md#cubecoders-row-f) | not applicable (Agones is a Kubernetes operator library) [A-f](docs/comparison-sources.md#agones-row-f) |
| Multi-tenancy & multi-cluster | Remote registration through Cluster CRDs in combined mode or SQL in standalone mode; central user grants, remote Pod logs/PTY, and optional private gateway for agent operations. UID-bound capture downloads/cleanup; modpack and ID-list configuration on the selected cluster. Requires matching gateway, operator, agent and capture-sidecar versions. [G-g](docs/comparison-sources.md#gameplane-row-g) | Single Panel managing multiple nodes; no documented remote cluster or cross-cluster streaming. [P-g](docs/comparison-sources.md#pterodactyl-row-g) | Multi-server management via controller architecture; multi-tenancy not supported in AMP 2 (planned for AMP 3). [C-g](docs/comparison-sources.md#cubecoders-row-g) | Multi-cluster allocation via GameServerAllocationPolicy; allocator service with mTLS authentication. [A-g](docs/comparison-sources.md#agones-row-g) |
| Licensing | GNU Affero General Public License v3.0 or later (AGPL-3.0-or-later). [G-h](docs/comparison-sources.md#gameplane-row-h) | MIT License (Panel and Wings). [P-h](docs/comparison-sources.md#pterodactyl-row-h) | Proprietary; per-instance tiers (Standard/Professional/Advanced/Enterprise). [C-h](docs/comparison-sources.md#cubecoders-row-h) | Apache License 2.0. [A-h](docs/comparison-sources.md#agones-row-h) |
| Target operator scope (self-hosted vs. managed SaaS) | Self-hosted only; panel runs on Docker Compose or Kubernetes, while games require Kubernetes (k3s, kubeadm, managed services); no managed SaaS offering. [G-i](docs/comparison-sources.md#gameplane-row-i) | Self-hosted only; requires Linux system capable of running Docker containers. [P-i](docs/comparison-sources.md#pterodactyl-row-i) | Self-installed on user hardware (Windows or Linux); no managed SaaS version. [C-i](docs/comparison-sources.md#cubecoders-row-i) | Self-hosted operator software; runs anywhere Kubernetes can run. [A-i](docs/comparison-sources.md#agones-row-i) |

## Features

- **Graceful Lifecycle Management**: Create, start, stop, restart, clone, and delete game servers. Stopping or restarting executes the game's native shutdown sequence (over RCON, telnet, or stdin) first, ensuring world saves complete before stopping containers.
- **Smart Idle Auto-Sleep & Wake-on-Connect**: Save hardware resources by automatically scaling empty servers down to zero after a set idle period. Servers wake up automatically on scheduled cron windows, via the dashboard **Wake** button, or when players attempt to join.
- **Built-in Relay Tunnels**: Host servers on home labs or behind CGNAT without public IPs or router port-forwarding using integrated `frp`, `Tailscale`, or `playit.gg` relay sidecars.
- **Live Console & Admin Actions**: Stream stdout/stderr in real time over WebSockets, send RCON/stdin commands, and execute custom game admin actions (like broadcast messages or give items) safely with built-in injection guards.
- **Web File Manager**: Browse, edit, upload, and download server files directly in the browser with an integrated Monaco code editor.
- **Player Management**: View active players and issue kicks or bans for supported game protocols.
- **S3 Backups & Restores**: Perform on-demand or cron-scheduled restic snapshots to S3-compatible storage, with one-click restoration into server volumes.
- **OCI Game Modules**: 30 ready-to-use game templates packaged as OCI artifacts (Minecraft Java, Valheim, Terraria, Rust, Palworld, Factorio, CS2, etc.).
- **Extensive Mod Support**: Browse and install mods across 10 registries (Modrinth, CurseForge, Steam Workshop, Thunderstore, SpigotMC, Hangar, etc.) with support for both direct file-drop and launch-parameter mod IDs.
- **Authentication & RBAC**: Local user accounts plus OIDC SSO support (Keycloak, Google, GitHub) with fine-grained access permissions.
- **Multi-Cluster Fleet Management**: Register, monitor, and manage game servers across multiple Kubernetes clusters from a single dashboard.

## Mod Management

Gameplane supports two distinct mod installation models, depending on how the game handles mods:

- **File-Drop**: The sidecar agent downloads mod files directly into dedicated plugin/mod volumes (e.g., Minecraft `mods/`/`plugins/` or Valheim BepInEx `plugins/`). All downloads are validated through `netguard` SSRF protection.
- **Mods-by-ID**: For games where the server binary fetches mods on launch (such as ARK's CurseForge `-mods=` flag, Project Zomboid, or Steam Workshop IDs), Gameplane projects selected mod IDs directly into launch environment variables.

### Supported Registries

Gameplane integrates with **10 mod registries**: Modrinth, CurseForge, Thunderstore, Hangar, Factorio Mod Portal, Steam Workshop, SpigotMC, GitHub Releases, uMod, and Nexus Mods.

- **No setup required**: Modrinth, Thunderstore, Hangar, Factorio, SpigotMC, GitHub, and uMod work out of the box.
- **API Key required**: CurseForge, Steam Workshop, and Nexus Mods require an API key configured under **Settings → Mod registries**. Combined installations store keys in Kubernetes Secrets; standalone panels encrypt them in the API database.
- **Registry Caveats**: Nexus Mods is browse-only because download links require a premium account and direct requester IP. Factorio downloads require the user's `factorio.com` credentials in the install form.

## Architecture

```text
Dashboard UI (React + TypeScript + Vite + HeroUI v3)
    │ HTTPS / WSS
Central API (Go): authentication, RBAC, REST, WebSocket, aggregation
    │
    ├── Direct Kubernetes API access ───────────────────────────────┐
    │   Resource management, Pod logs, PTY attach                   │
    │                                                              ▼
    │   ┌── Each registered cluster (local or remote) ──────────────────┐
    │   │ Kubernetes API ◄── Operator (Go, controller-runtime)          │
    │   │                      │ reconciles CRDs into local resources  │
    │   │                      ├── GameServer Pods + Services + PVCs    │
    │   │                      ├── Backup / restore Jobs               │
    │   │                      ├── Sentinel wake-on-connect Pods       │
    │   │                      └── Tunnel relay Pods                   │
    │   │                                                              │
    └───┼── Private mTLS ──► Optional per-cluster agent gateway         │
        │                      ├── mTLS ──► Agent sidecars (:8090)      │
        │                      │            RCON, logs, files, mods    │
        │                      └── mTLS ──► Capture sidecars (:9091)    │
        │                                   Capture downloads/cleanup  │
        └──────────────────────────────────────────────────────────────┘
```

In a combined installation, the central API connects directly to agents in its local cluster. Remote agent
operations use the optional gateway, which runs the API image's `gateway`
subcommand and verifies the selected GameServer identity. Direct Kubernetes
connectivity is still required for every registered cluster. Each cluster retains
its own operator and storage; users and authorization stay central. See
[gateway installation](docs/gateway-install.md) and
[remote request routing](docs/multicluster-agent-gateway.md).

A standalone panel has no `local` cluster. It stores remote registrations and
encrypted credentials in SQL and connects to the registered workload clusters.
Back up its database and `/data/panel.key` together.

### Components

| Path | Language | Description |
| ---- | -------- | ----------- |
| `agent/` | Go | Sidecar running in each game pod for RCON, file ops, PTY console, and metrics. |
| `api/` | Go | Front-end API gateway handling REST endpoints, WebSocket streaming, auth, and RBAC. |
| `api/cmd/gateway.go` | Go | Optional private per-cluster gateway (`gateway` subcommand in the API image); proxies authorized agent and capture operations over mTLS. |
| `operator/` | Go | Kubernetes controller reconciling Gameplane CRDs into K8s workloads and resources. |
| `sentinel/` | Go | Waker daemon listening on game ports while a server is sleeping to trigger wake-on-connect [optional]. |
| `capture-sidecar/` | Go | Network packet capture sidecar [optional], opt-in per server, admin-only. |
| `tunnel/` | Go | Relay supervisor pod managing third-party tunnels (`frp`, `Tailscale`, `playit`) [optional]. |
| `web/` | TS + React | Modern dashboard UI built with Vite, TanStack Query, xterm.js, and Monaco Editor. |
| `modules/` | YAML | 30 pre-packaged game templates (Minecraft, Valheim, Terraria, Rust, etc.) as OCI bundles. |
| `charts/` | Helm | Official Helm deployment chart for operator, API gateway, ingress, and helper services. |
| `gameproto/` | Go | Shared protocol library for parsing game handshakes (Minecraft, Terraria) in `sentinel`. |
| `gameaction/` | Go | Security guard and command renderer for custom module admin actions. |
| `netguard/` | Go | SSRF protection layer for outgoing mod downloads and OCI module fetches. |
| `svcutil/` | Go | Shared HTTP server lifecycle and environment configuration utilities. |
| `telemetryschema/` | Go | Shared telemetry report contract (types, strict decoder, official module catalog, report signing) used by the API and `telemetry-receiver`. |
| `audit-syslog-bridge/` | Go | HTTP-JSON to syslog relay [optional] for audit logging infrastructure. |
| `telemetry-receiver/` | Go | Collector [optional] for anonymous daily usage reports. |
| `mcp-server/` | Go | Strictly read-only Model Context Protocol server [optional] for AI tools. |

### Custom Resource Definitions (CRDs)

Gameplane extends Kubernetes using custom resources under `gameplane.local/v1alpha1`:

- **`GameTemplate`**: Reusable blueprint for a game (container image, default ports, environment variables, volume layouts, shutdown hooks, and custom admin actions).
- **`GameServer`**: An active game server instance created from a `GameTemplate` with customized resources, storage, idle auto-sleep settings, networking, relay tunnels, and backup policies.
- **`Backup`**: A one-shot restic snapshot job targeting S3-compatible storage.
- **`BackupSchedule`**: A recurring cron backup policy attached to a game server.
- **`Restore`**: A task that restores a backup snapshot into a server data volume.
- **`Module`**: An installed game bundle managed by the operator to materialize a `GameTemplate`.
- **`ModuleSource`**: A registered game template repository (OCI registry, Git repository, HTTP server, or local upload).
- **`Cluster`**: A registered remote Kubernetes cluster target for multi-cluster control plane management.

## Repository Layout

```
.
├── agent/                # Game pod sidecar (RCON, files, PTY console, metrics)
├── api/                  # REST & WebSocket API gateway
├── operator/             # Kubernetes operator (controller-runtime)
│   ├── api/v1alpha1/     # CRD Go type definitions
│   ├── internal/controller/
│   └── config/           # CRD manifests and RBAC rules
├── sentinel/             # Wake-on-connect daemon (Go)
├── tunnel/               # Relay client supervisor for frp/Tailscale/playit (Go)
├── web/                  # Dashboard frontend (React, Vite, Monaco, xterm.js)
├── modules/              # Submodule: Game templates (30 games shipped)
├── website/              # Submodule: Public documentation site
├── gameproto/            # Wire-protocol parsing library (Minecraft, Terraria)
├── gameaction/           # Console injection guard & action command renderer
├── netguard/             # SSRF dial-guard library
├── svcutil/              # Shared HTTP server & env utilities
├── telemetryschema/      # Shared telemetry report contract (types, decoder, signing)
├── audit-syslog-bridge/  # Optional syslog relay for audit logs
├── telemetry-receiver/   # Optional anonymous usage telemetry receiver
├── mcp-server/           # Optional read-only Model Context Protocol server
├── charts/gameplane/     # Helm deployment chart
├── deploy/kind/          # Local dev environment bootstrap scripts
├── deploy/standalone/    # API + dashboard Docker Compose deployment
├── docs/                 # Technical documentation & guides
├── specs/                # Feature specifications and proposals
└── test/                 # Integration & end-to-end kind test suites
```

## Installation

Choose where the panel and game workloads run:

| Installation | Panel host | Game workloads | Guide |
|---|---|---|---|
| Combined Helm install | Kubernetes | Local and registered remote clusters | [Combined installation](docs/install.md) |
| Standalone panel | Docker Compose, or a panel-only Helm release | Registered remote Kubernetes clusters | [Standalone setup](docs/standalone-panel.md) |
| Remote operator and optional gateway | Central panel runs elsewhere | This Kubernetes cluster | [Remote cluster setup](docs/standalone-panel.md#install-and-register-remote-game-clusters) |

Standalone support requires a checkout or release containing this feature. The
guide builds the Compose images from that checkout and covers admin setup,
HTTPS access, remote registration, permissions, and database/key backups.

For the default combined installation, use Helm:

The Helm chart and component images are published to the GitHub Container
Registry as OCI artifacts — no `helm repo add` required:

```sh
helm upgrade --install gameplane oci://ghcr.io/gameplanepanel/charts/gameplane \
  --version <version> \
  --namespace gameplane-system --create-namespace \
  --set ingress.host=gameplane.your-domain.test
```

The chart pins matching `ghcr.io/gameplanepanel/gameplane/{operator,api,agent}`
images by `appVersion`. To track the rolling edge build instead of a tagged release,
add `--set image.tag=edge`. Then seed an admin user and log in — see
[`docs/install.md`](docs/install.md) for the full flow, OIDC, Postgres, and
values reference. For address pool configuration (pinning servers to specific
public IP addresses), see [`docs/networking.md`](docs/networking.md).

All published images, the Helm chart, and official module bundles are signed
with the project's cosign key ([`cosign.pub`](cosign.pub), also baked into
the chart for module verification) and recorded in the public Sigstore Rekor
transparency log:

```sh
cosign verify --key cosign.pub \
  ghcr.io/gameplanepanel/gameplane/operator:<version>
```

Pre-rotation releases (v0.2.0-beta.7 and earlier) were signed with the retired <!-- doc-versions: historical -->
Ed25519 key and do not have transparency log entries — verify them with
`cosign-legacy.pub` and `--insecure-ignore-tlog=true`. See
[`docs/key-rotation.md`](docs/key-rotation.md) for details.

## Quickstart (local dev)

Requires: Go 1.26+, Node 20+, Docker, kind, kubectl, helm,
[oras](https://oras.land/docs/installation) (>= 1.2.0).

The game modules live in the separate `GameplanePanel/module` repo, wired in here
as the `modules/` submodule — clone with submodules (or initialize them after):

```sh
git clone --recurse-submodules <repo-url>
# already cloned? populate the submodule:
git submodule update --init
```

```sh
# spin up a local kind cluster with Gameplane preinstalled
make dev-up

# in another shell, run the web app against the in-cluster API
make web-dev

# tear down
make dev-down
```

The `make dev-up` target:

1. creates a kind cluster from `deploy/kind/cluster.yaml` and a local
   OCI registry at `localhost:5001` (reachable from cluster pods as
   `kind-registry:5000`),
2. loads every locally-built image (operator, api, web, agent, and every
   optional component's image),
3. pushes every directory under `modules/` (30 games at last count — see
   `modules/` for the current list) to the local registry as an OCI module
   bundle,
4. installs the Helm chart from `charts/gameplane/` with a default
   `ModuleSource` pointing at the local registry — the operator
   indexes it within seconds and the modules show up in the dashboard's
   Modules page.

See [`docs/module-authoring.md`](docs/module-authoring.md) for the
bundle format and how to author additional modules.

## Contributing

Design changes go through `design.pen` (Pencil) before any UI code is
written. See [`docs/contributing.md`](docs/contributing.md) for the
full guide: code style, test tiers, and the PR process.

## License

[AGPL-3.0-or-later](./LICENSE). Any network-accessible deployment of a
modified version must make its source available to users.
