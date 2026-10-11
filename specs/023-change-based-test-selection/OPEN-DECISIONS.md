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

No open decisions remain.
