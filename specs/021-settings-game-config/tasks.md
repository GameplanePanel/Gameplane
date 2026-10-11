---
description: "Task list for Feature 021: Edit game configuration from server Settings"
---

# Tasks: Edit game configuration from server Settings

**Input**: `specs/021-settings-game-config/spec.md`, `plan.md`, `OPEN-DECISIONS.md`.
**Local verification (rule 8)**: none beyond compile checks; CI is the verification authority.
**Format**: `T### [P?] [Group] Description` — Groups: Web, Test, Docs, Visual.

## Phase 1: Shared pieces
- [X] T001 [Web] `web/src/types.ts`: add `min`, `max`, `minLength`, `maxLength` to the configSchema type.
- [X] T002 [Web] `web/src/lib/validation.ts`: add `CONFIG_REDACTED_MARKER`, `ConfigError.text`, int/bool/length checks.
- [X] T003 [Test] `web/src/lib/validation.test.ts`: additive value-check tests.
- [X] T004 [Web] `web/src/components/server/ConfigFields.tsx`: `ConfigFieldInput` + `ConfigFields` (variants `wizard`, `settings`).
- [X] T005 [Test] `web/src/components/server/ConfigFields.test.tsx`.
- [X] T006 [Web] `web/src/routes/CreateServer.tsx`: use `ConfigFields` (wizard DOM unchanged).

## Phase 2: Settings section
- [X] T007 [Web] `web/src/routes/tabs/settings/GameConfig.tsx`.
- [X] T008 [Web] `web/src/routes/tabs/Settings.tsx`: register `config` after Version, read-only footer note.
- [X] T009 [Test] `GameConfig.test.tsx` and `Settings_gameconfig.test.tsx`.

## Phase 3: 404
- [X] T010 [Web] `web/src/routes/NotFound.tsx`, splat route in `web/src/router/tree.tsx`, breadcrumb in `AppLayout.tsx`.
- [X] T011 [Test] `NotFound.test.tsx`, `AppLayout_notfound.test.tsx`.

## Phase 4: Visual diff
- [X] T012 [Visual] Screenshot fixtures: mc-survival `spec.config`, single-get configSchema.
- [X] T013 [Visual] `web/e2e/screenshots/slice-gameconfig.spec.ts` (OC804, QFEg9, Ne5TA).

## Phase 5: Docs
- [X] T014 [Docs] `web/specs.md` (Settings list, invariant 8, component list, routing).
- [X] T015 [Docs] `docs/module-authoring.md` (configSchema editable after create; restart; orphaned keys).
- [X] T016 [Docs] This spec folder.

## Phase 6: Verification and merge
- [ ] T017 CI green (web, web e2e mock, design vs browser visual diff).
- [ ] T018 PR labels `type: feature`, `area: web`; human approval (rule 12).
- [ ] T019 Archive to `specs/done_021-settings-game-config` after merge (rule 16).

## Phase 7: Clear a stored optional password (OD-2)
- [X] T020 [Visual] `design.pen` OC804/QFEg9 Remove button, new frames h5clan (will be removed) and ffmEd (new password typed); export to `assets/design-export/`.
- [X] T021 [Web] `ConfigFields.tsx` (Remove/Undo, removing state) and `GameConfig.tsx` (`removing` set, key absent while removing).
- [X] T022 [Test] New tests for Remove, Undo, required password, typing after Remove; existing tests untouched.
- [X] T023 [Visual] `web/e2e/screenshots/slice-gameconfig.spec.ts`: add h5clan and ffmEd states.
- [ ] T024 CI green, PR labels `type: feature`, `area: web`, `area: specs`; human approval.

## Notes
- No existing test is modified. `wxINm` and `e9FnC` frames are not captured (need per-state fixtures).
