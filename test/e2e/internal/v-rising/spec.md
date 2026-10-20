# v-rising — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_VRisingBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Undocumented proprietary UDP protocol; transport layer unidentified
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=v-rising make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/v-rising`  
**Dependencies:** stdlib + shared protocol family `a2s` (Go 1.25+); tested against trueosiris/vrising:2026-04-23-1036 (UNMEASURED against a real server — see "Measured connectivity")

## Purpose

End-to-end test harness for V Rising, proving that a Gameplane-managed V Rising server answers a real Valve A2S (Source Engine Query) query on its query port. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path a player uses.
3. Sends an A2S_INFO request to the query port (9877) via the shared `protocol/a2s` package and requires a well-formed A2S_INFO response.
4. On failure, sends one bounded, non-gating raw-byte diagnostic probe for evidence.
5. Logs all probe results and measurements for visibility into server responsiveness.

This is part of the game-bot test suite (`test/e2e/vrising_bot_e2e_test.go`) and demonstrates that the server boots, initializes its network stack, and answers the real query protocol on the query port.

## Responsibilities

1. **A2S_INFO query:** Send a real A2S_INFO request (via the shared `protocol/a2s` package) to the query port (9877) and parse the response.
2. **Diagnostic logging:** On A2S success, log the parsed server name/map/player count. On failure, send one bounded, non-gating raw-byte diagnostic and log the response (or explicitly log silence).
3. **Depth measurement:** Return QUERY if the query port answers A2S_INFO. Deeper join depths cannot be measured without documented join-protocol knowledge.
4. **Retrying:** The probe retries A2S because the server may be bootstrapping — it may bind the query port after the game port becomes ready.

## Non-goals / boundaries

- Does not implement the play state; no movement, commands, or entity data.
- Does not authenticate or perform a real join; CI has no player accounts or credentials.
- Does not implement A2S_PLAYER or A2S_RULES; only A2S_INFO is queried.
- Does not attempt to reach deeper join depths (PARTIAL or JOINED) without confirmed join-protocol documentation (V Rising's actual join wire format remains undocumented; only the query layer is proven here).

## Directory & package layout

```
test/e2e/internal/v-rising/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

The probe imports the shared `a2s` protocol family from `test/e2e/internal/protocol/`. No game-specific protocol subpackage; V Rising's query port speaks plain A2S (see "Measured connectivity" below), so no per-game wire-format work is needed.

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

### App (`app.go`)

**`func main()`**
- Calls `probe.ParseFlags()` to register and parse shared flags (`-addr`, `-deadline`, `-expect-depth`).
- Calls `probe.Main()` with a closure that runs `probeVRising`.
- Exits 0 only if `probeVRising` returns the expected depth.

**`func probeVRising(ctx context.Context, addr string) (probe.Depth, error)`**
- Retries `a2s.QueryInfo` until successful, deadline expires, or a fatal error occurs.
- On A2S success, logs server name, map, and player count; returns `probe.Query`.
- On A2S failure (deadline exhausted), sends one bounded, non-gating raw-byte diagnostic probe (own short independent timeout — the shared probe deadline is expired by this point) and returns a fatal error.

**`func logRawDiagnostic(ctx context.Context, addr string)`**
- Sends a single non-protocol byte and logs the raw response (hex, bounded) or explicitly logs that nothing came back within the diagnostic window.
- Purely diagnostic: never affects the probe's pass/fail outcome.

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

## Measured connectivity

**Status:** UNMEASURED against a real server. V Rising is in the heavy set (opt-in only, never runs in CI); this probe has never been run against trueosiris/vrising. The A2S implementation has been verified locally against a throwaway UDP server speaking real A2S_INFO, and against a dead address (must fail) — see "Local verification" below.

**Server:** trueosiris/vrising (V Rising dedicated server, Stunlock Studios)  
**Query port:** UDP 9877  
**Protocol:** Valve A2S (Source Engine Query) — see below

### Protocol research (2026-07-25)

An earlier version of this spec stated V Rising's query protocol was "Unknown (not A2S, not published by Stunlock)." That was wrong. **node-gamedig** — a widely used, actively maintained open-source game-server-query library (https://github.com/gamedig/node-gamedig) — documents V Rising's query port directly: its game definition (`lib/games.js`, `vrising` entry) declares `protocol: 'valve', port_query_offset: [1, 15]` — i.e. try A2S at game-port+1 first (falling back to game-port+15). This template's game/query port pair (9876 game / 9877 query, per Stunlock's own default configuration) is exactly the game-port+1 candidate. There is no per-game protocol override for V Rising in node-gamedig, meaning it uses the exact same generic Source-query wire format our shared `protocol/a2s` package implements.

**Why the earlier version treated A2S as a mere diagnostic:** the raw-UDP fallback it fell back to on "A2S failure" was itself not falsifiable (an invented packet requiring a reply — the same failure mode proven empirically against Factorio's query port) — but that defect doesn't mean A2S itself was speculative. It is in fact the real, documented protocol; the fix removes the unfalsifiable fallback rather than the sound primary check.

**A latent control-flow bug also existed and is fixed here:** `probe.Retry` consumes the entire remaining deadline retrying A2S before returning an error, so the old raw-UDP fallback — itself wrapped in another `probe.Retry` call sharing the same parent context — always started with an already-expired context and could never actually dial. It was dead code in practice, not just unsound in principle.

**What this proves, if it passes against a real server:** the container is running the actual game binary, has bootstrapped its network stack, and answers a real Source-family query request.
**What this does NOT prove:** that a player can actually join (V Rising's join protocol remains undocumented) or that world state is correct.

### Local verification (not a substitute for a real-server run)

Built with `GOWORK=off go build -o /tmp/p-v-rising ./internal/v-rising`:

- **Dead address** (`-addr 127.0.0.1:59999 -deadline 8s -expect-depth QUERY`): exits 1 — every A2S attempt gets `connection refused`, the diagnostic probe explicitly logs no response.
- **Live listener**: a throwaway local UDP server bound to 127.0.0.1:9877 that parses a real A2S_INFO request and replies with a spec-correct A2S_INFO response — exits 0, logging the parsed fake server name/map/player count.

## Key invariants

1. **A2S is the real, documented protocol, not a mere diagnostic.** node-gamedig confirms V Rising's Steamworks GameServer integration answers A2S on game-port+1 (9877 here) — see above. It is the sole basis for QUERY depth.

2. **Any UDP attempt beyond A2S is a pure diagnostic.** If A2S fails, the probe sends one bounded, non-gating raw byte purely for evidence (including explicit "no response" logging); it can never grant QUERY depth on its own, unlike the earlier version.

3. **Port 9877 is UDP.** The template declares port 9877/UDP for queries. The probe uses UDP exclusively.

4. **Retrying.** The probe retries A2S because the server may be bootstrapping — it may bind the query port after the game port becomes ready or after initial world generation. The deadline bounds the retry loop.

5. **QUERY depth is maximal.** A2S_INFO answering proves the server is alive and speaking a real protocol; it does not prove a player can join, which would require the (still undocumented) join protocol.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type
- `protocol/a2s/` — A2S query protocol family (primary, sole gating assertion)

**External:**
- Stdlib only: `context`, `encoding/hex`, `fmt`, `log`, `net`, `time`.

No external modules.

## Security considerations

1. **No credentials in argv.** The probe sends no player names, passwords, or tokens; it only queries for server status.

2. **Read-only queries.** All probes (A2S, the diagnostic raw byte) are observational — they request status data and do not send commands, chat, or state modifications.

3. **Unprivileged execution.** The probe runs as an unprivileged Kubernetes Job (same security context as other per-game probes).

## Testing & coverage

**No unit tests for the probe itself.** V Rising probe testing relies entirely on the e2e test (`test/e2e/vrising_bot_e2e_test.go`) running against a real cluster and server. The shared `a2s` protocol family has its own unit test coverage in `test/e2e/internal/protocol/a2sproto/a2s_test.go`.

**Manual local verification** (not a substitute for CI/real-server coverage): see "Local verification" under "Measured connectivity" above.

## Runtime characteristics

### Image Pin

- **Image:** `trueosiris/vrising:2026-04-23-1036` (explicit version pin, not floating :latest).
- **Rationale:** Floating tags can drift and break tests; this image is pinned to a specific release known to work with the test infrastructure.

### Boot Time and Disk

**Configured budgets (from `vrising_bot_e2e_test.go`):**
- **Ready timeout budget:** 15 minutes (configured in the test). V Rising's initial boot includes world initialization and may pull the image from the registry on first run.
- **Storage size:** 20Gi (configured in `modules/v-rising/template.yaml`; covers both game install and persistent world data).
- **Probe deadline:** 4 minutes (the in-cluster probe retries A2S and UDP queries before timing out).
- **CPU/memory requests:** 1 CPU, 4Gi memory; limits 4 CPU, 8Gi memory.

**Measurements:**
- **Boot + probe time:** Not yet measured (first CI run pending).
- **Actual disk usage:** Not yet measured.

### Readiness Probe

The GameServer template includes a tcpSocket probe on the RCON port (25575/TCP). This is a conventional liveness check for Stunlock's V Rising RCON service (enabled via `HOST_SETTINGS_Rcon__Enabled=true`). The query port (9877/UDP) does not have a built-in readiness check; the test relies on polling `GameServer.status.phase == Running`.

- **initialDelaySeconds:** 30 (gives the server time to initialize).
- **periodSeconds:** 10 (check every 10 seconds).
- **failureThreshold:** 30 (require 30 consecutive failures before marking unhealthy).

### Console and RCON

V Rising uses Source RCON (confirmed via Stunlock's documentation). The template declares `rcon.protocol: source` on port 25575 and enables RCON via the `HOST_SETTINGS_Rcon__Enabled` environment variable. The operator injects the RCON password via `rcon.passwordEnv: HOST_SETTINGS_Rcon__Password`.

## Heavy Set

V Rising is included in the **heavy set** of bot tests (runs only with `GAMEPLANE_E2E_GAMES=all`, never in CI). The heavy set includes all games not in the fast set (Minecraft Java, Terraria, Factorio, Garry's Mod). Heavy games are opt-in because they require large disk allocations (20+ GB each for V Rising and others like CS2, 7-days-to-die, etc.), and multiple concurrent boots exhaust GitHub-hosted runner resources.

**Manual run command:**
```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=all make test-e2e-keep
```

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_VRisingBot_Query` and expects `ExpectDepth: "QUERY"` (test name unchanged by this fix). The depth is established solely by A2S:

- **A2S succeeds:** Logs server metadata and returns QUERY depth.
- **A2S fails (deadline exhausted):** Sends one bounded, non-gating diagnostic probe for evidence, then fails — no fallback can grant QUERY depth.

**What changed from the previous version:** the probe used to treat A2S as a mere "diagnostic" and fall back to a hand-rolled raw UDP probe that also granted QUERY on any reply. That fallback was not falsifiable (the same failure mode proven empirically against Factorio's query port) and was also dead code in practice (see "Measured connectivity" above for why). A2S is in fact the real, documented protocol here (confirmed via node-gamedig), so it is now the sole basis for QUERY depth, and the previous unfalsifiable fallback is gone.

**Configuration details:**
- Server name is configured via the required `SERVERNAME` configSchema entry.
- World name is configured via the `WORLDNAME` configSchema entry (defaults to "world1").
- RCON is enabled via `HOST_SETTINGS_Rcon__Enabled=true`.

## References

- **Probe application:** `test/e2e/internal/v-rising/app.go`
- **E2E test:** `test/e2e/vrising_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **A2S protocol family:** `test/e2e/internal/protocol/a2sproto/` (primary, sole gating assertion)
- **Shipped template:** `modules/v-rising/template.yaml`
- **Docker image:** https://github.com/TrueOsiris/docker-vrising (trueosiris/vrising)
- **node-gamedig (query protocol reference):** https://github.com/gamedig/node-gamedig — `lib/games.js` "vrising" entry (`protocol: 'valve', port_query_offset: [1, 15]`)
- **Valve A2S documentation (protocol reference):** https://developer.valvesoftware.com/wiki/Server_queries
- **V Rising official site:** https://vrising.gg (no public join-protocol documentation found)
