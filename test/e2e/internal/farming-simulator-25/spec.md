# farming-simulator-25 — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_FarmingSimulator25Bot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=farming-simulator-25 make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (GIANTS web portal HTTP response)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/farming-simulator-25`  
**Dependencies:** stdlib only  
**Heavy game:** Yes (>30GB disk download, headless Wine/Proton)

## Purpose

End-to-end test harness for Farming Simulator 25, verifying that the dedicated server's GIANTS web management portal is responding to HTTP status queries within the cluster.

## Responsibilities

1. **HTTP Query Protocol:** Query the GIANTS web admin interface on port 8080 to confirm the application server is responsive.
2. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
