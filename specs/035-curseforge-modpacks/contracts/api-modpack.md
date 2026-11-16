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
  "downloadsPath": "downloads/",
  "backupAvailable": true
}
```

- `active` null when no pack. `pinned` false = "Not pinned" (adopted legacy pack).
- `missingFiles` read live from `missingFilesPath` via the agent; empty when the file is
  absent, the server is not running, or the provider has no `missingFilesPath`.
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

Removes the pack: deletes `spec.modpack`, sets the `gameplane.local/modpack-remove`
annotation. 404 when no pack; same 400/409 rules as POST. 202 with the GET body.

## PUT /admin/registries/curseforge/share (new, admin only)

`{ "share": true }` sets/clears the share annotation on the managed key Secret. 409
`server.registry.share_needs_managed_key` when no managed key exists (only the startup flag).
GET on the same path returns `{ "share": bool, "managedKey": bool }`.
