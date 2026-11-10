# A2S (Valve Source Query) Protocol

**Status:** pre-v1 (v0.4.0)  
**Module / package:** `test/e2e/internal/protocol/a2s`  
**Dependencies:** stdlib only (Go 1.25+)

## Purpose

Shared A2S (Valve Server to Client) query protocol implementation used by games built on the Source engine (CS2, Garry's Mod, DayZ, Palworld, etc.). Provides headless clients for `A2S_INFO` and `A2S_PLAYER` queries to measure server availability and player counts without requiring a game client or in-game join. Used by the e2e test suite's probe binaries to verify that Gameplane-managed game servers are genuinely playable and reachable.

## Responsibilities

1. Query game server info (name, map, player count, version) via `A2S_INFO`.
2. Transparently handle the modern challenge-response flow (post-2020 reflection attack mitigation): when a server responds with `S2C_CHALLENGE` (0x41), resend the request with the challenge appended.
3. Query player list via `A2S_PLAYER` (always requires a challenge).
4. Parse wire-format responses into structured Go types (`Info`, `Player`).
5. Handle truncated/garbage responses gracefully (errors, not panics).
6. Respect context deadlines; timeout on unresponsive servers.

## Non-goals / boundaries

- Does not handle the full Valve server query protocol (e.g., `A2S_RULES`, `A2S_PING`), only the two queries used by the probe suite.
- Does not provide a server implementation; this is client-only.
- Does not retry on lost UDP packets; a single lost request/response times out (matching real player behavior).
- Does not support IPv6; dials are UDP only to the address provided.

## Directory & package layout

```
test/e2e/internal/protocol/a2s/
├── a2s.go           # Info/Player query functions and wire parsing
├── a2s_test.go      # Fake UDP server tests covering immediate/challenge flows
└── spec.md          # (this file)
```

Single package; no subdirectories.

## External interface / contracts

### Query functions

**`QueryInfo(ctx context.Context, addr string) (*Info, error)`**  
Performs an A2S_INFO query against the given UDP address. Handles the challenge-response flow transparently:
- Sends request to `addr`.
- If server replies with `S2C_CHALLENGE` (0x41), extracts the 4-byte challenge and resends the request with the challenge appended.
- Returns parsed `Info` struct or error.

**`QueryPlayers(ctx context.Context, addr string) ([]Player, error)`**  
Performs an A2S_PLAYER query (always requires a challenge).
- First calls `QueryInfo` to obtain a challenge (and verify the server is reachable).
- Sends `A2S_PLAYER` request with the challenge.
- Returns slice of `Player` structs or error.

### Data types

**`Info` struct:**
```go
type Info struct {
    Protocol    byte   // Valve protocol version (typically 17 for Source)
    Name        string // Server name
    Map         string // Current map
    Folder      string // Game directory (e.g., "csgo", "garrysmod")
    Game        string // Game description (e.g., "Counter-Strike: Global Offensive")
    ID          uint16 // App ID (e.g., 730 for CS:GO)
    Players     byte   // Players online
    MaxPlayers  byte   // Max players slot
    Bots        byte   // Bot count
    ServerType  byte   // 'd' (dedicated), 'l' (listen), 'p' (SourceTV proxy)
    Environment byte   // 'l' (Linux), 'w' (Windows), 'm' (Mac)
    Visibility  byte   // 0 (public), 1 (private)
    VAC         byte   // 0 (unsecured), 1 (VAC secured)
    Version     string // Version string (e.g., "1.0.0.0")
}
```

**`Player` struct:**
```go
type Player struct {
    Index    byte    // Player index (0-based)
    Name     string  // Player name
    Score    int32   // Score (kills, points, etc.; varies by game)
    Duration float32 // Time online in seconds
}
```

## Key invariants

1. **UDP is connectionless and lossy.** A single lost request or response causes a timeout. There is no automatic retry; the caller supplies the deadline context.

2. **Challenge-response is mandatory for modern servers.** Post-2020, most Source servers reply to bare `A2S_INFO` with a challenge to block reflection attacks. The implementation transparently retries with the challenge. A client that doesn't handle this silently fails on modern servers.

3. **Challenge is derived from prior query.** `A2S_PLAYER` always requires a challenge; this implementation obtains it by calling `QueryInfo` first.

4. **Wire format is little-endian (LE).** All multi-byte integers in A2S are encoded little-endian (`0xFFFFFFFF` header, uint16 app ID, int32 score, float32 duration).

5. **Strings are null-terminated (C-string style).** No length prefix; end on the first 0x00 byte.

6. **No partial joins.** A2S queries are read-only and measure server availability. They do not attempt to join the game or reach a Play state; even if a server requires login, `QUERY` depth is the limit.

## Dependencies

**Internal:** None.  
**External:** stdlib only (`bytes`, `context`, `encoding/binary`, `errors`, `fmt`, `io`, `net`, `time`). Go 1.25+.

## Security considerations

1. **No authentication.** A2S queries are unauthenticated and sent in plaintext (though typically over LAN or the public internet). Do not embed secrets in query parameters or request bodies.

2. **DoS-safe.** The implementation respects context deadlines and does not loop indefinitely on malformed responses. Oversized or garbage datagrams are rejected cleanly.

3. **No DNS rebinding risk.** UDP queries provide limited surface for DNS rebinding compared to HTTP, but the dial is still to a potentially user-supplied address. Callers should validate addresses before calling if this is a concern (the probe harness runs inside the cluster).

## Testing & coverage

**Unit tests** (`a2s_test.go`) — untagged, run on every `make test-go`:
- `TestQueryInfoImmediate` — info response without challenge.
- `TestQueryInfoChallenge` — server sends S2C_CHALLENGE, client retries with challenge.
- `TestQueryInfoRequestFormat` — verifies A2S_INFO request includes the required magic string.
- `TestQueryPlayersChallenge` — player list query with challenge flow.
- `TestTruncatedResponse`, `TestWrongHeader`, `TestGarbageData` — error handling (truncated/malformed responses).
- `TestDeadlineExceeded` — timeout on unresponsive server.
- `TestParseInfoMinimal`, `TestParsePlayersList` — wire-format parsing.
- `TestQueryInfoNonLoopbackServer`, `TestQueryPlayersNonLoopbackServer` — verify client works against non-loopback peers on the same host. **Do NOT catch a loopback-only bind regression** (all local addresses are reachable from a loopback-bound socket; the real bug required an off-host, routed destination like a Kubernetes ClusterIP).

All tests use an in-process fake UDP server (`startFakeA2SServer`). Responses are hand-built from bytes (not generated by an encoder) to catch encoding bugs in the tests themselves.

**Regression guard (loopback-bind class of defects):** This package's unit tests cannot catch a loopback-only bind bug because all addresses on the same host are locally reachable; the bug requires an off-host routed destination (Kubernetes Service ClusterIP). This defect class is guarded by the e2e tier: the e2e game bot CI job dials a real Service ClusterIP and did catch this bug once in production before the fix.

## Wire format reference

Based on: https://developer.valvesoftware.com/wiki/Server_queries

### A2S_INFO request

```
[4] 0xFFFFFFFF                          header (LE)
[1] 0x54 ('T')                          request type
[25] "Source Engine Query\0"            magic string (required)
[0-4] (optional) 4-byte challenge      only if retrying after S2C_CHALLENGE
```

### S2C_CHALLENGE response

```
[4] 0xFFFFFFFF           header (LE)
[1] 0x41 ('A')           response type
[4] uint32 (LE)          challenge value to echo in retry
```

### A2S_INFO response

```
[4] 0xFFFFFFFF           header (LE)
[1] 0x49 ('I')           response type
[1] byte                 protocol version
[?] \0-terminated        server name
[?] \0-terminated        map name
[?] \0-terminated        folder (game directory)
[?] \0-terminated        game description
[2] uint16 (LE)          app ID
[1] byte                 players online
[1] byte                 max players
[1] byte                 bots
[1] byte                 server type ('d'|'l'|'p')
[1] byte                 environment ('l'|'w'|'m')
[1] byte                 visibility (0|1)
[1] byte                 VAC secured (0|1)
[?] \0-terminated        version string
```

Note: The spec allows optional fields at the end (e.g., EDF extra data flags), but this implementation reads only the core fields documented above. Servers that include extra data may cause parsing errors; this is acceptable for the e2e probe (which runs against known-good server images).

### A2S_PLAYER request

```
[4] 0xFFFFFFFF           header (LE)
[1] 0x55 ('U')           request type
[4] uint32 (LE)          challenge (obtained from A2S_INFO)
```

### A2S_PLAYER response

```
[4] 0xFFFFFFFF           header (LE)
[1] 0x44 ('D')           response type
[1] byte                 player count
[repeated] for each player:
  [1] byte               player index
  [?] \0-terminated      player name
  [4] int32 (LE)         score (kills, points, etc.)
  [4] float32 (LE)       duration (seconds online)
```

## References

- **Valve Developer Community:** https://developer.valvesoftware.com/wiki/Server_queries
- **E2E harness spec:** `test/e2e/internal/specs.md`
- **Probe pattern:** `test/e2e/internal/<game>/app.go`
