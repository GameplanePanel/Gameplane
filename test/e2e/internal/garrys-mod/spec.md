# garrys-mod — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_GarrysModBot_Query
- **Bucket**: bot-fast
- **Last Verified**: —
- **Blocker**: C2S_CONNECT field offsets unknown; challenge exchange already decoded, sv_lan 1 removes the Steam gate
- **Blocker Class**: documentation

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/garrys-mod`  
**Dependencies:** stdlib only (Go 1.25+); tested against ceifa/garrysmod:debian; shared protocol families `a2s`, `source`

## Purpose

End-to-end test harness for Garry's Mod, proving that a Gameplane-managed Garry's Mod server is playable via the Source engine protocol. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path a player uses.
3. Performs an A2S (All-Seeing-Eye) query to verify the server is alive and responsive to status queries (establishes QUERY depth).
4. Attempts a Source engine protocol connection challenge and handshake (escalates to PARTIAL or JOINED depth).
5. Logs the raw connect response bytes for visibility into what the server actually sent.

This is part of the game-bot test suite (`test/e2e/garrysmod_bot_e2e_test.go`) and demonstrates that server discovery and join attempts succeed end-to-end.

## Responsibilities

1. **A2S protocol:** Query the server's info (name, map, player counts, version) to verify it is alive and protocol-responsive.
2. **Source connection:** Execute a source-protocol challenge/response handshake to test login depth.
3. **Depth measurement:** Return QUERY (A2S succeeds), with source connection as diagnostic only (never changes depth).
4. **Raw response logging:** Log the raw bytes of the connect response (hex-encoded) for debugging.
5. **Retrying:** The probe retries both A2S and source queries because the server may be bootstrapping — it may answer queries before accepting connections.

## Non-goals / boundaries

- Does not implement the Play state; no movement, commands, or entity data.
- Does not authenticate via Steam; relies on `sv_lan 1` to allow unauthenticated joins.
- Does not handle RCON or console interaction (the shipped template sets `rcon.protocol: none`).
- Does not handle credential gates beyond PARTIAL depth; if the server requires Steam auth despite `sv_lan 1`, that is reported as PARTIAL, not pursued further.
- Does not perform multi-attempt recovery on authentication failure (a failed connect is final).

## Directory & package layout

```
test/e2e/internal/garrys-mod/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

The probe imports the shared `a2s` and `source` protocol families from `test/e2e/internal/protocol/`. No game-specific protocol subpackage; Garry's Mod uses the standard Source engine A2S and source-protocol queries.

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
func QueryPlayers(ctx context.Context, addr string) ([]Player, error)
```

**`github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/sourceproto`**

```go
func Challenge(ctx context.Context, addr string) (uint32, error)

type ConnectResult struct {
    Accepted bool
    RejectMsg string
    Raw []byte     // raw server response bytes (hex-encoded in logs)
}
func Connect(ctx context.Context, addr string, challenge uint32, name string, protocol uint32) (*ConnectResult, error)
```

### App (`app.go`)

**`func main()`**
- Calls `probe.ParseFlags()` to register and parse shared flags (`-addr`, `-deadline`, `-expect-depth`).
- Calls `probe.Main()` with a closure that runs `probeGarrysMod`.
- Exits 0 only if `probeGarrysMod` returns the expected depth.

**`func probeGarrysMod(ctx context.Context, addr string) (probe.Depth, error)`**
- Retries A2S.QueryInfo until successful or deadline expires.
- Logs server name, map, and player count from the A2S response (indicates QUERY depth at minimum).
- Retries source.Challenge + source.Connect until successful or deadline expires.
- On successful connect, logs whether accepted and the raw response bytes (hex-encoded).
- Returns `probe.Joined` if the server accepted the connection, `probe.Partial` if the protocol handshake completed but the server rejected the login (auth gate), or `probe.Query` if only A2S worked.

### Shared Probe Package

(Defined by parallel agent; app.go is coded against this contract.)

```go
var ErrFatal error
type Depth string
const (Joined Depth = "JOINED"; Partial Depth = "PARTIAL"; Query Depth = "QUERY")
type Flags struct { Addr string; Deadline time.Duration; Expect Depth }
func ParseFlags() Flags
func Retry(ctx context.Context, what string, attempt time.Duration, fn func(context.Context) error) error
func Main(f Flags, run func(context.Context) (Depth, error))
```

## Measured connect evidence

### Round 1 — Protocol Version 17 (2026-07-24)

**Date:** 2026-07-24  
**Server:** ceifa/garrysmod:debian, `sv_lan 1`, AUTOUPDATE=0  
**CI run:** PR #197, job `e2e game bot`  
**Boot + probe time:** 134.82 seconds (one observation, not an average)

After A2S_INFO succeeded, the probe attempted a Source protocol connect handshake with **protocol version 17**:

- **A2S_GETCHALLENGE:** succeeded, challenge obtained
- **C2S_CONNECT:** sent with protocol 17; parsed by server; rejected with response type **0x39** + "game#GameUI_ServerRejectOldVersion"

The rejection identified protocol version as the blocker. The server did not respond with a Steam authentication gate; the protocol version was explicitly too old. Full details in `protocol/source/spec.md#Measured evidence`.

### Round 2 — Protocol Version 18 (2026-07-25)

**Action:** Updated `ProtocolSource1` constant from 17 → 18 based on ceifa/garrysmod:debian's explicit "ServerRejectOldVersion" response.

**Improved diagnostics (2026-07-25):**
- The probe now decodes the response type byte and logs it as `0x%02x` instead of requiring manual hex parsing.
- Rejection messages are logged clearly: "response type 0x%02x — REJECTED: <message>" for immediate visibility.
- Example improved log output:
  ```
  connect-probe: response type 0x39 — REJECTED: game#GameUI_ServerRejectOldVersion
  ```
- Raw hex dump is still logged for deep diagnosis, but bounded to prevent log flooding.

**Next expectation (pending CI run):** The next CI run will attempt protocol 18. Possible outcomes:
1. **ACCEPTED:** The diagnostic will log "response type 0x03 — ACCEPTED (new client)"; depth escalates to JOINED.
2. **Rejected with different reason:** Diagnostic clearly shows the rejection reason (e.g., Steam auth gate); depth is PARTIAL.
3. **Rejected with version mismatch again:** Diagnostic shows the server's next-best protocol version; we iterate.

## Key invariants

1. **A2S is always attempted first.** Establishing QUERY depth (server responds to A2S) is a prerequisite. A2S failure means the server is not even answering queries and the probe fails.

2. **Source protocol provides measured evidence.** After A2S succeeds, the probe attempts source challenge/connect to gather evidence on which the next iteration will be based:
   - The challenge exchange works (CONFIRMED, PR #197).
   - The server parses our connect request (CONFIRMED, PR #197).
   - Protocol version is the measured blocker (CONFIRMED, PR #197).
   - The returned depth remains QUERY until the connect gate is passed.
   - Errors are logged, not propagated; the probe does not fail on source diagnostics.

3. **sv_lan 1 disables Steam auth.** The template environment includes `ARGS: "+sv_lan 1"`, which disables Steam GSLT requirement in srcds. This is necessary for unauthenticated joins in CI. Without it, even with a correct connect format, the server would reject on an auth gate.

4. **AUTOUPDATE=0 prevents network I/O on boot.** The ceifa/garrysmod image defaults `AUTOUPDATE=on`, which runs `app_update 4020 validate` on every startup. Setting `AUTOUPDATE=0` boots from the pre-baked image, avoiding wall-clock and network flakiness.

5. **Port 27015 is UDP for game and A2S, TCP for RCON.** A2S queries operate exclusively over UDP 27015. RCON and certain status checks use TCP 27015. The probe queries A2S over UDP (per the A2S specification) but the readiness probe on the GameServer uses TCP 27015 (a conventional liveness check for query services).

6. **Raw bytes logging is critical for evidence.** The connect response bytes are logged hex-encoded. The CI job log is the only window into what the real server sends; these bytes settle ambiguous outcomes and guide format corrections.

7. **Depth is measured, not guessed.** The ExpectDepth in the test is a *result* of what the server actually does. It is not a target or hope. QUERY is what A2S proves; source diagnostics gather evidence for future escalation.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type
- `protocol/a2s/` — A2S query protocol family
- `protocol/source/` — Source engine connection protocol family

**External:**
- Stdlib only: `context`, `encoding/hex`, `errors`, `fmt`, `log`, `time`.

No external modules.

## Security considerations

1. **No credentials in argv.** The bot name "gameplane-e2e-bot" is hardcoded; no passwords or tokens are passed.

2. **sv_lan 1 disables Steam auth.** This is intentional for CI (unauthenticated joins allowed). In production, GSLT (Game Server Login Token) would be required to authenticate the server to Valve's servers and allow the server to appear in public server lists. The e2e template omits GSLT, relying solely on sv_lan for LAN-mode testing.

3. **No mod/addon installation.** The probe does not install mods or interact with Workshop. The shipped template documents Workshop addons via the ARGS field but the probe does not use it.

4. **Protocol is read-only.** The probe sends challenge requests and connect attempts but does not send commands, chat, or any state modifications.

## Testing & coverage

**No unit tests for the probe itself.** Garry's Mod probe testing relies entirely on the e2e test (`test/e2e/garrysmod_bot_e2e_test.go`) running against a real cluster and server. The shared `a2s` and `source` protocol families have their own unit test coverage in their respective packages (`test/e2e/internal/protocol/a2sproto/protocol_test.go`, `test/e2e/internal/protocol/sourceproto/protocol_test.go`).

## Runtime characteristics

### Image Pin

- **Image:** `ceifa/garrysmod:debian` (explicit version tag, not floating :latest).
- **Rationale:** Floating tags (especially `:latest`) have drifted and broken tests in the past (Terraria example documented in the shared specs). This image is pinned to the `debian` production variant recommended by the ceifa/garrysmod repo itself.

### Boot Time and Disk

**Configured budgets:**
- **Ready timeout budget:** 10 minutes (configured in `garrysmod_bot_e2e_test.go`). First boot requires pulling the image from Docker registry.
- **Storage size:** 2Gi (allocated in PVC; actual usage depends on server save data, typically small).
- **Probe deadline:** 4 minutes (configured in `garrysmod_bot_e2e_test.go`; how long the in-cluster probe retries A2S and source queries before timing out).
- **CPU/memory requests:** 500m CPU, 1Gi memory; limits 2 CPU, 2Gi memory.

**Measured in CI (PR #197, job `e2e game bot`):**
- **Boot + probe time:** 134.82 seconds (one observation, not an average). This is the sum of pod ready time (server boot) plus the probe's retry loop (challenge + connect attempts until success or deadline).

These budgets are conservative starting points; if actual performance diverges significantly on repeated runs, the budgets will be adjusted.

### Readiness Probe

The GameServer template includes a tcpSocket probe on port 27015/TCP. This is a conventional liveness check for Source engine query services (RCON, status commands). The actual game traffic uses UDP 27015, and A2S queries also use UDP 27015. A successful TCP connection indicates the server has bound the query port and is ready, which is a good readiness signal.

- **initialDelaySeconds:** 30 (gives the server time to initialize before the first probe attempt).
- **periodSeconds:** 10 (check every 10 seconds).
- **failureThreshold:** 30 (require 30 consecutive failures before marking unhealthy; gives bootstrap operations time to complete).

### Console and RCON

The shipped template declares `rcon: protocol: none`, so there is no RCON console for this game. The ceifa/garrysmod image only exposes RCON via `+rcon_password` launch token (no environment variable), and mounting a ConfigFile to `/home/gmod/server/garrysmod/cfg/server.cfg` is impossible (that path is baked into the image, not persisted). Therefore, there is no way to deliver an RCON password to the running server under this mount, and RCON is disabled entirely.

This is not a regression; it is documented in the module comments and is the correct assessment of this specific image.

### Path A: Control Channel

**Status:** No control channel available.

Path A (driving the server through Gameplane) requires a control surface: either RCON or stdin/PTY. Garry's Mod has neither in this module:

- No RCON (protocol: none, and no way to pass RCON password to the image, as noted above).
- No PTY console (consoleMode is not set; the operator does not allocate a TTY for the game container).

This is documented as a genuine product gap — not a blocker for v1 (Path B proves the server works), but a future enhancement if a user brings a Garry's Mod image that supports RCON or desires console control via a configured RCON password. For CI, Path A is skipped; the test runs Path B only.

### Fast Set

Garry's Mod is included in the **fast set** of bot tests (runs by default without `GAMEPLANE_E2E_GAMES=all`). The fast set includes Minecraft Java, Terraria, Factorio, and Garry's Mod — four games covering protocol variety (Java/TCP, .NET/TCP, Lua/UDP, Source/UDP) and depth tiers (three at JOINED, one at QUERY). Garry's Mod exercises the Source engine and UDP protocols, which are shared with CS2 (heavy set).

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_GarrysModBot_Query` and expects `ExpectDepth: "QUERY"`. The depth is established by A2S_INFO succeeding:

- A2S succeeds: proves QUERY depth, server is alive and responding to queries.
- Source protocol connection: measured evidence gathering (PR #197, 2026-07-24). The challenge exchange works; the server parses our connect request and rejects on protocol version.

**Connect measurement (PR #197 & 2026-07-25):**
The probe attempts a source-protocol challenge and connect handshake after A2S succeeds. This attempt:
- Does not change the returned depth (stays QUERY until connect succeeds).
- Logs all evidence to the CI job log with **improved readability (2026-07-25)**: response type decoded as `0x%02x`, rejection reasons displayed as "response type 0x%02x — REJECTED: <reason>" rather than requiring manual hex parsing.
- Wraps errors gracefully; network failures or protocol errors do not fail the probe.

**Evolution (PR #197 → 2026-07-25):**
- PR #197 measured protocol version 17 rejection ("GameUI_ServerRejectOldVersion").
- 2026-07-25 updated `ProtocolSource1` from 17 → 18 and improved diagnostic output to decode responses for immediate visibility.
- The depth stays QUERY until the version gate is passed, then can escalate to PARTIAL (auth gate) or JOINED (success) on the next CI run.

**Configuration details:**
- `sv_lan 1` is delivered via the `ARGS` environment variable, disabling Steam GSLT requirement.
- `AUTOUPDATE: "0"` is set to prevent SteamCMD `app_update` on boot, which would add network I/O and flakiness to CI.

## References

- **Probe application:** `test/e2e/internal/garrys-mod/app.go`
- **E2E test:** `test/e2e/garrysmod_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **A2S protocol family:** `test/e2e/internal/protocol/a2sproto/` (created by parallel agent)
- **Source protocol family:** `test/e2e/internal/protocol/sourceproto/` (created by parallel agent)
- **Shipped template:** `modules/garrys-mod/template.yaml`
- **ceifa/garrysmod Docker image:** https://github.com/ceifa/garrysmod-docker
- **Source engine A2S documentation:** https://developer.valvesoftware.com/wiki/Server_queries
