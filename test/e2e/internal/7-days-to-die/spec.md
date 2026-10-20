# 7 Days to Die — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_SevenDaysToDieBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Undocumented proprietary UDP protocol; Steam A2S query reachable but join handshake unknown
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=7-days-to-die make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/7-days-to-die`  
**Dependencies:** stdlib + shared protocol family `a2s` (Go 1.25+); tested against vinanrra/7dtd-server:v0.9.3 (UNMEASURED against a real server — see "Measured connectivity")

## Purpose

End-to-end test harness for 7 Days to Die, proving that a Gameplane-managed 7 Days to Die server is reachable and responsive on its advertised network ports. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`.
3. Attempts A2S (Source-compatible) query protocol on the documented query port, using up to half of whatever deadline remains.
4. Falls back to basic TCP connectivity on the declared primary game port if A2S fails, using the rest of the deadline — a genuine, falsifiable signal even without understanding 7DtD's join protocol.
5. Measures the connectivity depth (QUERY: server is reachable and responsive).
6. Logs diagnostic information about protocol support.

This is part of the game-bot test suite and demonstrates that server bootstrap and network exposure succeed end-to-end. However, the join protocol cannot be verified in CI because it requires credentials or undocumented protocol knowledge.

## Responsibilities

1. **A2S query on the documented query port:** Attempt A2S (Source engine query protocol) on UDP 26901 (game port + 1, per node-gamedig — see "Measured connectivity"), not the game port itself.
2. **TCP connectivity fallback:** If A2S fails, verify the server accepts a TCP connection on the declared primary game port (26900/TCP) to establish a real, falsifiable lower bound on responsiveness.
3. **Depth measurement:** Return QUERY (server answers A2S_INFO, or accepts a TCP connection on a declared port).
4. **Protocol evidence:** Log all protocol attempts and results (A2S success/failure, TCP connectivity).
5. **Retrying:** The probe retries because the server may still be initializing when the Job first runs. Each phase (A2S, then TCP) gets a genuinely separate share of the deadline, so neither can starve the other.

## Non-goals / boundaries

- Does not implement the join handshake; 7 Days to Die uses an undocumented custom protocol (possibly LiteNetLib, but unconfirmed).
- Does not authenticate or supply credentials; join requires player credentials (EOS, Discord, or account-based).
- Does not handle telnet console interaction (the shipped template declares `rcon.protocol: none` because the telnet password lives in serverconfig.xml under an unmounted path and cannot be wired).
- Does not attempt to reach the Play state or complete a game session.
- Does not attempt LiteNetLib protocol directly; if A2S fails and TCP is the only signal available, that remains a measurement gap for future work (the "sdtd" node-gamedig protocol is confirmed A2S — see below — so this gap is expected to be rare in practice).

## Directory & package layout

```
test/e2e/internal/7-days-to-die/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

The probe imports the shared `a2s` protocol family from `test/e2e/internal/protocol/a2sproto/`. No game-specific protocol subpackage; node-gamedig's "sdtd" game definition confirms 7 Days to Die's query layer is plain A2S (with an optional telnet enrichment this probe does not use), so no per-game wire-format work is needed.

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

(Defined in `test/e2e/internal/probe/probe.go`.)

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
- Calls `probe.Main()` with a closure that runs `probeSevenDaysToDay`.
- Exits 0 only if `probeSevenDaysToDay` returns the expected depth.

**`func probeSevenDaysToDay(ctx context.Context, addr string) (probe.Depth, error)`**
- Derives the query port address (host:26901 — game port + 1) from the game Service address.
- Retries `a2s.QueryInfo` on the query port, bounded to at most half of whatever deadline remains on `ctx` (so the TCP fallback below is guaranteed a genuine share of the budget rather than starting with an already-expired context).
- If A2S succeeds, logs server name, map, and player count; returns `probe.Query`.
- If A2S fails, retries TCP connectivity to the primary port (26900/TCP) using the rest of the ORIGINAL `ctx`'s remaining budget.
- Returns `probe.Query` if either A2S or TCP succeeds, or a fatal error wrapping both failures if neither does.

## Measured connectivity

**Status:** UNMEASURED against a real server. 7 Days to Die is in the heavy set (opt-in only, never runs in CI); this probe has never been run against vinanrra/7dtd-server:v0.9.3. Both code paths (A2S primary, TCP fallback) have been verified locally against throwaway servers, and against a dead address (must fail) — see "Local verification" below.

### Protocol research (2026-07-25)

**node-gamedig** — a widely used, actively maintained open-source game-server-query library (https://github.com/gamedig/node-gamedig) — documents 7 Days to Die's query support directly: its game definition (`lib/games.js`, `sdtd` entry) declares `port: 26900, port_query_offset: 1, protocol: 'sdtd'`. The `sdtd` protocol implementation (`protocols/sdtd.js`) is literally `class sdtd extends Valve`: it runs the exact same A2S_INFO/A2S_PLAYER wire format as every other Source-family game, then optionally layers a telnet enrichment step on top (player list via `listplayers`, world day/mods via `gettime`/`version`) — which this probe does not use, since the telnet password is not wired in this template.

**The query port is game port + 1 (26901), NOT the game port itself (26900).** An earlier version of this probe sent A2S_INFO to 26900 — the wrong port per the above, which could never succeed against a real server (the actual game protocol lives there, not the query layer). This version queries 26901.

**A latent control-flow bug is also fixed here:** the earlier version's TCP fallback was wrapped in its own `probe.Retry` call sharing the same parent context as the (wrong-port) A2S retry loop. Since `probe.Retry` consumes the entire remaining deadline retrying its closure before returning an error, the TCP fallback always started with an already-expired context and could never actually dial — dead code in practice, not just a wrong-port bug. This version gives A2S a bounded sub-context (half of whatever remains) so the TCP fallback, using the original context, is guaranteed real retry time.

**What this proves, if it passes against a real server:** either (a) the container answers a real Source-family query request (A2S path — the container is running the actual game binary and has bootstrapped networking), or (b) at minimum, the server accepts a TCP handshake on its declared primary port (TCP path — weaker, but still a real signal a live server grants, unlike an invented-packet UDP probe).
**What this does NOT prove:** that a player can actually join (the join protocol remains undocumented, possibly LiteNetLib) or that world/mod state is correct.

### Local verification (not a substitute for a real-server run)

Built with `GOWORK=off go build -o /tmp/p-7-days-to-die ./internal/7-days-to-die`:

- **Dead address** (`-addr 127.0.0.1:59999 -deadline 8s -expect-depth QUERY`): exits 1 — A2S gets `connection refused` on 26901, the TCP fallback then also gets `connection refused` on 59999, both causes reported.
- **Live listener, A2S path**: a throwaway local UDP server bound to 127.0.0.1:26901 that parses a real A2S_INFO request and replies with a spec-correct A2S_INFO response — exits 0 via the A2S path, logging the parsed fake server name/map/player count.
- **Live listener, TCP-fallback path**: a throwaway local TCP listener bound to 127.0.0.1:26900 with NO A2S responder on 26901 — the probe's A2S phase times out as expected, then the TCP fallback phase (using the remaining deadline budget) successfully connects and the probe exits 0, proving the deadline-split fix actually gives the TCP phase a fair chance to run.

## Key invariants

1. **The join protocol remains undocumented; the query protocol does not.** 7 Days to Die (Unity engine) does not publicly document its join wire format (the game may use LiteNetLib, unconfirmed). But node-gamedig documents the **query** layer directly (`protocol: 'sdtd'`, literally `class sdtd extends Valve`) — a real A2S implementation at game-port+1, confirmed, not speculative.

2. **The query port is game-port+1, not the game port.** node-gamedig's `port_query_offset: 1` means A2S lives on 26901/UDP, not 26900. This was a real bug in the earlier version (which queried A2S on 26900) — fixed here.

3. **TCP connectivity proves server responsiveness at minimum.** If A2S fails, TCP successfully connecting to 26900/TCP proves the server is listening and responsive — a real, declared-port TCP handshake, not an invented UDP packet requiring a reply. This establishes QUERY depth by a weaker but still falsifiable measure.

4. **Both phases get genuine retry budget.** A2S is bounded to at most half of whatever deadline remains (via a derived sub-context), guaranteeing the TCP fallback — which uses the original context — a real chance to run rather than inheriting an already-expired deadline. The earlier version had this bug: `probe.Retry`'s A2S loop consumed the entire deadline before the TCP fallback's own `probe.Retry` call ever got a live context.

5. **Telnet port (8081 TCP) is not probed.** The shipped template declares `rcon.protocol: none` because the telnet password lives in serverconfig.xml (an unmounted path) and cannot be wired at runtime. The telnet port cannot be authenticated in CI, so it is not a probe target.

6. **Depth is measured, not guessed.** QUERY is what A2S proves, or what TCP connectivity proves as a fallback. The join handshake's credential gate is not attempted, so depth does not escalate to PARTIAL or JOINED.

7. **Retrying is essential for slow boots.** 7 Days to Die includes SteamCMD downloads (game install ~17.5GB on first boot) and mod installation. The server may listen on TCP and UDP long before it finishes world initialization. Retrying allows the probe to tolerate server bootstrap delays.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type
- `protocol/a2s/` — A2S query protocol family (confirmed via node-gamedig's `sdtd` definition, which extends the same base Valve/A2S wire format)

**External:**
- Stdlib only: `context`, `fmt`, `log`, `net`, `time`.

No external modules.

## Security considerations

1. **No credentials in argv.** The probe sends only A2S queries (standard Source protocol) or TCP connection attempts; no passwords, tokens, or authentication is attempted.

2. **No attempt to authenticate or modify server state.** The probe is read-only; it dials and reads but does not send commands or game-protocol packets.

3. **TCP diagnostics only.** The probe establishes TCP connections and reads the socket deadline, but does not send game-specific packets or payloads (when TCP falls back kicks in).

## Testing & coverage

**No unit tests for the probe itself.** 7 Days to Die probe testing relies entirely on the e2e test (`test/e2e/sevendaystodie_bot_e2e_test.go`) running against a real cluster and server. The shared `a2s` protocol family has its own unit test coverage in `test/e2e/internal/protocol/a2sproto/a2s_test.go`.

**Manual local verification** (not a substitute for CI/real-server coverage): see "Local verification" under "Measured connectivity" above.

## Runtime characteristics

### Image Pin

- **Image:** `vinanrra/7dtd-server:v0.9.3` (explicit version tag).
- **Rationale:** The image is pinned to a specific version for CI hermetic-ness. This version is verified to exist and is actively maintained by the vinanrra/Docker-7DaysToDie project.

### Boot Time and Disk

**Configured budgets:**
- **Ready timeout budget:** 25 minutes (configured in `sevendaystodie_bot_e2e_test.go`). First boot requires pulling the image (~5GB) and SteamCMD download (~17.5GB for the game install), so a long boot is expected.
- **Storage size:** 10Gi + 45Gi (two PVCs: 10Gi for world saves at `/home/sdtdserver/.local/share/7DaysToDie`, 45Gi for the game install at `/home/sdtdserver/serverfiles`). See the template's header comment for why two separate mounts are needed.
- **Probe deadline:** 6 minutes (configured in `sevendaystodie_bot_e2e_test.go`; how long the in-cluster probe retries A2S and TCP connectivity before timing out).
- **CPU/memory requests:** 2 CPU, 6Gi memory; limits 4 CPU, 12Gi memory.

**Measurements:** None yet (under development).

These budgets are conservative starting points informed by the template's resource declarations. Actual performance will be measured and recorded after first CI run.

### Readiness Probe

The GameTemplate shipped in `modules/7-days-to-die/template.yaml` deliberately declares **no readiness probe**. The image's entrypoint (`user.sh`) traps SIGTERM and performs a graceful save+shutdown (LinuxGSM's `sdtdserver stop`). The server is considered ready once the container is started; the e2e test waits for `status.phase == Running` before running the probe Job.

### Console and RCON

The shipped template declares `rcon.protocol: none`. This is not a regression but a documented architectural constraint:

- The telnet console port (8081 TCP) is exposed but has no settable password.
- The password lives in `serverconfig.xml`, which is located under `/home/sdtdserver/serverfiles/` (the 45Gi storage mount).
- The Gameplane template's single data mount is `/home/sdtdserver/.local/share/7DaysToDie` (the world saves), not the parent directory (which would shadow the ENTRYPOINT script).
- Therefore, the probe cannot wire the telnet password at pod start, and RCON is disabled.

See the template's README for the consequences and design rationale.

### Mods and ConfigSchema

The template provides `configSchema` for:
- **UNDEAD_LEGACY** and **DARKNESS_FALLS**: Overhaul mods (mutually exclusive).
- **ALLOC_FIXES**: Server-side fixes/tools mod.
- **MODS_URLS**: Comma-separated list of direct .zip/.rar mod URLs.

These are installed at first boot (or on every restart, depending on the image's behavior). The probe does not verify mod installation; it only verifies that the server is reachable and responsive.

### Heavy Set

7 Days to Die is deliberately in the **heavy set** of bot tests (not in the default `make test-e2e` run). It is enabled only with `GAMEPLANE_E2E_GAMES=all` and `GAMEPLANE_E2E_REUSE_CLUSTER=1`. The heavy set never runs in CI.

**Why:** 7 Days to Die requires 55Gi total storage (10Gi world saves + 45Gi game install). GitHub-hosted runners have ~14GB usable disk after the OS and Docker overhead. This game, combined with others in the heavy set, would exhaust node disk and cause concurrent test failures or timeouts.

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_SevenDaysToDieBot_Query` and expects `ExpectDepth: "QUERY"` (test name unchanged by this fix). The depth is established by either:

- A2S query on 26901/UDP succeeding (proves QUERY depth, server answers a real Source-compatible query).
- TCP connectivity to 26900/TCP succeeding (proves QUERY depth at minimum, server accepts a real TCP handshake on its declared primary port).

**What changed from the previous version:** two bugs are fixed here. First, A2S was queried on the wrong port (26900, the game port, instead of 26901, the query port — per node-gamedig's `port_query_offset: 1`), so it could never have succeeded against a real server. Second, the TCP fallback was starved of retry budget by a control-flow bug (the A2S retry loop consumed the entire deadline before the TCP fallback's own retry loop ever ran with time left) — fixed by bounding A2S to at most half of whatever deadline remains, guaranteeing the TCP phase genuine retry time.

**A2S query (confirmed, not speculative):**
node-gamedig documents 7 Days to Die's query port directly (`protocol: 'sdtd'`, extending the base A2S/Valve implementation). The probe now targets the correct port (26901).

**TCP fallback:**
If A2S fails, TCP connectivity to 26900/TCP serves as a fallback, now with a genuine chance to run. Successful TCP connection proves the server is listening and accepts real TCP handshakes on its declared primary port — a falsifiable signal, unlike an invented-packet UDP probe.

**LiteNetLib investigation (future):**
The specs document notes that LiteNetLib (Unity's default transport) is a candidate for 7 Days to Die's join-protocol network layer (separate from the now-confirmed A2S query layer). If/when LiteNetLib support is documented or reverse-engineered, a follow-up can implement the connect-request wire format and escalate toward PARTIAL/JOINED.

**Measured on:** Not yet — UNMEASURED against a real server (heavy set, opt-in only). Verified locally only (see "Local verification" under "Measured connectivity").

**Credential gate (future escalation):**
If either A2S or a LiteNetLib implementation eventually allow protocol handshakes to complete, the next blocker will likely be credentials (Discord, EOS, or account-based authentication). That would appear as a PARTIAL depth (protocol handshake succeeds, but login is rejected). Escalation to JOINED would require valid test credentials, which is out of scope for the current iteration.

## References

- **Probe application:** `test/e2e/internal/7-days-to-die/app.go`
- **E2E test:** `test/e2e/sevendaystodie_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **A2S protocol family:** `test/e2e/internal/protocol/a2sproto/`
- **Shipped template:** `modules/7-days-to-die/template.yaml`
- **vinanrra/7dtd-server Docker image:** https://github.com/vinanrra/Docker-7DaysToDie
- **LinuxGSM (game server management):** https://linuxgsm.com/
- **LiteNetLib (Unity networking):** https://github.com/RevenantX/LiteNetLib (candidate join-protocol transport, unconfirmed)
- **node-gamedig (query protocol reference):** https://github.com/gamedig/node-gamedig — `lib/games.js` "sdtd" entry (`port_query_offset: 1, protocol: 'sdtd'`); `protocols/sdtd.js` (`class sdtd extends Valve`)
- **Source engine A2S documentation:** https://developer.valvesoftware.com/wiki/Server_queries
