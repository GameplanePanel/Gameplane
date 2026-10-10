# Implementation Plan: Nuclear Option Module & Load-Balancer IP Pool Override

**Branch**: `002-nuclear-option-ip-pool` | **Date**: 2026-08-21 | **Spec**: `./spec.md`

**Input**: Feature specification from `/specs/002-nuclear-option-ip-pool/spec.md`

## Summary

The feature is two independent tracks sharing no code and requiring separate planning and execution:

**Track A — Nuclear Option Game Module**: Adds a new `modules/nuclear-option/` OCI bundle to the gameplane-module submodule, with a new agent console protocol handler (`nuclearoption`) for the game's remote-command port (separate from the game/query ports). The module ships with a server template (8–16 GB RAM, 2–4 CPU, 30 GB storage), configuration schema (name, password, max-players, mission rotation), and moderation commands (get-player-list, kick, ban, broadcast, mission-set) reachable via Remote Console. Includes a real protocol-level E2E join test exercising the UDP 7777 game port; due to 8–16 GB runtime footprint, the test is authored and committed to `test/e2e/` but excluded from all CI buckets in `test/e2e/buckets.sh` and remains runnable on demand.

**Track B — Load-Balancer Address-Pool Override**: Adds typed `addressPool` and `address` fields to `GameServerNetworking` CRD in the operator, translates them into cluster-vendor-specific Service mutations (MetalLB annotation or Cilium label), updates the API and dashboard to display/edit pool preferences, and reports pool-assignment errors distinctly within 30 seconds. Ships first because it has no external unknowns and is independently testable. E2E coverage runs in default CI buckets.

Track B is unblocked and independently shippable; it should ship FIRST. Track A's blocking risk (game availability/licensing) is resolved by publisher-official documentation confirming Steam app 3930080 ships a native Linux binary downloadable via SteamCMD without owning the base game; however, undocumented runtime details (remote-command protocol format, on-disk log path) must be verified against a real running server during implementation.

## Technical Context

**Language/Version**: Go 1.26 (operator, agent, api) + TypeScript 5.6 strict (web); React 18.3, Vite 5.4

**Primary Dependencies**: 
- Operator/Agent/API: controller-runtime v0.19.0, client-go v0.35.0, chi v5, coder/websocket v1.8.12
- Web: TanStack Router, TanStack Query, Radix + shadcn/ui, Tailwind 3.4, lucide-react, Monaco editor, xterm.js
- **In-repo shared module `netguard`** (Track A, Steam display-name lookup — see Decision 9): the API server's Steam Web API resolver dials through `netguard` using the strict `IsPublic` policy. Wiring status verified: `api/go.mod` already declares `require github.com/GameplanePanel/gameplane/netguard v0.0.0` with a local `replace` (lines 5–9), and `api/` is **already** a netguard importer via `api/internal/notify/notify.go:17` and `api/internal/notify/deliver.go:18` — so no new `go.work` / `go.mod` wiring is needed and the earlier planning assumption that this would be netguard's first API-side importer is superseded. What *is* in scope: `netguard`'s coverage gate (`netguard/.testcoverage.yml`, total 91%) applies to any change made inside that package for this feature, and the new resolver's own coverage lands under the `api` gate (80%).

**Storage**: N/A for this feature (no new database tables). Existing GameServer CRD status fields carry pool/address state.

**Testing**: 
- Go unit tests + envtest 1.31 (operator/api modules)
- Vitest 2.1 + @testing-library/react + msw (web)
- Kind-based E2E in `test/e2e/` with per-test unique resource names, t.Parallel() calls, shared-state guards

**Target Platform**: Kubernetes 1.28+, Helm 3.13+; CNCF-standard load-balancer address managers (MetalLB, Cilium, cloud LBs)

**Project Type**: Kubernetes operator + REST/WS gateway + React dashboard; multi-package Go workspace + npm frontend

**Performance Goals & Constraints**:
- SC-001: Deploy-to-join under 5 min (Track A)
- SC-003: Pool misconfiguration error visible within 30 seconds (Track B)
- SC-004: Remote Console command result visible within 5 seconds (Track A)
- SC-008: Assigned public address visible in dashboard within 30 seconds (Track B)

**Scale/Scope**: Per-cluster; no per-tenant pool quotas in v1 (explicitly out of scope per spec's Assumptions section). Game module resource requirements: 8–16 GB RAM, 2–4 CPU, 30 GB storage.

## Constitution Check

*GATE: PASS with one residual risk noted below. Re-check after design pass.*

**I. E2E-Tested Delivery**: 
- Track A REQUIRES a real protocol-level join test using the game's actual UDP 7777 wire protocol, committed to `test/e2e/` at JoinDepth JOINED. The test MUST be proven to fail against a dead address and succeed against a real listener per constitution principle I. Due to 8–16 GB footprint, the test is bucketed in `test/e2e/buckets.sh` in the `bot-heavy` bucket with a comment explaining the exclusion (never execute in default CI), but remains runnable on demand and is NOT skipped or deleted. The module's row in `docs/game-coverage.md` records status as `covered-deferred` (test exists, excluded from CI) with a `lastVerified` date when next run against a real cluster.
- Track B REQUIRES e2e coverage of pool assignment (valid pool assignment, nonexistent pool error, exhausted pool error, incompatible exposure mode warning) in a default-executed CI bucket.

**II. Design-First for User-Facing Change**: 
- Track B adds three dashboard screens with pool/address UI. REQUIRES Pencil `design.pen` updates via pencil MCP server (never hand-edit .pen files) on nodes: Create Server — Step 4 Network (`f1Vga`), Server Detail — Settings · Networking (`J5pjJ3`), Server Detail — Overview (`EZFW0`). Same change must re-export touched nodes to `design-export/json/<node-id>.json` and `design-export/screenshots/<node-id>.png` via pencil MCP.
- Track A requires NO new UI (module configuration renders through existing generic config-schema form).

**III. Language & Ecosystem Best Practice**: 
- No in-source suppressions; Go errors wrap with `%w`; TypeScript strict, no unjustified `any`. Coverage gates must remain green: operator 72%, api 80%, agent 90%, gameaction 91%, web 92/76/82/92.

**IV. Spec-Driven Development**: 
- Track A must ship `modules/nuclear-option/specs.md` (spec FR-027) documenting setup, configuration options, remote-console command syntax, resource requirements, known limitations.
- Any new agent protocol (Track A) must be documented in module specs.md in the same change as the protocol implementation.

**VI. CI Bears the Heavy Lifting**: 
- Nothing runs locally (no builds, tests, lint, codegen). Work is verified by pushing to branch and watching GitHub Actions.

**Residual Risk (Not a Violation)**: The remote-command protocol (opt-in via `-ServerRemoteCommands [port]`, default TCP 7779) has NO authentication of any kind. Design MUST ensure the port is pod-internal only — never advertised in the Service, never exposed to external clients. The agent sidecar alone reaches this port. Security boundary: pod loopback only.

## Project Structure

### Documentation (this feature)

```text
specs/002-nuclear-option-ip-pool/
├── plan.md              # This file (implementation plan)
├── spec.md              # Feature specification (already exists)
├── research.md          # Phase 0 research artifacts (to be created by /speckit-plan if needed)
├── data-model.md        # Phase 1 data/CRD model details (to be created if Phase 0 research surfaces unknowns)
├── quickstart.md        # Phase 1 onboarding for implementers (to be created if reference implementation complexity warrants)
├── contracts/           # Phase 1 API contracts directory (to be created if external API changes are significant)
└── tasks.md             # Phase 2 task breakdown (produced by /speckit-tasks, not by /speckit-plan)
```

### Source Code — Track B (Load-Balancer IP Pool Override)

```text
operator/api/v1alpha1/
  gameserver_types.go       [EDIT] GameServerNetworking struct (lines 226–278), add 
                                  typed addressPool + address fields; GameServerEndpoint 
                                  struct (lines 494–513) mirrors these for status reporting
  zz_generated.deepcopy.go  [REGENERATED by make generate]

operator/config/crd/
  *.yaml                    [REGENERATED by make manifests — GameServer CRD updated]

charts/gameplane/crds/
  *.yaml                    [REGENERATED by make manifests — synced from operator/config/crd/]

operator/internal/controller/
  gameserver_controller.go  [EDIT] reconcileService() method (lines 433–490) to apply 
                                  pool/address as Service annotations/labels per cluster 
                                  flavor; managed-annotation handling (lines 474–530) 
                                  to preserve the translation
  gameserver_status.go      [EDIT] endpointsFromService() (lines 620–646) to extract 
                                  assigned address from Service status; computeConditions() 
                                  (lines 211–300) to emit distinct error messages for pool 
                                  misconfiguration

api/cmd/main.go            [EDIT] if needed for GameServer REST read/write paths 
                                  (existing handlers already expose types.go changes)

api/internal/handlers/      [READ] Existing GameServer handlers already marshal/unmarshal 
                                  CRD changes via the types.go mirror

web/src/types.ts           [EDIT] GameServerNetworking interface (~lines 394–401) 
                                  add addressPool?: string, address?: string

web/src/lib/endpoints.ts   [EDIT] ServerCreate helper (~lines 68–84) to include 
                                  pool/address in network configuration output

web/src/routes/
  CreateServer.tsx         [EDIT] Network step form (~lines 787–812) to collect 
                                  addressPool preference and optional explicit address request
  tabs/settings/Networking.tsx
                           [EDIT or CREATE if not present] Settings screen to display 
                                  current pool/address, allow editing, show pool-selection 
                                  warnings (e.g., pool ignored when mode != LoadBalancer)
  tabs/Overview.tsx        [EDIT] Endpoint display section (~lines 83–205) to show 
                                  assigned address and pool name
```

### Source Code — Track A (Nuclear Option Module)

```text
modules/nuclear-option/       [NEW in gameplane-module submodule]
  module.yaml                 Metadata (name, description, supported versions)
  template.yaml               GameTemplate with resource defaults (8–16 GB RAM, 
                              2–4 CPU, 30 GB storage), port config (UDP 7777 game, 
                              7778 query, TCP 7779 remote-command opt-in), config 
                              schema (name, password, max-players, mission-rotation)
  specs.md                    [REQUIRED by FR-027] Setup, configuration, remote-console 
                              command syntax, resource requirements, known limitations, 
                              ports, log locations
  README.md                   User-facing documentation
  icon.png                    [optional] Module icon

agent/internal/               [EDIT to add new rcon protocol]
  console/                    Existing console protocol handlers (source, telnet, websocket, etc.)
  
  rcon/
    nuclearoption.go          [NEW] Nuclear Option remote-command protocol handler:
                              - Asymmetric framing: request = 4-byte LE length + JSON; 
                                response = 4-byte status code + 4-byte length + JSON
                              - Commands: get-player-list, kick-player, banlist-add, 
                                banlist-remove, send-chat-message, set-next-mission
                              - Auth: none (pod-internal loopback only, enforced by 
                                agent sidecar configuration)
                              - Port: TCP 7779 (configurable via template)

agent/internal/console/       [EDIT to register new protocol]
  console.go                  Add "nuclearoption" to protocol allowlist

test/e2e/                     [NEW test, EXCLUDED from all CI buckets]
  nuclearoption_join_test.go   Real protocol-level join test:
                              - Boots a Nuclear Option GameServer
                              - Implements hand-rolled UDP 7777 client per game wire format
                              - Connects and verifies player appears in server
                              - Probe proven to fail against dead address, succeed against real
                              - t.Parallel(), unique resource names per test
                              - JoinDepth: JOINED (per feature 001 data model)

test/e2e/buckets.sh          [EDIT] Add nuclear-option test to bot-heavy bucket with 
                              comment: "8–16 GB footprint, excluded from default CI, 
                              runnable on demand"

docs/game-coverage.md        [EDIT] Add Nuclear Option row:
                              | Nuclear Option | covered-deferred | bot-heavy | 
                              lastVerified: [date of next manual run] | UDP 7777, 
                              remote protocol undocumented, see specs.md |
```

## Key Technical Decisions

**Decision 1: Ship Track B First**
Track A and Track B are independent (no shared code). Track B has no external unknowns and is independently deployable. Track B should ship in its own PR to unblock product value (pool assignment) before Track A's more exploratory remote-command protocol is finalized. Rationale: reduces risk, allows parallel work, proves pool assignment doesn't break existing servers before adding a new game.

**Decision 2: CRD Shape for Track B**
Add two typed fields to `GameServerNetworking`:
- `addressPool?: string` — operator-requested pool name (e.g., "production-us-east")
- `address?: string` — operator-requested explicit address (e.g., "198.51.100.42")

The operator's reconcileService() translates these into cluster-vendor-specific Service mutations:
- **MetalLB**: sets `metallb.io/address-pool` annotation
- **Cilium**: sets label `gameplane.local/lb-pool=<value>` (a Gameplane convention; cluster admin must mirror this key in CiliumLoadBalancerIPPool's `spec.serviceSelector` for pool selection to bind) and annotation `lbipam.cilium.io/ips` for explicit addresses
- **Fallback**: uses `serviceAnnotations` map as escape hatch for vendor-specific pool hints

Rationale: maintains backward compatibility (both fields optional), supports portability across vendors (single typed input, vendor-specific output), allows error reporting on invalid pool names (FR-020).

**Decision 3: Address Manager Abstraction**
Do NOT hard-code MetalLB-specific logic into the controller. Instead:
- Define a "flavor" enum in the template/chart (values: `metallb`, `cilium`, `none`)
- Operator checks cluster's address-manager configuration (Helm value or auto-detect via API server)
- Per-flavor translation in reconcileService() applies the right annotation/label scheme
- Status reporting reads back `Service.status.loadBalancer.ingress[0].ip` (K8s standard)

Rationale: satisfies FR-022 (CNCF-standard compatibility). If the cluster has no load-balancer support, pool preference is silently ignored (Service stays ClusterIP, operator reports no address assigned).

**Decision 4: Address Assignment Crux Risk**
MetalLB selects pool via Service **annotation** (`metallb.io/address-pool: pool-name`), but Cilium selects via Service **label** (`gameplane.local/lb-pool: pool-name`) matched by CiliumLoadBalancerIPPool selector. One operator field must produce two different Service mutations. Solution:
- Operator reads cluster flavor from Helm values or auto-detects via API server resourceVersion on LB controller
- reconcileService() applies the correct annotation OR label per flavor
- Status always reads back from `Service.status.loadBalancer.ingress[0].ip`
- Never set deprecated `Service.spec.loadBalancerIP` (K8s 1.24+)

Rationale: supports heterogeneous clusters. If auto-detect fails, defaults to "no pool selection" (Service gets default pool or fails to assign); operator sees error in status.conditions.

**Decision 5: Track A Requires NEW Agent Console Protocol**
The existing agent console protocol allowlist (source, telnet, websocket, battleye, satisfactory, palworld, none) does not cover Nuclear Option's format. Add a new `nuclearoption` protocol handler in `agent/internal/console/`:
- Protocol type: TCP 7779 (from spec assumptions, must be verified at runtime)
- Request framing: 4-byte little-endian length + JSON payload
- Response framing: 4-byte status code + 4-byte length + JSON payload (ASYMMETRIC)
- Commands documented in module `specs.md`: get-player-list, kick-player, banlist-add, banlist-remove, send-chat-message, set-next-mission
- Authentication: none (pod-loopback only)
- Dashboard: commands flow through existing Remote Console UI (no new screens)

Rationale: game's protocol is undocumented (third-party docs only); asymmetric framing is unusual and must be confirmed against real server. Test-driven approach: E2E test exercises each command, catching protocol drift if the game updates.

**Decision 6: Security Constraint — Pod-Internal Remote-Command Port**
The remote-command port (TCP 7779, unauthenticated) MUST remain pod-internal. Template MUST NOT expose it in the Service. Agent sidecar reaches it over loopback (127.0.0.1:7779), operator cannot reach it externally. Dashboard displays an "unauthenticated" warning in the Remote Console UI. Rationale: no authentication means the port is an internal attack surface; exposing it externally would invite unauthorized administration. The operator (who has dashboard access) is already trusted; the agent sidecar is in the same pod.

**Decision 7: Blocking Risk Resolution — Game Availability**
Publisher-official documentation (Steam app store listing for app 3930080) confirms:
- Dedicated server binary is available via SteamCMD with `+login anonymous` (no base-game ownership required)
- Native Linux binary `NuclearOptionServer.x86_64` exists and is not a compatibility layer
- Template runs SteamCMD at container start, same pattern as existing Steam games (Factorio, Valheim, etc.)

Gameplane redistributes nothing. This resolves the spec's "BLOCKING RISK" (unverified precondition). Remaining undocumented details (on-disk log path, per-command JSON response shapes) are marked UNVERIFIED and must be confirmed during implementation.

**Decision 8: Wake-on-Connect OUT OF SCOPE for v1**
Nuclear Option module template will NOT declare a `wakeProtocol` field. Idle auto-sleep and wake-on-connect are v1 features, but a dedicated `gameproto` handshake parser for Nuclear Option's UDP 7777 join wire format is deferred to a future feature. Until then, sentinel falls back to its generic UDP packets-in-window heuristic when a server is asleep. This heuristic is not game-specific and is already proven to work for UDP-only games. Rationale: the handshake parser requires access to an authoritative game binary or protocol documentation; the publisher's docs do not cover the wire format. Adding it later is straightforward (gameproto package, no operator changes needed).

**Decision 9: Player Display Names Resolved in the API Server via Steam Web API — DECIDED, GATE CLOSED**

*Status: **DECIDED and closed.** Spec FR-007 and User Story 3 / Acceptance Scenario 1 stand as written and are NOT amended. Do not re-open or re-litigate this; the full design is below and the implementation is tasked as T081–T104 in tasks.md.*

The Nuclear Option dedicated server cannot supply display names: the publisher's own protocol documentation states `get-player-list` "returns only the steamId and faction fields (the displayName field has been removed since the server runs headlessly and does not cache names)" and directs integrators to "fetch steam name using Steam's Web API". Gameplane therefore hydrates names itself.

Design, as decided:

- **Location — the API server (`api/internal/…`), not the agent.** This is forced by the cluster's own network policy, not by style preference: `charts/gameplane/templates/networkpolicies.yaml` installs a `default-deny-egress` NetworkPolicy in the games namespace (policy at line 24, `podSelector: {}` at line 28) that applies to **every** pod in that namespace and opens only DNS. Game pods get outbound internet access solely through the opt-in `allow-game-public-egress` policy (line 149), which exists for SteamCMD/asset/mod downloads. Putting the resolver in the agent sidecar would mean punching a new egress hole into every game pod *and* distributing the Steam Web API key to every game pod. The API server runs in the control-plane namespace, so it yields exactly one egress path, one Secret, and one shared cache.
- **The agent's contract is unchanged.** The agent returns exactly what the game returns — `steamId` and `faction`. Name hydration is a presentation concern layered on top in the API, consistent with the project rule that the API is a UX layer and never re-implements game behavior.
- **Outbound calls route through `netguard`, strict policy.** Steam's Web API is a public internet endpoint, so the resolver uses `netguard.IsPublic` (the strict policy the agent uses for mod downloads), not the permissive `IsAllowed` the operator uses for self-hosted registries.
- **Endpoint and batching.** `ISteamUser/GetPlayerSummaries/v2`, which accepts up to 100 `steamids` in a single call — the resolver batches a player list into one request rather than issuing one request per player.
- **The key is an optional credential.** Provisioned as a Kubernetes Secret, surfaced through a Helm value, never logged, never returned to the browser, never committed. Absent by default.
- **Graceful degradation is mandatory.** When the key is absent, Steam is unreachable, the call times out, or an individual id fails to resolve, the player list MUST still render with the raw Steam ID in place of the name. Name resolution must never block, fail, or error the player-list response. SC-004 requires a moderation-command result within 5 seconds, so the lookup carries a hard bounded timeout and degrades rather than exceeding it.
- **Cached with a TTL** so repeated player-list calls do not hammer Steam.
- **Steam ID remains the identifier.** Kick, ban, and unban continue to key on Steam ID. The display name is presentation-only and must never become the identifier used for a moderation action.

Rationale: it keeps the promised UX (FR-007 / US3-AC1) intact, confines a third-party dependency and a credential to a single control-plane component that already has an egress path, and reuses the repo's existing SSRF dial-guard instead of inventing a second outbound-HTTP policy.

**Decision 10: Players Capability Parsing Strategy for JSON Response — DECIDED 2026-10-09: Option 2**

*Status: **DECIDED** by the maintainer on 2026-10-09 (project thread decision card "Names + faction"). Implemented additively so no existing client breaks: when the list command's output is a JSON object with a `Players` array, the agent's `/players` snapshot keeps `players` as the flat list of Steam IDs and adds `entries: [{steamId, faction}]` (`agent/internal/players/players.go`); the API adds `displayName` to those entries (`api/internal/ws/players_hydrate.go`). The dashboard rendering (T095/T096/T102) waits for the Pencil design pass.*

The agent's player-list capability (agent/internal/caps/caps.go, type `PlayerList`, lines 115–123) is currently structured around text-based RCON output: it declares a `Command` (the RCON command to run) and an optional `EntryRegex` (regex applied over the console text output to extract player names). The `Snapshot` struct returned by the `/players` endpoint (agent/internal/players/players.go, lines 57–64) carries a `Players []string` field populated by this regex-parsing flow.

Nuclear Option's remote-command protocol returns structured JSON instead: `{"Players": [{"steamId": "...", "faction": "..."}]}` per the official docs (contracts/nuclear-option-remote-command.md). A regex over rendered JSON is semantically wrong and fragile. Three options exist:

1. **Regex over rendered JSON (least preferred)**: Convert the JSON response to a string, apply the regex to extract player names, populate `Snapshot.Players` with the result. Consequence: defeats the purpose of having structured data, makes the parsing path unmaintainable (regex on stringified JSON changes on every protocol version), and does not capture the additional data (faction, Steam ID) the JSON already carries. Unblocks US3-AC1 (player list visible), idle auto-sleep, and the Players tab, but at high technical debt.

2. **Protocol-aware parser (recommended)**: Teach the agent to understand that `nuclearoption` returns JSON, decode it into a structured format, populate not just player names but also Steam IDs and faction fields. Requires extending the `Snapshot` struct or introducing a protocol-specific response type. Consequence: agent code gains protocol-specific branching (similar to how different RCON protocols are already handled in agent/internal/rcon/), but the result is maintainable, reuses the structured data the protocol provides, and unblocks US3-AC1, idle auto-sleep, and the Players tab with richer information. Aligns with the design principle that agent protocols are allowed to be game-specific.

3. **No players capability for v1 (fallback)**: Nuclear Option template does not declare a `PlayerActions` capability in its spec.capabilities. Consequence: the agent returns `online: -1` (unknown) from the `/players` endpoint per the code path at player/players.go lines 118–127, the Players tab in the dashboard shows "Unavailable for this game", idle auto-sleep cannot use players-based triggers, and remote moderation (kick, ban) continues to work via the Remote Console with raw Steam IDs (the operator must know the ID without the dashboard helping). This unblocks the module from shipping but defers the full player-list UX to a future v1.x release.

**Recommendation**: Option 2 (protocol-aware parser). Rationale: it preserves the feature value promised in US3-AC1 and spec FR-007 (operator can see player list), enables richer player information (faction, Steam ID), aligns with the design pattern already established for game-specific protocols (agent code is allowed to know about protocol details), and is implementable without breaking the existing capability contract. The agent's console package already routes different protocols to different handlers (nuclearoption, satisfactory, palworld, etc. in agent/internal/console/); a protocol-specific JSON decoder for Nuclear Option is consistent with that pattern.

**Alternative (if Option 2 proves too complex during implementation)**: Fall back to Option 3 (no players capability v1). This is safe for the module's core gameplay (the operator can still run remote commands by Steam ID), but leaves the dashboard's Players tab and idle auto-sleep at reduced capability for this game until the protocol-aware parser is implemented in a follow-up release.

**Decision 11: RCON Password Secret Minting for Unauthenticated Protocols — DECIDED 2026-10-09: template switch**

*Status: **DECIDED** by the maintainer on 2026-10-09: instead of a protocol check, GameTemplate gains `spec.rcon.authentication` (default `true`, today's behaviour). `false` means the console has no authentication, so the operator mints no `<gs>-rcon` Secret, mounts nothing for the agent and injects no password env; a CEL rule rejects `false` together with `passwordSecretRef`, `passwordEnv` or `passwordFile` (`operator/api/v1alpha1/gametemplate_types.go`, `operator/internal/controller/gameserver_rcon.go`). The Nuclear Option template sets `authentication: false`. The options below are kept for history.*

The operator's `reconcileRCONSecret` function (operator/internal/controller/gameserver_rcon.go, lines 66–100) ensures that whenever a template declares an RCON interface, a per-GameServer Secret is minted (unless the template references an external PasswordSecretRef or uses a game-managed password file). The Secret holds a randomly-generated password, mounted into the agent sidecar at `/etc/gameplane/rcon` for use over the RCON protocol.

Nuclear Option's remote-command protocol has **no authentication of any kind** (spec Decision 6, confirmed in contracts/nuclear-option-remote-command.md, Enablement section: "The remote-command protocol defines **NO authentication mechanism, NO password validation, and NO handshake**"). The protocol runs on TCP 7779 as a pod-local loopback-only port (never advertised to external clients).

Two strategies exist:

1. **Exempt `nuclearoption` from Secret minting**: Modify `resolveRCON` to check the template's protocol type (e.g., a new `RCON.Type` field or a convention based on the port number) and skip Secret generation for unauthenticated protocols. Consequence: `reconcileRCONSecret` returns early for Nuclear Option templates, no Secret is created, the agent is mounted with no password file, and any downstream code that tries to read `--rcon-password-file` must handle the absence gracefully. Requires auditing: (a) agent code — agent/internal/rcon/ — to confirm it never assumes the password file exists (it should handle the absent case via a `--rcon-password-file` flag default or explicit nil check); (b) API code — api/internal/handlers/ — to confirm it never tries to read a Secret for unauthenticated-protocol GameServers; (c) dashboard — web/src/ — to confirm it never tries to mount or display a password field when the protocol is unauthenticated. If all three handle absence correctly, this is a clean solution with no dead weight.

2. **Accept dead Secret per server (lower friction)**: `reconcileRCONSecret` continues to mint a Secret for all RCON templates, even Nuclear Option. The Secret is created and mounted but never read (since the protocol has no password). Consequence: every Nuclear Option GameServer gets a `<name>-rcon` Secret with an unused random password; it takes up etcd space, complicates backup/restore (the Secret must be backed up even though it serves no function for this game), and violates the principle of creating only necessary artifacts. The upside: no code changes required, and if the upstream protocol is ever updated to require authentication, the Secret is already in place.

**Recommendation**: Option 1 (exempt `nuclearoption` from Secret minting). Rationale: the dead Secret serves no function and consumes resources unnecessarily; the cleanup is straightforward (a one-line check in `resolveRCON`), and the agent code already handles the absent-password case gracefully (the agent does not require a password to run an unauthenticated protocol). This aligns with the design principle that only necessary components are created. If the future v1.x update to Nuclear Option requires authentication, the Secret-minting logic is re-enabled for that protocol in the same change that adds the password.

**Verification steps**: (1) Confirm agent/internal/rcon/ handles the absent password file (grep for `--rcon-password-file`, verify the flag default is safe or that the code checks for nil); (2) Grep api/internal/handlers/ for references to RCON Secrets or password fields to confirm no downstream code breaks; (3) Grep web/src/ for RCON password display/input logic to confirm it does not assume all RCON-capable games have a password field to show.

## Residual Unknowns

**Unverified 1: On-Disk Log Path**
Spec Verification Claim 5: Nuclear Option server logs are assumed to be in a standard game directory (e.g., `/game/logs/`, `~/saves/logs/`) but exact path is undocumented. Agent pod's read permissions must be verified. **Action if false**: if logs are inaccessible, operational visibility degrades; backup of config/ban-list still works; documentation notes the limitation.

**Unverified 2: Per-Command JSON Response Body Shapes**
Spec Verification Claim 3: Remote-command protocol framing (4-byte status + 4-byte length + JSON) is assumed from third-party docs. Exact field names/types in each response are undocumented — with the single exception of `get-player-list`, whose body is documented upstream as `{"Players": [{"steamId", "faction"}]}` with **no name field** (see Decision 9; the name is hydrated in the API server, never on the wire). **Action if false**: E2E test against real server fails, signals the drift, moderation commands are reworked to match actual server behavior. Module coverage status becomes blocked-doc if reverse-engineering is required.

**Unverified 3: Readiness Signal**
Spec Verification Claim 4: Assumed the dedicated server exposes a signal (log pattern, status port, state file) distinguishing "process started" from "accepting players." If no such signal exists, dashboard readiness must conservatively infer from the same real-protocol probe used by the E2E join test, increasing readiness-check latency. **Action if false**: remote console reports "ready" only after a successful join probe, not just process liveness.

**RESOLVED — Player List Display Name** (no longer an unknown)
The conflict between spec FR-007 / US3-AC1 and the server returning only `steamId` and `faction` is **decided**: the API server hydrates display names via a batched, cached, netguard-guarded Steam Web API lookup that degrades to the raw Steam ID. See **Key Technical Decisions → Decision 9** for the full design and rationale. The spec is unamended and this gate is closed.

These unknowns do NOT block the start of implementation but MUST be resolved before Track A ships. Track B has no unknowns and can ship immediately.

## Complexity Tracking

No constitution violations. No complexity justifications needed. The design is straightforward:
- Track B is a structured CRD extension with vendor-specific translation in the operator; no new concepts.
- Track A is a new game module and a new agent protocol; both follow established patterns (game modules use existing template machinery; agent protocols follow existing console-protocol framework).
- E2E test design is clear: real join probe, proven both ways (fail on dead address, succeed on real listener).
- Heavy game footprint is handled by excluding test from CI buckets, not deleting it.

