# enshrouded — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_EnshroudedBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Undocumented proprietary protocol; wire format and handshake feasibility unknown
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=enshrouded make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/enshrouded`  
**Dependencies:** stdlib only (Go 1.25+); tested against mornedhels/enshrouded-server:1.7.2

## Purpose

End-to-end test harness for Enshrouded, proving that a Gameplane-managed Enshrouded server answers real Valve A2S (Source Engine Query) queries on its query port. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path a player uses.
3. Sends an A2S_INFO request to the query port (UDP 15637) via the shared `protocol/a2s` package and requires a well-formed A2S_INFO response.
4. Logs the parsed server info (name/map/players) on success, or a bounded, non-gating raw-byte diagnostic on failure, for visibility into what the server actually sent.

This is part of the game-bot test suite (`test/e2e/enshrouded_bot_e2e_test.go`).

**Constraints and Gateway:** Enshrouded has no console of any kind (no RCON, no stdin input). The module declares `rcon.protocol: none` and `consoleMode: none`. This means Path A (control through Gameplane) is impossible — there is no remote command execution surface. Path B (direct game protocol) is the only measurement available.

## Responsibilities

1. **A2S_INFO query:** Send a real A2S_INFO request (via the shared `protocol/a2s` package) to the query port (UDP 15637) and parse the response.
2. **Diagnostic logging:** On A2S success, log the parsed server name/map/player count. On failure, send one bounded, non-gating raw-byte diagnostic probe and log the response (or explicitly log that nothing came back).
3. **Depth measurement:** Return QUERY if the query port answers A2S_INFO. Return a fatal error if it never does before the deadline, as there is no other path available.
4. **Retrying:** The probe retries because the server may be bootstrapping — it may answer queries before being fully ready for game join attempts.

## Non-goals / boundaries

- Does not implement the Play state; no movement, commands, or entity data.
- Does not authenticate via any external service or Steam.
- Does not handle RCON or console interaction (the shipped template declares no RCON at all).
- Does not attempt to join the game; the query port is the only measurement surface.
- Does not implement A2S_PLAYER or A2S_RULES; only A2S_INFO is queried (sufficient to prove the port is alive and answering the real protocol).

## Directory & package layout

```
test/e2e/internal/enshrouded/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

The probe imports the shared `protocol/a2s` package (used by several other games in this bucket — v-rising, project-zomboid, 7-days-to-die). No enshrouded-specific protocol subpackage exists or is needed.

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
    ServerType byte      // 'd' = dedicated, 'l' = listen, 'p' = SourceTV
    Environment byte
    Visibility byte
    VAC byte
    Version string
}
func QueryInfo(ctx context.Context, addr string) (*Info, error)
```

Reference: https://developer.valvesoftware.com/wiki/Server_queries

### Shared Probe Package

(Defined by `probe` package; app.go is coded against this contract.)

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
- Calls `probe.Main()` with a closure that runs `probeEnshrouded`.
- Exits 0 only if `probeEnshrouded` returns the expected depth.

**`func probeEnshrouded(ctx context.Context, addr string) (probe.Depth, error)`**
- Parses the game server address and derives the query port address (host:15637).
- Retries `a2s.QueryInfo` on the query port until it succeeds or the deadline expires.
- On success, logs the parsed server name/map/player count and returns `probe.Query`.
- On failure, sends one bounded, non-gating raw-byte diagnostic probe (own short timeout, independent of the — by then expired — probe deadline), logs whatever came back (or explicitly logs silence), and returns a fatal error.

**`func logRawDiagnostic(ctx context.Context, addr string)`**
- Sends a single non-protocol byte and logs the raw response (hex, bounded) or explicitly logs that nothing came back within the diagnostic window.
- Purely diagnostic: never affects the probe's pass/fail outcome.

## Measured connectivity

**Status:** UNMEASURED against a real server (heavy set; never runs in CI). The A2S implementation has been verified locally against a throwaway UDP server speaking real A2S_INFO (see "Local verification" below), and against a dead address (must fail). Neither is the same as a real Enshrouded server.

**Protocol research findings (2026-07-25):**
- Enshrouded's developers do not publish the query-port wire format directly.
- **node-gamedig** — a widely used, actively maintained open-source game-server-query library (https://github.com/gamedig/node-gamedig) — documents Enshrouded's query port as speaking plain Valve A2S: its game definition (`lib/games.js`, `enshrouded` entry) declares `port: 15636, port_query: 15637, protocol: 'valve'`, using the generic Source-query implementation with no per-game override (unlike e.g. 7 Days to Die's telnet-enriched `sdtd` variant). This is corroborated by community hosting guides describing port 15637 as "what the Steam server browser uses to find your server" — consistent with a Steamworks GameServer A2S query port.
- The game protocol on port 15636 (UDP) is the game join protocol (not probed; requires a join attempt, out of scope for this e2e harness).
- The server has **no console interface of any kind** — no RCON, no stdin, no remote command execution.

Therefore:
- Path A (Gameplane control) is impossible; there is no control surface.
- Path B (direct server protocol) uses the documented A2S_INFO query on the query port.
- **What this proves, if it passes against a real server:** the container is running the actual game binary, has bootstrapped its network stack, and answers a real Source-family query request — not just "some UDP socket is open" (which is what the earlier packet-and-hope-for-a-reply version could, at best, prove, and only if the server happened to be more permissive than expected).
- **What this does NOT prove:** that a player can actually join, that world generation succeeded, or that any game-specific state is correct. QUERY is a network/protocol-layer signal only.

### Local verification (not a substitute for a real-server run)

Built with `GOWORK=off go build -o /tmp/p-enshrouded ./internal/enshrouded`:

- **Dead address** (`-addr 127.0.0.1:59999 -deadline 8s -expect-depth QUERY`): exits 1 — every A2S attempt gets `connection refused`, the diagnostic probe explicitly logs no response, exit code is non-zero.
- **Live listener**: a throwaway local UDP server bound to 127.0.0.1:15637 that parses a real A2S_INFO request (validates the `0xFFFFFFFF` header, request type `0x54`, and the `"Source Engine Query\0"` magic string) and replies with a spec-correct A2S_INFO response — exits 0, logging the parsed fake server name/map/player count.

## Key invariants

1. **Query port is the only measurement surface.** Enshrouded has no console and no TCP port at all (only UDP 15636 game / 15637 query), so there is no other network path to probe and no TCP fallback is possible — unlike, e.g., 7 Days to Die or Project Zomboid.

2. **The probe is read-only.** It sends only A2S_INFO requests (and, on failure, one bounded diagnostic byte) but does not attempt game joins, state modifications, or multi-packet handshakes.

3. **The assertion is falsifiable.** A2S_INFO is a real, documented request (see node-gamedig citation above) that a correctly functioning server answers. This replaces an earlier version that sent an invented single byte and required any reply — not falsifiable, since a server that only answers well-formed requests would silently drop it (the exact failure mode proven empirically against Factorio's query port, which drops unrecognized UDP datagrams with no reply at all).

4. **QUERY is the achievable depth.** If the query port answers A2S_INFO, that is the deepest depth we can defend. We do not assume it means the server is accepting players — it only means the port is alive and speaking the real query protocol.

5. **No Path A measurement is possible.** The shipped template declares `rcon.protocol: none` and `consoleMode: none`. There is no stdin, no RCON, no command execution. The Gameplane stack has no control surface to exercise on Enshrouded.

6. **Any UDP attempt beyond A2S is a pure diagnostic.** The one-byte fallback probe sent on A2S failure never gates pass/fail; it exists purely to log evidence (including explicit "no response" logging) for whoever investigates a real failure.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type
- `protocol/a2s` — A2S query protocol family

**External:**
- Stdlib only: `context`, `encoding/hex`, `fmt`, `log`, `net`, `time`.

No external modules.

## Security considerations

1. **No credentials in argv.** The probe sends only an A2S_INFO request (and, on failure, one diagnostic byte); no passwords or tokens are passed.

2. **No state modification.** The probe is read-only; it does not send commands or modify server state.

3. **Protocol is a real, documented query.** A2S_INFO is Valve's standard Server Queries protocol (https://developer.valvesoftware.com/wiki/Server_queries), parsed via the shared `protocol/a2s` package.

## Testing & coverage

**No unit tests for the probe itself.** Enshrouded probe testing relies entirely on the e2e test (`test/e2e/enshrouded_bot_e2e_test.go`) running against a real cluster and server. The shared `a2s` protocol family has its own unit test coverage in `test/e2e/internal/protocol/a2sproto/a2s_test.go`.

**Manual local verification** (not a substitute for CI/real-server coverage): see "Local verification" under "Measured connectivity" above.

## Runtime characteristics

### Image Pin

- **Image:** `mornedhels/enshrouded-server:1.7.2` (explicit version tag, not floating :latest).
- **Rationale:** Floating tags (especially `:latest`) have drifted and broken tests in the past. This image is pinned to a specific production release.

### Boot Time and Disk

**Configured budgets:**
- **Ready timeout budget:** 20 minutes (configured in `enshrouded_bot_e2e_test.go`). First boot requires pulling the image from Docker registry and setting up game world data.
- **Storage size:** 25Gi (allocated in PVC; per the shipped module template; actual usage depends on world generation and save data).
- **Probe deadline:** 4 minutes (configured in `enshrouded_bot_e2e_test.go`; how long the in-cluster probe retries query port probes before timing out).
- **CPU/memory requests:** 2 CPU, 8Gi memory; limits 4 CPU, 16Gi memory.

**Measured in CI:** (pending first run)

### Readiness Probe

The GameServer template includes no custom readiness probe for Enshrouded (operator default behavior applies). Kubernetes will consider the pod Ready once all containers are Running. The actual gameplay readiness (world generation, etc.) is measured by the bot probe's query port connectivity.

### Console and RCON

The shipped template declares `rcon: protocol: none` and `consoleMode: none`. Enshrouded, by design, has no remote console of any kind. There is no way to send commands to the server remotely. This is a product limitation, not a test limitation.

### Heavy Set

Enshrouded is included in the **heavy set** of bot tests (runs only with `GAMEPLANE_E2E_GAMES=all`). The heavy set is opt-in only (never runs in CI) due to large disk and boot-time requirements. Enshrouded requires 25Gi of storage and significant boot time to initialize world generation.

The command to run heavy games against an existing cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=all make test-e2e-keep
```

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_EnshroudedBot_Query` and expects `ExpectDepth: "QUERY"`. The depth is established by the query port (UDP 15637) answering a real A2S_INFO request:

- A2S_INFO answered: proves QUERY depth — the server is alive and speaking the real, documented query protocol (not just "some UDP socket accepted a byte").
- A2S_INFO never answered before the deadline: fatal error, since there is no other path available (no console, no RCON, no TCP port).

**What changed from the previous version:** the probe used to send a single invented byte and require any reply, which is not falsifiable — a server that only answers well-formed requests (as Factorio's query port empirically does — it silently drops unrecognized packets) would fail this test permanently even while healthy. A2S_INFO is now used because node-gamedig documents it as the real protocol this port answers (see "Measured connectivity" above); this is item 1 in the fix's preference order ("a documented query protocol, if one genuinely exists — cite it"), the strongest option available since Enshrouded has no TCP port to fall back to (item 2 does not apply here).

**Escalation path (future work):**
1. Parse A2S_PLAYER / A2S_RULES for richer evidence (player count from A2S_INFO is already available but unused for gating).
2. Escalate to PARTIAL or JOINED if a join handshake can be initiated without credentials (still undocumented).

For now, QUERY is the honest, defensible measurement — and, unlike before, one that can actually pass against a healthy real server.

## References

- **Probe application:** `test/e2e/internal/enshrouded/app.go`
- **E2E test:** `test/e2e/enshrouded_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **A2S protocol family:** `test/e2e/internal/protocol/a2sproto/`
- **Shipped template:** `modules/enshrouded/template.yaml`
- **Enshrouded server image:** https://github.com/mornedhels/enshrouded-docker
- **Enshrouded official site:** https://www.enshrouded.com
- **node-gamedig (query protocol reference):** https://github.com/gamedig/node-gamedig — `lib/games.js` "enshrouded" entry (`port_query: 15637, protocol: 'valve'`); `GAMES_LIST.md` lists it under "Valve Protocol"
- **Valve A2S documentation:** https://developer.valvesoftware.com/wiki/Server_queries
