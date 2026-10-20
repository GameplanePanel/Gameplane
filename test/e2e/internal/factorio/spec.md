# factorio — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_FactorioBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Wire format undocumented; server and client must match exactly (version-locked)
- **Blocker Class**: documentation

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/factorio`  
**Dependencies:** stdlib only (Go 1.25+); tested against factoriotools/factorio:stable (2.0.x); image: `factoriotools/factorio:stable@sha256:7052b3cca8ca7790f99f4058617d5c8089df544de736b1baa23f2c5f58fb7f48`

## Purpose

End-to-end test harness for Factorio headless server, proving that a Gameplane-managed Factorio server is listening and responsive. The harness:

1. Implements a TCP connection probe (`app.go`) that runs as a Kubernetes Job inside the test cluster.
2. Connects to the server via the cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path.
3. Attempts to establish a TCP connection to the RCON port (27015) to verify the server is listening.
4. Optionally sends a diagnostic best-effort Factorio connection request on UDP 34197 and logs any response (bounded hex) for evidence.
5. Establishes QUERY depth: the RCON port is accepting connections, proving the server is running, but does not attempt a full join (which requires in-game UI and external credentials).

This is part of the game-bot test suite (`test/e2e/factorio_bot_e2e_test.go`) and demonstrates that the server is operational, even though a headless join is not possible in CI.

## Responsibilities

1. **Primary assertion:** Establish a TCP connection to the RCON port (27015) to verify the server is listening and responsive.
2. **Connection lifecycle:** Dial TCP 27015, verify the connection is accepted, and close it.
3. **Retrying:** The probe application retries connection attempts because the server may be bootstrapping — it accepts RCON connections while finishing world generation.
4. **Diagnostic logging:** Optionally send a best-effort Factorio connection request on UDP 34197 and log any response (bounded hex) for evidence. No response is expected (Factorio silently drops unrecognized UDP packets).
5. **Depth measurement:** Assert QUERY depth only (the weakest defensible level), because a full join requires credentials and the in-game multiplayer UI.
6. **Exit signaling:** Report the join depth reached (QUERY only, in this case) so the test harness can assert the expected outcome.

## Non-goals / boundaries

- Does not implement or probe Factorio's multiplayer handshake. The wire format is not publicly documented; only conceptual descriptions (FFF #149/#147) exist. The UDP diagnostic is best-effort only.
- Does not authenticate or join as a player. A real join requires the in-game multiplayer UI and handling of the documented credentials flow, neither of which CI can provide.
- Does not handle game state, saves, or mod loading. Those are tested by Path A (operator + agent).
- Does not retry a TCP connection if the network is down; a TCP dial failure is fatal (server not listening). UDP diagnostics do not affect the result.
- Does not interpret the server's response on UDP beyond logging it as raw bytes. The response format is opaque without protocol documentation.

## Directory & package layout

```
test/e2e/internal/factorio/
├── app.go              # Probe entry point (package main)
└── spec.md             # This file
```

- **`app.go`** — the main function that runs as a Kubernetes Job. Imports only the shared `probe` package for retry logic and test framework integration.
- No subpackage (`protocol/`) exists yet because Factorio's wire format is not documented. Once the protocol is formally reverse-engineered (e.g., via a community project with evidence), a separate `protocol/` package can be added following the Minecraft pattern.

## External interface / contracts

### App (`app.go`)

**`func main()`**
- Calls `probe.ParseFlags()` to register and parse shared flags (`-addr`, `-deadline`, `-expect-depth`).
- Calls `probe.Main()` with a closure that runs `probeFactorio`.
- Exits 0 only if `probeFactorio` returns `probe.Query` (the expected depth).

**`func probeFactorio(ctx context.Context, addr string) (probe.Depth, error)`**
- Retries the TCP connection attempt on the RCON port (27015) until the connection succeeds, ctx expires, or the dial fails fatally.
- Launches a diagnostic goroutine to send a best-effort UDP packet on port 34197 and logs any response (hex-encoded, bounded).
- Returns `probe.Query` if the TCP connection succeeds (RCON port is accepting).
- Returns error if the TCP dial fails (server not listening on RCON).

**`func sendConnectionRequestUDP(ctx context.Context, addr string) ([]byte, error)`**
- Sends a diagnostic best-effort connection request packet to `addr` (UDP port 34197).
- Returns the server's response bytes, or an error if the dial or send fails.
- Timeout or no response returns error (expected behavior; Factorio silently drops unrecognized UDP packets); probe does not retry.

### Shared Probe Package (`github.com/GameplanePanel/gameplane/test/e2e/internal/probe`)

(Defined by shared harness; app.go is coded against this contract.)

```go
var ErrFatal error
type Depth string
const (Joined Depth = "JOINED"; Partial Depth = "PARTIAL"; Query Depth = "QUERY")
type Flags struct { Addr string; Deadline time.Duration; Expect Depth }
func ParseFlags() Flags
func Retry(ctx context.Context, what string, attempt time.Duration, fn func(context.Context) error) error
func Main(f Flags, run func(context.Context) (Depth, error))
```

## Key invariants

1. **TCP assertion:** The probe establishes a TCP connection to the RCON port (27015) as the primary signal that the server is running and responsive. A successful TCP accept is a falsifiable assertion: it fails when the server is not listening, and succeeds when a listener is present.

2. **UDP is diagnostic only:** The connection request packet layout in `sendConnectionRequestUDP` is **inferred from conceptual descriptions only** (FFF #149/#147). It is **not derived from official documentation**. The byte layout (0x01 type, 8-byte session ID, etc.) is a best-effort guess. This probe does not affect the depth result. Real Factorio servers silently drop unrecognized UDP packets, so no response is expected.

3. **No interpretation of UDP response:** The probe does not attempt to parse the UDP server's response. Logging it as raw hex is the entire goal — to have evidence of what the server sends back (if anything).

4. **QUERY depth only:** The probe cannot reach PARTIAL or JOINED because:
   - PARTIAL would require sending structured protocol messages and interpreting the rejection (protocol unknown).
   - JOINED would require completing the handshake, authentication, and the in-game multiplayer UI (impossible in CI).
   The test asserts `ExpectDepth: QUERY`, so returning QUERY signals success.

5. **Measured behavior:** On a real Factorio 2.0.x server (factoriotools/factorio:stable as of 2026-07-25):
   - UDP port 34197: silently drops unrecognized packets (no response).
   - TCP port 27015 (RCON): accepts connections.
   These measurements prove that UDP has no query surface on Factorio, so the TCP assertion is the only falsifiable depth test.

6. **No credentials in logs:** The probe does not log usernames, passwords, or any authentication material. The connection request is generic and not repeated in logs.

7. **Image version pins:** The factorio module's template pins the Docker image to a specific digest (factoriotools/factorio:stable@sha256:...). This probe inherits that pin in the e2e test. The stable channel tracks releases (1.1.x, 2.0.x, etc.) but not pre-releases; floating tags are not acceptable for a blocking test.

## Dependencies

**Internal:** `github.com/GameplanePanel/gameplane/test/e2e/internal/probe` (shared probe harness).

**External:** stdlib only (`context`, `encoding/hex`, `fmt`, `log`, `net`, `time`).

No third-party Go modules. The probe image builds with `GOWORK=off` against `test/e2e/go.mod` alone.

## Security considerations

1. **No credentials in argv:** The probe accepts no user-supplied authentication material. The connection request is a generic best-effort packet.

2. **No command injection:** The probe does not accept or execute any user-supplied commands. It only sends a fixed packet and reads bytes.

3. **Bounded response logging:** The hex dump is bounded to ~256 bytes to prevent log flooding from a pathological server.

4. **Protocol assumptions:** The probe assumes Factorio listens on UDP 34197. A server listening on a different port will simply fail to respond, and the probe will retry until deadline.

## Testing & coverage

**Unit tests:** None yet. The probe is too simple (single UDP send/receive) to benefit from isolated tests; the e2e test is the primary validation.

Once the Factorio wire format is formally documented (e.g., via reverse engineering with evidence), a `protocol/` subpackage can be added with unit tests for packet parsing, following the Minecraft pattern.

### E2E test registration

The test `TestGameServer_FactorioBot_Query` is registered in `test/e2e/buckets.sh`:

- **Heavy set** (`bot-heavy` bucket) — **deliberately never runs in CI** — only on maintainer hand-run with `GAMEPLANE_E2E_GAMES=all`.

Rationale: Factorio boots quickly (no steamcmd, small initial world generation), but the test is classified as heavy until actual CI performance data supports fast-set inclusion. Early testing may reveal boot times or resource contention that justifies heavy-set status.

## Runtime characteristics

### Image Pin

- **Image:** `factoriotools/factorio:stable@sha256:7052b3cca8ca7790f99f4058617d5c8089df544de736b1baa23f2c5f58fb7f48` (pinned to stable channel digest from 2026-07-25).
- **Rationale:** The stable channel tracks Factorio releases but not pre-releases. Pinning the digest ensures hermetic tests and makes the protocol version explicit (2.0.x as of this date). If Factorio's protocol changes in future releases, the digest pin must be updated along with any probe changes.

### Boot Time and Disk

**Configured budgets (not measurements):**
- **Ready timeout budget:** 8 minutes (configured in `factorio_bot_e2e_test.go`). Factorio boots faster than Minecraft or Terraria (no steamcmd, small generated world), but map generation varies with difficulty and world size settings. 8 minutes is conservative.
- **Storage size:** 1Gi (allocated, not necessarily used; the initial world and save file are small).
- **Probe deadline:** 4 minutes (configured in `factorio_bot_e2e_test.go`; how long the in-cluster probe retries before failing).

**Measured (live k3s cluster, 2026-07-25):**
- Date: 2026-07-25
- Environment: Live k3s cluster via the Gameplane operator
- Image: `factoriotools/factorio:stable` (2.0.x, digest 7052b3cca8ca7790f99f4058617d5c8089df544de736b1baa23f2c5f58fb7f48)
- Server state: Running, 2/2 Ready (reached within one minute)
- UDP 34197: No response to an unrecognized datagram (measured; server silently drops the packet as expected).
- TCP 27015 (RCON): Open and accepting connections.
- Conclusion: Factorio exposes no UDP query surface, so TCP RCON acceptance is the only falsifiable depth assertion.

### Protocol Reference

**Factorio multiplayer:** Factorio 2.0.x (stable channel)  
**Wire format:** UDP, 34197  
**Documentation:** None (not publicly available).

**Packet format (unverified):**
The connection request is **inferred only**, not documented:
```
byte[0]:      0x01           # unverified connection request type (guess)
byte[1-8]:    8-byte ID      # unverified session ID (arbitrary)
byte[9+]:     optional       # no further payload sent
```

**Server response:** Opaque. May be:
- A structured rejection or connection offer (bytes are logged for evidence).
- Silent timeout (UDP, no guarantee of response).
- Garbage (port open but not Factorio protocol).

**References (conceptual only, no wire format detail):**
- FFF #149 (Factorio Friday Facts #149): Describes the multiplayer handshake at a high level but does not include packet formats.
- FFF #147 (Factorio Friday Facts #147): Similar conceptual description.
- No official Factorio protocol specification exists.

## Future work

1. **Protocol reverse engineering:** Once Factorio's wire format is documented (e.g., via a community project like `radioegor146/factorio-reverse` for Factorio's join protocol), a proper `protocol/` subpackage can be added with packet parsing and depth escalation to PARTIAL or JOINED.

2. **Depth escalation:** Once the protocol is documented, the probe can attempt to parse the server's response, distinguish rejection reasons, and potentially escalate to PARTIAL (if auth is needed but reachable) or JOINED (if a headless join is possible, e.g., via a scripted RPC interface).

3. **RCON integration:** Factorio's RCON port (TCP 27015) is open but not yet probed. A future enhancement could query RCON to verify it is responsive, escalating confidence that the server is fully initialized.

4. **CI performance measurement:** After the test runs successfully in CI, measure and document actual boot time and resource usage. If consistently fast (< 4 minutes total), consider moving to the fast set.

## References

- **Probe application:** `test/e2e/internal/factorio/app.go`
- **E2E test:** `test/e2e/factorio_bot_e2e_test.go`
- **Shared probe harness:** `test/e2e/internal/probe/` (defined by shared harness agent)
- **Factorio module template:** `modules/factorio/template.yaml` (game configuration, image pin, environment)
- **factoriotools/factorio Docker image:** https://github.com/factoriotools/factorio-docker
- **Factorio Friday Facts #149:** https://factorio.com/blog/post/fff-149 (multiplayer conceptual description)
- **Factorio Friday Facts #147:** https://factorio.com/blog/post/fff-147 (multiplayer conceptual description)
- **CLAUDE.md:** Rule 3 (login privacy) — the probe does not run on pre-auth screens; this is internal testing only.
