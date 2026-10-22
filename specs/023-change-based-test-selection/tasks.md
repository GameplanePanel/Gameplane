# Tasks: Change-Based Test Selection

**Input**: Design documents from `specs/023-change-based-test-selection/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md), [OPEN-DECISIONS.md](./OPEN-DECISIONS.md) (all ten settled)

**Tests**: requested. FR-011 requires the selection rules to have their own tests (`hack/test_ci_scope.py`), and OD-4 requires a Go e2e test (`test/e2e/ci_selection_e2e_test.go`).

**Organization**: phases follow the five-PR rollout settled in OD-10. The run-summary story (US4, P2) ships before US1 because OD-10 puts the summary table first, so selection decisions are visible before anything is skipped. Each phase is one branch and one PR.

**Binding rules for every task** (repeat them in every Workflow script and agent brief, CLAUDE.md rules 8, 11–14, 18):

- Locally only compile checks: `python3 -m py_compile hack/*.py`, `go build ./...`, `go vet -tags e2e ./...` in `test/e2e`, `npx tsc --noEmit` in `web`. Never run `make test|lint|cover`, `go test`, `npm test`, `make test-ci-scope` or any workflow locally; CI is the only verifier.
- `hack/ci_scope.py` and `hack/check_changed_coverage.py` stay Python stdlib only and never install Go or Node.
- No test, threshold or bucket is deleted, weakened or excluded from full runs (FR-012). No `.testcoverage.yml` or `vitest.config.ts` threshold is edited. No `//nolint`, `eslint-disable` or `# noqa` added.
- Signed commits (`git commit -s`), conventional prefix, `Co-Authored-By` trailer; never amend after push. One branch per phase; PR labels `type: ci` plus `area: shared` (and `area: e2e` / `area: web` where touched) via REST.
- Implementation runs through Workflow scripts with `model:` set on every agent, starting at haiku, review one tier up on the diff.
- Any doubt in the selection logic resolves to full scope (FR-002); it never selects less than today.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4 from spec.md

## Path Conventions

Repository root of `GameplanePanel/Gameplane`. Main files: `hack/ci_scope.py`, `hack/test_ci_scope.py`, `hack/check_changed_coverage.py`, `test/e2e/suite-components.json`, `test/e2e/ci_selection_e2e_test.go`, `test/e2e/buckets.sh`, `.github/workflows/ci.yaml`, `docs/contributing.md`.

---

## Phase 1: Setup

**Purpose**: repository prerequisites shared by every phase.

- [X] T001 Create the repository label `ci: full-run` (colour neutral, description "Force the full CI suite on this PR") via `POST repos/GameplanePanel/Gameplane/labels`; it is needed by OD-7 and by quickstart scenario 11
- [X] T002 [P] Read `hack/ci_scope.py` and `hack/test_ci_scope.py` end to end and write a scout brief at `specs/023-change-based-test-selection/briefs/ci-scope.md` listing every function, its `file:line`, its current outputs and the `changes`-job steps in `.github/workflows/ci.yaml` (lines ~71-301) that consume them (CLAUDE.md rule 18: fix agents edit from this brief only)

---

## Phase 2: Foundational (blocks every story)

**Purpose**: the full-scope switch and the `scope`/`selection` outputs every later phase extends. Ships in PR 1 with Phase 3.

- [X] T003 Add a `workflow_dispatch:` trigger (no inputs) to `on:` in `.github/workflows/ci.yaml` (OD-7)
- [X] T004 Pass `CI_FULL_RUN: ${{ contains(github.event.pull_request.labels.*.name, 'ci: full-run') }}` and `GITHUB_EVENT_NAME` into the `ci_scope.py modules` step env of the `changes` job in `.github/workflows/ci.yaml`
- [X] T005 In `hack/ci_scope.py`, add `full_scope_reason(env, paths) -> str | None` returning `master push`, `manual run`, `label ci: full-run`, `unknown base`, `selection-rule change` or `None`, and route every existing fail-open branch through it (contracts/ci-scope-cli.md "Inputs")
- [X] T006 In `hack/ci_scope.py`, emit the `scope` key (`full` or `partial`) from mode `summary`
- [X] T007 In `hack/ci_scope.py`, wrap the whole of mode `modules` so any exception selects full scope and records the exception text as the reason (contracts/ci-scope-cli.md "Failure behaviour")
- [X] T008 [P] Add unittest cases to `hack/test_ci_scope.py` for each `full_scope_reason` value, for `scope` output on full and partial runs, and for the exception fallback

**Checkpoint**: `scope` is emitted, full runs are unchanged, nothing is skipped yet.

---

## Phase 3: User Story 4 – Anyone can see why a test ran or was skipped (P2, ships first per OD-10) — PR 1

**Goal**: every run shows a per-suite ran/skipped table with the reason (FR-008, SC-005), describing today's decisions before any new skipping exists.

**Independent test**: on any PR run, the `detect changes` job summary lists every suite and job with a decision and reason; a master push prints `full: master push`.

### Tests for User Story 4

- [X] T009 [P] [US4] Add cases to `hack/test_ci_scope.py` asserting the `selection` JSON lists every suite id with `decision` and `reason`, cites at most five changed paths with a `(+N)` overflow, and that the Markdown table matches contracts/ci-scope-cli.md "Step summary"

### Implementation for User Story 4

- [X] T010 [US4] In `hack/ci_scope.py`, build `SelectionResult` records (data-model.md) for every existing suite and job gate the `changes` job already decides (Go modules, lint legs, e2e buckets, game-bot tests, web, web-e2e-mock, helm, chart-template, docs, images) and emit them as the `selection` output from mode `summary`
- [X] T011 [US4] In `hack/ci_scope.py` mode `summary`, append the Markdown table to `$GITHUB_STEP_SUMMARY`, with the one-line `full: <reason>` header on full scope
- [X] T012 [US4] Expose `selection` as a `changes` job output in `.github/workflows/ci.yaml` and add a link line to it in the `report` job's rendered summary (no change to `report.needs`, so `hack/check-ci-report-coverage.sh` stays green)
- [X] T013 [US4] Open PR 1 (Phases 2–3) with labels `type: ci`, `area: shared`; verify in its own CI run that the summary table appears and that every job still runs as before

**Checkpoint**: US4 delivers visibility alone; merge PR 1 before Phase 4.

---

## Phase 4: User Story 1 – End-to-end suites follow the changed component (P1) 🎯 MVP — PR 2

**Goal**: a PR boots only the e2e buckets, and only the tests inside them, that exercise what it changed, on both architectures (FR-001…FR-004, FR-010, OD-2, OD-6).

**Independent test**: quickstart scenarios 1–4 and 12 give the expected summary rows; the `TestCISelection_*` e2e tests pass in the `operator` bucket.

### Tests for User Story 1

- [X] T014 [P] [US1] Add `hack/test_ci_scope.py` cases for US1 acceptance scenarios 1–4: agent-only change, sentinel-only change, chart change (all suites), and a changed `test/e2e/*_test.go` selecting its own tests
- [X] T015 [P] [US1] Add `hack/test_ci_scope.py` cases for `verify-suites`: unmatched test, unknown component id, code component named by no entry and not in `unexercised`
- [X] T016 [P] [US1] Add `hack/test_ci_scope.py` cases for `e2e-run`: the regex lists only selected tests of a bucket, equals `buckets.sh regex <bucket>` on full scope, and the `ratelimit` tail is dropped when none of its tests is selected
- [X] T017 [US1] Write `test/e2e/ci_selection_e2e_test.go` (`//go:build e2e`, package `e2e`) per research R13: one `TestCISelection_<Scenario>` per scenario in T014 plus unmapped-path and full-run-label cases; each uses `t.Parallel()`, its own `t.TempDir()`, `git archive HEAD` for the base commit, runs `python3 hack/ci_scope.py modules` with `GITHUB_OUTPUT` in the temp dir, and asserts `selection`, `e2e-exclude`, `e2e-run`; then asserts every emitted regex matches at least one compiled `Test` function and nothing outside that bucket's `buckets.sh list`. Follow the `e2e-test-authoring` skill; no logins — Withdrawn in part (2026-10-22): the test runs mode `e2e`, not `modules`, and asserts `reason` and `e2e-run` only; `selection` and `e2e-exclude` are not outputs of mode `e2e` (contracts/ci-scope-cli.md, Mode `e2e`; PR #778 deviations 1 and 2)
- [X] T018 [US1] Register every `TestCISelection_*` name in `bucket_operator` in `test/e2e/buckets.sh`

### Implementation for User Story 1

- [X] T019 [US1] Write `test/e2e/suite-components.json` per contracts/suite-components.md from the per-test map in research.md: `universal` prefixes (research R3), one entry per test-name pattern covering every test of every bucket including `bot-heavy`, and `unexercised` entries for audit-syslog-bridge, mcp-server and the tunnel runtime with reasons; add an entry for `^TestCISelection_` with `paths` per research R13
- [X] T020 [US1] In `hack/ci_scope.py`, add mode `verify-suites` implementing contracts/suite-components.md rules 1, 2 and 5, reading bucket lists by running `test/e2e/buckets.sh list <bucket>`
- [X] T021 [US1] Add a `python3 hack/ci_scope.py verify-suites` step after `buckets.sh verify` in the `e2e-buckets` job of `.github/workflows/ci.yaml`
- [X] T022 [US1] In `hack/ci_scope.py` mode `e2e`, map changed paths to component ids (data-model.md Component), reusing `affected_modules()` for the Go reverse-dependency closure, and collect `unmapped` paths; a non-empty `unmapped` forces full scope with reason `unmapped path <p>`
- [X] T023 [US1] In `hack/ci_scope.py` mode `e2e`, select tests from `suite-components.json` (union over matching entries; a changed `test/e2e/<file>_test.go` selects the tests defined in that file by scanning `func Test` names) and emit `e2e-run` (OD-6) and the `e2e-multicluster`, `e2e-upgrade`, `e2e-web-live`, `e2e-capture-overhead` booleans
- [X] T024 [US1] In mode `e2e` of `hack/ci_scope.py`, emit only selected `bot-fast` tests as `bot-matrix` and a `bots-any` output (contracts/ci-scope-cli.md "Mode bots")
- [X] T025 [US1] In `.github/workflows/ci.yaml`, skip unselected legs with `RUN_REGEX` in the `e2e-go` and `e2e-go-arm64` jobs, both reading mode `e2e`'s `e2e-run` through one shared `&e2e-go-steps` anchor so both architectures always get the same tests (FR-010)
- [X] T026 [US1] In `.github/workflows/ci.yaml`, replace `RUN_REGEX="$(./test/e2e/buckets.sh regex '${{ matrix.bucket }}')"` (line ~1377 and its arm64 twin) with the bucket's value from `fromJSON(needs.changes.outputs.e2e-run)`, and gate the `ratelimit` tail step (line ~1389) on its presence
- [X] T027 [US1] In `.github/workflows/ci.yaml`, fold the four e2e booleans from T023 into the gates of `e2e-multicluster*`, `e2e-upgrade*`, `e2e-web-live*` and `e2e-capture-overhead`, and gate `e2e-game-bot` on `bots-any`
- [X] T028 [US1] Extend the `selection` records (T010) with the per-bucket and per-test decisions and reasons from T023
- [X] T029 [US1] Compile checks only: `python3 -m py_compile hack/ci_scope.py`, `go vet -tags e2e ./...` in `test/e2e`; open PR 2 with labels `type: ci`, `area: shared`, `area: e2e`
- [X] T030 [US1] Run quickstart scenarios 1–4 and 12 as draft PRs based on PR 2's branch, record the summaries and wall times in `specs/023-change-based-test-selection/validation.md`, close the draft PRs and delete their branches

**Checkpoint**: MVP. A single-component PR boots only its suites (SC-001).

---

## Phase 5: User Story 2 – Unit and integration tests follow the changed packages (P2) — PR 3

**Goal**: Go runs only changed packages and their importers; the dashboard runs only related Vitest files; coverage switches to changed lines on partial runs (FR-005, FR-009, OD-1, OD-5, OD-8, OD-9).

**Independent test**: quickstart scenarios 5–8.

### Tests for User Story 2

- [X] T031 [P] [US2] Add `hack/test_ci_scope.py` cases for the package graph: leaf change, importer chain across modules, `_test.go`-only change, `testdata/` and embedded file change, module `go.mod` change giving `./...`
- [X] T032 [P] [US2] Add `hack/test_ci_scope.py` cases for `web-tests`: a route file gives its source path, a change under `web/src/test/`, `web/package.json`, `package-lock.json`, `vitest.config.ts`, `vite.config.ts`, `tsconfig*.json` or `eslint.config.js` gives `all`
- [X] T033 [P] [US2] Write `hack/test_check_changed_coverage.py` (unittest) with fixture profiles: Go atomic profile covering/missing added lines, Istanbul JSON for web, zero-statement diff passes, threshold read from `.testcoverage.yml` and `vitest.config.ts`, missing threshold fails; add a second `unittest discover` line for `test_check_changed_coverage.py` to the `test-ci-scope` target in `Makefile` (a `test_*.py` glob would also run `test_auto_label.py`, which has its own workflow)

### Implementation for User Story 2

- [X] T034 [US2] In `hack/ci_scope.py`, build the package import graph by scanning `import` blocks of every `.go` file (including `_test.go`) in the selected modules, map changed files (including `testdata/` and embedded files) to their package directory, compute reverse closure, extend `file_consumers` for cross-package file reads, and emit `go-packages` (research R4, OD-8)
- [X] T035 [US2] In `hack/ci_scope.py`, emit `web-tests` per research R6 (OD-9)
- [X] T036 [US2] Write `hack/check_changed_coverage.py` per contracts/changed-coverage.md (threshold from `.testcoverage.yml` `threshold.total` or `vitest.config.ts` `lines`, OD-5)
- [X] T037 [US2] In the `go` job of `.github/workflows/ci.yaml` (lines ~621-674), replace `./...` in the unit and envtest `go test` commands with the module's `go-packages` list, keeping `-coverpkg=./...`; on `scope == full` keep `make cover-go-merge && make cover-go-check`, on `partial` merge profiles and run `hack/check_changed_coverage.py --go-profile ... --module <dir>` instead
- [X] T038 [US2] In the `web` job of `.github/workflows/ci.yaml` (lines ~676-733), keep `npm run lint` and `npm run build`; on partial scope with a file list run `npx vitest related --run --coverage --coverage.thresholds.lines=0 --coverage.thresholds.functions=0 --coverage.thresholds.branches=0 --coverage.thresholds.statements=0 <files>` then `hack/check_changed_coverage.py --vitest-json web/coverage/coverage-final.json`; otherwise run `npm run test:cover` unchanged. `web-e2e-mock` stays whole (OD-9)
- [X] T039 [US2] Extend the `selection` records with per-module package counts and the web related-file count
- [X] T040 [US2] Compile checks only; open PR 3 with labels `type: ci`, `area: shared`, `area: web`
- [X] T041 [US2] Run quickstart scenarios 5–8 as draft PRs on PR 3's branch, record results in `validation.md`, close them and delete their branches

**Checkpoint**: unit selection and changed-lines coverage work; full runs keep module gates.

---

## Phase 6: User Story 3 – Shared CI configuration edits stop forcing everything (P3) — PR 4

**Goal**: a `ci.yaml` edit selects only the job it sits in; other shared files map to the jobs they feed (FR-011, FR-014, OD-3).

**Independent test**: quickstart scenarios 9 and 10.

### Tests for User Story 3

- [X] T042 [P] [US3] Add `hack/test_ci_scope.py` cases: comment-only `ci.yaml` change selects only `workflow-lint`; a step change inside job `web` selects `web` and `workflow-lint`; a change in `on:`, `env:`, `concurrency:`, `permissions:`, or inside the `changes` or `report` job forces full scope with reason `selection-rule change`; `.golangci.yml` selects only `lint` legs; `hack/ci_scope.py`, `Makefile`, `go.work`, `go.work.sum`, `.github/actions/**` still force full scope

### Implementation for User Story 3

- [X] T043 [US3] In `hack/ci_scope.py`, add the `ci.yaml` line mapper from research R8 (job blocks by two-space keys under `jobs:`, blank/comment lines ignored) using `git diff -U0` line numbers, and emit `ci-jobs`
- [X] T044 [US3] In `hack/ci_scope.py`, narrow the shared-file list per OD-3: remove `.github/` and `.golangci.yml` from the force-all set, map them per research R8, keep `hack/ci_scope*`, `hack/check_changed_coverage.py`, `Makefile`, `go.work*`, `.github/actions/**`, `test/e2e/buckets.sh`, `test/e2e/suite-components.json` as force-all
- [X] T045 [US3] In `.github/workflows/ci.yaml`, OR each job's existing gate with `contains(fromJSON(needs.changes.outputs.ci-jobs), '<job-id>')`, keeping `workflow-lint` selected for any `.github/` change
- [X] T046 [US3] Compile checks only; open PR 4 with labels `type: ci`, `area: shared`
- [X] T047 [US3] Run quickstart scenarios 9–11 as draft PRs on PR 4's branch, record runner-minutes for scenario 9 (SC-003) in `validation.md`, close them and delete their branches

**Checkpoint**: all four stories work independently.

---

## Phase 7: Polish & Docs — PR 5

- [X] T048 [P] Add a "How CI picks tests" section to `docs/contributing.md`: what a partial run skips, the `ci: full-run` label and manual run, how to add a `suite-components.json` entry, where to read the run summary
- [X] T049 [P] Add the new selection files to the task map in `docs/agent-architecture.md`
- [X] T050 Compile and link checks only; open PR 5 with labels `type: docs`, `area: shared`
- [X] T051 After the first `master` push following PR 5's merge, confirm every job ran (FR-006, SC-006) and record it in `validation.md`
- [ ] T052 After 30 merged PRs, measure median runner-minutes against the 30 before PR 2 via the Actions API and record SC-002 in `validation.md`; for 60 days review each red `master` run for a selection miss (SC-004)

---

## Dependencies & Execution Order

### Phase dependencies

- Phase 1 has no dependencies.
- Phase 2 depends on T002 and blocks everything after it.
- Phase 3 (US4) depends on Phase 2; PR 1 merges before Phase 4 starts.
- Phase 4 (US1) depends on PR 1. Phase 5 (US2) and Phase 6 (US3) depend on PR 1 only and can be built in parallel with Phase 4, but merge in OD-10 order (PR 2, 3, 4).
- Phase 7 depends on PRs 2–4 merged.

### Within each story

- Tests first (they fail in CI until the implementation lands in the same PR).
- `hack/ci_scope.py` logic before the `.github/workflows/ci.yaml` wiring that reads its outputs.
- T017–T018 (e2e test) need T019–T023 in the same PR.

### Parallel opportunities

- T001 ‖ T002.
- Within each story, every test task marked [P] runs in parallel (separate test classes; for `hack/test_ci_scope.py` put each task's cases in its own `TestCase` class to avoid edit conflicts).
- T048 ‖ T049.
- Phases 5 and 6 can be developed alongside Phase 4 on separate branches.

## Parallel Example: User Story 1

```text
Workflow batch A (haiku, one agent each): T014, T015, T016 — each adds its own TestCase class to hack/test_ci_scope.py
Workflow batch B (haiku): T019 suite-components.json, then T020–T024 in hack/ci_scope.py (one agent, sequential)
Workflow batch C (haiku): T025–T027 ci.yaml wiring, then T017–T018 e2e test
Review (sonnet): one reviewer on the combined git diff against the scout brief and contracts
```

## Implementation Strategy

### MVP first

1. Phases 1–3 (PR 1): visibility only, zero behaviour change.
2. Phase 4 (PR 2): e2e selection, the largest saving. Stop and validate with quickstart 1–4 and 12.

### Incremental delivery

PR 3 (unit selection), PR 4 (CI-config mapping) and PR 5 (docs) each add value alone and can each be reverted alone (OD-10).

## Notes

- `bot-heavy` is mapped in `suite-components.json` for completeness but still never runs in CI (unchanged).
- `telemetry-default-gate` missing from `report.needs` (research R11) is out of scope here.
- Each draft PR from a quickstart scenario is closed and its branch deleted after its result is recorded; none is merged.
