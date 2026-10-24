---
description: "Task list for 028 split big packages and files"
---

# Tasks: Split big packages and files

**Input**: [spec.md](./spec.md), [plan.md](./plan.md), [research.md](./research.md), [contracts/](./contracts/), [OPEN-DECISIONS.md](./OPEN-DECISIONS.md)

**Tests**: no test is added, changed or deleted except the unit tests of the two new `hack/` scripts and the mechanical edits OD-6/OD-9 allow. Moving a test file unchanged is a move. Tests and lint run only in CI (CLAUDE.md rule 8).

**Every PR** follows the plan's per-PR procedure: open-PR check, scout brief (haiku) per [contracts/move-brief.md](./contracts/move-brief.md), move by one haiku agent, local compile check only, `move_parity.py` checks, sonnet diff review, `ci: full-run` label, `type: refactor` + `area:` labels, parity report per [contracts/parity-report.md](./contracts/parity-report.md), CI and CodeRabbit clean before ready. Each helper brief repeats: follow CLAUDE.md, explicit `model:` starting at haiku, never call `mcp__hearthbot__` tools, no local test/lint runs, no test edits beyond OD-6/OD-9, signed commits, no attribution block or session links. A PR starts only after the previous PR in its phase merges.

**Format**: `[ID] [P?] [Story] Description`. `[P]` = may run in parallel (different files, no dependency).

## Phase 1: Setup

- [X] T001 Settle OD-1..OD-10 in `OPEN-DECISIONS.md` and fold them into spec, plan and these tasks (valgul, 2026-10-20/21)
- [X] T002 Confirm spec 027's `docs/layout.md` has merged to master (027 implementation PR); if not, follow `specs/027-contributor-docs-layout-map/OPEN-DECISIONS.md` and cite it in every brief *Done note: merged via PR #795 (2026-10-22); briefs cite `docs/layout.md`.*
- [X] T003 Re-measure on current master: production files over 500 lines (Go, web), files per package for `api/internal/handlers` and `operator/internal/controller`, and record the SHA and numbers at the top of `research.md` *Done note: research.md "Re-measure"; raised OD-11.*

## Phase 2: Foundational (blocks every move PR)

- [X] T004 Write `hack/move_parity.py` (stdlib) with `names <base> <head>` (static scan of Go `Test*/Fuzz*/Example*` funcs and Vitest/Playwright title chains) and `test-edits <base> <head> --brief <file>` (R2 point 3: allowed kinds from OD-6 and OD-9 only)
- [X] T005 Write `hack/test_move_parity.py` unit tests: equal and unequal name lists; a renamed test file with only a package-line change passes; an assertion change fails; an extra `".."` in a copied envtest CRD path passes only when OD-9 allows it; a changed `vi.mock` path passes
- [X] T006 Wire `hack/test_move_parity.py` into the target that `make test-ci-scope` runs in CI (`.github/workflows/ci.yaml:1521`), and add a CI step that runs `move_parity.py names` and `move_parity.py test-edits` on every PR carrying a brief under `specs/028-split-big-packages/briefs/`, passing the PR base SHA, head SHA and that brief; coordinating with open spec 023 PRs that edit the same job; PR "ci: add move parity checks (spec 028)", labels `type: ci`, `area: shared`
- [X] T007 Create `specs/028-split-big-packages/briefs/README.md` pointing to the brief contract

**Checkpoint**: parity tooling merged; move PRs can start.

## Phase 3: User Story 4 — Web screens follow the pages layout (Priority: P2, first by OD-1)

- [ ] T008 [US4] PR: split `web/src/types.ts` (1,132) by domain into files of ≤ 500 lines under `web/src/types/`, with `web/src/types.ts` re-exporting them so every `@/types` import is unchanged; `web/src/api/types.ts` untouched (OD-5)
- [ ] T009 [US4] PR: create `web/src/pages/`; add one `routes/<Screen>.ts` per page holding its route definition moved out of `router/tree.tsx`, which keeps only the tree assembly (OD-7); move the screens under 500 lines that only need a move (AdminLogs, AuditLog, Cluster, Clusters, Login, Modules, NotFound, Share, ServerDetail shell) with their tests; update `router/tree.tsx` imports
- [ ] T010 [US4] PR: AdminSettings (2,478) → `pages/AdminSettings.tsx` + `pages/AdminSettings/<Section>.tsx`, each ≤ 500; its five test files move next to the file they test
- [ ] T011 [US4] PR: CreateServer (1,351) → `pages/CreateServer.tsx` + sections, each ≤ 500; three test files move
- [ ] T012 [US4] PR: ThemeSettings (886) and Users (825) → pages + sections
- [ ] T013 [US4] PR: Servers (680), Backups (642), Dashboard (596) → pages + sections
- [ ] T014 [US4] PR: `routes/tabs/` → `pages/ServerDetail/tabs/` (OD-8), same file names; Mods (1,290) and Files (670) split into `pages/ServerDetail/tabs/<Tab>/<Part>.tsx`, each ≤ 500
- [ ] T015 [US4] PR: `routes/tabs/settings/` → `pages/ServerDetail/tabs/settings/` (OD-8), same file names; Networking (1,045) and ShareLinks (658) split into parts, each ≤ 500
- [ ] T016 [US4] PR: `lib/endpoints.ts` (1,046) split in place, each file ≤ 500
- [ ] T017 [US4] PR: `components/modules/BuildModuleDialog.tsx` (973) and `components/modules/SourceDialog.tsx` (573) split in place (same folder)
- [ ] T018 [US4] PR: `components/CaptureWidget.tsx` (826) split in place
- [ ] T019 [US4] PR: `components/server/ServerActionsCard.tsx` (658) split in place
- [ ] T061 [US4] PR: `web/scripts/i18n-migrate.ts` (1,195) split in place into files of ≤ 500 lines under `web/scripts/` (OD-11); `npm run i18n:migrate` entry point unchanged
- [ ] T020 [US4] For each web PR: sonnet browser smoke test of the moved screens, visual-diff and screenshot jobs green, `web/specs.md` paths updated

**Checkpoint**: no screen code in `web/src/routes/`; no web production file over 500 lines.

## Phase 4: User Story 2 — API handlers split by resource (Priority: P2)

- [ ] T021 [US2] Scout the whole package once: call graph between the R3 groups, helpers used by several groups, cycles; update the R3 table in `research.md` with any reassignment and its reason
- [ ] T022 [US2] PR: shared helper package under `api/internal/handlers/` (name from T021) with the multi-group helpers, `semver.go`, `validate.go`
- [ ] T023 [US2] PR: `mods` group
- [ ] T024 [US2] PR: `shares` group, `shares.go` (617) split ≤ 500
- [ ] T025 [US2] PR: `capture` group, `capture.go` (1,569) split ≤ 500; move the `.golangci.yml` gosec path rule for `internal/handlers/capture\.go$` to the new file(s) with the same scope
- [ ] T026 [US2] PR: `settings` group, `config.go` (855) split
- [ ] T027 [US2] PR: `modules` group
- [ ] T028 [US2] PR: `clusters` group, `cluster.go` (517) and `fleet.go` (780) split
- [ ] T029 [US2] PR: `servers` group, `resources.go` (759) and `tunnelcreds.go` (523) split
- [ ] T030 [US2] PR: `access` group, `users.go` (957) split; `api/internal/handlers/` root then holds no resource code
- [ ] T031 [US2] PR: `api/cmd/main.go` (914) split into `package main` files ≤ 500 with the same mount order
- [ ] T032 [US2] PR: `api/internal/audit/audit.go` (1,050) split in place
- [ ] T033 [US2] PR: `api/internal/auth/oidc.go` (711) split in place
- [ ] T034 [US2] For each api PR: new `suite_envtest_test.go` per sub-package with envtest tests (OD-6, OD-9); envtest job time before/after in the PR (R5; stop and ask if > +2 min); `api/specs.md` and `docs/agent-architecture.md` paths updated

**Checkpoint**: SC-002 true for api; api coverage ≥ 80 on the full run.

## Phase 5: User Story 3 — Operator controllers split by resource (Priority: P2)

- [ ] T035 [US3] Scout the whole package once: call graph between the R4 groups, the backup/wipe/conflict guard coupling (#718), cycles; update the R4 table in `research.md`
- [ ] T036 [US3] PR: shared helper package under `operator/internal/controller/` (name from T035) with helpers, metrics, conflict, owned_delete, semver and the guard code both backup and gameserver call
- [ ] T037 [US3] PR: `cluster` group
- [ ] T038 [US3] PR: `networkcapture` group, controller (1,138) split
- [ ] T039 [US3] PR: `module` group, `module_controller.go` (630) and `module_locales.go` (502) split
- [ ] T040 [US3] PR: `backup` group (backup, schedule, restore, retention), `backup_controller.go` (1,105) and `restore_volumesnapshot.go` (794) split
- [ ] T041 [US3] PR: `gameserver` group, `gameserver_controller.go` (2,740), `gameserver_status.go` (1,446) and `gameserver_tunnel.go` (747) split
- [ ] T042 [US3] PR: `operator/cmd/main.go` (519) split with the same reconciler registration order and options
- [ ] T043 [US3] For each operator PR: `make generate && make manifests` give no diff in CI (CRD and ClusterRole identical); new `suite_envtest_test.go` per sub-package (OD-6, OD-9); envtest job time before/after (R5); `operator/specs.md` paths updated

**Checkpoint**: SC-002 true for operator; operator coverage ≥ 72 on the full run.

## Phase 6: Other modules over the ceiling (FR-018)

- [ ] T044 [P] PR: `capture-sidecar/internal/httpserver/handlers.go` (975) split in place
- [ ] T045 [P] PR: `agent/internal/mods/mods.go` (932) split in place; the `.golangci.yml` path rules for `internal/mods/mods\.go$` and `internal/(mods/mods|files/files)\.go$` follow the code with the same scope
- [ ] T046 [P] PR: `agent/internal/rcon/battleye.go` (703) split in place
- [ ] T047 [P] PR: `agent/internal/files/rooted_linux.go` (603) split in place
- [ ] T048 [P] PR: `agent/internal/players/players.go` (509) split in place
- [ ] T049 [P] PR: `i18n/cmd/i18n-check/main.go` (781) split into `package main` files

## Phase 7: User Story 1 — Small services match the service layout (Priority: P1 for layout, last by OD-1)

- [ ] T050 [US1] PR: `audit-syslog-bridge` → `cmd/main.go` + `internal/<concern>/`; Dockerfile and Makefile `image-audit-syslog` build path; `specs.md`
- [ ] T051 [US1] PR: `mcp-server` → `cmd/main.go` + `internal/{tools,fixadvice}/`; Dockerfile; `specs.md`, `README.md`
- [ ] T052 [US1] PR: `telemetry-receiver` → `cmd/main.go` + `internal/<concern>/`, `views.go` (733) split; `design-export/`, `web/`, `telemetry-dashboard.pen` stay; Dockerfile, Makefile e2e image target (line 190); `specs.md`
- [ ] T053 [US1] PR: `tunnel` → `cmd/main.go` + `internal/<concern>/`, `playit_reporter.go` (525) split; `main_test.go` moves with the code it tests, unsplit, with only the FR-005/OD-6 mechanical edits; the three Dockerfiles; only after #721, #722, #723 merge
- [ ] T054 [US1] PR: `sentinel` → `cmd/main.go` + `internal/<concern>/`; `main_test.go` moves with the code it tests, unsplit, with only the FR-005/OD-6 mechanical edits; Dockerfile; only after #729 merges

**Checkpoint**: every service in 027 OD-3 has a thin `cmd/main.go` and nothing at its root.

## Phase 8: User Story 5 — Layout and size rules stay true (Priority: P3)

- [ ] T055 [US5] Write `hack/check_layout.py` per [contracts/layout-check.md](./contracts/layout-check.md) and `hack/test_check_layout.py` (pass and fail tree per rule)
- [ ] T056 [US5] PR: add `check-layout` to the Makefile, to `lint`, and the unit tests to the CI `hack/` test target; master must be green with it (labels `type: ci`, `area: shared`)
- [ ] T057 [US5] ~~e2e test in `test/e2e/`~~ WITHDRAWN: OD-10 (valgul, 2026-10-21) rules no new e2e test for this refactor

## Phase 9: Polish

- [ ] T058 Sweep `docs/agent-architecture.md`, `docs/layout.md`, module `specs.md` files and open `specs/` folders for paths that no longer exist (exclude `specs/done_*`)
- [ ] T059 Final parity: test-name list on master vs the T003 baseline (plus tests added by other work), recorded in `research.md` (SC-005)
- [ ] T060 After the last PR merges and every task is done or withdrawn: `git mv specs/028-split-big-packages specs/done_028-split-big-packages` and update in-repo references in the same `docs:` commit (constitution IV, rule 16)

## Dependencies

- Phase 1 → Phase 2 → Phases 3, 4, 5 in OD-1 order (web, api, operator), each sequential inside.
- Phase 6 after Phase 5; its PRs touch different modules and may run in parallel with each other.
- Phase 7 after Phase 6; T053/T054 also wait on their audit fixes.
- Phase 8 after Phases 3–7. Phase 9 last.
