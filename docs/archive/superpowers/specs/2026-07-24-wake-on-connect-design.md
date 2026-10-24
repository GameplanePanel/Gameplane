> **Historical, superseded.** This July 2026 document is kept for reference only. The work it describes has shipped and the code may differ from it. Current behavior is described in `sentinel/specs.md`. See [docs/archive](../../README.md).

# Wake-on-connect for idle auto-sleep

**Date:** 2026-07-24  
**Status:** shipped; historical (see the banner above)

## Problem

Idle auto-sleep shipped (operator PR #180, dashboard PR #182). A `GameServer` with `spec.idle.enabled` scales to zero after N minutes with no players and wakes on a cron `wakeWindows` tick or `POST /servers/{name}:wake`. A player who finds a server asleep cannot start it by attempting to join — the connection fails immediately because no listener is running.

This spec closes that gap by intercepting the initial connection, triggering the wake, and proxying the player through as soon as the game becomes joinable, or gracefully declining if the wake times out.

## Fixed decisions

The following are established constraints and do not require re-evaluation.

1. **Hold-and-proxy with a pre-timeout bounce.** Accept the incoming connection and trigger the wake annotation. Hold the client while polling for the game to wake. Proxy through bidirectionally if the game becomes joinable within a deadline. On deadline expiry, send a protocol-native "waking up, try again" disconnect message *before* the client's own timeout fires, never a raw TCP/UDP error.

2. **Parse where we can, heuristic elsewhere.** Minecraft and Terraria receive real handshake parsing, so only a genuine login attempt wakes them and a server-list query/ping is answered in place without waking. UDP-only games and others use an N-packets-from-one-source heuristic.

3. **All four `expose` modes supported.** ClusterIP, NodePort, LoadBalancer, and Hostport all function correctly.

4. **Per-server sentinel Deployment.** A sentinel Deployment `<gs>-waker` is created when the GameServer enters sleep and deleted when it wakes. The sentinel never mounts the game PVC, ensuring a sleeping server's volume remains free for Backup Jobs. (Verified: the backup controller mounts `<name>-data` directly and does not require a running agent or game pod.)

5. **Opt-in, defaulting false.** `spec.idle.wakeOnConnect` is a new boolean field that defaults to `false`. Existing installs upgrade without change.

## Port survey and protocol selection

Of the 16 shipped modules, only `minecraft-java` (port 25565/TCP) and `terraria` (port 7777/TCP) advertise a TCP-only game port. `7-days-to-die` and `satisfactory` advertise both TCP and UDP. The remaining 12 advertise UDP only.

This inventory drives the protocol parsing strategy:

- **Minecraft:** real VarInt packet framing and login handshake parsing. Distinguishes Join from Status queries. Only login wakes the server.
- **Terraria:** real packet framing (`[len uint16 LE][type][payload]`). Recognizes connection initiation. Initiations wake; other packet types do not.
- **UDP-only and others:** N-packets-from-one-source heuristic. Wake on first packet from a new source.

## Architecture

### `gameproto/` module

A new top-level Go package following the precedent of `netguard/` and `gameaction/`. Holds:

- Minecraft VarInt and packet framing (`int`, `string`, `Position` types)
- Minecraft login handshake parsing (`NextState` from Handshake packet)
- Minecraft status query detection
- Terraria packet framing and connection-detection
- A `Classify(protocol string, data []byte) (JoinAttempt | StatusQuery | Unknown)` entry point

This module is shared because the sentinel (a separate Go module that cannot import `operator/internal/...` or `test/e2e/internal/...`) needs the protocol parsing logic.

### `sentinel/` module

A new Go module and distroless image, following the same shape as `mcp-server/`. Responsibilities:

- Accept incoming connections on advertised game ports
- Perform minimal protocol parsing via `gameproto.Classify`
- Issue the wake annotation (`gameplane.local/idle-wake-requested`) via a Kubernetes client
- Hold TCP connections and proxy to the game pod's `-game-direct` Service (see below)
- For UDP, wake-and-drop (the client retries)
- On timeout deadline, send a protocol-native disconnect and close

### Sequencing and port handover

A new Service `<gs>-game-direct` — an always-ClusterIP Service selecting the game pod directly — is created alongside the GameServer. This service cannot be the public endpoint because the sentinel *is* that endpoint while asleep.

**Into sleep:**

1. Sentinel Deployment created, not yet scheduled
2. Wait for sentinel Ready
3. Game Service selector flips from `gameserver: <name>` to `gameserver: <name>-waker`
4. StatefulSet scales to 0 (existing `softStop` sequencing)

**Out of sleep:**

1. Wake annotation set by client connection or API call
2. StatefulSet scales to 1
3. Game pod reaches Ready
4. Game Service selector flips back to `gameserver: <name>`
5. Sentinel Deployment deleted last

Connections already held in the proxy survive the flip-back because they connect through `-game-direct`, not the re-pointed game Service.

**Hostport correction (#730, October 2026):** Hostport uses a separate handoff.
Before its first sleep, the operator saves the live game's node name and Node UID
in `gameplane.local/hostport-node` and `gameplane.local/hostport-node-uid`. These
annotations survive Pod deletion and operator restart; the dashboard's current
`gameplane.local/node` annotation remains a separate, transient observation.
The game completes its normal graceful stop and scales to zero. Only after the
StatefulSet acknowledges that generation and a direct API list confirms all game
Pods have disappeared does the operator create the sentinel. The sentinel has
required node affinity for that identity, the game's tolerations and scheduling
constraints, and a Recreate strategy. It never bypasses the scheduler with
`spec.nodeName`.

On wake, foreground deletion removes the sentinel Deployment and its children.
The game remains at zero until a direct API read confirms the Deployment is gone
and no sentinel Pods remain, including terminating Pods. The game then starts
with the same required endpoint-node affinity. Hostport clients still reconnect;
the old "briefly Pending, self-heals" sequencing is superseded. Service-backed
exposure retains the hold-and-proxy drain described above.

`HostportWakeReady=False` explains `GameDraining`, `SentinelDraining`,
`EndpointNodeUnknown`, `EndpointNodeUnavailable`, `EndpointUnschedulable`, or
`ListenerPending`. A full node stays pinned and reports the scheduler's reason;
a missing, replaced or unready node makes wake unavailable. The operator never
silently changes the advertised node. Unrelated child reconciliation continues
during these waits. After restoring node capacity/readiness, reconciliation
resumes automatically. For intentional migration, suspend the server, wait for
both game and sentinel Pods to disappear, clear both durable endpoint annotations,
update scheduling/storage placement and the client-facing node address, then
resume the game before sleeping it again. Do not clear the annotations while a
listener remains active. An installation upgraded while already asleep without
a recorded game node must first start its game to establish placement.

**Wake budget correction (#729):** The join deadline starts before the Kubernetes
PATCH, which has a separate five-second maximum within that budget. Concurrent
callers share the actual result of the in-flight PATCH. UDP packet reads enqueue
coalesced requests to one bounded worker and never wait on the PATCH. Successful
TCP proxy sessions use the longer session/drain context after upstream connects.

### Connection handling state machine

**TCP (Minecraft, Terraria, any protocol):**

```
Read handshake/header (5s deadline)
├─ Classify as Join, Status, or Unknown
├─ Status query: answer in place ("Asleep — joining wakes it"), close
├─ Join attempt: 
│   ├─ Patch spec.idle-wake-requested annotation (same as API wake handler)
│   ├─ Poll <gs>-game-direct Service for a live endpoint (every 100ms)
│   ├─ On endpoint found: proxy bidirectionally
│   └─ On timeout (25s default): send protocol-native disconnect, close
└─ Unknown: proxy immediately if the game is already awake, else close
```

**UDP:**

```
Receive first packet from source (no connection state)
├─ Patch spec.idle-wake-requested annotation
├─ Respond immediately (no hold possible)
└─ Close / stop listening on this source
```

The 25s TCP hold deadline is tuned to fire before Minecraft's ~30s client timeout, ensuring the player sees the sentinel's "waking up" message rather than a generic connection error.

## CRD surface

### `IdleSpec` additions

```go
// WakeOnConnect enables proxying for incoming connections to a sleeping
// game server. The server wakes on connection and the client is held and
// proxied through as soon as the game is joinable. When disabled (default),
// connections to a sleeping server are rejected immediately.
// +optional
WakeOnConnect bool `json:"wakeOnConnect,omitempty"`
```

### `GamePort` additions

```go
// WakeProtocol classifies how to detect a connection-wake trigger on this port.
// "minecraft" performs real handshake parsing; "terraria" performs real packet
// parsing; "generic" uses an N-packets-from-one-source heuristic; "none" disables
// wake-on-connect for this port. Default is "generic".
// +kubebuilder:validation:Enum=minecraft;terraria;generic;none
// +kubebuilder:default=generic
// +optional
WakeProtocol string `json:"wakeProtocol,omitempty"`
```

These additions avoid cross-field CEL validation that would violate the apiserver's cost budget (a CEL rule inside an unbounded CRD map/array causes the apiserver to reject the CRD; envtest panics "unable to install CRDs"). The existing `GamePort.Port`, `GamePort.ContainerPort`, and `GamePort.Protocol` fields already bound the array to reasonable sizes.

## Known landmines

### Operator RBAC split across two roles

The operator's effective RBAC is **not** in `operator/config/rbac/role.yaml` (which grants only get/list/watch on statefulsets). It lives in two places:

- **Cluster-wide** `ClusterRole` in `charts/gameplane/templates/operator.yaml` for `watch`-only operations
- **Namespaced** `Role` in the same template for `create`/`update`/`patch`/`delete` operations

The `controller-manager.Owns(&appsv1.Deployment{})` call issues a **cluster-wide** LIST/WATCH, so `apps/deployments` must be added to **both** the ClusterRole and the namespaced Role. If the ClusterRole half is missing, the manager's cache never syncs, the operator crash-loops, and all reconciliation stops.

### Egress policies in games namespace

The `default-deny-egress` NetworkPolicy in the games namespace uses `podSelector: {}`, applying to **every** pod in that namespace, including the sentinel. The sentinel is thereby DNS-only, unable to reach either the apiserver or the game pod.

Three policy additions are required:

1. Inbound to the sentinel on its advertised ports (optional if the policy already allows inbound from outside the namespace)
2. Egress to the apiserver IP and port (e.g., `10.96.0.1:443` for in-cluster DNS resolving to the apiserver Service)
3. Egress to the game pod IP on the loopback/localhost interface for proxying (or the pod's own IP if intra-pod forwarding is used)

These must be deployed **before or alongside** the sentinel Deployment; otherwise the sentinel blocks and proxying never opens.

## Out of scope

- Flipping the default of `WakeOnConnect` to `true`
- Per-protocol parsing beyond Minecraft and Terraria
- Exposing `status.idle.nextWakeTime` in the API or dashboard
- Automatic retry-with-backoff for clients that time out (the client retries per its own logic)

## Sequencing

Two phases:

1. **Protocol and sentinel infrastructure.** `gameproto/` module, `sentinel/` module and image, operator sequencing for sentinel creation/deletion and port handover, CRD additions, Kubernetes RBAC fixes.
2. **Integration.** Operator integration tests for the sequencing. E2E testing of wake-on-connect via the sentinel against live Minecraft and Terraria servers. Live-cluster smoke testing before shipping.

## Testing

Per CLAUDE.md rule 8, all tests run on CI, not locally.

- **`gameproto/`** — unit tests for Minecraft VarInt/packet parsing, Terraria packet framing, and the `Classify` entry point. Cover valid and malformed packets, partial reads, and edge cases.
- **`sentinel/`** — unit tests for the connection state machine against a fake game Service backend, with fake client connections (TCP and UDP). Cover the hold-and-proxy path, timeout-and-disconnect path, and the heuristic waking path.
- **Operator** — envtest for Deployment creation/deletion, Service selector flipping, and the sequencing on state transitions (SleepingToWaking, WakingToRunning, RunningToSleeping). Verify the sentinel is never created with the game PVC mounted.
- **E2E** — a new e2e test (registering in `test/e2e/buckets.sh`) that wakes a sleeping Minecraft server by connection, verifies the player logs in, and wakes a sleeping Terraria server similarly. Both tests verify no manual API wake is required.

## Decisions and rejected alternatives

- **Hold-and-proxy vs. immediate rejection.** Rejecting immediately and asking the player to wait is simpler but degrades the UX (the player sees a connection failure, not a "waking up" message). Hold-and-proxy requires a sentinel and proxy logic, but delivers a seamless wake to the player's experience.
- **Generic TCP proxy vs. protocol-aware.** A generic proxy works for all protocols but fires a wake on every port interaction (health checks, monitoring, scanners). Protocol parsing limits wakes to genuine login attempts. The cost is implementing parsing for Minecraft and Terraria (the two high-value cases); the payoff is both correctness and reduced spurious wakes.
- **Sentinel Deployment vs. DaemonSet.** A per-server Deployment is created/deleted on sleep/wake transitions. A DaemonSet that existed globally would require per-port multiplexing and conflict-resolution logic, and could not carry Hostport bindings. A Deployment is simpler and co-scoped with the GameServer's lifecycle.
- **Shared `gameproto/` module.** The sentinel is its own Go module that cannot import from `operator/internal/...` (internal packages are not public API). Protocol parsing must be shareable, so it lives in a top-level `gameproto/` module following the `netguard/` and `gameaction/` precedent.
- **NetworkPolicy additions, not a relaxation.** Keeping the default-deny-egress policy active forces explicit allow rules, which is the correct security posture. The policy additions are scoped narrowly (only sentinel and game pod, only necessary destinations).
