---
description: "Task list for spec 035: CurseForge and FTB modpacks for Minecraft"
---

# Tasks: CurseForge and FTB modpacks for Minecraft

**Input**: `specs/035-curseforge-modpacks/` (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md)

**Tests**: required. Constitution I needs E2E for every feature; envtest and unit tests are the supporting layers. Tests run in CI only (constitution VI); locally only `go build ./...` and `npx tsc --noEmit`.

**Organization**: by user story (US1–US5 from spec.md). Every story phase that touches the dashboard depends on the Pencil design tasks (T011–T013) being approved first (constitution II).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1–US5 from spec.md
- Module repo paths are relative to `GameplanePanel/module` (the `modules/` submodule); everything else is relative to the Gameplane repo root.

---

## Phase 1: Setup

**Purpose**: confirm the upstream facts the design rests on before changing types.

- [ ] T001 Re-check research.md R1, R2 and R7 against the itzg image digests pinned in modules/minecraft-java/template.yaml (env names `AUTO_CURSEFORGE`, `CF_SLUG`, `CF_FILE_ID`, `CF_API_KEY_FILE`, `CF_DOWNLOADS_REPO`, `MODRINTH_VERSION`, `FTBA`, `FTB_MODPACK_ID`, `FTB_MODPACK_VERSION_ID`; manifest files `.modrinth-modpack-manifest.json`, `.curseforge-manifest.json`; `MODS_NEED_DOWNLOAD.txt`; built-in key present on java17/java21/java25 and absent on java8-multiarch); record any difference in specs/035-curseforge-modpacks/research.md and stop if one breaks the plan
- [X] T002 [P] Record recorded-response fixtures for the FTB API (search, pack detail, version detail) and a CurseForge modpack files response (class 4471) in api/internal/registry/testdata/ftb/ and api/internal/registry/testdata/curseforge/modpack-files.json

---

## Phase 2: Foundational (blocking)

**Purpose**: CRD shape, operator env rendering, module declaration and design. Nothing in Phase 3+ starts before T003–T010; web tasks also wait for T011–T013.

- [X] T003 Add `ftb` to the `ModProvider.provider` enum, add `ModProvider.modpacksOnly`, and add `ModpackSpec.versionEnv`, `apiKeyFileEnv` (CEL: only when provider is curseforge), `needsKeyImageTags`, `missingFilesPath`, `adoptVersionFile{path,field}` with the validation in data-model.md to operator/api/v1alpha1/gametemplate_types.go
- [X] T004 Add `GameServerSpec.Modpack *GameServerModpackSpec` (provider, project, version, title, versionName, backupFirst) and `GameServerStatus.Modpack` (observedGeneration, phase enum, applied, backupName, message, keyShared) plus annotation constants `gameplane.local/modpack-adopted` and `gameplane.local/modpack-remove` to operator/api/v1alpha1/gameserver_types.go
- [X] T005 Run `make generate && make manifests`; commit operator/api/v1alpha1/zz_generated.deepcopy.go, operator/config/crd/, operator/config/rbac/, charts/gameplane/charts/gameplane-crds/crds/ and charts/gameplane/crd-manifests/ with T003–T004 (core rule 7)
- [X] T006 Implement env rendering rule 1 of contracts/operator-modpack.md (strip every modpack-declared env name from the merged env, then add the applied provider's fixed env, `refEnv=project`, `versionEnv=version`) in new operator/internal/controller/gameserver_modpack.go and call it from the env build in operator/internal/controller/gameserver_controller.go
- [X] T007 Implement the change state machine without backups (`Pending → Applying → Applied`, `status.modpack.applied`, `ModpackReady` condition) in operator/internal/controller/gameserver_modpack.go
- [X] T008 [P] envtest: rendering strips stale `MODRINTH_MODPACK`/`CF_*`/`FTB_*` from `spec.env`, renders each provider's set, and `ModpackReady` reaches True, in operator/internal/controller/gameserver_modpack_envtest_test.go
- [ ] T009 [P] Update the minecraft-java template to the providers block in data-model.md (modrinth, curseforge, ftb, hangar), raise `gameplaneMinVersion` to the release that ships this spec and bump the module minor version in modules/minecraft-java/template.yaml and modules/minecraft-java/module.yaml (module repo PR; root pointer bump only after it merges)
- [X] T010 Replace the `spec.env` patch in `installModpack` with a `spec.modpack` write and add `GET /servers/{name}/modpack` (active, phase, message, backupName, keyShared, backupAvailable; missingFiles empty for now) per contracts/api-modpack.md in api/internal/handlers/registry.go
- [X] T011 Write the local Pencil prompt for the six surfaces in contracts/ui-modpacks-tab.md (repo `local-prompt` skill) and save it as notes/design-prompts/035-modpacks-tab.md in the project files
- [ ] T012 (valgul's machine, pencil-designer) Design the six surfaces in design.pen and export touched nodes to assets/design-export/json/ and assets/design-export/screenshots/ in the same commit
- [ ] T013 (valgul) Approve the T012 screenshots; record the approval in this file

**Checkpoint**: CRDs regenerated, operator renders pack env from `spec.modpack`, module declares all three registries, design approved.

---

## Phase 3: User Story 1 - Install a CurseForge modpack (P1) 🎯 MVP

**Goal**: CurseForge appears in the Modpacks switch when a key is configured, and installing a pack boots the server on it. Includes key sharing for images without a built-in key (FR-005).

**Independent test**: with a CurseForge key in admin config, install a small Fabric pack on a 1.21.x server; the server reaches Running with the pack's mods.

### Tests for US1

- [X] T014 [P] [US1] Unit test CurseForge modpack versions filtered by game version and loader using the T002 fixture in api/internal/registry/curseforge_test.go
- [X] T015 [P] [US1] envtest: POST install writes `spec.modpack`, needs manage permission, writes an audit entry without the key, returns 409 `server.modpack.key_required` on a java8-multiarch version while sharing is off, in api/internal/handlers/registry_modpack_envtest_test.go
- [ ] T016 [P] [US1] envtest: share on → `<gs>-curseforge-key` owned by the GameServer, mounted read-only at `/run/gameplane/curseforge/api-key` in the game container only, `CF_API_KEY_FILE` set, no key value in the pod spec, no config-hash change on key rotation; share off or key deleted → copy and mount removed, in operator/internal/controller/gameserver_modpack_key_envtest_test.go
- [ ] T017 [US1] E2E: install a small CurseForge Fabric pack by slug + file id through the API (image built-in key, no browse key in CI), assert `Applied`, Running and the pack env; then install a Modrinth pack and assert no `CF_*` env remains (SC-004); one admin login; register in a bucket with login headroom per the comments in test/e2e/buckets.sh (not api-auth, api-roles, api-mods, api-rbac, api-agent) in test/e2e/modpack_e2e_test.go

### Implementation for US1

- [X] T018 [US1] Make CurseForge `Versions` honour `type=modpack` (class 4471 files, game version + loader filter) and update the `ModpackDeps` comments in api/internal/registry/curseforge.go
- [X] T019 [US1] Add `modsBrowse` and `modpacksBlockedReason` to the providers listing (needsKeyImageTags vs the selected version's image tag and the share annotation) in api/internal/handlers/registry.go
- [X] T020 [US1] Add `PUT/GET /admin/registries/curseforge/share` (annotation on the managed key Secret; 409 `server.registry.share_needs_managed_key` when only the startup flag holds a key) in api/internal/handlers/registry_secret.go and mount it with admin RBAC in api/cmd/main.go
- [ ] T021 [US1] Implement key sharing (watch the managed key Secret in the control namespace, maintain the server-owned copy, mount, `apiKeyFileEnv`, `status.modpack.keyShared`, kept out of the config hash) per contracts/operator-modpack.md item 5 in operator/internal/controller/gameserver_modpack_key.go, with kubebuilder RBAC markers, then `make manifests`
- [X] T022 [P] [US1] Add the new API message ids (`server.modpack.*`, `server.registry.share_needs_managed_key`) to i18n/locales/en/app.json
- [ ] T023 [US1] Add `Servers.modpack`, `Servers.installModpack` (new body), `Admin.curseforgeShare` and the new types to web/src/lib/api.ts and web/src/types.ts
- [ ] T024 [US1] Filter the Mods tab to `modsBrowse` providers and the Modpacks switch to unblocked modpack providers in web/src/components/registry-browser.tsx
- [ ] T025 [US1] Build the install flow from the approved design (registry switch with CurseForge, version picker defaulting to newest, confirmation) in web/src/routes/tabs/Modpacks.tsx and new web/src/components/modpack-version-picker.tsx, web/src/components/modpack-confirm-dialog.tsx
- [ ] T026 [US1] Build the "Share the CurseForge key with game servers" switch from the approved design in web/src/routes/AdminSettings.tsx
- [ ] T027 [P] [US1] vitest for the install flow and the share switch in web/src/routes/tabs/Modpacks.test.tsx and web/src/routes/AdminSettings.test.tsx

**Checkpoint**: US1 deliverable on its own: CurseForge packs install and boot; Modrinth install keeps working through `spec.modpack`.

---

## Phase 4: User Story 2 - Choose and keep a pack version (P2)

**Goal**: pinned installs for all registries, downgrade warning, legacy Modrinth adoption without restart, optional backup before any pack change (FR-013).

**Independent test**: install an older file, restart twice, installed version unchanged; change version with "Back up first" and see the backup finish before the env changes.

### Tests for US2

- [X] T028 [P] [US2] envtest: `backupFirst` → Backup CR `<gs>-modpack-<generation>` owned by the GameServer, env unchanged until it succeeds; failed backup → `BackupFailed`, message set, env unchanged; a new change after `BackupFailed` restarts the flow, in operator/internal/controller/gameserver_modpack_envtest_test.go
- [X] T029 [P] [US2] envtest: adopted legacy server (annotation set, pod running) and a not-yet-swept legacy server (`refEnv` value in `spec.env`, no `spec.modpack`) both keep an identical pod template (no roll) while players are online; an adopted server with a version gets the pin with one restart once `status.agent.playersOnline` is 0, or right away when stopped or asleep (OD-8 B); after an operator-driven restart the pin env is rendered and the annotation removed, in operator/internal/controller/gameserver_modpack_envtest_test.go
- [X] T030 [P] [US2] envtest: legacy sweep reads `versionId` through the agent and writes `spec.modpack` + adopted annotation; unreadable → no `version` and `pinned: false`; 400 `server.modpack.backup_unavailable`; 409 `server.modpack.change_in_progress`, in api/internal/handlers/modpack_adopt_envtest_test.go
- [ ] T031 [US2] E2E: pinned version survives 3 restarts (read `.curseforge-manifest.json` `fileId` through the agent) in test/e2e/modpack_e2e_test.go

### Implementation for US2

- [X] T032 [US2] Add the backup step to the state machine (`BackingUp`, Backup CR creation and watch, `BackupFailed`) in operator/internal/controller/gameserver_modpack.go, with RBAC markers and `make manifests`
- [X] T033 [US2] Implement adopted-pack rendering (rule 2 of contracts/operator-modpack.md) in operator/internal/controller/gameserver_modpack.go
- [X] T034 [US2] Implement the legacy sweep at API startup plus the lazy retry on `GET /servers/{name}/modpack` (research R6) in new api/internal/handlers/modpack_adopt.go and wire it in api/cmd/main.go
- [X] T035 [US2] Validate `version` required, `backupFirst` vs destination, change-in-progress on POST in api/internal/handlers/registry.go
- [ ] T036 [US2] Build version change, older-version marking, downgrade warning and the "Back up first" checkbox from the approved design in web/src/components/modpack-version-picker.tsx and web/src/components/modpack-confirm-dialog.tsx
- [ ] T037 [P] [US2] vitest for downgrade warning and backup checkbox states (destination / no destination) in web/src/components/modpack-confirm-dialog.test.tsx

**Checkpoint**: versions are pinned for every registry; legacy servers are adopted without restarting while players are online (OD-8 B).

---

## Phase 5: User Story 3 - See and remove the active pack (P2)

**Goal**: Active modpack card with phase, Change version and Remove; Remove empties the mods folder (Q1; switching packs does not, OD-7 B).

**Independent test**: install a pack, see it on the card, remove it; the server restarts as a plain loader server with an empty mods folder and its world intact.

### Tests for US3

- [X] T038 [P] [US3] envtest: DELETE removes `spec.modpack`, sets the remove annotation with a request id, accepts a retry after a removal's backup failed, returns 404 only when neither `spec.modpack` nor `status.modpack.applied` is set, GET shows `pendingRemoval`, writes an audit entry, in api/internal/handlers/registry_modpack_envtest_test.go
- [X] T039 [P] [US3] envtest: removal adds the one-shot wipe init step for the active loader's mods mount only, guarded by `.gameplane-mods-wiped-<generation>`; install of a different pack adds no wipe step, in operator/internal/controller/gameserver_modpack_envtest_test.go
- [ ] T040 [US3] E2E: remove the pack; mods folder empty, world folder unchanged, server Running as plain loader, in test/e2e/modpack_e2e_test.go

### Implementation for US3

- [X] T041 [US3] Add `DELETE /servers/{name}/modpack?backupFirst=` per contracts/api-modpack.md in api/internal/handlers/registry.go
- [X] T042 [US3] Implement removal handling (annotation consume, optional backup, wipe init step with marker, annotation cleared once applied) in operator/internal/controller/gameserver_modpack.go
- [ ] T043 [US3] Build the Active modpack card (title, registry, version or "Not pinned", phase and message, Change version, Remove with the "every mod is deleted, including ones added by hand" confirmation) from the approved design in new web/src/components/modpack-active-card.tsx and web/src/routes/tabs/Modpacks.tsx
- [ ] T044 [P] [US3] vitest for card states (Applied, BackingUp, Applying, BackupFailed, Not pinned, read-only) in web/src/components/modpack-active-card.test.tsx

**Checkpoint**: operators can see and remove packs from any registry.

---

## Phase 6: User Story 4 - Packs with mods that block automatic download (P3)

**Goal**: list files that need a manual download and point to `downloads/`.

**Independent test**: install a pack with a blocked mod; the card lists its files with links; upload to `downloads/`, restart, server starts.

### Tests for US4

- [X] T045 [P] [US4] Unit test the `MODS_NEED_DOWNLOAD.txt` parser (rows, 2+ space columns, URL last, non-CurseForge URLs dropped, size cap) with a captured sample, and `missingFilesUnknown` when no pod exists, in api/internal/handlers/modpack_missing_test.go
- [ ] T046 [US4] E2E (written, excluded from every CI bucket with a comment: needs a manual browser download) for a pack with a blocked mod in test/e2e/modpack_blocked_e2e_test.go and test/e2e/buckets.sh

### Implementation for US4

- [X] T047 [US4] Implement the parser and fill `missingFiles` + `downloadsPath` from `missingFilesPath` via the agent files API in new api/internal/handlers/modpack_missing.go and api/internal/handlers/registry.go
- [ ] T048 [US4] Build the missing files notice from the approved design in new web/src/components/modpack-missing-files.tsx and web/src/routes/tabs/Modpacks.tsx
- [ ] T049 [P] [US4] vitest for the notice in web/src/components/modpack-missing-files.test.tsx

**Checkpoint**: blocked-file packs are usable with the Files tab.

---

## Phase 7: User Story 5 - Install an FTB modpack (P3)

**Goal**: FTB registry in the Modpacks switch, keyless, pinned installs.

**Independent test**: install a small FTB pack on a Forge server; it reaches Running on that version.

### Tests for US5

- [X] T050 [P] [US5] Unit test the FTB engine with the T002 fixtures (search resolves ids in batches, filters before the page limit so a match after the first 20 candidates is found, stops at 100 details, cache, bounded concurrency, upstream error) in api/internal/registry/ftb_test.go
- [X] T051 [P] [US5] Add an FTB case to the dial-guard test in api/internal/registry/registry_dialguard_test.go
- [ ] T052 [US5] E2E (written, excluded from every CI bucket with a comment: FTB packs exceed CI runner budget) installing a small FTB pack in test/e2e/modpack_ftb_e2e_test.go and test/e2e/buckets.sh

### Implementation for US5

- [X] T053 [US5] Implement the keyless FTB engine (research R8) in new api/internal/registry/ftb.go
- [X] T054 [US5] Register `ftb` in the engine set and `For()` in api/internal/registry/registry.go
- [ ] T055 [US5] Show FTB in the Modpacks switch (no new screens; uses the US1 flow) and add its label to i18n/locales/en/app.json and web/src/routes/tabs/Modpacks.tsx

**Checkpoint**: all three registries install pinned packs.

---

## Phase 8: Polish & cross-cutting

- [ ] T056 [P] Update operator/specs.md (spec.modpack rendering, state machine, wipe, key copies)
- [ ] T057 [P] Update api/specs.md (modpack routes, FTB engine, adoption sweep, share toggle)
- [ ] T058 [P] Update web/specs.md (Modpacks tab surfaces, admin share switch)
- [ ] T059 [P] Update modules/minecraft-java/specs.md and modules/minecraft-java/README.md (three registries, pinning, downloads folder, java8 key note) in the module repo
- [ ] T060 [P] Document `versionEnv`, `apiKeyFileEnv`, `needsKeyImageTags`, `missingFilesPath`, `adoptVersionFile`, `modpacksOnly` and `ftb` in docs/module-authoring.md
- [ ] T061 [P] Add the shared-key residual risk (research R2) to docs/security.md
- [ ] T062 [P] Add a CHANGELOG entry for CurseForge and FTB modpacks in CHANGELOG.md
- [ ] T063 Bump the modules submodule pointer after the module PR merges (`submodule-bump` skill), in modules
- [ ] T064 Walk quickstart.md scenarios 1–11 against the CI results and a dev cluster if valgul provides one; record results in specs/035-curseforge-modpacks/quickstart.md

---

## Dependencies & execution order

- Phase 1 → Phase 2. T003–T004 → T005 → T006 → T007 → T008. T009 needs T005 (CRD enum). T010 needs T005.
- T011 → T012 → T013 (design, valgul's machine) blocks every web task: T025, T026, T036, T043, T048, T055.
- US1 (Phase 3) needs Phase 2. US2, US3, US4, US5 need Phase 2 and US1's API/web plumbing (T019, T023, T025); otherwise they are independent of each other.
- Backup (T032) is shared by US2 and US3 removal (T042).
- Polish after the stories it documents; T063 only after the module PR merges.

## Parallel examples

- Phase 2: T008 and T009 together once T007 is done.
- US1: T014, T015, T016 and T022 together; then T018–T021 (different files) in parallel.
- US2: T028, T029, T030 together.
- US5: T050 and T051 together; T053 then T054.
- Polish: T056–T062 all together.

## Implementation strategy

1. MVP = Phases 1–3 (US1): CurseForge installs on Java 17+ servers, existing Modrinth flow moved onto `spec.modpack`.
2. Add US2 (pinning, backups, legacy adoption), then US3 (card, remove).
3. US4 and US5 last; both are P3 and independent.
4. One Gameplane branch and PR (`spec-035/curseforge-modpacks`) plus one module PR and its pointer bump, unless valgul asks for stacked PRs. Implementation runs as Workflow scripts (haiku first, tier-up review).
