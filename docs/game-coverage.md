# Game Server Join Protocol Coverage

Every Gameplane-shipped game module is listed here with its join-protocol coverage status: tested in CI, deferred from CI, or blocked by a specific constraint. Read the Status and Blocker columns to understand why each module is tested (or not) the way it is. A module blocked by an architectural constraint (anti-cheat, platform relay) is marked as such rather than left as an open item. All dates are ISO 8601 (YYYY-MM-DD).

| Module | Game | Status | Depth | Test | Bucket | Last Verified | Blocker | Blocker Class |
|---|---|---|---|---|---|---|---|---|
| `minecraft-java` | Minecraft: Java Edition | covered-in-ci | JOINED | TestGameServer_MinecraftJavaBot_Joined | bot-fast | 2026-08-16 | — | — |
| `terraria` | Terraria | covered-in-ci | JOINED | TestGameServer_TerrariaBot_Joined | bot-fast | 2026-08-16 | — | — |
| `valheim` | Valheim | out-of-scope-by-design | QUERY | TestGameServer_ValheimBot_Query | bot-heavy | — | Joins route through Steam Datagram Relay; no direct-UDP join path for a headless client | architectural |
| `dayz` | DayZ | out-of-scope-by-design | QUERY | TestGameServer_DayZBot_Query | bot-heavy | — | BattlEye anti-cheat gates the join; requires a real game binary | architectural |
| `garrys-mod` | Garry's Mod | blocked-doc | QUERY | TestGameServer_GarrysModBot_Query | bot-fast | — | C2S_CONNECT field offsets unknown; challenge exchange already decoded, sv_lan 1 removes the Steam gate | documentation |
| `factorio` | Factorio | blocked-doc | QUERY | TestGameServer_FactorioBot_Query | bot-heavy | — | Wire format undocumented; server and client must match exactly (version-locked) | documentation |
| `cs2` | Counter-Strike 2 | blocked-doc | QUERY | TestGameServer_CS2Bot_Query | bot-heavy | — | Source 2 connect packet reportedly carries embedded protobuf and Steam auth ticket; incomplete public documentation | documentation |
| `rust` | Rust | blocked-doc | QUERY | TestGameServer_RustBot_Query | bot-heavy | — | RakNet wire format partially documented but implementation incomplete; anti-cheat may block headless clients | documentation |
| `ark-survival-ascended` | ARK: Survival Ascended | blocked-doc | QUERY | TestGameServer_ArkBot_Query | bot-heavy | — | Unreal Engine 5 network stack partially documented; custom protocol variant under reverse-engineering | documentation |
| `palworld` | Palworld | blocked-doc | QUERY | TestGameServer_PalworldBot_Query | bot-heavy | — | Unreal Engine 5; stateless-connect handshake under reverse-engineering, Steam authentication integration incomplete | documentation |
| `satisfactory` | Satisfactory | blocked-doc | QUERY | TestGameServer_SatisfactoryBot_Query | bot-heavy | — | Undocumented proprietary HTTPS API (not standard UDP game protocol); TLS-over-UDP encryption from byte 0 | documentation |
| `7-days-to-die` | 7 Days to Die | blocked-doc | QUERY | TestGameServer_SevenDaysToDieBot_Query | bot-heavy | — | Undocumented proprietary UDP protocol; Steam A2S query reachable but join handshake unknown | documentation |
| `project-zomboid` | Project Zomboid | blocked-doc | QUERY | TestGameServer_ProjectZomboidBot_Query | bot-heavy | — | Undocumented custom UDP protocol; optional non-Steam private server mode possible but format unknown | documentation |
| `dont-starve-together` | Don't Starve Together | blocked-doc | QUERY | TestGameServer_DontStarveTogetherBot_Query | bot-heavy | — | Klei reliable-UDP frame format and handshake not documented; server credentials required at join | documentation |
| `enshrouded` | Enshrouded | blocked-doc | QUERY | TestGameServer_EnshroudedBot_Query | bot-heavy | — | Undocumented proprietary protocol; wire format and handshake feasibility unknown | documentation |
| `v-rising` | V Rising | blocked-doc | QUERY | TestGameServer_VRisingBot_Query | bot-heavy | — | Undocumented proprietary UDP protocol; transport layer unidentified | documentation |
| `nuclear-option` | Nuclear Option | blocked-doc | QUERY | TestGameServer_NuclearOptionBot_Query | bot-heavy | — | UDP 7777 not bound; joins route through Steam game-server networking and the join handshake requires a real-client packet capture | documentation |
| `fivem` | FiveM | blocked-doc | QUERY | TestGameServer_FiveMBot_Query | bot-heavy | — | CitizenFX join protocol undocumented; dynamic.json query reachable but ENet connection format requires packet capture | documentation |
| `team-fortress-2` | Team Fortress 2 | blocked-doc | QUERY | TestGameServer_TeamFortress2Bot_Query | bot-heavy | — | Source engine C2S_CONNECT field offsets and Steam auth ticket format incomplete in public documentation | documentation |
| `farming-simulator-25` | Farming Simulator 25 | blocked-doc | QUERY | TestGameServer_FarmingSimulator25Bot_Query | bot-heavy | — | Undocumented proprietary UDP protocol; GIANTS web API health reachable but game join handshake unknown | documentation |
| `euro-truck-simulator-2` | Euro Truck Simulator 2 | blocked-doc | QUERY | TestGameServer_EuroTruckSimulator2Bot_Query | bot-heavy | — | Undocumented Prism3D multiplayer protocol; Steam A2S query verified but convoy join handshake requires packet capture | documentation |
| `mount-and-blade-2-bannerlord` | Mount & Blade II: Bannerlord | blocked-doc | QUERY | TestGameServer_MountAndBlade2BannerlordBot_Query | bot-heavy | — | Undocumented TaleWorlds UDP protocol; Steam A2S query verified but custom server authentication token format undocumented | documentation |
| `tmodloader` | tModLoader | blocked-doc | QUERY | TestGameServer_TModLoaderBot_Query | bot-heavy | — | Terraria TCP message framing and mod synchronization handshake require reverse-engineering against active modpack | documentation |
| `beammp` | BeamMP | blocked-doc | QUERY | TestGameServer_BeamMPBot_Query | bot-heavy | — | Undocumented custom TCP/UDP bridge protocol; auth handshake verified but client vehicle synchronization protocol format unknown | documentation |
| `left-4-dead-2` | Left 4 Dead 2 | blocked-doc | QUERY | TestGameServer_Left4Dead2Bot_Query | bot-heavy | — | Source engine connection challenge verified but C2S_CONNECT field offsets and Steam auth ticket format incomplete in documentation | documentation |
| `the-isle` | The Isle | blocked-doc | QUERY | TestGameServer_TheIsleBot_Query | bot-heavy | — | Unreal Engine 4 network protocol variant partially documented; stateless connect handshake requires packet capture | documentation |
| `ark-survival-evolved` | ARK: Survival Evolved | blocked-doc | QUERY | TestGameServer_ArkSurvivalEvolvedBot_Query | bot-heavy | — | Unreal Engine 4 custom net driver; A2S query verified but ShooterGame client login handshake requires reverse-engineering | documentation |
| `arma-reforger` | Arma Reforger | blocked-doc | QUERY | TestGameServer_ArmaReforgerBot_Query | bot-heavy | — | Bohemia Interactive Enfusion engine proprietary network protocol; A2S query verified but backend authentication format undocumented | documentation |
| `squad` | Squad | blocked-doc | QUERY | TestGameServer_SquadBot_Query | bot-heavy | — | Unreal Engine 4 network stack; Steam A2S query verified but client connection handshake format undocumented | documentation |

## Covered (in CI)

These modules have a real protocol-join test that runs in the `bot-fast` bucket on every PR. The server is booted and the protocol client completes a login handshake, observing server-originated evidence of acceptance (e.g., Minecraft Login Success packet). Depth is JOINED.

- **minecraft-java**: Login state machine via Encryption Request rejection (offline-mode). Runs ~3 minutes.
- **terraria**: TCP message frames with per-player handshake and WorldData packet. Runs ~2 minutes.

## Covered (deferred)

Currently empty. No modules are committed to deferred join coverage at this time.

## Blocked

These modules lack a join-protocol client. The reason is documented as temporary (documentation gap, needs a packet capture or reverse-engineering). Each is a candidate for future protocol work once the missing documentation or wire format is available.

### Documentation Blockers (reversible)

- **garrys-mod**: C2S_CONNECT field offsets unknown. Challenge exchange is already decoded and sv_lan 1 removes the Steam authentication gate, but the version negotiation field order cannot be derived by blind iteration — a real client capture is needed to establish the exact packet layout.
- **factorio**: Wire format undocumented. Mutual random-ID exchange is known but no further byte layout is published. Critical constraint: Factorio is version-locked (client and server must match exactly); a capture must record both versions.
- **cs2**: Source 2 connect packet reportedly carries embedded protobuf and Steam auth ticket, making it more opaque than Source 1. Incomplete public documentation; packet capture would accelerate reverse-engineering.
- **rust**: RakNet wire format is open-source and partially documented, but implementation in the project is incomplete. Anti-cheat may block headless clients once protocol is solved.
- **ark-survival-ascended**: Unreal Engine 5 network stack is partially documented. Custom protocol variant under reverse-engineering; anti-cheat may apply.
- **palworld**: Unreal Engine 5 stateless-connect handshake under reverse-engineering. Steam authentication integration incomplete for a headless client.
- **satisfactory**: Undocumented proprietary HTTPS API (not standard UDP game protocol). Uses TLS-over-UDP with encryption from byte 0, making raw packet captures unreadable without the server's key material.
- **7-days-to-die**: Undocumented proprietary UDP protocol. Steam A2S query is reachable but the join handshake format is unknown. No public protocol analysis or modding documentation covers networking.
- **project-zomboid**: Undocumented custom UDP protocol. Optional non-Steam private server mode exists but the wire format is unknown. Packet capture and analysis required.
- **dont-starve-together**: Klei reliable-UDP frame format and handshake are not documented. Server credentials are required at join but the authentication flow is undocumented.
- **enshrouded**: Undocumented proprietary protocol. Wire format and handshake feasibility are completely unknown; no public reverse-engineering exists.
- **v-rising**: Undocumented proprietary UDP protocol. Transport layer is unidentified despite community modding ecosystem. Packet capture and reverse-engineering required.
- **fivem**: CitizenFX join protocol undocumented. dynamic.json query is reachable but ENet connection format requires packet capture and analysis.
- **team-fortress-2**: Source engine C2S_CONNECT field offsets and Steam auth ticket format are incomplete in public documentation.
- **farming-simulator-25**: Undocumented proprietary UDP protocol. GIANTS web API health is reachable but game join handshake requires packet capture.
- **euro-truck-simulator-2**: Undocumented Prism3D multiplayer protocol. Steam A2S query verified but convoy join handshake requires packet capture.
- **mount-and-blade-2-bannerlord**: Undocumented TaleWorlds UDP protocol. Steam A2S query verified but custom server authentication token format is undocumented.
- **tmodloader**: Terraria TCP message framing and mod synchronization handshake require reverse-engineering against active modpacks.
- **beammp**: Undocumented custom TCP/UDP bridge protocol. Auth handshake verified but client vehicle synchronization protocol format is unknown.
- **nuclear-option**: UDP 7777 is not bound and joins route through Steam game-server networking. The QUERY probe targets the UDP 7778 query port; the join handshake requires a real-client packet capture.
- **left-4-dead-2**: Source engine connection challenge verified but C2S_CONNECT field offsets and Steam auth ticket format are incomplete in documentation.
- **the-isle**: Unreal Engine 4 network protocol variant partially documented. Stateless connect handshake requires packet capture.
- **ark-survival-evolved**: Unreal Engine 4 custom net driver. A2S query verified but ShooterGame client login handshake requires reverse-engineering.
- **arma-reforger**: Bohemia Interactive Enfusion engine proprietary network protocol. A2S query verified but backend authentication format is undocumented.
- **squad**: Unreal Engine 4 network stack. Steam A2S query verified but client connection handshake format is undocumented.

## Out of Scope by Design

These modules encounter architectural constraints that prevent a headless automated client from ever completing a join, per spec FR-008. They are recorded here not as an open item forever, but as a deliberate decision that CI cannot test them further.

- **valheim**: Joins route exclusively through Steam Datagram Relay, a platform-specific UDP relay transport. There is no direct-UDP join path for a standalone headless client; Steam authentication is mandatory at the platform level. No headless client is possible.
- **dayz**: BattlEye anti-cheat validation is gated before the login handshake completes. Even a fully correct protocol implementation cannot join without a real game binary running the anti-cheat kernel module. No headless client is possible.
