---

description: "Task list for feature 002 — Nuclear Option Module & Load-Balancer IP Pool Override"
---

# Tasks: Nuclear Option Module & Load-Balancer IP Pool Override

**Input**: Design documents from `/specs/002-nuclear-option-ip-pool/`

**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/address-pool-api.md, contracts/module-contract.md, contracts/nuclear-option-remote-command.md, quickstart.md

**Tests**: Test tasks ARE included — the project constitution (Principle I, E2E-Tested Delivery) and the coverage gates (operator 72%, api 80%, agent 90%, gameaction 91%, netguard 91%, web 92/76/82/92) make them mandatory, not optional.

**Organization**: Tasks are grouped by user story so each story can be implemented, tested, and shipped as independently as its declared dependencies allow.

## ⚠️ Read this before starting

This feature is **two independent tracks that share no code**.

- **Track B — load-balancer address-pool override** (US2, US4, US6). **No external unknowns.** Everything it needs is decided: two typed fields on `GameServerNetworking`, MetalLB annotation translation, Cilium label + annotation translation, status conditions, dashboard surfacing. **Track B ships FIRST and is the MVP.**
- **Track A — Nuclear Option game module** (US1, US3, US5). **The critical gate is T016**; display-name decision is closed.
  1. **The display-name spec conflict (T022) — CLOSED.** The maintainer resolved it: display names are hydrated through a **Steam Web API lookup** (`ISteamUser/GetPlayerSummaries/v2`) that lives in the **API server** (`api/internal/steam/`), dials through this repo's `netguard` SSRF dial-guard using the **strict `IsPublic`** policy, takes an **optional** key from a Kubernetes Secret, batches ids (≤100 per call), and keeps results in a **small, bounded, in-process RAM cache** — LRU-capped by entry count, a ~12-hour positive TTL, a shorter negative TTL for ids that never resolve, and single-flight de-duplication, with **no** database table, migration, Redis or cross-replica sharing — and **degrades to the raw Steam ID** rather than ever blocking or failing the player list. **FR-007 and US3 Acceptance Scenario 1 stand exactly as written and are NOT amended.** The implementation is tasked as T081–T104 in Phase 6.
  2. **The true blocking gate is T016 (server availability).** T018 (wire framing) is **already satisfied** by the contract's publisher-official status; T019 (per-command response body shapes for four unverified fire-and-forget commands) is informational and does not block transport implementation. See the evidence note in Phase 2 below for details.

Additional standing constraints for every task below:

- **No local execution.** Builds, tests, lint, codegen and cluster runs happen on GitHub Actions (or the sanctioned remote build host), never on the maintainer's machine. Codegen output is still committed alongside the source change that triggered it; CI is the gate that catches drift.
- **Two-repo change.** `modules/` is a git submodule pointing at the separate `gameplane-module` repo. Module files are committed **there**, then the submodule pointer is bumped **here** (T131). Nothing under `modules/` is visible to this repo's CI until that pointer moves — which is why the coverage-table row (T130) must land in the pointer-bump commit.
- **Never set the deprecated `Service.spec.loadBalancerIP`** (deprecated since K8s 1.24). Pool/address selection is expressed only through annotations and the Gameplane label.
- **Wake-on-connect is out of scope for v1**; the Nuclear Option module declares no wake-on-connect protocol.
- **The remote-command port (TCP 7779) has no authentication.** It must never be advertised in the Service, never exposed outside the pod, and is reached only by the agent sidecar over pod-local loopback.
- **The Steam Web API key is a credential.** It is provisioned as a Kubernetes Secret, surfaced through one Helm value, never logged, never committed, and never returned to the browser. Everything the dashboard keys on today (kick, ban, unban) keeps keying on the **Steam ID**; the resolved name is display-only and must never become an identifier.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: `US1`–`US6`. Setup, Foundational and Polish tasks carry **no** story label.
- Every task names an exact file path. Paths that do not exist yet are marked `(NEW)`.

## Path Conventions

- Operator (Go): `operator/api/v1alpha1/`, `operator/internal/controller/`, `operator/cmd/`
- API (Go): `api/internal/ws/`, `api/internal/steam/` (NEW), `api/internal/notify/` (the structural precedent for outbound HTTP), `api/cmd/`
- Agent (Go): `agent/internal/rcon/`, `agent/cmd/`
- Shared Go packages: `netguard/` (SSRF dial-guard — `IsAllowed` permissive, `IsPublic` strict)
- Dashboard (TS/React): `web/src/routes/`, `web/src/lib/`, `web/src/types.ts`
- E2E: `test/e2e/`
- Game module (submodule `gameplane-module`): `modules/nuclear-option/`
- Design source: `design.pen` (Pencil MCP only) with exports in `design-export/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Make both tracks workable in the checkout before any code is written. Track B has no load-balancer provider today — `metallb` appears nowhere in `deploy/`, `charts/` or `test/`, and kind ships no LoadBalancer implementation — so installing one is Setup work, not an assumption.

- [X] T001 Initialize the `modules/` git submodule (`git submodule update --init`) and confirm the pinned `gameplane-module` commit resolves, per /home/valgul/project/kubernetes-game-dashboard/.gitmodules
- [X] T002 Create the module directory skeleton /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/ (NEW) inside the `gameplane-module` submodule checkout (requires T001 — the directory does not exist until the submodule is populated) — 2026-10-09: already implemented: modules/nuclear-option/ exists (module.yaml, template.yaml, README.md).
- [X] T003 [P] Install MetalLB (controller + speaker, pinned manifest) and define the two test address pools `pool-us-east` and `pool-us-west` as `IPAddressPool` + `L2Advertisement` CRs, with ranges carved from the kind docker bridge subnet (e.g. `172.18.255.100-172.18.255.110` and `172.18.255.200-172.18.255.210`) and never from TEST-NET-1 `192.0.2.0/24`, which is unroutable from kind nodes, in the CI e2e bootstrap /home/valgul/project/kubernetes-game-dashboard/deploy/kind/e2e.sh
- [X] T004 [P] Install MetalLB and define the same two pools in the developer bootstrap /home/valgul/project/kubernetes-game-dashboard/deploy/kind/up.sh
- [X] T005 [P] Register the three Track B design nodes (`f1Vga`, `J5pjJ3`, `EZFW0`) as in-scope for this feature in /home/valgul/project/kubernetes-game-dashboard/design-export/MANIFEST.md

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented. Track B foundational work (T006–T015) unblocks US2/US4/US6; Track A gates and foundations (T016–T036) unblock US1/US3/US5.

**⚠️ CRITICAL**: No user story work can begin until the relevant half of this phase is complete. **Track A's blocking gate is T016** — if the Steam app 3930080 does not download with a native Linux binary, the entire track dies. T017/T020/T021/T053 are observation gates (ports, readiness signal, log location) that flow into the module spec. T018 (wire framing) is effectively **already satisfied** (see evidence note below); T019 (per-command response bodies) gates only four fire-and-forget commands (banlist-add, send-chat-message, banlist-remove, set-next-mission) and does **not block** the transport implementation or fire-and-forget execution — see evidence note below. T022 is no longer a gate — it is a decision **record** (see below) whose implementation is tasked in Phase 6.

### Track B foundation — CRD, flavor selection, Service translation

- [X] T006 Add optional `addressPool` (MaxLength 63, DNS-1123 subdomain pattern) and `address` (MaxLength 45, **no CEL regex** — this repo has had CRDs rejected on CEL cost) to the `GameServerNetworking` struct (~lines 226–278), add the reconciler-owned `pool` field to `GameServerEndpoint` (~lines 494–511), and declare the `AddressAssignment` condition type in /home/valgul/project/kubernetes-game-dashboard/operator/api/v1alpha1/gameserver_types.go
- [X] T007 Regenerate the CRD artifacts (`make generate && make manifests`, run on CI or the sanctioned remote build host — never locally) and commit /home/valgul/project/kubernetes-game-dashboard/operator/api/v1alpha1/zz_generated.deepcopy.go, /home/valgul/project/kubernetes-game-dashboard/operator/config/crd/*.yaml, /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/crds/*.yaml and /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/crd-manifests/*.yaml (the copy the pre-upgrade apply hook ships — omitting it means existing clusters never get the new fields) in the SAME commit as T006, per project rule 7
- [X] T008 Add the explicit cluster address-manager flavor setting (`metallb` | `cilium` | `none`, defaulting to `none`) as an operator flag/env in /home/valgul/project/kubernetes-game-dashboard/operator/cmd/main.go
- [X] T009 [P] Expose the address-manager flavor as a Helm value in /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/values.yaml
- [X] T010 Wire the address-manager flavor into the operator Deployment's container args/env in /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/templates/operator.yaml (the operator Deployment and its ClusterRole live here; there is no `templates/deployment.yaml` in this chart)
- [X] T011 Set the flavor to `metallb` on the chart install so the kind clusters actually translate pool requests instead of silently no-opping, in /home/valgul/project/kubernetes-game-dashboard/deploy/kind/e2e.sh and /home/valgul/project/kubernetes-game-dashboard/deploy/kind/up.sh
- [X] T012 Extend the managed-key mechanism (currently `gameplane.local/managed-service-annotations`, annotations only, ~lines 474–540) to also track managed **labels**, so the Cilium `gameplane.local/lb-pool` label is removed cleanly when the pool preference is unset, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go
- [X] T013 Implement the MetalLB branch of pool/address translation in `reconcileService()` (~lines 433–490) — annotations `metallb.io/address-pool` and `metallb.io/loadBalancerIPs`, registered through the managed-key mechanism, never writing the deprecated `spec.loadBalancerIP` — in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go
- [X] T014 Implement the Cilium branch of pool/address translation in `reconcileService()` — label `gameplane.local/lb-pool` (a Gameplane convention the cluster admin must mirror in `CiliumLoadBalancerIPPool.spec.serviceSelector`; Cilium does not recognise it natively) plus annotation `lbipam.cilium.io/ips`, both registered through the managed-key mechanism — in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go
- [X] T015 Implement the remaining translation branches in `reconcileService()` — flavor `none`: mutate nothing but record that a pool/address was requested with no address manager configured (feeding the `NoAddressManagerConfigured` reason in T106, so the request never silently falls back to the default pool); non-LoadBalancer expose modes: ignore and flag — in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go

### Track A gates — unverified claims, the remaining open decisions, and the recorded ones

- [ ] T016 [P] Verify Claim 1 (dedicated-server availability): Steam app 3930080 downloads via SteamCMD with `+login anonymous` without owning base game 2168680, and ships an executable native Linux `NuclearOptionServer.x86_64`; record the result (or the licensing blocker) in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md (NEW)
- [ ] T017 Verify Claim 2 (network ports): confirm the running server listens on UDP 7777 (game, advertised), UDP 7778 (query) and TCP 7779 (remote-command, opt-in via the `-ServerRemoteCommands` launch flag, never advertised) and append the observed ports to /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [X] T018 [P] **GATE** — Verify Claim 3a (remote-command wire framing) against a real server: request = 4-byte little-endian length counting ONLY the UTF-8 JSON body bytes + that JSON; response is ASYMMETRIC = 4-byte status code + 4-byte body length (0 when absent) + body; status codes 2000 Success, 4000–4005 client errors, 5000–5002 server errors — recording the confirmed framing in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/contracts/nuclear-option-remote-command.md — 2026-10-09: already implemented: satisfied by the live verification in contracts/nuclear-option-remote-command.md § "Live Protocol Verification — 2026-08-22" and spec.md Verification Required.
- [ ] T019 **GATE — STILL OPEN.** Verify Claim 3b: capture the **per-command JSON response body shape** for every command this feature implements (`get-player-list`, `kick-player`, `banlist-add`, `banlist-remove`, `send-chat-message`, `set-next-mission`) and append them to /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/contracts/nuclear-option-remote-command.md
- [ ] T020 Verify Claim 4 (readiness signal): confirm the line `[DedicatedServerManager] Waiting for Players before loading next map` appears in `./logs/server-<timestamp>.log` when the server starts accepting players, and append it to /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [ ] T021 Verify Claim 5 (on-disk log location and format): confirm the pod-accessible log path and format and append it to /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [X] T022 **DECISION RECORDED — the display-name question is CLOSED. Do not reopen, re-litigate or re-surface it.** The dedicated server's player list returns only `steamId` and `faction` (upstream removed `displayName` because the server runs headless and caches no names, and directs integrators to fetch the name from Steam's Web API). **Resolution: add a Steam Web API display-name lookup; FR-007 and US3 Acceptance Scenario 1 stand as written and are NOT amended.** The lookup lives in the **API server** (`api/internal/steam/`), never the agent — /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/templates/networkpolicies.yaml declares `default-deny-egress` with `podSelector: {}` (~line 24) over every pod in the games namespace, so an agent-side resolver would need a new egress hole in every game pod plus the Steam key distributed to every game pod, whereas the control-plane namespace gives one egress path, one Secret and one shared cache. Write this decision, its rationale, its constraints (strict `netguard.IsPublic` dialling, optional Secret-provided key, batched `GetPlayerSummaries`, bounded timeout inside the SC-004 five-second budget, a small bounded in-process LRU cache with an hours-scale positive TTL plus a shorter negative TTL and single-flight de-duplication — explicitly **not** a DB table, a migration, Redis or a cross-replica cache, mandatory degradation to the raw Steam ID, moderation still keyed on Steam ID) and its pointer to the implementation tasks **T081–T104** into /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/spec.md — 2026-10-09: done: written up in spec.md § Decision Record: Player Display Names (T022).
- [X] T023 **DECISION** — The players capability is parsed by regex over console text (`PlayerList.EntryRegex` in /home/valgul/project/kubernetes-game-dashboard/agent/internal/caps/caps.go, ~lines 96–123), which is a poor fit for this protocol's JSON response body; the Players tab, `status.agent.playersOnline` and idle auto-sleep all flow through it. Write up the options (regex over the rendered JSON, a protocol-aware parser, or declaring no players capability for v1) with consequences and record the outcome in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/plan.md
- [X] T024 **DECISION** — The operator mints and mounts a per-GameServer RCON password Secret (`reconcileRCONSecret`, /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_rcon.go ~line 73) for every template declaring an RCON protocol, but the Nuclear Option remote-command port has no authentication; decide whether `nuclearoption` is exempted or a dead Secret per server is accepted, recording the outcome in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/plan.md

---

### ℹ️ Evidence Note — T018/T019 Framing Correction (2026-08-22)

**Background**: An earlier draft claimed T018 and T019 block all Track A stories. This was corrected after evidence review:

1. **T018 (wire framing verification) is already satisfied.** The contract `/specs/002-nuclear-option-ip-pool/contracts/nuclear-option-remote-command.md` (lines 1–8) states: "All details are **PUBLISHER-OFFICIAL** and transcribed exactly from the game's developer documentation... Primary Source: Shockfront Studios' official Nuclear Option Server Tools documentation." The request and response framing (4-byte little-endian length, asymmetric status+length+body response) are sourced from the publisher's own `ServerCommands/Readme.md` and are **not** marked UNVERIFIED. Thus T018's verification gate is already closed by the contract's publisher-official status.

2. **T019 (per-command response body shapes) does not block transport implementation or fire-and-forget execution.** Verification shows:
   - Every RCON transport in `agent/internal/rcon/*.go` (battleye, palworld, satisfactory, websocket, telnet, source) has the signature `Exec(cmd string) (string, error)` and returns no typed response struct.
   - In the contract, `get-player-list` (the only command whose response body must be decoded for the Players tab) has **no** UNVERIFIED marker; `banlist-add`, `send-chat-message`, `banlist-remove` and `set-next-mission` have **UNVERIFIED response body** markers but are fire-and-forget commands where success is determined by status code alone.
   - This repo's RCON pattern — returning `(string, error)` — never requires typed per-command response parsing. Hence T019 gates nothing that the implementation pattern actually needs.

3. **The real Track A precondition is T016.** If Steam app 3930080 does not download with a native Linux binary, the entire feature dies. T017/T020/T021/T053 are observation gates (confirming ports, readiness signals, log locations) that inform the module spec.

**Result**: Phase 2 framing, Dependencies & Execution Order, and Parallel Opportunities sections have been re-baselined to reflect this evidence. T018 and T019 no longer appear as blanket blockers; T016 is identified as the critical gate.

---

### Track A foundation — module bundle, agent protocol, protocol registration

- [X] T025 Author the module manifest (name, version, maintainer, game metadata, no wake-on-connect protocol) in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/module.yaml (NEW) — 2026-10-09: already implemented: modules/nuclear-option/module.yaml; no wake protocol declared.
- [ ] T026 Author the base server template — resources (2–4 CPU, 8–16 GB RAM, 30 GB storage) and ports (UDP 7777 game advertised, UDP 7778 query, TCP 7779 remote-command pod-local only, never advertised) — in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml (NEW)
- [X] T027 [P] Write the operator-facing module overview in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/README.md (NEW) — 2026-10-09: already implemented: modules/nuclear-option/README.md.
- [ ] T028 Write the FR-027 module spec — setup, configuration options, remote-console command syntax, resource usage, known limitations, ports and log locations, incorporating the verified findings recorded by T016, T017, T020 and T021 — in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [ ] T029 [P] Add the 256×256 module icon, recording its source and licence terms (the game's art is commercial; use only assets cleared for redistribution) alongside it, at /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/icon.png (NEW)
- [X] T030 Implement the `nuclearoption` console protocol transport — connect over pod-local loopback, 4-byte LE length-prefixed JSON request, asymmetric status+length+body response decode, status-code mapping, bounded body length, errors wrapped with `%w` — in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go (NEW) — 2026-10-09: already implemented: agent/internal/rcon/nuclearoption.go (framing, status codes, body cap).
- [X] T031 Register the protocol identifier exactly `"nuclearoption"` (one lowercase word) in the protocol dispatch switch (~lines 126–145, alongside `case strings.EqualFold(rconProtocol, "palworld")`) and in the `--rcon-protocol` flag help text (~lines 68–73) in /home/valgul/project/kubernetes-game-dashboard/agent/cmd/main.go — note `agent/internal/rcon/rcon.go` holds only the Source-protocol client and has no allowlist — 2026-10-09: already implemented: agent/cmd/main.go protocol dispatch registers "nuclearoption".
- [X] T032 Add `nuclearoption` to the RCON protocol enum marker `+kubebuilder:validation:Enum=source;telnet;websocket;battleye;satisfactory;palworld;none` (~line 992) in /home/valgul/project/kubernetes-game-dashboard/operator/api/v1alpha1/gametemplate_types.go — without this the apiserver rejects the module's `template.yaml` outright
- [X] T033 Regenerate and commit the CRD artifacts for T032 — /home/valgul/project/kubernetes-game-dashboard/operator/api/v1alpha1/zz_generated.deepcopy.go, /home/valgul/project/kubernetes-game-dashboard/operator/config/crd/*.yaml, /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/crds/*.yaml and /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/crd-manifests/*.yaml — in the SAME commit as T032, per project rule 7
- [X] T034 Add `"nuclearoption"` to the `RCON_PROTOCOLS` allowlist (~line 129) in the `gameplane-module` repo's validate.py, reached here at /home/valgul/project/kubernetes-game-dashboard/modules/validate.py — 2026-10-09: already implemented: modules/validate.py RCON_PROTOCOLS includes nuclearoption.
- [X] T035 Add `nuclearoption` to the enumerated `rcon.protocol` values (~line 701) in /home/valgul/project/kubernetes-game-dashboard/docs/module-authoring.md
- [X] T036 Configure the readiness probe to match the startup log line verified in T020, in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml — 2026-10-09: already implemented: readiness uses an exec probe on loopback TCP 7779 (tcpSocket cannot reach a loopback-only port; see the template comment) instead of the log line.

**Checkpoint**: Track B foundation (T006–T015) ready → US2 can start. Track A's critical gate T016 resolved (or documented as a blocker), its observations T017/T020/T021 recorded, its decisions T022 (recorded), T023, T024 settled and its foundation (T025–T036) landed → US1 can start; US3 and US5 additionally need the US1 image (see their phase headers). T018 (already satisfied) and T019 (informational) are no longer blocking gates.

---

## Phase 3: User Story 2 - Operator pins a game server's public address to a chosen load-balancer address pool (Priority: P1) 🎯 MVP

**Goal**: An operator can name an address pool when creating or editing a LoadBalancer-exposed game server, and the server reliably receives an address from that pool, shown in the dashboard.

**Independent Test**: On a kind cluster with the MetalLB install from T003/T004 and the flavor set to `metallb` (T011), create a LoadBalancer game server with `addressPool: pool-us-west`, and confirm the assigned address falls inside the `pool-us-west` range and is displayed with its pool name on the Server Detail overview within 30 seconds — with no Track A code present.

**Depends on**: T003/T004 (MetalLB), T006–T015 (Track B foundation). No Track A dependency of any kind.

### Design pass (Constitution Principle II — blocks all React work in this phase)

- [X] T037 [US2] Via the Pencil MCP server only (never hand-edit), add pool-preference and explicit-address fields to `f1Vga` (Create Server — Step 4 Network), a pool/address display + edit section **and its failed-assignment error state** to `J5pjJ3` (Server Detail — Settings · Networking), and the assigned address + pool name to `EZFW0` (Server Detail — Overview) in /home/valgul/project/kubernetes-game-dashboard/design.pen — then **explicitly ask the user to press Ctrl/Cmd-S**, because Pencil does not auto-save
- [X] T038 [US2] In one Pencil MCP session after the save (never two concurrent agents against the same in-memory document), re-export the three touched nodes as JSON to /home/valgul/project/kubernetes-game-dashboard/design-export/json/f1Vga.json, /home/valgul/project/kubernetes-game-dashboard/design-export/json/J5pjJ3.json and /home/valgul/project/kubernetes-game-dashboard/design-export/json/EZFW0.json and as screenshots to /home/valgul/project/kubernetes-game-dashboard/design-export/screenshots/f1Vga.png, /home/valgul/project/kubernetes-game-dashboard/design-export/screenshots/J5pjJ3.png and /home/valgul/project/kubernetes-game-dashboard/design-export/screenshots/EZFW0.png, in the same change as T037

### Operator status surface

- [X] T039 [US2] Extend `endpointsFromService()` (~lines 616–644) to populate `GameServerEndpoint.pool` from the requested pool (and from the address manager's own metadata only where it actually exposes one — MetalLB does not, so the requested pool is the honest source) alongside the assigned address, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_status.go
- [X] T040 [US2] Add the `AddressAssignment` condition to `computeConditions()` (~lines 211–300) with the success path `Assigned` (`Address 172.18.255.203 assigned from pool 'pool-us-west'`) and the transient reasons `AssignmentPending` / `ServiceNotReady`, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_status.go

### Dashboard

- [X] T041 [P] [US2] Mirror the CRD change with `addressPool?: string` and `address?: string` on the `GameServerNetworking` interface (~lines 394–401) **and `pool?: string` on the `GameServerEndpoint` interface (~lines 437–445)** — without the second, T045 does not compile under `strict` — in /home/valgul/project/kubernetes-game-dashboard/web/src/types.ts
- [X] T042 [P] [US2] Widen the file-local `networking` object type in `ServerCreate` (~line 76 — this file carries its own inline type, independent of `web/src/types.ts`) and include `networking.addressPool` and `networking.address` in the create payload when set (omitted when empty, for backward compatibility) in /home/valgul/project/kubernetes-game-dashboard/web/src/lib/endpoints.ts
- [X] T043 [US2] Add the optional pool-preference and explicit-address inputs to the Network step (~lines 787–812), including the "ignored unless exposure mode is LoadBalancer" affordance, in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/CreateServer.tsx
- [X] T044 [US2] Add the pool/address display and edit section (current assignment, pool preference field, explicit address field, exposure-mode warning) and render the `AddressAssignment` condition message — including a router link to the conflicting server when the reason is `AddressInUse` — **and extend the `setNet()` `cleaned` allowlist (~lines 45–64) to carry `addressPool` and `address`, or every unrelated settings save silently wipes them**, in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/settings/Networking.tsx
- [X] T045 [US2] Render the assigned address with its pool name in the endpoint list (~lines 83–205), falling back to the bare address when no pool is set, in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/Overview.tsx

### Tests

- [X] T046 [P] [US2] Cover the Network-step pool/address fields and the emitted payload in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/CreateServer.test.tsx
- [X] T047 [P] [US2] Cover the Networking settings display/edit round-trip, the exposure-mode warning, and a save of an unrelated field preserving `addressPool`/`address`, in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/settings/Networking.test.tsx
- [X] T048 [P] [US2] Cover endpoint rendering with and without a pool name in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/Overview.test.tsx
- [X] T049 [US2] Add envtest cases for CRD validation of both fields, MetalLB annotation translation, Cilium label + annotation translation, the `none` flavor no-op, managed-key cleanup when the pool is unset, changing `addressPool` on an existing server mutating the Service in place (US2 Acceptance Scenario 2), and unchanged behaviour for servers with no preference, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_envtest_test.go
- [X] T050 [US2] Add unit tests for endpoint pool extraction and the `Assigned` / `AssignmentPending` / `ServiceNotReady` conditions in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_status_test.go

**Checkpoint**: US2 is unit- and envtest-complete and independently shippable; its end-to-end proof is T119/T120 in Phase 9, which ship with it. **This is the MVP — stop here, validate, and ship Track B before starting Track A.**

---

## Phase 4: User Story 1 - Operator deploys a playable Nuclear Option server from the catalog (Priority: P1)

**Goal**: An operator finds Nuclear Option in the module catalog, fills in the configuration form, deploys, and the server becomes joinable within five minutes.

**Independent Test**: With the module bundle pushed to the registry, create a Nuclear Option GameServer from the dashboard catalog with default settings, and join it from a real game client on UDP 7777 within five minutes (SC-001) — no remote-console commands needed.

**Depends on**: T016 (availability gate), T025–T036 (module bundle + protocol registration).

- [ ] T051 [US1] Define the `configSchema` fields — `SERVER_NAME` (string), `MAX_PLAYERS` (int), `SERVER_PASSWORD` (password), `MISSION_ROTATION` (enum `sequence`|`random`), `MISSION_LIST` (comma-separated), each marked required/optional per FR-003 — in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml
- [X] T052 [US1] Render `DedicatedServerConfig.json` through `configFiles` with port overrides (7777/7778/7779, `IsOverride: true`) and the configSchema values, using the structure confirmed in T018/T019, in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml — 2026-10-09: already implemented: configFiles renders DedicatedServerConfig.json (ports left IsOverride:false, the server defaults are used).
- [X] T053 [US1] Declare the persisted on-disk paths (server config, ban list, mission files and logs) on the PVC mount so the stock backup framework captures them (FR-013) — if the SteamCMD install writes any of them outside the mount, move them — in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml — 2026-10-09: already implemented: single /data mount holds install, config, logs and ban list, so the stock backup captures them.
- [X] T054 [US1] **DECISION** — This repo has no module image-build convention: every module pins a third-party image by digest (e.g. `modules/palworld/template.yaml`) and `modules/build.sh` is an `oras push` bundle wrapper with no `docker build`. Choose **(A)** pin an existing published Nuclear Option dedicated-server image by digest, or **(B)** build and publish our own image from a new CI workflow, and record the choice with its consequences in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md — 2026-10-09: done: decided (B), own image, by the maintainer 2026-10-09: images/games/nuclear-option + images.yaml; module repinned to ghcr.io/gameplanepanel/gameplane/nuclear-option.
- [X] T055 [US1] If T054 chose (B), implement the container entrypoint — SteamCMD `+login anonymous +app_update 3930080`, launch `NuclearOptionServer.x86_64` with the remote-command flag bound pod-local, stream server logs to stdout — in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/Dockerfile (NEW) — 2026-10-09: already implemented: implemented at images/games/nuclear-option/{Dockerfile,entrypoint.sh}.
- [X] T056 [US1] If T054 chose (B), add the image build-and-publish workflow (buildx, push to GHCR, emit the digest; never a local `docker build`) in /home/valgul/project/kubernetes-game-dashboard/.github/workflows/module-image-nuclear-option.yaml (NEW) — 2026-10-09: already implemented: implemented in .github/workflows/images.yaml (matrix game: nuclear-option).
- [X] T057 [US1] Point `spec.image` at the resolved digest (never a floating tag) in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml — 2026-10-09: already implemented: template.yaml is digest-pinned; repinned to ghcr.io/gameplanepanel/... in the module PR (T054).
- [ ] T058 [US1] Author the real protocol-level join test `TestGameServer_NuclearOptionBot_Joined` — hand-rolled UDP 7777 join client, `t.Parallel()`, per-test unique resource names, and proof the probe **fails against a dead address AND passes against a real listener** — in /home/valgul/project/kubernetes-game-dashboard/test/e2e/nuclearoption_bot_e2e_test.go (NEW)
- [ ] T059 [US1] Bucket `TestGameServer_NuclearOptionBot_Joined` under `bot-heavy` — bucketed for the coverage audit, never executed by default CI because of its 8–16 GB footprint, still runnable on demand via `make test-e2e-bucket BUCKET=bot-heavy` — in the SAME commit as T058, because `buckets.sh verify` fails both on an unbucketed suite test and on a bucketed test that no longer exists, in /home/valgul/project/kubernetes-game-dashboard/test/e2e/buckets.sh
- [ ] T060 [US1] Assert catalog-to-joinable in under five minutes (module visible, config form renders every schema field, GameServer reaches Ready, join succeeds) — bucketing any new top-level `func Test…` this adds, per T059 — in /home/valgul/project/kubernetes-game-dashboard/test/e2e/nuclearoption_bot_e2e_test.go

**Checkpoint**: A default Nuclear Option server deploys and is playable.

---

## Phase 5: User Story 4 - Operator requests one specific fixed address so a published DNS record stays stable (Priority: P2)

**Goal**: An operator can pin one exact address to a server, and a request for an address already in use is rejected with a message naming the conflicting server rather than silently reassigning.

**Independent Test**: Create server A with an explicit address from `pool-us-east`, confirm the Service receives exactly that address; then create server B requesting the same address and confirm B reports `AddressAssignment=False / AddressInUse` naming server A within 30 seconds, while A keeps its address.

**Depends on**: T006–T015 and the `AddressAssignment` condition from T040 (Phase 3) — US4 is **not** independent of US2 and cannot land before it. The dashboard surface it needs is T044.

- [X] T061 [US4] Detect conflicts for an explicitly requested `spec.networking.address` by listing LoadBalancer Services cluster-wide (the operator already holds cluster-scoped `services` `get;list;watch`, so this needs no RBAC change) before applying the annotation, keeping re-requests by the same server idempotent, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go
- [X] T062 [US4] Emit the `AddressInUse` reason on the `AddressAssignment` condition with a message naming the conflicting server in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_status.go
- [X] T063 [US4] Add an envtest case where two GameServers request the same address and only the first is granted it in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_envtest_test.go
- [X] T064 [US4] Add an envtest case proving an address released when its GameServer is deleted becomes assignable to another server (US4 Acceptance Scenario 3) in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_envtest_test.go
- [X] T065 [P] [US4] Cover the explicit-address field and the rendered `AddressInUse` message with its link to the conflicting server in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/settings/Networking_more.test.tsx

**Checkpoint**: Fixed-address requests work and conflicts are explicit.

---

## Phase 6: User Story 3 - Operator administers a running Nuclear Option match remotely (Priority: P2)

**Goal**: An operator can list players, kick, ban/unban, broadcast chat and set the next mission from the existing Remote Console, each command returning within five seconds (SC-004). The player list shows Steam ID, display name and faction (FR-007 / US3 Acceptance Scenario 1) — the name coming from the Steam Web API resolver decided in T022, degrading to the raw Steam ID whenever it cannot.

**Independent Test**: With a running Nuclear Option server and one connected player, run each moderation command from the Remote Console and confirm the effect on the server (player disconnects, message appears in game chat, mission rotates) within five seconds — no new dashboard screens involved. Then, with the Steam key Secret absent, confirm the player list still renders every connected player with the raw Steam ID in the name column and kick still works.

**Depends on**: T022 (recorded decision), T023 (players capability decision), T024 (RCON password decision), T030/T031 (transport + registration), **and the whole of Phase 4 (US1)** — the independent test needs a running server, which needs the image from T054–T057. US3 is not independent of US1. T018's wire framing is already verified; T019 (unverified per-command response bodies) is informational and does not block implementation.

**Serialization warning**: T066, T068, T070, T071, T073 and T074 all edit the same file (`agent/internal/rcon/nuclearoption.go`) and T067, T069, T072, T075, T076, T077 and T078 all edit the same test file. They are **serial**, not parallel. The Steam-resolver tasks T081–T104 touch the API, chart, web and docs instead, so they are independent of the agent-side block — but T081, T082 and T089 are themselves serial on `api/internal/steam/resolver.go`, T085 → T086 are serial on `api/internal/steam/cache.go`, and T098 → T099 are serial on `api/internal/steam/cache_test.go`.

### Agent-side remote commands (returns exactly what the game returns: `steamId` + `faction`, never a name)

- [X] T066 [US3] Implement `get-player-list` (request `{"name":"get-player-list","arguments":[]}`, decode the status-2000 body into steamId/faction entries — the agent adds no name and performs no outbound lookup) in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go — 2026-10-09: done: maintainer chose structured entries (T023 Option 2). Implemented in agent/internal/players/players.go (Snapshot.Entries with steamId/faction, players kept as the flat Steam-ID list) rather than in nuclearoption.go, because the transport already returns the raw JSON body.
- [X] T067 [US3] Test `get-player-list` across populated list, empty list, 4003 JsonError and 5000 InternalServerError in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go (NEW) — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go.
- [X] T068 [US3] Implement `kick-player` (status 2000 success, 4005 BadArguments when the Steam ID is unknown) per FR-008 in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go — 2026-10-09: already implemented: kick-player is reachable through the generic Exec/command parser in agent/internal/rcon/nuclearoption.go; the live server returns 2000 even for unknown ids (spec.md Verification), so no 4005 path exists.
- [X] T069 [US3] Test `kick-player` with a valid ID, an unknown ID and an oversized ID in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go.
- [X] T070 [US3] Implement `banlist-add` (status 2000 success, 5002 ConfigError when the ban-list file is missing) per FR-009 in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go — 2026-10-09: already implemented: banlist-add via the generic Exec/command parser (nuclearoption.go).
- [X] T071 [US3] Implement `banlist-remove` (status 2000 success, 4005 BadArguments when the ID is not banned) per FR-010 in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go — 2026-10-09: already implemented: banlist-remove via the generic Exec/command parser (nuclearoption.go).
- [X] T072 [US3] Test `banlist-add` and `banlist-remove` across success, unknown ID and missing-config paths in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go.
- [X] T073 [US3] Implement `send-chat-message` (Rich Text tags preserved, 4005 BadArguments when too long) per FR-011 in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go — 2026-10-09: already implemented: send-chat-message via the generic Exec path with its chat special case (nuclearoption.go).
- [X] T074 [US3] Implement `set-next-mission` (group/name/maxtime arguments, 4005 BadArguments when the mission is not in rotation) and, for FR-012's second half, either wire the command that adjusts the **currently running** mission's remaining time or record in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/contracts/nuclear-option-remote-command.md that the protocol exposes none, in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption.go — 2026-10-09: already implemented: set-next-mission / set-time-remaining via the generic Exec/command parser (nuclearoption.go).
- [X] T075 [US3] Test `send-chat-message` across success, empty message and over-long message in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go.
- [X] T076 [US3] Test `set-next-mission` across a valid mission, an unknown mission and a read timeout in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go.
- [X] T077 [US3] Add the high-visibility table-driven framing test proving the response is **not** a mirror of the request — request `len(body)+body`, response `status+len(body)+body`, little-endian throughout, length counting only UTF-8 body bytes, oversized and truncated lengths rejected, and a `net.Pipe` case delivering the status word, the length word and the body in separate one-byte segments — in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go (one-byte segment framing, not-a-mirror case).
- [X] T078 [US3] Add the explicit error-path cases — unknown status code, a frame shorter than the 8-byte status+length header, `bodyLen == 0` paired with a non-2000 status, and a JSON decode failure — so the agent module stays at its 90% gate, in /home/valgul/project/kubernetes-game-dashboard/agent/internal/rcon/nuclearoption_test.go — 2026-10-09: done: agent/internal/rcon/nuclearoption_test.go (unknown status, short header).
- [ ] T079 [US3] Declare the moderation capability actions (player-list per the T023 decision, kick-player with confirm+danger, banlist-add, banlist-remove, send-chat-message, set-next-mission) so they route through the existing Remote Console with no new dashboard screens, in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml
- [ ] T080 [US3] Add the SC-004 five-second command-result assertion for the moderation commands — bucketing any new top-level `func Test…` this adds, per T059 — in /home/valgul/project/kubernetes-game-dashboard/test/e2e/nuclearoption_bot_e2e_test.go

### Steam Web API display-name resolver (the T022 resolution — API-server side only)

- [X] T081 [US3] Add the display-name resolver package — an exported `Resolver` with `Resolve(ctx, steamIDs []string) map[string]string`, batching the ids into `https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v2/` calls of **at most 100 `steamids` per request** (so a cache miss across a whole 16-player server is ONE upstream request, never one request per player), one hard-bounded per-call timeout chosen well inside the SC-004 five-second budget (≤2s, with the whole hydration capped so the player list can never exceed it), key passed as a query parameter but **never** logged and never included in any logged URL or error string, errors wrapped with `%w` — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/resolver.go (NEW), mirroring the client construction and package shape of /home/valgul/project/kubernetes-game-dashboard/api/internal/notify/notify.go — 2026-10-09: already implemented: api/internal/steam/resolver.go.
- [X] T082 [US3] Route all Steam egress through the SSRF dial-guard with the **strict** policy — `netguard.HTTPClient(timeout, netguard.IsPublic)`, and treat `netguard.ErrBlockedAddr` as a degradation (not an error to the caller); explicitly do **not** copy the permissive `netguard.IsAllowed` used by /home/valgul/project/kubernetes-game-dashboard/api/internal/notify/notify.go (~line 86), which exists for self-hosted sinks on private addresses and is wrong for a public internet endpoint — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/resolver.go — 2026-10-09: already implemented: resolver.go dials through netguard.HTTPClient(timeout, netguard.IsPublic).
- [X] T083 [US3] Confirm the Go module wiring the new imports imply: /home/valgul/project/kubernetes-game-dashboard/api/go.mod already carries `require github.com/GameplanePanel/gameplane/netguard v0.0.0` with its local `replace` (api reaches netguard today through `api/internal/notify`, so this package is **not** a new importer — it only selects a different policy) and already carries `golang.org/x/sync v0.22.0` as a **direct** require — and `golang.org/x/sync/singleflight` is **already imported inside this very module** at /home/valgul/project/kubernetes-game-dashboard/api/internal/registry/registry.go (import at line 38, `singleflight.Group` field at line 195) — so the T087 import is neither a new module dependency nor a new package in the binary, and this task is provably a no-op; /home/valgul/project/kubernetes-game-dashboard/go.work already lists both `./api` and `./netguard` — verify no edit is needed and add none if so; if anything here turns out to pull a genuinely new dependency, prefer the hand-rolled equivalent named in T087 over adding it, and otherwise update /home/valgul/project/kubernetes-game-dashboard/api/go.mod (and its `go.sum`) in the same commit as T081 — 2026-10-09: already implemented: no-op confirmed: api/go.mod already requires netguard and golang.org/x/sync.
- [X] T084 [P] [US3] Record the display-name cache's **NON-GOALS** so a future implementer does not "helpfully" add persistence — (a) the cache is **in-process memory only**: no new database table, no migration file under /home/valgul/project/kubernetes-game-dashboard/api/internal/db/migrations/, no Redis, no on-disk persistence of any kind; a cold start simply re-resolves; (b) **no shared or distributed cache**: multiple API replicas each keep their own, and the handful of duplicate upstream calls that implies is accepted and explicitly not worth Redis or a DB table; (c) the resolved name is a **cosmetic, display-only** field — every moderation action keys on `steamId` — so an empty cache costs nothing but a re-resolve. Record alongside it the arithmetic that justifies the design: ≤100 ids per batched call plus a 12-hour TTL costs a busy 16-player server roughly **one Steam call per 12 hours** instead of one per dashboard refresh, against Steam's documented order-of-100,000-calls/day-per-key quota — an enormous margin. Write all of it into /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/plan.md — 2026-10-09: already implemented: the non-goals are recorded in data-model.md §8d-vi/§8d-vii.
- [X] T085 [P] [US3] Implement the **small, bounded, in-process RAM cache** — a `map[string]entry` plus a `container/list` recency list guarded by a `sync.Mutex`, because it is read *and* written from concurrent HTTP handlers and must be safe for concurrent use (it has to pass `-race`); **bounded by entry COUNT with LRU eviction**, default **10,000 entries** (a Steam ID plus a display name is on the order of tens of bytes, so a cap in the low thousands to ~10k keeps the whole cache well under a megabyte and it can never grow without limit); positive TTL measured in **hours, not minutes** — default **12h**, because display names change rarely and a stale name is harmless given every moderation action keys on `steamId` and never on the name; clock injectable so expiry is testable without sleeping — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/cache.go (NEW) — 2026-10-09: already implemented: api/internal/steam/cache.go.
- [X] T086 [US3] Add **negative caching** to that cache (serial on T085 — same file) — a Steam ID that fails to resolve (private profile, deleted account, malformed id, or simply omitted from Steam's response) is stored as a *negative* entry under a **shorter** TTL, default **15m**, and is not retried until it expires. This is the property most likely to be missed and the one that matters most: without it every player-list refresh retries every unresolvable id forever, which is exactly the Steam spam this cache exists to prevent. A negative hit must be distinguishable from a miss so `Resolve` reports the id as unresolved (caller falls back to the raw Steam ID) instead of re-querying, and a negative entry must never mask a later successful resolve once it has expired — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/cache.go — 2026-10-09: already implemented: api/internal/steam/cache.go negative entries.
- [X] T087 [US3] Add **single-flight de-duplication** in front of the upstream call — concurrent requests needing the same not-yet-cached ids collapse into ONE `GetPlayerSummaries` call whose result fans out to every waiter, rather than issuing N identical upstream requests; use `golang.org/x/sync/singleflight` (already reachable per T083 and already used in-repo — `api/internal/registry/registry.go` imports it at line 38 and holds a `singleflight.Group` at line 195 — so this adds no new dependency; were that ever untrue, hand-roll the small equivalent — a `map[string]*call` of in-flight batches plus a `sync.WaitGroup` — rather than taking on a new dependency), keyed on the sorted set of ids in the batch, with context handling such that one waiter's timeout or cancellation cannot cancel the shared flight for the others — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/singleflight.go (NEW) — 2026-10-09: already implemented: api/internal/steam/singleflight.go.
- [X] T088 [P] [US3] Define the four cache/resolver knobs as an `Options` struct with sane defaults and exported `Default*` constants — `MaxEntries` (10000 entries, the LRU bound), `TTL` (12h, positive entries), `NegativeTTL` (15m, unresolvable ids), `Timeout` (2s, the per-call upstream bound inside the SC-004 budget) — with zero/negative values falling back to the default rather than disabling the bound or the timeout, so a misconfigured value can never produce an unbounded cache or an unbounded call; the process-level wiring of these knobs lives in `api/cmd/main.go` (T090), not here — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/options.go (NEW) — 2026-10-09: already implemented: api/internal/steam/options.go.
- [X] T089 [US3] Implement the mandatory graceful-degradation contract — the resolver **never** returns a fatal error to the player-list path: no key configured → nil resolver, empty map, zero outbound calls; DNS failure, dial failure, `netguard.ErrBlockedAddr`, context timeout, non-200 status or malformed JSON → empty map plus one rate-limited warn log carrying no key material; a partially-successful batch → exactly the ids that resolved; an id Steam does not return → simply absent (and negative-cached per T086) so the caller falls back to the raw Steam ID; a negative-cached id is treated exactly like an unresolved one and issues no request — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/resolver.go — 2026-10-09: done: call-level failures and netguard blocks no longer write negative entries (data-model §8d-i); warning rate-limited to once a minute.
- [X] T090 [US3] Wire the optional key and the four knobs from the environment, following the **existing** api config pattern rather than inventing one — the local `envOr` / `envOrInt` helpers already in this file (~lines 428 and 458); note `api` does **not** import `svcutil` today (there is no `svcutil` require in `api/go.mod`) and this feature must not make it the first, so use the in-file helpers, not `svcutil.Or`/`svcutil.OrInt`. The key is `GAMEPLANE_STEAM_API_KEY`, **env-only with no CLI flag** (exactly like `c.telemetryAuth` ~line 387 and `c.auditWebhookAuth` ~line 397, so it never appears in the pod spec or in `ps`). The knobs are ordinary flag+env pairs like every other setting: `--steam-cache-max-entries` / `GAMEPLANE_STEAM_CACHE_MAX_ENTRIES` (10000), `--steam-cache-ttl` / `GAMEPLANE_STEAM_CACHE_TTL` (12h), `--steam-cache-negative-ttl` / `GAMEPLANE_STEAM_CACHE_NEGATIVE_TTL` (15m), `--steam-timeout` / `GAMEPLANE_STEAM_TIMEOUT` (2s), durations parsed with `time.ParseDuration` over `envOr(...)` inline, or via a small `envOrDuration` helper added beside `envOrInt` — this file has **no** duration helper today (only `envOr` at ~line 428 and `envOrInt` at ~line 459, and every existing flag is `StringVar`/`IntVar`/`BoolVar`), and `svcutil` must not be imported to supply one — degrading to the T088 default on a malformed or empty value. Construct the resolver only when the key is non-empty, pass nil through to the player-list route otherwise, and log at most that name resolution is disabled (never the key) — in /home/valgul/project/kubernetes-game-dashboard/api/cmd/main.go — 2026-10-09: done: api/cmd/main.go (GAMEPLANE_STEAM_API_KEY env-only + four flag/env knobs, envOrDuration).
- [X] T091 [P] [US3] Add the optional `api.steam.apiKeySecretRef` block (`name: ""`, `key: api-key`) with a comment stating it is optional, off by default, that the Secret must be created out-of-band in the release namespace, and that leaving it empty means player lists show raw Steam IDs — plus a commented-out `api.steam.cache` block exposing the four T088/T090 knobs (`maxEntries: 10000`, `ttl: 12h`, `negativeTTL: 15m`, `timeout: 2s`) with a note that they are an in-memory-only cache and need no storage of any kind — following the `api.audit.webhook.authSecretRef` / `api.telemetry.authSecretRef` precedent (~lines 128–133 and ~lines 190–196) — in /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/values.yaml — 2026-10-09: done: charts/gameplane/values.yaml api.steam.
- [X] T092 [US3] Project that Secret key into the API container as `GAMEPLANE_STEAM_API_KEY` via `secretKeyRef`, guarded by `{{- if .Values.api.steam.apiKeySecretRef.name }}`, and render the four cache knobs as plain env vars only when they are set, so the Deployment renders unchanged when the whole `api.steam` block is unset, in /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/templates/api.yaml — 2026-10-09: done: charts/gameplane/templates/api.yaml.
- [X] T093 [US3] Add the hydrating player-list handler — proxy to the agent exactly as `httpProxy("/players")` does today, then hydrate the decoded response with resolved display names **only when the agent returned structured entries carrying Steam IDs and a resolver is configured — today `agent/internal/players/players.go:61` returns `Players []string`, so the structured shape arrives only for the `nuclearoption` players capability decided in T023, and every flat-string payload must be passed through byte-for-byte**, passing the whole hydration a bounded context so a slow Steam call is abandoned and the un-hydrated body is returned instead; on any decode failure return the agent's bytes verbatim — in /home/valgul/project/kubernetes-game-dashboard/api/internal/ws/players_hydrate.go (NEW) — 2026-10-09: done: api/internal/ws/players_hydrate.go (1.5s budget, verbatim passthrough otherwise).
- [X] T094 [US3] Register the hydrating handler in place of `p.httpProxy("/players")` (~line 62) and leave `/players/kick`, `/players/ban` and `/players/unban` (~lines 64–66) as pure proxies **still keyed on the Steam ID** — the resolved name is display-only and must never reach a moderation call — in /home/valgul/project/kubernetes-game-dashboard/api/internal/ws/dialer.go — 2026-10-09: done: api/internal/ws/dialer.go registers the hydrating GET /players; moderation routes unchanged.
- [ ] T095 [P] [US3] Widen the player payload on the client types — `PlayersResp.players` is `string[]` today (~line 742), which cannot carry a Steam ID, a faction or a name; change it to a discriminated shape that keeps every existing game's flat-string payload valid (e.g. `players: (string | { steamId: string; faction?: string; displayName?: string })[]`) so no other game's Players tab breaks under `strict` — in /home/valgul/project/kubernetes-game-dashboard/web/src/types.ts
- [ ] T096 [US3] Render structured entries in the player rows — display name with a fallback to the raw Steam ID whenever `displayName` is absent or empty (never a blank cell, never "unknown"), **add** the faction column (there is none today; `Players.tsx` ~line 145 maps flat strings), keep flat-string entries rendering exactly as they do now for every other game, and keep every kick/ban/unban mutation keyed on the Steam ID rather than the rendered name, in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/Players.tsx — **and, in the same task, narrow the second consumer of the widened type**: /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/Overview.tsx binds `roster?.players` into `names` (~line 327) and then calls `n.slice(0, 2)` (~line 355), uses `n` as the React `key` (~line 353) and renders `n` (~line 357), all of which are hard `tsc --noEmit` errors once the element type is `string | { steamId; faction?; displayName? }`, so resolve each entry to a string first (display name, else Steam ID, else the flat string). The existing fixtures (`web/src/test/factories.ts` ~line 255) and `Overview.test.tsx` stay valid under the union, so only the build catches this — do not skip it
- [X] T097 [P] [US3] Cover the resolver against an `httptest` server — 250 ids split into three ≤100-id batches, a 16-id lookup issuing exactly ONE upstream request, successful mapping, no key configured (zero requests issued), a `netguard.ErrBlockedAddr` destination, an unreachable host, a server that never answers (per-call timeout), a partially-populated response, an id Steam omits, a non-200 status, malformed JSON, and an assertion that no log line or returned error ever contains the key — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/resolver_test.go (NEW) — 2026-10-09: done: api/internal/steam/resolver_test.go.
- [X] T098 [P] [US3] Cover the bounded-LRU-with-TTL cache against a counting fake fetcher — a hit inside the TTL issues **zero** upstream calls, an expired entry re-resolves on the next lookup (injected clock, no sleeping), inserting far more than `MaxEntries` distinct ids leaves `Len()` pinned **at** the bound rather than growing unbounded and evicts the least-recently-used entry, a read refreshes recency so the correct victim is chosen, a zero/negative `MaxEntries` falls back to the default instead of disabling the bound, and concurrent readers and writers are race-free under `-race` — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/cache_test.go (NEW) — 2026-10-09: already implemented: api/internal/steam/cache_test.go.
- [X] T099 [US3] Cover **negative caching** (serial on T098 — same test file) — an id Steam fails to resolve is remembered as a negative entry, a second and third lookup inside the negative TTL issue **zero** upstream calls (proving the "retries every unresolvable id on every refresh" failure mode is closed), the negative entry expires on its own shorter TTL and only then is retried, a negative entry never masks a later successful resolve, and negative entries count against the same LRU bound as positive ones — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/cache_test.go — 2026-10-09: already implemented: api/internal/steam/cache_test.go negative-cache cases.
- [X] T100 [P] [US3] Cover **single-flight de-duplication** — N goroutines requesting the same uncached ids concurrently collapse to exactly one upstream request (counted on an `httptest` handler that blocks until every goroutine has arrived), all N receive the same result, an upstream error fans out to every waiter and is not cached as a positive, disjoint id sets are *not* collapsed, and one waiter cancelling its context neither cancels the shared flight nor corrupts the others' results — in /home/valgul/project/kubernetes-game-dashboard/api/internal/steam/singleflight_test.go (NEW) — 2026-10-09: done: api/internal/steam/singleflight_test.go.
- [X] T101 [P] [US3] Cover the hydrating handler — hydrated list with names, no resolver configured → raw Steam IDs passed through untouched, resolver returning nothing → raw Steam IDs, Steam unreachable → raw Steam IDs with no error surfaced to the browser, resolver exceeding the bounded context → raw Steam IDs and a response still well inside SC-004, a non-Steam-ID game's payload passed through byte-for-byte, and an undecodable agent body proxied verbatim — in /home/valgul/project/kubernetes-game-dashboard/api/internal/ws/players_hydrate_test.go (NEW), keeping the api module at its 80% gate — 2026-10-09: done: api/internal/ws/players_hydrate_test.go.
- [ ] T102 [P] [US3] Cover the Players tab — a row with a resolved name shows the name, a row without one shows the raw Steam ID, and a kick issued from a named row sends the Steam ID — in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/Players.test.tsx
- [X] T103 [P] [US3] Document the new **optional** Steam Web API key — how to obtain it, how to create the Secret, the `api.steam.apiKeySecretRef` value, the four `api.steam.cache` knobs and their defaults (10000 entries, 12h TTL, 15m negative TTL, 2s timeout), that the cache is in-memory only and needs no database or Redis, that the key is never logged nor returned to the browser, and exactly what happens when it is unset (player lists render with raw Steam IDs; nothing else changes) — in /home/valgul/project/kubernetes-game-dashboard/docs/install.md — 2026-10-09: done: docs/install.md.
- [X] T104 [P] [US3] Record the new outbound dependency in the threat model — egress originates only from the control-plane namespace (the games namespace stays under `default-deny-egress`), it is dialled through `netguard`'s strict `IsPublic` policy, the key is Secret-only and never surfaced in an API response, the display-name cache holds no credential and never leaves process memory, and the resolved name is display-only and never an identifier for kick/ban/unban — in /home/valgul/project/kubernetes-game-dashboard/docs/security.md — 2026-10-09: done: docs/security.md.

**Checkpoint**: Remote administration works end to end, and the player list satisfies FR-007 / US3 Acceptance Scenario 1 with names when the key is present and raw Steam IDs when it is not.

---

## Phase 7: User Story 6 - Operator gets a clear status message when address pool assignment fails (Priority: P3)

**Goal**: Every pool-assignment failure mode surfaces a specific, actionable message in the dashboard within 30 seconds instead of an indefinite "Pending" (SC-003).

**Independent Test**: Create a LoadBalancer server naming a pool that does not exist and confirm the dashboard shows `Pool 'xyz' not found in cluster` within 30 seconds; repeat with an exhausted pool, with a non-LoadBalancer exposure mode, and with the address-manager flavor set to `none`.

**Depends on**: T040 (condition type) and T044 (the Networking panel that renders it) — both in Phase 3. US6 is **not** independent of US2.

- [X] T105 [US6] Grant the operator read access to Service events — add `+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch` beside the existing `create;patch` marker (~line 172) in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go, commit the regenerated /home/valgul/project/kubernetes-game-dashboard/operator/config/rbac/role.yaml, and hand-edit the matching ClusterRole rule (~lines 64–66, the chart RBAC is not generated from the markers) in /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/templates/operator.yaml
- [X] T106 [US6] Derive `PoolNotFound`, `PoolExhausted` and `ManagerFailure` from MetalLB's `Warning`-type Service events and Cilium's Service status conditions, add `NoAddressManagerConfigured` for a pool/address requested while the flavor is `none` (SC-002 forbids the silent default-pool fallback), and fall back to `ServiceNotReady` when 30 seconds have elapsed since the `AddressAssignment` condition's `lastTransitionTime` — naming the requeue interval that guarantees the re-evaluation — in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_status.go
- [X] T107 [US6] Emit the informational `IgnoredForExposureMode` reason when a pool or address is set while `expose != LoadBalancer` in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_controller.go
- [X] T108 [P] [US6] Add unit tests for all five failure reasons (`PoolNotFound`, `PoolExhausted`, `ManagerFailure`, `NoAddressManagerConfigured`, `IgnoredForExposureMode`) and their message text in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_status_test.go
- [X] T109 [P] [US6] Cover the failing `AddressAssignment` condition messages rendered by the Networking settings panel in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/tabs/settings/Networking.test.tsx

**Checkpoint**: Pool failures are diagnosable from the dashboard alone.

---

## Phase 8: User Story 5 - Operator edits Nuclear Option server settings and invalid configuration surfaces as a clear error (Priority: P3)

**Goal**: Invalid Nuclear Option configuration is rejected at save time with a message naming the offending field, never as a boot crash-loop (SC-007).

**Independent Test**: Save a Nuclear Option server with `MAX_PLAYERS: 100` and confirm a condition appears within 10 seconds reading like `MAX_PLAYERS must be between 4 and 64`, with no pod ever entering CrashLoopBackOff.

**Depends on**: T051 (configSchema) and the whole of Phase 4 (US1) — the e2e assertion needs a running server. **`ConfigField` has no constraint keys today** (`Name, DisplayName, Description, Type, Default, Enum, Required, Target, AutoFromMemoryLimit` only), and `materializeConfig` checks only int/bool parseability, enum membership and required-ness — so FR-023 needs the mechanism built first, in T110–T115.

- [X] T110 [US5] Add the numeric and length constraint keys (`min`, `max`, `minLength`, `maxLength`) to the `ConfigField` struct (~lines 1051–1102) in /home/valgul/project/kubernetes-game-dashboard/operator/api/v1alpha1/gametemplate_types.go — 2026-10-09: already implemented: operator/api/v1alpha1/gametemplate_types.go ConfigField Min/Max/MinLength/MaxLength, in all CRD copies.
- [X] T111 [US5] Regenerate and commit the CRD artifacts for T110 — /home/valgul/project/kubernetes-game-dashboard/operator/api/v1alpha1/zz_generated.deepcopy.go, /home/valgul/project/kubernetes-game-dashboard/operator/config/crd/*.yaml, /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/crds/*.yaml and /home/valgul/project/kubernetes-game-dashboard/charts/gameplane/crd-manifests/*.yaml — in the SAME commit as T110, per project rule 7 — 2026-10-09: already implemented: CRD artifacts regenerated and committed with T110.
- [X] T112 [US5] Enforce the new constraints in `materializeConfig()` (~lines 86–175), rejecting with a message that names the field and the constraint, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_config.go — 2026-10-09: already implemented: operator/internal/controller/gameserver_config.go constraint enforcement.
- [X] T113 [US5] Add validation unit tests asserting each rejection carries a message naming the field and the constraint, and that templates declaring no constraints behave exactly as before, in /home/valgul/project/kubernetes-game-dashboard/operator/internal/controller/gameserver_config_test.go — 2026-10-09: already implemented: gameserver_config_test.go and envtest TestGameServer_InvalidConfigFailsThenRecovers.
- [X] T114 [US5] Mirror the constraint keys in the wizard's client-side field validation so the operator sees the error before saving, in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/CreateServer.tsx — 2026-10-09: already implemented: web/src/lib/validation.ts (used by CreateServer.tsx).
- [X] T115 [P] [US5] Cover the wizard-side constraint rejections in /home/valgul/project/kubernetes-game-dashboard/web/src/routes/CreateServer.test.tsx — 2026-10-09: already implemented: covered in web/src/lib/validation.test.ts and components/server/ConfigFields.test.tsx.
- [ ] T116 [US5] Declare the field constraints (`SERVER_NAME` 1–64 chars, `MAX_PLAYERS` 4–64, `MISSION_ROTATION` enum, `MISSION_LIST` entries checked against the available missions) using the new keys in the configSchema in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/template.yaml
- [ ] T117 [US5] Assert that an invalid configuration surfaces on `status.conditions` within 10 seconds without a crash-loop — bucketing any new top-level `func Test…` this adds, per T059 — in /home/valgul/project/kubernetes-game-dashboard/test/e2e/nuclearoption_bot_e2e_test.go
- [X] T118 [US5] Reconcile the two US5 acceptance scenarios that no mechanism covers — Scenario 1's "Configuration Changed — Restart Required" status and Scenario 4's "Invalid JSON in [field]" (there is no `json` config-field type) — by either tasking them explicitly or amending them, recording the outcome in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/spec.md — 2026-10-09: done: maintainer chose to amend: spec.md US5 scenarios 1 and 4 amended.

**Checkpoint**: All six user stories are functional.

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Cross-story e2e coverage, documentation, real-cluster validation, and the gates that must be green before either track ships.

- [X] T119 Author the Track B e2e suite — default pool for a server with no preference (SC-006), assignment from a named pool (SC-002), nonexistent-pool error within 30s (SC-003), exhausted pool, address in use, wrong exposure mode, address visible in status and dashboard within 30s (SC-008), and a GameServer created through the REST API carrying `networking.addressPool` receiving the same assignment and the same audit trail (FR-021, FR-025) — with `t.Parallel()` and unique per-test names in /home/valgul/project/kubernetes-game-dashboard/test/e2e/pool_assignment_e2e_test.go (NEW) — 2026-10-09: done: TestAddressPool_AddressInUseConflict and TestAddressPool_PoolExhausted (dedicated one-address kind pool pool-e2e-single in deploy/kind/e2e.sh).
- [X] T120 Register every top-level `func Test…` from T119 in the default-executed `operator` bucket, in the SAME commit as T119 (`buckets.sh verify` fails both on unbucketed suite tests and on bucketed tests it cannot find), in /home/valgul/project/kubernetes-game-dashboard/test/e2e/buckets.sh — 2026-10-09: already implemented: test/e2e/buckets.sh operator bucket registers every TestAddressPool_* test.
- [X] T121 [P] Document address-pool configuration for MetalLB and Cilium (including the `gameplane.local/lb-pool` serviceSelector convention the cluster admin must mirror), setting a pool or fixed address, and troubleshooting each failure reason, in /home/valgul/project/kubernetes-game-dashboard/docs/networking.md (NEW)
- [X] T122 [P] Document the operator's address-manager flavor Helm value in /home/valgul/project/kubernetes-game-dashboard/docs/install.md
- [X] T123 Link the new networking guide (created by T121) from /home/valgul/project/kubernetes-game-dashboard/README.md
- [X] T124 Correct the Cilium translation description (~line 193), which names a bare `pool=<value>` label instead of the `gameplane.local/lb-pool` label the contracts and T014 specify, in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/plan.md
- [X] T125 Reconcile the two Track B requirements the implementation diverges from — FR-022's "no bespoke integration per address manager" (the design ships two hand-written per-manager translations behind an explicit flavor selector) and SC-006's latency-parity claim (no baseline is measured anywhere) — by amending them or tasking the generic mechanism, in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/spec.md
- [ ] T126 Run `TestGameServer_NuclearOptionBot_Joined` against a real cluster and record the outcome, updating the join wire format if the handshake differs, in /home/valgul/project/kubernetes-game-dashboard/test/e2e/nuclearoption_bot_e2e_test.go
- [ ] T127 Walk the full moderation command suite against a real server per quickstart.md — including one pass with the Steam key Secret present (names resolve) and one with it removed (raw Steam IDs, no error, no latency regression against SC-004) — and record any signature drift in /home/valgul/project/kubernetes-game-dashboard/specs/002-nuclear-option-ip-pool/contracts/nuclear-option-remote-command.md
- [ ] T128 Walk the valid/invalid configuration matrix against a real server per quickstart.md, confirm no crash-loops or hung states, and record the results in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [ ] T129 Prove the FR-013 backup/restore round-trip preserves config, ban list and mission state using the stock Gameplane backup framework, recording the verified paths in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [ ] T130 Add the Nuclear Option row with status `covered-deferred`, depth `JOINED`, test `TestGameServer_NuclearOptionBot_Joined`, bucket `bot-heavy` and a `lastVerified` date to /home/valgul/project/kubernetes-game-dashboard/docs/game-coverage.md — in the SAME commit as T131, because `joincoverage.sh` fails a module directory with no row (check 2) and a row with no module directory (check 4), and `modules/` only changes here when the pointer moves
- [X] T131 Bump the `modules/` submodule pointer to the `gameplane-module` commit containing the Nuclear Option bundle, in the `modules` gitlink of this repo recorded against /home/valgul/project/kubernetes-game-dashboard/.gitmodules — 2026-10-09: already implemented: the modules/ submodule pointer already contains modules/nuclear-option/ Bumped again to GameplanePanel/module b1cbfe4 (module PR #47: Nuclear Option 1.1.0 with `rcon.authentication: false`).
- [X] T132 Satisfy every remaining check of the coverage verifier (`test/e2e/joincoverage.sh verify`) for the Nuclear Option row in /home/valgul/project/kubernetes-game-dashboard/docs/game-coverage.md — 2026-10-09: already implemented: docs/game-coverage.md row present; the CI joincoverage gate is the authority.
- [ ] T133 Final module documentation sync — setup, commands and limitations complete and consistent with the verified findings, including the note that display names come from the API server's optional Steam Web API lookup and are absent (raw Steam IDs) when no key is configured — in /home/valgul/project/kubernetes-game-dashboard/modules/nuclear-option/specs.md
- [X] T134 Add the release entry for both tracks — including the new optional `api.steam.apiKeySecretRef` value and its raw-Steam-ID fallback — in /home/valgul/project/kubernetes-game-dashboard/CHANGELOG.md — 2026-10-09: done: CHANGELOG.md [Unreleased].
- [ ] T135 Exit criterion, not implementation work: confirm on CI that every coverage gate stayed green with no new suppressions — /home/valgul/project/kubernetes-game-dashboard/operator/.testcoverage.yml (72%), /home/valgul/project/kubernetes-game-dashboard/api/.testcoverage.yml (80%), /home/valgul/project/kubernetes-game-dashboard/agent/.testcoverage.yml (90%), /home/valgul/project/kubernetes-game-dashboard/gameaction/.testcoverage.yml (91%), /home/valgul/project/kubernetes-game-dashboard/netguard/.testcoverage.yml (91% — in scope because the Steam resolver is a new consumer of `netguard.IsPublic`) and /home/valgul/project/kubernetes-game-dashboard/web/vitest.config.ts (92/76/82/92)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies — start immediately. T002 requires T001.
- **Foundational (Phase 2)**: depends on Setup. Splits cleanly in two:
  - T006–T015 (Track B) block Phase 3 (US2), Phase 5 (US4) and Phase 7 (US6).
  - T016–T036 (Track A) block Phase 4 (US1), Phase 6 (US3) and Phase 8 (US5). **T016 is the critical gate**: if Steam app 3930080 does not download with a native Linux binary, the entire track dies. T017/T020/T021/T053 are observation gates feeding the module spec. T018's wire-framing verification is already satisfied by the contract's publisher-official status; T019 (per-command response bodies) does not block transport or fire-and-forget execution and is informational only (see evidence note above). T022 is closed — it is now a write-up task, not a blocker, and its implementation lives in T081–T104.
- **Phase 3 (US2, MVP)**: depends on T003/T004 and T006–T015. The only genuinely story-independent phase.
- **Phase 4 (US1)**: depends on T016 and T025–T036.
- **Phase 5 (US4)**: depends on T006–T015, T040 and T044 — i.e. on US2 having landed.
- **Phase 6 (US3)**: depends on T022 (recorded), T023, T024, T030, T031 **and all of Phase 4** (its independent test needs a running server built by T054–T057). T018's wire framing is already verified; T019 (unverified per-command bodies on four fire-and-forget commands) does not block the transport implementation (see evidence note above).
- **Phase 7 (US6)**: depends on T040 and T044 (US2), and on T105 for the RBAC T106 needs.
- **Phase 8 (US5)**: depends on T051 and all of Phase 4, and on the constraint mechanism T110–T113 which does not exist yet.
- **Phase 9 (Polish)**: T119–T125 and T135 depend on Track B stories; T126–T134 depend on Track A stories. Track B's polish subset can be completed and shipped before Track A starts.

### Task-Level Blocking

- T007 must be in the **same commit** as T006; T033 as T032; T111 as T110 (project rule 7).
- T007 must land **before** T012, T013, T014, T015, T039, T040 and T049 — without the regenerated CRD YAML the apiserver strips the new fields and the envtest fails looking like a reconciler bug.
- T013, T014 and T015 depend on T008 (flavor) and T012 (managed labels); T011 depends on T009/T010.
- T003 and T004 block the US2 independent test, T049's LoadBalancer cases and all of T119.
- T037 **blocks** T038, T043, T044 and T045 — no React work before the design pass is saved and re-exported.
- T041 blocks T043, T044 and T045. T042 does **not** depend on T041: `endpoints.ts` carries its own inline networking type.
- T043–T045 block T046–T048.
- T040 blocks T062, T106 and T109; T044 blocks T065 and T109.
- T036 depends on T020; T052 depends on T018/T019 and T051; T057 depends on T054 (and T055/T056 when branch B is chosen).
- T017, T020, T021 append to the file T016 creates; T028 depends on all four.
- T019 appends to the file T018 creates.
- T051, T052, T053, T057, T079 and T116 all edit `modules/nuclear-option/template.yaml`, which T026 creates — they are serial on that file.
- T066, T068, T070, T071, T073 and T074 are serial on `agent/internal/rcon/nuclearoption.go`; T067, T069, T072, T075, T076, T077 and T078 are serial on `agent/internal/rcon/nuclearoption_test.go`.
- T080 depends on T030 and on T066–T074; T079 depends on T026 and T066–T074, not on the transport alone.
- T058 depends on T051, T052, T057 (there is nothing to join before the image exists); T059 same commit as T058; T060, T080 and T117 append to T058's file and must bucket any new test function they introduce.

**Steam display-name resolver (T081–T104)**

- T022 must be written up before T081 starts — the tasks implement a recorded decision, not an option.
- T081 → T082 → T089 are **serial** on `api/internal/steam/resolver.go`, in that order.
- T085 → T086 are **serial** on `api/internal/steam/cache.go`. T084 (non-goals write-up in `plan.md`), T085 (`cache.go`) and T088 (`options.go`) are three different files, but T085/T086 consume the bounds and defaults T088 declares, so T088 lands first (or the two are authored together).
- T087 (`singleflight.go`) depends on T081's `Resolve` shape and on T086 — a negative-cache hit must short-circuit *before* a flight is started, or the de-duplication just de-duplicates wasted calls.
- T089's negative-cache and single-flight behaviour depends on T086 and T087 having landed.
- T083 is a verification of `api/go.mod` / `go.work` and must be settled before T081 and T087 are committed (it should be a no-op: api already requires and replaces `netguard`, and `golang.org/x/sync` — which provides `singleflight` — is already a direct require).
- T081, T085, T086, T087, T088 and T089 block T097, T098, T099, T100 and T101.
- T098 → T099 are **serial** on `api/internal/steam/cache_test.go`.
- T090 depends on T081 and T088 (there is nothing to construct or configure before they exist); T092 depends on T091 (the value must exist before the template reads it); T090 and T092 must agree on the exact env var names, starting with `GAMEPLANE_STEAM_API_KEY`.
- T093 depends on T081/T089 and T090; T094 depends on T093 and is the only task that edits `api/internal/ws/dialer.go`.
- T093, T095 and T096 additionally depend on **T023** (the players-capability decision): structured player entries carrying a Steam ID and a faction exist only if that decision produces them, so until T023 lands there is nothing to hydrate or render.
- T095 blocks T096; T096 blocks T102. T095 widens a type with **two** consumers — `web/src/routes/tabs/Players.tsx` and `web/src/routes/tabs/Overview.tsx` — and T096 owns both, so no other task may touch either file while it runs.
- T101 depends on T093/T094; the api 80% gate is measured over T097–T101 together.
- T103 and T104 depend on T090–T092 (they document the shipped value names and the shipped egress path), not on the resolver internals.
- T127 exercises T081–T104 on a real cluster in both the key-present and key-absent configurations; T133 and T134 describe the shipped behaviour and therefore follow all of them.

**Remaining cross-cutting**

- T105 blocks T106.
- T119 and T120 same commit; T130 and T131 same commit; T132 depends on T059, T130 and T131.
- T131 depends on every `modules/nuclear-option/` file being committed in the `gameplane-module` repo first.
- T135 is last: it observes the state produced by everything else.

### Within Each User Story

- Operator types → codegen → reconciler → status → dashboard → tests.
- Design pass → React → React tests (never the other way round).
- Agent protocol transport → per-command wrappers → per-command tests → framing regression test.
- Resolver package → bounded LRU cache → negative caching → single-flight → config/chart plumbing → route hydration → web types → web render → tests → docs.

---

## Parallel Opportunities

Concrete examples that touch disjoint files with no incomplete dependency:

**Setup wave** — launch together: T003 (e2e kind MetalLB + pools), T004 (dev kind MetalLB + pools), T005 (design manifest). T002 waits for T001.

**Foundational wave 1** — launch together: T006 (CRD types), T009 (Helm value), T016 (availability gate — blocking precondition for all Track A), T018 (wire framing documentation — publisher-official, already verified). T017, T020 and T021 append to the same `specs.md` T016 creates and are therefore serial behind it; T019 appends to the same contract file as T018. T019 is informational; it does not block the transport or fire-and-forget command implementation (see evidence note in Phase 2 above).

**Foundational wave 2** — launch together: T027 (module README), T029 (icon), T030 (agent protocol transport), T034 (validate.py), T035 (module-authoring docs) — five different files. T028 waits for T016–T021.

**US2 wave 1** — launch together: T037 (design pass, no code dependency), T039 and T040 are serial on `gameserver_status.go`, T041 (web types) and T042 (endpoint helper) are independent files.

**US2 wave 2** — after T037 is saved and T041 has landed: T043, T044 and T045 are three different route files and run in parallel. T038 is a single Pencil session and must not be split across agents.

**US2 wave 3** — launch together: T046, T047, T048 (three separate test files).

**US3 agent block** — **no parallel wave exists.** Every command wrapper writes `agent/internal/rcon/nuclearoption.go` and every command test writes `agent/internal/rcon/nuclearoption_test.go`; run T066–T080 serially.

**US3 Steam wave 1** — the resolver block is independent of the agent block and can run alongside it. Launch together: T084 (`specs/002-nuclear-option-ip-pool/plan.md`, the cache non-goals), T085 (`api/internal/steam/cache.go`), T088 (`api/internal/steam/options.go`), T091 (`charts/gameplane/values.yaml`), T095 (`web/src/types.ts`). T081 → T082 → T089 are serial on `api/internal/steam/resolver.go`; T085 → T086 are serial on `cache.go`; T083 is a read-only check of `api/go.mod` and `go.work`.

**US3 Steam wave 2** — after the resolver, the cache and the chart value have landed: T087 (`api/internal/steam/singleflight.go`), T090 (`api/cmd/main.go`), T092 (`charts/gameplane/templates/api.yaml`) and T096 (`web/src/routes/tabs/Players.tsx` **and** `web/src/routes/tabs/Overview.tsx`) are four different units of work over disjoint files. T093 → T094 are serial on the API route surface.

**US3 Steam wave 3** — launch together: T097, T098, T100, T101, T102 (five separate test files) and T103, T104 (two separate docs files). T099 appends to T098's file and is serial behind it.

**Polish wave** — launch together: T121 (docs/networking.md) and T122 (docs/install.md). T123 waits for T121. T122 and T103 both edit `docs/install.md` — run them serially, never concurrently.

**Cross-track**: the tracks share four files — `test/e2e/buckets.sh` (T059 vs T120), `charts/gameplane/crds`/`crd-manifests` (T007/T033/T111), `charts/gameplane/values.yaml` (T009 vs T091) and `docs/install.md` (T103 vs T122) — so those tasks must not run concurrently across tracks. Everything else is disjoint.

---

## Implementation Strategy

### MVP First — User Story 2 only

1. Complete Phase 1 (Setup), including the MetalLB install.
2. Complete the Track B half of Phase 2 (T006–T015). Track A's gates can proceed in the background but block nothing here.
3. Complete Phase 3 (US2), design pass first.
4. **STOP and VALIDATE**: run the US2 independent test on a kind cluster with the MetalLB pools and the `metallb` flavor.
5. Complete the Track B polish subset (T119–T125, T135) and ship. Track B is a complete, useful feature with zero Track A code in the tree.

### Incremental Delivery

1. Setup + Track B foundation → foundation ready.
2. **US2 (P1, Track B)** → validate → ship. **MVP.**
3. **US4 (P2, Track B)** → fixed-address pinning → ship.
4. **US6 (P3, Track B)** → failure diagnostics → ship. Track B is now complete.
5. Resolve the critical Track A gate T016 (can Steam app 3930080 download and run with a native Linux binary?). Record the open decisions T023/T024, and write up the closed T022 decision. T019 (unverified per-command response bodies) is informational and does not block implementation. If T016 fails, document the blocker and stop — Track A is dead and Track B is unaffected.
6. **US1 (P1, Track A)** → a playable server → ship.
7. **US3 (P2, Track A)** → remote administration, including the Steam display-name resolver (T081–T104) → ship.
8. **US5 (P3, Track A)** → configuration validation → ship.
9. Complete Track A polish (T126–T134) and the coverage sweep (T135).

Each step adds value without breaking the previous one; the two tracks can be released independently and in either order once Track B is out.

### Parallel Team Strategy

- Developer A owns Track B end to end: Phase 1 MetalLB, Phase 2 (T006–T015), Phase 3, Phase 5, Phase 7, and polish T119–T125.
- Developer B owns Track A gates first (T016–T024) — the highest-uncertainty work in the feature — then Phase 2 (T025–T036), Phase 4, Phase 6 (agent block T066–T080 **and** Steam resolver block T081–T104), Phase 8, and polish T126–T134.
- The two developers must coordinate on `test/e2e/buckets.sh`, `charts/gameplane/values.yaml`, `docs/install.md` and the regenerated CRD artifacts; only T135 needs both tracks finished.

---

## Notes

- `[P]` means different files and no incomplete dependency.
- Story labels appear on user-story phases only; Setup, Foundational and Polish tasks carry none.
- Commit per logical unit, signed (`git commit -s`), conventional prefixes; codegen output ships in the commit that triggered it.
- One branch per unit of work, deleted once merged.
- Fix lint findings; never suppress them.
- The Steam Web API key is optional and never required for any test: every path in T097–T102 must pass with no key configured, and the cache tests must not require network access at all.

---

## Phase 10: Convergence

- [X] T136 CRITICAL — Update /home/valgul/project/kubernetes-game-dashboard/operator/specs.md to document the address-pool behaviour already shipped in the operator: the `spec.networking.addressPool` / `spec.networking.address` inputs, the `--address-manager` flavor flag (`metallb` | `cilium` | `none`) and its per-flavor Service translation (MetalLB annotations, Cilium label + annotation, `none` no-op), the managed-label pruning invariant, and the `AddressAssignment` condition with its `Assigned` / `AssignmentPending` / `ServiceNotReady` / `IgnoredForExposureMode` / `NoAddressManagerConfigured` reasons, per Constitution IV (contradicts)
- [X] T137 Update /home/valgul/project/kubernetes-game-dashboard/web/specs.md so the Networking settings sub-section and the typed endpoint-namespace description reflect the widened `GameServerNetworking` / `GameServerEndpoint` contract (`addressPool`, `address`, `pool`) now carried by `web/src/types.ts` and `web/src/lib/endpoints.ts`, per Constitution IV (partial)
- [X] T138 Correct the now-stale scope note in /home/valgul/project/kubernetes-game-dashboard/design-export/MANIFEST.md, which still claims "No design pass has been made yet; these exports still reflect the pre-feature design" even though the pass landed and re-exported `f1Vga`, `J5pjJ3` and `EZFW0`, per Constitution II (partial)
- [X] T139 Verify and record that the dashboard renders Nuclear Option configuration and status through the same generic template-driven surface as every other game type, with no game-specific branching, per FR-024 (missing)
- [X] T140 State in /home/valgul/project/kubernetes-game-dashboard/docs/networking.md what happens to connected players when the address pool of a live server is changed — whether the assigned address is sticky or a brief disconnect occurs — per spec edge case EC-2 (missing)

---

## Phase 11: Convergence

- [X] T141 CRITICAL — Add an e2e test covering a **granted** explicit address request (`spec.networking.address` set, exposure mode LoadBalancer, the Service receiving exactly that IP from a real MetalLB and `AddressAssignment` reaching `Assigned`), and register its top-level `func Test…` in the default-executed `operator` bucket of /home/valgul/project/kubernetes-game-dashboard/test/e2e/buckets.sh in the SAME commit — today /home/valgul/project/kubernetes-game-dashboard/test/e2e/pool_assignment_e2e_test.go carries seven `TestAddressPool_*` tests and not one reference to `networking.address`, and T061–T065 cover the explicit-address path only at envtest/unit/web level against a fake client, so the FR-015 / US4 path ships with no end-to-end proof, per Constitution I and FR-015 (missing)
- [X] T142 Resolve the uncommitted design source and its mirror: `design.pen` is modified in the working tree alongside 115 modified files under /home/valgul/project/kubernetes-game-dashboard/design-export/json/ whose diffs are one line each and change only `x`/`y` canvas coordinates (sampled: `AT7ya.json`) — confirm the change is layout-only, then commit both together as a single design-hygiene change or revert both, so the git-tracked export mirror stops diverging from the Pencil source, per Constitution II (partial)
