# Nuclear Option Module Contract

**Feature**: 002-nuclear-option-ip-pool  
**Phase**: 1 (Contracts)  
**Date**: 2026-08-21  
**Status**: Proposal for Code Review  

This document specifies the contents and structure of the `modules/nuclear-option/` game module bundle (spec FR-001 to FR-003, FR-013, FR-023, FR-027). The module is a deployable game template distributed as an OCI artifact.

---

## Overview

The Nuclear Option module is a directory within the `gameplane-module` repository (checked out as the `modules/` git submodule in this repo). The module contains:
- `module.yaml` — bundle metadata
- `template.yaml` — Kubernetes GameTemplate spec (game configuration, ports, RCON, capabilities)
- `README.md` — operator-facing documentation
- `icon.png` (optional) — 256×256 brand icon for the catalog
- `specs.md` — detailed module-specific documentation (per spec FR-027)

### Repository Structure

```
modules/nuclear-option/
├── module.yaml         # required
├── template.yaml       # required
├── README.md           # required
├── specs.md            # required (per FR-027)
└── icon.png            # optional
```

**Module Location**: The `modules/` directory is a git submodule pointing to the separate `gameplane-module` repository. Changes to module files are committed in that repository, and the submodule pointer is bumped in this (main) repository. After a fresh clone of this repo, run `git submodule update --init` to initialize `modules/`.

---

## module.yaml

The module metadata file declares the module's identity, version, and display properties. See `docs/module-authoring.md`, § "module.yaml schema" (lines 49–92).

### Required Fields

```yaml
apiVersion: gameplane.local/module/v1
name: nuclear-option
displayName: Nuclear Option
version: 1.0.0
game: nuclear-option
categories: [Shooter, PvP]
summary: Squad-based RTS multiplayer action
homepage: https://store.steampowered.com/app/2168680/Nuclear_Option/
license: Proprietary
icon: icon.png
```

**Field Details**:
- `name`: Canonical module identifier (DNS-1123 label: lowercase alphanumerics and hyphens). Must match the directory name.
- `displayName`: Human-readable name shown in the catalog.
- `version`: Semantic version; MUST match the OCI tag this bundle is pushed under.
- `game`: Free-form game family identifier (mirrors the module name; used for grouping related modules).
- `categories`: List from the official canon: `Shooter`, `PvP`, `Simulation`, etc. (see `docs/module-authoring.md` line 79 for the full list). "Shooter" and "PvP" are appropriate for Nuclear Option.
- `summary`: One-line catalog card description (≤ 100 chars recommended).
- `homepage`: Link to the game's Steam store page or official site.
- `license`: SPDX identifier or "Proprietary" (Nuclear Option is proprietary to Shockfront Studios).
- `icon`: Filename of the icon layer (relative to this directory). Typically `icon.png`.

---

## template.yaml

The template file is a Kubernetes `GameTemplate` CRD spec (no metadata.name — the name is assigned from `module.yaml#name` on install). It declares resource defaults, ports, RCON configuration, and operator capabilities.

**Location**: `operator/api/v1alpha1/gametemplate_types.go` (reference for all `spec` fields)  
**Example**: `modules/minecraft-java/template.yaml` (canonical reference for layout and conventions)  
**Authoring Guide**: `docs/module-authoring.md`, § "Anatomy of a GameTemplate spec" (lines 379+)

### Required Sections

#### Branding

```yaml
spec:
  displayName: Nuclear Option
  game: nuclear-option
  version: 1.0.0
  categories: [Shooter, PvP]
  accentColor: "#4a90e2"  # brand blue, as CSS hex
  description: |
    Squad-based RTS multiplayer action. The dedicated server requires
    2–4 CPU cores and 8–16 GB RAM. Join up to 64 players per mission.
```

#### Image (Container Runtime)

```yaml
spec:
  image: nuclear-option-dedicated:1.0.0@sha256:abc123...
```

**Requirements** (spec FR-002):
- Must be a public OCI image URI, pinned by digest (SHA256).
- Must be a native Linux binary (no Proton/WINE compatibility layer).
- Must be the dedicated server binary (app ID 3930080), not the base game.
- Do not use a floating tag (`:latest`, `:stable`); digest pin is mandatory (enforced by `validate.py`).

#### Resource Defaults

```yaml
spec:
  resources:
    requests:
      cpu: 2
      memory: 8Gi
    limits:
      cpu: "4"
      memory: 16Gi

  storage:
    size: 30Gi
    mountPath: /game
```

**Specification** (spec FR-002):
- **CPU**: 2–4 cores (request 2, limit 4). CPU-bound workload; actual usage depends on player count.
- **Memory**: Minimum 8 GB (request), recommended 16 GB (limit). 8 GB can run smaller matches; 16 GB is recommended for the full 64 players.
- **Storage**: 30 GB minimum for the game binary, mission data, and logs. (Actual size depends on mission count and log retention.)

#### Ports

```yaml
spec:
  ports:
    - name: game
      containerPort: 7777
      protocol: UDP
      advertise: true
    
    - name: query
      containerPort: 7778
      protocol: UDP
      advertise: false
    
    - name: remote-command
      containerPort: 7779
      protocol: TCP
      advertise: false  # CRITICAL: must NOT be advertised
```

**Specification** (spec FR-023, FR-025):
- **Game Port (7777)**: UDP, primary player-join port. Advertised externally (LoadBalancer, NodePort).
- **Query Port (7778)**: UDP, server-list-ping (status queries from server browsers). Advertised externally.
- **Remote-Command Port (7779)**: TCP, operator console for moderation. **MUST NOT be advertised** (internal only). Set `advertise: false`.
- **Protocol Verification**: These port numbers are assumed from third-party documentation (spec Verification Required Before Implementation, Claim 2). They MUST be confirmed against a real running server before the module ships.

**Wake-on-Connect** (spec FR-023):
- **OUT OF SCOPE FOR v1**: Wake-on-connect is not enabled for this module in v1. No `wakeProtocol` field is declared on the game port; the sentinel falls back to its generic UDP packets-in-window heuristic for detecting join attempts while the server is asleep. A dedicated `gameproto/nuclearoption.go` handshake parser is deferred to a future feature.

#### Console Configuration (RCON)

```yaml
spec:
  rcon:
    protocol: nuclearoption
    port: 7779
    passwordEnv: RCON_PASSWORD  # or passwordFile if password is game-managed
```

**Specification** (spec FR-006):
- `protocol: nuclearoption` identifies the length-prefixed JSON protocol (see `nuclear-option-remote-command.md`).
- `port: 7779` matches the remote-command port declared above.
- `passwordEnv: RCON_PASSWORD`: The operator generates a password into a Secret and injects it as an env var. The agent reads this env var and uses it (if required by the protocol).
- **CRITICAL — No Authentication**: The remote-command protocol defines **NO authentication mechanism, NO password validation, and NO handshake**. The port MUST NOT be advertised externally and MUST ONLY be reached by the agent sidecar over pod-local loopback (localhost/127.0.0.1). Any network exposure of this port allows unauthenticated remote command execution.

#### Game Configuration Schema

```yaml
spec:
  configSchema:
    - name: SERVER_NAME
      displayName: Server name
      type: string
      required: true
      maxLength: 64
      default: "Nuclear Option Server"
    
    - name: MAX_PLAYERS
      displayName: Max players
      type: int
      required: true
      minimum: 4
      maximum: 64
      default: "16"
    
    - name: SERVER_PASSWORD
      displayName: Server password
      type: password
      required: false
      default: ""
    
    - name: MISSION_ROTATION
      displayName: Mission rotation mode
      type: enum
      enum: [sequence, random]
      default: sequence
    
    - name: MISSION_LIST
      displayName: Initial mission list
      type: string
      required: true
      default: "Escalation,Terminal Control"  # comma-separated mission names
      description: "Comma-separated list of mission names from the game's available missions"
```

**Specification** (spec FR-003, FR-023):
- All fields are validated and rendered as env vars or config-file entries before the server starts.
- `SERVER_NAME`: String, max 64 chars (reasonable limit to prevent buffer overflows or config file corruption).
- `MAX_PLAYERS`: Integer, 4–64 (typical range; exact limits confirmed with real server).
- `SERVER_PASSWORD`: Password type (never appears in pod spec; stored in a Secret and injected via `SecretKeyRef`).
- `MISSION_ROTATION`: Enum, either sequential or random mission selection.
- `MISSION_LIST`: Comma-separated mission names. Validated against the game's list of available missions (if possible).
- Invalid configuration (unsupported mission name, out-of-range player limit, malformed JSON) is detected at validation time and reported as a server-creation error (spec FR-023).

#### Config Files

If the game reads configuration from a file (rather than env vars), use `configFiles`:

```yaml
spec:
  configFiles:
    - name: DedicatedServerConfig.json
      path: /game/config/DedicatedServerConfig.json
      template: |
        {
          "ServerName": "{{.Config.SERVER_NAME}}",
          "MaxPlayers": {{.Config.MAX_PLAYERS}},
          "Password": "{{.Config.SERVER_PASSWORD | default \"\"}}",
          "MissionRotation": "{{.Config.MISSION_ROTATION}}",
          "InitialMissions": "{{.Config.MISSION_LIST}}"
        }
```

**Specification** (spec FR-003):
- The template is a Go `text/template` rendered with `.Config` (resolved config values).
- File is written to the container before the game starts.
- Template syntax allows conditionals (`{{if}}`) and defaults (`{{default}}`).

#### Capabilities

```yaml
spec:
  capabilities:
    # Player management (FR-007, FR-009, FR-010)
    players:
      kick:
        command: 'kick-player "{{.Player}}"'
      ban:
        command: 'banlist-add "{{.Player}}" "{{.Reason | default \"\" }}"'
      unban:
        command: 'banlist-remove "{{.Player}}"'
    
    # Readiness / startup probe
    readinessProbe:
      exec:
        command:
          - sh
          - -c
          - grep -q "Waiting for Players before loading next map" /game/logs/server.log
      initialDelaySeconds: 60
      periodSeconds: 10
      timeoutSeconds: 5
    
    # Lifecycle / graceful stop
    lifecycle:
      stop:
        command: update-ready  # if the game supports a graceful shutdown command
    
    # Quiesce before backup (FR-013)
    quiesce:
      exec:
        - command: update-ready
      # UNVERIFIED: the exact quiesce sequence is not documented upstream;
      # confirm with real server before implementation.
    
    # Actions / moderation (FR-007 to FR-012)
    actions:
      - id: send-chat-message
        displayName: Broadcast message
        description: Send a message to all players
        command: 'send-chat-message "{{.Params.message}}"'
        params:
          - name: message
            displayName: Message text
            type: string
            required: true
      
      - id: set-mission
        displayName: Change next mission
        description: Set the next mission to load
        command: 'set-next-mission "{{.Params.group}}" "{{.Params.name}}" "3600.0"'
        params:
          - name: group
            displayName: Mission group
            type: string
            default: "BuiltIn"
          - name: name
            displayName: Mission name
            type: string
            required: true
```

**Specification** (spec FR-007–FR-012, FR-023):
- `players.kick`: Calls `kick-player` command with Steam ID.
- `players.ban`: Calls `banlist-add` command with Steam ID and optional reason.
- `players.unban`: Calls `banlist-remove` command with Steam ID.
- `readinessProbe`: Detects when the server is ready to accept players (spec Assumption: log line `"Waiting for Players before loading next map"` signals readiness). **UNVERIFIED** — must be confirmed against a real running server.
- `lifecycle.stop`: **UNVERIFIED** — confirm the graceful-stop sequence with the publisher.
- `quiesce`: Backup pause/resume; **UNVERIFIED** — must be confirmed.
- `actions`: Operator-initiated commands like broadcast message and mission change. Templates use remote-command protocol identifiers.

#### Log Path

```yaml
spec:
  logPath: /game/logs/server.log
```

**Specification** (spec Assumption, Claim 5):
- Agent tails this file for the "Game log" view in the dashboard.
- **UNVERIFIED** — actual location must be confirmed against a real running server.

### Optional Sections

#### Version Catalog

If the game has multiple release channels (e.g., stable vs. experimental):

```yaml
spec:
  versions:
    - id: "1.0.0-stable"
      displayName: "1.0.0 (Stable)"
      image: nuclear-option-dedicated:1.0.0@sha256:abc123...
      default: true
    
    - id: "1.1.0-preview"
      displayName: "1.1.0 (Preview)"
      image: nuclear-option-dedicated:1.1.0-preview@sha256:def456...
      gameplane:floating  # optional: mark as floating tag
```

Not required for v1 if only one stable release is available.

---

## README.md

Operator-facing documentation rendered in the catalog detail drawer.

**Specification** (spec FR-027):
- Describe server setup, hardware requirements, network topology, and known limitations.
- Include mission names and availability.
- Document any manual steps (e.g., in-game admin claims).
- Link to Gameplane operator guides and the module's `specs.md`.

**Content Outline**:
```markdown
# Nuclear Option

Squad-based RTS multiplayer action. Supports up to 64 players per mission.

## Requirements

- **CPU**: 2–4 cores
- **Memory**: 8 GB minimum, 16 GB recommended
- **Storage**: 30 GB
- **Network**: UDP ports 7777 (game), 7778 (query); TCP 7779 (internal console)

## Configuration

### Server name
Sets the name displayed in server browsers.

### Max players
Player limit per match (4–64).

### Password
Optional server-join password.

### Mission rotation
Sequential or random mission selection.

### Mission list
Comma-separated names of missions to include in the rotation.

## Available Missions

- Escalation
- Terminal Control
- [... confirm full list from publisher ...]

## Console Commands

Remote moderation via the console:
- `get-player-list` — list connected players
- `kick-player <steamid>` — remove a player
- `banlist-add <steamid>` — ban a player
- `send-chat-message <text>` — broadcast to all players
- `set-next-mission <group> <name> <time>` — change the next mission

See the [full command reference](specs.md#remote-console-commands).

## Backups

Automatic backups include server config, mission progress, and ban lists. Restore via the dashboard.

## Troubleshooting

...
```

---

## specs.md

Per-module technical documentation (spec FR-027). Not rendered in the catalog but linked from the README and visible in the repository.

**Specification** (spec FR-027):
- Detailed protocol documentation (remote-command port, status codes, command reference).
- Resource usage under various player counts.
- Known limitations (e.g., spectator mode, mod support).
- Upgrade notes (binary changes, mission list changes).
- Integration points (how the operator's RCON and agent interact with the game).

**Location**: `modules/nuclear-option/specs.md`

**Content Outline**:
```markdown
# Nuclear Option — Technical Specification

## Network Ports

- UDP 7777: Game join port
- UDP 7778: Server-list query port
- TCP 7779: Remote-command console (internal only)

## Remote Console Commands

[Full command reference from nuclear-option-remote-command.md contract]

## Status Reporting

Server readiness is detected via the log line: "Waiting for Players before loading next map"

## Resource Usage

- Idle: ~2–4 GB RAM, <1 core
- 16 players: ~8–10 GB RAM, 1–2 cores
- 64 players: ~14–16 GB RAM, 3–4 cores

## Known Limitations

- Display names are not cached; Steam Web API lookup required for player list rendering.
- Missions must be from the game's built-in set (no custom mission support).

...
```

---

## icon.png

Optional visual branding asset.

**Specification**:
- **Size**: 256×256 pixels recommended.
- **Format**: PNG with transparency.
- **Content**: Game logo or brand icon.
- **License**: Must be redistributable with the module (either published by Shockfront Studios or licensed permissively).

If not included, the dashboard uses a generic game icon.

---

## Bundle Assembly & Distribution

### OCI Artifact

The module is assembled and distributed as an OCI artifact. The `modules/build.sh` script handles the assembly:

```sh
oras push \
  --artifact-type application/vnd.gameplane.module.v1+json \
  ghcr.io/gameplanepanel/gameplane-modules/nuclear-option:1.0.0 \
  module.yaml:application/vnd.gameplane.module.metadata.v1+yaml \
  template.yaml:application/vnd.gameplane.module.template.v1+yaml \
  README.md:application/vnd.gameplane.module.readme.v1+md \
  icon.png:image/png
```

### Push & Install

```bash
# From the repo root:
make modules-push REGISTRY=ghcr.io/gameplanepanel/gameplane-modules

# Or for a single module:
modules/build.sh push --registry ghcr.io/gameplanepanel/gameplane-modules --name nuclear-option
```

The operator discovers the module via a `ModuleSource` (default: `ghcr.io/gameplanepanel/gameplane-modules`) and users install it from the Modules catalog.

---

## Validation & Testing

### Static Validation

`validate.py` (in the `gameplane-module` repo) checks:
- `module.yaml` schema (required fields, version semver).
- `template.yaml` schema (GameTemplate CRD compatibility).
- Image digests are pinned (no floating tags on required images).
- Port declarations match the protocol.
- Capabilities (actions, console commands) reference valid RCON protocols.

**Validation must pass before the module is committed to the `gameplane-module` repo.**

### Protocol Allowlist

The module repo's `validate.py` maintains a `RCON_PROTOCOLS` allowlist. When `template.yaml` declares `rcon.protocol: nuclearoption`, the validator checks that `nuclearoption` is in the allowlist.

**Action Required**: Update `gameplane-module` repo's `validate.py` to add `"nuclearoption"` to `RCON_PROTOCOLS` once the agent's implementation is complete.

### E2E Testing

A real protocol-level join test must be authored in `test/e2e/` and bucketed per `test/e2e/buckets.sh`:

- **Test Goal**: Confirm a real game client can join a running Nuclear Option server and stay connected for ≥ 30 seconds.
- **Prerequisites**: A working game binary, join handshake implementation (`gameproto/nuclearoption.go`), and network access to the server's UDP 7777 port.
- **Status**: Per spec FR-004 & FR-005, the module's join-coverage status must be recorded in `docs/game-coverage.md` (covered-in-ci, covered-deferred, blocked-doc, or out-of-scope-by-design).

---

## Unverified Assumptions

The following assumptions about Nuclear Option's dedicated server must be confirmed before implementation:

| Assumption | Evidence Required | Fallback |
|---|---|---|
| Dedicated server app (3930080) is publicly downloadable without owning the base game. | Proof of download access; confirmation of native Linux binary. | Module cannot ship; licensing blocker documented. |
| Game port is UDP 7777; query port is UDP 7778. | Netstat output from a running server; confirmation of reachable ports. | Update template.yaml with real ports; update docs. |
| Remote-command protocol is length-prefixed JSON on TCP 7779 (per third-party docs). | Packet capture or live-server testing; confirm all 19 command names, status codes, and response formats. | Update nuclear-option-remote-command.md contract; mark affected FR as blocked-doc. |
| Server logs a line like "Waiting for Players before loading next map" on readiness. | Observation of real server boot sequence; identify exact log line. | Use a real-protocol probe (e2e join) as readiness signal; increase readiness latency. |
| Log files are accessible under `/game/logs/server.log` or similar. | Real server instance; confirm log locations and formats. | Document log location limitation; note reduced operator visibility. |

---

## File References

- **Module Authoring Guide**: `docs/module-authoring.md`
- **Module Example (Minecraft)**: `modules/minecraft-java/template.yaml`
- **CRD Types**: `operator/api/v1alpha1/gametemplate_types.go`
- **Module Repository**: `gameplane-module` repo (checked out as `modules/` submodule)
- **Build Script**: `modules/build.sh`
- **Validation Script**: `gameplane-module/validate.py` (in the submodule)
- **Join-Coverage Registry**: `docs/game-coverage.md`
- **E2E Test Buckets**: `test/e2e/buckets.sh`
