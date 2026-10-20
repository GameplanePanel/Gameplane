# fivem — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_FiveMBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=fivem make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (HTTP info.json query success)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/fivem`  
**Dependencies:** stdlib only  
**Heavy game:** Yes (>5Gi storage and embedded database supervision)

## Purpose

End-to-end test harness for FiveM, proving that a Gameplane-managed FiveM server is genuinely reachable and speaking its real server discovery/query protocol (`/info.json` endpoint).

## Responsibilities

1. **HTTP Query Protocol:** Query the FiveM server discovery endpoint (`/info.json`) to confirm server liveness and capability reporting.
2. **Retrying:** Retry with deadline until the server completes its internal initialization and answers queries.
3. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
