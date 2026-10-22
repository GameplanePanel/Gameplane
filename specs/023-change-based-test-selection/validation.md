# Validation results

Results of the [quickstart](./quickstart.md) scenarios. Each scenario was a throwaway
draft PR based on PR 2's branch (`claude/project-thread-63yei6-pr2`, head `82b77ab`),
allowed by OD-11. The drafts were closed and their branches deleted after the check.

## T030: scenarios 1–4 and 12 (2026-10-22)

All five runs were queued for about 65 minutes behind other PRs' runs before any job
started, and every later job waited for a runner too. Wall time therefore says nothing
about SC-001 yet; it is measured again after rollout on an idle queue. Runner-minutes
are the sum of the durations of jobs that ran (success or failure).

| # | Draft PR | Change | Scope | Runner-min | Result |
|---|---|---|---|---|---|
| 1 | #780 | comment in `agent/internal/console/console.go` | partial | 170 | selection broader than the quickstart row, see below |
| 2 | #781 | comment in `sentinel/main.go` | partial | 61 | RBAC and auth suites skipped (US1-2 met for those); 11 operator tests ran |
| 3 | #782 | comment in `charts/gameplane/values.yaml` | partial, every suite selected | 83 (arm64 only) | every e2e suite selected, 78 of 78 operator tests (US1-3 met) |
| 4 | #783 | comment in `test/e2e/api_agent_e2e_test.go` | partial | 104 | `api-agent` ran 7 of 7 (US1-4 met) |
| 12 | #784 | new file `tools/x.sh` | full: `unmapped path tools/x.sh` | 146 (still running at close) | everything selected; `e2e bucket coverage` failed with `no selection rule maps these paths: ["tools/x.sh"]` (FR-012 met) |

Every summary listed each suite with a decision and a reason (SC-005 met).

### Selections in detail

**Scenario 1 (agent).** Selected: `operator` 55 of 78, `api-agent` 7 of 7, `api-mods`
6 of 7, `api-rbac` 2 of 8, `api-roles` 1 of 7, `optional-components` 1 of 1,
`multicluster` 2 of 6, `upgrade` 1 of 1, web live, all six game-bot joins. Skipped:
`api-auth`, `telemetry`, `ratelimit`, `postgres`. `capture-overhead` was selected in
the summary but its job stayed skipped, because its PR gate still uses the
`capture_overhead` path filter.

The quickstart row expects `upgrade`, `api-rbac`, `api-roles`, `api-mods` and
`capture-overhead` to skip, and spec US1-1 names multicluster and upgrade as examples
of skipped suites. They ran because `suite-components.json` maps `TestMultiCluster_`,
`TestUpgrade_`, `TestAPI_Lifecycle*`, `TestAPI_OwnerCollaboratorAccess` and the mod
tests to the `agent` component: each of them starts a game server, and the agent runs
in that pod. The rows and the mapping disagree; see "Open item" below.

**Scenario 2 (sentinel).** Selected: `operator` 11 of 78 (the ten `TestHelmInstall_`
tests, mapped to every component, plus `TestGameServer_IdleNeverSleepsWithoutAPlayerCount`)
and the three `WakeOnConnect` game-bot joins. Skipped: `api-auth`, `api-rbac`,
`api-roles`, `api-agent`, `api-mods`, `telemetry`, `ratelimit`, `multicluster`,
`upgrade`, web live, the other three bots. Spec US1-2 says the operator suite does not
run; 11 of its tests ran, because `TestHelmInstall_` is mapped to `*`.

**Scenario 3 (chart).** Every suite was selected with "changed:
charts/gameplane/values.yaml". The scope reads `partial` because the chart is a
`universal` path rather than a full-run trigger, which gives the same suites.

**Scenario 4 (e2e test file).** Selected: `api-agent` 7 of 7 plus the tests that share
the file's helpers: `operator` 16 of 78, `api-rbac` 2 of 8, `api-mods` 5 of 7,
`optional-components`, three game-bot joins.

**Scenario 12 (unmapped path).** `all: true`, reason `unmapped path tools/x.sh`,
every gate on, and the `e2e bucket coverage` job failed on the unmapped path as
designed.

### Failures unrelated to selection

- `lint` failed on scenarios 1, 2 and 4 with `gofmt`: the throwaway comment line was
  appended without a blank line. It does not affect the selection result.
- Scenario 2: `e2e operator / amd64` failed in `TestHelmInstall_MetricsNotOnPublicPort`
  (`helm_install_e2e_test.go:239`, metrics port not serving within 1m30s). The change
  was a comment in `sentinel/main.go`, and the same test passed on arm64 in the same run.
- Scenarios 3 and 12: the amd64 `build e2e images` job was cancelled at its 15-minute
  timeout (layer-cache miss), so only the arm64 e2e jobs ran.
- `TestCISelection_*` ran in scenarios 3 and 12 (operator, arm64) and passed in
  scenario 3; scenario 12's operator job was still running at close.

### OD-12

Scenarios 1 and 2 selected more than the quickstart rows and spec US1-1/US1-2 said, and
the cause is the agreed `suite-components.json` mapping, not a code defect. Settled as
OD-12 in [OPEN-DECISIONS.md](./OPEN-DECISIONS.md): the mapping stays, and US1-1, US1-2
and quickstart rows 1-2 were reworded to match it. With the new wording, scenarios 1 and
2 pass.

## T041 and T047: scenarios 5–11 (2026-10-22)

Each scenario was a throwaway draft PR based on PR 3's branch at `4b90043` (PRs 3–5
combined), allowed by OD-11. Results come from each run's `detect changes` outputs and
the jobs that ran. The drafts were closed once their selection was read; their queued
e2e jobs were cancelled because the selection, not the e2e result, is what each scenario
checks.

| # | Draft PR | Change | Scope | Result |
|---|---|---|---|---|
| 5 | #787 | comment in `api/internal/steam/options.go` | partial | `go api` tested `./cmd ./internal/messages ./internal/steam` (the package and its importers); other modules empty; changed-lines gate line `changed-lines coverage: no added statement lines, passes` (US2-1, US2-2 met) |
| 6 | #788 | scenario 5 plus `var scenarioSixBreaksTheBuild int = "not an int"` in `api/cmd/main.go` | partial | same packages selected; `go (api)` failed in `go vet` with `cmd/main.go:916:37: cannot use "not an int"` (US2 independent test met) |
| 7 | #789 | new file `api/internal/steam/testdata/scenario7.txt` | partial | `go api` tested `./cmd ./internal/messages ./internal/steam`, so the owning package ran (US2-3 met) |
| 8 | #790 | comment in `web/src/routes/AdminLogs.tsx` | partial | `web-tests` = `["web/src/routes/AdminLogs.tsx"]`; Vitest `Affected 2 of 175 test files`, 36 tests passed; changed-lines gate passed; no Go job (US2-4 met) |
| 9 | #791 | comment line above `jobs:` in `ci.yaml` | partial | `ci-jobs` = `["workflow-lint"]`; jobs that ran: detect changes, workflow lint, ingress-nginx smoke, dependency review, telemetry default gate, ci report; about 3 runner-minutes (SC-003 met) |
| 10 | #792 | comment in `hack/ci_scope.py` | full: `selection-rule change` | every module `./...`, `web-tests` `"all"`, every e2e test selected, module coverage gates ran (`go api` 84.2% against 80%) (US3-2, FR-011 met) |
| 11 | #793 | scenario 1 (comment in `agent/internal/console/console.go`), then label `ci: full-run` and a second push | first push partial; second push full: `label ci: full-run` | second run: `all: true`, reason `label ci: full-run` in the e2e and units outputs, 69 jobs scheduled, every e2e suite selected (FR-007 met) |

**Scenario 9.** The ingress-nginx smoke ran because the base still listed `ci.yaml` in
the `kindsmoke` path filter. Commit `d4ee6ac` removed that entry, so a comment-only
`ci.yaml` edit now runs only `workflow lint` plus the always-on jobs. The six
`TestCISelection_*` tests appeared in `e2e-run`, but the e2e gate was off, so no e2e job
ran.

**Scenarios 5 and 7, e2e.** An `api` change selects most e2e tests (`operator` 64 of 78,
every API bucket whole), because nearly every e2e test goes through the API. This
follows the agreed `suite-components.json` mapping (OD-12).

**Scenario 7.** A `testdata/` file is not a `_test.go` file, so its package's importers
are selected too. That is broader than US2-3 asks for, never narrower.

## T051: first master push after the rollout (2026-10-22)

The merge of #779 (`3093f03`) was followed within minutes by the merge of #748
(`54d5819`), which cancelled the earlier push run. Run
[39861872464](https://github.com/GameplanePanel/Gameplane/actions/runs/39861872464)
on `54d5819` was the first master push run to finish with every selection change in
place:

- `detect changes` emitted `"all": "true"` and `"reason": "master push"`.
- 101 jobs were scheduled: 100 succeeded and `dependency review` was skipped, because it
  runs only on pull requests. No job was skipped by selection, so FR-006 and SC-006 are
  met.
- The jobs took about 386 runner-minutes in total.
