# Research: Change-Based Test Selection

Phase 0 output for [plan.md](./plan.md). Facts were gathered read-only on 2026-10-11 from
`master` at `8388366` (no test, lint or build was run). Each item gives a decision, its
rationale and the alternatives that were rejected. Items marked **OPEN** depend on a
maintainer ruling recorded in [OPEN-DECISIONS.md](./OPEN-DECISIONS.md).

## What exists today

- `changes` job (`.github/workflows/ci.yaml:71-301`) computes booleans with
  `dorny/paths-filter` plus `hack/ci_scope.py modules`. `ci_scope.py` already selects Go
  **modules** and their reverse-dependency closure by scanning literal import strings,
  without installing Go. It fails open: a missing or non-ancestor base, a master push,
  or a shared file (`Makefile`, `go.work*`, `.golangci.yml`, `test/e2e/buckets.sh`,
  anything under `.github/`, `hack/ci_scope*`, any `go.mod`/`go.sum`) selects every module.
- Every Kubernetes e2e job (`e2e-go`, `e2e-go-arm64`, `e2e-multicluster*`, `e2e-upgrade*`,
  `e2e-game-bot`, `e2e-web-live*`) is gated on a single `e2e` boolean, which is true for
  any Go, chart, `test/e2e`, `deploy/kind` or game-module change (`ci.yaml:262`).
- The `go` job runs the whole module: `go test -covermode=atomic -coverpkg=./...
  -coverprofile=coverage/unit.out ./...` (`ci.yaml:621-626`), plus `-tags=envtest ./...`
  for operator and api (`ci.yaml:647-657`), then `make cover-go-merge && make
  cover-go-check` (`ci.yaml:673-674`). Thresholds live in each module's
  `.testcoverage.yml`, `total` only, checked by go-test-coverage v2.18.9 (`Makefile:55,123-148`).
- The `web` job runs `npm run lint`, `npm run build` and `npm run test:cover`, which is
  `vitest run --coverage` over 153 test files with thresholds 92/76/82/92
  (`web/vitest.config.ts`). `web-e2e-mock` runs every Playwright mock spec (27 specs,
  1 worker). Neither uses Vitest `related`/`--changed` or Playwright `--only-changed`.
- `report` (`ci.yaml:1922+`) aggregates 27 jobs, counts `skipped` separately from failures
  and never fails the run. `hack/check-ci-report-coverage.sh` checks that every `needs:`
  entry is also in `NEEDS_ORDER` and `JOB_MATCHERS`.
- `hack/test_ci_scope.py` (about 12 unittest cases) runs from `workflow-lint` via
  `make test-ci-scope`, only when the `github` filter matches.
- Triggers: `push` to `master` and `pull_request` with a paths filter. There is no
  `workflow_dispatch`, `merge_group` or `labeled` trigger.

## What the e2e suites exercise

Every cluster job runs `deploy/kind/e2e.sh up`, which installs `charts/gameplane` and
loads the operator, api, agent, sentinel, capture-sidecar and telemetry-receiver images
(`deploy/kind/e2e.sh:190,322-347`). Loading an image is not the same as exercising it.

| Suite (CI job) | Exercises beyond the chart/operator/api baseline |
|---|---|
| `operator` | agent sidecar presence, capture-sidecar (ephemeral capture), netguard (module-source SSRF test); also hosts `TestAPI_EventStreamAndRoleEdits` for login budget |
| `api-auth` (+ `ratelimit` tail) | api auth, audit endpoint, OIDC via `internal/fakeoidc` |
| `api-roles` | api role mappings via Helm values |
| `api-rbac` | api RBAC matrix only |
| `api-agent` | agent files, players, console PTY and log tail |
| `api-mods` | ModuleSource/Module reconcile and api archive confinement |
| `multicluster` | two clusters, gateway chart upgrade, agent, capture-sidecar |
| `upgrade` | previous released chart, CRD pre-upgrade hook, api DB migration (does not use `e2e.sh up`) |
| `telemetry` | telemetry-receiver, telemetryschema, api consent |
| `bot-fast` (one runner per test) | operator, agent, real game images; wake tests exercise sentinel |
| `capture-overhead` (non-blocking) | capture-sidecar, gameproto |
| `e2e-web-live` | dashboard via Vite against a port-forwarded api, agent console |
| `e2e-kind-ingress-smoke` | `deploy/kind` cluster config and ingress pin only |

No suite exercises audit-syslog-bridge, mcp-server or the tunnel runtime (CRD validation
only); gameaction and svcutil are reached only through the binaries that import them.
Bucket membership is cut by **login budget, not subject**, so the component map must be
declared per test, not per bucket.

## Decisions

### R1. Declare e2e coverage per test-name pattern, in a JSON file beside `buckets.sh`

- **Decision**: add `test/e2e/suite-components.json`. Each entry maps a test-name pattern
  (for example `^TestCRD_Validation_`) to the components and extra paths it exercises.
  `ci_scope.py` reads it, and a bucket is selected when at least one of its tests is
  selected. `bot-fast` already runs one runner per test, so game-bot selection is per test.
- **Rationale**: FR-003 asks for declarations next to the bucket definitions. JSON keeps
  `ci_scope.py` stdlib-only (it deliberately installs nothing). Patterns keep the file
  small: the test names already share prefixes by subject (`TestHelmInstall_`,
  `TestCRD_Validation_`, `TestAPI_Agent`, `TestTelemetry`).
- **Alternatives**: per-bucket declarations (wrong because of login-budget cutting);
  annotations in each `_test.go` file (scattered, needs a Go parser); YAML (needs PyYAML
  or a hand parser in the `changes` job).

### R2. Run every test of a selected bucket (bucket-level skip only) — **OPEN (OD-6)**

- **Proposed**: when a bucket is selected, run its whole bucket regex as today.
- **Rationale**: almost all the saving comes from not booting a cluster at all (each
  e2e job boots kind and installs the chart before any test runs). Running only part
  of a bucket would change login-budget timing that the buckets were tuned for, and
  would make one bucket's runtime depend on the diff.
- **Alternative**: also filter tests inside a selected bucket with a narrower `-run`
  regex. Saves more on wide buckets (`operator`, `telemetry`) at the cost of more moving
  parts.

### R3. Component dependency closure reuses the module closure, plus a universal set

- **Decision**: a component is a Go module directory, `web`, `charts`, `deploy/kind`,
  `modules` (submodule) or `test/e2e`. A changed path maps to its component; the
  reverse-dependency closure from `affected_modules()` lifts library changes to the
  binaries that import them (a `svcutil` change selects every service). These paths
  select **every** e2e suite: `charts/**`, `operator/api/v1alpha1/**`,
  `operator/config/**`, `deploy/kind/**` (except the ingress-smoke-only pin),
  `containers/**` and any image Dockerfile, `test/e2e/env.go`, `test/e2e/internal/fakeoidc/**`,
  `test/e2e/go.mod|go.sum`, and `suite-components.json` / `buckets.sh` themselves.
- **Rationale**: matches spec scenario US1-3 and the "images are built from the whole
  workspace" fact (`containers/docker-bake.hcl`). Reusing the module closure means one
  dependency model for unit and e2e selection.
- **Alternatives**: a hand-maintained component dependency table (drifts from `go.work`).

### R4. Package-level Go selection by import scanning, still without installing Go

- **Decision**: extend `ci_scope.py` with a package graph: for every package directory in
  the selected modules, read the import blocks of its `.go` files (including `_test.go`)
  and build reverse edges. Selected packages = packages that own a changed file (any file
  under the package directory, including `testdata/` and embedded files) plus every
  package that imports them, transitively, across modules. The `go` job then runs
  `go test` on that package list instead of `./...`. `file_consumers` grows to cover tests
  that read files outside their package (today: `operator/config/crd` → api envtest,
  `modules` → gp-module).
- **Rationale**: the module scanner already proves literal import scanning is reliable
  in this repo, and keeping the `changes` job Go-free keeps it at about one minute.
- **Alternatives**: `go list -deps -test -json` (exact, but needs Go and a module
  download in the gate job); Bazel/Pants-style build graphs (far out of scope).

### R5. Coverage: module gate on full runs, changed-lines gate on partial runs

- **Decision (settled by OD-1)**: full runs keep `make cover-go-merge && make
  cover-go-check` and Vitest thresholds unchanged. Partial runs skip the module totals
  and run a new `hack/check_changed_coverage.py` that intersects `git diff -U0` added
  lines with the Go cover profile and the Vitest `coverage-final.json`, and fails when
  the covered share is below the threshold. Lines that are not statements (comments,
  blank, declarations) are ignored because neither profile lists them.
- **Threshold (settled OD-5, 2026-10-11)**: the module's own minimum. The script reads
  `threshold.total` from the module's `.testcoverage.yml`, and for web the `lines`
  threshold from `web/vitest.config.ts` (92), so no number is duplicated.
- **Rationale**: go-test-coverage v2.18.9 has no changed-lines mode that fits this repo;
  a stdlib script reading the two profile formats is about the size of the existing
  `ci_scope.py` helpers.
- **Alternatives**: third-party diff-coverage services (external upload, secrets);
  `diff-cover` (Python package to install in CI, Cobertura/lcov conversion step).

### R6. Web unit tests via `vitest related`

- **Decision**: on partial runs the `web` job runs `vitest related --run --coverage
  <changed web/src files>`, with thresholds disabled on the command line and the
  changed-lines check from R5 applied instead. `web/package.json`, `package-lock.json`,
  `vitest.config.ts`, `vite.config.ts`, `tsconfig*.json`, `src/test/**` (setup and
  helpers) and `eslint.config.js` force the full web suite. Lint and build stay whole
  (they are fast and catch cross-file type errors).
- **Rationale**: `related` walks Vite's module graph from the changed files to the tests
  that import them, which is exactly FR-001's "dashboard test file" level.
- **Alternatives**: `vitest --changed <ref>` (same graph but needs the base ref fetched
  inside the job and does not let `ci_scope.py` own the decision and the summary).

### R7. Playwright mock e2e stays whole when web is selected

- **Decision**: no per-spec selection for `web-e2e-mock`.
- **Rationale**: specs drive a browser and rarely import app code, so Playwright's
  `--only-changed` would select only edited spec files, which is unsafe. The job is about
  6 minutes and runs in parallel, so it is not on the critical path.

### R8. Shared CI configuration mapped per job (settled by OD-3)

- **Decision**: for `.github/workflows/ci.yaml`, map each changed line to the job whose
  block contains it (job keys are the two-space-indented keys under `jobs:`). Lines that
  are blank or comments after stripping select nothing beyond `workflow-lint`. A change
  outside any job (`on`, `env`, `concurrency`, `permissions`) or inside the `changes`
  or `report` jobs is a selection-rule change and forces the full suite (FR-011).
  Other shared files: `.golangci.yml` → every `lint` leg only; other workflow files and
  `.github/zizmor.yml`, `.github/dependabot.yml` → `workflow-lint` only. Still forcing
  everything (FR-014): `hack/ci_scope.py`, `hack/test_ci_scope.py`, `Makefile`,
  `go.work`, `go.work.sum`, `.github/actions/**`.
- **Rationale**: run 2201 changed one comment in `ci.yaml` and paid for all 89 jobs.
- **Alternatives**: a YAML-aware diff (needs a parser in the gate job; line ranges are
  enough because job keys are always at a fixed indent in this file).

### R9. Force a full run: a label read on each run, plus `workflow_dispatch`

- **Decision**: a PR carrying the label `ci: full-run` gets the full suite on its next
  run. `workflow_dispatch` is added so a maintainer can start a full run on a branch
  without pushing. The `labeled` event is **not** added as a trigger.
- **Rationale**: `auto-label.yaml` adds labels to every PR, so a `labeled` trigger would
  start a run per label and, through `cancel-in-progress`, cancel the real run.
- **Alternatives**: a comment command (needs `issue_comment` and a token with write
  scope); an empty commit (forbidden by the repo's PR rules).

### R10. Selection summary written by `ci_scope.py`

- **Decision**: `ci_scope.py` writes one Markdown table to `$GITHUB_STEP_SUMMARY` of the
  `changes` job: every suite and job, ran/skipped, and up to five changed paths that
  selected it (or the fail-open reason). It also emits a `selection` JSON output with the
  same content for the `report` job to link.
- **Rationale**: FR-008 and SC-005; the step summary shows on the run page without a
  new artifact or comment.

### R11. Skipped jobs and required checks

- **Finding**: GitHub reports a job skipped by `if:` as a successful check, and the
  existing `report` job already treats `skipped` as non-failing. The arm64 jobs get the
  same `exclude` list as their amd64 twins, so FR-010 holds by construction. Whether
  `master`'s ruleset currently requires named checks was not verified from this session;
  FR-013 holds either way.
- **Out of scope, noted**: `telemetry-default-gate` is missing from `report.needs`, and
  `bot-heavy` never runs in CI by design. Neither changes here.

### R12. No new measurement tooling

- **Decision**: SC-002 and SC-004 are measured by hand from the Actions API after
  rollout, recorded in this folder. No dashboard or script is added for them.
- **Rationale**: spec 008 recorded the cost of an unrequested verifier subsystem
  (`specs/done_008-hardened-github-actions/OPEN-DECISIONS.md`, D-H). This plan adds only
  what the requirements name.

### R13. The e2e test required by OD-4

- **Decision**: add `test/e2e/ci_selection_e2e_test.go` (`//go:build e2e`) with one
  `TestCISelection_<Scenario>` per spec acceptance scenario that a single commit can
  express (US1-1..4, US2-1..3, US3-1..2, plus the unmapped-path and full-run edges).
  Each test, with `t.Parallel()` and its own `t.TempDir()`:
  1. exports the checked-out tree with `git archive HEAD` into the temp dir and commits it
     as the base (works on the shallow checkout every e2e job uses);
  2. applies the scenario's edit and commits it as the head;
  3. runs `python3 hack/ci_scope.py modules --base <base> --head <head>` with
     `GITHUB_OUTPUT` pointed at a temp file;
  4. asserts the `selection`, `e2e-exclude` and `go-packages` outputs match the scenario;
  5. for every selected bucket, runs `test/e2e/buckets.sh regex <bucket>` and asserts the
     regex matches at least one `Test` function compiled into this e2e package, so a
     selection can never point at a bucket that runs nothing.
- **Bucket**: `operator` (zero logins, runs wide). The file is declared in
  `suite-components.json` with `paths` = `hack/ci_scope.py`, `test/e2e/suite-components.json`,
  `test/e2e/buckets.sh`, `.github/workflows/ci.yaml`; most of those already force the
  full suite, so the test always runs when the rules change.
- **Rationale**: the maintainer ruled the test required (OD-4, 2026-10-11). Step 5 is the
  part only the e2e package can check: it ties the selection output to the tests that
  actually exist in the compiled suite.
- **Needs on the runner**: `git` and `python3`, both present on `ubuntu-latest` and
  `ubuntu-24.04-arm`. No cluster resources are created, so the test adds seconds, not a
  new job.
