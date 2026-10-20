# cs2 — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_CS2Bot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Source 2 connect packet reportedly carries embedded protobuf and Steam auth ticket; incomplete public documentation
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=cs2 make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0); probe depth measured: **QUERY** (A2S query success)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/cs2`  
**Dependencies:** stdlib only (Go 1.25+); tested against CS2 Image 4.0.1 (joedwards32/cs2); image pin required (floating tag drifts)  
**Heavy game (never runs in CI):** See "Why CS2 is heavy and never in CI" section below

## Purpose

End-to-end test harness for Counter-Strike 2, proving that a Gameplane-managed CS2 server is not merely "Running" in Kubernetes but genuinely reachable and speaking its real game protocol. The harness:

1. Implements the Source engine query protocol client (A2S — `protocol/a2s`) for CS2 server discovery and status.
2. Implements the Source engine connection protocol (`protocol/source`) for challenge/connect handshake.
3. Provides a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
4. Connects to the game server via the cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path.
5. Performs an A2S query to verify the server answers protocol requests and reports status.
6. Attempts a Source protocol Challenge and Connect to confirm the server accepts real client connections.

This is part of the heavy-game test suite — deliberately never run in CI — and measures the exact protocol depth CS2 reaches.

## Responsibilities

1. **A2S protocol client:** Query the server for name, map, player count via the Source A2S query protocol (UDP).
2. **Source protocol client:** Perform Challenge and Connect handshake to test login-phase accessibility.
3. **Connection lifecycle:** Dial the server, exchange Challenge packets, parse Connect response.
4. **Status probing:** Extract version, server name, map, and player counts from A2S response.
5. **Credential gating detection:** Identify where a GSLT (Game Server Login Token) gate occurs if present; report PARTIAL depth if Connect is rejected.
6. **Retrying:** The probe retries both A2S query and Source handshake because the server may be bootstrapping — it answers queries while loading maps but may not accept clients immediately.
7. **Exit signaling:** Report the join depth reached (JOINED if server accepts the connection, PARTIAL if it rejects it, QUERY if even A2S fails).

## Non-goals / boundaries

- Does not attempt to log into the game or reach the Play state.
- Does not supply a valid GSLT token; if the server demands one, that is a PARTIAL gate (known and documented).
- Does not implement Metamod or CounterStrikeSharp mod configuration.
- Does not perform compression or any protocol extensions beyond the base Challenge/Connect flow.
- Does not verify map availability or game settings.

## Directory & package layout

```
test/e2e/internal/cs2/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file

test/e2e/internal/protocol/
├── a2s/
│   ├── a2s.go                  # A2S QueryInfo implementation (concurrent agent)
│   └── a2s_test.go             # Unit tests
├── source/
│   ├── source.go               # Source Challenge and Connect implementation (concurrent agent)
│   └── source_test.go          # Unit tests
└── [more families...]
```

- **`protocol/a2s`** — shared package implementing the A2S query protocol (used by CS2, garrys-mod, and other Source-family games).
- **`protocol/source`** — shared package implementing the Source connection protocol (Challenge + Connect).
- **`app.go`** — the main function that runs as a Kubernetes Job. Imports the protocol packages and uses the shared `probe` package for retry logic and test framework integration.

## External interface / contracts

### Protocol Packages (created by concurrent agents; contract assumed)

**`a2s.QueryInfo(ctx context.Context, addr string) (*a2s.Info, error)`**
- Sends an A2S query to the server and retrieves server info.
- Returns `Info` containing: Name, Players, MaxPlayers, Map.
- Enforces a context deadline; returns immediately if the server does not answer.

**`source.Challenge(ctx context.Context, addr string) (uint32, error)`**
- Sends a Source `get_challenge` packet and receives the server's challenge token.
- Returns the 32-bit challenge value.
- Enforces a context deadline.

**`source.ConnectResult`** — connection response:
```go
type ConnectResult struct {
    Accepted  bool   // true if the server accepted the connection
    RejectMsg string // reason if rejected
    Raw       []byte // raw response packet bytes (hex-logged for debugging)
}
```

**`source.Connect(ctx context.Context, addr string, challenge uint32, name string, protocol uint32) (*ConnectResult, error)`**
- Sends a Source `connect` packet with the challenge, player name, and protocol version.
- Returns whether the server accepted the connection.
- If rejected, includes the reject reason and raw bytes.
- Enforces a context deadline.

### App (`app.go`)

**`func main()`**
- Calls `probe.ParseFlags()` to register and parse shared flags (`-addr`, `-deadline`, `-expect-depth`).
- Calls `probe.Main()` with a closure that runs `probeCS2`.
- Exits 0 only if `probeCS2` returns the expected depth.

**`func probeCS2(ctx context.Context, addr string) (probe.Depth, error)`**
- Retries A2S QueryInfo until successful or deadline expires (reports error on persistent failure).
- Logs the server's name, map, and player counts.
- Returns `probe.Query` (depth is determined by A2S success alone).
- Runs `connectProbe()` diagnostically (after returning): attempts Source Challenge and Connect, logs all outcomes richly with hex-encoded response bytes (bounded), but never changes the returned depth.

**`func connectProbe(ctx context.Context, addr string)`**
- Attempts Source Challenge; logs success or error with greppable prefix `connect-probe:`.
- Attempts Source Connect with challenge token and player name "gameplane-e2e-bot"; logs acceptance or rejection.
- Logs raw response bytes (hex-encoded, bounded to ~256 chars) and any reject message.
- Never fails the probe or changes the return value; purely diagnostic.

## Key invariants

1. **Depth is QUERY.** A2S query success is the only criterion; A2S is verified against Valve's spec. The Source protocol handshake (Challenge + Connect) is attempted diagnostically *after* returning QUERY but is **unverified** — the exact packet format may diverge on CS2 (Source 2 engine) from earlier implementations — so it determines no depth and never fails the probe. If Source Connect were to be asserted as a depth gate, that would require a verified-correct implementation; today, we retain the attempt only for evidence: does the server speak Source protocol? Where does it fail?

2. **Heavy game — never in CI.** CS2 declares 60Gi storage on the shipped module and pulls multi-GB SteamCMD content on first boot. This test is run only by maintainer hand-run with `GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAMES=all make test-e2e-keep` against a pre-existing cluster (e.g., `kubelab`). No CI job ever runs this test. The test is runnable only on a cluster with at least 60Gb of free disk.

3. **SRCDS_TOKEN placeholder:** The module's `configSchema` marks `SRCDS_TOKEN` as `required: true` (type: password, no default). The e2e GameServer spec supplies a placeholder token (e.g., `"test-gslt-placeholder"`) so the CR reconciles without error. Note: a placeholder token is **not** a valid Valve GSLT; real player connections would be rejected at the GSLT gate (PARTIAL depth), but that is not tested here.

4. **Image pin required.** The template references `joedwards32/cs2:latest` as the floating tag and pins specific versions (4.0.1, 4.0.0, etc.). The e2e test MUST pin an explicit version tag (currently 4.0.1) because the default image behavior may change across releases, affecting protocol responses.

5. **Readiness probe on UDP:** CS2 boots with SteamCMD downloads and map initialization. The module's template should include a readiness probe if not already present. Since the game port is UDP, the probe cannot use a simple tcpSocket check. The e2e test uses the probe Job itself as the readiness gate: the pod must be Running before the probe Job starts.

6. **Source protocol is unverified.** Counter-Strike 2 is built on Source 2 engine, which evolved from Source 1 (Garry's Mod). The exact Challenge/Connect packet format may differ between Source 1 and Source 2. The Source protocol implementation in `test/e2e/internal/protocol/sourceproto/` is best-effort; its correct behavior is not guaranteed. The diagnostic connect attempt allows observation of the divergence, but no depth decision is based on it.

## Dependencies

**Internal:** 
- `github.com/GameplanePanel/gameplane/test/e2e/internal/probe` (shared probe harness)
- `github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/a2sproto` (concurrent agent)
- `github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/sourceproto` (concurrent agent)

**External:** stdlib only (`context`, `encoding/hex`, `errors`, `fmt`, `log`, `time`).

No third-party Go modules. The probe image builds with `GOWORK=off` against `test/e2e/go.mod` alone.

## Security considerations

1. **GSLT is a credential:** The placeholder token is not a real credential (Valve's servers would reject it), so no secrets leak. In production use, the SRCDS_TOKEN is stored as a Kubernetes Secret and injected via SecretKeyRef, never inline in the GameServer CR.

2. **No credentials in logs:** The probe does not log the player name at connection time (only on success). Placeholder token is not logged.

3. **Unprivileged probe Job:** The probe runs with `runAsNonRoot: true`, `readOnlyRootFilesystem: true`, `capabilities.drop: ["ALL"]`, `allowPrivilegeEscalation: false`, `seccompProfile: RuntimeDefault`.

4. **Connection data is observational:** The probe parses server responses without mutating server state. Raw response bytes are logged hex-encoded for debugging (bounded to 256 chars).

## Testing & coverage

### Protocol packages

**`internal/protocol/a2sproto/`** and **`internal/protocol/sourceproto/`** (concurrent agents)

Each carries untagged unit tests that run on every `make test-go`:

- **`TestQueryInfo`** — exercises A2S query against pre-recorded server responses or a mock.
- **`TestChallenge`** — validates challenge packet parsing.
- **`TestConnect`** — validates connect response parsing, including accepted/rejected cases.

### E2E test registration

The CS2 bot test is registered in `test/e2e/buckets.sh` in the **heavy bucket** (`bot-heavy`):
- Test name: `TestGameServer_CS2Bot_Query`
- **NEVER runs in CI** — only on `GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=… GAMEPLANE_E2E_GAMES=all make test-e2e-keep`
- **Requires:** cluster with ≥60Gb free disk (vendor minimum for SteamCMD install on first boot)

### Configured budgets (declared vendor/module requirements, not measured performance)

**Vendor-declared budgets (joedwards32/cs2 image):**
- **Storage:** ≥60Gb required (multi-GB SteamCMD download and game assets, pulled on first boot into the PVC).
- **Image:** actively maintained upstream; version 4.0.1 verified on this test.
- **Network:** multi-GB SteamCMD content download (throughput-dependent; 10–30 minutes estimated).
- **Resources:** requests=1 CPU / 2Gi RAM, limits=4 CPU / 4Gi RAM (from shipped module template).

**E2E test spec (matches vendor minimum):**
- **Image pin:** joedwards32/cs2:4.0.1@sha256:34c093d24a23751fb409ae41dffe88884fb36964b365fcd25a31ec45e88ad8cf (explicit version, no floating tag).
- **Storage:** 60Gi (vendor-declared minimum; test runs only on clusters with this much free disk).
- **Ready timeout:** 10 minutes (SteamCMD + server startup upper bound).
- **Probe deadline:** 4 minutes (in-cluster A2S query attempt, retried within deadline before failing).

### Measurements (not yet observed by this test)

- **Actual boot time:** Unknown (not yet measured; awaiting first maintainer hand-run).
- **Actual disk usage:** Unknown (vendor declares ≥60Gb required; actual measured usage TBD).
- **A2S response turnaround:** Unknown (awaiting first run).
- **Source protocol behavior:** Unknown; attempted diagnostically only (see invariant 1).

## Why CS2 is heavy and never in CI

1. **Disk:** The vendor declares ≥60Gb required (multi-GB SteamCMD pull). GitHub-hosted runners have ~14Gb usable disk total after OS/Docker overhead. A 60Gi PVC immediately exhausts a single node.

2. **Network:** SteamCMD content is large and throughput-sensitive. On shared GitHub runners, a 10+ minute download blocks the job. Multi-GB downloads can fail partway through and require retries.

3. **Duration:** Boot + SteamCMD + map load = 10–30 minutes. Adding one CS2 job would consume a third of the entire e2e job's timeout budget, blocking other tests and making CI unreliable.

4. **Standing decision:** This is NOT temporary. CS2's A2S and Source protocol packages still ship in `test/e2e/internal/protocol/` and their unit tests run in `make test-go` on every PR. Only the real-image boot test is maintainer-initiated via hand-run:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAMES=all make test-e2e-keep
```

No CI job ever sets `GAMEPLANE_E2E_GAMES=all` or runs the `bot-heavy` bucket.

## References

- **Protocol implementations:** (concurrent agents create)
  - `test/e2e/internal/protocol/a2sproto/a2s.go` — Source A2S query
  - `test/e2e/internal/protocol/sourceproto/source.go` — Source Challenge/Connect
- **Probe application:** `test/e2e/internal/cs2/app.go`
- **E2E test:** `test/e2e/cs2_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/` (shared across all games)
- **Shipped module:** `modules/cs2/template.yaml` — GameTemplate for CS2
- **Buckets:** `test/e2e/buckets.sh` — e2e test bucketing, `bot-heavy` definition
- **CI:** `.github/workflows/ci.yaml` — `e2e-game-bot` job (runs `bot-fast` only)
- **Source engine query protocol:** https://developer.valvesoftware.com/wiki/Server_queries
- **Counter-Strike 2 server:** https://github.com/joedwards32/CS2 (joedwards32/cs2 Docker image)
