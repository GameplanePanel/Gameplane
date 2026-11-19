# Research: spec 035 CurseForge and FTB modpacks

Checked on 2026-11-16 against Gameplane master `7413822b`, module `33e1095`, itzg docs
(`docker-minecraft-server` master) and `itzg/mc-image-helper` master. The plan's first
implementation task re-checks every itzg fact below against the image digests the module pins.

## R1: How the itzg image installs and pins each registry's packs

- **Decision**: drive all three registries through the image's own modpack modes, with one
  "reference" env and one "pin" env each:

  | Registry | Fixed env | Reference env | Pin env | Installed-version record on disk |
  |---|---|---|---|---|
  | Modrinth | `TYPE=MODRINTH` | `MODRINTH_MODPACK` (slug) | `MODRINTH_VERSION` (version id) | `/data/.modrinth-modpack-manifest.json` → `versionId` |
  | CurseForge | `TYPE=AUTO_CURSEFORGE`, `CF_DOWNLOADS_REPO=/data/downloads` | `CF_SLUG` | `CF_FILE_ID` | `/data/.curseforge-manifest.json` → `fileId` |
  | FTB | `TYPE=FTBA` | `FTB_MODPACK_ID` (numeric) | `FTB_MODPACK_VERSION_ID` | not needed (every FTB install is new and pinned from the start) |

- **Rationale**: no custom image (FR-011). The manifest names come from mc-image-helper:
  `Manifests.buildManifestPath` writes `.<id>-manifest.json` with ids `modrinth-modpack`
  (`ModrinthModpackManifest`, fields `projectSlug`, `versionId`) and `curseforge`
  (`CurseForgeManifest`, fields `slug`, `modId`, `fileId`, `fileName`, `modpackVersion`).
- **Alternatives**: the deprecated `TYPE=CURSEFORGE` server-pack zip mode needs no key but is
  deprecated upstream and many packs ship no server zip; rejected. `CF_PAGE_URL` with a file
  URL pins too, but splitting slug and file id keeps the pin a single field.

## R2: CurseForge key for game servers (OD-1 C)

- **T001 re-check (2026-11-19)**: env names in R1 match itzg `master` scripts
  (`start-deployAutoCF`, `start-deployModrinth`, `start-deployFTBA`) and mc-image-helper 1.78.2
  (`--slug`, `--file-id`, `--downloads-repo`, `--missing-mods-filename` default
  `MODS_NEED_DOWNLOAD.txt`, `--api-key-file` = `CF_API_KEY_FILE`). The `java8` build matrix pins
  mc-image-helper `1.51.7-java8`, which has `--api-key` (`CF_API_KEY`) but **no** `--api-key-file`.
  A file mount would therefore not be read on the images that need the key. OD-9 A (valgul,
  2026-11-19) replaces the file mount with an env var: see decision 3.

- **Facts**: itzg Java 17+ tags include a CurseForge key in mc-image-helper; `java8`,
  `java8-multiarch`, `java11`, `java16` do not (itzg `docs/versions/java.md`). Newer helpers read
  `CF_API_KEY_FILE`; the pinned `java8` helper reads `CF_API_KEY`. The module uses
  `java8-multiarch` for Minecraft ≤1.16.5.
- **Decision**:
  1. Default: no key in the pod; the image's built-in key is used.
  2. Admin opt-in "Share the CurseForge key with game servers" is stored as an annotation on
     the managed key Secret `gameplane-modreg-curseforge` in the control namespace
     (`gameplane.local/share-with-game-servers: "true"`). The API sets/clears it from the
     admin registry settings; it is never readable or writable by server-scoped roles.
  3. The operator reads that Secret on each reconcile (60s requeue, no watch). For each GameServer whose active pack is CurseForge,
     while sharing is on, it keeps a server-owned Secret `<gs>-curseforge-key` (key `apiKey`)
     copied from the managed Secret `gameplane-modreg-curseforge`, and sets the game container
     env named by the module's `providers.curseforge.apiKeyEnv` (`CF_API_KEY` for
     minecraft-java) via `valueFrom.secretKeyRef` to that Secret. The env is set on the game
     container only (not the agent, not init containers). Sharing off or admin key deleted: the
     operator deletes the copy and drops the env.
  4. The key value is NOT part of the config hash: the hash covers only the secretKeyRef
     (Secret name and key), never the value. A rotated key changes only the copy Secret, not the
     pod template, so it does not restart servers and applies at the next container start.
     Toggling share on/off adds or removes the env var, which changes the pod template and rolls
     that server's pod. OD-10 A (valgul, 2026-11-19): a share toggle rolls the pod only when the server has 0 players online (or is stopped or asleep), reusing the OD-8 B gate; rotation needs no restart.
  5. The module marks images without a built-in key (`needsKeyImageTags: [java8-multiarch,
     java8]`). While sharing is off, the API hides CurseForge in the Modpacks registry switch
     for servers whose selected version uses such an image, and refuses install with 409.
  6. Only a key stored through admin config (the managed Secret) can be shared. A key given
     only by the `--curseforge-api-key` flag lives in API memory; the toggle is disabled with a
     reason in that case.
- **Rationale**: the operator stays the only writer of pod specs (core rule 10) and the copy
  is a server-owned Secret, so `validateServerEnvSources`/the ownership rule holds unchanged.
  The env `valueFrom` keeps the key value out of the pod spec and `kubectl describe pod`
  (only the Secret name shows) and works on every image tag, including the pinned `java8` helper.
- **Alternatives**: env `valueFrom` the control-namespace Secret (cross-namespace refs are
  impossible and the ownership rule forbids it); API writes the copy (bypasses the
  operator); a key-injecting HTTP proxy (mc-image-helper has `--api-base-url`, but running a
  proxy is far more moving parts).
- **Residual risk (documented in docs/security.md)**: with sharing on, code running in the
  game container (any mod) can read the key. That is the reason sharing is off by default.

## R3: Where the active pack lives (FR-004, FR-006, FR-007)

- **Decision**: new optional `GameServer.spec.modpack` (provider, project, version, display
  labels, `backupFirst`) plus `status.modpack` (applied snapshot, phase, backup
  name, missing files). The operator renders the env: it removes every env name declared in
  any provider's `modpacks` block (reference, pin and fixed env) from the merged env, then
  adds the active provider's set. That one rule enforces "one pack per server" (FR-004)
  even if a user left a stale `MODRINTH_MODPACK` in `spec.env`.
- **Rationale**: the panel's state is derived from the GameServer itself (spec Key Entities:
  no new DB table), and the operator owns rendering (rule 10). The API's current
  `setEnvVars` patch of `spec.env` is replaced.
- **Alternatives**: keep writing `spec.env` from the API (no place for phase, backup or
  missing-files status; logic outside the reconciler).

## R4: Applying a pack change: backup first, then apply (FR-013)

- **Decision**: the operator runs a small state machine per pack change, keyed by
  `metadata.generation`:
  `Pending → BackingUp (Backup CR created, owner = GameServer) → Applying → Applied`, or
  `BackupFailed` (nothing applied; message from the Backup's status). While not `Applied`, the
  pod keeps rendering `status.modpack.applied` (the previous pack). `backupFirst` is honoured
  only when the server has a backup destination; the API rejects `backupFirst: true` without
  one (400) so the UI and the server agree.
- **Rationale**: survives browser close and API restarts; the Backup CRD and its controller
  already exist (`backup_types.go`).
- **Alternatives**: the dashboard creates the backup and waits, then installs (breaks if the
  tab closes; logic in the UI).

## R5: Emptying the mods folder (Clarification Q1)

- **Decision**: on Remove only (switching packs does not wipe, OD-7 B), the operator adds a one-shot init container step that empties the active loader's
  mods directory (the per-version+loader mods PVC mounted at `<mountPath>/<loader path>`)
  before the game container starts. The step writes a marker file
  `.gameplane-mods-wiped-<generation>` and skips when the marker exists, so restarts do not
  wipe again.
- **Rationale**: the mods PVC is RWO and mounted by the running pod; doing it inside the next
  pod start avoids a separate Job and a scale-down dance. World data is on the data volume
  and never touched.
- **Alternatives**: API deletes files through the agent while the server runs (races the
  running JVM and the image's own cleanup); a Job (needs the pod stopped first).

## R6: Existing unpinned Modrinth servers (Clarification Q2)

- **Decision**: an API startup sweep (repeated lazily when the Modpacks tab loads a server
  still unmigrated) finds GameServers with a template `refEnv` value in `spec.env` and no
  `spec.modpack`. For running servers it reads `/data/.modrinth-modpack-manifest.json`
  through the agent and writes `spec.modpack` with that `versionId` and the annotation
  `gameplane.local/modpack-adopted: "true"`. Unreadable (server stopped or file missing):
  `spec.modpack` without `version`; the card shows "Not pinned".
- **No restart**: while the adopted annotation is set and the pod is running, the operator
  renders exactly the legacy env (same names, values and order as before), so the pod
  template hash does not change. The pin env is rendered at the next pod start the operator
  drives anyway (restart, stop/start, sleep/wake, or any other spec change), and the
  annotation is then removed. Servers the sweep has not reached yet (legacy `refEnv` value
  in `spec.env`, no `spec.modpack`) get the same legacy rendering, so the order of operator
  reconcile and API sweep cannot roll a pod. To close the crash-restart window (OD-8 B),
  the operator applies the pin to an adopted server with a version as soon as
  `status.agent.playersOnline` is 0 (one operator-driven restart), or immediately when the
  server is stopped or asleep; it never restarts an adopted server with players online.
- **Alternatives**: pin immediately (restarts every such server once on panel upgrade).

## R7: Files that need a manual download (FR-008, OD-5 A)

- **Facts**: mc-image-helper writes `MODS_NEED_DOWNLOAD.txt` into the output directory
  (`/data`) when mods block automated download, as a table with columns Mod, Version Name,
  Filename, Download page; it deletes the file after a successful install. It reads manual
  files from `CF_DOWNLOADS_REPO` and its `mods/`, `modpacks/`, `worlds/` subfolders.
- **Decision**: module sets `CF_DOWNLOADS_REPO=/data/downloads`. The API's
  `GET /servers/{name}/modpack` reads `MODS_NEED_DOWNLOAD.txt` through the agent files API
  when the active pack is CurseForge, parses rows (columns split on 2+ spaces, URL taken as
  the last field and validated as `https://www.curseforge.com/...`), and returns them as
  `missingFiles`. The agent is a sidecar with its own copy of the data mount, so it can
  read the file while the game container exits or crash-loops on the blocked download.
  With no pod at all (stopped or asleep) the API returns `missingFilesUnknown: true`.
  Only CurseForge packs have this case.
- **Alternatives**: parse the container log (lines scroll away, harder to bound).

## R8: FTB registry engine (OD-6 B, FR-012)

- **Facts** (live calls 2026-11-16, no key): `GET
  https://api.feed-the-beast.com/v1/modpacks/public/modpack/search/{limit}?term=…` returns
  pack ids only (`packs`, plus `curseforge` ids); `GET …/modpack/{id}` returns name, slug,
  art, authors, installs and `versions[]` each with `targets` (`game` = Minecraft version,
  `modloader` = `neoforge`/`forge`/`fabric`/`quilt` + version, `runtime` = Java) and
  `type` (release/beta/alpha).
- **Decision**: new keyless engine `api/internal/registry/ftb.go` on the shared
  `netguard.IsPublic` client. Search asks FTB for up to 100 candidate ids, then resolves
  details in batches of 20 with bounded concurrency (4) and a 10-minute detail cache,
  keeping only packs with at least one version whose targets match the server's Minecraft
  version and loader. It stops when the filtered page (20 packs) is full, the candidates are
  exhausted, or 100 details have been resolved, so filtering happens before the page limit
  and latency stays bounded. Versions lists only matching
  versions (Q5), newest first. Empty search term uses FTB's popular list. `ftb` joins the
  `ModProvider.provider` enum and is modpack-only: the providers listing gains
  `modsBrowse: false` so the Mods tab skips it.
- **Compatibility**: the CRD enum change means the updated minecraft-java module needs the
  new operator; the module's `gameplaneMinVersion` is raised to the first release that ships
  this spec.

## R9: Version lists filtered to the server (Clarification Q5)

- **Decision**: reuse each engine's `Versions(projectID, Filter{Loader, GameVersion})`.
  Modrinth and CurseForge already filter files by game version and loader; the plan's first
  API task adds fixture tests proving CurseForge modpack files (class 4471) filter the same
  way as mods. FTB filters on `targets` (R8).

## R10: Downgrades (Clarification Q4)

- **Decision**: the picker marks versions older than the pinned one; choosing one sets
  `downgrade: true` in the confirmation, which shows the warning. No server-side block. The
  image handles downgrades itself (it cleans files its manifest no longer lists).
