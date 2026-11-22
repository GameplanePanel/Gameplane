# Quickstart: validating spec 035

Validation runs in CI (constitution VI). This guide lists what the E2E and envtest suites
prove and how to repeat a scenario by hand on a dev cluster (`make dev-up`).

## Prerequisites

- Dev cluster with the branch's images (`make images dev-load dev-install`) and the updated
  minecraft-java module pushed to the dev registry (`make modules-push`).
- A CurseForge API key saved in Admin → Mod registries (browsing only; installs on Java 17+
  images use the image's built-in key).
- A Fabric or Forge server on Minecraft 1.21.x with at least 4 GB memory.

## Scenarios

| # | Scenario | Steps | Expected | Spec |
|---|---|---|---|---|
| 1 | CurseForge install | Modpacks → CurseForge → search a small Fabric pack → Install → keep newest → confirm without backup | `GET /servers/{name}/modpack` goes `Applying` → `Applied`; pod env has `TYPE=AUTO_CURSEFORGE`, `CF_SLUG`, `CF_FILE_ID`; server Running | US1, FR-003, FR-006 |
| 2 | Pin holds | Restart the server 3 times | `.curseforge-manifest.json` `fileId` unchanged | SC-002 |
| 3 | Switch registry | Install a Modrinth pack on the same server | Pod env has no `CF_*`/`FTB_*` names; only Modrinth env | FR-004, SC-004 |
| 4 | Remove | Active card → Remove → confirm | mods folder empty, world folder unchanged, `spec.modpack` absent, plain loader boots | US3, Q1 |
| 5 | Backup first | Server with a backup destination → change version with "Back up first" | Backup CR `<gs>-modpack-<gen>` Succeeded before env changes; break the destination → `BackupFailed`, env unchanged | FR-013 |
| 6 | Downgrade | Pick an older version | Confirmation shows the downgrade warning; server boots on the older file | Q4 |
| 7 | Blocked files | Install a pack known to contain a no-third-party-download mod | Card lists the files with download links; upload to `downloads/`, restart → Applied | US4, FR-008 |
| 8 | FTB | Modpacks → FTB → small pack → Install | `TYPE=FTBA`, `FTB_MODPACK_ID`, `FTB_MODPACK_VERSION_ID`; server Running | US5, FR-012 |
| 9 | Key sharing | Admin → share key on; Minecraft 1.16.5 Forge server → CurseForge pack | `<gs>-curseforge-key` Secret owned by the server; game container env `CF_API_KEY` from `valueFrom.secretKeyRef` on that Secret; `kubectl get pod -o yaml` shows the secretKeyRef name only, no key value; sharing off → copy deleted and env removed | FR-005, SC-003 |
| 10 | Legacy adoption | Server created with `MODRINTH_MODPACK` in `spec.env` by the old API → upgrade panel | `spec.modpack.version` = installed `versionId`, pod not restarted, pin env appears after the next restart | Q2 |
| 11 | Picker filter | Open versions on a 1.20.1 Forge server | only 1.20.1 + Forge versions listed | Q5 |

## Automated coverage

- envtest (operator): env rendering rules 1–2, state machine incl. `BackupFailed`, wipe init
  step and marker, key copy lifecycle, adopted-pack no-roll check.
- envtest (api): routes in `contracts/api-modpack.md`, RBAC, audit entries, key never in
  bodies.
- unit (api/registry): FTB engine against recorded fixtures; CurseForge modpack version
  filtering; `MODS_NEED_DOWNLOAD.txt` parser.
- web (vitest): Modpacks tab states from `contracts/ui-modpacks-tab.md`.
- E2E (`test/e2e/`, bucket `api-mods`): scenarios 1, 3, 4 with a small CurseForge Fabric pack
  installed by slug + file id through the API (no browse key needed in CI, image built-in key).
  Scenario 8 (FTB) and 7 (blocked files) are written but excluded from CI buckets with a
  comment (pack size and manual download), runnable on demand.
