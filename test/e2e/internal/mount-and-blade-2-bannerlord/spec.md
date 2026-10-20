# mount-and-blade-2-bannerlord — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_MountAndBlade2BannerlordBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=mount-and-blade-2-bannerlord make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (A2S query response received)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/mount-and-blade-2-bannerlord`  
**Dependencies:** `test/e2e/internal/protocol/{a2sproto,joindepth}`  
**Heavy game:** Yes (>30GB SteamCMD download, headless Wine)

## Purpose

End-to-end test harness for Mount & Blade II: Bannerlord, verifying that a Gameplane-managed Bannerlord dedicated server responds to Steam A2S_INFO queries on its query port (7211).

## Responsibilities

1. **A2S Query Protocol:** Issue standard Steam A2S queries over UDP to inspect server metadata and availability.
2. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
