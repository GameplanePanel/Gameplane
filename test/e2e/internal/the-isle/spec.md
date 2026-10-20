# the-isle — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_TheIsleBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=the-isle make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (A2S query response received)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/the-isle`  
**Dependencies:** `test/e2e/internal/protocol/{a2sproto,joindepth}`  
**Heavy game:** Yes (>20GB SteamCMD download)

## Purpose

End-to-end test harness for The Isle, verifying that a Gameplane-managed dedicated server answers A2S_INFO query packets on port 7778.

## Responsibilities

1. **A2S Query Protocol:** Issue standard Steam A2S query packets over UDP.
2. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
