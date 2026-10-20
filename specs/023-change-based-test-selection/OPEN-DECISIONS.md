# Open Decisions: 023-change-based-test-selection

Unsettled items. Do not treat any of the below as decided until a maintainer rules on it here.

## Settled (user, 2026-10-09)
OD-1 (FR-009): module coverage minimums are enforced on full runs only (default-branch pushes, forced full runs); partial PR runs check coverage of the lines they changed.
OD-2 (FR-010): every selected end-to-end suite keeps running on both amd64 and arm64 on PRs.
OD-3 (US3, FR-014): each shared CI config file runs only the jobs it feeds plus workflow validation; only the selection script and its tests, Makefile, go.work/go.work.sum and composite actions still force the full suite.

## Settled (user, 2026-10-11)
OD-4 (Principle I): a Go e2e test is required for this feature even though it is CI-only. Plan research R13 defines it: `TestCISelection_*` in `test/e2e/`, registered in the `operator` bucket.
OD-5 (FR-009): changed lines on a partial run must meet the module's own coverage minimum (`total` from its `.testcoverage.yml`; web uses the Vitest `lines` threshold, 92).
OD-6 (plan R2): a selected e2e bucket runs only its affected tests; full runs still run every test of every bucket.
OD-7 (FR-007): a PR forces the full suite with the label `ci: full-run` (read on each run, no `labeled` trigger) or a manual `workflow_dispatch` run on the branch.
OD-8 (plan R4): Go package dependencies are found by scanning import lines in `ci_scope.py`; the `changes` job stays Go-free.
OD-9 (plan R6, R7): on partial runs only Vitest is narrowed (`vitest related`); ESLint, the `tsc` build and the Playwright mock suite run whole whenever web is selected.
OD-10 (plan Phasing): rollout is five PRs: summary table, e2e selection, unit selection, CI-config mapping, docs.
OD-11 (tasks T030, T041, T047; quickstart): keep the throwaway draft PRs from quickstart.md, including scenario 6's deliberate compile error. valgul confirmed this twice on 2026-10-11 as an explicit exception to his no-demo-PR rule, for spec 023 only.

No open decisions remain.
