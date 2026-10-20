# T006 catalog: modules, mod registries, RBAC roles, Go modules

Scope: MOD = `/home/user/module`, GP = `/home/user/Gameplane`. Read-only research; no tests or builds run.

## (a) Game modules in MOD

Each top-level directory containing a `module.yaml` is a module. Display name = `displayName:` in `module.yaml` (line 4 in every file checked; `grep -m1 '^displayName:'`).

Count: **29** (29 directories with `module.yaml`; checked with `ls -d */ | wc -l` at MOD 91b64cc)

| Dir | displayName |
|---|---|
| 7-days-to-die | 7 Days to Die |
| ark-survival-ascended | ARK: Survival Ascended |
| ark-survival-evolved | ARK: Survival Evolved |
| arma-reforger | Arma Reforger |
| beammp | BeamMP |
| cs2 | Counter-Strike 2 |
| dayz | DayZ |
| dont-starve-together | Don't Starve Together |
| enshrouded | Enshrouded |
| euro-truck-simulator-2 | Euro Truck Simulator 2 |
| factorio | Factorio (Headless) |
| farming-simulator-25 | Farming Simulator 25 |
| fivem | FiveM |
| garrys-mod | Garry's Mod |
| left-4-dead-2 | Left 4 Dead 2 |
| minecraft-java | Minecraft (Java Edition) |
| mount-and-blade-2-bannerlord | Mount & Blade II: Bannerlord |
| nuclear-option | Nuclear Option |
| palworld | Palworld (Dedicated) |
| project-zomboid | Project Zomboid |
| rust | Rust |
| satisfactory | Satisfactory (Dedicated) |
| squad | Squad |
| team-fortress-2 | Team Fortress 2 |
| terraria | Terraria |
| the-isle | The Isle |
| tmodloader | tModLoader |
| v-rising | V Rising |
| valheim | Valheim (Dedicated) |

Hell Let Loose: **absent**. A case-insensitive recursive search of `/home/user/module` for `hell let loose` / `helllet` returned no matches.

Non-module top-level files in MOD (not counted): `build.sh`, `validate.py`, `test-build-sh.sh`, `test-validate-py.sh`.

## (b) Mod registries implemented in code

Canonical list: `api/internal/registry/registry.go:243-281`, the `switch cfg.Provider` in `func (s *Set) For` (`registry.go:241`). Ten providers:

1. modrinth (`registry.go:243`)
2. thunderstore (`registry.go:245`, needs Community)
3. curseforge (`registry.go:250`, needs CurseForgeGameID and key)
4. hangar (`registry.go:259`)
5. factorio (`registry.go:261`)
6. spigot (`registry.go:263`)
7. github (`registry.go:265`, needs GitHubOwner and GitHubRepo)
8. umod (`registry.go:270`)
9. steam (`registry.go:272`, needs SteamAppID and key)
10. nexus (`registry.go:281`, needs Community as domain and key)

Corroborating sources:
- Package doc lists the same ten: `registry.go:1-3`, and the upstream hosts at `registry.go:13-15` (api.modrinth.com, thunderstore.io, api.steampowered.com, api.nexusmods.com, api.spiget.org, api.github.com, umod.org; hangar and factorio are not listed in that host line).
- Constructors in `NewSet`: `registry.go:216-224` (`newModrinth`, `newThunderstore`, `newHangar`, `newFactorio`, `newSpigot`; the rest are built lazily).
- Engine files: `api/internal/registry/{modrinth,thunderstore,curseforge,hangar,factorio,spigot,github,umod,steam,nexus}.go`.
- Keyed providers: `api/internal/registry/keys.go:36-40` (`KeyedProviders` = curseforge, steam, nexus).
- Admin validation of the modRegistries config: `api/internal/handlers/config.go:802` (message says `curseforge|steam|nexus`; this is a key-provider check, not the full list).
- Agent: no registry engines. `agent/internal/mods/mods.go:220` and `:836-840` only mention factorio for mod-portal credential injection.

Count: **10**. Names: modrinth, thunderstore, curseforge, hangar, factorio, spigot, github, umod, steam, nexus.

## (c) Built-in RBAC roles

Constants: `api/internal/rbac/rbac.go:44-48`:
- `RoleAdmin = "admin"` (`rbac.go:45`)
- `RoleOperator = "operator"` (`rbac.go:46`)
- `RoleViewer = "viewer"` (`rbac.go:47`)

Comment at `rbac.go:43`: custom roles may use any other valid name.

Seed: `api/internal/db/migrations/sqlite/003_roles.sql:38-41` inserts the three rows with `builtin = 1`. Admin gets `*` (`003_roles.sql:43-44`). Operator and viewer permissions follow at `003_roles.sql:46` onward.

Count: **3** (admin, operator, viewer).

## (d) Go modules

- `go.work` `use (...)` block: `/home/user/Gameplane/go.work:3-20`. Count: **16** entries: agent, api, audit-syslog-bridge, capture-sidecar, gameaction, gameproto, gp-module, mcp-server, netguard, operator, sentinel, svcutil, telemetryschema, telemetry-receiver, test/e2e, tunnel.
- Makefile `GO_MODULES`: `/home/user/Gameplane/Makefile:43`. Count: **15** entries: netguard, gameaction, gameproto, telemetryschema, gp-module, operator, api, agent, audit-syslog-bridge, telemetry-receiver, sentinel, mcp-server, capture-sidecar, svcutil, tunnel.
- Difference: `test/e2e` is in go.work but not in `GO_MODULES`. It is added only in `GO_UNIT_MODULES := $(GO_MODULES) test/e2e` (`Makefile:50`). So go.work (16) = GO_MODULES (15) + test/e2e.
