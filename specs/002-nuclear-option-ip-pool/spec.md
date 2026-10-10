# Feature Specification: Nuclear Option Module & Load-Balancer IP Pool Override

**Feature Branch**: `002-nuclear-option-ip-pool`

**Created**: 2026-08-16

**Status**: Draft

**Amendments**: 2026-08-16 — (1) Join-coverage language aligned with feature 001's framework (canonical status vocabulary, JoinDepth, tracked artifact); (2) unverified assumptions restructured and explicitly marked in affected requirements; blocking risk (unlicensable or no Linux build) plainly stated.

**Input**: User description: "I want a new module for nuclear option https://store.steampowered.com/app/2168680/Nuclear_Option/ and support for ip pool ovveride"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Operator deploys a playable Nuclear Option server from the catalog (Priority: P1)

An operator discovers Nuclear Option in the game catalog and wants to run a multiplayer server. They select the module, configure basic settings (server name, password, max players), and the server becomes joinable by real players within a few minutes.

**Why this priority**: This is the fundamental user journey — making a new game available. Without a deployable, joinable server, the entire feature is incomplete. This is what confirms the game is actually supported in practice, not just declared in a template.

**Independent Test**: Can be tested independently by creating a GameServer for the Nuclear Option module, waiting for it to boot, and confirming that a real game client can connect and join the match using the game's actual network protocol (not a mock handshake or bare socket dial).

**Acceptance Scenarios**:

1. **Given** an operator with permissions to create GameServers, **When** they select Nuclear Option from the catalog and click "Deploy", **Then** the GameServer is created with sensible defaults (server name, 8–16 player limit, 8 GB of memory, 30 GB storage for game binary and mission data).
2. **Given** a freshly created Nuclear Option GameServer, **When** the pod reports Running and the agent reports ready, **Then** the dashboard shows the server as "Accepting Players" or similar and displays its join address.
3. **Given** a running Nuclear Option server with an advertised address, **When** a real game client attempts to join using that address and the game's wire protocol, **Then** the join succeeds and the player appears on the server without errors (verified by the client receiving a successful connection message and the operator seeing the player in the player list).
4. **Given** an operator viewing the server's dashboard details, **When** they check the Remote Console section, **Then** the console is accessible and ready to receive commands once the server has finished its boot sequence.

---

### User Story 2 - Operator pins a game server's public address to a chosen load-balancer address pool (Priority: P1)

An operator running multiple clusters or a cluster with multiple address pools wants to ensure a specific server gets an address from a designated pool (e.g., "production-us-east" or "testing-tier"). They set this preference in the server's networking settings and the server is assigned an address from that pool instead of the default.

**Why this priority**: This is the core IP pool override feature. Without this, operators cannot control which pool a server's address comes from, making it impossible to meet network topology or compliance requirements. It enables predictable network segregation for multi-cluster deployments.

**Independent Test**: Can be tested independently by creating a GameServer with a specific pool preference set, observing that the assigned public address falls within the requested pool's address range, and confirming that changing the pool preference (before or after initial assignment) either reassigns to a different pool or clearly explains why reassignment cannot occur.

**Acceptance Scenarios**:

1. **Given** a cluster with multiple configured load-balancer address pools (e.g., "pool-us-east", "pool-us-west", "pool-testing"), **When** an operator creates a GameServer and sets the pool preference to "pool-us-east", **Then** the server is assigned an address from the "pool-us-east" range.
2. **Given** a running server assigned to "pool-us-east", **When** the operator changes the pool preference to "pool-us-west", **Then** either (a) the server is reassigned an address from "pool-us-west" (with a brief service interruption) or (b) the dashboard clearly explains why the change cannot be applied without a restart.
3. **Given** a server with no pool preference set, **When** the server is created and deployed, **Then** it receives an address from the cluster's default pool (the behavior unchanged from today).
4. **Given** an operator creating a server via the REST API (not the dashboard), **When** they specify a pool preference in the GameServer's networking config, **Then** the same pool-assignment logic applies and the server is assigned accordingly.

---

### User Story 3 - Operator administers a running Nuclear Option match remotely (Priority: P2)

An operator needs to moderate a live match: see who is online, remove a disruptive player, manage bans, broadcast a message, or adjust the next mission rotation. They use the Remote Console in the dashboard to run these commands and see real-time feedback without restarting the server.

**Why this priority**: Remote administration is essential for operators running live matches. Without this, responding to player grievances or chat spam requires either restarting the server or logging in directly, both disruptive. This is a standard expectation in server hosting products.

**Independent Test**: Can be tested independently by connecting a real player to a Nuclear Option server, then running each moderation command (get-player-list, kick, ban, broadcast, mission-set) from the Remote Console and confirming the command executes and the result is visible both in the console output and on the server side (e.g., the player disconnects when kicked, the next mission changes, the message appears in game).

**Acceptance Scenarios**:

1. **Given** a running Nuclear Option server with at least one connected player, **When** an operator opens the Remote Console and runs the "get-player-list" command, **Then** the console returns a list of connected players with their Steam ID, display name, and faction.
2. **Given** the same server and player, **When** the operator runs "kick-player" with that player's Steam ID, **Then** the player is disconnected from the server and the console confirms the kick command was accepted.
3. **Given** a server with an empty ban list, **When** the operator runs "banlist-add" with a Steam ID, **Then** that Steam ID is added to the ban list and the console confirms the operation; a subsequent join attempt by that player fails with a "banned" message.
4. **Given** a live match, **When** the operator runs "send-chat-message" with text, **Then** the message appears in the in-game chat and all connected players see it.
5. **Given** the current mission near completion, **When** the operator runs "set-next-mission" with a valid mission name from the rotation list, **Then** the console confirms the change and the server advances to that mission when the current one ends.

---

### User Story 4 - Operator requests one specific fixed address for a server so a published DNS record stays stable (Priority: P2)

An operator publishes a server address in a community listing or configures a stable DNS name for their game server. They need the same public address across restarts. Instead of using a pool name (which may assign a different address each time), they can request one specific address and the system assigns it to them durably.

**Why this priority**: This supports a common operational need (community rosters, published listings, DNS records) that random pool assignment alone cannot solve. It is lower priority than pool selection (which is more common), but essential for operators who have already advertised their server.

**Independent Test**: Can be tested independently by requesting a specific address, noting that address, deleting and recreating the server (or triggering a pod restart), and confirming the same address is reassigned to the new server instance.

**Acceptance Scenarios**:

1. **Given** a cluster with available addresses in a pool, **When** an operator creates a server and specifies a preferred address (in addition to or instead of a pool name), **Then** if that address is available, the server is assigned that specific address.
2. **Given** that address is now in use, **When** the operator later tries to create a new server and request the same address, **Then** the server creation is rejected or warned with a message like "Address X.X.X.X is already in use by [other-server]".
3. **Given** a server assigned to a specific address that is later released (server deleted), **When** the address is freed, **Then** the address becomes available for reassignment to another server (no permanent lock or orphaning).

---

### User Story 5 - Operator edits Nuclear Option server settings and invalid configuration surfaces as a clear error (Priority: P3)

An operator wants to change the server's name, password, or mission rotation without restarting manually. They edit the settings in the dashboard, and if they make a mistake (e.g., invalid JSON in a custom config, unsupported mission name, max-players value outside valid range), the system rejects the change with a clear, actionable error message rather than letting the server crash or hang.

**Why this priority**: Configuration validation is essential for a stable user experience but is lower priority than basic deployment and pool selection. A clear error message avoids frustration and enables operators to self-serve rather than asking support.

**Independent Test**: Can be tested independently by attempting several configuration edits: one valid (changes take effect on restart), and several invalid (server name too long, password with forbidden characters, mission name not in the rotation list, max-players below 1 or above 64). Each invalid edit is rejected with a message that names the problem field and suggests a fix.

**Acceptance Scenarios**:

1. **Given** a running Nuclear Option server, **When** an operator edits the server name to a valid value (e.g., "My Awesome Server") and attempts to save, **Then** the dashboard validates the change, accepts it, and marks the server as "Configuration Changed — Restart Required" or similar. **AMENDMENT (2026-10-09, T118)**: there is no separate "Restart Required" status; a saved configuration change is reconciled by the operator, which re-renders the config and rolls the pod through the `gameplane.local/config-hash` pod-template annotation (`operator/internal/controller/gameserver_config.go:28`), so the scenario is satisfied by the change being accepted and applied by that restart.
2. **Given** an operator editing the server name to an invalid value (e.g., a string 256+ characters), **When** they try to save, **Then** the dashboard validates the change, rejects it with a message like "Server name must be 1–64 characters", and does not apply the change.
3. **Given** the server is restarted with the new valid name, **When** the server boots, **Then** the new name is reflected in the config file and in the in-game server browser.
4. **Given** an operator editing the mission rotation or other game settings via a JSON text field, **When** the JSON is malformed and they attempt to save, **Then** the dashboard validates the JSON format, rejects it with "Invalid JSON in [field]: [specific error]", and does not apply the change (preventing the server from entering a crash loop on the next restart). **AMENDMENT (2026-10-09, T118)**: Gameplane has no JSON config-field type; mission rotation and other settings are typed fields (enum, int with `min`/`max`, string with `minLength`/`maxLength`). An invalid value is rejected in the dashboard before saving and, if it reaches the operator, surfaces on `status.conditions` naming the field and the constraint, without a crash loop. That is the behaviour this scenario now requires.

---

### User Story 6 - Operator gets a clear status message when address pool assignment fails (Priority: P3)

An operator creates a server with a pool preference but nothing happens — no address is assigned, and the server stays in a "Pending" state indefinitely. Instead, the dashboard clearly tells the operator why: the pool does not exist in the cluster, the pool has no available addresses, the requested address is already in use, or the exposure mode is set to something other than "Load Balancer" (which is incompatible with pool selection).

**Why this priority**: Error clarity is important for self-service operation but lower priority than the happy path. Operators need to understand why their request was not fulfilled so they can fix it.

**Independent Test**: Can be tested independently by triggering each failure scenario (nonexistent pool name, exhausted pool, conflicting address, wrong exposure mode) and confirming the dashboard displays a distinct, readable error message for each, making it clear what went wrong and how to fix it.

**Acceptance Scenarios**:

1. **Given** an operator creating a server with a pool name that does not exist in the cluster, **When** the server is submitted, **Then** the status immediately shows "Pool 'unknown-pool' not found in cluster" or similar, allowing the operator to choose a different pool.
2. **Given** a pool that has no available addresses (all addresses in use or exhausted), **When** an operator creates a server requesting that pool, **Then** the status shows "Address pool 'pool-name' is exhausted; no addresses available" (not a hanging "Pending" state).
3. **Given** an operator requesting a specific address that is already assigned to another server, **When** the request is submitted, **Then** the status shows "Address X.X.X.X is already in use by [server-name]; choose a different address or pool" with a link to view the conflicting server.
4. **Given** an operator setting the exposure mode to "Internal" (ClusterIP) while also specifying a pool preference (which requires LoadBalancer), **When** the server is created, **Then** the dashboard warns "Pool preference is ignored when exposure mode is not 'Load Balancer'" and does not assign a public address.

---

### Edge Cases

- What happens when a Nuclear Option server's JSON config file becomes malformed (e.g., corruption, partial write)? The operator must be able to detect this via the server status or logs, and either fix it manually or trigger a rollback/restart with a known-good config.
- How does pool reassignment work if the server is live with active players? Does the player see a brief disconnect while the address changes, or is the address sticky for the lifetime of the server instance? The spec assumes the address is fixed for a server's lifetime (no mid-flight reassignment without a deliberate restart), but this must be clarified against real load-balancer behavior.
- What if the cluster's address manager (e.g., MetalLB, Cilium) does not support pool names or explicit address requests? The spec must work with any CNCF-standard address manager, either by falling back to a generic preference hint or by documenting which managers are compatible.
- Can an operator set a pool preference on a running server created before the IP pool feature existed (i.e., already has an address)? If so, does it trigger a reassignment or stay on the original address?
- What happens if two servers request the same specific address at the same time? The cluster's address manager's concurrency handling is authoritative, but the dashboard must reflect the outcome (one succeeds, one is told the address is taken).
- Nuclear Option's remote-command JSON protocol is documented in third-party sources, not the official Steam documentation (see Verification Required Before Implementation, Claim 3). If the protocol changes in a future game update, drift is detected and handled via the real-world protocol-level E2E test (per the constitution); a test failure signals that the assumed protocol or ports are no longer valid.
- The Nuclear Option dedicated server is believed to be a separate binary (app ID 3930080, per third-party hosting-provider documentation, not confirmed against Steam's own store listing; see Verification Required Before Implementation, Claim 1) from the base game (app 2168680). Can the operator update the binary without manually pulling a new module/image? (This is a templating/upgrade-policy question, out of scope for this spec but noted as a known gap.)
- Nuclear Option's server logs are assumed to be readable by the agent for status/backup purposes, consistent with every other supported module (see Verification Required Before Implementation, Claim 5). The exact on-disk log location for this specific game must be verified during implementation.
- Does the dedicated server expose any signal that distinguishes "process started" from "actually accepting player connections"? This has not been confirmed (see Verification Required Before Implementation, Claim 4). If no such signal exists, "Accepting Players" status (User Story 1, Acceptance Scenario 2) must be inferred conservatively (e.g., from the same real-protocol probe used by the join test).
- If a mission name in the config becomes invalid (the mission is removed from the game in an update), the server may fail to load that mission. The operator must be able to either revert the mission or pick a different one from the current valid list.

## Requirements *(mandatory)*

### Functional Requirements

**Nuclear Option Module:**

- **FR-001**: The Nuclear Option game module MUST be available in the catalog and selectable by operators for deployment just like any other supported game.
- **FR-002**: The Nuclear Option module MUST include a server template with sensible resource defaults: CPU allocation of 2–4 cores (CPU-bound workload), memory allocation of 8 GB minimum and 16 GB recommended, and storage for the game binary and mission data. **AMENDMENT (2026-08-22)**: The prior assumption of "30 GB of storage" is far too high. Live measurement shows the dedicated server binary installed footprint is 891 MB, not 30 GB. Storage recommendations MUST be revisited during implementation; a reasonable default is 2–4 GB to allow for saves, logs, and mission packs without excessive waste. The 30 GB figure is flagged as a documentation correction and does not block implementation, but the template's storage request will be substantially lower than previously assumed.
- **FR-003**: Operators MUST be able to configure the Nuclear Option server's settings (server name, password, max-player limit, mission rotation mode, mission list) via the dashboard's server configuration form, with validation applied to each field.
- **FR-004**: The Nuclear Option server MUST be recorded in docs/game-coverage.md with a join-coverage status (covered-in-ci, covered-deferred, blocked-doc, or out-of-scope-by-design) by the time the module ships, per feature 001's coverage framework. If the join protocol is documented well enough to build a client, a real protocol-join E2E test MUST be committed to test/e2e/ (with JoinDepth JOINED per the data model); the test MUST be bucketed in test/e2e/buckets.sh, and the module's row in docs/game-coverage.md MUST reflect its status and bucket. **Unverified:** The game port is assumed to be UDP 7777 per third-party hosting-provider documentation (see Verification Required Before Implementation, Claim 2), and a real join using that port is assumed possible without platform-specific credentials. If the protocol proves undocumented or the port incorrect, the module is marked blocked-doc and names the specific artifact required to unblock it (e.g., packet capture, reverse-engineered field map) per feature 001 FR-010.
- **FR-005**: If the Nuclear Option join protocol can be documented and a test authored, that test MUST be verified to fail when run against a non-listening address and to succeed when run against a real, booted Nuclear Option server before it is committed, per the project constitution's E2E-Tested Delivery principle. If protocol reverse-engineering or platform constraints make a join test infeasible, the module's coverage status becomes blocked-doc (with the specific blocker named) or out-of-scope-by-design (if the constraint is architectural), per feature 001 FR-008.
- **FR-006**: The Nuclear Option server's Remote Console interface MUST support the remote-command protocol, with moderation commands (get-player-list, kick, ban, broadcast, mission-set) functional via that interface. **Unverified:** The protocol is assumed to be a length-prefixed JSON request/response scheme on a dedicated remote-command port, separate from the game and query ports, based on third-party hosting-provider documentation. The specific port numbers are deliberately not restated here — see Verification Required Before Implementation, Claim 2, so there is one place to correct when they are confirmed. Because this protocol is not officially documented by the game's publisher, its exact shape (port, framing, command names, result-code semantics) MUST be confirmed against a real running server during implementation. If the protocol proves undocumented or incompatible with the assumed ports, the E2E join test and Remote Console coverage MUST record that drift; the module's coverage status becomes blocked-doc with the specific reverse-engineering artifact required to proceed, or transitions to out-of-scope-by-design if the protocol proves permanently inaccessible.
- **FR-007**: Operators MUST be able to view a list of connected players (Steam ID, display name, faction) via a Remote Console command, with the results displayed in the console output.
- **FR-008**: Operators MUST be able to kick a connected player by Steam ID via a Remote Console command, disconnecting that player from the server.
- **FR-009**: Operators MUST be able to add a Steam ID to the server's ban list via a Remote Console command, preventing that Steam ID from joining.
- **FR-010**: Operators MUST be able to remove a Steam ID from the server's ban list via a Remote Console command.
- **FR-011**: Operators MUST be able to broadcast a chat message to all connected players on the Nuclear Option server via a Remote Console command.
- **FR-012**: Operators MUST be able to change the next mission in the rotation and adjust the time remaining in the current mission via Remote Console commands.
- **FR-013**: Nuclear Option servers MUST support full backup and restore workflows (snapshot of configuration, missions, ban list, and logs) using Gameplane's existing backup and restore capabilities, with no Nuclear-Option-specific backup tooling required.

**Load-Balancer IP Pool Override:**

- **FR-014**: Operators MUST be able to specify a load-balancer address pool preference when creating or editing a GameServer's networking settings.
- **FR-015**: Operators MUST be able to request a specific IP address (instead of or in addition to a pool name) for a server's public endpoint.
- **FR-016**: When a GameServer is created with a pool preference set and the exposure mode is "Load Balancer", the server's assigned public address MUST come from that pool, not the default pool.
- **FR-017**: When a GameServer is created with no pool preference set, the server MUST be assigned an address from the cluster's default load-balancer pool, maintaining backward compatibility with servers created before the pool-override feature.
- **FR-018**: When a GameServer's exposure mode is set to anything other than "Load Balancer" (e.g., Internal/ClusterIP, Node Port, Host Port), any pool preference MUST be ignored and a dashboard warning MUST indicate that the pool setting has no effect.
- **FR-019**: The operator MUST be able to view the server's assigned public address and, if applicable, the address pool it came from, displayed prominently in the server's networking details in the dashboard.
- **FR-020**: When a pool assignment cannot be fulfilled (pool does not exist, pool is exhausted, requested address is in use, or exposure mode is incompatible), the GameServer status MUST display a distinct, readable error message naming the specific reason and the corrective action available, rather than remaining in an indefinite "Pending" state.
- **FR-021**: A GameServer whose pool preference is set via the REST API, or applied directly to the cluster without going through the dashboard, MUST be assigned an address using the same pool-assignment logic as one created through the dashboard — since reconciliation is driven by the GameServer record itself, not by which path created it.
- **FR-022**: The pool-override feature MUST support MetalLB and Cilium load-balancer address managers. **Amendment (2026-08-22)**: The implementation does NOT use a generic mechanism; instead, it contains two hand-written per-manager translation branches in `operator/internal/controller/gameserver_controller.go::reconcileService()`, selected by an explicit `--address-manager` flavor flag at operator startup (values: `metallb`, `cilium`, `none`). MetalLB translation sets the `metallb.io/address-pool` annotation on the Service; Cilium translation sets the `gameplane.local/lb-pool` label and `lbipam.cilium.io/ips` annotation. A generic mechanism is not possible: MetalLB and Cilium expose fundamentally incompatible vendor-specific surfaces (MetalLB requires an annotation, Cilium requires a label *and* a cluster admin mirror of that label in the CiliumLoadBalancerIPPool's `spec.serviceSelector`), and they report pool-assignment failures differently (MetalLB via Events, Cilium via a Service condition). Supporting an additional address manager requires adding a new translation branch and flavor value in the operator code.

**Cross-Cutting & Validation:**

- **FR-023**: Invalid Nuclear Option configuration (malformed JSON config, unsupported mission names, invalid player caps) MUST be detected and reported as a clear validation error before the server attempts to boot, preventing crash-loops or hung servers.
- **FR-024**: The dashboard MUST display the server's current configuration and status for Nuclear Option in the same layout and detail as other game types, with no Nuclear-Option-specific UI or extra steps required to view/edit settings.
- **FR-025**: Both the operator and the system MUST be able to read the assigned address pool name and/or explicit address from a GameServer created either via dashboard or REST API, ensuring visibility and auditability.
- **FR-026**: Nuclear Option's join-coverage status MUST be verified by the test/e2e/joincoverage.sh verifier gate before the module ships, per feature 001. The module's row in docs/game-coverage.md MUST record either covered-in-ci (if a real join test runs in a default CI bucket), covered-deferred (if the test is authored and committed but excluded from CI due to resource constraints, with a lastVerified date), or blocked-doc/out-of-scope-by-design (if the protocol cannot be tested). If the game server is too resource-heavy to run in the default CI runners, its real protocol-level join test MUST still be authored and committed to `test/e2e/`; it MUST be excluded from all CI bucket execution in `test/e2e/buckets.sh` with a comment explaining the exclusion, and MUST remain runnable on demand (against a manually provided cluster or triggered via workflow).
- **FR-027**: Documentation MUST describe the Nuclear Option module's setup, configuration options, remote-console command syntax, resource requirements, and any known limitations or workarounds, including the module's `specs.md` (per the project's per-module documentation convention). This documentation MUST be updated in the same change as any change to the module's actual behavior, so it never drifts from what the module does.

### Key Entities

- **Game Module (Nuclear Option)**: A deployable game template representing the Nuclear Option dedicated server; has a configuration schema (name, password, max-players, mission rotation), port exposure settings, and resource defaults (8–16 GB RAM, 2–4 CPU, 30 GB storage). Exact port assignments (game, query, and remote-command ports) are assumed from third-party documentation and unconfirmed against the publisher — see Assumptions.
- **Game Server (Nuclear Option instance)**: A running instance of the Nuclear Option module, with a lifecycle (Pending, Running, Suspended, Failed) and a networking configuration (exposure mode, pool preference, assigned address).
- **Address Pool**: A named group of public IP addresses (or address ranges) managed by the cluster's load-balancer controller. Examples: "production-us-east", "testing-tier", or "default".
- **Pool Preference**: An optional operator-set constraint on a GameServer specifying which address pool should provide its public endpoint.
- **Assigned Public Address**: The actual IP address (or FQDN) that the cluster's address manager assigns to a server's public network endpoint, visible to external clients for joining.
- **Remote Console Session**: An interactive channel allowing the operator to send commands (get-player-list, kick, ban, broadcast, mission-set) to the Nuclear Option server and receive parsed responses.
- **Player Entry**: A record of a connected player to a Nuclear Option server, with Steam ID, display name, and faction.
- **Ban List Entry**: A persistent entry (Steam ID) in the server's ban list, preventing that player from joining.
- **Mission Rotation**: The sequence of missions that the server will cycle through, configurable by the operator.
- **Server Configuration**: The JSON-format configuration file for a Nuclear Option server (server name, password, max-players, mission list, voting parameters, etc.).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can deploy a playable Nuclear Option server from the catalog and have a real player join it within 5 minutes from clicking "Deploy" (measured from server creation to a confirmed join).
- **SC-002**: 100% of pool-assignment requests for servers with a valid pool preference set result in the server being assigned an address from the requested pool (no false failures or silent fallbacks).
- **SC-003**: Within 30 seconds of a pool misconfiguration (nonexistent pool, exhausted pool, conflicting address), the dashboard status displays the specific error message (not a generic "Pending" or timeout).
- **SC-004**: An operator can execute a moderation command (kick, ban, broadcast, mission-set) on a Nuclear Option server and see the result reflected on the server within 5 seconds of issuing the command.
- **SC-005**: Nuclear Option's join-coverage status (covered-in-ci, covered-deferred, blocked-doc, or out-of-scope-by-design) is recorded in docs/game-coverage.md at ship time per feature 001's framework; test/e2e/joincoverage.sh verify confirms the record is complete and consistent. A module must never be shipped with an unknown or unrecorded coverage status.
- **SC-006**: Servers created without a pool preference set (backward-compatible case) continue to receive addresses from the default pool. **Amendment (2026-08-22)**: The latency-parity claim ("no change in behavior or latency compared to the previous release") is **unmeasured and out of scope for v1**. What IS verifiable: a server with no pool preference receives an address from the cluster's default pool, with no annotation or label written to its Service (preserving pre-feature behavior), and `reconcileService()` takes the same code path as before (no new branching or delay). A real latency measurement would require (1) a baseline from a pre-feature build, (2) a test environment with stable load characteristics, and (3) a defined latency metric (time-to-address-assignment from Service creation). This measurement is deferred to a future operational audit and is not part of v1 acceptance.
- **SC-007**: Invalid Nuclear Option configuration (malformed JSON, unsupported mission, out-of-range player limit) surfaces as a validation error within 10 seconds of the server attempting to boot, preventing the server from entering a crash-loop or hung state.
- **SC-008**: The assigned public address for a newly created server is visible in the dashboard networking details within 30 seconds of the address manager assigning it.

## Assumptions

- **BLOCKING RISK — RESOLVED (2026-08-22)**: This spec assumed the Nuclear Option dedicated server (Steam app 3930080) is publicly downloadable, requires no ownership of the base game, and ships a native Linux build. **CONFIRMED**: Anonymous SteamCMD (`+login anonymous +app_update 3930080 validate`) succeeds without owning the base game (app 2168680). Binary is native Linux (`NuclearOptionServer.x86_64`, not Proton/WINE). Installed size is **891 MB** (not the 30 GB assumed in FR-002 resource defaults — see amendment to FR-002 below). The module can ship from a licensing and platform perspective.

- **Network Footprint — AMENDED (2026-08-22)**: The exact port assignments are now confirmed against a live running server. TCP port 7779 (remote-command, loopback-only) is confirmed. UDP port 7778 is bound (query). **UDP port 7777 (assumed game join port) is NOT bound.** The server logs `SteamGameServer.LogOnAnonymous` and `Set Advertise Server: True`, indicating player traffic routes through Steam's game-server networking rather than a raw UDP 7777 socket. This is a significant finding: the assumption that a real join test can be built against UDP 7777 is **in doubt**. See Verification Required Before Implementation, Claim 2 (amended) and FR-004/FR-005 follow-up questions.

- **Remote-Command Protocol — RESOLVED (2026-08-22)**: The protocol is confirmed as a length-prefixed JSON request/response scheme with numeric status codes. All observed status codes match the contract: 2000 (success), 4003 (malformed JSON), 4004 (unknown command), 4005 (bad arguments). Command names and signatures are confirmed via the live capture in the contract document (20 registered commands, matching the registered list observed at startup). Three critical contradictions with prior assumptions are documented in the contract:
  1. `set-next-mission` requires three arguments (group, name, time), not one.
  2. `kick-player`, `banlist-add`, `banlist-remove` return status 2000 (success) even for nonexistent targets; success does NOT confirm the target existed.
  3. `get-player-list` returns a `"Players"` wrapper (capital P) with an empty array when no players are connected.

- **Readiness Signal — RESOLVED (2026-08-22)**: The server emits a clear readiness log line: `[DedicatedServerManager] Waiting for Players before loading next map` at approximately 3.9 seconds after startup. This signal reliably marks the point at which the server is ready to accept connections.

- **Log Location & Format — RESOLVED (2026-08-22)**: Logs are written to `./logs/server-<timestamp>.log` by default when invoked via `RunServer.sh` with `-logFile` flag. Configuration flag `-DedicatedServer <path/to/config.json>` overrides the config location. Log format is plain text, readable by the agent.

- **Configuration Schema — DOCUMENTED (2026-08-22)**: The auto-generated `DedicatedServerConfig.json` schema is captured in the contract document, "Live Protocol Verification" section. Key fields: `ServerName` (string), `Password` (string), `MaxPlayers` (integer, default 16), `Port` and `QueryPort` (objects with `{IsOverride, Value}` structure), `BanListPaths` (array), `MissionDirectory` (string), `MissionRotation` (array of mission objects). The override pattern for Port/QueryPort is confirmed as real.

- **AMENDMENT to FR-002 — Resource Footprint**: The 30 GB storage figure assumed in FR-002 is **far too high**. Live measurement shows **891 MB installed footprint**, not 30 GB. The module template's storage recommendation must be revisited; 30 GB is a regression from modern standards and should be reduced substantially (likely 2–4 GB to leave headroom for saves and mission packs). This is flagged as a documentation correction, not a blocking issue.

- Pool selection is expected to be implemented via whatever mechanism the cluster's address manager (e.g., MetalLB, Cilium) exposes for expressing a pool or address preference on a load-balancer-type endpoint. Gameplane does not itself manage address allocation — it only records the operator's preference and reports back the system's assigned address. The specific mechanism is a planning-phase decision, not specified here.
- The pool-preference setting is only visible and editable by users who already have permission to edit a server's networking settings (i.e., no new RBAC role is required; the feature re-uses existing networking permissions).
- A server's assigned public address is fixed for the lifetime of that server instance. Changing the pool preference on a running server either triggers a pod restart (with a brief service interruption while a new address is assigned) or the change is deferred until the next operator-initiated restart. The exact behavior is a planning-phase decision; this spec assumes one consistent behavior across all cases.
- No per-tenant pool quotas, allow-lists, or advanced scheduling policies are in scope for v1. Any operator with permission to create a GameServer can request any pool. Advanced policies (per-user/per-namespace pool restrictions) are roadmap items.
- Nuclear Option's actual resource footprint (891 MB binary footprint, estimated 4–8 GB with saves and logs) is modest enough that the server can be tested on a real cluster in CI under operator-provided conditions. However, if the server is determined to be too heavy for the default GitHub Actions runners, the E2E test is still authored and committed but excluded from the CI buckets, per the project constitution.
- The term "load-balancer" includes any Kubernetes LoadBalancer-type Service (MetalLB, cloud-provider LBs, Cilium). If a cluster uses a different exposure mechanism (e.g., Ingress-only, no external IPs), the pool-override feature has no effect and the dashboard indicates this clearly.
- Existing servers with no pool preference will continue to work unchanged. There is no migration effort, data conversion, or backward-compatibility breaking change.

## Verification Required Before Implementation

The following claims have been confirmed or amended against a live running server on 2026-08-22. Each is listed with the evidence collected and any follow-up actions required.

**Claim 1: Dedicated Server Availability & Platform — RESOLVED (2026-08-22)**

*Previous Status:* Unverified.

*Evidence:* Anonymous SteamCMD install (`+login anonymous +app_update 3930080 validate`) succeeds without owning the base game (app 2168680). Binary is `NuclearOptionServer.x86_64` (native Linux, confirmed via file type `x86-64 executable`), not Proton/WINE. Server starts and accepts remote-command connections. Installed size: 891 MB.

*Resolution:* The module can ship. No licensing or platform blockers remain.

**Claim 2: Network Ports — AMENDED (2026-08-22)**

*Previous Status:* Assumed UDP 7777 (game), UDP 7778 (query), TCP 7779 (remote-command).

*Evidence:* Netstat output from the running server:
- TCP LISTEN 127.0.0.1:7779 (remote-command, loopback-only) — **CONFIRMED**
- UDP bound on ports 7778 and 45793 (query and ephemeral) — **7778 CONFIRMED**
- **UDP 7777 NOT bound** — no listening socket on 7777

Server logs: `[CommandLineArgParser] Starting remote command server on port 7779`. Also: `SteamGameServer.LogOnAnonymous` and `Set Advertise Server: True`, indicating game client traffic routes through Steam's game-server networking rather than a raw UDP 7777 socket.

*Fallback & Follow-Up:* The assumption that a real join test can be built against raw UDP 7777 is **in doubt**. The mechanism by which game clients reach the server (Steam's NAT traversal vs. direct socket) must be clarified before FR-004/FR-005 join-test assumptions are finalized. **Questions for implementation**:
1. Can a real game client successfully join this server, and if so, what transport mechanism does it use?
2. Can the join address (the address advertised to game clients) be determined from the server's logs or config?
3. Is a direct protocol-level join test against a port socket feasible, or must the test rely on Steam's game-server API?

Answers will determine whether the module's join-coverage is "covered-in-ci" (real join test possible) or "blocked-doc" (join test requires undocumented Steam integration), per feature 001's classification framework.

**Claim 3: Remote-Command Protocol Format — RESOLVED (2026-08-22)**

*Previous Status:* Unverified.

*Evidence:* Live packet captures from a real server responding to 11 test commands, documented in the contract file, "Live Protocol Verification" section. Wire format confirmed: request = 4-byte LE length + JSON body; response = 4-byte LE status + 4-byte LE body length + body. Status codes observed: 2000 (success), 4003 (malformed JSON), 4004 (unknown command), 4005 (bad arguments with detail message). 20 registered commands confirmed at server startup.

*Resolution:* The moderation commands (FR-007–FR-012) can proceed with the confirmed protocol. Three critical contradictions with prior assumptions are documented in the contract:
1. `set-next-mission` signature is three arguments (group, name, time), not one.
2. Moderation commands (kick, ban add/remove) return status 2000 even for nonexistent targets.
3. `get-player-list` wrapper key is `"Players"` (capital P), confirmed when empty.

*Unverified Remainder:* The per-entry structure of `get-player-list` (exact fields when a player is connected) could not be confirmed because no player was connected during the probe. This is the one genuine remaining protocol unknown; it blocks FR-007 (player list display) until resolved. A real join test with a connected player is required.

**Claim 4: Readiness Signal — RESOLVED (2026-08-22)**

*Previous Status:* Unverified.

*Evidence:* Log line observed at t=3.903s after startup:
```
[DedicatedServerManager] Waiting for Players before loading next map
```
This marks the exact moment the server is ready to accept connections.

*Resolution:* Dashboard readiness can report "Accepting Players" as soon as this log line appears, without a real join probe. Latency is ~4 seconds from pod startup to accepting connections.

**Claim 5: On-Disk Log Location & Format — RESOLVED (2026-08-22)**

*Previous Status:* Unverified.

*Evidence:* Log file written to `./logs/server-<timestamp>.log` when invoked via `RunServer.sh` with `-logFile` flag. Format is plain text, readable by the agent. Override via `-DedicatedServer <path/to/config.json>` controls both config and (implicitly) log location if specified in config.

*Resolution:* Logs are accessible and parseable. Backup of logs is feasible; log inclusion in backups is fully supported.

---

## Decision Record: Player Display Names (T022)

The dedicated server's `get-player-list` returns only `steamId` and `faction` (upstream removed `displayName` because the headless server caches no names and directs integrators to Steam's Web API). **FR-007 and US3 Acceptance Scenario 1 stand as written.** Gameplane resolves names with a Steam Web API lookup that lives in the **API server** (`api/internal/steam/`), never the agent: the games namespace runs `default-deny-egress` over every pod (`charts/gameplane/templates/networkpolicies.yaml`), so an agent-side lookup would need an egress hole and the Steam key in every game pod, whereas the control-plane namespace gives one egress path, one Secret and one shared cache.

Constraints: outbound calls dial through `netguard` with the strict `IsPublic` policy; the key is optional and comes from a Secret (`api.steam.apiKeySecretRef`, env `GAMEPLANE_STEAM_API_KEY` only); ids are batched into `ISteamUser/GetPlayerSummaries/v2` calls of up to 100; each call is bounded by a timeout inside the SC-004 five-second budget; results live in a small bounded in-process LRU cache (hours-scale positive TTL, shorter negative TTL for ids Steam omits, single-flight de-duplication) and **not** in a database table, migration, Redis or a cross-replica cache; any failure degrades to the raw Steam ID; kick, ban and unban stay keyed on the Steam ID. Full design: `data-model.md` §8 and `plan.md` Decision 9; implementation: T081–T104.

## Out of Scope

- **Address pool creation and management**: Gameplane does not create or manage address pools. Pools must be created and configured by the cluster operator (via MetalLB/Cilium/other address manager configuration), and Gameplane only consumes pool names/hints the operator provides.
- **IPv6-specific behavior**: The feature works with IPv6 addresses (they are treated identically to IPv4), but advanced IPv6-only features (dual-stack handling, IPv6-specific pool policies) are not required.
- **Per-tenant pool quotas or allow-lists**: Administrators cannot restrict which pools specific users or namespaces can request; any user with GameServer-edit permissions can use any pool.
- **Nuclear Option mod support**: The feature does not support BepInEx mods, third-party mods, or custom mod loading. Only the unmodded dedicated server is in scope.
- **Dynamic address reassignment without restart**: A server's address does not change without a deliberate operator action (restart, manual reassignment). Address reassignment due to cluster rebalancing is handled by the address manager, but Gameplane does not drive this.
- **Custom mission authoring tooling**: The feature does not provide UI for creating or uploading custom missions. Operators use only missions included in the game or configured externally.
- **Multi-cluster address pools**: Pools are cluster-local. Multi-cluster federation (addressing across multiple physical Kubernetes clusters as one pool) is out of scope.
