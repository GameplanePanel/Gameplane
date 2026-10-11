# Implementation Plan: Change-Based Test Selection

**Branch**: `claude/project-thread-4uisie` | **Date**: 2026-10-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/023-change-based-test-selection/spec.md`

## Summary

PR CI already skips whole areas and selects Go modules, but any Go change still boots
every Kubernetes e2e suite on both architectures, every selected module runs its whole
test suite, and any CI config edit runs all 89 jobs. This plan extends the existing
`changes` job and `hack/ci_scope.py` (stdlib Python, no Go install) to decide, from the
PR's changed files:

1. **which e2e suites run**, from a per-test component map in
   `test/e2e/suite-components.json` beside `buckets.sh`;
2. **which Go packages and dashboard test files run**, from an import graph built by
   scanning sources (`go test <packages>`, `vitest related <files>`);
3. **which jobs a CI config edit feeds**, by mapping changed `ci.yaml` lines to job blocks.

Full runs (master pushes, `workflow_dispatch`, the `ci: full-run` label, unknown or
unmapped changes, selection-rule edits) behave exactly as today, including the module
coverage gates. Partial runs replace those gates with a changed-lines coverage check
(`hack/check_changed_coverage.py`). The `changes` job writes a per-suite ran/skipped
table to the run summary. Details: [research.md](./research.md).

## Technical Context

**Language/Version**: Python 3 (stdlib only, as `hack/ci_scope.py` today); GitHub Actions YAML; Bash for `buckets.sh`

**Primary Dependencies**: existing `dorny/paths-filter` v4.0.3, go-test-coverage v2.18.9, Vitest (`related` mode, already installed), no new third-party actions or packages

**Storage**: N/A (values pass between jobs as `GITHUB_OUTPUT` keys within one run)

**Testing**: `hack/test_ci_scope.py` unittest suite (extended), run in CI via `make test-ci-scope`; end-to-end validation by draft PRs per [quickstart.md](./quickstart.md)

**Target Platform**: GitHub-hosted runners (`ubuntu-latest`, `ubuntu-24.04-arm`)

**Project Type**: CI tooling inside the monorepo

**Performance Goals**: single-component PR result in ≤ 20 min (SC-001); median runner-minutes −50% (SC-002); comment-only CI config PR < 30 runner-minutes (SC-003); `detect changes` job stays ≤ 2 min

**Constraints**: fail open to the full suite on any doubt (FR-002); never select less than a test needs; no test deleted, weakened or excluded from full runs (FR-012); amd64 and arm64 share every decision (FR-010); `ci_scope.py` installs nothing

**Scale/Scope**: 15 Go modules + `test/e2e`, 13 e2e buckets (~205 bucketed tests), 6 game-bot runners, 153 Vitest files, 27 Playwright mock specs, 89 jobs on a full run

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design (below).*

| Principle | Status | Notes |
|---|---|---|
| **I. E2E-Tested Delivery** (NON-NEGOTIABLE) | **NEEDS RULING (OD-4)** | No CRD, API, agent or dashboard behaviour changes, so there is no `test/e2e/` Go test to write. Verification is the extended `test_ci_scope.py` plus the draft-PR scenarios in quickstart.md, which are real runs of the feature itself. Spec 008 left the same question unresolved for CI-only work. |
| **II. Design-First** | Pass (exempt) | No dashboard or website surface. `design.pen` untouched. |
| **III. Language & Ecosystem Best Practice** | Pass | No `//nolint`, `eslint-disable` or config loosening. Coverage thresholds in `.testcoverage.yml` and `vitest.config.ts` are not edited; partial runs swap in the changed-lines gate per settled OD-1. |
| **IV. Spec-Driven Development** | Pass | Spec settled (OD-1..OD-3), this plan, then `/speckit-tasks`. No module `specs.md` is affected (`hack/`, `.github/` and `test/e2e/` harness files are not module roots); `test/e2e/internal/specs.md` untouched. |
| **V. Delegate to Workflows & Subagents** | Pass for research | Research ran in two haiku subagents; the main loop wrote these artifacts. Implementation is planned as Workflow scripts at haiku with sonnet review (CLAUDE.md rule 13). |
| **VI. CI Bears the Heavy Lifting** | Pass | The feature is CI. Locally only `py_compile`; correctness is proven by CI runs. |
| Dev workflow 6: merges need all checks green | Pass with FR-013 | Skipped jobs report success; every master push still runs everything (FR-006). |

**Post-design re-check (after Phase 1)**: unchanged. The design adds two stdlib scripts'
worth of logic, one JSON file and workflow wiring; it adds no subsystem the spec does
not name (see research R12). Principle I still needs OD-4.

## Project Structure

### Documentation (this feature)

```text
specs/023-change-based-test-selection/
├── spec.md
├── plan.md                    # this file
├── research.md                # Phase 0
├── data-model.md              # Phase 1
├── quickstart.md              # Phase 1
├── contracts/
│   ├── suite-components.md    # e2e component map format and rules
│   ├── ci-scope-cli.md        # ci_scope.py modes, outputs, summary
│   └── changed-coverage.md    # changed-lines coverage gate
├── OPEN-DECISIONS.md
├── checklists/requirements.md
└── tasks.md                   # /speckit-tasks (not created here)
```

### Source Code (repository root)

```text
hack/
├── ci_scope.py                # extended: package graph, suite selection, ci.yaml line mapper,
│                              #   verify-suites mode, step summary
├── test_ci_scope.py           # extended: one case per acceptance scenario
└── check_changed_coverage.py  # new: changed-lines coverage for Go profiles and Vitest JSON
test/e2e/
├── buckets.sh                 # unchanged membership
└── suite-components.json      # new: per-test component declarations
.github/workflows/ci.yaml      # changes job outputs; go/web jobs take package/file lists;
                               #   e2e matrices take e2e-exclude; workflow_dispatch;
                               #   ci: full-run label; e2e-buckets runs verify-suites
Makefile                       # test-ci-scope unchanged; no new targets needed
docs/contributing.md           # how selection works, the label, how to add a suite entry
```

**Structure Decision**: everything stays in the existing CI entry points (`changes` job,
`ci_scope.py`, `buckets.sh` neighbourhood). No new workflow file, action or service.

## Phasing

1. **Foundation**: `ci_scope.py` full-scope triggers (dispatch, label), `selection`
   output and step summary listing today's decisions unchanged. Ships alone; gives
   SC-005 before any skipping.
2. **P1 e2e selection** (US1): `suite-components.json`, `verify-suites`, `e2e-exclude`,
   per-test bot matrix, special-job booleans.
3. **P2 unit selection** (US2): package graph, `go-packages`, `web-tests`,
   `check_changed_coverage.py`, coverage switch on `scope`.
4. **P3 CI config mapping** (US3): `ci.yaml` line mapper and the narrowed shared-file list.
5. **Docs**: `docs/contributing.md` section.

Each phase is its own PR so a mistake in one can be reverted alone.

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|---|---|---|
| Principle I has no e2e test for this feature (pending OD-4) | The feature's surface is the CI workflow itself; there is no cluster path to exercise | A Go e2e test that parses workflow YAML would boot a kind cluster to check text, and misfile a CI gate as a cluster test |
| New `check_changed_coverage.py` | OD-1 requires a changed-lines gate and go-test-coverage v2.18.9 has no such mode | Installing `diff-cover` adds a dependency and a format-conversion step to every Go and web job |
