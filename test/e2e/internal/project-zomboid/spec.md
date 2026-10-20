# project-zomboid — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_ProjectZomboidBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Undocumented custom UDP protocol; optional non-Steam private server mode possible but format unknown
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=project-zomboid make test-e2e
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/project-zomboid`  
**Dependencies:** stdlib + shared `protocol/a2s` (Go 1.25+); tested against sknnr/zomboid-dedicated-server:v1.1.1 (UNMEASURED against a real server — see "Measured connectivity")

## Purpose

End-to-end test harness for Project Zomboid, proving that a Gameplane-managed Project Zomboid server answers a real Valve A2S (Source Engine Query) query on its game port. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path a player uses.
3. Sends an A2S_INFO request to the game port (16261) via the shared `protocol/a2s` package and requires a well-formed A2S_INFO response.
4. Logs the parsed server info (name/map/players) on success, or a bounded, non-gating raw-byte diagnostic on failure.
5. Returns QUERY depth when the server answers A2S_INFO.

This is part of the game-bot test suite (`test/e2e/projectzomboid_bot_e2e_test.go`) and demonstrates that the server is bootable and answers the real query protocol.

## Responsibilities

1. **A2S_INFO query:** Send a real A2S_INFO request (via the shared `protocol/a2s` package) to the game port (16261) and parse the response.
2. **Diagnostic logging:** On A2S success, log the parsed server name/map/player count. On failure, send one bounded, non-gating raw-byte diagnostic and log the response (or explicitly log silence).
3. **Depth measurement:** Return QUERY when the server answers A2S_INFO.
4. **Retry logic:** The probe retries the query because the server may be bootstrapping.

## Non-goals / boundaries

- Does not implement a full game join. Project Zomboid's join wire protocol is not publicly documented; only the query (A2S) layer is used.
- Does not authenticate players or enter the Play state.
- Does not handle the direct port (UDP 16262) or RCON console interaction.
- Does not implement A2S_PLAYER or A2S_RULES; only A2S_INFO is queried.
- Does not claim PARTIAL or JOINED depth without understanding the join protocol.
- Does not perform Steam authentication or interact with Workshop mods.

## Directory & package layout

```
test/e2e/internal/project-zomboid/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

The probe imports the shared `probe` and `protocol/a2s` packages from `test/e2e/internal/`. No project-zomboid-specific protocol subpackage exists or is needed.

## External interface / contracts

### Shared protocol families

**`github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/a2sproto`**

```go
type Info struct {
    Protocol byte
    Name string
    Map string
    Folder string
    Game string
    ID uint16
    Players, MaxPlayers, Bots byte
    ServerType byte
    Environment byte
    Visibility byte
    VAC byte
    Version string
}
func QueryInfo(ctx context.Context, addr string) (*Info, error)
```

### Shared Probe Package

```go
var ErrFatal error
type Depth string
const (Joined Depth = "JOINED"; Partial Depth = "PARTIAL"; Query Depth = "QUERY")
type Flags struct { Addr string; Deadline time.Duration; Expect Depth }
func ParseFlags() Flags
func Retry(ctx context.Context, what string, attempt time.Duration, fn func(context.Context) error) error
func Main(f Flags, run func(context.Context) (Depth, error))
```

### App (`app.go`)

**`func main()`**
- Calls `probe.ParseFlags()` to register and parse shared flags (`-addr`, `-deadline`, `-expect-depth`).
- Calls `probe.Main()` with a closure that runs `probeProjectZomboid`.
- Exits 0 only if `probeProjectZomboid` returns the expected depth.

**`func probeProjectZomboid(ctx context.Context, addr string) (probe.Depth, error)`**
- Retries `a2s.QueryInfo` on the game port (16261, same port as `-addr` — no separate query port) until it succeeds or the deadline expires.
- On success, logs the parsed server name/map/player count and returns `probe.Query`.
- On failure, sends one bounded, non-gating raw-byte diagnostic probe (own short timeout, independent of the — by then expired — probe deadline) and returns a fatal error.

**`func logRawDiagnostic(ctx context.Context, addr string)`**
- Sends the same 4-byte zero-value packet this probe used to require a reply to, purely for evidence, and logs the raw response (hex, bounded) or explicitly logs that nothing came back.
- Purely diagnostic: never affects the probe's pass/fail outcome.

## Measured connectivity

**Status:** UNMEASURED against a real server. Project Zomboid is in the heavy set (opt-in only, never runs in CI); this probe has never been run against sknnr/zomboid-dedicated-server:v1.1.1. The A2S implementation has been verified locally against a throwaway UDP server speaking real A2S_INFO, and against a dead address (must fail) — see "Local verification" below. Neither is the same as a real Project Zomboid server.

### Protocol research (2026-07-25)

Project Zomboid's **join** wire format is a custom, undocumented UDP protocol — closed-source, no official spec, only unauthoritative community reverse-engineering. That part of the earlier spec was correct. But Project Zomboid is also a **Steamworks GameServer title**, and its Steamworks GameServer integration answers Valve's A2S (Source Engine Query) protocol on the game port, separately from the join protocol:

- **node-gamedig** — a widely used, actively maintained open-source game-server-query library (https://github.com/gamedig/node-gamedig) — documents this directly: its game definition (`lib/games.js`, `projectzomboid` entry) declares `port: 16261, protocol: 'valve'` — plain A2S, on the same port as the game, no per-game protocol override.
- This is consistent with community reports describing port 16261 as used "for Steam connection."

**Why this is stronger than the previous hand-rolled probe:**
- The earlier version sent a hand-rolled 4-byte zero packet and required any reply. That is not falsifiable: a server that only answers well-formed requests would silently drop an unrecognized packet — the same failure mode proven empirically against Factorio's query port (it silently drops unrecognized UDP datagrams with no reply at all, so a probe requiring a response to a made-up packet fails permanently against a healthy server).
- A2S_INFO is the actual documented request this port answers, per node-gamedig, so it is used now instead.

**What the probe currently does:**
- Sends a real A2S_INFO request (0xFFFFFFFF header, type 0x54, "Source Engine Query\0" magic) to UDP 16261, via the shared `protocol/a2s` package.
- On success, logs the parsed server name/map/player count and returns QUERY.
- On failure, sends one bounded, non-gating diagnostic probe (the old hand-rolled 4-byte packet, kept only as evidence) and fails.

**What this proves, if it passes against a real server:** the container is running the actual game binary and answers a real Source-family query request — not just "some UDP socket is open."
**What this does NOT prove:** that a player can actually join (the join protocol remains undocumented and unimplemented), or that Workshop mods/world state are correct.

### Local verification (not a substitute for a real-server run)

Built with `GOWORK=off go build -o /tmp/p-project-zomboid ./internal/project-zomboid`:

- **Dead address** (`-addr 127.0.0.1:59999 -deadline 8s -expect-depth QUERY`): exits 1 — every A2S attempt gets `connection refused`, the diagnostic probe explicitly logs no response.
- **Live listener**: a throwaway local UDP server bound to 127.0.0.1:16261 that parses a real A2S_INFO request and replies with a spec-correct A2S_INFO response — exits 0, logging the parsed fake server name/map/player count.

**Future escalation path:**
- Capture real server responses from a real CI/kubelab run (logs will show the parsed A2S fields).
- Independently reverse-engineer the join handshake based on community documentation, if pursued.
- Implement PARTIAL depth once a rejection packet from the join protocol is identified.
- Implement JOINED depth once a full login sequence is confirmed.

## Key invariants

1. **UDP port 16261 is both the game port and the A2S query port.** The template declares two UDP ports: 16261 (game) and 16262 (direct peer). node-gamedig confirms A2S rides the same port as the game (no separate query port, unlike V Rising or Enshrouded).

2. **The join protocol remains undocumented; the query protocol does not.** Unlike Minecraft's protocol wiki, Project Zomboid has no official join wire-format spec — that part of the earlier assessment was correct. But the query layer (A2S, via Steamworks GameServer integration) is documented by node-gamedig. The probe uses the documented layer, not the undocumented one.

3. **The assertion is falsifiable.** A2S_INFO is a real request that a correctly functioning server answers. This replaces the earlier hand-rolled 4-byte probe, which was not falsifiable for the reasons above.

4. **The probe is unprivileged.** It runs as UID 65532, read-only rootfs, dropped capabilities. It cannot execute the game, install mods, or authenticate as a real Steam player.

5. **Non-Steam server mode.** The template doesn't configure Steam GSLT or credentials. A2S query availability is independent of full Steam backend authentication (standard Steamworks GameServer SDK behavior), so this does not block the query-layer assertion — though it does still block a real join.

6. **QUERY is the honest measurement.** Returning QUERY (server answers A2S_INFO) is defensible and falsifiable. Returning PARTIAL or JOINED requires evidence from the join protocol's response; claiming either without that evidence is fabrication.

7. **Any UDP attempt beyond A2S is a pure diagnostic.** The 4-byte fallback probe sent on A2S failure never gates pass/fail; it exists purely to log evidence (including explicit "no response" logging).

8. **Storage and boot time are budgets.** The PVC is sized at 15Gi (inherited from the template). Boot time is unbudgeted initially; the first run will establish a baseline.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type
- `protocol/a2s` — A2S query protocol family

**External:**
- Stdlib only: `context`, `encoding/binary`, `encoding/hex`, `fmt`, `log`, `net`, `time`.

No external modules.

## Security considerations

1. **No credentials in argv.** The probe sends no usernames, passwords, or tokens. No authentication data is hardcoded or passed via flags.

2. **Runs from outside the games namespace.** The probe Job runs in the `default` namespace (unrestricted network policy). The game's `allow-kubelet-probes` policy admits ingress, so the probe is a legitimate observer, not an anomaly.

3. **UDP is connectionless.** Unlike TCP, UDP has no formal connection state. Sending a probe packet is non-destructive; the server's response (or lack thereof) is observable but does not commit the server to any state.

4. **Protocol is read-only.** The probe sends only an A2S_INFO request; it does not send commands, chat, or any state modifications. It observes and logs.

## Testing & coverage

**No unit tests for the probe itself.** Project Zomboid probe testing relies entirely on the e2e test (`test/e2e/projectzomboid_bot_e2e_test.go`) running against a real cluster and server. The shared `probe` package has its own unit test coverage in `test/e2e/internal/probe/probe_test.go`, and the shared `a2s` protocol family has its own coverage in `test/e2e/internal/protocol/a2sproto/a2s_test.go`.

**Manual local verification** (not a substitute for CI/real-server coverage): see "Local verification" under "Measured connectivity" above.

## Runtime characteristics

### Image Pin

- **Image:** `sknnr/zomboid-dedicated-server:v1.1.1` (explicit version tag, not floating :latest).
- **Rationale:** Floating tags (especially `:latest`) have drifted and broken tests in the past. This image is pinned to a known stable release recommended by the jsknnr/project-zomboid-server repository.

### Boot Time and Disk

**Configured budgets:**
- **Ready timeout budget:** 10 minutes (configured in `projectzomboid_bot_e2e_test.go`). First boot requires pulling the image from Docker registry.
- **Storage size:** 15Gi (allocated in PVC, inherited from the template). Actual usage depends on server world saves; should be << 15Gi.
- **Probe deadline:** 4 minutes (configured in `projectzomboid_bot_e2e_test.go`; how long the in-cluster probe retries the handshake before timing out).
- **CPU/memory requests:** 1 CPU, 4Gi memory; limits 4 CPU, 8Gi memory.

**Measurements:**
- **Boot + probe time:** UNMEASURED (not yet run on a real cluster). The first run in CI or on kubelab will establish a baseline and this field will be updated.

### Readiness Probe

The GameServer template includes a tcpSocket probe on the RCON port (27015/TCP). This is appropriate because:
- The game port (UDP 16261) is UDP-only; TCP readiness probes do not work on UDP services.
- RCON (TCP 27015) uses Source protocol and is expected to be ready after the server initializes.
- A successful TCP connection to port 27015 indicates the server has bootstrapped.

- **initialDelaySeconds:** 30 (gives the server time to initialize before the first probe attempt).
- **periodSeconds:** 10 (check every 10 seconds).
- **failureThreshold:** 10 (require 10 consecutive failures before marking unhealthy; gives bootstrap time).

### Console and RCON

The shipped template declares `rcon: protocol: source` and `passwordEnv: RCON_PASSWORD`. This is Source RCON protocol, which is separate from and independent of the game protocol. The operator generates a password and injects it via the secret, same as minecraft-java.

## Heavy Set — Never Runs in CI

**This test is part of the HEAVY SET and deliberately never runs in CI.** It is only executed when a maintainer explicitly hand-runs the full suite:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> \
  GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=all make test-e2e-keep
```

**Reason:** Project Zomboid is a heavy game:
- The image requires a full download on first pull (Proton layers + game assets).
- The server boots slowly (10+ minutes on the first run to generate world).
- Boot time and disk usage are not yet measured; they may exceed CI runner budgets.

This classification is deliberate, not temporary. The probe's source code, unit tests (if any exist), and this spec are first-class artifacts and ship with the repo. Only the *real-image boot* is maintainer-initiated.

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_ProjectZomboidBot_Query` and expects `ExpectDepth: "QUERY"` (test name unchanged by this fix). The depth is established by the server answering A2S_INFO:

- A2S_INFO request sent to UDP 16261 (the game port, same port node-gamedig documents as the query port).
- Server answers with a well-formed A2S_INFO response: proves the server is alive and speaking the real, documented Source-family query protocol.
- QUERY depth is returned: server is alive; full join not yet possible without understanding the (still undocumented) join protocol.

**What changed from the previous version:** the probe used to send a hand-rolled 4-byte zero packet and required any reply — not falsifiable, since a server that only answers well-formed requests would silently drop it (the same failure mode proven empirically against Factorio's query port). A2S_INFO is now used because node-gamedig documents it as the real protocol this port answers.

**Measured on:** Not yet — UNMEASURED against a real server (heavy set, opt-in only). Verified locally only (see "Local verification" under "Measured connectivity").

**Future escalation:**
- If the join protocol can be reverse-engineered from captured server responses (via community docs or a real CI/kubelab run), escalate to PARTIAL once a rejection packet is identified.
- If a full login sequence can be implemented and verified, escalate to JOINED.
- Evidence for any escalation must come from captured server responses, not speculation.

**Configuration details:**
- The template's `configSchema` requires `MAX_MEMORY` and `ADMIN_PASSWORD` (no defaults).
- The template sets `HOME=/home/steam`, `uid: 10000`, `fsGroup: 10000` — these are REQUIRED and must not be changed.
- The template sets `GAME_PORT: 16261` and `DIRECT_PORT: 16262` — the probe targets 16261.

## References

- **Probe application:** `test/e2e/internal/project-zomboid/app.go`
- **E2E test:** `test/e2e/projectzomboid_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **A2S protocol family:** `test/e2e/internal/protocol/a2sproto/`
- **Shipped template:** `modules/project-zomboid/template.yaml`
- **sknnr/project-zomboid-server repository:** https://github.com/jsknnr/project-zomboid-server
- **Project Zomboid Official Wiki:** https://projectzomboid.fandom.com/wiki/Server (limited documentation; no join wire-format spec)
- **node-gamedig (query protocol reference):** https://github.com/gamedig/node-gamedig — `lib/games.js` "projectzomboid" entry (`port: 16261, protocol: 'valve'`)
- **Valve A2S documentation:** https://developer.valvesoftware.com/wiki/Server_queries
