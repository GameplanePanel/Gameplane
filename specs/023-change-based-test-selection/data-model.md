# Data Model: Change-Based Test Selection

Phase 1 output for [plan.md](./plan.md). These are the values `hack/ci_scope.py` computes
and passes between CI jobs; nothing is stored outside a single workflow run.

## ChangedPathSet

The files a PR changes against its merge base.

| Field | Type | Rule |
|---|---|---|
| `base`, `head` | SHA (40–64 hex) | Base must be an ancestor of head, else the set is `None` |
| `paths` | list of repo-relative paths | `git diff --name-only --no-renames`; a rename lists both sides |
| `lines` | map path → added line numbers | `git diff -U0`; used only by the changed-lines coverage check and the `ci.yaml` job mapper |

`None` means "unknown" and selects everything (FR-002). A master push, a
`workflow_dispatch` run and a PR with the `ci: full-run` label are treated the same way.

## Component

A unit a changed path can belong to.

| Field | Type | Rule |
|---|---|---|
| `id` | string | A `go.work` module path (`operator`, `api`, …), or one of `web`, `charts`, `deploy-kind`, `modules`, `e2e-harness` |
| `paths` | prefixes | Module directory, `web/`, `charts/`, `deploy/kind/`, `modules`, `test/e2e/` |
| `dependents` | component ids | Go modules: the reverse-import closure from `affected_modules()`; others: none |

Paths in the universal set (research R3) map to the pseudo-component `*`, which selects
every suite.

## SuiteDeclaration

One entry of `test/e2e/suite-components.json` (see [contracts/suite-components.md](./contracts/suite-components.md)).

| Field | Type | Rule |
|---|---|---|
| `tests` | regex over Go test names | Anchored at `^`; every test listed by `buckets.sh list <bucket>` for every bucket must match at least one entry |
| `components` | component ids | Non-empty; each id must exist |
| `paths` | path prefixes, optional | Extra inputs the test reads (fixtures, game probe sources) |

Validation (FR-003): an unmatched test, an unknown component id, or a component with
code (a `go.work` module, `web`, `charts`) that no entry names fails the `ci_scope.py
verify-suites` step. Components that no suite exercises today (audit-syslog-bridge,
mcp-server, tunnel runtime) are listed in an explicit `"unexercised"` array with a
reason, so the gap is visible instead of silently passing.

## Suite

A CI unit that can be skipped as a whole.

| Kind | Members | Selected when |
|---|---|---|
| e2e bucket | `operator`, `api-auth` (+`ratelimit` tail), `api-roles`, `api-rbac`, `api-agent`, `api-mods`, `telemetry`, `multicluster`, `upgrade`, `capture-overhead` | any of its tests matches a declaration whose components or paths intersect the changed set |
| game-bot test | each `bot-fast` test | the test itself is selected |
| web-live | `e2e-web-live*` | `web` changed, or any component its Playwright live run uses (api, operator, agent) |
| ingress smoke | `e2e-kind-ingress-smoke` | unchanged: `kindsmoke` filter |
| Go package set | per module: list of import paths | research R4 |
| web unit | `vitest related` file list, or `all` | research R6 |
| job (other) | `lint`, `helm`, `chart-template`, `workflow-lint`, `docs`, … | existing filters, plus the `ci.yaml` job mapper (R8) |

The amd64 and arm64 copies of an e2e suite always share one decision (FR-010).

## SelectionResult

Per suite, written once by the `changes` job.

| Field | Type | Rule |
|---|---|---|
| `suite` | string | Suite id as shown in the run summary |
| `decision` | `run` \| `skip` | |
| `reason` | string | `full: <why>` (master, label, dispatch, unknown base, unmapped path, selection-rule change), `changed: <paths>` (up to five), or `not affected` |
| `scope` | `full` \| `partial` | Run-level; partial runs use the changed-lines coverage check (FR-009) |

State is computed once per run and never updated; a new push recomputes it.
