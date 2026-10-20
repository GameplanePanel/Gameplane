# minecraft-java — E2E Probe Specification

## Coverage Status

- **Status**: covered-in-ci
- **Depth**: JOINED
- **Test**: TestGameServer_MinecraftJavaBot_Joined
- **Bucket**: bot-fast
- **Last Verified**: 2026-08-16
- **Blocker**: —
- **Blocker Class**: —

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/minecraft-java`  
**Dependencies:** stdlib only (Go 1.25+); tested against Minecraft 1.21.4; image: `itzg/minecraft-server:java21`

## Purpose

End-to-end test harness for Minecraft: Java Edition, proving that a Gameplane-managed Minecraft server is not merely "Running" in Kubernetes but genuinely playable. The harness:

1. Implements the Minecraft wire protocol client (`protocol/minecraft.go`) for the e2e suite.
2. Provides a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
3. Connects to the game server via the cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path.
4. Performs a Server List Ping (status query) to verify the server answers protocol requests.
5. Attempts a login handshake in offline mode to confirm the world is ready for players.

This is part of the game-bot test suite (`test/e2e/minecraft_bot_e2e_test.go`) and demonstrates that a player-facing control flow (discovery + join) succeeds end-to-end.

## Responsibilities

1. **Protocol client:** Implement the Minecraft 1.7+ post-Netty wire protocol for Ping and Login states only.
2. **Connection lifecycle:** Dial the server, send the handshake and state-change packets, parse the server's responses.
3. **Status probing:** Extract version, protocol number, and player counts from the Server List Ping response.
4. **Login probing:** Detect whether the server accepts offline-mode logins (the standard for lab/test installs) or requires online-mode authentication (Mojang).
5. **Retrying:** The probe application retries both ping and login because the server may be bootstrapping — it answers pings while preparing the world but refuses logins until that is done.
6. **Exit signaling:** Report the join depth reached (JOINED on success; PARTIAL or QUERY on failure) so the test harness can assert the expected outcome.

## Non-goals / boundaries

- Outside sustained mode, does not implement the Play state. Sustained mode (see below) stays in Configuration and Play for a fixed window, but still exchanges no chat, commands, chunks, or player movement beyond the packets it must answer.
- Does not authenticate with Mojang (online-mode); only offline-mode is supported.
- Does not handle chat, world data, or any data beyond the login handshake.
- Compression: Set Compression is honoured for both reads and writes in every mode (zlib, Data Length 0 below the threshold). Sustained mode relies on it; the Ping/Login modes only need it when the server enables it.
- Does not retry the handshake itself; if the network is down, the probe fails immediately.

## Directory & package layout

```
test/e2e/internal/minecraft-java/
├── protocol/
│   ├── minecraft.go           # Wire protocol client: Ping, Login, Connect
│   └── minecraft_test.go       # Unit tests (no build tag; runs under `make test-go`)
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

- **`protocol/`** — a subpackage (`github.com/GameplanePanel/gameplane/test/e2e/internal/minecraft-java/minecraftproto`) implementing the wire protocol. Stdlib only, no external imports.
- **`app.go`** — the main function that runs as a Kubernetes Job. Imports the protocol package and uses the shared `probe` package for retry logic and test framework integration.

## External interface / contracts

### Protocol Package (`protocol/`)

Exported types and functions:

**`type Status`** — Server List Ping response (subset of the full JSON):
```go
type Status struct {
    Version struct {
        Name     string // e.g., "1.21.4"
        Protocol int    // protocol version ID, e.g., 767
    }
    Players struct {
        Max    int // max player count
        Online int // current online count
    }
}
```

**`type Outcome int`** — login attempt result:
```go
const (
    Success Outcome = iota      // Login Success: server accepted offline login
    NeedsAuth                    // Encryption Request: server is in online-mode
    Disconnected                 // Disconnect: server refused the login
)
```

**`type LoginResult`** — login outcome + detail:
```go
type LoginResult struct {
    Outcome Outcome // one of the above constants
    Detail  string  // username on success, reason on failure
}
```

**`func Ping(ctx context.Context, addr string) (*Status, error)`**
- Dials `addr` and sends a Server List Ping (status query).
- Returns the server's version, protocol, and player counts.
- Enforces a 15-second timeout.

**`func Login(ctx context.Context, addr string, protocol int, username string) (*LoginResult, error)`**
- Dials `addr` and completes the login handshake in offline mode.
- `protocol` — the protocol version from `Ping` (ignored by servers for status but required for login).
- `username` — the player name; must be ≤16 characters (enforced with a hard rejection before any bytes are sent).
- Returns whether the server sent Login Success, Encryption Request (online-mode), or Disconnect.
- Enforces a 25-second timeout and transparently handles compression if the server enables it.

**`func Connect(ctx context.Context, addr, username string) (*Status, *LoginResult, error)`**
- Convenience function combining Ping and Login in a single call.
- Pings first to extract the protocol version, then logs in using it.

### App (`app.go`)

**`func main()`**
- Registers a `-user` flag (default: `gameplane-bot`, max 16 chars).
- Calls `probe.ParseFlags()` to register and parse shared flags (`-addr`, `-deadline`, `-expect-depth`).
- Calls `probe.Main()` with a closure that runs `probeMinecraft`.
- Exits 0 only if `probeMinecraft` returns `probe.Joined` and the exit code matches the expected depth.

**`func probeMinecraft(ctx context.Context, addr, user string) (probe.Depth, error)`**
- Retries the server-list ping until successful or deadline expires (reports `probe.Query` on failure).
- Logs the server's version and player counts.
- Retries the login handshake until successful or deadline expires.
- Treats `NeedsAuth` as a fatal failure (returns `probe.Partial` because the server is online-mode, not offline-mode as expected).
- Returns `probe.Joined` on login success.

### Shared Probe Package (`github.com/GameplanePanel/gameplane/test/e2e/internal/probe`)

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

## Sustained mode (`-mode sustained`)

Sustained mode checks that a player stays connected, not only that the login succeeds. It is used by the capture-overhead regression test (`test/e2e/capture_overhead_e2e_test.go`), which runs it repeatedly while packet capture is on.

**Flags**

| Flag | Default | Meaning |
|------|---------|---------|
| `-mode sustained` | — | Selects this mode. |
| `-hold` | `30s` | How long the session must stay open after Play starts. Must be > 0. |
| `-ping-every` | `2s` | Interval between Ping Requests used for RTT samples. Must be > 0. |
| `-user`, `-addr`, `-deadline`, `-expect-depth` | as in join mode | `-expect-depth` should be `JOINED`. |

**Flow.** The probe runs the same ping and login warm-up retries as `-mode join` (OD-5: the warm-up is retried, the hold is not). Once a login succeeds, it calls `minecraftproto.Hold` exactly once.

**States handled** (protocol 769 / Minecraft 1.21.4 only; ids per OD-3):

- Login: sends Login Acknowledged (serverbound `0x03`) after Login Success.
- Configuration: clientbound Select Known Packs `0x0e` is answered with serverbound `0x07` and an empty set. Keep Alive `0x04` is echoed as serverbound `0x04`. Ping `0x05` is answered with Pong `0x05`. Finish Configuration `0x03` is acknowledged with serverbound `0x03`, which starts Play. Disconnect `0x02` ends the hold.
- Play: Keep Alive `0x27` is echoed as serverbound `0x1a`. Ping `0x37` is answered with serverbound `0x2b`. Synchronize Player Position `0x42` is confirmed with Confirm Teleportation `0x00`, using the teleport id, which is the first VarInt of the packet. Disconnect `0x1d` ends the hold. Other packets are ignored.
- Every other packet in either state is ignored.

**Keep-alive and RTT semantics (OD-3).** A Keep Alive is server-initiated: the client only echoes its id, so no round trip can be timed from it. `keepalives_received` therefore counts Keep Alives received in Configuration and Play, and every one is echoed. The RTT samples (`keepalive_rtt_samples_ms`) come from the Ping Request / Pong Response pair instead. The probe sends serverbound Ping Request `0x24` with an incrementing `i64` id every `-ping-every`, and records the time from send to the clientbound Pong Response `0x38` with the same id. Pings still unanswered when the hold ends get a grace period of up to 1 second to be answered. Any still missing then count as loss.

**Hold window.** The clock starts when Play starts (after Finish Configuration is acknowledged), not at dial. `held_full` is true only when the full `-hold` elapsed with no failure.

**Failure classes** (the `failure` field, also used to choose the exit code):

| `failure` | Cause | Verdict |
|-----------|-------|---------|
| `""` | Hold window completed. | `JOINED`, exit 0 if it matches `-expect-depth`. |
| `drop` | Connection closed or reset after login. | `FAIL_TRANSPORT`, exit 3. |
| `timeout` | No packet from the server for 20 seconds, or the probe context ended. | `FAIL_TRANSPORT`, exit 3. |
| `disconnect: <reason>` | The server sent a Disconnect packet in Configuration or Play. The reason is the text component, decoded when it is a plain string and shown as hex otherwise. | `FAIL_TRANSPORT`, exit 3. |
| `join: <err>` | Dial, handshake or login failed. | Exit 3 for a network error, exit 1 otherwise. |

Transport classification uses the keywords in `test/e2e/internal/protocol/joindepth/verdict.go`. Hold's error text is worded to contain them: `connection reset: ...` for drop and disconnect, and `timeout: ...` for timeout.

**METRICS line.** Before the VERDICT line, sustained mode prints exactly one line:

```
METRICS	{"handshake_latency_ms":…,"join_duration_sec":…,"held_sec":…,"held_full":…,"keepalives_received":…,"keepalive_rtt_samples_ms":[…],"pings_sent":…,"packets_sent":…,"packets_received":…,"bytes_sent":…,"bytes_received":…,"packet_loss_pct":…,"failure":"…"}
```

- `handshake_latency_ms`: from writing the handshake and Login Start to receiving Login Success.
- `join_duration_sec`: from dialling to the start of Play.
- `held_sec`: from the start of Play to the end of the hold, or to the failure.
- `packet_loss_pct`: `(pings_sent - pongs_received) / pings_sent * 100`, or 0 when no pings were sent.
- `keepalive_rtt_samples_ms` is `[]`, never `null`, when there are no samples.

This line is printed even when the warm-up never succeeded. Its `failure` is then `join: …`.

**VERDICT and exit codes.** On success the verdict is `VERDICT	JOINED	JOINED	held <hold>, <n> keepalives, <m> rtt samples`. Failures map as in the table above. Exit codes follow `joindepth.ExitCodeFromVerdict`: 0 success, 1 internal or bad flags (including non-positive `-hold` or `-ping-every`), 2 wrong depth (for example an online-mode server, which yields PARTIAL), 3 transport failure.

**Protocol support (FR-009).** Sustained measurement is implemented and tested for protocol 769 (Minecraft 1.21.4) only. Other protocol versions have different packet ids and are not supported. The first CI run against the real `itzg/minecraft-server` 1.21.4 container is the verification gate for the ids in OD-3.

## Key invariants

1. **16-character username cap:** Minecraft reads login names with a fixed-length 16-byte UTF-8 reader. A longer name causes the server to throw `DecoderException` and drop the connection with the opaque error "Failed to decode packet 'serverbound/minecraft:hello'". The `Login` function rejects usernames > 16 chars *before* dialing, failing fast and loudly.

2. **Offline-mode only:** The probe does not support online-mode (Mojang authentication). If the server sends an Encryption Request, it reports `NeedsAuth` as a fatal outcome. This is correct for the lab/test templates, which always set `ONLINE_MODE=FALSE`.

3. **Ping/Login split:** The ping and login retries are independent. A server may answer pings while the world is still generating, but refuse logins until the world is ready. The app retries each step separately so a delayed world-gen does not cause a false negative.

4. **Compression transparence:** The protocol client detects and handles zlib compression on compressed packets without exposing it to callers. This keeps the wire format details internal and allows the probe to work across server versions with different compression policies.

5. **Protocol version stability:** The login handshake (packet IDs 0x00–0x03 in the login state) has not changed since Minecraft 1.7. The probe targets a specific Minecraft version (1.21.4) but the wire format is stable, so updates to that version are low-risk.

6. **Join depth semantics:** Returning `probe.Joined` (not just "login returned success") signals that a player can join and play. In Minecraft, a player can join once the login handshake completes with Login Success.

## Dependencies

**Internal:** `github.com/GameplanePanel/gameplane/test/e2e/internal/probe` (shared probe harness).

**External:** stdlib only (`bufio`, `bytes`, `compress/zlib`, `context`, `crypto/rand`, `encoding/binary`, `encoding/json`, `errors`, `flag`, `fmt`, `io`, `log`, `net`, `strconv`, `strings`, `time`).

No third-party Go modules. The probe image builds with `GOWORK=off` against `test/e2e/go.mod` alone, so external imports would break the image build.

## Security considerations

1. **Username length check:** The 16-char cap is not just a convenience; it's a security boundary. An oversized username would cause the server to drop the connection and log an error, making debugging harder. Failing fast in the probe protects against typos and configuration errors.

2. **No credentials in logs:** The probe does not log the username at login time (only on success). This avoids leaking credentials if logs are exposed.

3. **Offline-mode assumption:** The probe assumes the server is in offline-mode and rejects online-mode servers. This is correct for test/lab installs but would fail on production servers. That's intentional — the templates are configured for their test use.

4. **No command injection:** The probe does not accept or execute any user-supplied commands; it only connects and reads server responses. Wire format parsing is strict (VarInt bounds, string length validation), so there is no injection surface.

## Testing & coverage

**Test file:** `protocol/minecraft_test.go` (no build tag; runs under `make test-go`)

**Test cases cover:**
- **`TestVarIntRoundTrip`** — VarInt encoding/decoding for single-byte, multi-byte, and boundary values.
- **`TestVarIntTooLong`** — Reading a 6-byte VarInt (beyond the 5-byte limit) returns an error.
- **`TestStringRoundTrip`** — Length-prefixed string encoding/decoding for empty, short, and long strings.
- **`TestPingWithFakeServer`** — Pings a fake local server and extracts version/protocol/player counts.
- **`TestLoginLongUsernameRejected`** — Attempts to login with a 25-char username; the function rejects it before dialing.
- **`TestLoginConnectFailure`** — Attempts to login to a closed port; dials fail immediately.
- **`TestTruncatedPacketError`** — Receiving an incomplete packet (fewer than 5 bytes for the length VarInt) produces an error, not a hang.
- **`TestGarbageResponseError`** — Receiving a packet with an unexpected ID (0xFF instead of 0x00) produces an error.
- **`TestContextDeadlineEnforced`** — A tight context deadline (100ms) times out before the server responds.
- **`TestStringLengthValidation`** — Receiving a truncated string (declared 100 bytes but only 5 sent) produces an error.

- **`TestFrameRoundTrip`** — Writer and reader agree on plain framing and on compressed framing (both the Data Length 0 path and the deflated path).
- **`TestHoldFullSession`** — A 1.5s sustained session against an in-process fake server, with and without compression. Checks every METRICS field, the echoes in both states, and that every ping is answered.
- **`TestHoldServerClosesMidHold`** — The server closes during Play, which must report `drop`.
- **`TestHoldServerDisconnects`** — A Disconnect in Play reports `disconnect: <reason>`.
- **`TestHoldSilentServerTimesOut`** — A server that never finishes Configuration reports `timeout`. The test shortens the package variable `holdSilenceTimeout` to 300ms.
- **`TestHoldRejectsBadArguments`** — Non-positive durations and overlong usernames are rejected before dialling.

The `Ping` and `Login` tests use `t.Parallel()` and `net.Listen` on 127.0.0.1:0 for isolation. The `Hold` tests do not, because they change `holdSilenceTimeout`. No external services or fixtures.

## Runtime characteristics

### Image Pin

- **Image:** `itzg/minecraft-server:java21` (floating tag, no digest pin).
- **Rationale:** This is a test image, not a production release artifact. The `java21` variant is chosen for fast startup (smaller JVM). Floating tags are acceptable here because e2e tests re-pull images on each run.

### Boot Time and Disk

**Configured budgets (not measurements):**
- **Ready timeout budget:** 10 minutes (configured in `minecraft_bot_e2e_test.go`). First boot requires pulling the Docker image, downloading the Minecraft server jar, and generating the superflat world—all heavyweight I/O operations that justify the generous budget.
- **Storage size:** 2Gi (allocated, not necessarily used; a superflat world with VIEW_DISTANCE=4 is small).
- **Probe deadline:** 4 minutes (configured in `minecraft_bot_e2e_test.go`; how long the in-cluster probe retries ping and login before failing).

**Measured in CI (GitHub-hosted amd64 runner, kind e2e cluster):**
- **Full test runtime:** 51.09s (GitHub Actions log timestamp 2026-07-24T20:59:34Z, CI run 30125535832, job `e2e game bot (kind)`). This includes GameServer creation, polling for Running status, probe Job creation, and probe execution.
- **Server version reported:** Minecraft 1.21.4, protocol version 769
- **Probe outcome:** Login succeeded (server accepted player name "gameplane-bot"), depth JOINED confirmed.
- **Subsequent-boot times:** Not yet measured.

**Fast set:** This test is in the **fast set** — it runs in every CI job (not just heavy jobs) because the 10-minute timeout + 4-minute probe deadline is acceptable for a bot test, and the turnaround is important for catching regressions quickly.

### Protocol Reference

**Minecraft protocol:** Minecraft 1.7+ (post-Netty)  
**Wire format:** TCP, big-endian  
**Reference:** https://wiki.vg/Protocol and https://minecraft.wiki/w/Protocol

**Packet format (uncompressed):**
```
VarInt(total_length) | VarInt(packet_id) | payload...
```

**Login state packets used:**
```
0x00 (Login Start)       → sent to server
0x00 (Disconnect)        ← received if server rejects login
0x01 (Encryption Request) ← received if server is in online-mode
0x02 (Login Success)     ← received if server accepted offline login
0x03 (Set Compression)   ← received to enable zlib compression for subsequent packets
```

**Status state packets used:**
```
0x00 (Status Request)    → sent to server
0x00 (Status Response)   ← JSON response with version, protocol, players
```

## References

- **Protocol implementation:** `test/e2e/internal/minecraft-java/minecraftproto/minecraft.go`
- **Probe application:** `test/e2e/internal/minecraft-java/app.go`
- **E2E test:** `test/e2e/minecraft_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/` (created by parallel agent)
- **Minecraft protocol wiki:** https://wiki.vg/Protocol
- **Minecraft protocol reference:** https://minecraft.wiki/w/Protocol
- **itzg/minecraft-server Docker image:** https://github.com/itzg/docker-minecraft-server
- **CLAUDE.md:** Rule 3 (login privacy) — the probe does not run on pre-auth screens; this is internal testing only.
