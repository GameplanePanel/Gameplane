# Scout brief: `hack/ci_scope.py` and the `changes` job (T002)

Written from `master` at 8a67f2e (2026-10-20). Fix agents edit from this brief only
(CLAUDE.md rule 18).

## `hack/ci_scope.py` today (231 lines, stdlib only)

| Symbol | Line | What it does / outputs |
|---|---|---|
| `ROOT` | 12 | repo root (`Path(__file__).parent.parent`) |
| `EDGE_IMAGES` | 13 | 9 component images + 3 tunnel images, for mode `images` |
| `workspace_modules(root)` | 25 | `{dir: import path}` for every `use` in `go.work` (16 entries incl. `test/e2e`) |
| `changed_paths(root, base, head, *, history=False)` | 41 | list of changed paths, or `None` (bad base / not ancestor / git error) = unknown |
| `affected_modules(root, paths)` | 63 | sorted selected module dirs. `paths is None` or any path in `shared` (`Makefile`, `go.work`, `go.work.sum`, `.golangci.yml`, `test/e2e/buckets.sh`), prefix `.github/`, `hack/ci_scope`, `hack/test_ci_scope`, any `go.mod`/`go.sum`, or a non-module Dockerfile → every module. Else modules containing a changed non-`.md` path, `file_consumers` seeds (`operator/config/crd` → api, `modules` → gp-module), then reverse-dependency closure by scanning string literals in `*.go` and `go.mod` |
| `image_inputs`, `select_images`, `publish_paths` | 114–184 | mode `images` only; untouched by this feature |
| `emit(values)` | 187 | appends `key=<compact JSON>` lines to `$GITHUB_OUTPUT` |
| `bot_matrix(names)` | 194 | `{"test": names}`; raises on empty/duplicate/non-`Test…` names |
| `main()` | 202 | modes `modules`, `images`, `dockerfiles`, `bots` |

`main()` mode `modules` (lines 222–231) today:

```python
    full = (os.environ.get("GITHUB_EVENT_NAME") == "push"
            and os.environ.get("GITHUB_REF") == "refs/heads/master")
    paths = None if full else changed_paths(ROOT, args.base, args.head)
    selected = affected_modules(ROOT, paths)
    modules = sorted(workspace_modules(ROOT))
    emit({
        "all": paths is None,
        "modules": selected,
        "lint": bool(selected),
        "go": bool(set(selected) - {"test/e2e"}),
        "e2e-unit": "test/e2e" in selected,
        "lint-exclude": [{"module": m} for m in modules if m not in selected],
        "go-exclude": [{"module": m} for m in modules if m not in selected and m != "test/e2e"],
    })
```

Two existing tests (`test_master_push_runs_full_scope_even_for_docs_only_changes`,
`test_non_master_push_events_keep_docs_only_scope_empty`) assert the **exact** dict
above. Any new key emitted by mode `modules` breaks them, so PR 1 adds no key to
`modules` (see decision below).

## `changes` job in `.github/workflows/ci.yaml` (lines 75–368)

Step order: checkout (fetch-depth 0) → `scope` (`ci_scope.py modules`, env `BASE_SHA`,
`HEAD_SHA`) → `bots` (`ci_scope.py bots`, matrix of the 6 `bot-fast` tests) → `filter`
(`dorny/paths-filter` v4.0.3, filters `go web charts standalone e2etree kindsmoke
gamecoverage capture_overhead submodules codegen ci github specs docs`) → `combine`
(bash folds filters into gates).

`combine` gate formulas (`CI = filter.ci || scope.all`):

| Gate output | Formula | Feeds job(s) |
|---|---|---|
| `go` | `scope.go \|\| CI` | `go` (matrix minus `go-exclude`), `capture-sidecar-setcap-proof`, `vuln-go` (`\|\| push`) |
| `web` | `filter.web \|\| CI` | `web`, `web-e2e-mock` |
| `charts` | `filter.charts \|\| CI` | `helm`, `chart-template` |
| `standalone` | `filter.standalone \|\| CI` | `standalone-compose` |
| `e2e` | `filter.go \|\| charts \|\| e2etree \|\| gamecoverage \|\| CI` (note: `GO` env here is `scope.go`) | `e2e-buckets`, `e2e-go`, `e2e-go-arm64`, `e2e-multicluster*`, `e2e-upgrade*`, `e2e-game-bot`, `e2e-postgres` |
| `weblive` = `images` | `e2e \|\| web \|\| CI` | `build-images*`, `vuln-images`, `e2e-web-live*` |
| `github` | `filter.github \|\| CI` | `workflow-lint` |
| `specs`, `docs` | filter `\|\| CI` | `docs` (also `scope.lint`) |
| `capture_overhead` | filter `\|\| CI` | `e2e-capture-overhead` (PR only; master push uses `e2e`) |
| `submodules`, `codegen` | filter `\|\| CI` | `submodule-freshness`, `codegen-drift` (both `\|\| push`) |
| `kindsmoke` | `filter.kindsmoke \|\| master push` | `e2e-kind-ingress-smoke` |

Other scope outputs used directly: `scope.lint` (job `lint`, matrix minus
`lint-exclude`), `scope.e2e-unit` (`go-e2e-unit`), `scope.modules` contains `api`
(`api-postgres`). `dependency-review` runs on `pull_request` only;
`telemetry-default-gate` always runs.

The `ci` filter list (forces everything): `.github/workflows/ci.yaml`,
`.github/actions/**`, `hack/ci_scope.py`, `hack/test_ci_scope.py`, `Makefile`,
`go.work`, `go.work.sum`, `test/e2e/buckets.sh`, `.golangci.yml`. Every one of these is
also in `affected_modules`' force-all set, so a `ci` hit always means every Go module.

e2e-go matrix buckets (both arches): `operator, api-auth, api-roles, api-rbac,
api-agent, api-mods, telemetry, optional-components`; `api-auth` carries the
`ratelimit` tail step. Bucket regex today: `./test/e2e/buckets.sh regex <bucket>`
(lines ~1730–1736 and the arm64 twin).

`report` job (line 2357): `needs` lists 33 jobs, guarded by
`hack/check-ci-report-coverage.sh` (`needs` ⊆ `NEEDS_ORDER` ∩ `JOB_MATCHERS`). Do not
add to `needs`. The render script reads env, writes `/tmp/report.md`.

## PR 1 design (Phases 2–3)

Settled by valgul 2026-10-20 (option A on the spec 023 card): the selection table is
built by a **new mode `summary`**, run as the last `changes` step after `combine`, so it
reads the real gate values instead of re-implementing paths-filter in Python, and mode
`modules` keeps its exact outputs.

- `full_scope_reason(env, paths)`: `master push` (push + `refs/heads/master`),
  `manual run` (`workflow_dispatch`), `label ci: full-run` (`CI_FULL_RUN == "true"`),
  `unknown base` (`paths is None`), `selection-rule change` (any path in the `ci` filter
  list above), else `None`.
- Mode `modules`: event-level reasons (master, manual, label) set `paths = None`, so
  `all` is true and `combine`'s `CI` forces every gate. Any exception → `paths = None`,
  every module selected, `::warning::` with the exception text.
- Mode `summary`: reads `SCOPE_OUTPUTS`, `FILTER_OUTPUTS` (paths-filter with
  `list-files: json`, giving `<filter>_files`), `GATE_OUTPUTS`, `BOT_MATRIX` as JSON env;
  emits `scope`, `reason`, `selection`; appends the Markdown table to
  `$GITHUB_STEP_SUMMARY`. Wrapped so any exception still emits `scope=full` and a
  one-line summary; the step is `continue-on-error: true` so it can never block CI.
