# Open Decisions: Feature 015 (Dedicated Server Modules for Top Steam Games)

This document tracks unresolved questions and findings that remain explicitly open for maintainer ruling, per tasks T005, T012, T018, T019, T086, T089, and T096.

---

## 1. `cli` rcon.protocol Semantics (T005, T019, T025)

### Context
In the CRD:
- `spec.capabilities.actions[].transport` automatically resolves to `rcon` when `rcon.protocol != "none"`, and to `stdin` (pod-attach) when `rcon.protocol == "none"`.
- `spec.consoleMode: pty` configures the dashboard console to attach directly to container stdin/stdout.

The maintainer ruled that `cli` becomes a first-class enum value on `spec.rcon.protocol`.

### Resolution: Option A Confirmed
1. **Relationship to Pod-Attach**:
   - Option A is confirmed: `cli` represents an agent-driven console client where the agent communicates via standard input / Unix PTY locally in the container or via local process execution, enabling scheduled commands, health checks, and lifecycle stop sequences through the agent interface without requiring remote TCP networking. The web dashboard console continues to attach directly via Kubernetes pod-attach / PTY (`consoleMode: pty`).

2. **Operator Implications (`templateHasRCON` / `resolveRCON`)**:
   - For `rcon.protocol: cli`, if neither `passwordSecretRef` nor `passwordEnv` is specified, `resolveRCON` in the operator does not require or mint an RCON password Secret. The agent sidecar's CLI client executes locally without requiring an external password secret.

3. **Web Dashboard Gating (`capabilities.ts` `rconAvailable`)**:
   - In `web/src/lib/capabilities.ts:20`, `rconAvailable` evaluates whether network-interactive RCON commands are supported. For `rcon.protocol: cli`, the web dashboard routes user interaction through the interactive PTY console (`consoleMode: pty`), so `rconAvailable` gates `cli` appropriately (`proto !== "none" && proto !== "cli"`).

---

## 2. Factorio Protocol Finding (T005)

### Finding
`modules/factorio/template.yaml` ships with:
```yaml
rcon:
  protocol: source
  port: 27015
  passwordSecretRef:
    name: factorio-rcon
    key: password
consoleMode: pty
```
However, `contracts/engine-matrix-contract.md` previously claimed `rcon.protocol: none` + `consoleMode: pty`.
Factorio natively supports Source RCON over UDP/TCP when `--rcon-port` and `--rcon-password` are passed, alongside its interactive stdin console.

### Recommendation
The shipped template (`rcon.protocol: source`) is correct and functional. Both RCON and stdin can coexist. Retain the shipped template configuration pending final maintainer confirmation.

### Maintainer Ruling (2026-10-09)
Keep `rcon.protocol: source` as shipped. `contracts/engine-matrix-contract.md` is corrected to match.

---

## 3. Project Zomboid Protocol Finding (T005)

### Finding
`modules/project-zomboid/template.yaml` ships with:
```yaml
rcon:
  protocol: source
  port: 27015
  passwordSecretRef:
    name: project-zomboid-rcon
    key: password
```
However, `contracts/engine-matrix-contract.md` claimed stdin-only (`rcon.protocol: none`).
Project Zomboid's dedicated server supports RCON (Source-compatible protocol) through `RCONPort` and `RCONPassword` in `server_settings.ini`.

### Recommendation
The shipped template (`rcon.protocol: source`) is valid and matches PZ's native RCON server capabilities. Retain the shipped template configuration without change pending final maintainer confirmation.

### Maintainer Ruling (2026-10-09)
Keep `rcon.protocol: source` as shipped. `contracts/engine-matrix-contract.md` is corrected to match.

---

## 4. Don't Starve Together Stop Sequence under `none` Protocol (T005, T064, T090)

### Finding
`modules/dont-starve-together/template.yaml` defines:
```yaml
rcon:
  protocol: none
capabilities:
  lifecycle:
    stop:
      - "c_save()"
      - "c_shutdown(true)"
```
The CRD schema description notes that `capabilities.lifecycle.stop` requires `rcon.protocol != none`, but no CEL rule enforces this, and DST's container entrypoint or stdin handler executes these commands upon stop.

### Status
Recorded as an intentional exception in `dont-starve-together`. The configuration is preserved as-is without removing the stop sequence.

---

## 5. Generic `rest` Wire Contract (T005, T018, T047, T052)

### Contract Specification for `agent/internal/rcon/rest.go`
To unify FiveM's txAdmin API (T047) and Farming Simulator 25's web admin API (T052) behind a generic HTTP REST client without requiring new CRD fields:

1. **Declared Template Fields**:
   - `spec.rcon.protocol`: `rest`
   - `spec.rcon.port`: Target HTTP port (e.g. `40120` for FiveM/txAdmin, `8080` for Farming Simulator 25).
   - `spec.rcon.passwordSecretRef` or `spec.rcon.passwordEnv`: Mounted by the operator to the file passed to the agent via `-rcon-password-file`.

2. **Endpoint Paths & Adapters**:
   - The agent provides an adapter interface `RESTAdapter`:
     ```go
     type RESTAdapter interface {
         Name() string
         BuildRequest(ctx context.Context, baseURL, cmd, credential string) (*http.Request, error)
         ParseResponse(resp *http.Response, body []byte) (string, error)
     }
     ```
   - **FiveM txAdmin Adapter (`txadmin`)**:
     - Selected when game is `fivem` or port is `40120`.
     - Target Path: `POST http://<host>:<port>/fxserver/commands`
     - Auth: `X-TxAdmin-Token: <token>` and `Authorization: Bearer <token>`
     - Request Envelope: `{"action": "console", "parameter": "<cmd>"}` (also accepts `{"command": "<cmd>"}`)
     - Response: JSON decoding `output`, `result`, `message`, or `data`; accepts HTTP 200/204; returns extracted text or empty string.
   - **Farming Simulator 25 Web Admin Adapter (`farming-simulator-25`)**:
     - Selected when game is `farming-simulator-25` (or `fs25`) or port is `8080`.
     - Target Path: `POST http://<host>:<port>/api/console`
     - Auth: `Authorization: Basic <base64(admin:<password>)>` (auto-prefixes `admin:` if no colon exists in credential).
     - Request Envelope: `{"command": "<cmd>"}`
     - Response: JSON decoding `output`, `result`, `message`, or raw text response.
   - **Generic Fallback Adapter (`generic`)**:
     - Target Path: `POST http://<host>:<port>/api/command`
     - Auth: If credential contains `:`, uses `Basic <base64(credential)>`; otherwise uses `Bearer <credential>`.
     - Request Envelope: `{"command": "<cmd>"}`
     - Response: Parses JSON fields (`output`, `result`, `message`, `data`, `commandResult`) or fallback to raw string body.

3. **Client Architecture & Safety Properties**:
   - Lazy auth resolution: Credential resolved on each command invocation via `PassFn`.
   - Bounded response size: Reads capped at 1 MiB (`restMaxResponseBytes`) to prevent unbounded memory consumption.
   - Timeouts: Configurable dial timeout (default 5s) and request timeout (default 10s) as struct fields for unit testability.
   - Auth failure cooldown: 15s cooldown on HTTP 401/403 with `ErrAuth` return to prevent poller hammering.
   - TLS: As implemented, HTTPS uses standard certificate verification with `MinVersion: tls.VersionTLS12` (`agent/internal/rcon/rest.go`); there is no loopback-only `InsecureSkipVerify` path (corrected 2026-10-09).

---

## 6. SC-005 and SC-006 Test Coverage Scope (T005)

### Finding
- `spec.md` Success Criteria state:
  - **SC-005**: 100% of game servers restart without data loss or corruption.
  - **SC-006**: 100% of modules with remote console interfaces successfully execute administrative commands and graceful save-on-shutdown.
- However, running full restart and RCON E2E tests for all 26 games in CI would exceed runner memory, disk, and execution time limits (e.g. downloading 30+ GB SteamCMD binaries for heavy games like Ark, Squad, Reforger, Hell Let Loose).
- Phases 4 and 5 implement verified sampling:
  - Two dedicated persistence restart tests (`fivem_persistence_e2e_test.go`, `arksurvival_cluster_persistence_e2e_test.go`).
  - Two dedicated RCON/lifecycle tests (`teamfortress2_rcon_e2e_test.go`, `squad_rcon_e2e_test.go`).
  - Systematic static/config audits for all remaining modules (T076-T079, T083-T092).

### Recommendation
Maintainer ruling requested: Amend SC-005 and SC-006 in `spec.md` to reflect the sampled verification strategy, or acknowledge the remaining coverage as deferred heavy tests in `bucket_bot_heavy`.

### Maintainer Ruling (2026-10-09)
Amend the spec. SC-005 and SC-006 in `spec.md` now state sampled e2e verification plus a static audit of all 26 modules; no per-module restart or RCON e2e tests are added.

---

## 7. Storage Mount Path Divergence Findings (T012)

Comparison between `contracts/engine-matrix-contract.md`'s Storage Mount Path column and shipped `modules/<name>/template.yaml` `spec.storage.mountPath`. Per T012, shipped values stand until maintainer rules on these findings.

### 7.1 Palworld (`palworld`)
- **Shipped**: `/palworld`
- **Contract**: `/palworld/Pal/Saved`
- **Recommendation**: Shipped `/palworld` encompasses world saves, engine settings, and server logs. Narrowing to `/palworld/Pal/Saved` would lose custom configuration files unless extra mounts are configured. Retain shipped `/palworld`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/palworld`; contract updated.

### 7.2 Rust (`rust`)
- **Shipped**: `/steamcmd/rust`
- **Contract**: `/serverdata`
- **Recommendation**: The image (`didstopia/rust-server`) installs into and runs from `/steamcmd/rust`. Retain shipped `/steamcmd/rust`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/steamcmd/rust`; contract updated.

### 7.3 Project Zomboid (`project-zomboid`)
- **Shipped**: `/home/steam/Zomboid`
- **Contract**: `/home/pzuser/Zomboid`
- **Recommendation**: The shipped container runs with user `steam` (UID 10000) whose HOME is `/home/steam`. Contract path `/home/pzuser` is inaccurate for this image. Retain shipped `/home/steam/Zomboid`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/home/steam/Zomboid`; contract updated.

### 7.4 DayZ (`dayz`)
- **Shipped**: `/data`
- **Contract**: `/serverdata`
- **Recommendation**: Shipped image mounts persistent world data at `/data`. Retain shipped `/data`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/data`; contract updated.

### 7.5 Garry's Mod (`garrys-mod`)
- **Shipped**: `/home/gmod/server/garrysmod/data`
- **Contract**: `/home/steam/gmod-dedicated`
- **Recommendation**: The container user is `gmod`. Mounting at `/home/gmod/server/garrysmod/data` avoids shadowing the game server binary launcher at `/home/gmod/server`. Retain shipped `/home/gmod/server/garrysmod/data`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/home/gmod/server/garrysmod/data`; contract updated.

### 7.6 Terraria (`terraria`)
- **Shipped**: `/opt/terraria/config`
- **Contract**: `/root/.local/share/Terraria/Worlds`
- **Recommendation**: Shipped image stores server configs, world files, and bans in `/opt/terraria/config`. Retain shipped `/opt/terraria/config`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/opt/terraria/config`; contract updated.

### 7.7 ARK: Survival Ascended (`ark-survival-ascended`)
- **Shipped**: `/home/gameserver`
- **Contract**: `/serverdata/ShooterGame/Saved`
- **Recommendation**: Image `mschnitzer/asa-linux-server` uses `/home/gameserver` as WorkingDir and storage location for cluster and saved data. Retain shipped `/home/gameserver`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/home/gameserver`; contract updated.

### 7.8 Factorio (`factorio`)
- **Shipped**: `/factorio`
- **Contract**: `/factorio/saves`
- **Recommendation**: Shipped image `factoriotools/factorio` mounts `/factorio` to persist saves, mods, and `config/` together. Retain shipped `/factorio`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/factorio`; contract updated.

### 7.9 Don't Starve Together (`dont-starve-together`)
- **Shipped**: `/data`
- **Contract**: `/root/.klei/DoNotStarveTogether`
- **Recommendation**: Shipped image `jamesstevens/dont-starve-together` standardizes on `/data` with internal symlinks to cluster configs. Retain shipped `/data`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/data`; contract updated.

### 7.10 Valheim (`valheim`)
- **Shipped**: `/config`
- **Contract**: `/config/worlds_local`
- **Recommendation**: Shipped image `lloesche/valheim-server` uses `/config` to persist server state, worlds, and BepInEx configs. Retain shipped `/config`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/config`; contract updated.

### 7.11 Satisfactory (`satisfactory`)
- **Shipped**: `/config`
- **Contract**: `/home/steam/.config/Epic/FactoryGame/Saved`
- **Recommendation**: Shipped image `wolveix/satisfactory-server` maps `/config` to game configuration, saves, and blueprint storage. Retain shipped `/config`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/config`; contract updated.

### 7.12 7 Days to Die (`7-days-to-die`)
- **Shipped**: `/home/sdtdserver/.local/share/7DaysToDie`
- **Contract**: `/home/sdtduser/.local/share/7DaysToDie`
- **Recommendation**: Image `vinanrra/7dtd-server` uses `sdtdserver` as its system user, not `sdtduser`. Retain shipped `/home/sdtdserver/.local/share/7DaysToDie`.
- **Maintainer Ruling (2026-10-09)**: Keep shipped `/home/sdtdserver/.local/share/7DaysToDie`; contract updated.

---

## 8. 7 Days to Die Declared Telnet Port Divergence (T057, T089)

### Finding
`modules/7-days-to-die/template.yaml` declares a telnet port at `8081/TCP`:
```yaml
ports:
  - name: telnet
    containerPort: 8081
    protocol: TCP
    advertise: false
```
The contract table previously recorded port `8082`. The shipped template declares `8081`, matching the default LinuxGSM telnet port documentation in `vinanrra/7dtd-server`.
Since `rcon.protocol: none` is configured (TelnetPassword is in serverconfig.xml unreachable from the world-saves PVC mount, and LinuxGSM user.sh traps SIGTERM directly for `sdtdserver stop`), this port is declared for documentation and firewall reservation only.

### Recommendation
Retain the shipped template's port `8081` without silently renumbering it.

### Maintainer Ruling (2026-10-09)
Keep `8081/TCP` as shipped. `contracts/engine-matrix-contract.md` carries no telnet port number for this module (its RCON column reads None), so it needs no correction.

---

## 9. Deliberate Omission of `capabilities.mods` in Garry's Mod, 7 Days to Die, and Project Zomboid (T094, T096)

### Context & Findings
Per T096, adding a `capabilities.mods` block to `modules/garrys-mod/template.yaml`, `modules/7-days-to-die/template.yaml`, or `modules/project-zomboid/template.yaml` would reverse documented deliberate architectural omissions:

1. **Garry's Mod (`garrys-mod`)**:
   - `modules/garrys-mod/template.yaml` header explicitly states: *"No mods capability: the per-loader/legacy mod volume is always mounted at storage.mountPath/<path> ... i.e. always nested under `.../garrysmod/data`. srcds reads addons from `garrysmod/addons`, a SIBLING of `data`, not a child of it ... and the CRD rejects '..' in mods paths anyway ... Structurally impossible with this image, not just unconfigured. Workshop addons still work with none of the above: `+host_workshop_collection <id> -authkey <key>` via the ARGS field downloads and mounts a Workshop collection at boot without touching the addons dir or RCON at all."*
   - Adding a `capabilities.mods` block would violate path isolation or fail CRD validation.

2. **7 Days to Die (`7-days-to-die`)**:
   - `modules/7-days-to-die/template.yaml` header states: `Mods/` lives under `serverfiles/` (a sibling of the persistent world-saves mount `.local/share/7DaysToDie`).
   - Mod installation is handled natively by the container's built-in LinuxGSM script hooks via environment variables (`UNDEAD_LEGACY`, `DARKNESS_FALLS`, `MODS_URLS`) defined in `configSchema`.

3. **Project Zomboid (`project-zomboid`)**:
   - The shipped template does not declare `capabilities.mods`. PZ dedicated servers configure mods via `server_settings.ini` (`Mods=`, `WorkshopItems=`).

### Maintainer Ruling / Recommendation
Maintain the deliberate omission of `capabilities.mods` for all three modules. Workshop integration is handled via launch parameters / env vars (`ARGS` in GMod, `MODS_URLS` in 7DtD).
Consequently, per T094, the workshop E2E test is retargeted from Garry's Mod to DayZ (`test/e2e/dayz_workshop_e2e_test.go`), which ships authoritative Steam Workshop `capabilities.mods.idList` wiring under `MOD_LIST`.

---

## 10. Image Pinning and Image Source Gaps (plan refresh, 2026-10-09)

### Finding
Found while refreshing `plan.md` against the shipped templates (plan.md, Known Gaps 1-3):
- Ten new modules pin the placeholder digest `@sha256:0000000000000000000000000000000000000000000000000000000000000000`: `ark-survival-evolved`, `arma-reforger`, `beammp`, `euro-truck-simulator-2`, `farming-simulator-25`, `fivem`, `hell-let-loose`, `mount-and-blade-2-bannerlord`, `squad`, `the-isle`. This contradicts FR-003, although T119 is marked complete.
- Six of those (`mount-and-blade-2-bannerlord`, `the-isle`, `ark-survival-evolved`, `arma-reforger`, `hell-let-loose`, `squad`) reference `ghcr.io/valgulnecron/gameplane/<game>` images that no Dockerfile in either repository builds; `modules/build-images.sh` knows only the four images from research.md Decision 1.
- All ten Gameplane-owned refs use the pre-move `valgulnecron` registry owner, while `build-images.sh`'s usage example pushes to `ghcr.io/gameplanepanel/gameplane`.

### Recommendation
Open for maintainer ruling: (a) widen research.md Decision 1 to the six extra games and add Dockerfiles for them, or (b) move those six templates to pinned community images. Either way, publish the images under `ghcr.io/gameplanepanel/gameplane`, pin real digests with `validate.py --pin` (T119), and re-check before T126.


### Maintainer Ruling (2026-10-09)
- **Image source for the six unbuilt images: Mixed.** For each of `mount-and-blade-2-bannerlord`, `the-isle`, `ark-survival-evolved`, `arma-reforger`, `hell-let-loose`, `squad`: use a maintained community image pinned by real digest where a suitable one exists; otherwise add a Gameplane-owned `Dockerfile` + `entrypoint.sh` under `modules/<game>/` and register it in `modules/build-images.sh`. The per-game choice is recorded here when the change lands.
- **Registry owner: split by build date.** Images already published under `ghcr.io/valgulnecron/gameplane/…` stay there and existing refs to them are not rewritten. Every image built from now on, including any new build for an existing module, is pushed and signed under `ghcr.io/gameplanepanel/gameplane/…`. The ten placeholder-digest refs in this section were never published, so their first real build goes to `gameplanepanel` and the templates are repointed when they are pinned.

### Per-game image choice (T133, 2026-10-09)

| Module | Choice | Why |
|---|---|---|
| `arma-reforger` | Community: `ghcr.io/acemod/arma-reforger` (ACE team), pinned by digest | Actively maintained, configured by env vars, RCON via `RCON_PASSWORD`. It runs as root by design (SteamCMD), as Palworld's image does, so the template drops `spec.security`; the volume mounts at `/reforger`. |
| `squad` | Gameplane-built (`containers/games/squad/`), maintainer ruling 2026-10-09 | `cm2network/squad` is maintained but cannot set the RCON password from env (it lives in `SquadGame/ServerConfig/Rcon.cfg` inside the install), so console and probes would stay broken until hand-edited. |
| `the-isle` | Gameplane-built (`containers/games/the-isle/`) | No community image. SteamCMD app 412680, public `evrima` beta. |
| `ark-survival-evolved` | Gameplane-built (`containers/games/ark-survival-evolved/`), maintainer ruling 2026-10-09 | No clearly maintained community image: recent ones are single-maintainer, older popular ones are 2-5 years stale. SteamCMD app 376030, anonymous. |
| `mount-and-blade-2-bannerlord` | Gameplane-built (`containers/games/mount-and-blade-2-bannerlord/`) | No community image. Windows-only dedicated server (app 1863440), downloaded with `+@sSteamCmdForcePlatformType windows` and run under Wine; a missing `SERVER_TOKEN` idles with instructions (FR-013). |
| `hell-let-loose` | *Pending maintainer card* (recommended: keep the module, mark it blocked) | The server files go only to Team17's licensed hosting partners; there is no public SteamCMD download, so no image can install it. |

Build location (maintainer ruling 2026-10-09): the Gameplane-built images live in this repo's `containers/games/<name>/` on the shared SteamCMD base (UID 10000) and are built by `.github/workflows/images.yaml`, which checks each build on PRs and publishes and signs to `ghcr.io/gameplanepanel/gameplane` on merge to `master`, as Nuclear Option does. This supersedes the "`modules/<game>/` + `modules/build-images.sh`" wording of the ruling above. `modules/.github/workflows/build-images.yml` still gets the lowercase registry-owner fix for its four existing images.

Build location for the four original images (maintainer ruling 2026-10-10): `fivem`, `beammp`, `farming-simulator-25` and `euro-truck-simulator-2` move unchanged from `modules/<game>/` to this repo's `containers/games/<game>/` and join the `images.yaml` matrix, because the module repo's `build-images.yml` has no signing key and only ever built them without pushing. The module repo drops its copies, `build-images.sh` and `build-images.yml`.
