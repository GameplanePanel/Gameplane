# Open Decisions: 023-change-based-test-selection

Unsettled items. Do not treat any of the below as decided until a maintainer rules on it here.

## OD-1 (spec Q1, FR-009): Coverage minimums on partial PR runs — **Open**
A run that tests only some packages cannot compute a whole-module coverage figure. Options: (a) enforce module minimums on full runs only (master pushes, forced full runs); PRs check coverage of changed lines; (b) a module with a coverage gate always runs its whole suite on PRs, so only the end-to-end tier is narrowed; (c) no coverage on PRs. Recommended: (a).

## OD-2 (spec Q2, FR-010): arm64 end-to-end on PRs — **Open**
Options: (a) PRs run selected end-to-end suites on amd64 only; arm64 runs on master and when a PR touches arch-sensitive files; (b) keep both architectures for every selected suite. Recommended: (b), keeps today's coverage; selection alone meets SC-001.

## OD-3 (spec Q3, US3): Shared CI configuration edits — **Open**
Options: (a) map each shared CI file to the jobs it feeds; only the selection script, Makefile, go.work and composite actions still force everything; (b) keep "any CI config edit runs everything". Recommended: (a).
