# nuclear-option — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_NuclearOptionBot_Query
- **Bucket**: bot-heavy
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-heavy` bucket and does not run by default in CI. To run it against an operator-provided cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=nuclear-option make test-e2e-keep
```

**Status:** pre-v1; probe depth targeted: **QUERY** (A2S query response on UDP 7778). Not yet hand-run: that the server answers Steam A2S on 7778 is UNVERIFIED (spec 002 confirms only that 7778 is bound as the query port).  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/nuclear-option`  
**Dependencies:** `test/e2e/internal/protocol/{a2sproto,joindepth}`  
**Heavy game:** Yes (8–16 GiB memory, ~900 MB SteamCMD download on first boot)

## Purpose

End-to-end test harness for Nuclear Option, verifying that a Gameplane-managed Nuclear Option dedicated server responds to Steam A2S_INFO queries on its query port (7778). The join handshake is not probed: the server does not bind UDP 7777 and joins route through Steam game-server networking, so a JOINED test waits on a real-client packet capture (spec 002 FR-004 amendment, tasks T058–T060).

## Responsibilities

1. **A2S Query Protocol:** Issues standard Steam A2S query packets over UDP to discover server name, map, and players.
2. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
