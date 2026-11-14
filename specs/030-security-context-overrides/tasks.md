# Tasks: Module security defaults with a per-server override

**Input**: Design documents from `specs/030-security-context-overrides/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md), [OPEN-DECISIONS.md](./OPEN-DECISIONS.md) (OD-1 to OD-6 all settled)

**Tests**: requested. Constitution I requires a Kind E2E on the real path; each component also gets unit and envtest tests under its coverage gate (operator 72, api 80, gp-module 80, mcp-server 70, web 92/76/82/92). Tests are written first and fail in CI until the implementation in the same PR lands.

**Organization**: one phase per user story in spec priority order (US1 P1, US2 P1, US3 P2, US4 P2). Stories share files (`gameserver_security.go`, `SecurityAdvanced.tsx`, the two E2E files), so the phases build on each other in order; see "Dependencies & Execution Order". Backend, operator, API, module and docs work never waits for the dashboard design.

**Binding rules for every task** (repeat them in every Workflow script and agent brief, CLAUDE.md rules 4, 6-8, 11-14, 18):

- Locally only compile checks: `go build ./...` in `operator`, `api`, `gp-module`, `mcp-server`; `go vet -tags e2e ./...` in `test/e2e`; `npx tsc --noEmit` in `web`; `python3 -m py_compile` for Python. Never run `make test|lint|cover`, `go test`, `npm test`, envtest, `i18n-check` or E2E locally; CI is the only verifier.
- No `//nolint`, `eslint-disable`, `# noqa` or loosened config. Wrap Go errors with `%w`. Strict TypeScript, every promise awaited or `void`-prefixed. No test is deleted or weakened.
- Never open, read, grep or edit a `.pen` file. The design task (T003) is the maintainer's, through the Pencil MCP.
- Every CRD edit ends with `make generate && make manifests`, and the deepcopy, `operator/config/crd/*.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml` and `charts/gameplane/crd-manifests/*.yaml` are committed in the same changeset.
- Dashboard text only through the shared language packs (`i18n/locales/en/app.json`, then `fr` per `docs/translating.md`); API refusals through `api/internal/messages/`.
- Signed commits (`git commit -s`), conventional prefix, `Co-Authored-By` trailer; never amend after push; one branch per unit of work. PR labels via REST (`type: feature`, `area: operator|api|web|modules|chart|e2e|specs|shared` as touched, never `gh pr edit`).
- Implementation runs through Workflow scripts with `model:` set on every agent, starting at haiku, review one tier up on the diff, scout once and brief many (T001).
- Unsettled values never go into a contract; anything new goes to `OPEN-DECISIONS.md` and pauses the task.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US4 from spec.md; setup, foundational and polish tasks carry none
- **MAINTAINER-OWNED**: only valgul does it, on his machine

## Path Conventions

Repository root of `GameplanePanel/Gameplane` unless a task says `module repo` (`GameplanePanel/module`, local clone `/home/user/module`, checked out in this repo at `modules/`). Main files: `operator/api/v1alpha1/{gameserver,gametemplate}_types.go`, `operator/internal/controller/`, `api/internal/{rbac,handlers,messages}/`, `web/src/`, `gp-module/internal/`, `test/e2e/`, `charts/gameplane/`, `docs/`.

---

## Phase 1: Setup

**Purpose**: prerequisites shared by every phase, including the design gate that blocks all dashboard work.

- [ ] T001 Scout once (CLAUDE.md rule 18): read the code named in plan.md "Source Code" and write the factual brief `specs/030-security-context-overrides/briefs/security-overrides.md` with exact `file:line`, before/after snippets and the justification for every edit below (builders `gameContainerSecurityContext`, `gamePodSecurityContext`, `buildConfigInitContainer`, the `reconcileStatefulSet` gate near `gameserver_controller.go:1889-1905`, `validateRestrictedTemplate` callers, backup and restore call sites, `validateAndProtectGameServer`, `newValidators`, the operator `Watches` list near `gameserver_controller.go:583`, the API Role in `charts/gameplane/templates/api.yaml`); also confirm which operator flag names the control-plane namespace the ConfigMap lives in (used by T057 and T060). Fix agents edit from this brief only
- [ ] T002 [P] Prepare the module repo: confirm `git submodule update --init modules` works, create a branch `spec-030/module-security` in `/home/user/module` (separate PR to `GameplanePanel/module`, default branch per that repo), and record in the brief that no module-repo file is edited on this repo's branch
- [ ] T003 [P] **MAINTAINER-OWNED (valgul, local machine, Pencil MCP). BLOCKS EVERY WEB TASK (T023, T024, T025, T029, T034, T035, T041, T045, T046, T052, T062, T063; each is tagged "Needs T003").** In `design.pen` design the dashboard frames, then export touched nodes to `assets/design-export/json/<id>.json` and `assets/design-export/screenshots/<id>.png` (skills `design-export`, `pen-componentize`, `pen-canvas-layout`): (a) the collapsed "Advanced" security section in the create-server wizard; (b) the same section on the server Settings tab; (c) states: module default, "image default", override field, "use image default" choice, applied value with source, pending value with the "applies on the next restart" notice, "no effect" marker for the ownership check without a filesystem group, read-only state without `servers:security`; (d) the file-access warning before saving a changed run-as user or group on a server with data; (e) the risky-value confirmation dialog showing the module's reason; (f) the inline refusal messages (forbidden, root disabled, invalid); (g) the notice for a server held back with reason `RootOverrideDisallowed` (stopped and running variants); (h) the Admin Settings "Security" section with the allow-root switch and its explanatory text. The maintainer saves in the Pencil UI and commits the exports; an agent may prepare the copy-paste prompt with the `local-prompt` skill but never touches the `.pen` file. Done when `assets/design-export/{json,screenshots}/` for those frames is in the branch and `assets/design-export/MANIFEST.md` lists them
- [ ] T004 [P] Confirm that the labels this feature's PRs need (`type: feature` and the `area:` labels) already exist with `gh api repos/GameplanePanel/Gameplane/labels`; no new label is expected

**Checkpoint**: brief written; design started (it runs in parallel with everything below until a web task needs it).

---

## Phase 2: Foundational (blocks every story)

**Purpose**: the CRD fields, the generated artifacts, the permission key with its field-level check, and the API messages. No override is ever accepted without the permission check, so these land before any story.

**CRITICAL**: no user story work begins until this phase is complete.

- [ ] T005 [P] Extend `GameSecuritySpec` in `operator/api/v1alpha1/gametemplate_types.go:185` with `volumeOwnershipCheck` (enum `EveryStart|OnlyWhenWrong`) and `risky` (new `SecurityRiskSpec`, each member a `SecurityRiskNote{reason string, MaxLength=200}`) per [contracts/crd-gametemplate-security.md](./contracts/crd-gametemplate-security.md)
- [ ] T006 [P] Add to `operator/api/v1alpha1/gameserver_types.go`: `GameServerSpec.Security *GameServerSecuritySpec` after `Capture` (`:161`) with `SecurityIDOverride{value *int64 0..4294967295, useImageDefault bool}` and the two CEL rules (exactly one of the two; `useImageDefault` must be `true`, messages as in [contracts/crd-gameserver-security.md](./contracts/crd-gameserver-security.md)); `GameServerStatus.Security *GameServerSecurityStatus` (`:558`) with `desired`, `applied`, `pending`, `SecuritySettings`, `SecurityIDStatus`, `SecurityCheckStatus` and the `source` enum (`ModuleDefault|Override|ImageDefaultOverride|ImageDefault|BuiltInDefault`); and the condition type/reason constants `SecurityAllowed` / `RootOverrideDisallowed` (data-model.md "Start refusal")
- [ ] T007 Run `make generate && make manifests` and commit `operator/api/v1alpha1/zz_generated.deepcopy.go`, `operator/config/crd/gameplane.local_gameservers.yaml`, `operator/config/crd/gameplane.local_gametemplates.yaml`, both `charts/gameplane/charts/gameplane-crds/crds/*.yaml` copies and both `charts/gameplane/crd-manifests/*.yaml` copies in the same change as T005 and T006 (CLAUDE.md rule 7); depends on T005, T006
- [ ] T008 [P] Add CEL and bounds cases to `operator/internal/controller/crd_validation_envtest_test.go`: `{value:1000}` and `{useImageDefault:true}` accepted; both, neither, `useImageDefault:false`, `value:-1`, `value:4294967296`, a bad `volumeOwnershipCheck`, and a `risky.reason` over 200 characters rejected; an existing GameServer without `security` still applies
- [ ] T009 [P] Add the catalog entry `{Key: "servers:security", Label: "Set or change a server's security overrides (run-as user and group, filesystem group, volume ownership check)", Namespaced: true}` after `servers:console` in `api/internal/rbac/catalog.go:25-29` ([contracts/rbac-permission.md](./contracts/rbac-permission.md)); do not add it to `privilegedPermissions`
- [ ] T010 [P] Add to `api/internal/rbac/catalog_test.go` and `api/internal/rbac/rbac_test.go` a test that `servers:security` exists, is namespaced, is held by the built-in `admin` role (through `*`) and by no other built-in role, and that `TestCatalog_CoversSeededPermissions` and `TestCatalog_CoversRuleTablePermissions` still pass
- [ ] T011 [P] Add the three messages `server.security.forbidden`, `server.security.root_disabled` (arg `setting`) and `server.security.invalid` (args `setting`, `detail`) to `api/internal/messages/entries_server.go` with English text from [contracts/api-server-security.md](./contracts/api-server-security.md), and the matching keys to `i18n/locales/en/app.json` and `i18n/locales/fr/app.json` (FR-014); the existing `messages_test.go` and `usage_test.go` require every ID to be used, so T012 uses them
- [ ] T012 Create `api/internal/handlers/resources_security.go` with the field-level security step called from `validateAndProtectGameServer` (`api/internal/handlers/resources.go:538`, after the capture step) for create (`:218`), update (`:290`) and clone (`lifecycle.go:243`): case 1 unchanged accepts without the permission, case 2 absent-in-request restores the live value, case 3 any other difference without `servers:security` returns 403 `server.security.forbidden`; errors carry their own status so `writeServerValidationError` (`resources_snapshot.go:24`) passes them through (research R10); depends on T006, T009, T011
- [ ] T013 [P] Unit tests in `api/internal/handlers/resources_security_test.go` for T012 cases 1 to 3 (unchanged both absent, unchanged equal, restored field, forbidden on create, update and clone with `live == nil`)
- [ ] T014 [P] Envtest `api/internal/handlers/resources_security_envtest_test.go` for create, update and clone permission behavior: `servers:write` without `servers:security` gets 403 on a changed block, 200 on an unrelated edit that sends the same block back, 200 and kept override when the request omits the block, 403 on cloning a server that has one; an `admin` caller succeeds

**Checkpoint**: CRDs and generated artifacts committed, permission enforced; stories can start.

---

## Phase 3: User Story 1 - Server uses the module's security defaults (P1) MVP

**Goal**: with no override a server runs exactly as today, the ownership check defaults to "only when wrong" without restarting any existing server, and the details show each value and its source (FR-001, FR-002a, FR-003, FR-004, FR-009, FR-012, SC-001, SC-004, SC-006).

**Independent test**: create a server from a module with security settings and one without; each runs with the module's values (or the image's user), `status.security` shows `ModuleDefault` / `ImageDefault` / `BuiltInDefault`, and an operator upgrade restarts no running server (quickstart S1, S6).

### Tests for User Story 1

- [ ] T015 [P] [US1] Table test `operator/internal/controller/gameserver_security_test.go` for `resolveGameSecurity(tmpl, gs)` (research R4): every id source (module default, image default when neither sets it, `BuiltInDefault` for the ownership check), the `fsGroupChangePolicy` mapping (`EveryStart`=`Always`, `OnlyWhenWrong`=`OnRootMismatch`, written only when a filesystem group applies), the annotation `gameplane.local/security-applied` encode/decode round trip, the pre-feature reader (no annotation: non-empty values `ModuleDefault`, empty ids `ImageDefault`, ownership check `EveryStart` with `BuiltInDefault`), and `pending` computation (source-only change is not pending)
- [ ] T016 [P] [US1] Extend `operator/internal/controller/build_game_container_test.go` and `operator/internal/controller/workload_security_test.go`: with nothing applied the container and pod `securityContext` are byte-identical to today (no empty `securityContext: {}`); helper containers (agent, config-init user, sentinel, tunnel, wipe) keep their fixed users (FR-012)
- [ ] T017 [P] [US1] Envtest `operator/internal/controller/gameserver_security_envtest_test.go`: a server with no override gets the module's values and `status.security.desired` equals `applied` with `ModuleDefault`; a template with no security block shows `ImageDefault`; a StatefulSet created before this feature (no annotation, no change policy) is NOT recreated when the operator reconciles it (pod template untouched, `pending=true`, `applied.volumeOwnershipCheck=EveryStart`, `desired=OnlyWhenWrong`); after a scale to zero and back the pod template carries `fsGroupChangePolicy: OnRootMismatch` and `pending=false`

### Implementation for User Story 1

- [ ] T018 [US1] Create `operator/internal/controller/gameserver_security.go` (NEW): the pure resolver `resolveGameSecurity` returning value plus source per setting, the pod encoding helpers, the `gameplane.local/security-applied` annotation codec, the applied-values reader (the StatefulSet annotation `gameplane.local/security-applied-observed`, copied from the pod template once an owned pod found via `ownedTemplatePod`, `gameserver_template_revision.go:480`, has `PodReadyToStartContainers=True` or a started container; pre-feature fallback to the running pod, else the StatefulSet template; research R6) and the `fsGroupChangePolicy` mapping; depends on T006, T007
- [ ] T019 [US1] Change `gameContainerSecurityContext` (`gameserver_controller.go:2470`), `gamePodSecurityContext` (`:2586`) and `buildConfigInitContainer` (`:2310`, its `RunAsGroup` and `chgrp` target) to take the resolved values instead of reading `tmpl.Spec.Security`; keep the `nil`-when-empty behavior; depends on T018
- [ ] T020 [US1] In `reconcileStatefulSet` (`gameserver_controller.go:1611` mutate function, gate near `:1889-1905`) write the security-derived pod-template parts (pod `securityContext`, game container `securityContext`, config-init container, the `security-applied` annotation) only when `ss.ResourceVersion == ""` or the StatefulSet goes from zero replicas to one or more; otherwise copy them forward from the existing StatefulSet (research R5, R8). This is what keeps the ownership-check default flip from restarting servers on operator upgrade (SC-001); depends on T018, T019
- [ ] T021 [US1] In `operator/internal/controller/gameserver_status.go` publish `status.security` (`desired`, `applied`, `pending`) with a `MergeFrom` patch of that field only (the agent's `status.agent` heartbeat stays untouched, `:42-60` pattern); `applied` omitted before the first start; depends on T018
- [ ] T022 [P] [US1] Add `mcp-server/main_test.go` fixture asserting a GameServer carrying `spec.security` and `status.security` round-trips unredacted through the read-only tools (research R15); `mcp-server/README.md` gets one example and `mcp-server/specs.md` one sentence (no code change in `mcp-server/tools.go`)
- [ ] T023 [P] [US1] **Needs T003.** Mirror the types in `web/src/types.ts`: `GameServer.spec.security` and `status.security` (`:557`), `GameTemplate.spec.security` with `risky` and `volumeOwnershipCheck` (`:303`)
- [ ] T024 [US1] **Needs T003.** Create `web/src/components/SecurityAdvanced.tsx` (NEW shared section, collapsed by default, read-only first cut) showing per setting the module default or "image default", the applied value and its source, and the "no effect" marker for the ownership check when no filesystem group applies, exactly as the exported frames in `assets/design-export/`; depends on T023
- [ ] T025 [US1] **Needs T003.** Add `web/src/components/SecurityAdvanced.test.tsx` (vitest: sources render, no value renders "image default", the ownership check marks "no effect") and the English strings under `web.routes.server_settings.security_*` in `i18n/locales/en/app.json` with the `fr` pack entries (FR-014); depends on T024

**Checkpoint**: no-override behavior and sources verified; MVP. Compile checks: `go build ./...` in `operator` and `mcp-server`, `npx tsc --noEmit` in `web`.

---

## Phase 4: User Story 2 - Override the security settings when creating a server (P1)

**Goal**: a `servers:security` holder sets any of the four settings in the wizard's Advanced section, the server runs with only that setting changed, "use image default" applies nothing, and invalid values are refused before anything is created (FR-002, FR-003, FR-005 shape, FR-006, FR-006a, SC-002; OD-5, OD-6).

**Independent test**: in the wizard open Advanced, set run-as user to a non-default value, create the server, confirm the running uid with `id -u` while the other settings keep the module default (quickstart S2, S8).

### Tests for User Story 2

- [ ] T026 [P] [US2] Extend `operator/internal/controller/gameserver_security_test.go`: override `value`, override `useImageDefault` (value none, source `ImageDefaultOverride`), override over module default, module default over nothing; restricted-mode validation on the effective values (OD-5): a root value or "use image default" for run-as user is refused by `validateRestrictedTemplate`'s rule while a positive override of a root module default passes
- [ ] T027 [P] [US2] Extend `operator/internal/controller/gameserver_security_envtest_test.go`: a server created with `runAsUser {value: 2000}` gets 2000 with `Override` and the other settings `ModuleDefault`; `runAsGroup {useImageDefault: true}` leaves the container `runAsGroup` unset; with `RestrictedWorkloads` on, an override the rule refuses sets the existing failure phase message (`gameserver_controller.go:334` behavior) and a pod that is already running is untouched (OD-5)
- [ ] T028 [P] [US2] Extend `api/internal/handlers/resources_security_test.go` and `resources_security_envtest_test.go`: shape cases (`value` 4294967296, `-1`, both `value` and `useImageDefault`, neither, `useImageDefault:false`, unknown member, bad enum) return 422 `server.security.invalid` with the offending setting in `args` and create nothing; a valid override is stored and returned
- [ ] T029 [P] [US2] **Needs T003.** vitest for the wizard section in `web/src/components/SecurityAdvanced.test.tsx` and `web/src/routes/CreateServer.test.tsx`: no security values are sent when Advanced is never opened (US2 scenario 1), each setting shows module default and an empty override (scenario 2), one override changes only that setting (3), "use image default" is sent as `{useImageDefault: true}` (4), an invalid value is rejected before submit (5), a risky setting asks for the confirmation with the module's reason and cancelling sends nothing (OD-6: dashboard-only)
- [ ] T030 [P] [US2] Kind E2E `test/e2e/gameserver_security_e2e_test.go` (NEW, `//go:build e2e`, `TestGameServer_SecurityOverrideAppliesOnRestart`, `t.Parallel()`, unique names, busybox fixture from `test_helpers_e2e_test.go:42-90`, zero logins): part 1 creates a server with a run-as user override and asserts the running uid via `Env.KubectlExec(... id -u)` (`env.go:259`) with the other ids at the module default

### Implementation for User Story 2

- [ ] T031 [US2] Extend `resolveGameSecurity` in `operator/internal/controller/gameserver_security.go` with the override layer (research R4): `useImageDefault` gives none and `ImageDefaultOverride`, `value` gives that number and `Override`, else module default, else none; ownership check override over template over `OnlyWhenWrong`
- [ ] T032 [US2] Make `validateRestrictedTemplate` and `restrictedContainerSecurity` (`workload_security.go:15, 23`), `applyWorkloadSecurity` (`:39`) and the call sites at `gameserver_controller.go:334`, `gameserver_template_revision.go:216, 278` validate and apply the effective (desired) values; a violation reports the server failed with the existing message until fixed; a running pod is untouched (OD-5 settled A)
- [ ] T033 [US2] Add the shape validation (case 4: ids whole numbers 0 to 4294967295, exactly one of `value`/`useImageDefault`, `useImageDefault` true, no unknown members, valid enum) to `api/internal/handlers/resources_security.go`, returning 422 `server.security.invalid`; the CRD CEL stays the second line
- [ ] T034 [US2] **Needs T003.** Make `SecurityAdvanced.tsx` editable for `servers:security` holders (value, "use image default", clear) with client-side range validation and the risky-value confirmation dialog showing `template.spec.security.risky.<setting>.reason` (plain text, never rendered as markup); depends on T024
- [ ] T035 [US2] **Needs T003.** Add the collapsed Advanced section to `web/src/routes/CreateServer.tsx`, sending `spec.security` only when a setting was set; surface the server refusals inline (the `root_disabled` message tells an administrator can allow it in Settings); add `web.routes.create_server.advanced_*` keys to `i18n/locales/en/app.json` and `fr`; client calls in `web/src/lib/api.ts` pass `spec` already (no new route); depends on T034
- [ ] T036 [P] [US2] Module repo (separate PR to `GameplanePanel/module`, branch from T002): regenerate `.schema/gametemplate.schema.json` with `make module-schema` (`hack/gen-module-schema.py:21-22`, run from this repo after T007 merges or on its branch, output copied into the module repo); add to `validate.py` the rules that a `risky` key is one of the four settings, a `reason` over 200 characters is an error, and `volumeOwnershipCheck` without a template `fsGroup` is a warning (existing rules at `validate.py:610-660, 903-958` unchanged); add cases to `test-validate-py.sh`; depends on T007
- [ ] T037 [P] [US2] Module repo, same PR: mark `runAsUser` risky with a reason in `palworld/template.yaml` ("The image must start as root to drop privileges; forcing a user breaks its start.") and update `palworld/specs.md`; other modules only if the module review says so
- [ ] T038 [P] [US2] Add the same three template checks to `gp-module/internal/validator/rules.go` with tests in the validator test file so `gp-module validate` agrees with `validate.py`; make `gp-module/internal/preview/` print the resolved defaults, the ownership check and the risky settings with their reasons; leave `internal/scaffold/scaffold.go:167, 213` unchanged (no archetype asks for `risky`); tests under the gp-module gate (80)

**Checkpoint**: US1 and US2 work end to end for an admin; module repo PR open. Compile checks as above plus `go build ./...` in `gp-module`.

---

## Phase 5: User Story 3 - Change or remove an override on an existing server (P2)

**Goal**: a saved change applies on the next restart and never restarts the server; removing an override returns the module default; module upgrades keep overrides; backups and restores use the applied filesystem group; the dashboard warns and shows pending values (FR-007, FR-008, FR-009, FR-011, SC-004).

**Independent test**: on a running server set a run-as user override, save, observe no restart and `pending: true`, restart and observe the new uid, remove the override, restart and observe the module default (quickstart S3, S7).

### Tests for User Story 3

- [ ] T039 [P] [US3] Extend `operator/internal/controller/gameserver_security_envtest_test.go`: saving a changed override does not change the StatefulSet pod template or the pod while it runs (same generation of the template, `pending=true`, `applied` unchanged); an unrelated env change rolls the pod without picking up the pending security change; scale to zero and up refreshes and clears `pending`; editing back to the applied value clears `pending`; removal returns `ModuleDefault`; a template rollout keeps an overridden setting and the others follow the new default (US3 scenario 3); a never-started server has no `applied`
- [ ] T040 [P] [US3] Extend `operator/internal/controller/backup_envtest_test.go` and add cases to `operator/internal/controller/gameserver_security_test.go`: with a pending `fsGroup` change on a stopped server, and during a pending start whose pod has not passed volume setup, `buildBackupPodSecurityContext` (`backup_controller.go:1133`) and the restore pod (`restore_controller.go:425`) use `applied.fsGroup`, not `desired`; a start that fails before volume setup leaves `applied` and `pending` unchanged; a never-started server uses the desired value
- [ ] T041 [P] [US3] **Needs T003.** vitest `web/src/routes/tabs/Settings.test.tsx` and `web/src/routes/tabs/settings/Security.test.tsx`: the section shows applied, pending and the "applies on the next restart" notice; saving a changed run-as user or group on a server with data shows the file-access warning first; removing an override shows "module default"; the save never triggers a restart call
- [ ] T042 [P] [US3] Extend `test/e2e/gameserver_security_e2e_test.go` (`TestGameServer_SecurityOverrideAppliesOnRestart` part 2): change the override and assert the same pod UID, `status.security.pending` true and `applied` unchanged; restart and assert the new uid and `pending` false; remove the override, restart, assert the module default

### Implementation for User Story 3

- [ ] T043 [US3] Make `buildBackupPodSecurityContext` (`backup_controller.go:1133`, caller `:1035`) and the restore call (`restore_controller.go:425`) take the applied filesystem group from the same pod or StatefulSet read as `status.security.applied` (FR-011, research R6); fall back to the desired value only for a server that never started; depends on T018
- [ ] T044 [US3] Ensure the status writer recomputes `pending` when the template pin changes (template rollout admitted, `gameserver_template_revision.go`) without touching the running pod; confirm in the brief that `OnRequest` and `Automatic` rollouts both end in a start that refreshes the template (plan.md spec gap 3)
- [ ] T045 [US3] **Needs T003.** Create `web/src/routes/tabs/settings/Security.tsx` (NEW) and mount it from `web/src/routes/tabs/Settings.tsx` (mounted by `ServerDetail.tsx:377`): the shared `SecurityAdvanced` section for an existing server with the pending marker, the restart notice, the file-access warning and clear-to-module-default; the whole spec sent back unchanged is accepted by the API (case 1); depends on T034
- [ ] T046 [US3] **Needs T003.** Add `web.routes.server_settings.security_*` notice and warning strings to `i18n/locales/en/app.json` and `fr` (pending, restart notice, file-access warning); depends on T045

**Checkpoint**: change, restart and removal verified; backups safe.

---

## Phase 6: User Story 4 - Restrict who can force security settings (P2)

**Goal**: only `servers:security` holders change the four settings (dashboard read-only otherwise, API 403), root values need the admin switch, a stopped server holding a root override is refused a start while the switch is off, and every change, refusal and flip is audited (FR-005, FR-005a, FR-010, SC-003, SC-005; OD-1, OD-2, OD-4, OD-6).

**Independent test**: as a user without the permission the Advanced fields are read-only and an API override gets 403; as an admin with the switch off a root value gets 403, and a stopped root-override server will not start (quickstart S4, S5, S5a, S9).

### Tests for User Story 4

- [ ] T047 [P] [US4] Extend `api/internal/handlers/config_envtest_test.go` and add validator unit tests: `PUT /admin/config/security` accepts `{"allowRootOverrides":true}`, rejects a non-boolean and an empty body (422), drops unknown members; a viewer without `config:manage` gets 403; the section round-trips through `GET /admin/config`; a flip writes the ConfigMap `gameplane-security-settings` with `"true"`/`"false"`, a failing ConfigMap write returns an error and leaves the config row unchanged, and the start-up sync recreates a deleted ConfigMap ([contracts/cluster-setting.md](./contracts/cluster-setting.md))
- [ ] T048 [P] [US4] Extend `api/internal/handlers/resources_security_test.go` and `resources_security_envtest_test.go` for the root rule (case 5): a new or changed `runAsUser` or `runAsGroup` with `value: 0` or `useImageDefault`, or `fsGroup` `value: 0`, gets 403 `server.security.root_disabled` while off and is accepted once on; `fsGroup` `useImageDefault` is free; an unchanged stored root value passes an unrelated whole-spec PUT with the setting off; removal and replacement are always allowed
- [ ] T049 [P] [US4] Audit tests in `api/internal/handlers/resources_security_test.go` (unit) and `resources_security_envtest_test.go`: the `reason` builder for not set, number, `"image"`, ownership check, create (`before` `{}`), and the `risky` list (a risky changed setting appears with names only; non-risky, unchanged risky and an absent `risky` map add no key; the module's reason text never appears; OD-6); each of change, permission refusal (`security_forbidden`), root refusal (`security_root_disabled:runAsUser`), invalid (`security_invalid:fsGroup`) and setting flip (`allowRootOverrides:false->true`) writes exactly one row with `MarkRecorded`, a change row is written before the Kubernetes write, a failed `WriteSync` on a change returns 500 with nothing saved, a failed Kubernetes write after the row adds a `security_save_failed` row, a failed `WriteSync` on a refusal falls back to the middleware row, and `GET /admin/audit/verify` still passes ([contracts/audit-events.md](./contracts/audit-events.md))
- [ ] T050 [P] [US4] Operator unit tests in `operator/internal/controller/gameserver_security_test.go` for `hasRootOverride` and `rootOverrideSettings` (every root form in the rule table, plus non-root values and a module-default root that is not an override) and the ConfigMap reader (absent, missing key, `"false"`, garbage and `"true"` read as off/off/off/off/on)
- [ ] T051 [P] [US4] Envtest `operator/internal/controller/gameserver_security_envtest_test.go` for OD-4 (FR-005a): with the ConfigMap absent or `"false"`, a stopped server holding a root override stays at zero replicas with `SecurityAllowed=False` reason `RootOverrideDisallowed` naming the settings; a running one keeps its pod and restart count and gets only the informational condition; flipping the ConfigMap to `"true"` starts the stopped one without any other event (the watch re-enqueues it); clearing the override starts it; flipping off never restarts or stops any server; unrelated servers are not enqueued by the flip
- [ ] T052 [P] [US4] **Needs T003.** vitest: `web/src/routes/AdminSettings.test.tsx` (the Security section's switch is off by default, reads `GET /admin/config`, saves through `PUT /admin/config/security`, shows the "does not change existing servers" text) and `web/src/components/SecurityAdvanced.test.tsx` (no `servers:security`: applied, sources and pending visible and read-only, no `spec.security` is ever sent; a server held back shows the `RootOverrideDisallowed` reason for the stopped and running variants; the three API refusals render inline)
- [ ] T053 [P] [US4] Kind E2E `test/e2e/api_security_override_e2e_test.go` (NEW, `//go:build e2e`, `TestAPI_SecurityOverride_PermissionAndRootRule`, `t.Parallel()`, unique names): one admin login and one login as its own non-admin user created for the test; the non-admin (holds `servers:write`, not `servers:security`) is refused 403 on create and on update; granted `servers:security` it succeeds; a root value is refused 403 while `allowRootOverrides` is off and accepted after the admin enables it; the ConfigMap `gameplane-security-settings` mirrors each flip; the audit log has the rows
- [ ] T054 [P] [US4] Extend `test/e2e/gameserver_security_e2e_test.go` (`TestGameServer_SecurityOverrideAppliesOnRestart` part 3, zero logins): save a root `fsGroup` override, stop the server with the ConfigMap absent or `"false"`, start it and assert it stays at zero replicas with `SecurityAllowed=False` / `RootOverrideDisallowed`; set the ConfigMap to `"true"` with `kubectl` and assert it starts; set it back to `"false"` and assert the running pod keeps its UID and restart count

### Implementation for User Story 4

- [ ] T055 [US4] Add the root rule (case 5) to `api/internal/handlers/resources_security.go`, reading `allowRootOverrides` through the `*db.Store` `MountResources` already receives (one indexed `config` read, only on a request that carries a new or changed root value); 403 `server.security.root_disabled` with `args.setting`
- [ ] T056 [US4] Register the `security` validator in `newValidators` (`api/internal/handlers/config.go:462`) and write the extra audit row on a flip, before the ConfigMap and config row writes (500 and no change if it fails; a `security_save_failed` row if a later write fails; contracts/audit-events.md "Write order"), reusing the row format of the `config.go:174-179` pattern (`PUT /admin/config/security`, `target` `security`, `reason` `allowRootOverrides:false->true`); the existing rule table (`rbac.go:223-228`) already needs `config:manage`
- [ ] T057 [US4] Create `api/internal/handlers/security_settings_sync.go` (NEW): write the ConfigMap `gameplane-security-settings` (key `allowRootOverrides`, label `app.kubernetes.io/managed-by: gameplane-api`) in the control-plane namespace of every registered cluster the API can reach, ConfigMap first then the config row, failing the request if the write fails; rewrite it from the config row on API start; call it from the `security` section's save in `config.go`; depends on T056
- [ ] T058 [US4] Add to `charts/gameplane/templates/api.yaml` a Role and RoleBinding in the release namespace giving the API `get`, `create`, `update`, `patch` on the single ConfigMap `gameplane-security-settings` by `resourceNames`; the operator ClusterRole already has `get;list;watch` on ConfigMaps (`charts/gameplane/templates/operator.yaml:66`); add a Helm unit/template check if the chart tests have a RBAC fixture for the API roles
- [ ] T059 [US4] Wire `MountResources` (`api/internal/handlers/resources.go:51`, called from `api/cmd/main.go:382`) to receive the `*audit.Auditor`; in create (`:218`) and update (`:290`) write one synchronous row via `Auditor.WriteSync` before the Kubernetes write (500 and no save on failure; a `security_save_failed` row if the save then fails; [contracts/audit-events.md](./contracts/audit-events.md) "Write order") then `audit.MarkRecorded` (capture precedent `handlers/capture.go:1252-1258`) for a changed block and for each refusal; build `reason` as `security:{"before":..,"after":..,"risky":[..]}` from the two blocks and the pinned template's `risky` map (names only, OD-6); confirm the clone path (`lifecycle.go:243`) is covered by the shared validator
- [ ] T060 [US4] Create `operator/internal/controller/security_settings.go` (NEW): the ConfigMap reader from the informer cache (fail closed), `hasRootOverride`, `rootOverrideSettings`, and the map function that enqueues every GameServer holding a root override; add `Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(...))` filtered to that name and namespace next to the Pod and GameTemplate watches (`gameserver_controller.go:583`), no new RBAC marker needed (`:258` already lists ConfigMaps)
- [ ] T061 [US4] In `reconcileStatefulSet` hold a stopped server with a root override at zero replicas while the setting is off (before the R5 refresh, so the StatefulSet is left exactly as it was) and never touch a running one; set the `SecurityAllowed` condition (`False` / `RootOverrideDisallowed` with the settings named and what clears it; `True` once allowed or cleared) in `gameserver_status.go`; emit an event; never restart or stop on a flip (research R9, FR-005a); depends on T020, T060
- [ ] T062 [US4] **Needs T003.** Make `SecurityAdvanced.tsx` read-only without the permission (`can("servers:security", namespace)` from the permission data the dashboard already loads, [contracts/rbac-permission.md](./contracts/rbac-permission.md)) and show the `RootOverrideDisallowed` notice with the stopped and running texts; depends on T034, T045
- [ ] T063 [US4] **Needs T003.** Add the "Security" section with the allow-root switch to `web/src/routes/AdminSettings.tsx` and its get/put client methods to `web/src/lib/api.ts`; strings under `web.routes.admin_settings.security_*` in `i18n/locales/en/app.json` and `fr` (FR-014); depends on T052

**Checkpoint**: all four stories functional. Compile checks: `go build ./...` in `operator`, `api`; `go vet -tags e2e ./...` in `test/e2e`; `npx tsc --noEmit` in `web`.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: register the E2E tests, document the feature, land the module PR and pointer bump, and close the loose ends. Docs tasks do not wait for the design.

- [ ] T064 Register both tests in `bucket_operator` of `test/e2e/buckets.sh` (`TestGameServer_SecurityOverrideAppliesOnRestart`, `TestAPI_SecurityOverride_PermissionAndRootRule`). **Bucket decision**: `operator` already spends about four admin logins ("operator: 4 admin logins with the EventStream test", `buckets.sh:355-362`) against the documented ceiling near seven; `TestAPI_SecurityOverride_PermissionAndRootRule` adds one admin login plus one login as its own user, so the bucket reaches about five admin logins and fits. `api-auth`, `api-roles`, `api-rbac`, `api-agent` and `api-mods` are at their ceilings and must not receive it. Add a comment above the bucket recording the tally; no new bucket, CI matrix entry or `suite-components.json` entry is needed (`^TestGameServer_` and `^TestAPI_` already map both tests, `suite-components.json:32, 46`); `buckets.sh verify` must still pass; depends on T030, T053
- [ ] T065 [P] Rewrite "Security context" in `docs/module-authoring.md` (`:1137-1173`) per FR-013: the block is a default that server owners can override; the new `volumeOwnershipCheck` and `risky` fields in the field table; when to mark a value risky (the image only works with that uid, or must start as root) and how to word the reason (one sentence, what breaks); when to set `EveryStart`; the built-in default flip to `OnlyWhenWrong`
- [ ] T066 [P] Add a "Server security overrides" subsection under "Pod security" in `docs/security.md` (`:435`): who may override (`servers:security`, admin by default), the root rule and the allow-root setting, the refusal to start (`RootOverrideDisallowed`), apply-on-restart, the applied filesystem group for backups, the ownership-check trade-off, the audit trail, and that `kubectl` writers bypass the API checks
- [ ] T067 [P] Update `operator/specs.md` (resolver, gate, annotation, status, `SecurityAllowed`, ConfigMap watch, restricted-mode effective values), `api/specs.md` (permission, field-level check, `security` config section and ConfigMap mirror, audit reason), `web/specs.md` (shared Advanced section, wizard, Settings, admin switch), `gp-module/specs.md` (validator rules, preview), `mcp-server/specs.md` (read-only exposure, T022) and `test/e2e/specs.md` (the two tests); each in the same change as its code (constitution IV)
- [ ] T068 [P] Add the new files (`operator/internal/controller/gameserver_security.go`, `security_settings.go`, `api/internal/handlers/resources_security.go`, `security_settings_sync.go`, `web/src/components/SecurityAdvanced.tsx`) to the task map in `docs/agent-architecture.md`
- [ ] T069 Open the module repo PR (T036, T037) to `GameplanePanel/module` from `spec-030/module-security`, run its `test-validate-py.sh` in its CI, get it merged (needs human approval, agents cannot merge); depends on T036, T037
- [ ] T070 After T069 merges, bump the pointer in this repo with the `submodule-bump` skill (`git submodule update --remote modules`, `git add modules`, signed commit `chore: bump modules for spec 030`); run `make modules-push` only if the module PR notes require it; depends on T069
- [ ] T071 Open the main PR with labels `type: feature` plus `area: operator`, `area: api`, `area: web`, `area: modules`, `area: chart`, `area: e2e`, `area: specs` as touched (REST call per CLAUDE.md rule 14, `ship-branch` skill); do not call it ready until CI is green; the PR may be split along the delivery slices in plan.md (A and B, C, G, then H) as long as US2 and the permission check ship together
- [ ] T072 Walk [quickstart.md](./quickstart.md) S1 to S11 on a dev Kind cluster once CI is green (manual, optional per scenario; CI E2E is the authority) and record any deviation in `specs/030-security-context-overrides/validation.md`
- [ ] T073 Final bookkeeping check: every task above is done or marked withdrawn with citations (never deleted), `OPEN-DECISIONS.md` has no open item, and the specs folder passes `check-specs` in CI

---

## Phase 8: Archive

- [ ] T074 After every task is complete or withdrawn, the main PR and the module PR are merged and the pointer bump (T070) is on `master`, archive with the `spec-archive` skill: `git mv specs/030-security-context-overrides specs/done_030-security-context-overrides` and update the in-repo references in the same commit (CLAUDE.md rule 16); depends on T070, T071, T073

---

## Dependencies & Execution Order

### Phase dependencies

- Phase 1 has no dependencies. **T003 (maintainer design) starts on day one and runs in parallel with all backend work.** It blocks only the web tasks.
- Phase 2 depends on T001 (brief) and blocks every story. T007 needs T005 and T006; T012 needs T006, T009, T011.
- Phase 3 (US1, MVP) depends on Phase 2. Its web tasks (T023-T025) also need T003.
- Phase 4 (US2) depends on Phase 3 (it extends `gameserver_security.go`, `SecurityAdvanced.tsx`, and the E2E file). Module-repo tasks T036-T038 depend only on T007 and can start as soon as Phase 2 is merged, in parallel with Phase 3.
- Phase 5 (US3) depends on Phase 3 for the gate and status (T020, T021) and on Phase 4 for the editable section. T043 can start after T018.
- Phase 6 (US4) depends on Phase 2 and on T020 (the gate it extends). The API half (T047-T049, T055-T059), the chart RBAC (T058) and the operator half (T050, T051, T060, T061) are independent of Phases 4 and 5 and can run in parallel with them; only the web tasks (T062, T063) need the earlier web tasks and T003.
- Phase 7 depends on its inputs: T064 needs T030 and T053; T069 needs T036 and T037; T070 needs T069.
- Phase 8 is last.
- **Shipping constraint**: US2's API accept path must not reach a release without US4's root rule and ConfigMap mirror; Phase 2 already enforces the permission, but the root rule (T055) and the operator refusal (T061) land in the same release as US2's wizard (T035).

### Hard blockers

- **T003 (maintainer-owned design) blocks T023, T024, T025, T029, T034, T035, T041, T045, T046, T052, T062, T063.** If the design is late, every other task proceeds and these wait.
- T007 (generated CRDs) blocks every operator, API test and module-schema task.
- T069 (module PR merged) blocks T070; T070 blocks T074. The main-repo changes work without them (a template with no `risky` block has no risky values).

### Within each story

- Tests first (they fail in CI until the implementation lands in the same PR).
- Resolver and codec (`gameserver_security.go`) before builders, builders before the StatefulSet gate, the gate before status and the start refusal.
- API: validator step before audit; config section before the ConfigMap mirror; chart RBAC with the mirror.
- Web: types before the shared section, the shared section before the wizard, Settings tab and admin switch.

### Parallel opportunities

- T002 ‖ T003 ‖ T004; T005 ‖ T006; T008 ‖ T009 ‖ T010 ‖ T011 ‖ T013 ‖ T014.
- Phase 3 tests T015-T017 in parallel; T022 and T023 in parallel with the operator work.
- Phase 4 tests T026-T030 in parallel; the module repo PR (T036-T037) and gp-module (T038) in parallel with the operator and web work.
- Phase 6 tests T047-T054 in parallel; the API half and the operator half in parallel; docs T065-T068 in parallel with everything after Phase 2.

## Parallel Example: Phase 6 (US4)

```text
Workflow batch A (haiku, one agent each): T047, T048, T049, T050, T051 — test files only, from the brief
Workflow batch B (haiku): API half T055, T056, T057, T059 (sequential in one agent, same files); chart T058 (separate agent)
Workflow batch C (haiku): operator half T060, T061 (one agent, sequential)
Web batch (haiku, only after T003): T052, T062, T063
Review (sonnet): one reviewer per batch on `git diff` against the brief and contracts; compile checks only
```

## Implementation Strategy

### MVP first

1. Phases 1 and 2: brief, CRDs, permission and messages (design starts in parallel).
2. Phase 3 (US1): module defaults, ownership-check default without fleet restarts, status with sources, read-only section. Stop and review the diff; CI is the verifier.

### Incremental delivery

1. US2 adds the override (API shape check, operator override layer, restricted-mode validation, wizard, module repo PR).
2. US3 adds apply-on-restart semantics tests, backup and restore fsGroup, Settings tab.
3. US4 adds the root rule, the admin switch, the ConfigMap mirror and operator start refusal, and the audit trail; US2's wizard ships with it.
4. Phase 7 registers E2E, documents, bumps the pointer after the module PR merges; Phase 8 archives.

## Notes

- Do not renumber tasks; a new task takes the next free ID and states its phase.
- The OD-4 refusal is exercised without the API in the E2E (the test writes the ConfigMap with `kubectl`), and the mirror is exercised in the API E2E (T053), so each test stays within its login budget.
- No `.pen` file is read or edited by any task except T003, which is the maintainer's.
