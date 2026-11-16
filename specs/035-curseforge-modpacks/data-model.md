# Data model: spec 035 CurseForge and FTB modpacks

No database tables change. All state lives on Kubernetes objects.

## GameTemplate: `spec.capabilities.mods.registry.providers[]` (CRD change)

`ModProvider.provider` enum gains `ftb`.

`ModpackSpec` (existing: `refEnv`, `env`) gains:

| Field | Type | Rules | Meaning |
|---|---|---|---|
| `versionEnv` | string | max 64, env-name pattern | Env the operator sets to the pinned version id (`MODRINTH_VERSION`, `CF_FILE_ID`, `FTB_MODPACK_VERSION_ID`). Required for env-mode packs that support pinning; when empty the registry is not offered for pinned install. |
| `apiKeyFileEnv` | string | max 64, env-name pattern, only valid when `provider: curseforge` | Env that receives the mounted key path when key sharing is on (`CF_API_KEY_FILE`). |
| `needsKeyImageTags` | []string | max 8 items, each max 64 | Image tags (the part after `:` and before `@`) that have no built-in key. Servers on these images get the provider only while key sharing is on. |
| `missingFilesPath` | string | max 128, relative path, no `..` | File (relative to the data mount) where the image lists files needing a manual download (`MODS_NEED_DOWNLOAD.txt`). |
| `adoptVersionFile` | object {`path`, `field`} | path relative, no `..`; field max 64 | Where the image records the installed version, used to adopt legacy unpinned packs (`.modrinth-modpack-manifest.json` / `versionId`). |

`ModProvider` gains `modpacksOnly` (bool): the provider is hidden in the Mods tab (FTB).

## GameServer `spec.modpack` (new, optional; CRD change)

| Field | Type | Rules |
|---|---|---|
| `provider` | string | enum of the template's providers that declare `modpacks`; required |
| `project` | string | 1–128, `^[A-Za-z0-9._-]+$`; slug (Modrinth, CurseForge) or numeric id (FTB) |
| `version` | string | 0–64, `^[A-Za-z0-9._-]*$`; empty only for adopted packs whose version could not be read ("Not pinned") |
| `title` | string | 0–128, display only |
| `versionName` | string | 0–128, display only |
| `backupFirst` | bool | honoured only when a backup destination exists |

Absent `spec.modpack` = no pack. Remove deletes `spec.modpack` and sets the annotation
`gameplane.local/modpack-remove` (below), which tells the operator whether to back up first.
The operator always empties the mods folder on removal (Clarification Q1).

Annotations:

- `gameplane.local/modpack-adopted: "true"`: set by the legacy sweep (research R6); the operator renders the legacy env until the next pod start it drives, then removes it.
- `gameplane.local/modpack-remove`: JSON `{"backupFirst": bool}`; set by Remove, cleared by the operator once applied.

## GameServer `status.modpack` (new)

| Field | Type | Meaning |
|---|---|---|
| `observedGeneration` | int64 | generation of the last change handled |
| `phase` | enum `Pending`, `BackingUp`, `Applying`, `Applied`, `BackupFailed` | progress of the current change |
| `applied` | object (same shape as `spec.modpack` without `backupFirst`) or null | what the pod renders now |
| `backupName` | string | Backup CR created for this change |
| `message` | string, max 512 | human-readable reason for `BackupFailed` |
| `keyShared` | bool | the pod currently mounts a shared CurseForge key |

Missing-download files are NOT stored in status (the API reads them live, research R7).

### State transitions

```text
spec.modpack changes (generation N)
  ├─ backupFirst && destination exists → BackingUp ── Backup Succeeded ──→ Applying → Applied
  │                                         └─ Backup Failed ──→ BackupFailed (applied unchanged)
  └─ otherwise ───────────────────────────────────────────────→ Applying → Applied
BackupFailed + new spec change → starts again from the top
```

`Applying` = operator renders the new env (and the one-shot wipe init step when asked); the StatefulSet rolls. `Applied` = rollout done.

## Secrets

- `gameplane-modreg-curseforge` (existing, control namespace): gains annotation
  `gameplane.local/share-with-game-servers: "true"` when the admin opts in.
- `<gs>-curseforge-key` (new, server namespace, owned by the GameServer, label
  `gameplane.local/curseforge-key-copy: "true"`): key `apiKey`. Exists only while sharing is
  on and the applied pack is CurseForge.

## Module (`GameplanePanel/module`, `minecraft-java/template.yaml`)

```yaml
providers:
  - provider: modrinth
    modpacks:
      refEnv: MODRINTH_MODPACK
      versionEnv: MODRINTH_VERSION
      adoptVersionFile: { path: .modrinth-modpack-manifest.json, field: versionId }
      env: [{ name: TYPE, value: MODRINTH }]
  - provider: curseforge
    curseforgeGameID: 432
    modpacks:
      refEnv: CF_SLUG
      versionEnv: CF_FILE_ID
      apiKeyFileEnv: CF_API_KEY_FILE
      needsKeyImageTags: [java8-multiarch, java8]
      missingFilesPath: MODS_NEED_DOWNLOAD.txt
      env:
        - { name: TYPE, value: AUTO_CURSEFORGE }
        - { name: CF_DOWNLOADS_REPO, value: /data/downloads }
  - provider: ftb
    modpacksOnly: true
    modpacks:
      refEnv: FTB_MODPACK_ID
      versionEnv: FTB_MODPACK_VERSION_ID
      env: [{ name: TYPE, value: FTBA }]
  - provider: hangar
```

`gameplaneMinVersion` rises to the first Gameplane release that ships this spec; module
version gets a minor bump.
