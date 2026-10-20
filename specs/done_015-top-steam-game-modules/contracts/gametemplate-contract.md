# Contract: GameTemplate CRD & Lifecycle Mechanics

**Feature**: `015-top-steam-game-modules`  
**Contract Version**: `1.0.0`  
**Status**: Normative  

---

## 1. Port Declaration Contract

Every port defined in `spec.ports` MUST satisfy:

1. **Named Convention**: Standardized names (`game`, `query`, `rcon`, `web`, `beacon`, `txadmin`, `telnet`).
2. **Protocol Validation**: Explicit `UDP` or `TCP` protocol identifier.
3. **Collision-Free Defaults**: Default container port allocations must match the game's upstream standard to simplify client direct connections.

---

## 2. Remote Administration & RCON Contract

Supported remote administration protocols in `spec.rcon.protocol`:

| Protocol Key | Handshake / Transport | Supported Games | Example Port |
|---|---|---|---|
| `source` | Valve Source RCON (TCP) | CS2, TF2, L4D2, GMod, Squad, The Isle, ARK, HLL | `27015` |
| `battleye` | BattlEye RCON (UDP) | DayZ | `2306` |
| `websocket` | Rust WebSocket RCON (TCP/WS) | Rust | `28016` |
| `satisfactory` | Satisfactory HTTPS TLS API | Satisfactory | `7777` |
| `palworld` | Palworld REST API | Palworld | `8212` |
| `nuclearoption` | Nuclear Option JSON-RPC | Nuclear Option | `7778` |
| `rest` | Generic HTTP REST API | FiveM (txAdmin), Farming Simulator 25 (Web Admin) | `40120` / `8080` |
| `telnet` | Plaintext Telnet Socket (TCP) | 7 Days to Die | `8082` |
| `cli` | Stdin/PTY Console (Agent-driven) | Open pending OPEN-DECISIONS.md (T005); unassigned | N/A |
| `none` | No remote RCON protocol | Games utilizing consoleMode: pty or stateless | N/A |

---

## 3. Lifecycle Stop Action Contract

For games supporting persistence or graceful shutdown sequences, `spec.capabilities.lifecycle.stop` MUST be declared as a 1-16 item array of plain command strings executed in sequence prior to container termination:

```yaml
capabilities:
  lifecycle:
    stop:
      - "<pre-shutdown warning or save command>"
      - "<engine save or shutdown command>"
```

Examples:
- Unreal Engine / Source: `["SaveWorld"]`
- Rust: `["server.save", "server.writecfg"]`
- Factorio: `["/server-save"]`
- Terraria: `["save", "exit"]`
- Don't Starve Together: `["c_save()", "c_shutdown(true)"]`
