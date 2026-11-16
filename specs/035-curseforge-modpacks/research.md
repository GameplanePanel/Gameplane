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

- **Facts**: itzg Java 17+ tags include a CurseForge key in mc-image-helper; `java8`,
  `java8-multiarch`, `java11`, `java16` do not (itzg `docs/versions/java.md`). The image reads
  `CF_API_KEY_FILE`. The module uses `java8-multiarch` for Minecraft ≤1.16.5.
- **Decision**:
  1. Default: no key in the pod; the image's built-in key is used.
  2. Admin opt-in "Share the CurseForge key with game servers" is stored as an annotation on
     the managed key Secret `gameplane-modreg-curseforge` in the control namespace
     (`gameplane.local/share-with-game-servers: "true"`). The API sets/clears it from the
     admin registry settings; it is never readable or writable by server-scoped roles.
  3. The operator watches that Secret. For each GameServer whose active pack is CurseForge,
     while sharing is on, it reconciles a server-owned Secret `<gs>-curseforge-key` (key
     `apiKey`), mounts it read-only at `/run/gameplane/curseforge/api-key` in the game
     container only (not the agent, not init containers), and sets `CF_API_KEY_FILE` to that
     path. Sharing off or admin key deleted: the operator deletes the copy and drops the mount.
  4. The key copy is NOT part of the config hash: kubelet refreshes mounted Secret files in
     place and the image reads the key only at boot, so a rotation never restarts servers.
  5. The module marks images without a built-in key (`needsKeyImageTags: [java8-multiarch,
     java8]`). While sharing is off, the API hides CurseForge in the Modpacks registry switch
     for servers whose selected version uses such an image, and refuses install with 409.
  6. Only a key stored through admin config (the managed Secret) can be shared. A key given
     only by the `--curseforge-api-key` flag lives in API memory; the toggle is disabled with a
     reason in that case.
- **Rationale**: the operator stays the only writer of pod specs (core rule 10) and the copy
  is a server-owned Secret, so `validateServerEnvSources`/the ownership rule holds unchanged.
  File mount instead of env keeps the key out of `kubectl describe pod` and the pod spec.
- **Alternatives**: env `valueFrom` the control-namespace Secret (cross-namespace refs are
  impossible and the ownership rule forbids it); API writes the copy (bypasses the
  operator); a key-injecting HTTP proxy (mc-image-helper has `--api-base-url`, but running a
  proxy is far more moving parts).
- **Residual risk (documented in docs/security.md)**: with sharing on, code running in the
  game container (any mod) can read the key. That is the reason sharing is off by default.

## R3: Where the active pack lives (FR-004, FR-006, FR-007)

- **Decision**: new optional `GameServer.spec.modpack` (provider, project, version, display
  labels, `backupFirst`, `wipeMods`) plus `status.modpack` (applied snapshot, phase, backup
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

- **Decision**: when a change has `wipeMods: true` (always for Remove; see OD-7 for switching
  packs), the operator adds a one-shot init container step that empties the active loader's
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
  annotation is then removed. An in-place container crash restart inside that window still
  boots with the old env; accepted and documented.
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
  `missingFiles`. Only CurseForge packs have this case.
- **Alternatives**: parse the container log (lines scroll away, harder to bound).

## R8: FTB registry engine (OD-6 B, FR-012)

- **Facts** (live calls 2026-11-16, no key): `GET
  https://api.feed-the-beast.com/v1/modpacks/public/modpack/search/{limit}?term=…` returns
  pack ids only (`packs`, plus `curseforge` ids); `GET …/modpack/{id}` returns name, slug,
  art, authors, installs and `versions[]` each with `targets` (`game` = Minecraft version,
  `modloader` = `neoforge`/`forge`/`fabric`/`quilt` + version, `runtime` = Java) and
  `type` (release/beta/alpha).
- **Decision**: new keyless engine `api/internal/registry/ftb.go` on the shared
  `netguard.IsPublic` client. Search resolves up to 20 ids with bounded concurrency (4),
  caches pack details for 10 minutes, and keeps only packs with at least one version whose
  targets match the server's Minecraft version and loader. Versions lists only matching
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
