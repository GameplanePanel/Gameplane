# Contract: Top 100 Steam Dedicated Server Engine & Protocol Matrix

**Feature**: `015-top-steam-game-modules`  
**Contract Version**: `1.0.0`  
**Status**: Normative  

---

## 1. Engine & Protocol Specifications

This matrix defines the required configurations, port specifications, query protocols, and modding structures for all 26 supported games:

| Module Identifier | App ID | Query Protocol | Game Port | Query Port | Admin Port & Protocol | Storage Mount Path | Save Action |
|---|---|---|---|---|---|---|---|
| `cs2` | 730 | A2S_INFO (UDP) | 27015 UDP | 27015 UDP | 27015 TCP (Source RCON) | `/home/steam/cs2-dedicated` | N/A (Stateless) |
| `palworld` | 2394010 | A2S_INFO (UDP) | 8211 UDP | 27015 UDP | 8212 TCP (REST) / 25575 TCP (RCON) | `/palworld/Pal/Saved` | `Save` |
| `fivem` | N/A | CFX Query (UDP) | 30120 UDP | 30120 UDP | 40120 TCP (txAdmin HTTP) | `/server-data` | `quit` |
| `rust` | 258550 | A2S_INFO (UDP) | 28015 UDP | 28015 UDP | 28016 TCP (WebSocket RCON) | `/serverdata` | `server.save` |
| `project-zomboid` | 380870 | A2S_INFO (UDP) | 16261 UDP | 16261 UDP | 27015 TCP (Source RCON) | `/home/pzuser/Zomboid` | `save` |
| `team-fortress-2` | 232250 | A2S_INFO (UDP) | 27015 UDP | 27015 UDP | 27015 TCP (Source RCON) | `/home/steam/tf-dedicated` | N/A (Stateless) |
| `dayz` | 223350 | A2S_INFO (UDP) | 2302 UDP | 27016 UDP | 2306 UDP (BattlEye RCON) | `/serverdata` | `#shutdown` |
| `farming-simulator-25`| N/A | GIANTS HTTP | 10823 UDP | 10823 UDP | 8080 TCP (Web Admin HTTP) | `/data/My Games/FarmingSimulator2025` | Web Save API |
| `euro-truck-simulator-2`| 1948400 | A2S_INFO (UDP) | 27015 UDP | 27016 UDP | Stdin Console | `/home/steam/.local/share/Euro Truck Simulator 2` | `exit` |
| `garrys-mod` | 4020 | A2S_INFO (UDP) | 27015 UDP | 27015 UDP | None (no console reachable: baked cfg/, no password env, agent rcon-port wiring unfixed) | `/home/steam/gmod-dedicated` | N/A (Stateless, no capabilities block) |
| `mount-and-blade-2-bannerlord`| 1863440 | A2S_INFO (UDP) | 7210 UDP | 7211 UDP | Stdin Console | `/serverdata` | N/A (Match-based) |
| `terraria` | 105600 | Custom TCP | 7777 TCP | 7777 TCP | Stdin / REST | `/root/.local/share/Terraria/Worlds` | `save` |
| `7-days-to-die` | 294420 | A2S_INFO (UDP) | 26900 UDP | 26900 UDP | None (TelnetPassword lives in serverconfig.xml under serverfiles/, unreachable from the world-saves mount) | `/home/sdtduser/.local/share/7DaysToDie` | N/A (no capabilities block; LinuxGSM user.sh traps SIGINT/SIGTERM) |
| `tmodloader` | 1281930 | Custom TCP | 7777 TCP | 7777 TCP | Stdin Console | `/root/.local/share/Terraria/tModLoader` | `save` |
| `beammp` | N/A | Custom TCP/UDP | 30814 UDP | 30814 UDP | 30814 TCP (Auth/CLI) | `/server/Root` | N/A |
| `ark-survival-ascended`| 2430930 | A2S_INFO (UDP) | 7777 UDP | 7777 UDP | 27020 TCP (Source RCON) | `/serverdata/ShooterGame/Saved` | `SaveWorld` |
| `left-4-dead-2` | 222860 | A2S_INFO (UDP) | 27015 UDP | 27015 UDP | 27015 TCP (Source RCON) | `/home/steam/l4d2-dedicated` | N/A (Stateless) |
| `factorio` | Standalone | Custom UDP Ping | 34197 UDP | 34197 UDP | 27015 TCP (RCON) | `/factorio/saves` | `/server-save` |
| `dont-starve-together`| 343050 | A2S_INFO (UDP) | 10999 UDP | 27018 UDP | Stdin Lua Console | `/root/.klei/DoNotStarveTogether` | `c_save()` |
| `valheim` | 896660 | A2S_INFO (UDP) | 2456 UDP | 2457 UDP | In-game Admin | `/config/worlds_local` | `save` |
| `satisfactory` | 1690800 | HTTPS TLS API | 7777 UDP | 7777 UDP | 7777 TCP (HTTPS TLS API) | `/home/steam/.config/Epic/FactoryGame/Saved` | `SaveGame` |
| `the-isle` | 412680 | A2S_INFO (UDP) | 7777 UDP | 7778 UDP | 8888 TCP (Source RCON) | `/serverdata/TheIsle/Saved` | `save` |
| `ark-survival-evolved`| 376030 | A2S_INFO (UDP) | 7777 UDP | 27015 UDP | 27020 TCP (Source RCON) | `/serverdata/ShooterGame/Saved` | `SaveWorld` |
| `arma-reforger` | 1874900 | A2S_INFO (UDP) | 2001 UDP | 17777 UDP | Stdin Console | `/home/steam/.local/share/ArmaReforgerServer` | `save` |
| `hell-let-loose` | 731790 | A2S_INFO (UDP) | 7787 UDP | 27165 UDP | 22222 TCP (UE4 RCON) | `/serverdata/HLL/Saved` | N/A (Match-based) |
| `squad` | 403240 | A2S_INFO (UDP) | 7787 UDP | 27165 UDP | 21114 TCP (Squad RCON) | `/serverdata/Squad/Saved` | N/A (Match-based) |
