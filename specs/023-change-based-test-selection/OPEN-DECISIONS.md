# Open Decisions: 023-change-based-test-selection

Unsettled items. Do not treat any of the below as decided until a maintainer rules on it here.

## Settled (user, 2026-10-09)
OD-1 (FR-009): module coverage minimums are enforced on full runs only (default-branch pushes, forced full runs); partial PR runs check coverage of the lines they changed.
OD-2 (FR-010): every selected end-to-end suite keeps running on both amd64 and arm64 on PRs.
OD-3 (US3, FR-014): each shared CI config file runs only the jobs it feeds plus workflow validation; only the selection script and its tests, Makefile, go.work/go.work.sum and composite actions still force the full suite.

No open decisions remain.
