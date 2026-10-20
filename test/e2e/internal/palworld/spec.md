# palworld — E2E Probe Specification

## Coverage Status

- **Status**: blocked-doc
- **Depth**: QUERY
- **Test**: TestGameServer_PalworldBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: Unreal Engine 5; stateless-connect handshake under reverse-engineering, Steam authentication integration incomplete
- **Blocker Class**: documentation

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster (not a local machine), use:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=palworld make test-e2e-keep
```

A successful run is the only event that licenses updating `Last Verified` to the current date.

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/palworld`  
**Dependencies:** stdlib only (Go 1.25+); tested against Palworld via thijsvanloef/palworld-server-docker:latest

## Purpose

End-to-end test harness for Palworld dedicated server, proving that a Gameplane-managed Palworld server is not merely "Running" in Kubernetes but genuinely playable. The harness:

1. Implements the Steam A2S query protocol client to probe the query port.
2. Provides a probe application (`app.go`) that runs as a Kubernetes Job inside the test cluster.
3. Connects to the query port via the cluster network (Service DNS) rather than `kubectl port-forward`, exercising the real network path.
4. Verifies the server answers A2S queries and reports player capacity.

This is part of the game-bot test suite and demonstrates that the server is actively running and discoverable.

## Responsibilities

1. **Protocol client:** Implement/reuse the Steam A2S query protocol for discovering server info.
2. **Query probing:** Verify the server responds to A2S_INFO on port 27015.
3. **Retrying:** The probe retries queries because the server may be bootstrapping — it answers queries once world initialization is complete.
4. **Exit signaling:** Report the join depth reached (QUERY on success) so the test harness can assert the expected outcome.

## Non-goals / boundaries

- Does not attempt to join the game (no Unreal protocol client).
- Does not authenticate with the REST admin API (requires generated admin password).
- Does not measure or test the game's actual gameplay.
- Does not supply Steam credentials or Epic Online Services authentication.

## Directory & package layout

```
test/e2e/internal/palworld/
├── app.go                      # Probe entry point (package main)
└── spec.md                     # This file
```

- **`app.go`** — the main function that runs as a Kubernetes Job. Imports the protocol package and uses the shared `probe` package for retry logic and test framework integration.
- **Protocol client** — reuses the shared A2S client from `test/e2e/internal/protocol/a2sproto`.

## External interface / contracts

### Probe Configuration (from GameTemplate)

Palworld template declares (modules/palworld/template.yaml):
- **Query port:** UDP 27015 (Steam A2S)
- **Game port:** UDP 8211 (Unreal Engine protocol, undocumented)
- **REST API port:** TCP 8212 (admin control, requires HTTP Basic auth)
- **Admin password:** generated operator secret, passed via ADMIN_PASSWORD env var

The probe measures only the query port depth.

### A2S Protocol

**Reference:** https://developer.valvesoftware.com/wiki/Server_queries

A2S_INFO request (UDP, connectionless):
- Send type 0x54 with optional challenge
- Modern servers reply with challenge response (0x41)
- Resend with challenge appended

A2S_INFO response structure (after header 0xFFFFFFFF 0x49):
- Protocol version (1 byte)
- Server name (null-terminated string)
- Map name (null-terminated string)
- Folder name (null-terminated string)
- Game description (null-terminated string)
- App ID (2 bytes LE)
- Players online (1 byte)
- Max players (1 byte)
- ... (additional fields not captured by probe)

This is a standard, citable protocol with no fabrication. Implementation lives in `test/e2e/internal/protocol/a2sproto` and is shared across multiple games (garrys-mod, cs2, palworld).

## Depth Measurement

**Measured Depth:** QUERY

**Evidence:** Palworld's module template (`modules/palworld/template.yaml`) declares `QUERY_PORT=27015` and exposes it as UDP port "query". A successful A2S_INFO query on this port proves the server is running and discoverable.

**Why not deeper?**

- **PARTIAL (protocol handshake):** Palworld's game protocol is Unreal Engine networking, which is not publicly documented. No official client library or specification exists in the open; joining requires Epic/Steam identity federation.
- **JOINED (player acceptance):** Same as above; no headless join without interactive auth.
- **REST API (8212):** Requires HTTP Basic auth with the admin password. The operator generates this password and supplies it via ADMIN_PASSWORD env var to the pod, but the probe (running in a separate container) has no mechanism to retrieve it. Measuring a 401 would be a secondary indication but is not counted as join depth since authentication is operator-controlled, not server-capability controlled.

**Configured budgets:**

| Measurement | Value | Notes |
|---|---|---|
| Readiness probe target | REST API (TCP 8212) | Module uses TCP socket probe on port rest-api for startup/readiness/liveness |
| Probe deadline | 4 minutes | Standard probe deadline from probe harness |
| Retry interval | 3 seconds | Standard retry interval from probe harness |
| Probe timeout | 15 seconds | A2S query timeout |

**No measurements:** Storage, memory, boot time, download size (first steamcmd boot is ~5GB; subsequent runs are much faster). These are infrastructure-dependent and not measured by the probe.

## Constraints

Palworld is in the **heavy set** and **never runs in CI**. Run it manually:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=palworld make test-e2e-keep
```

First boot downloads several GB via steamcmd; see module template's startup probe budget (120 failures × 15 seconds = 30 minutes).

## References

- **A2S query protocol:** https://developer.valvesoftware.com/wiki/Server_queries (Valve's authoritative source)
- **Palworld module:** `modules/palworld/template.yaml`
- **A2S client implementation:** `test/e2e/internal/protocol/a2sproto/a2s.go`
- **Probe harness:** `test/e2e/internal/probe/probe.go`
- **E2E test:**`test/e2e/palworld_bot_e2e_test.go`
- **Buckets:** `test/e2e/buckets.sh` (Palworld in `bot-heavy` bucket)
