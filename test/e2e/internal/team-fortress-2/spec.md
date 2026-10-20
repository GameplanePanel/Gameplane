# team-fortress-2 — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_TeamFortress2Bot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=team-fortress-2 make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (A2S query response received)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/team-fortress-2`  
**Dependencies:** `test/e2e/internal/protocol/{a2sproto,sourceproto,joindepth}`  
**Heavy game:** Yes (>15GB disk download)

## Purpose

End-to-end test harness for Team Fortress 2, proving that a Gameplane-managed TF2 dedicated server is reachable and answering Source engine A2S_INFO queries with live server status.

## Responsibilities

1. **A2S Query Protocol:** Issue standard Source engine A2S queries over UDP to inspect server name, map, and player counts.
2. **Diagnostic Handshake:** Optionally attempt Source protocol challenge/connect.
3. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
