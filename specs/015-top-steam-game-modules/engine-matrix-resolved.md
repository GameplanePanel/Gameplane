# Resolved Engine, Protocol & CI Classification Matrix

**Feature**: `015-top-steam-game-modules`  
**Status**: Authoritative Reference for Phase 3-7 Implementation  
**Dependencies**: Derived from `specs/015-top-steam-game-modules/contracts/engine-matrix-contract.md` (T009) and `specs/015-top-steam-game-modules/OPEN-DECISIONS.md` (T005, T012).

---

## 1. Per-Module Resolved Configuration Table

This table is the single authoritative reference for the 26 in-scope modules.
Protocol values are drawn from: `source | telnet | websocket | battleye | satisfactory | palworld | nuclearoption | rest | cli | none`.
No module is assigned `cli` pending maintainer ruling on its semantics (T005).

| Module Identifier | Status | Real `rcon.protocol` | `consoleMode` | Declared Ports (`spec.ports`) | `spec.storage.mountPath` | `spec.capabilities.lifecycle.stop` | CI Classification | CI Classification Justification |
|---|---|---|---|---|---|---|---|---|
| `cs2` | Existing | `source` | unset | `game` (27015/UDP), `rcon` (27015/TCP), `gotv` (27020/UDP) | `/home/steam/cs2-dedicated` | `["quit"]` | `bot-heavy` | 60Gi disk required; exceeds GitHub runner disk capacity |
| `palworld` | Existing | `palworld` | unset | `game` (8211/UDP), `query` (27015/UDP), `rest-api` (8212/TCP) | `/palworld` (OD 7.1) | `["save", "shutdown 1"]` | `bot-heavy` | 20Gi storage + multi-GB first boot download |
| `fivem` | New | `rest` | `rcon` | `game` (30120/UDP), `http` (30120/TCP), `txadmin` (40120/TCP) | `/server-data` | `["quit"]` | `bot-heavy` | >5Gi storage, embedded database + txAdmin supervision |
| `rust` | Existing | `websocket` | `rcon` | `game` (28015/UDP), `rcon` (28016/TCP) | `/steamcmd/rust` (OD 7.2) | `["server.save", "quit"]` | `bot-heavy` | 10Gi storage + 4Gi memory requests |
| `project-zomboid` | Existing | `source` (OD 2) | unset | `game` (16261/UDP), `direct` (16262/UDP), `rcon` (27015/TCP) | `/home/steam/Zomboid` (OD 7.3) | `["save", "quit"]` | `bot-heavy` | 15Gi storage + 4Gi memory requests |
| `team-fortress-2` | New | `source` | `rcon` | `game` (27015/UDP), `rcon` (27015/TCP), `sourcetv` (27020/UDP) | `/home/steam/tf-dedicated` | `["quit"]` | `bot-heavy` | SteamCMD download (>15Gi storage) |
| `dayz` | Existing | `battleye` | `rcon` | `game` (2302/UDP), `query` (27015/UDP), `rcon` (2305/UDP) | `/data` (OD 7.4) | `[]` (OD T086) | `bot-heavy` | 40Gi storage + >10GB SteamCMD download |
| `farming-simulator-25` | New | `rest` | `none` | `game` (10823/UDP), `web` (8080/TCP) | `/data/My Games/FarmingSimulator2025` | `["save"]` | `bot-heavy` | Headless Wine/Proton layer + >20Gi storage |
| `euro-truck-simulator-2` | New | `none` | `pty` | `game` (27015/UDP), `query` (27016/UDP) | `/home/steam/.local/share/Euro Truck Simulator 2` | `["exit"]` | `bot-heavy` | SteamCMD download (>10Gi storage) |
| `garrys-mod` | Existing | `none` (T009) | unset | `game` (27015/UDP), `game-tcp` (27015/TCP), `client` (27005/UDP) | `/home/gmod/server/garrysmod/data` (OD 7.5) | `[]` (T009, no capabilities) | `bot-fast` | Pre-baked container image; fits in fast CI bucket |
| `mount-and-blade-2-bannerlord` | New | `none` | `pty` | `game` (7210/UDP), `query` (7211/UDP) | `/data` | `[]` (Match-based) | `bot-heavy` | SteamCMD download (>15Gi storage) |
| `terraria` | Existing | `none` | `pty` | `game` (7777/TCP) | `/opt/terraria/config` (OD 7.6) | `["exit"]` | `bot-fast` | Lightweight .NET runtime, <1Gi storage |
| `7-days-to-die` | Existing | `none` (T009) | unset | `game` (26900/TCP), `game-udp` (26900/UDP), `game2` (26901/UDP), `game3` (26902/UDP), `telnet` (8081/TCP) | `/home/sdtdserver/.local/share/7DaysToDie` (OD 7.12) | `[]` (T009, no capabilities) | `bot-heavy` | 55Gi combined storage requirement |
| `tmodloader` | New | `none` | `pty` | `game` (7777/TCP) | `/opt/terraria/config` | `["exit"]` | `bot-heavy` | Lightweight Terraria-based mod runtime, 4Gi storage |
| `beammp` | New | `none` | `pty` | `game` (30814/UDP), `auth` (30814/TCP) | `/server/Root` | `[]` | `bot-heavy` | Standalone C++ binary, 2Gi storage, no SteamCMD |
| `ark-survival-ascended` | Existing | `source` | unset | `game` (7777/UDP), `peer` (7778/UDP), `rcon` (27020/TCP) | `/home/gameserver` (OD 7.7) | `["SaveWorld", "DoExit"]` | `bot-heavy` | 30Gi persistent storage requirement |
| `left-4-dead-2` | New | `source` | `rcon` | `game` (27015/UDP), `query` (27015/UDP), `rcon` (27015/TCP) | `/home/steam/l4d2-dedicated` | `["quit"]` | `bot-heavy` | SteamCMD download (>12Gi storage) |
| `factorio` | Existing | `source` (OD 2) | `pty` | `game` (34197/UDP), `rcon` (27015/TCP) | `/factorio` (OD 7.8) | `["/server-save"]` | `bot-heavy` | Fast boot and lightweight memory/storage |
| `the-isle` | New | `source` | `rcon` | `game` (7777/UDP), `query` (7778/UDP), `rcon` (8888/TCP) | `/data` | `["save"]` | `bot-heavy` | SteamCMD download, UE4 (>20Gi storage) |
| `dont-starve-together` | Existing | `none` | `pty` | `game` (10999/UDP), `query` (27018/UDP), `caves` (11000/UDP), `steam1` (12346/UDP), `steam2` (12347/UDP) | `/data` (OD 7.9) | `["c_save()", "c_shutdown(true)"]` (OD 4) | `bot-heavy` | Multi-shard master/caves overhead |
| `valheim` | Existing | `none` | `pty` | `game` (2456/UDP), `game2` (2457/UDP), `game3` (2458/UDP), `status` (80/TCP) | `/config` (OD 7.10) | `["save"]` | `bot-heavy` | >12GB SteamCMD download on first boot |
| `satisfactory` | Existing | `satisfactory` | `rcon` | `game` (7777/UDP), `game-tcp` (7777/TCP), `messaging` (8888/TCP) | `/config` (OD 7.11) | `["SaveGame"]` | `bot-heavy` | 25Gi storage + multi-GB SteamCMD download |
| `ark-survival-evolved` | New | `source` | `rcon` | `game` (7777/UDP), `query` (27015/UDP), `rcon` (27020/TCP) | `/data` | `["SaveWorld", "DoExit"]` | `bot-heavy` | SteamCMD download, cluster travel (>30Gi storage) |
| `arma-reforger` | New | `none` | `pty` | `game` (2001/UDP), `query` (17777/UDP) | `/reforger` | `["save"]` | `bot-heavy` | SteamCMD download, Enfusion engine (>25Gi storage) |
| `hell-let-loose` | New | `source` | `rcon` | `game` (7787/UDP), `query` (27165/UDP), `rcon` (22222/TCP) | `/serverdata/HLL/Saved` | `[]` (Match-based) | `bot-heavy` | SteamCMD download (>30Gi storage) |
| `squad` | New | `source` | `rcon` | `game` (7787/UDP), `query` (27165/UDP), `rcon` (21114/TCP) | `/data` | `[]` (Match-based) | `bot-heavy` | SteamCMD download (>35Gi storage) |

---

## 2. 13 New Modules Classification Summary

- **`bot-fast` (0 modules)**: none of the 13 new modules runs in CI's `bot-fast` bucket.
- **`bot-heavy` (13 modules)**:
  - `fivem`, `team-fortress-2`, `farming-simulator-25`, `euro-truck-simulator-2`, `mount-and-blade-2-bannerlord`, `tmodloader`, `beammp`, `left-4-dead-2`, `the-isle`, `ark-survival-evolved`, `arma-reforger`, `hell-let-loose`, `squad` (`test/e2e/buckets.sh`, `bucket_bot_heavy`).
  - All need multi-GB downloads, large volumes or emulation layers (Wine/Proton) beyond a GitHub Actions runner's disk and memory, so the bucket is hand-run only.

## 3. Distinction Between `fastGameSet` and CI Buckets

- `fastGameSet` (`test/e2e/gamebot_helpers_e2e_test.go:29`) controls the **local / default developer filter** when `GAMEPLANE_E2E_GAMES` is unset.
- `bucket_bot_fast` and `bucket_bot_heavy` in `test/e2e/buckets.sh` control **CI runner job segmentation**.
- For the 13 new modules:
  - `tmodloader` and `beammp` are in `fastGameSet` (`test/e2e/gamebot_helpers_e2e_test.go`) for local runs, but are bucketed in `bucket_bot_heavy`, not `bucket_bot_fast`.
  - The other 11 are in `heavyGameSet` and bucketed in `bucket_bot_heavy` with CI-exclusion justification comments.

Table values above (protocol, `consoleMode`, ports, mount path, stop, CI bucket) were re-read from the shipped `modules/<game>/template.yaml` files and `test/e2e/buckets.sh` on 2026-10-11 (T143); the templates are authoritative. "unset" means the template leaves `consoleMode` to its default. The justification column is unchanged.
