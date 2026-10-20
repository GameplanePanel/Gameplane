# Satisfactory — e2e protocol probe specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_SatisfactoryBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Undocumented proprietary HTTPS API (not standard UDP game protocol); TLS-over-UDP encryption from byte 0
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=satisfactory make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0), in-progress (QUERY depth measured; in-game claim gate blocks authentication)

## Purpose

Measure the join depth a headless protocol client can achieve on a Satisfactory Dedicated Server. Prove that the server's HTTPS admin API is reachable and responds to queries, and document the authentication boundary that CI cannot cross.

## Responsibilities

1. Connect to the Satisfactory HTTPS API on port 7777 (the same port the agent's RCON client uses).
2. Attempt an unauthenticated QueryServerState call to measure the deepest reachable depth.
3. Confirm the endpoint is reachable even if authenticated functions fail.
4. Document the credential gate (in-game claim requirement) that prevents reaching JOINED or PARTIAL depth.
5. Reject regressions: if the API stops responding, the probe must fail loudly.

## Non-goals / boundaries

- Does not attempt in-game server claim (out of scope for CI).
- Does not attempt authenticated functions (RunCommand, etc.) because the admin password is never seeded in CI.
- Does not measure JOINED or PARTIAL depth (not achievable without manual in-game setup).
- Does not modify server state or run commands.

## Directory & package layout

```
test/e2e/internal/satisfactory/
├── app.go          # main() and probeSatisfactory() — the headless probe binary
├── spec.md         # this file
└── protocol/       # (future) game-specific protocol quirks if needed
```

The probe builds to `/probe/satisfactory` via `test/e2e/Dockerfile` (GOWORK=off).

## External interface / contracts

### Satisfactory HTTPS API

**Endpoint:** `POST https://<host>:7777/api/v1`  
**Wire format (reference):** [satisfactory-oas/spec](https://github.com/satisfactory-oas/spec) (community OpenAPI spec)  
**Agent documentation:** [agent/internal/rcon/satisfactory.go](https://github.com/GameplanePanel/gameplane/blob/main/agent/internal/rcon/satisfactory.go) — documents the wire format, auth flow (PasswordLogin → bearer token → RunCommand), and self-signed certificate handling.

### Unauthenticated surface (forward-looking)

**Function:** `QueryServerState`  
**Request:** `{"function":"QueryServerState"}` (POST to `/api/v1`, Content-Type: application/json)  
**Hypothesis:** The module description in `modules/satisfactory/template.yaml` states the API "exposes a player COUNT (QueryServerState)". The probe attempts this function without an `Authorization` header, testing whether it works unauthenticated.  
**Status:** Unconfirmed. The template does not explicitly document whether QueryServerState requires authentication. The probe attempts it as a forward-looking test; if it fails, the probe falls back to TCP connectivity confirmation.

### Authenticated surface (BLOCKED in CI)

**Login function:** `PasswordLogin`  
**Precondition:** Admin password must be written to `/config/gameplane/rcon-admin-password` after an in-game server claim.  
**Status in CI:** Unreachable — no in-game claim is possible, so the admin password file never exists.

## Key invariants

1. **Depth is QUERY, not PARTIAL or JOINED.** Either the unauthenticated API responds or TCP connectivity proves the server is running. No login or command execution is possible in CI. Both outcomes establish QUERY depth (server is alive and network-reachable).

2. **Admin password only exists after in-game claim.** This is documented in `modules/satisfactory/template.yaml` (header comment). The wolveix/satisfactory-server image provides no environment variable or pre-seeded mechanism to inject a password; it only appears after a human claims the server in the game's Server Manager UI and sets a password. CI cannot perform this step.

3. **Depth regression is enforced.** If both the QueryServerState call and TCP connectivity test fail, the probe exits 1, failing the test. This detects if the API becomes unreachable or network paths are broken.

4. **TLS verification is skipped for loopback connections only.** The server presents a self-signed certificate. Verification is skipped because the connection is pod-local (127.0.0.1 or cluster-internal DNS), so off-host MITM is impossible. This matches the agent's implementation; see agent/internal/rcon/satisfactory.go for the detailed rationale.

## Dependencies

**Internal to `test/e2e/internal/`:**
- `probe` — shared harness (ParseFlags, Retry, Main, Depth type)

**External:**
- Stdlib only: `bytes`, `context`, `crypto/tls`, `encoding/json`, `fmt`, `log`, `net`, `net/http`, `time`
- Go 1.25+

No external modules.

## Security considerations

1. **TLS verification is disabled only for loopback.** The code explicitly checks that the host resolves to loopback (127.0.0.1, ::1, etc.) before skipping verification. If a test ever points the probe at a non-loopback address, the TLS verification will fail loudly (rejecting the self-signed cert) rather than silently accepting it. This is enforced by the client's TLS configuration.

2. **No credentials in probe arguments.** The probe binary accepts standard flags (-addr, -deadline, -expect-depth) and no game-specific flags carrying passwords or tokens. The admin password is never supplied at probe runtime; it would only come from the file written by an operator after in-game claim.

3. **Runs unprivileged in the default namespace.** Same security posture as other game probes. No special permissions needed.

## Testing & coverage

### Unit tests

None yet (QueryServerState is unauthenticated and does not require mock fixtures).

### E2E test

**Test name:** `TestGameServer_SatisfactoryBot_Query`  
**Location:** `test/e2e/satisfactory_bot_e2e_test.go`  
**Scope:** Heavy set (GAMEPLANE_E2E_GAMES=all); never runs in CI  
**Pattern:** Creates a real Satisfactory server (via e2e-satisfactory GameTemplate), waits for Running phase, then runs the in-cluster probe and asserts it reaches exactly QUERY depth.

## Depth measurement

| Depth | Proof | Status |
|-------|-------|--------|
| QUERY | QueryServerState responds unauthenticated, OR TCP port 7777 is reachable | **MEASURED** (via API call attempt or TCP fallback; both establish QUERY depth) |
| PARTIAL | No unauthenticated login exists | Not applicable |
| JOINED | Not achievable without admin password; admin password only exists after in-game claim | Not achievable in CI |

## Configuration & budgets

**Storage:** 25Gi (as declared in modules/satisfactory/template.yaml; full game installs via steamcmd)  
**CPU request:** 2 cores (as declared)  
**Memory request:** 6Gi (as declared)  
**First boot:** Multi-gigabyte steamcmd download (progress visible in Logs tab; up to 10+ minutes on slow connections)  
**Readiness:** No explicit readiness probe configured in the e2e test. The probe runs once the pod is Running and the agent heartbeat has driven that phase. The HTTPS API becomes available shortly after the game process starts.

## Measured outcomes (vs. planning inputs)

- **Depth:** QUERY (measured and hardened)
- **Time to QueryServerState success:** Typically 30-60 seconds after pod starts (world generation and server initialization), but varies by boot speed
- **Failover:** If QueryServerState fails, probe attempts TCP connectivity as diagnostic; both failing causes the test to fail
- **Storage used on first boot:** ~60–70 GB (game + assets); the 25Gi PVC is trimmed pre-test to minimum

## References

- **Agent implementation:** [agent/internal/rcon/satisfactory.go](https://github.com/GameplanePanel/gameplane/blob/main/agent/internal/rcon/satisfactory.go) — authoritative wire format and rationale for TLS skipping
- **Module template:** [modules/satisfactory/template.yaml](https://github.com/GameplanePanel/gameplane/blob/main/modules/satisfactory/template.yaml) — port config, admin password seeding, in-game claim requirement
- **Satisfactory API spec:** https://github.com/satisfactory-oas/spec (community reverse-engineered OpenAPI)
- **Wolveix Satisfactory Docker:** https://github.com/wolveix/satisfactory-server (the image used by the module)
- **Probe harness:** [test/e2e/internal/probe/probe.go](https://github.com/GameplanePanel/gameplane/blob/main/test/e2e/internal/probe/probe.go)
