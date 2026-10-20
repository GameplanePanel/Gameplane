# dont-starve-together — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_DontStarveTogetherBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Klei reliable-UDP frame format and handshake not documented; server credentials required at join
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=dont-starve-together make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/dont-starve-together`  
**Dependencies:** stdlib + shared `protocol/a2s` (Go 1.25+); tested against jamesits/dst-server:vanilla (query-port assertion UNMEASURED against a real server — see "Measured connectivity")

## Purpose

End-to-end test harness for Don't Starve Together, proving that a Gameplane-managed DST server answers a real Valve A2S (Source Engine Query) query on its Steam GameServer query port. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path.
3. Sends an A2S_INFO request to the Steam query port (UDP 27016) via the shared `protocol/a2s` package and requires a well-formed A2S_INFO response.
4. On failure, sends one bounded, non-gating raw-byte diagnostic probe at the game port (10999) and logs whatever came back (or explicitly logs silence).

This is part of the game-bot test suite (`test/e2e/dontstarve_bot_e2e_test.go`) and demonstrates that the DST server boots and answers the real Steam query protocol.

## Responsibilities

1. **A2S_INFO query:** Send a real A2S_INFO request (via the shared `protocol/a2s` package) to the Steam query port (UDP 27016) and parse the response.
2. **Depth measurement:** Return QUERY if the query port answers A2S_INFO.
3. **Diagnostic logging:** On failure, send one bounded, non-gating raw-byte probe at the game port and log the response (or explicitly log that nothing came back).
4. **Retrying:** The probe retries because the server may be bootstrapping — it may start listening after a delay.

## Non-goals / boundaries

- Does not implement the DST game-port (ENet) wire protocol. That protocol is not publicly documented and reverse-engineering it is out of scope; A2S on the Steam query port is used instead, since it is documented.
- Does not attempt a join or authentication. No player name, cluster token, or Klei credentials are used.
- Does not handle credential gates or authentication flows.
- Does not implement A2S_PLAYER or A2S_RULES; only A2S_INFO is queried.
- Does not fall back to any other port if the Steam query port is unreachable: DST declares no TCP port at all in this template, so there is no TCP fallback available (unlike, e.g., 7 Days to Die or Project Zomboid).

## Directory & package layout

```
test/e2e/internal/dont-starve-together/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

No DST-specific protocol subpackage; the probe imports the shared `protocol/a2s` package used by several other games in this bucket.

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

(Defined in `test/e2e/internal/probe/probe.go`; app.go is coded against this contract.)

```go
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
- Calls `probe.Main()` with a closure that runs `probeDontStarveTogether`.
- Exits 0 only if `probeDontStarveTogether` returns the expected depth.

**`func probeDontStarveTogether(ctx context.Context, addr string) (probe.Depth, error)`**
- Derives the Steam query port address (host:27016) from the game Service address.
- Retries `a2s.QueryInfo` on the query port until it succeeds or the deadline expires.
- On success, logs the parsed server name/map/player count and returns `probe.Query`.
- On failure, sends one bounded, non-gating raw-byte diagnostic at the game port (10999, its own short independent timeout) and returns a fatal error.

**`func logRawDiagnostic(ctx context.Context, addr string)`**
- Sends a single non-protocol byte and logs the raw response (hex, bounded) or explicitly logs that nothing came back within the diagnostic window.
- Purely diagnostic: never affects the probe's pass/fail outcome.

## Key invariants

1. **The join protocol (ENet, game port) is undocumented; the query protocol (A2S, Steam query port) is not.** Klei Entertainment does not publish the DST game-port wire-protocol specification, and reverse-engineering a join is out of scope. But DST is a Steamworks GameServer title (Steam AppID 322330), and its Steamworks GameServer integration answers Valve's A2S protocol on a separate query port — documented by node-gamedig (see "Measured connectivity" below) and independent of the game protocol.

2. **The assertion is falsifiable.** A2S_INFO is a real, documented request that a correctly functioning server answers. This replaces an earlier version that sent an invented single byte to the game port and required any reply — not falsifiable, since ENet (DST's game-port transport) does not answer arbitrary unrecognized datagrams, the same failure mode proven empirically against Factorio's query port.

3. **No credentials in CI.** The template declares `DST_CLUSTER_TOKEN` as optional config, but CI does not set it. The jamesits/dst-server image boots offline without a token.

4. **QUERY is the measured depth.** A2S_INFO answering proves the server is alive and speaking a real protocol; it does not prove a player can join, which would require the (still undocumented) ENet join handshake.

5. **The Steam query port (27016) is UNVERIFIED against this specific image.** Unlike the other four games in this bucket, port 27016 is not mentioned in jamesits/dst-server-docker's own documentation (which calls out only 10999/11000). Whether this specific image's entrypoint binds the Steamworks GameServer query port without a Steam cluster token has not been confirmed against a real server — see the CAVEAT in "Measured connectivity" below.

6. **Caves port and other Steam ports are not probed.** Port 11000 is the caves shard; ports 12345/12346/12347 are other Steam integration ports (auth, master-server-shard for secondary shards, etc.). Only the game port (10999, diagnostic-only) and the Steam query port (27016, the actual assertion) are used.

## Measured connectivity

**Status:** UNMEASURED against a real server. DST is in the heavy set (opt-in only, never runs in CI); this probe has never been run against jamesits/dst-server:vanilla. The A2S implementation has been verified locally against a throwaway UDP server speaking real A2S_INFO, and against a dead address (must fail) — see "Local verification" below. Neither is the same as a real DST server.

**Protocol research findings (2026-07-25):**
- **node-gamedig** documents DST's query port: its game definition (`lib/games.js`, `dst` entry) declares `port: 10999, port_query: 27016, protocol: 'valve'` — plain A2S, no per-game override.
- Community documentation of DST's `cluster.ini` `[STEAM]` section corroborates the port number: a DST master shard's `master_server_port` defaults to **27016** (secondary/Caves shards use a different value, e.g. 12346).
- A four-year-old node-gamedig issue (#276, filed 2022) has a maintainer comment stating "I don't believe DST has a query protocol at all" — but the game is now present in node-gamedig's current `GAMES_LIST.md` under "Valve Protocol", meaning support was added after that comment; the issue is stale evidence, not current.

**CAVEAT — the biggest open risk in this bucket:** unlike Enshrouded, Project Zomboid, V Rising, and 7 Days to Die, port 27016:
1. Is **not mentioned anywhere** in the jamesits/dst-server-docker image's own README (which tells operators to open only 10999 and 11000).
2. Was **not previously declared** in this template's `Ports` list — it has been added as part of this fix (see `dontstarve_bot_e2e_test.go`), since the probe dials the game's Kubernetes Service (not the pod IP directly), and only advertised ports get a Service port + NetworkPolicy ingress rule.
3. Depends on the DST server binary initializing its Steamworks GameServer object even when CI runs it offline (no `DST_CLUSTER_TOKEN`). This is standard Steamworks GameServer SDK behavior for A2S availability (query answering does not require successful Steam backend authentication), and node-gamedig's authors presumably verified it works in practice — but this project has not independently confirmed it against this specific image.

**If a real run shows the query port never responds:** do not weaken the assertion back to "any UDP reply." Instead: (a) confirm from the failing Job's pod logs whether A2S_INFO requests reached the server at all (check the game pod's own logs/network counters if available), (b) check whether `jamesits/dst-server` documents a way to force-enable the Steam query port offline, and (c) if it's confirmed the image genuinely never binds this port, downgrade this spec's Depth Expectation to state plainly that DST cannot currently be asserted past raw game-port reachability (which is itself not falsifiable per the analysis above) — do not fabricate a passing signal.

### Local verification (not a substitute for a real-server run)

Built with `GOWORK=off go build -o /tmp/p-dst ./internal/dont-starve-together`:

- **Dead address** (`-addr 127.0.0.1:59999 -deadline 8s -expect-depth QUERY`): exits 1 — every A2S attempt gets `connection refused`, the diagnostic probe explicitly logs no response.
- **Live listener**: a throwaway local UDP server bound to 127.0.0.1:27016 that parses a real A2S_INFO request and replies with a spec-correct A2S_INFO response — exits 0, logging the parsed fake server name/map/player count.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type
- `protocol/a2s` — A2S query protocol family

**External:**
- Stdlib only: `context`, `encoding/hex`, `fmt`, `log`, `net`, `time`.

No external modules.

## Security considerations

1. **UDP-only, no auth.** The probe does not authenticate. It relies on network isolation (the game pod's firewall ingress rules) to prevent unauthorized players. The probe is a technical test, not a security model.

2. **No player persistence.** The server receives the probe but never registers a player (no handshake beyond the port listening). No state is persisted.

3. **No credentials transmitted.** The probe never sends a Klei cluster token, Steam auth, or player credentials. The probe is an unauthenticated network test.

4. **Respects server silence.** If the server is firewalled or filters the probe, the probe retries and eventually times out. This is expected behavior; no escalation to a network attack occurs.

## Testing & coverage

**No unit tests for the probe itself.** DST probe testing relies entirely on the e2e test (`test/e2e/dontstarve_bot_e2e_test.go`) running against a real cluster and server. The shared `a2s` protocol family has its own unit test coverage in `test/e2e/internal/protocol/a2sproto/a2s_test.go`.

**Manual local verification** (not a substitute for CI/real-server coverage): see "Local verification" under "Measured connectivity" above.

## Runtime characteristics

### Image Pin

- **Image:** `jamesits/dst-server:vanilla` (explicit version tag).
- **Rationale:** Explicit tags prevent image drift. The `vanilla` variant ships the game client without mods, matching the minimal footprint required for testing. The `latest` tag (if available) could drift to a newer major version with different protocol, breaking the test.

### Boot Time and Disk

**Configured budgets (not measurements):**
- **Ready timeout:** 15 minutes (configured in `dontstarve_bot_e2e_test.go`; gives DST time to boot, generate/load worlds, and become ready).
- **Probe deadline:** 4 minutes (configured in `dontstarve_bot_e2e_test.go`; how long the in-cluster probe retries UDP probes before timing out).
- **Storage request:** 5Gi (configured in `modules/dont-starve-together/template.yaml`; persistent volume for `/data`).
- **CPU/memory requests:** 1 CPU, 1Gi memory; limits 2 CPU, 2Gi memory.

**Measurements:**
- **Actual boot + probe time:** *not yet measured* (this is the first probe run).
- **Actual disk usage:** *not yet measured*.

### Readiness Probe

The GameServer template includes a tcpSocket probe on port 10999/TCP (the game port), inherited unchanged from before this fix. **This is itself an unverified assumption** — DST's game port is documented as UDP (ENet-based), and nothing found during this fix's research confirms the server also accepts a bare TCP connection there. This readiness probe has never run against a real server either (heavy set, opt-in only). If a real run shows the pod never reaching Ready because of this probe, that is a separate, pre-existing issue from the query-port assertion fixed here — track it separately rather than conflating the two.

- **initialDelaySeconds:** 30 (gives DST time to bind the port and initialize).
- **periodSeconds:** 10 (check every 10 seconds).
- **failureThreshold:** 30 (require 30 consecutive failures before marking unhealthy; gives world generation time).

### Advertised ports

Ports 10999 (game), 11000 (caves), 12346/12347 (Steam auth/master) were already declared. **This fix adds `steamquery` (27016/UDP)**, required for the probe (which dials the game's Kubernetes Service, not the pod IP) to be able to reach the port at all — only advertised ports get a Service port and NetworkPolicy ingress rule from the operator.

### Console and Actions

The shipped template declares `rcon.protocol: none` and `consoleMode: pty`. DST supports Lua console commands via stdin (e.g., `c_save()`, `c_announce(msg)`) but does not support RCON. The template declares actions via stdin that the agent can deliver; the probe does not exercise these (Path A responsibility; this is Path B).

### Heavy Set (Never Runs in CI)

Don't Starve Together is included in the **heavy set** of bot tests (opt-in only, via `GAMEPLANE_E2E_GAMES=all`). It does not run in GitHub Actions CI. The reasoning:

- **Disk:** DST world generation and persistent storage can grow large; 5Gi is a conservative request but GitHub runners have limited storage.
- **Throughput:** First boot requires downloading the game server binary and world generation; timeouts are possible on slow CI infrastructure.
- **Shared resource contention:** Multiple heavy games running on a single CI runner cause OOM and I/O contention; running them serially is safer.

The command to run this test locally (against a persistent cluster like kubelab):

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 \
GAMEPLANE_E2E_CONTEXT=kubelab \
GAMEPLANE_E2E_GAME_BOT=1 \
GAMEPLANE_E2E_GAMES=dont-starve-together \
make test-e2e-keep
```

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_DontStarveTogetherBot_Query` and expects `ExpectDepth: "QUERY"` (test name unchanged by this fix).

The depth is established by A2S_INFO answering on the Steam query port (27016/UDP):
- **A2S_INFO answered:** proves the server is alive and speaking the real, documented Steamworks GameServer query protocol. Depth = QUERY.
- **Game-port join protocol not documented:** escalating to PARTIAL (protocol handshake) or JOINED (player acceptance) requires reverse-engineered ENet protocol specification or public documentation, which does not exist.

**What changed from the previous version:** the probe used to send a single invented byte to the game port (10999) and require any reply — not falsifiable, since ENet does not answer arbitrary datagrams (the same failure mode proven empirically against Factorio's query port). The A2S_INFO query on 27016 is used instead because node-gamedig documents it as the real protocol this port answers. **This is the least-verified fix in the bucket** (see CAVEAT under "Measured connectivity"): unlike the other four games, the query port isn't in the base image's own docs and this specific probe/port pairing has never run against a real server.

**If protocol documentation becomes available for the game port itself:**
Once Klei publishes the DST wire protocol or a trusted reverse-engineering surfaces, the test can escalate to PARTIAL (protocol handshake) or JOINED (player acceptance) by:
1. Implementing a proper handshake in the probe (with citations to the specification).
2. Updating this spec.md with protocol references.
3. Re-measuring against a real server.
4. Updating the test to expect the new depth.

**If the first real run shows 27016 never answers:** see the CAVEAT's remediation steps under "Measured connectivity" — do not revert to an invented-packet assertion.

## References

- **Probe application:** `test/e2e/internal/dont-starve-together/app.go`
- **E2E test:** `test/e2e/dontstarve_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **A2S protocol family:** `test/e2e/internal/protocol/a2sproto/`
- **Shipped template:** `modules/dont-starve-together/template.yaml`
- **jamesits/dst-server Docker image:** https://github.com/jamesits/dst-server-docker
- **Don't Starve Together:** https://www.dontstarve.com/ (no public game-port wire-protocol documentation)
- **Klei Entertainment:** https://www.klei.com/ (DST developer)
- **node-gamedig (query protocol reference):** https://github.com/gamedig/node-gamedig — `lib/games.js` "dst" entry (`port_query: 27016, protocol: 'valve'`)
- **DST cluster.ini [STEAM] section (community docs):** https://dontstarve.wiki.gg/wiki/Guides/Don%E2%80%99t_Starve_Together_Dedicated_Servers
- **Valve A2S documentation:** https://developer.valvesoftware.com/wiki/Server_queries
