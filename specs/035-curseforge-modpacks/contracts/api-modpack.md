# Contract: modpack REST API (api/internal/handlers/registry.go)

All routes are under the existing server scope and use the same RBAC as the Modpacks tab
today: reads need server read, writes need server manage (`mods:manage` on the server).
Every write is audit-logged with action `server.modpack.install|change|remove`, the
provider, project and version. Never in any response or audit entry: the CurseForge key.

## GET /servers/{name}/mods/registry/providers (changed)

Adds two fields per provider:

```json
{ "provider": "ftb", "available": true, "modpacks": true, "modsBrowse": false, "modpacksBlockedReason": "" }
```

- `modsBrowse` false for `modpacksOnly` providers (Mods tab hides them).
- `modpacksBlockedReason`: `"curseforge_key_not_shared"` when the selected version's image
  is in `needsKeyImageTags` and sharing is off. The Modpacks switch hides such providers.

## GET /servers/{name}/mods/registry/projects/{project}/versions?provider=…&type=modpack (changed)

`type=modpack` makes FTB and CurseForge return pack versions. Only versions matching the
server's selected Minecraft version and loader are returned, newest first (Clarification Q5).

## GET /servers/{name}/modpack (new)

```json
{
  "active": { "provider": "curseforge", "project": "all-the-mods-10", "version": "6512345",
              "title": "All the Mods 10", "versionName": "ATM10-2.41", "pinned": true },
  "phase": "Applied",
  "message": "",
  "backupName": "",
  "keyShared": false,
  "missingFiles": [
    { "mod": "Some Mod", "versionName": "1.2.3", "filename": "somemod-1.2.3.jar",
      "downloadPage": "https://www.curseforge.com/minecraft/mc-mods/some-mod/download/123" }
  ],
  "missingFilesUnknown": false,
  "downloadsPath": "downloads/",
  "backupAvailable": true
}
```

- `active` null when no pack. `pinned` false = "Not pinned" (adopted legacy pack).
  While a removal is pending (remove annotation set, `status.modpack.applied` still set),
  `active` is the applied pack with `"pendingRemoval": true`.
- `missingFiles` read live from `missingFilesPath` through the agent sidecar. The agent runs
  beside the game container and mounts the data volume, so the list stays readable while
  the game container exits or crash-loops on the blocked download. Empty when the file is
  absent or the provider has no `missingFilesPath`.
- `missingFilesUnknown` true when no pod exists (server stopped or asleep), so the file
  cannot be read; the tab then says to start the server to check for missing files.
- `backupAvailable` true when the server has a backup destination.
- Triggers the lazy legacy adoption (research R6) when needed.

## POST /servers/{name}/modpack (changed)

Request:

```json
{ "provider": "curseforge", "project": "all-the-mods-10", "version": "6512345",
  "title": "All the Mods 10", "versionName": "ATM10-2.41", "backupFirst": true }
```

- `version` required (400 `server.modpack.version_required`).
- Same provider + project as the active pack = version change; else install. Neither
  empties the mods folder (OD-7 B); only DELETE does.
- 409 `server.modpack.key_required` when the provider is blocked for this server (see
  `modpacksBlockedReason`).
- 400 `server.modpack.backup_unavailable` when `backupFirst` and no destination.
- 409 `server.modpack.change_in_progress` while `phase` is `BackingUp` or `Applying`.
- 202 with the GET body. Writes `spec.modpack` only; the operator applies it.

## DELETE /servers/{name}/modpack?backupFirst=true|false (new)

Removes the pack: deletes `spec.modpack` and sets the `gameplane.local/modpack-remove`
annotation with a new request id. Allowed when `spec.modpack` is set, or when a removal
failed (`status.modpack.applied` set, remove annotation present, `phase=BackupFailed`):
that retry replaces the annotation, so the operator runs the removal again. 404 only when
neither `spec.modpack` nor `status.modpack.applied` is set; same 400/409 rules as POST.
202 with the GET body.

## PUT /admin/registries/curseforge/share (new, admin only)

`{ "share": true }` sets/clears the share annotation on the managed key Secret. 409
`server.registry.share_needs_managed_key` when no managed key exists (only the startup flag).
GET on the same path returns `{ "share": bool, "managedKey": bool }`.
