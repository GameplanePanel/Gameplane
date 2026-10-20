# terraria — Specification

## Coverage Status

- **Status**: covered-in-ci
- **Depth**: JOINED
- **Test**: TestGameServer_TerrariaBot_Joined
- **Bucket**: bot-fast
- **Last Verified**: 2026-08-16
- **Blocker**: —
- **Blocker Class**: —

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/terraria`  
**Dependencies:** stdlib only (Go 1.25+)

## Purpose

Per-game probe for Terraria: a headless protocol client that dials a running Terraria server and proves it is playable by completing the connection handshake (ConnectRequest → ContinueConnecting) and requesting world data.

Terraria is the one non-Minecraft shipped game where this is practical: the server ships in the image (no external download), speaks TCP (not UDP-only like Factorio), and has a simple protocol. Valheim/Palworld require multi-GB steamcmd downloads; Factorio is UDP-only — neither are bot-testable in CI.

The probe reaches the **JOINED** depth: server accepts the client as a player and answers WorldData.

## Responsibilities

1. Dial the game server over TCP at a specified address and port.
2. Complete the Terraria wire-protocol handshake: send ConnectRequest with a protocol version string, receive ContinueConnecting or an error.
3. Handle version-mismatch kicks: if the server sends a Disconnect message naming a different protocol version, retry once with that version.
4. Request and receive world data to prove the server considers the connection a real joining client (not merely a TCP accept).
5. Implement the shared probe interface: `Main()` consumes flags, retries until success or timeout, and reports join depth.

## Non-goals / boundaries

- Does not authenticate as a real player (no name, character data, or post-connect framing).
- Does not parse or validate world data; presence of any non-empty WorldData packet is sufficient.
- Does not handle credential gates (passwords, IP bans, server-full). A password prompt is treated as fatal (misconfiguration; retrying won't help).
- Does not handle UDP protocols or other transports.
- Does not handle game updates or multiple protocol versions beyond a single self-correction attempt.

## Directory & package layout

```
test/e2e/internal/terraria/
├── protocol/
│   ├── terraria.go           # Wire-protocol client (Connect, RequestWorldData)
│   └── terraria_test.go      # Unit tests (all public functions and edge cases)
├── app.go                    # Probe entry point (package main)
└── spec.md                   # This file
```

The `protocol` package is a pure wire-format client usable by any code (tests, tools, future integrations). The `app.go` main entry point sits in the parent `terraria` package so the binary built from `test/e2e/Dockerfile` can simply invoke `/probe/terraria`.

## External interface / contracts

### protocol package types

**`ConnectResult`** — outcome of a successful handshake.

```go
type ConnectResult struct {
	Slot    byte   // player slot the server assigned (0–255)
	Version string // protocol string that was accepted ("Terraria279", etc.)
}
```

**`Conn`** — a live, handshaken Terraria connection.

```go
type Conn struct {
	c net.Conn
}

func (c *Conn) Close() error
func (c *Conn) RequestWorldData(ctx context.Context) error
```

**`DefaultVersion`** — the protocol string for Terraria 1.4.4.x.

```go
const DefaultVersion = "Terraria279"
```

**`ErrPasswordRequired`** — sentinel error: handshake reached the password prompt.

### protocol package functions

**`Connect(ctx context.Context, addr string) (*Conn, *ConnectResult, error)`**

Dials `addr` (host:port) over TCP and completes the ConnectRequest handshake.

Behavior:
- Sends ConnectRequest with `DefaultVersion`.
- On a Disconnect message that names a different version (via NetworkText substitutions), retries once with that version.
- On a Disconnect message that does NOT name a version (like Terraria's LegacyMultiplayer.4 kick), fails immediately — retrying won't help because the server hasn't told us what version it wants.
- On a PasswordRequired message, closes and returns `ErrPasswordRequired`.
- On success (ContinueConnecting), returns a `*Conn` and `*ConnectResult`.

The version-self-correction is critical for forward compatibility: a module may ship with a pinned server image, but if that image drifts and the new version's protocol differs, `Connect` can adapt on the fly — but only if the server's kick message names the version. This is why the e2e template pins `terraria-docker:terraria-1.4.4.9`: Terraria 1.4.5.x's protocol differs, and 1.4.5.x images have drifted into :latest; the pin prevents the bot from racing world generation against an image we didn't test.

### app.go entry point

The `main()` function calls `probe.ParseFlags()` (registers `-addr`, `-deadline`, `-expect-depth`), then `probe.Main(flags, probeTerraria)` to run the probe and exit appropriately.

The probe calls `probe.Retry()` internally: it retries the handshake + world-data request until success, the deadline expires, or a fatal error occurs (password prompt). Success = reaching `probe.Joined` depth.

## Key invariants

1. **Protocol pinning:** The template pins `terraria-docker:terraria-1.4.4.9` (protocol Terraria279). Floating tags (`terraria-latest`) have drifted past the bot's version before; pinning is non-negotiable here.

2. **Version self-correction:** `Connect` retries on a version-mismatch kick, but only if the server names the version. A kick that doesn't name a version (Terraria's LegacyMultiplayer.4) is not retriable.

3. **Password is fatal:** `ErrPasswordRequired` causes `probeTerraria` to return `probe.ErrFatal`, aborting the probe. This template sets `SECURE=0` (no password), so a password prompt means misconfiguration, not a flaky server.

4. **World data proves joinability:** Receiving a non-empty WorldData packet proves the server sees the connection as a real player, well past TCP accept. This is the final assertion.

5. **TCP-only:** Terraria speaks TCP on port 7777 (configurable per template). UDP is not used by the handshake.

## Dependencies

**Stdlib only:**
- `context` — deadline and cancellation
- `net` — TCP dial, connection I/O
- `time` — timeouts, delays
- `bytes` — buffering and parsing wire data
- `encoding/binary` — little-endian integer encoding
- `errors` — error wrapping and matching
- `fmt` — error messages
- `io` — ReadFull primitive
- `regexp` — version-token matching
- `flag` — CLI arg parsing (via the shared `probe` package)

No external modules. The `protocol` package imports only stdlib; `app.go` imports the shared `probe` package and `protocol`.

## Security considerations

1. **TCP-only, no auth:** The probe does not authenticate. It relies on network isolation (the game pod's firewall ingress rules) to prevent unauthorized players. The probe is a technical test, not a security model.

2. **No player persistence:** The server sees the connection but does not persist state for it (no character save, no inventory). The connection is closed immediately after world data is received.

3. **Password handling:** If the server requires a password, the probe treats it as fatal. This is intentional: the e2e template sets `SECURE=0` to allow unauthenticated joins, so a password prompt indicates misconfiguration in CI, not a real security gate.

4. **Protocol resilience:** The version-mismatch self-correction allows the probe to adapt to image drifts, but only within the constraints of the pinned template. A human must update the pin if the image's protocol diverges too far.

## Testing & coverage

**Test file:** `protocol/terraria_test.go`

**Test coverage:** Covers all public functions and error paths.

**Test cases:**
- **Wire format:** 7-bit-encoded-int round-trip for string lengths (single-byte and multi-byte); truncated/overflow input rejection.
- **Framing:** `writeMessage`/`readMessage` round-trip; malformed frame length handling.
- **Handshake:** successful `Connect` against a fake server; password-required path; version-mismatch kick with self-correction; version-mismatch kick with NO version (fatal).
- **World data:** successful `RequestWorldData`; empty payload rejection; disconnect before WorldData.
- **NetworkText parsing:** substitution extraction; literal mode; truncated input (best-effort, no panic).
- **Connection lifecycle:** `Close()` and write-after-close verification.

No integration or e2e tests here — `terraria_bot_e2e_test.go` runs the probe against a real cluster and server.

## References

- **Terraria protocol:** Reverse-engineered from the official Terraria server source. No public spec exists; community wikis document the handshake but not all fields.
- **Image pinning:** `modules/terraria/template.yaml` specifies the image and environment for shipped deployments.
- **Probe harness:** `test/e2e/internal/probe/probe.go` provides the retry loop, deadline management, and depth reporting.
- **E2E test:** `test/e2e/terraria_bot_e2e_test.go` runs the probe against a real GameServer.
- **Shared specs:** `gameaction/specs.md`, `netguard/specs.md` — sister packages with similar documentation.

## Appendix: Wire Format

Every message is `[length uint16 LE][type byte][payload]`, where length counts the full message including the 2 length bytes.

Strings are .NET BinaryWriter style: 7-bit-encoded length (variable 1–3 bytes) followed by UTF-8 bytes.

### Message types used by the handshake

- **0x01 ConnectRequest:** client initiates join. Payload: protocol version string (e.g., `Terraria279`).
- **0x02 Disconnect:** server rejects or terminates. Payload: NetworkText (reason + optional substitutions for version kicks).
- **0x03 ContinueConnecting:** server accepts. Payload: player slot byte (0–255).
- **0x06 RequestWorldData:** client asks for world header. No payload.
- **0x07 WorldData:** server responds with world data. Payload: world header (non-empty).
- **0x25 (37 decimal) PasswordRequired:** server requires a password. No payload.

### NetworkText format

Used in Disconnect messages. Structure: `[mode byte][text string][substitution count (if mode != 0)][substitutions...]`

- **mode 0:** literal text only.
- **mode 1+:** text + substitutions. Each substitution is a nested NetworkText.

Version-mismatch kicks encode the wanted version as a substitution, e.g., `mode=1, text="server wants Terraria280", subs=[NetworkText{mode=0, text="Terraria280"}]`. The regex `^Terraria\d+$` matches version tokens.

### Boot time and disk cost

**Configured budgets (not measurements):**
- **Ready timeout:** 10 minutes (configured in `terraria_bot_e2e_test.go` line 55; how long the test waits for `GameServer.status.phase` to reach `Running`).
- **Probe deadline:** 4 minutes (configured in `terraria_bot_e2e_test.go` line 57; how long the in-cluster probe retries the handshake and world-data request before failing).
- **Storage request:** 2Gi (configured in `terraria_bot_e2e_test.go` line 47; persistent volume requested for `/opt/terraria/config` — a PVC request, not a measurement of actual disk usage).

**Measured in CI (GitHub-hosted amd64 runner, kind e2e cluster):**
- **Full test runtime:** 67.07s (GitHub Actions log timestamp 2026-07-24T20:59:34Z, CI run 30125535832, job `e2e game bot (kind)`). This includes GameServer creation, polling for Running status, probe Job creation, and probe execution.
- **Probe outcome:** Handshake succeeded (server accepted connection and assigned player slot), world data received, depth JOINED confirmed.
- **Actual disk usage:** *not yet measured*.
- **Image pull time:** *not applicable in CI* (images are side-loaded into kind via `docker load`).
- **Subsequent-boot times:** *not yet measured*.

### Belongs to fast set

Terraria is included in the **fast set** of bot tests (runs by default without `GAMEPLANE_E2E_GAMES=all`). The other fast-set games are Minecraft Java, Factorio, and Garry's Mod.
