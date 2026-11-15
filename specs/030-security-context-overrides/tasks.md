# Tasks: Module security defaults with a per-server override

**Input**: Design documents from `specs/030-security-context-overrides/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md), [OPEN-DECISIONS.md](./OPEN-DECISIONS.md) (OD-1 to OD-10 all settled; `briefs/00-rulings.md` is read first)

**Tests**: requested. Constitution I requires a Kind E2E on the real path; each component also gets unit and envtest tests under its coverage gate (operator 72, api 80, gp-module 80, mcp-server 70, web 92/76/82/92). Tests are written first and fail in CI until the implementation in the same PR lands.

**Organization**: one phase per user story in spec priority order (US1 P1, US2 P1, US3 P2, US4 P2). Stories share files (`security_overrides.go`, `SecurityAdvanced.tsx`, the two E2E files), so the phases build on each other in order; see "Dependencies & Execution Order". Backend, operator, API, module and docs work never waits for the dashboard design.

**Binding rules for every task** (repeat them in every Workflow script and agent brief, CLAUDE.md rules 4, 6-8, 11-14, 18):

- Locally only compile checks: `go build ./...` in `operator`, `api`, `gp-module`, `mcp-server`; `go vet -tags e2e ./...` in `test/e2e`; `npx tsc --noEmit` in `web`; `python3 -m py_compile` for Python. Never run `make test|lint|cover`, `go test`, `npm test`, envtest, `i18n-check` or E2E locally; CI is the only verifier.
- No `//nolint`, `eslint-disable`, `# noqa` or loosened config. Wrap Go errors with `%w`. Strict TypeScript, every promise awaited or `void`-prefixed. No test is deleted or weakened.
- Never open, read, grep or edit a `.pen` file. The design task (T003) is the maintainer's, through the Pencil MCP.
- Every CRD edit ends with `make generate && make manifests`, and the deepcopy, `operator/config/crd/*.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml` and `charts/gameplane/crd-manifests/*.yaml` are committed in the same changeset.
- Dashboard text only through the shared language packs (`i18n/locales/en/app.json`, then `fr` per `docs/translating.md`); API refusals through `api/internal/messages/`.
- Signed commits (`git commit -s`), conventional prefix, `Co-Authored-By` trailer; never amend after push; one branch per unit of work. PR labels via REST (`type: feature`, `area: operator|api|web|modules|chart|e2e|specs|shared` as touched, never `gh pr edit`).
- Implementation runs through Workflow scripts with `model:` set on every agent, starting at haiku, review one tier up on the diff, scout once and brief many (T001).
- Unsettled values never go into a contract; anything new goes to `OPEN-DECISIONS.md` and pauses the task.
- Read `briefs/00-rulings.md` before any other brief. It is binding and supersedes the earlier briefs where they differ. Spec `file:line` citations are stale; use the citations in the briefs (OD-9 item 7).
- No existing test file is edited (OD-9 item 2). A task that names an existing `_test.go`, `*.test.tsx` or `test-validate-py.sh` creates the NEW file named in `briefs/00-rulings.md` section 8 instead. Existing call sites keep compiling through optional or variadic parameters or new functions (briefs/00 section 11).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US4 from spec.md; setup, foundational and polish tasks carry none
- **MAINTAINER-OWNED**: only valgul does it, on his machine

## Path Conventions

Repository root of `GameplanePanel/Gameplane` unless a task says `module repo` (`GameplanePanel/module`, local clone `/home/user/module`, checked out in this repo at `modules/`). Main files: `operator/api/v1alpha1/{gameserver,gametemplate}_types.go`, `operator/internal/controller/`, `api/internal/{rbac,handlers,messages}/`, `web/src/`, `gp-module/internal/`, `test/e2e/`, `charts/gameplane/`, `docs/`.

---

## Phase 1: Setup

**Purpose**: prerequisites shared by every phase, including the design gate that blocks all dashboard work.

- [X] T001 Scout once (CLAUDE.md rule 18): read the code named in plan.md "Source Code" and write the factual brief `specs/030-security-context-overrides/briefs/security-overrides.md` with exact `file:line`, before/after snippets and the justification for every edit below (builders `gameContainerSecurityContext`, `gamePodSecurityContext`, `buildConfigInitContainer`, the `reconcileStatefulSet` gate near `gameserver_controller.go:1889-1905`, `validateRestrictedTemplate` callers, backup and restore call sites, `validateAndProtectGameServer`, `newValidators`, the operator `Watches` list near `gameserver_controller.go:583`, the API Role in `charts/gameplane/templates/api.yaml`); also confirm which operator flag names the control-plane namespace the ConfigMap lives in (used by T057 and T060). Fix agents edit from this brief only
- [ ] T002 [P] Prepare the module repo: confirm `git submodule update --init modules` works, create a branch `spec-030/module-security` in `/home/user/module` (separate PR to `GameplanePanel/module`, default branch per that repo), and record in the brief that no module-repo file is edited on this repo's branch
- [ ] T003 [P] **MAINTAINER-OWNED (valgul, local machine, Pencil MCP). BLOCKS EVERY WEB TASK (T023, T024, T025, T029, T034, T035, T041, T045, T046, T052, T062, T063, T079; each is tagged "Needs T003").** In `design.pen` design the dashboard frames, then export touched nodes to `assets/design-export/json/<id>.json` and `assets/design-export/screenshots/<id>.png` (skills `design-export`, `pen-componentize`, `pen-canvas-layout`): (a) the collapsed "Advanced" security section in the create-server wizard; (b) the same section on the server Settings tab; (c) states: module default, "image default", override field, "use image default" choice, applied value with source, pending value with the "applies on the next restart" notice, "no effect" marker for the ownership check without a filesystem group, read-only state without `servers:security`; (d) the file-access warning before saving a changed run-as user or group on a server with data; (e) the risky-value confirmation dialog showing the module's reason; (f) the inline refusal messages (forbidden, root disabled, invalid); (g) the notice for a server held back with reason `RootOverrideDisallowed` (stopped and running variants); (h) the "Security" panel under Clusters, cluster, Configuration with the allow-root switch and its explanatory text (not Admin Settings); (i) the register-cluster form with the optional control-plane namespace field (default `gameplane`, OD-10 A), and the cluster page showing the stored value (T079). The maintainer saves in the Pencil UI and commits the exports; an agent may prepare the copy-paste prompt with the `local-prompt` skill but never touches the `.pen` file. Done when `assets/design-export/{json,screenshots}/` for those frames is in the branch and `assets/design-export/MANIFEST.md` lists them
- [ ] T004 [P] Confirm that the labels this feature's PRs need (`type: feature` and the `area:` labels) already exist with `gh api repos/GameplanePanel/Gameplane/labels`; no new label is expected

**Checkpoint**: brief written; design started (it runs in parallel with everything below until a web task needs it).

---

## Phase 2: Foundational (blocks every story)

**Purpose**: the CRD fields, the generated artifacts, the permission key with its field-level check, and the API messages. No override is ever accepted without the permission check, so these land before any story.

**CRITICAL**: no user story work begins until this phase is complete.

- [X] T005 [P] Extend `GameSecuritySpec` in `operator/api/v1alpha1/gametemplate_types.go:185` with `volumeOwnershipCheck` (enum `EveryStart|OnlyWhenWrong`) and `risky` (new `SecurityRiskSpec`, each member a `SecurityRiskNote{reason string, MaxLength=200}`) per [contracts/crd-gametemplate-security.md](./contracts/crd-gametemplate-security.md)
- [X] T006 [P] Add to `operator/api/v1alpha1/gameserver_types.go`: `GameServerSpec.Security *GameServerSecuritySpec` after `Capture` (`:161`) with `SecurityIDOverride{value *int64 0..4294967295, useImageDefault bool}` and the two CEL rules (exactly one of the two; `useImageDefault` must be `true`, messages as in [contracts/crd-gameserver-security.md](./contracts/crd-gameserver-security.md)); `GameServerStatus.Security *GameServerSecurityStatus` (`:558`) with `desired`, `applied`, `pending`, `SecuritySettings`, `SecurityIDStatus`, `SecurityCheckStatus` and the `source` enum (`ModuleDefault|Override|ImageDefaultOverride|ImageDefault|BuiltInDefault`); and the condition type/reason constants `SecurityAllowed` / `RootOverrideDisallowed` (data-model.md "Start refusal")
- [X] T007 Run `make generate && make manifests` and commit `operator/api/v1alpha1/zz_generated.deepcopy.go`, `operator/config/crd/gameplane.local_gameservers.yaml`, `operator/config/crd/gameplane.local_gametemplates.yaml`, both `charts/gameplane/charts/gameplane-crds/crds/*.yaml` copies and both `charts/gameplane/crd-manifests/*.yaml` copies in the same change as T005, T006 and T078 (CLAUDE.md rule 7; the Cluster CRD and its chart copies, which T078 changes); depends on T005, T006, T078
- [X] T008 [P] Add CEL and bounds cases to the NEW `operator/internal/controller/security_crd_validation_envtest_test.go` (the existing `crd_validation_envtest_test.go` is not edited): `{value:1000}` and `{useImageDefault:true}` accepted; both, neither, `useImageDefault:false`, `value:-1`, `value:4294967296`, a bad `volumeOwnershipCheck`, and a `risky.reason` over 200 characters rejected; an existing GameServer without `security` still applies
- [X] T009 [P] Add the catalog entry `{Key: "servers:security", Label: "Set or change a server's security overrides (run-as user and group, filesystem group, volume ownership check)", Namespaced: true}` after `servers:console` in `api/internal/rbac/catalog.go:25-29` ([contracts/rbac-permission.md](./contracts/rbac-permission.md)); do not add it to `privilegedPermissions`
- [X] T010 [P] Add the NEW file `api/internal/rbac/catalog_security_test.go` (the existing `catalog_test.go` and `rbac_test.go` are not edited) with a test that `servers:security` exists, is namespaced, and is held by the built-in `admin` role (through `*`) and by no other built-in role; the test may copy the `userForRole` helper from `rbac_test.go`. `TestCatalog_CoversSeededPermissions` and `TestCatalog_CoversRuleTablePermissions` must still pass unchanged
- [X] T011 [P] Add the three messages (plus `cluster.security_settings_missing`, 409, from briefs/00 section 2) `server.security.forbidden`, `server.security.root_disabled` (arg `setting`) and `server.security.invalid` (args `setting`, `detail`) to `api/internal/messages/entries_server.go` with English text from [contracts/api-server-security.md](./contracts/api-server-security.md), and the matching keys to `i18n/locales/en/app.json` and `i18n/locales/fr/app.json` (FR-014); the existing `messages_test.go` and `usage_test.go` check registration and descriptions but not use (brief 03 D2), so T012 and T057 use every ID anyway
- [X] T012 Create `api/internal/handlers/security_overrides.go` with the field-level security step called from `validateAndProtectGameServer` (`api/internal/handlers/resources.go:538`, after the capture step) for create (`:218`), update (`:290`) and clone (`lifecycle.go:243`): case 1 unchanged accepts without the permission, case 2 absent-in-request restores the live value, case 3 any other difference without `servers:security` returns 403 `server.security.forbidden`; errors carry their own status so `writeServerValidationError` (`resources_snapshot.go:24`) passes them through (research R10); depends on T006, T009, T011
- [X] T013 [P] Unit tests in the NEW file `api/internal/handlers/security_overrides_test.go` (the existing `security_overrides_test.go` is not edited) for T012 cases 1 to 3 (unchanged both absent, unchanged equal, restored field, forbidden on create, update and clone with `live == nil`)
- [X] T014 [P] Envtest `api/internal/handlers/security_overrides_envtest_test.go` for create, update and clone permission behavior: `servers:write` without `servers:security` gets 403 on a changed block, 200 on an unrelated edit that sends the same block back, 200 and kept override when the request omits the block, 403 on cloning a server that has one; an `admin` caller succeeds
- [X] T078 [P] Add the optional `ControlPlaneNamespace string` field to `ClusterSpec` in `operator/api/v1alpha1/cluster_types.go` (JSON `controlPlaneNamespace`), with `+kubebuilder:default=gameplane`, `MaxLength=63` and a DNS label pattern (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`), per OD-10 A and briefs/00 section 12. Generated with T007 (`make generate && make manifests`; the Cluster CRD copies are committed in the same change). The API cluster registration handler (`api/internal/handlers/clusters.go`) accepts and returns it; an absent value means `gameplane`. Feeds T057 and T076.

**Checkpoint**: CRDs and generated artifacts committed, permission enforced; stories can start.

---

## Phase 3: User Story 1 - Server uses the module's security defaults (P1) MVP

**Goal**: with no override a server runs exactly as today, the ownership check defaults to "only when wrong" without restarting any existing server, and the details show each value and its source (FR-001, FR-002a, FR-003, FR-004, FR-009, FR-012, SC-001, SC-004, SC-006).

**Independent test**: create a server from a module with security settings and one without; each runs with the module's values (or the image's user), `status.security` shows `ModuleDefault` / `ImageDefault` / `BuiltInDefault`, and an operator upgrade restarts no running server (quickstart S1, S6).

### Tests for User Story 1

- [X] T015 [P] [US1] Table test `operator/internal/controller/security_overrides_test.go` for `resolveGameSecurity(tmpl, gs)` (research R4): every id source (module default, image default when neither sets it, `BuiltInDefault` for the ownership check), the `fsGroupChangePolicy` mapping (`EveryStart`=`Always`, `OnlyWhenWrong`=`OnRootMismatch`, written only when a filesystem group applies), the annotation `gameplane.local/security-applied` encode/decode round trip, the pre-feature reader (no annotation: non-empty values `ModuleDefault`, empty ids `ImageDefault`, ownership check `EveryStart` with `BuiltInDefault`), and `pending` computation (source-only change is not pending)
- [X] T016 [P] [US1] Create the NEW file `operator/internal/controller/security_builders_test.go` (the existing `build_game_container_test.go` and `workload_security_test.go` are not edited): with nothing applied the container and pod `securityContext` are byte-identical to today (no empty `securityContext: {}`); helper containers (agent, config-init user, sentinel, tunnel, wipe) keep their fixed users (FR-012)
- [X] T017 [P] [US1] Envtest `operator/internal/controller/security_overrides_envtest_test.go`: a server with no override gets the module's values and `status.security.desired` equals `applied` with `ModuleDefault`; a template with no security block shows `ImageDefault`; a StatefulSet created before this feature (no annotation, no change policy) is NOT recreated when the operator reconciles it (pod template untouched, `pending=true`, `applied.volumeOwnershipCheck=EveryStart`, `desired=OnlyWhenWrong`); after a scale to zero and back the pod template carries `fsGroupChangePolicy: OnRootMismatch` and `pending=false`

### Implementation for User Story 1

- [X] T018 [US1] Create `operator/internal/controller/security_overrides.go` (NEW): the pure resolver `resolveGameSecurity` returning value plus source per setting, the pod encoding helpers, the `gameplane.local/security-applied` annotation codec, the applied-values reader (the StatefulSet annotation `gameplane.local/security-applied-observed`, copied from the pod template once an owned pod found via `ownedTemplatePod`, `gameserver_template_revision.go:480`, has `PodReadyToStartContainers=True` or a started container; pre-feature fallback to the running pod, else the StatefulSet template; research R6) and the `fsGroupChangePolicy` mapping; depends on T006, T007
- [X] T019 [US1] Keep `gameContainerSecurityContext` (`gameserver_controller.go:2496`), `gamePodSecurityContext` (`:2612`) and `buildConfigInitContainer` (`:2336`, its `RunAsGroup` and `chgrp` target) callable with their current signatures, because existing tests call them (briefs/00 section 11). Add `...Resolved` variants that take the resolved values and are used by production code; the old names become wrappers that resolve from the template. Keep the `nil`-when-empty behavior; depends on T018
- [X] T020 [US1] In `reconcileStatefulSet` (mutate function inside `CreateOrUpdate` at `gameserver_controller.go:1637`, gate near `:1916-1925`; briefs/01 section 3 and briefs/00 section 11; `reconcileStatefulSet` keeps its signature) write the security-derived pod-template parts (pod `securityContext`, game container `securityContext`, config-init container, the `security-applied` annotation) only when `ss.ResourceVersion == ""` or the StatefulSet goes from zero replicas to one or more; otherwise copy them forward from the existing StatefulSet (research R5, R8). This is what keeps the ownership-check default flip from restarting servers on operator upgrade (SC-001); depends on T018, T019
- [X] T021 [US1] In `operator/internal/controller/gameserver_status.go` publish `status.security` (`desired`, `applied`, `pending`) with a `MergeFrom` patch of that field only (the agent's `status.agent` heartbeat stays untouched, `:42-60` pattern); `applied` omitted before the first start; depends on T018
- [X] T022 [P] [US1] Add `mcp-server/main_test.go` fixture asserting a GameServer carrying `spec.security` and `status.security` round-trips unredacted through the read-only tools (research R15); `mcp-server/README.md` gets one example and `mcp-server/specs.md` one sentence (no code change in `mcp-server/tools.go`)
- [ ] T023 [P] [US1] **Needs T003.** Mirror the types in `web/src/types.ts`: `GameServer.spec.security` and `status.security` (`:557`), `GameTemplate.spec.security` with `risky` and `volumeOwnershipCheck` (`:303`)
- [ ] T024 [US1] **Needs T003.** Create `web/src/components/SecurityAdvanced.tsx` (NEW shared section, collapsed by default, read-only first cut) showing per setting the module default or "image default", the applied value and its source, and the "no effect" marker for the ownership check when no filesystem group applies, exactly as the exported frames in `assets/design-export/`; depends on T023
- [ ] T025 [US1] **Needs T003.** Add `web/src/components/SecurityAdvanced.test.tsx` (vitest: sources render, no value renders "image default", the ownership check marks "no effect") and the English strings under `web.routes.server_settings.security_*` in `i18n/locales/en/app.json` with the `fr` pack entries (FR-014); depends on T024
- [X] T075 [US1] Exclude `spec.security` from the template baseline: `baselineRenderInputs` (`operator/internal/controller/gameserver_template_revision.go:338-340`, the range OD-9 cites; verify before editing) leaves out `spec.security`, so saving an override never rewrites a running pod (FR-008, OD-9 item 6). Test cases go in `security_overrides_envtest_test.go` (T039). Depends on T006.

**Checkpoint**: no-override behavior and sources verified; MVP. Compile checks: `go build ./...` in `operator` and `mcp-server`, `npx tsc --noEmit` in `web`.

---

## Phase 4: User Story 2 - Override the security settings when creating a server (P1)

**Goal**: a `servers:security` holder sets any of the four settings in the wizard's Advanced section, the server runs with only that setting changed, "use image default" applies nothing, and invalid values are refused before anything is created (FR-002, FR-003, FR-005 shape, FR-006, FR-006a, SC-002; OD-5, OD-6).

**Independent test**: in the wizard open Advanced, set run-as user to a non-default value, create the server, confirm the running uid with `id -u` while the other settings keep the module default (quickstart S2, S8).

### Tests for User Story 2

- [X] T026 [P] [US2] Add cases to `operator/internal/controller/security_overrides_test.go` (created by T015): override `value`, override `useImageDefault` (value none, source `ImageDefaultOverride`), override over module default, module default over nothing; restricted-mode validation on the effective values (OD-5): a root value or "use image default" for run-as user is refused by `validateRestrictedTemplate`'s rule while a positive override of a root module default passes
- [X] T027 [P] [US2] Extend `operator/internal/controller/security_overrides_envtest_test.go`: a server created with `runAsUser {value: 2000}` gets 2000 with `Override` and the other settings `ModuleDefault`; `runAsGroup {useImageDefault: true}` leaves the container `runAsGroup` unset; with `RestrictedWorkloads` on, an override the rule refuses sets the existing failure phase message (`gameserver_controller.go:334` behavior) and a pod that is already running is untouched (OD-5)
- [X] T028 [P] [US2] Add cases to `api/internal/handlers/security_overrides_test.go` and `security_overrides_envtest_test.go` (created by T013 and T014): shape cases (`value` 4294967296, `-1`, both `value` and `useImageDefault`, neither, `useImageDefault:false`, unknown member, bad enum) return 422 `server.security.invalid` with the offending setting in `args` and create nothing; a valid override is stored and returned
- [ ] T029 [P] [US2] **Needs T003.** vitest for the wizard section in `web/src/components/SecurityAdvanced.test.tsx` and the NEW `web/src/routes/CreateServerSecurity.test.tsx` (the existing CreateServer test is not edited): no security values are sent when Advanced is never opened (US2 scenario 1), each setting shows module default and an empty override (scenario 2), one override changes only that setting (3), "use image default" is sent as `{useImageDefault: true}` (4), an invalid value is rejected before submit (5), a risky setting asks for the confirmation with the module's reason and cancelling sends nothing (OD-6: dashboard-only)
- [ ] T030 [P] [US2] Kind E2E `test/e2e/gameserver_security_e2e_test.go` (NEW, `//go:build e2e`, `TestGameServer_SecurityOverrideAppliesOnRestart`, `t.Parallel()`, unique names, busybox fixture from `test_helpers_e2e_test.go:42-90`, zero logins): part 1 creates a server with a run-as user override and asserts the running uid via `Env.KubectlExec(... id -u)` (`env.go:259`) with the other ids at the module default

### Implementation for User Story 2

- [X] T031 [US2] Extend `resolveGameSecurity` in `operator/internal/controller/security_overrides.go` with the override layer (research R4): `useImageDefault` gives none and `ImageDefaultOverride`, `value` gives that number and `Override`, else module default, else none; ownership check override over template over `OnlyWhenWrong`
- [X] T032 [US2] Make `validateRestrictedTemplate` and `restrictedContainerSecurity` (`workload_security.go:15, 23`), `applyWorkloadSecurity` (`:39`) and the call sites at `gameserver_controller.go:334`, `gameserver_template_revision.go:216, 278` validate and apply the effective (desired) values; a violation reports the server failed with the existing message until fixed; a running pod is untouched (OD-5 settled A)
- [X] T033 [US2] Add the shape validation (case 4: ids whole numbers 0 to 4294967295, exactly one of `value`/`useImageDefault`, `useImageDefault` true, no unknown members, valid enum) to `api/internal/handlers/security_overrides.go`, returning 422 `server.security.invalid`; the CRD CEL stays the second line
- [ ] T034 [US2] **Needs T003.** Make `SecurityAdvanced.tsx` editable for `servers:security` holders (value, "use image default", clear) with client-side range validation and the risky-value confirmation dialog showing `template.spec.security.risky.<setting>.reason` (plain text, never rendered as markup); depends on T024
- [ ] T035 [US2] **Needs T003.** Add the collapsed Advanced section to `web/src/routes/CreateServer.tsx`, sending `spec.security` only when a setting was set; surface the server refusals inline (the `root_disabled` message tells an administrator can allow it in Settings); add `web.routes.create_server.advanced_*` keys to `i18n/locales/en/app.json` and `fr`; client calls in `web/src/lib/api.ts` pass `spec` already (no new route); depends on T034
- [ ] T036 [P] [US2] Module repo (separate PR to `GameplanePanel/module`, branch from T002): regenerate `.schema/gametemplate.schema.json` with `make module-schema` (`hack/gen-module-schema.py:21-22`, run from this repo after T007 merges or on its branch, output copied into the module repo); add to `validate.py` the rules that a `risky` key is one of the four settings, a `reason` over 200 characters is an error, and `volumeOwnershipCheck` without a template `fsGroup` is a warning (existing rules at `validate.py:610-660, 903-958` unchanged); add the NEW file `test-validate-py-security.sh` (the existing `test-validate-py.sh` is not edited); depends on T007
- [ ] T037 [P] [US2] Module repo, same PR: mark `runAsUser` risky with a reason in `palworld/template.yaml` ("The image must start as root to drop privileges; forcing a user breaks its start.") and update `palworld/specs.md`; other modules only if the module review says so
- [X] T038 [P] [US2] Add the same three template checks to `gp-module/internal/validator/rules.go`, with tests in the NEW `gp-module/internal/validator/rules_security_test.go` (the existing validator tests are not edited), so `gp-module validate` agrees with `validate.py`; make `gp-module/internal/preview/` print the resolved defaults, the ownership check and the risky settings with their reasons, with tests in the NEW `gp-module/internal/preview/preview_security_test.go`; leave `internal/scaffold/scaffold.go:167, 213` unchanged (no archetype asks for `risky`); tests under the gp-module gate (80)

**Checkpoint**: US1 and US2 work end to end for an admin; module repo PR open. Compile checks as above plus `go build ./...` in `gp-module`.

---

## Phase 5: User Story 3 - Change or remove an override on an existing server (P2)

**Goal**: a saved change applies on the next restart and never restarts the server; removing an override returns the module default; module upgrades keep overrides; backups and restores use the applied filesystem group; the dashboard warns and shows pending values (FR-007, FR-008, FR-009, FR-011, SC-004).

**Independent test**: on a running server set a run-as user override, save, observe no restart and `pending: true`, restart and observe the new uid, remove the override, restart and observe the module default (quickstart S3, S7).

### Tests for User Story 3

- [ ] T039 [P] [US3] Extend `operator/internal/controller/security_overrides_envtest_test.go`: saving a changed override does not change the StatefulSet pod template or the pod while it runs (same generation of the template, `pending=true`, `applied` unchanged); an unrelated env change rolls the pod without picking up the pending security change; scale to zero and up refreshes and clears `pending`; editing back to the applied value clears `pending`; removal returns `ModuleDefault`; a template rollout keeps an overridden setting and the others follow the new default (US3 scenario 3); a never-started server has no `applied`
- [ ] T040 [P] [US3] Create the NEW file `operator/internal/controller/security_backup_envtest_test.go` (the existing `backup_envtest_test.go` is not edited) and add the unit cases to the NEW `operator/internal/controller/security_overrides_test.go`: with a pending `fsGroup` change on a stopped server, and during a pending start whose pod has not passed volume setup, `buildBackupPodSecurityContext` and the restore pod (`restore_controller.go:433`) use `applied.fsGroup`, not `desired`; a start that fails before volume setup leaves `applied` and `pending` unchanged; a never-started server uses the desired value
- [ ] T041 [P] [US3] **Needs T003.** vitest in the NEW `web/src/routes/tabs/settings/Security.test.tsx` (the existing `Settings.test.tsx` is not edited): the section shows applied, pending and the "applies on the next restart" notice; saving a changed run-as user or group on a server with data shows the file-access warning first; removing an override shows "module default"; the save never triggers a restart call
- [ ] T042 [P] [US3] Extend `test/e2e/gameserver_security_e2e_test.go` (`TestGameServer_SecurityOverrideAppliesOnRestart` part 2): change the override and assert the same pod UID, `status.security.pending` true and `applied` unchanged; restart and assert the new uid and `pending` false; remove the override, restart, assert the module default

### Implementation for User Story 3

- [ ] T043 [US3] Give `buildBackupPodSecurityContext` (`backup_controller.go:1156`, caller `:1058`) a trailing variadic applied filesystem group (briefs/00 section 11), and pass it from the restore call (`restore_controller.go:433`, job call `:277`) and from the backup chain (`backup_worker.go:306`, through `:129-130`). The applied group comes from the same pod or StatefulSet read as `status.security.applied` (FR-011, research R6); fall back to the desired value only for a server that never started; depends on T018
- [ ] T044 [US3] The status writer recomputes `pending` when the template pin changes (template rollout admitted, `gameserver_template_revision.go`) without touching the running pod. A rollout on a suspended or sleeping server ends at zero replicas, so its security refresh happens at the next start, not at the rollout, and `pending` stays true until then (OD-9 item 5, briefs/01 section 4.3). Tests must not assert a start for those servers. Confirm that `OnRequest` and `Automatic` rollouts on a running, awake server both end in a start that refreshes the template (plan.md spec gap 3)
- [ ] T045 [US3] **Needs T003.** Create `web/src/routes/tabs/settings/Security.tsx` (NEW) and mount it from `web/src/routes/tabs/Settings.tsx` (mounted by `ServerDetail.tsx:377`): the shared `SecurityAdvanced` section for an existing server with the pending marker, the restart notice, the file-access warning and clear-to-module-default; the whole spec sent back unchanged is accepted by the API (case 1); depends on T034
- [ ] T046 [US3] **Needs T003.** Add `web.routes.server_settings.security_*` notice and warning strings to `i18n/locales/en/app.json` and `fr` (pending, restart notice, file-access warning); depends on T045

**Checkpoint**: change, restart and removal verified; backups safe.

---

## Phase 6: User Story 4 - Restrict who can force security settings (P2)

**Goal**: only `servers:security` holders change the four settings (dashboard read-only otherwise, API 403), root values need the admin switch, a stopped server holding a root override is refused a start while the switch is off, and every change, refusal and flip is audited (FR-005, FR-005a, FR-010, SC-003, SC-005; OD-1, OD-2, OD-4, OD-6).

**Independent test**: as a user without the permission the Advanced fields are read-only and an API override gets 403; as an admin with the switch off a root value gets 403, and a stopped root-override server will not start (quickstart S4, S5, S5a, S9).

### Tests for User Story 4

- [ ] T047 [P] [US4] Create the NEW API test files `api/internal/handlers/security_settings_test.go` (validator and shapes: `{"allowRootOverrides":true}` accepted, non-boolean 422, empty body 400 `server.empty_body`, unknown members dropped) and `api/internal/handlers/security_settings_envtest_test.go` (a dedicated router with `rbac.Middleware`, never `mountedR`, and the test cluster registered): `GET /security-settings?cluster=<id>` round trip, and `present` false for a missing ConfigMap; `PUT` by a viewer without `config:manage` gets 403; a flip updates the ConfigMap `gameplane-security-settings` to `"true"` or `"false"` and writes one `security/<cluster id>` row; a failing audit write returns 500 and leaves the ConfigMap unchanged; a failing update returns 502 and writes `security_save_failed`; a missing ConfigMap returns 409 `cluster.security_settings_missing` and writes no row; nothing creates the ConfigMap ([contracts/cluster-setting.md](./contracts/cluster-setting.md))
- [ ] T048 [P] [US4] Add cases to `api/internal/handlers/security_overrides_test.go` and `security_overrides_envtest_test.go` (created by T013 and T014) for the root rule (case 5): a new or changed `runAsUser` or `runAsGroup` with `value: 0` or `useImageDefault`, or `fsGroup` `value: 0`, gets 403 `server.security.root_disabled` while off and is accepted once on; `fsGroup` `useImageDefault` is free; an unchanged stored root value passes an unrelated whole-spec PUT with the setting off; removal and replacement are always allowed
- [ ] T049 [P] [US4] Audit tests in `api/internal/handlers/security_overrides_test.go` (unit) and `security_overrides_envtest_test.go`: the `reason` builder for not set, number, `"image"`, ownership check, create (`before` `{}`), and the `risky` list (a risky changed setting appears with names only; non-risky, unchanged risky and an absent `risky` map add no key; the module's reason text never appears; OD-6); each of change, permission refusal (`security_forbidden`), root refusal (`security_root_disabled:runAsUser`), invalid (`security_invalid:fsGroup`) and setting flip (`allowRootOverrides:false->true`) writes exactly one row with `MarkRecorded`, a change row is written before the Kubernetes write, a failed `WriteSync` on a change returns 500 with nothing saved, a failed Kubernetes write after the row adds a `security_save_failed` row, a failed `WriteSync` on a refusal falls back to the middleware row, and `GET /admin/audit/verify` still passes ([contracts/audit-events.md](./contracts/audit-events.md))
- [ ] T050 [P] [US4] Operator unit tests in the NEW `operator/internal/controller/security_settings_test.go` for `hasRootOverride` and `rootOverrideSettings` (every root form in the rule table, plus non-root values and a module-default root that is not an override) and the ConfigMap reader (absent, missing key, `"false"`, garbage and `"true"` read as off/off/off/off/on)
- [ ] T051 [P] [US4] Envtest `operator/internal/controller/security_overrides_envtest_test.go` for OD-4 (FR-005a): with the ConfigMap absent or `"false"`, a stopped server holding a root override stays at zero replicas with `SecurityAllowed=False` reason `RootOverrideDisallowed` naming the settings; a running one keeps its pod and restart count and gets only the informational condition; flipping the ConfigMap to `"true"` starts the stopped one without any other event (the watch re-enqueues it); clearing the override starts it; flipping off never restarts or stops any server; unrelated servers are not enqueued by the flip
- [ ] T052 [P] [US4] **Needs T003.** vitest: the NEW `web/src/components/clusters/SecuritySettingsPanel.test.tsx` (the switch is off by default, reads `GET /security-settings?cluster=<id>`, saves through `PUT /security-settings?cluster=<id>`, shows the "does not change existing servers" text and the missing-settings state) and the NEW cases in `web/src/components/SecurityAdvanced.test.tsx` (no `servers:security`: applied, sources and pending visible and read-only, no `spec.security` is ever sent; a server held back shows the `RootOverrideDisallowed` reason for the stopped and running variants; the three API refusals render inline)
- [ ] T053 [P] [US4] Kind E2E `test/e2e/api_security_override_e2e_test.go` (NEW, `//go:build e2e`, `TestAPI_SecurityOverride_PermissionAndRootRule`, `t.Parallel()`, unique names): one admin login and one login as its own non-admin user created for the test; the non-admin (holds `servers:write`, not `servers:security`) is refused 403 on create and on update; granted `servers:security` it succeeds; a root value is refused 403 while `PUT /security-settings?cluster=<id>` has the setting off, and accepted after the admin sets it on; the ConfigMap `gameplane-security-settings` value follows each PUT; the audit log has the rows
- [ ] T054 [P] [US4] Extend `test/e2e/gameserver_security_e2e_test.go` (`TestGameServer_SecurityOverrideAppliesOnRestart` part 3, zero logins): save a root `fsGroup` override, stop the server with the ConfigMap at `"false"` (created by the chart, T077), start it and assert it stays at zero replicas with `SecurityAllowed=False` / `RootOverrideDisallowed`; set the ConfigMap to `"true"` with `kubectl` and assert it starts; set it back to `"false"` and assert the running pod keeps its UID and restart count

### Implementation for User Story 4

- [ ] T055 [US4] Add the root rule (case 5) to `api/internal/handlers/security_overrides.go` (NEW, see briefs/00 section 9): read the setting from the ConfigMap `gameplane-security-settings` of the request's own cluster, through that cluster's registry client (`resolveCluster`), and only on a request that carries a new or changed root value. Exact `"true"` is on; any other result (missing, unreadable) is off. Refuse with 403 `server.security.root_disabled` and `args.setting`. The store is not used for this rule
- [ ] T056 [US4] Create `api/internal/handlers/security_settings.go` (NEW): `GET` and `PUT /security-settings?cluster=<id>`, mounted by `MountSecuritySettings(r chi.Router, reg *kube.Registry, auditor *audit.Auditor)` from `api/cmd/main.go` (briefs/00 section 2). Rules `config:read` (GET) and `config:manage` (PUT) in `api/internal/rbac/rbac.go`, next to the `/admin/config` rules (`:209`, `:214`). Empty body 400 `server.empty_body`; invalid JSON or non-boolean 422. Read the ConfigMap by name first: NotFound makes GET report `present` false, and PUT return 409 `cluster.security_settings_missing` with no row. On a changed value write the flip row first (`WriteSync`, `target` `security/<cluster id>`, `reason` `allowRootOverrides:false->true` or `true->false`; 500 and no change if it fails), then update the ConfigMap by name (never create). An update failure returns 502 and writes `security_save_failed` ([audit-events.md](./contracts/audit-events.md) "Write order"). No config-table row and no `newValidators` entry. Depends on T057
- [ ] T057 Add the per-cluster ConfigMap helper used by T055 and T056 in `api/internal/handlers/security_settings.go`: read by name, update by name, no create. Resolve the namespace for each cluster. The home cluster uses `moduleConfigMapNS` (`api/cmd/main.go:231`, the credentials namespace on a default install). A remote cluster's control-plane namespace is read from its registration, `Cluster.spec.controlPlaneNamespace` (T078, OD-10 A; an absent value means `gameplane`); do not hard-code one (briefs/00 section 12). Depends on T078. Implement this before T056
- [ ] T058 [US4] Add the API's ConfigMap rule to the home Role in `charts/gameplane/templates/api-credentials.yaml` (`:14-37`, credentials namespace): `resources: [configmaps]`, `resourceNames: [gameplane-security-settings]`, `verbs: [get, update, patch]`. No `create` verb (briefs/00 section 4). For standalone, add the equivalent Role and RoleBinding (subject `gameplane-api`, release namespace) inside the `apiEnabled` guard of `charts/gameplane/templates/api.yaml`, after the logs RoleBinding and before that guard's closing `{{- end }}`. The operator ClusterRole already has `get;list;watch` on ConfigMaps (`charts/gameplane/templates/operator.yaml:66`). No chart test is added without sign-off (briefs/04 Q3)
- [ ] T059 [US4] Wire `MountResources` (`api/internal/handlers/resources.go:51`, called from `api/cmd/main.go:396`) to receive the `*audit.Auditor`. In create (`:250`) and update (`:322`), write one synchronous row via `Auditor.WriteSync` before the Kubernetes write (500 and no save on failure; a `security_save_failed` row if the save then fails; [contracts/audit-events.md](./contracts/audit-events.md) "Write order"), then `audit.MarkRecorded` for a changed block and for each refusal. Clone (`lifecycle.go:243`): `MountLifecycle(r, reg, auditor ...*audit.Auditor)` takes the auditor as an optional variadic parameter, so the existing call sites compile, and clone writes the same row (OD-9 item 3). Build `reason` as `security:{"before":..,"after":..,"risky":[..]}` from the two blocks and the pinned template's `risky` map (names only, OD-6), in `api/internal/handlers/security_overrides.go`
- [ ] T060 [US4] Create `operator/internal/controller/security_settings.go` (NEW): the ConfigMap reader from the informer cache (namespace from `ControlPlaneNamespace`, briefs/02 D7) (fail closed), `hasRootOverride`, `rootOverrideSettings`, and the map function that enqueues every GameServer holding a root override; add `Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(...))` filtered to that name and namespace next to the Pod and GameTemplate watches (`gameserver_controller.go:609-611`), no new RBAC marker needed (`:261` already lists ConfigMaps)
- [ ] T061 [US4] In `reconcileStatefulSet` hold a stopped server with a root override at zero replicas while the setting is off (before the R5 refresh, so the StatefulSet is left exactly as it was) and never touch a running one; set the `SecurityAllowed` condition (`False` / `RootOverrideDisallowed` with the settings named and what clears it; `True` once allowed or cleared) in `gameserver_status.go`; emit an event; never restart or stop on a flip (research R9, FR-005a); also held before `reserveWakeStart` (`gameserver_idle.go:333`), so a held sleeping server takes no wake slot (OD-9 item 4); depends on T020, T060
- [ ] T062 [US4] **Needs T003.** Make `SecurityAdvanced.tsx` read-only without the permission (`can("servers:security", namespace)` from the permission data the dashboard already loads, [contracts/rbac-permission.md](./contracts/rbac-permission.md)) and show the `RootOverrideDisallowed` notice with the stopped and running texts; depends on T034, T045
- [ ] T063 [US4] **Needs T003.** Add the "Security" panel with the allow-root switch as the NEW `web/src/components/clusters/SecuritySettingsPanel.tsx` (not Admin Settings), mounted in `web/src/components/clusters/ClusterConfiguration.tsx` next to `BackupDestinationsPanel.tsx`. Add its get and put client methods to `web/src/lib/api.ts`, and its strings under `web.clusters.security_*` in `i18n/locales/en/app.json` and `fr` (FR-014); depends on T052

- [ ] T076 [US4] Remote-chart RBAC: find in the chart the identity the API uses for a remote cluster (the remote operator install path under `charts/gameplane/`), and grant it `get`, `update` and `patch` on the ConfigMap `gameplane-security-settings` by `resourceNames`, in that cluster's control-plane namespace, which is the `Cluster.spec.controlPlaneNamespace` value on its registration (T078, OD-10 A; the remote chart's namespace must render the same value). No `create` verb (OD-8 A). If the identity cannot be found in the chart, stop and ask (briefs/00 section 7). Depends on T077 and T078.
- [ ] T077 [US4] Helm creates the ConfigMap `gameplane-security-settings` in the operator's control-plane namespace, wherever the operator chart renders (home and remote installs). Data `allowRootOverrides: "false"`, annotation `helm.sh/resource-policy: keep`. On upgrade it reads the current object with Helm `lookup` and re-renders its value, so an upgrade never flips it. A deleted object reads as off until the next `helm upgrade` (OD-8 A, briefs/00 section 4). CI checks the render with `helm template`; no chart test is added without sign-off.
- [ ] T079 [US4] **Needs T003.** The register-cluster form (web) gets an optional "Control-plane namespace" field, default `gameplane`, sent as `spec.controlPlaneNamespace` through the cluster registration call in `web/src/lib/api.ts` (no new route); the cluster page shows the stored value. Strings under `web.clusters.control_plane_namespace_*` in `i18n/locales/en/app.json` and `fr` (FR-014). Vitest for the field's default, edit and invalid value (not a lowercase DNS label, or over 63 characters) in a NEW test file (name proposed in the PR; the maintainer accepts or renames it, briefs/00 section 7). Depends on T003, T063 and T078.

**Checkpoint**: all four stories functional. Compile checks: `go build ./...` in `operator`, `api`; `go vet -tags e2e ./...` in `test/e2e`; `npx tsc --noEmit` in `web`.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: register the E2E tests, document the feature, land the module PR and pointer bump, and close the loose ends. Docs tasks do not wait for the design.

- [ ] T064 Register both tests in `bucket_operator` of `test/e2e/buckets.sh` (`TestGameServer_SecurityOverrideAppliesOnRestart`, `TestAPI_SecurityOverride_PermissionAndRootRule`). **Bucket decision**: `operator` already spends about four admin logins ("operator: 4 admin logins with the EventStream test", `buckets.sh:355-362`) against the documented ceiling near seven; `TestAPI_SecurityOverride_PermissionAndRootRule` adds one admin login plus one login as its own user, so the bucket reaches about five admin logins and fits. `api-auth`, `api-roles`, `api-rbac`, `api-agent` and `api-mods` are at their ceilings and must not receive it. Add a comment above the bucket recording the tally; no new bucket, CI matrix entry or `suite-components.json` entry is needed (`^TestGameServer_` and `^TestAPI_` already map both tests, `suite-components.json:32, 46`); `buckets.sh verify` must still pass; depends on T030, T053
- [ ] T065 [P] Rewrite "Security context" in `docs/module-authoring.md` (`:1137-1173`) per FR-013: the block is a default that server owners can override; the new `volumeOwnershipCheck` and `risky` fields in the field table; when to mark a value risky (the image only works with that uid, or must start as root) and how to word the reason (one sentence, what breaks); when to set `EveryStart`; the built-in default flip to `OnlyWhenWrong`
- [ ] T066 [P] Add a "Server security overrides" subsection under "Pod security" in `docs/security.md` (`:435`): who may override (`servers:security`, admin by default), the root rule and the per-cluster allow-root setting, the refusal to start (`RootOverrideDisallowed`), apply-on-restart, the applied filesystem group for backups, the ownership-check trade-off, the audit trail, and that `kubectl` writers bypass the API checks
- [ ] T067 [P] Update `operator/specs.md` (resolver, gate, annotation, status, `SecurityAllowed`, ConfigMap watch, restricted-mode effective values), `api/specs.md` (permission, field-level check, the `/security-settings` routes and the per-cluster ConfigMap, audit reason), `web/specs.md` (shared Advanced section, wizard, Settings, cluster Security panel), `gp-module/specs.md` (validator rules, preview), `mcp-server/specs.md` (read-only exposure, T022) and `test/e2e/specs.md` (the two tests); each in the same change as its code (constitution IV)
- [ ] T068 [P] Add the new files (`operator/internal/controller/security_overrides.go`, `security_settings.go`, `api/internal/handlers/security_overrides.go`, `security_settings.go`, `web/src/components/SecurityAdvanced.tsx`, `web/src/components/clusters/SecuritySettingsPanel.tsx`) to the task map in `docs/agent-architecture.md`
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
- Phase 4 (US2) depends on Phase 3 (it extends `security_overrides.go`, `SecurityAdvanced.tsx`, and the E2E file). Module-repo tasks T036-T038 depend only on T007 and can start as soon as Phase 2 is merged, in parallel with Phase 3.
- Phase 5 (US3) depends on Phase 3 for the gate and status (T020, T021) and on Phase 4 for the editable section. T043 can start after T018.
- Phase 6 (US4) depends on Phase 2 and on T020 (the gate it extends). The API half (T047-T049, T055-T059), the chart RBAC (T058, T076, T077) and the operator half (T050, T051, T060, T061) are independent of Phases 4 and 5 and can run in parallel with them; only the web tasks (T062, T063, T079) need the earlier web tasks and T003, and T079 also needs T078.
- Phase 7 depends on its inputs: T064 needs T030 and T053; T069 needs T036 and T037; T070 needs T069.
- Phase 8 is last.
- **Shipping constraint**: US2's API accept path must not reach a release without US4's root rule and per-cluster ConfigMap; Phase 2 already enforces the permission, but the root rule (T055) and the operator refusal (T061) land in the same release as US2's wizard (T035).

### Hard blockers

- **T003 (maintainer-owned design) blocks T023, T024, T025, T029, T034, T035, T041, T045, T046, T052, T062, T063, T079.** If the design is late, every other task proceeds and these wait.
- T007 (generated CRDs) blocks every operator, API test and module-schema task.
- T069 (module PR merged) blocks T070; T070 blocks T074. The main-repo changes work without them (a template with no `risky` block has no risky values).

### Within each story

- Tests first (they fail in CI until the implementation lands in the same PR).
- Resolver and codec (`security_overrides.go`) before builders, builders before the StatefulSet gate, the gate before status and the start refusal.
- API: validator step before audit; the `/security-settings` routes after the ConfigMap helper (T057); chart RBAC with the helper.
- Web: types before the shared section, the shared section before the wizard, Settings tab and admin switch.

### Parallel opportunities

- T002 ‖ T003 ‖ T004; T005 ‖ T006; T008 ‖ T009 ‖ T010 ‖ T011 ‖ T013 ‖ T014.
- Phase 3 tests T015-T017 in parallel; T022 and T023 in parallel with the operator work.
- Phase 4 tests T026-T030 in parallel; the module repo PR (T036-T037) and gp-module (T038) in parallel with the operator and web work.
- Phase 6 tests T047-T054 in parallel; the API half and the operator half in parallel; docs T065-T068 in parallel with everything after Phase 2.

## Parallel Example: Phase 6 (US4)

```text
Workflow batch A (haiku, one agent each): T047, T048, T049, T050, T051: test files only, from the brief
Workflow batch B (haiku): API half T055, T056, T057, T059 (sequential in one agent, same files); chart T058, T076, T077 (separate agent)
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
3. US4 adds the root rule, the per-cluster switch, its ConfigMap and the operator start refusal, and the audit trail; US2's wizard ships with it.
4. Phase 7 registers E2E, documents, bumps the pointer after the module PR merges; Phase 8 archives.

## Notes

- Do not renumber tasks; a new task takes the next free ID and states its phase.
- The OD-4 refusal is exercised without the API in the E2E (the test writes the ConfigMap with `kubectl`), and the `/security-settings` route is exercised in the API E2E (T053), so each test stays within its login budget.
- No `.pen` file is read or edited by any task except T003, which is the maintainer's.
