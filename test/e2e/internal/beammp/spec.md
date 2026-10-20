# beammp — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_BeamMPBot_Query
- **Bucket**: bot-fast
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-fast` bucket and can run in CI against a test cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=beammp make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (BeamMP TCP handshake success)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/beammp`  
**Dependencies:** stdlib only  
**Heavy game:** No (<5Gi storage, fast container startup)

## Purpose

End-to-end test harness for BeamMP (BeamNG.drive multiplayer), verifying that a Gameplane-managed BeamMP dedicated server accepts network connections on port 30814.

## Responsibilities

1. **Network Handshake:** Connect over TCP to port 30814 to confirm server availability and bridge listener state.
2. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
