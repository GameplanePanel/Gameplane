# Feature Specification: Dedicated Server Modules for Top Steam Games

**Feature Branch**: `015-top-steam-game-modules`

**Created**: 2026-09-03

**Status**: Draft

**Input**: User description: "Based on the current top 100 most played games listed on SteamDB, here are the games that officially support user-hosted dedicated servers (allowing players or communities to run their own independent server instances): Counter-Strike 2 (Rank 1), Palworld (Rank 4), FiveM (Rank 5 - Multiplayer framework for GTA V), Rust (Rank 7), Project Zomboid (Rank 9), Team Fortress 2 (Rank 25), DayZ (Rank 31), Farming Simulator 25 (Rank 37), Euro Truck Simulator 2 (Rank 41), Garry's Mod (Rank 43), Mount & Blade II: Bannerlord (Rank 48), Terraria (Rank 49), 7 Days to Die (Rank 54), tModLoader (Rank 57 - Terraria modding framework), BeamNG.drive (Rank 59 - via the popular BeamMP multiplayer mod), ARK: Survival Ascended (Rank 64), Left 4 Dead 2 (Rank 71), Factorio (Rank 72), Don't Starve Together (Rank 74), Valheim (Rank 75), Satisfactory (Rank 76), The Isle (Rank 79), ARK: Survival Evolved (Rank 84), Arma Reforger (Rank 90), Hell Let Loose: Vietnam (Rank 97), Squad (Rank 98). Add support for those as module"

## Clarifications

### Session 2026-09-03

- Q: How should Gameplane handle auxiliary dependencies (such as external databases for FiveM or secondary web administration portals for Farming Simulator 25 and txAdmin) within these game modules? → A: Option B (All-in-One Bundles: Include lightweight auxiliary services such as embedded database processes and companion web interfaces within the container runtime for single-pod zero-dependency deployment).
- Q: Should module #25 target the standard official Hell Let Loose dedicated server (Steam AppID 731790) with support for community configurations/maps, or a specific Vietnam modded variant? → A: Option C (Both Variants: Provide standard Hell Let Loose as the default template version with an optional version profile / preset for Vietnam community conversions).
- Q: How should game modules that require external authorization keys or master server registration tokens (such as FiveM CFX keys, BeamMP auth keys, and ETS2 server tokens) behave when launched without a configured token? → A: Option A (Graceful Idle with Diagnostics: Containers missing mandatory registration tokens log a clear step-by-step diagnostic message for obtaining and setting the key, and idle cleanly without crash-looping).

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - One-Click Deployment for Top Steam Multiplayer Dedicated Servers (Priority: P1)

Game server administrators, gaming community operators, and homelab enthusiasts want to instantly provision dedicated game servers for the top multiplayer games on Steam without manually writing container specifications, calculating port collisions, or figuring out game-specific storage permissions. An administrator selects any game from the top 100 Steam list (such as Team Fortress 2, FiveM, Squad, Farming Simulator 25, Euro Truck Simulator 2, BeamMP, Arma Reforger, or Left 4 Dead 2) via the Gameplane catalog or CLI, specifies initial parameters (server name, player limits, passwords, game mode), and deploys a fully functional server instance.

**Why this priority**: The fundamental value proposition of Gameplane is turnkey orchestration of game servers. Expanding the first-party module catalog to cover the top multiplayer games on Steam directly satisfies the most popular community hosting demands.

**Independent Test**: Can be tested independently by selecting any of the 26 supported game modules, instantiating a server deployment with default parameters, and verifying that the server boots into a healthy, ready-to-accept-players state with correct network port exposure and storage persistence.

**Acceptance Scenarios**:

1. **Given** an operator deploying a newly added module (e.g., `team-fortress-2`, `fivem`, `squad`, `beammp`, `left-4-dead-2`, `arma-reforger`, `farming-simulator-25`), **When** the instance is created from the template, **Then** the server starts up cleanly, binds to its designated game, query, and administrative ports, and completes initialization without permission errors or boot crashes.
2. **Given** any existing module in the catalog (e.g., `cs2`, `palworld`, `rust`, `project-zomboid`, `dayz`, `garrys-mod`, `terraria`, `7-days-to-die`, `ark-survival-ascended`, `factorio`, `dont-starve-together`, `valheim`, `satisfactory`), **When** verified against the top 100 standard requirements, **Then** it presents standard metadata, valid digest-pinned images, accurate resource reservations, and valid schema definitions.
3. **Given** games with non-standard runtime requirements (e.g., Wine/Proton execution for Windows binaries like Farming Simulator 25, or custom authentication tokens for FiveM, BeamMP, DST, ETS2), **When** deployed with required environment configuration, **Then** the initialization pipeline handles prerequisite checks and guides the administrator with actionable diagnostics if a required token is missing.
4. **Given** modules requiring auxiliary local services (e.g., embedded database for FiveM or companion web manager for txAdmin/FS25), **When** launched, **Then** the runtime supervises both the auxiliary services and the dedicated server process within the single pod container with appropriate port declarations.

---

### User Story 2 - Persistent Game State, World Saves, and Multi-Shard Clustering (Priority: P1)

Server operators need durable persistence of savegames, world files, player inventories, and server configuration files across container restarts, image upgrades, and node migrations. For complex multi-instance games (such as Don't Starve Together master/caves shards or ARK cluster travel networks), operators need straightforward volume layouts and shared storage topologies that preserve game state integrity.

**Why this priority**: Game servers hold valuable player progress and world builds; data loss or corruption on container restart destroys community trust.

**Independent Test**: Can be tested independently by starting a game server, generating in-game state changes (e.g., world generation, save creation, player configuration edits), restarting the container instance or upgrading its image version, and verifying that all world data, configs, and player saves are fully preserved and loaded without corruption.

**Acceptance Scenarios**:

1. **Given** a running game server instance with persistent storage mounted, **When** the container is restarted or recreated, **Then** the game resumes from the existing save files without resetting to initial world generation.
2. **Given** persistent storage volumes mounted to non-root game processes (e.g., UID 1000/10000), **When** files are created or modified during server runtime, **Then** ownership permissions remain consistent and do not cause file lockups or permission denial.
3. **Given** cluster-capable game servers (e.g., Don't Starve Together master and cave shards, or ARK cluster hubs), **When** configured to communicate over shared volumes or network tokens, **Then** cross-shard synchronization and player transfer state remain consistent.

---

### User Story 3 - Interactive Administration, Remote Console (RCON/API), and Operational Actions (Priority: P2)

Community moderators and server managers need real-time operational control to monitor active player counts, broadcast announcements, execute administrative commands (kicking, banning, changing maps/modes), and initiate safe shutdown sequences that trigger in-game world saves before process termination.

**Why this priority**: Operational control and graceful lifecycle management prevent world rollback and give server hosts the tools required to manage live multiplayer communities.

**Independent Test**: Can be tested independently by issuing administrative commands via the supported control protocol (Source RCON, BattlEye RCON, WebSocket RCON, REST API, or stdin CLI) to an active server, verifying expected responses, and triggering a stop action to observe the pre-shutdown save command sequence.

**Acceptance Scenarios**:

1. **Given** a running game server configured with remote management (e.g., Source RCON for CS2/TF2/L4D2/GMod/Squad/HLL, REST API for Palworld/Satisfactory, WebSockets for Rust, Telnet/Web for 7 Days to Die), **When** an administrator issues an administrative command from the console, **Then** the command is processed by the game engine and the output is streamed back in real time.
2. **Given** a running server receiving a stop/terminate signal, **When** the graceful shutdown sequence executes, **Then** the platform issues the game-appropriate save command (e.g., `save`, `server.save`, `/save-all`, `saveworld`) before stopping the container process, preventing rollback.
3. **Given** an administrator scheduling maintenance or restarts, **When** invoking the broadcast action, **Then** in-game warning messages are displayed to active players with configurable countdown timers.

---

### User Story 4 - Automated Modding, Frameworks, and Workshop Synchronization (Priority: P2)

Community server owners heavily customize their servers using mods, custom maps, frameworks, and plugins (e.g., Steam Workshop collections for GMod/Squad/DayZ/TF2, Metamod/CounterStrikeSharp for CS2, tModLoader for Terraria, BeamMP custom vehicles/maps, BepInEx for Valheim, txAdmin/CFX resources for FiveM). Administrators need clear module structures and designated volume mounts to install, update, and manage mods without hand-editing container entrypoints.

**Why this priority**: A large majority of dedicated multiplayer servers run mods or custom frameworks. Built-in support for mod loaders and workshop collections turns complex multi-hour installations into minutes of configuration.

**Independent Test**: Can be tested independently by deploying a modded framework module (e.g., `tmodloader`, `fivem`, `beammp`, or `garrys-mod` with a workshop collection ID), booting the server, and verifying that the specified mods or resources are downloaded, mounted, and loaded by the game engine.

**Acceptance Scenarios**:

1. **Given** a game server module with Steam Workshop support (e.g., DayZ, Squad, Garry's Mod, Arma Reforger, Project Zomboid), **When** the operator supplies a workshop collection ID or mod list, **Then** the server downloads and activates the mods during startup.
2. **Given** dedicated modding framework modules (such as `tmodloader`, `fivem`, `beammp`), **When** deployed, **Then** they expose dedicated resource directories, plugin paths, and configuration files matching the framework's standard layout.
3. **Given** binary/bytecode mod loaders (e.g., Metamod/SourceMod for Valve engines, BepInEx for Unity games), **When** enabled, **Then** addon hooks and configuration directories are mounted in isolated, non-shadowed paths.

---

### User Story 5 - Automated Health Probing & Protocol Verification (Priority: P3)

Platform monitoring systems and cluster operators need continuous, accurate health status for every running game server. A game server must not report "Ready" merely because a container is running; it must be verified as actively listening on its protocol port, initialized, and capable of accepting player connections.

**Why this priority**: Prevents routing traffic to hanging, crashing, or still-initializing game servers, ensuring high availability and accurate dashboard metrics.

**Independent Test**: Can be tested independently by querying the running server using its specific query protocol (e.g., A2S_INFO for Source/Enfusion/Unreal, game-specific ping packets for Factorio/Terraria/Minecraft, HTTP health checks for REST-based servers) and observing status transitions between Initializing, Ready, Unhealthy, and Stopped.

**Acceptance Scenarios**:

1. **Given** a booted game server undergoing initial world generation or asset caching, **When** probed by the health monitoring system, **Then** its status remains `Starting`/`Initializing` until the game engine signals readiness on its query or game port.
2. **Given** an active, healthy game server, **When** probed via its native protocol (or hand-rolled protocol join client), **Then** the probe succeeds and returns real-time metrics (player counts, max players, current map/mission, game version).
3. **Given** a frozen or crashed game server process that still has a running container PID, **When** protocol probes fail across the configured timeout threshold, **Then** the instance is marked `Unhealthy` and triggers automated recovery according to policy.

---

### Edge Cases

- **SteamCMD Anonymous vs. Authenticated Login**: Some dedicated servers (e.g., Arma Reforger or specific mod downloads) require a valid Steam account with game ownership, while most dedicated servers (e.g., CS2, TF2, Rust, Valheim, Palworld) install via anonymous login. The module templates must clearly configure authentication credentials or default to anonymous safely.
- **Resource-Heavy Engines and GitHub Actions Runner Constraints**: Large game servers (e.g., ARK: Survival Ascended, Squad, Arma Reforger, DayZ) require upwards of 8–16 GB RAM and substantial CPU to initialize, which exceeds standard CI runner limits. Per Gameplane Constitution Principle I, their end-to-end tests must be written and committed, but annotated with explicit CI bucket exclusion reasons for on-demand/heavy execution.
- **Windows-Only Server Binaries on Linux Host (Wine/Proton Layer)**: Games without native Linux server binaries (e.g., Farming Simulator 25) require a headless Wine/Proton compatibility layer. Mount points, X11/dummy display drivers, and Windows paths must be cleanly isolated and normalized.
- **Dynamic Port Allocations and Multi-Port Range Mappings**: Certain engines (e.g., Project Zomboid requiring port 16261 UDP plus per-player direct-connect ports 16262–16272, or Rust needing Game + RCON + Rust+ App ports) require multiple port mappings across UDP and TCP. Templates must cleanly specify default ports and avoid port collision.
- **Non-Root Containers Shadowing Entrypoint Paths**: Module storage paths must never mount over the container's baked-in launch scripts or binaries (preventing the empty-PVC shadowing defect).
- **UID/GID and SteamCMD `$HOME` Environment Invariants**: Containers running as non-root UIDs (e.g., 1000, 10000, 25000) must declare `security.runAsUser`, `security.fsGroup`, and explicit `HOME` environment variables so SteamCMD and game binaries can resolve user configuration paths without crashing.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide standardized, validated module definitions for all 26 top Steam multiplayer games supporting dedicated servers:
  1. Counter-Strike 2 (`cs2`)
  2. Palworld (`palworld`)
  3. FiveM (`fivem`)
  4. Rust (`rust`)
  5. Project Zomboid (`project-zomboid`)
  6. Team Fortress 2 (`team-fortress-2`)
  7. DayZ (`dayz`)
  8. Farming Simulator 25 (`farming-simulator-25`)
  9. Euro Truck Simulator 2 (`euro-truck-simulator-2`)
  10. Garry's Mod (`garrys-mod`)
  11. Mount & Blade II: Bannerlord (`mount-and-blade-2-bannerlord`)
  12. Terraria (`terraria`)
  13. 7 Days to Die (`7-days-to-die`)
  14. tModLoader (`tmodloader`)
  15. BeamNG.drive / BeamMP (`beammp`)
  16. ARK: Survival Ascended (`ark-survival-ascended`)
  17. Left 4 Dead 2 (`left-4-dead-2`)
  18. Factorio (`factorio`)
  19. Don't Starve Together (`dont-starve-together`)
  20. Valheim (`valheim`)
  21. Satisfactory (`satisfactory`)
  22. The Isle (`the-isle`)
  23. ARK: Survival Evolved (`ark-survival-evolved`)
  24. Arma Reforger (`arma-reforger`)
  25. Hell Let Loose with Vietnam community preset (`hell-let-loose`)
  26. Squad (`squad`)
- **FR-002**: Every game module MUST contain the complete, standardized file layout:
  - `module.yaml`: Module metadata conforming to `module.schema.json` (name, display name, version, categories, summary, homepage, license, gameplaneMinVersion).
  - `template.yaml`: Complete `GameTemplate` CRD specification conforming to `gametemplate.schema.json`.
  - `README.md`: Clear documentation on game-specific ports, environment parameters, volumes, mods, and administration.
  - `specs.md`: Module architecture, protocols, invariants, inputs/outputs, and boundaries mandated by Constitution Principle IV.
  - `samples/`: At least one ready-to-run sample `GameServer` manifest.
- **FR-003**: Every game template MUST define pinned image references with exact sha256 digests for all concrete versions, alongside a curated catalog of version options.
- **FR-004**: Every game template MUST explicitly declare all required network ports with appropriate protocol tags (`UDP`, `TCP`), standard port names (e.g., `game`, `query`, `rcon`, `web`, `beacon`), and descriptions.
- **FR-005**: Every game template MUST specify a persistent storage configuration (`storage.mountPath`, volume sub-paths, recommended default sizes) targeting only game state, saves, configs, and mods, strictly avoiding shadowing container executables or entrypoints.
- **FR-006**: Every game template MUST declare security context specifications (`runAsUser`, `fsGroup`, explicit `HOME` environment variables) matching the target container image's runtime user to eliminate permission errors.
- **FR-007**: Every game template MUST declare remote administration mechanisms appropriate for the game engine (Source RCON, BattlEye RCON, WebSocket RCON, REST API, or Stdin CLI) including connection parameters, port mappings, and password references.
- **FR-008**: Every game template MUST implement a graceful stop lifecycle action that dispatches engine-specific world-save commands prior to container termination.
- **FR-009**: The system MUST pass static preflight verification across all 26 module templates using the project's module validator (`modules/validate.py`) with 0 errors.
- **FR-010**: All game modules MUST support configurable modding paths, workshop collections, or plugin directories where supported upstream by the game engine.
- **FR-011**: The system MUST provide end-to-end protocol testing specifications for all modules in `test/e2e/`, ensuring real wire-protocol join probes for lightweight servers, and formal CI bucket exclusion annotations for resource-heavy servers per Constitution Principle I.
- **FR-012**: Modules requiring auxiliary services or embedded datastores (such as FiveM with txAdmin/database or Farming Simulator 25 with web management portals) MUST bundle and supervise these auxiliary processes within the single-pod container runtime, exposing dedicated secondary port definitions and unifying persistent storage on a single volume mount.
- **FR-013**: Game modules requiring external authentication or registration tokens MUST provide a non-crashing initialization entrypoint that logs clear diagnostic instructions if the token is missing and idles cleanly rather than crash-looping.

---

### Key Entities

- **Game Module**: A self-contained package (`modules/<name>/`) containing metadata, game templates, documentation, architecture specifications, and sample manifests for a specific game server.
- **GameTemplate**: A Kubernetes Custom Resource Definition (CRD) declaring the container image, version catalog, port bindings, environment variable schemas, volume mount points, resource recommendations, administrative protocols, and lifecycle actions for instantiating game servers.
- **GameServer**: An operational instance of a dedicated game server reconciled by the Gameplane operator based on a chosen `GameTemplate`.
- **Administrative Protocol Interface**: The communication channel (Source RCON, BattlEye RCON, WebSockets, REST API, Stdin) used by Gameplane agents to issue live commands, broadcast alerts, and trigger world saves.
- **Protocol Health Probe**: A protocol-aware client or handshake probe that establishes a wire-level connection with the game server to verify readiness, player capacity, and liveliness.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of the 26 target Steam multiplayer games have complete, schema-compliant, and fully validated module directories (`modules/<name>/`) with `module.yaml`, `template.yaml`, `README.md`, `specs.md`, and `samples/`.
- **SC-002**: 100% of module templates pass `modules/validate.py` preflight checks with 0 errors and 0 unacknowledged warnings.
- **SC-003**: 100% of newly authored modules have corresponding `specs.md` documentation satisfying Gameplane Constitution Principle IV.
- **SC-004**: Server creation time from selecting a template to receiving a running container pod takes less than 30 seconds (excluding one-time image pull and SteamCMD game asset download time).
- **SC-005**: Game servers restart without data loss, world corruption, or file permission errors on persistent storage volumes, verified by restart e2e tests on a representative sample (FiveM, ARK cluster) plus a static storage/security audit of all 26 modules (mount path, `runAsUser`/`fsGroup`/`HOME`).
- **SC-006**: Modules with remote console interfaces execute administrative commands and graceful save-on-shutdown sequences, verified by RCON e2e tests on a representative sample (Team Fortress 2, Squad) plus a static audit of every module's `rcon` and `capabilities.lifecycle.stop` configuration.

---

## Assumptions

- **Container Image Ecosystem**: Community-standard, actively maintained OCI container images (such as Valve/SteamCMD Linux dedicated servers, LinuxGSM, cm2network, ich777, thijsvanloef, or dedicated project images) will be used as upstream container backends.
- **Anonymous Game Downloads**: Games supporting dedicated servers without a commercial license check will use anonymous SteamCMD authentication by default; games requiring game license verification or third-party tokens (e.g., FiveM license key, BeamMP auth token, ETS2 logon token) will accept them via environment variables or secret mounts.
- **Hardware Resources**: End-user hosting clusters have adequate memory and CPU to run chosen games; recommended resource limits and requests in templates will reflect standard minimum operational requirements for 8–32 player community servers.
- **E2E Testing in CI**: Resource-intensive game servers exceeding GitHub Actions memory/disk budgets will have real protocol test suites authored in `test/e2e/` with explicit CI bucket exclusions documented per Constitution Principle I.
