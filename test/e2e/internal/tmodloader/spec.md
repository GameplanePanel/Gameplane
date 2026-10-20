# tmodloader — E2E Probe Specification

## Coverage Status

- **Status**: active
- **Depth**: QUERY
- **Test**: TestGameServer_TModLoaderBot_Query
- **Bucket**: bot-fast
- **Last Verified**: —
- **Blocker**: None
- **Blocker Class**: none

## On-Demand Invocation

This test is part of the `bot-fast` bucket and can run in CI against a test cluster:

```bash
GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context-name> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=tmodloader make test-e2e-keep
```

**Status:** pre-v1; probe depth measured: **QUERY** (Terraria TCP handshake response)  
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e/internal/tmodloader`  
**Dependencies:** `test/e2e/internal/terraria/terrariaproto`, `test/e2e/internal/protocol/joindepth`  
**Heavy game:** No (<5Gi storage, fast container startup)

## Purpose

End-to-end test harness for tModLoader, verifying that a Gameplane-managed tModLoader dedicated server accepts Terraria-family binary protocol handshake connections over TCP port 7777.

## Responsibilities

1. **Protocol Handshake:** Connect over TCP port 7777 and send a `ConnectRequest` packet to verify server availability.
2. **Verdict Reporting:** Emit standard `VERDICT` output conforming to `joindepth.ProbeVerdict`.
