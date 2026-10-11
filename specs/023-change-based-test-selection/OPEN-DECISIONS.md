# Open Decisions: 023-change-based-test-selection

Unsettled items. Do not treat any of the below as decided until a maintainer rules on it here.

## Settled (user, 2026-10-09)
OD-1 (FR-009): module coverage minimums are enforced on full runs only (default-branch pushes, forced full runs); partial PR runs check coverage of the lines they changed.
OD-2 (FR-010): every selected end-to-end suite keeps running on both amd64 and arm64 on PRs.
OD-3 (US3, FR-014): each shared CI config file runs only the jobs it feeds plus workflow validation; only the selection script and its tests, Makefile, go.work/go.work.sum and composite actions still force the full suite.


## OD-4 (plan, Constitution Principle I): e2e test for a CI-only feature — **Open**
This feature changes CI only, so there is no cluster path for a `test/e2e/` test. Options: (a) rule Principle I not applicable to CI-only work; verification is `hack/test_ci_scope.py` plus the draft-PR scenarios in quickstart.md; (b) require a Go e2e test anyway. Recommended: (a). Spec 008 left the same question unresolved.

## OD-5 (plan R5, FR-009): changed-lines coverage threshold on partial runs — **Open**
Options: (a) the module's own minimum (operator 72, api 80, agent 90, web 92 lines, …); (b) one flat 80% everywhere; (c) report the number but never fail. Recommended: (a).

## OD-6 (plan R2): granularity inside a selected e2e bucket — **Open**
Options: (a) a selected bucket runs all its tests, as today; only whole buckets are skipped; (b) also run only the selected tests inside a bucket. Recommended: (a); most of the time is cluster boot, and the buckets are tuned for login budget.
