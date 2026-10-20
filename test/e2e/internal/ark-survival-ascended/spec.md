# ARK: Survival Ascended — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_ArkBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Unreal Engine 5 network stack partially documented; custom protocol variant under reverse-engineering
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=ark-survival-ascended make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/ark-survival-ascended`  
**Dependencies:** stdlib only (Go 1.25+); tested against mschnitzer/asa-linux-server:1.5.1

## Purpose

End-to-end test harness for ARK: Survival Ascended, proving that a Gameplane-managed ARK server is reachable and has a TCP listener on the RCON port. The harness:

1. Implements a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the game server via cluster network (Service DNS) rather than `kubectl port-forward`.
3. Verifies the server is listening on the RCON port (TCP 27020) by attempting a TCP handshake.
4. Measures the connectivity depth (QUERY: a TCP listener is present and accepting connections).
5. Logs diagnostic information about connection success.

This is part of the game-bot test suite and demonstrates that server bootstrap and network exposure succeed end-to-end. The RCON port is the only port that can be verified without game-specific protocol knowledge or credentials. ARK's join protocol cannot be verified in CI because it requires EOS/Steam identity that CI environments cannot mint.

## Responsibilities

1. **TCP connectivity:** Dial the RCON port (27020 TCP) and verify the server is accepting connections.
2. **Handshake verification:** A successful TCP connect proves a listener exists on that port (TCP requires a real handshake).
3. **Depth measurement:** Return QUERY (RCON port is listening and accepting connections).
4. **Honest depth boundary:** ARK's join protocol requires EOS/Steam credentials; CI cannot proceed beyond QUERY. UDP probes alone are not falsifiable because UDP dial succeeds even on dead addresses.
5. **Retrying:** The probe retries because the server may be still initializing when the Job first runs.

## Non-goals / boundaries

- Does not implement the join handshake; ARK's UE5 protocol and credential requirements place this out of CI scope.
- Does not authenticate via EOS or Steam; such authentication is a credential gate.
- Does not handle RCON or console interaction; the probe only verifies the TCP port is listening, not that RCON is enabled.
- Does not attempt to reach the Play state or complete a game session.
- Does not probe the game port (UDP 7777) directly; UDP dial is not falsifiable (succeeds even when nothing is listening).
- Does not interpret or validate any protocol data; connection success is the only measurement.

## Directory & package layout

```
test/e2e/internal/ark-survival-ascended/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

The probe imports only the shared `probe` package from `test/e2e/internal/`. No game-specific protocol implementation; ARK has no standard query protocol (A2S, LiteNetLib, etc.) that CI can speak.

## External interface / contracts

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
- Calls `probe.Main()` with a closure that runs `probeARK`.
- Exits 0 only if `probeARK` returns the expected depth.

**`func probeARK(ctx context.Context, addr string) (probe.Depth, error)`**
- Retries UDP connectivity on the game port until successful or deadline expires.
- Sends a minimal probe packet (single 0x00 byte) and logs any response (hex-encoded).
- Returns `probe.Query` if the server is reachable, or an error if the server is not listening.

## Measured connectivity evidence

**Date:** Not yet measured (probe under development, awaiting first CI run)  
**Server:** mschnitzer/asa-linux-server:1.5.1  
**Ports probed:** TCP 27020 (RCON)  
**Other ports available:** UDP 7777 (game), UDP 7778 (peer) — not probed, UDP dial is not falsifiable

The probe will measure server responsiveness on first CI run. RCON port TCP connectivity (QUERY depth) is the honest measurement, since:
- ARK's join protocol requires EOS/Steam identity
- ARK's game port has no query mechanism  
- TCP handshake on RCON is the only falsifiable test without protocol knowledge

## Key invariants

1. **No query protocol available.** ARK: Survival Ascended has no standard query mechanism (A2S, LiteNetLib, etc.). The image's own documentation states plainly: "ASA does no longer offer a way to query the server." Protocol-based depth measurement is impossible.

2. **Join protocol requires credentials CI cannot mint.** ARK's UE5 join handshake requires EOS PlayFab or Steam authentication. Without valid credentials, the join cannot proceed past the initial handshake. This is a credential gate at PARTIAL depth at best.

3. **UDP dial is not falsifiable.** `net.Dialer.DialContext` on UDP creates a locally-bound socket without communicating with the remote address. It succeeds even when the remote port is closed or the host is unreachable. UDP dial proves nothing about server state.

4. **TCP handshake is falsifiable.** A TCP connection attempt requires the remote to accept the connection. This is a real handshake that fails if the port is closed or the host is unreachable. A successful TCP connect proves a listener exists on that port.

5. **RCON port proves listener presence.** The RCON port (27020 TCP) is declared by all ARK servers as per the Source protocol spec. A successful TCP connect on this port proves the server is running and has basic network functionality. This is a defensible QUERY depth: "a listener is present."

6. **Depth is measured, not guessed.** QUERY is proven by TCP handshake success. The join handshake's credential gate is not attempted, so depth does not escalate to PARTIAL or JOINED.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness and Depth type

**External:**
- Stdlib only: `context`, `encoding/hex`, `fmt`, `log`, `net`, `time`.

No external modules.

## Security considerations

1. **No credentials in argv.** The probe packet is a single 0x00 byte; no passwords, tokens, or authentication attempts are made.

2. **No attempt to join the game.** The probe is read-only; it dials and reads but does not authenticate or inject commands.

3. **UDP diagnostics only.** The probe sends and reads UDP packets; no state modification is possible.

## Testing & coverage

**No unit tests for the probe itself.** ARK probe testing relies entirely on the e2e test (`test/e2e/ark_bot_e2e_test.go`) running against a real cluster and server. There is no protocol family to test separately (unlike A2S or Source, which have unit tests in `test/e2e/internal/protocol/`).

## Runtime characteristics

### Image Pin

- **Image:** `mschnitzer/asa-linux-server:1.5.1` (explicit version tag).
- **Rationale:** The image is pinned to a specific version for CI hermetic-ness. This version is verified to exist and is actively maintained.

### Boot Time and Disk

**Configured budgets:**
- **Ready timeout budget:** 20 minutes (configured in `ark_bot_e2e_test.go`). ARK requires pulling a ~20GB image from registry and extracting SteamCMD files, so a long boot time is expected.
- **Storage size:** 30Gi (allocated in PVC; covers Steam cache, SteamCMD cache, server-files, and cluster-shared data as documented in the template).
- **Probe deadline:** 6 minutes (configured in `ark_bot_e2e_test.go`; how long the in-cluster probe retries UDP connectivity before timing out).
- **CPU/memory requests:** 2 CPU, 10Gi memory; limits 6 CPU, 20Gi memory.

**Measurements:** None yet (under development).

These budgets are conservative starting points informed by the template's resource declarations. Actual performance will be measured and recorded after first CI run.

### Readiness Probe

The GameTemplate shipped in `modules/ark-survival-ascended/template.yaml` deliberately declares **no readiness probe**. See the template's header comment:

> NO PROBES — deliberately. ASA exposes no probeable TCP port:
> - game/peer are UDP, which the kubelet cannot probe
> - RCON is NOT enabled by default (only when the admin edits GameUserSettings.ini)

The operator relies on kubectl's default `phase == Running` readiness (all containers started and stayed up for `minReadySeconds`). The e2e test waits for `status.phase == Running` before running the probe Job.

### Console and RCON

The shipped template declares `rcon.protocol: source` on TCP 27020, but RCON is **not enabled in the server by default**. The image only honors RCON settings from GameUserSettings.ini, which the template deliberately does not render (to avoid clobbering the admin's in-game settings like difficulty, taming rates, structure limits, etc.).

A one-time manual step is required: the admin must edit GameUserSettings.ini directly (via the Files tab in the dashboard) to enable RCON and set the password. After that, the Gameplane console and RCON features become available. Until then, there is no RCON console for this game.

### Heavy Set

ARK: Survival Ascended is deliberately in the **heavy set** of bot tests (not in the default `make test-e2e` run). It is enabled only with `GAMEPLANE_E2E_GAMES=all` and `GAMEPLANE_E2E_REUSE_CLUSTER=1`. The heavy set never runs in CI.

**Why:** ARK's image is ~20GB. GitHub-hosted runners have ~14GB usable disk after the OS and Docker overhead. A single ARK server exhausts node disk, and concurrent other tests would compete for space.

## Depth Expectation & Reconciliation

**Current expectation:** QUERY

This test is named `TestGameServer_ArkBot_Query` and expects `ExpectDepth: "QUERY"`. The depth is established by successful TCP handshake on the RCON port (27020):

- **TCP port 27020 accepts connections:** proves QUERY depth, a listener is present and accepting connections.
- **What this proves:** the ARK server is running and has basic network functionality.
- **What this does NOT prove:** that RCON is enabled, that game players can join, or that any protocol handshake succeeded. TCP accept only proves the port is open.

**Why not the game port (UDP 7777)?**
UDP dial does not handshake. It creates a locally-bound socket and succeeds even when the remote address is unreachable or the port is closed. Any assertion based on "UDP dial succeeded" is not falsifiable.

**Why the RCON port (TCP 27020)?**
TCP requires a real handshake with the remote listener. A successful connect proves a listener exists; a failed connect proves it doesn't. This is falsifiable: a dead server will reject the connection. It's a defensible proxy for "server is alive" in the absence of a real query protocol.

**Credential gate analysis:**
The join handshake requires EOS/Steam identity. CI cannot mint such credentials. The depth cannot escalate to PARTIAL (where the gate appears) without:
1. Obtaining valid EOS/Steam test credentials in CI (may be possible via CI-maintained test accounts).
2. Implementing the EOS/Steam authentication flow (out of scope for this iteration).

For now, QUERY (RCON port listening) is the honest, defensible measurement.

**ConfigFiles investigation:**
The task notes suggest investigating whether `configFiles` could be used to render GameUserSettings.ini at boot. This is worth documenting:

- `configFiles` is a GameTemplate field that renders templates into mounted paths at pod start.
- ARK's GameUserSettings.ini is a game-owned configuration that the server itself rewrites on shutdown (storing player settings like difficulty, taming rates, structure limits, etc.).
- Using `configFiles` to render a fresh GameUserSettings.ini on every restart would:
  - Wipe the admin's in-game settings on every pod restart, which is destructive.
  - Require the admin to re-tune the server every time the pod restarts unexpectedly (which defeats the purpose of persisting settings).

**Conclusion:** `configFiles` is not suitable for GameUserSettings.ini. The template's approach (using `rcon.passwordFile` for the Gameplane-owned password, but leaving GameUserSettings.ini alone) is correct. Enabling RCON requires a one-time manual edit of that file via the Files tab.

## References

- **Probe application:** `test/e2e/internal/ark-survival-ascended/app.go`
- **E2E test:** `test/e2e/ark_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/probe.go`
- **Shipped template:** `modules/ark-survival-ascended/template.yaml`
- **mschnitzer/asa-linux-server Docker image:** https://github.com/mschnitzer/ark-survival-ascended-linux-container-image
- **Unreal Engine 5:** https://www.unrealengine.com/ (no public join protocol specification available)
