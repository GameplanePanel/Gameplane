# Quickstart: validating change-based test selection

All verification runs on GitHub Actions (CLAUDE.md rule 8). Locally only
`python3 -m py_compile hack/ci_scope.py hack/check_changed_coverage.py` is allowed.
Each scenario is a throwaway draft PR against `master`, closed after the check; the
results go in `validation.md` in this folder.

## Prerequisites

- The implementation branch is merged or the scenario PRs are based on it.
- The label `ci: full-run` exists in the repository.

## Scenarios

| # | Draft PR changes | Expect in the `detect changes` summary | Expect in the run |
|---|---|---|---|
| 1 | One line in `agent/internal/console/` | scope partial; agent suites `run` with only their agent tests in the `-run` regex, `telemetry`, `upgrade`, `api-rbac`, `api-auth`, `api-roles`, `api-mods`, `capture-overhead` `skip` | Full result in ≤ 20 min (SC-001) |
| 2 | One line in `sentinel/` | operator RBAC and auth suites `skip` (US1-2) | |
| 3 | One line in `charts/gameplane/values.yaml` | `full`-equivalent: every e2e suite `run` (US1-3) | |
| 4 | One line in an existing `test/e2e/*_e2e_test.go` | the bucket holding that file's tests `run` (US1-4) | |
| 5 | One leaf package in `api/internal/` | `go api` runs only that package and its importers (US2-1, US2-2) | changed-lines coverage line in summary |
| 6 | Same as 5 plus a deliberate compile error in an importing package | | `go api` fails (US2 independent test) |
| 7 | A file under an `api` package's `testdata/` | that package's tests `run` (US2-3) | |
| 8 | One `web/src/routes/<screen>.tsx` line | `web` runs the related test files only (US2-4) | |
| 9 | A comment in `.github/workflows/ci.yaml` | only `workflow-lint` selected (US3-1) | under 30 runner-minutes (SC-003) |
| 10 | One line in `hack/ci_scope.py` | `full: selection-rule change` (US3-2, FR-011) | `make test-ci-scope` runs |
| 11 | Scenario 1 plus label `ci: full-run`, then push again | `full: label ci: full-run` (FR-007) | every suite runs |
| 12 | A new top-level file `tools/x.sh` | `full: unmapped path tools/x.sh` and a failing `selection-rules` check | |

Every scenario's summary must list every suite with a decision and reason (SC-005).

The `TestCISelection_*` e2e tests (research R13) run in the `operator` bucket on every
scenario PR that selects it, and must be green on each.

## After rollout

- On the first `master` push after merge, every job runs (FR-006, SC-006).
- After 30 merged PRs, compare median runner-minutes with the 30 before rollout (SC-002)
  using the Actions API, and record the numbers in `validation.md`.
- For 60 days, for every red `master` run, check whether the PR's selection skipped the
  failing suite (SC-004); a miss gets a rule fix in `suite-components.json` or
  `ci_scope.py`.

Contracts: [ci-scope-cli.md](./contracts/ci-scope-cli.md),
[suite-components.md](./contracts/suite-components.md),
[changed-coverage.md](./contracts/changed-coverage.md).
