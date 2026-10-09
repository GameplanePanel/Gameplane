# Implementation Plan: Expand Test Coverage

**Branch**: `claude/project-thread-nuvx3j` (the spec-kit script derived `013-expand-test-coverage`) | **Date**: 2026-10-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/013-expand-test-coverage/spec.md`

## Summary

Close the spec's coverage gaps across all four test tiers, starting from a re-surveyed baseline ([research.md R0](research.md#r0-current-baseline-re-survey-of-the-specs-gaps)) because several gaps closed after the spec was written (telemetry E2E, two of the three UI component tests, five of the live dashboard flows).

**Static (US1).** Five new blocking gates:
- `govulncheck` per Go module (R1)
- Trivy on every built image, also before `cosign sign` in the publish workflows (R2)
- hadolint inside `workflow-lint` (R3)
- `dependency-review-action` on PRs (R4)
- codegen/tidy drift and submodule-pointer freshness (R5)

Each ships with a fixture-driven `_test.sh` that proves it can fail. The one exception is dependency-review, whose proof is a recorded, closed validation PR. Pre-existing findings are fixed in the PR that turns the gate on; nothing is suppressed.

**Live dashboard E2E (US2).** Five new live Playwright specs: notifications, users + roles, role bindings, backup restore, mod registries. Each seeds through the real API, reloads to prove persistence, and asserts one real API error path (R6).

**Optional components (US3).** A new `optional-components` bucket holds one lifecycle test. It proves the disabled state, then enables the MCP server, syslog bridge and an frp tunnel against in-cluster sinks, exercises each, and proves core flows still work (R7). The telemetry receiver is already covered by `TestTelemetryLifecycle`.

**Unit (US4).** `secrets_managed_test.go` (100 % of the file) and `ConsoleShell.test.tsx` (≥ 80 % lines). The existing `CloneServerDialog`/`ServerActionsMenu` tests are topped up only if CI shows them under 80 % (R8).

**Postgres (US5).** An `e2e-postgres` job re-runs the `api-auth`, `api-roles` and `api-rbac` buckets against a kind cluster whose API is built with `-tags postgres` and backed by in-cluster PostgreSQL 17. The upgrade path is proven at the DB layer from the frozen 001–012 baseline (R9).

**Tracking (FR-009).** `coverage-gaps.md` in this folder is the single Coverage Gap Record, validated by `hack/check-coverage-gaps.sh` (R10).

## Technical Context

**Language/Version**: Go 1.26 (`go.work`; images build on `golang:1.27-alpine`), TypeScript strict / React 19, Bash for gate scripts, GitHub Actions YAML.

**Primary Dependencies**: new CI tools only, all pinned:
- `golang.org/x/vuln/cmd/govulncheck` (`go install @vX.Y.Z`)
- `aquasecurity/trivy-action` (SHA)
- hadolint release binary (sha256-verified)
- `actions/dependency-review-action` (SHA)

Test-side: Playwright 1.63, vitest 5, `client-go/kubernetes/fake`, existing e2e helpers. No runtime dependency is added to any shipped component.

**Storage**: N/A for the gates. The Postgres E2E uses a throwaway in-cluster PostgreSQL 17 (same digest as the `api-postgres` service, `ci.yaml:1217`).

**Testing**: everything runs in CI only (Constitution VI, CLAUDE.md rule 8). Locally only `go build ./...`, `go vet -tags e2e ./...` compile checks and `npx tsc --noEmit` / `npm run typecheck:e2e`.

**Target Platform**: GitHub Actions `ubuntu-latest` (amd64) and `ubuntu-24.04-arm` (arm64); kind clusters.

**Project Type**: multi-component monorepo (16 Go modules, React dashboard, Helm chart); this feature touches CI, tests, two Dockerfile/bake inputs and one kind profile.

**Performance Goals**:
- New static jobs finish in ≤ 10 min each and run in parallel with existing jobs, never on the e2e critical path.
- `e2e-web-live` stays under its 60 min timeout.
- `optional-components` ≤ 30 min per arch.
- `e2e-postgres` ≤ the slowest existing `e2e-go` leg.

**Constraints**:
- No suppressions or threshold changes (FR-008, Constitution III).
- Login budget: per-IP burst 10, per-user burst 6 per cluster.
- Every E2E test is `t.Parallel()`-safe, uses unique names and is bucketed.
- No arm64-only bucket.
- Actions are SHA-pinned (zizmor/actionlint).
- Every new job is registered in the `report` job (`check-ci-report-coverage.sh`).

**Scale/Scope**:
- 5 static gates, 5 gate-proof scripts
- 5 live Playwright specs
- 1 lifecycle E2E test (+1 helper test)
- 2 unit test files (+ conditional top-ups)
- 1 Postgres E2E job + 1 DB-layer upgrade test
- 1 gap record with validator

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design (below).*

| Principle | Assessment | Status |
|---|---|---|
| **I. E2E-Tested Delivery** | The feature *adds* E2E. New Go E2E tests are bucketed (`optional-components`, and `TestE2E_ResticRepoReady` in `operator`), `t.Parallel()`, with unique names and budgeted logins. Postgres reuses existing buckets, so no test is unbucketed. The static gates are CI config, verified by their own fixture proofs, not E2E; this is the same pattern as the existing `joincoverage_test.sh` and `lint-gate-verify_test.sh`. | PASS |
| **II. Design-First** | No visual change. Tests only. A UI defect a new live spec exposes is fixed in its own task, designed first in `design.pen`. | PASS (N/A) |
| **III. Best Practice / no suppressions** | No `//nolint`, `eslint-disable`, `@ts-ignore`, hadolint ignore, Trivy ignore file, or `allow-ghsas`. Pre-existing findings are fixed in code. `.hadolint.yaml` holds only a threshold and trusted registries. New Go test code wraps errors with `%w`. | PASS |
| **IV. Spec-Driven** | The spec → plan → tasks chain is followed. `api/specs.md` and `tunnel/specs.md` are unaffected (no behavior change). `deploy/kind` and `.github` have no `specs.md` requirement. `api/Dockerfile` gains a build arg with the default unchanged, so the published image is identical. | PASS |
| **V. Delegation** | Implementation runs through `Workflow` scripts per CLAUDE.md rule 13, at haiku first with tier-up diff review. Gate scripts are rule-shaped work, so one agent writes them as scripts (rule 13, "scripts over fan-out"). | PASS |
| **VI. CI Bears the Load** | No local test or lint runs. Notably, hadolint's pre-existing findings are discovered by the gate PR's first CI run, not locally (R3). Nothing is reported green until CI is. | PASS |

**Post-design re-check (after Phase 1)**: unchanged. The design adds one deviation, recorded below: dependency-review's proof-of-failure is a recorded PR rather than a script.

## Project Structure

### Documentation (this feature)

```text
specs/013-expand-test-coverage/
├── spec.md
├── plan.md                         # this file
├── research.md                     # Phase 0 (R0 baseline + R1–R11 decisions)
├── data-model.md                   # Phase 1: gate, fixture, gap, record entities
├── quickstart.md                   # Phase 1: how to validate each story in CI
├── OPEN-DECISIONS.md               # OD-1 ruled; OD-2, OD-3 open (rule 10)
├── contracts/
│   ├── static-gates.md             # per-gate trigger / failing condition / proof script contract
│   ├── coverage-gap-record.md      # coverage-gaps.md format + validator rules
│   └── e2e-additions.md            # new buckets, jobs, live specs, image/profile inputs
├── coverage-gaps.md                # created by the first implementation task (FR-009)
├── checklists/requirements.md
└── tasks.md                        # /speckit-tasks (not created here)
```

### Source Code (repository root)

```text
.github/
├── workflows/ci.yaml               # new jobs: vuln-go, vuln-images, dependency-review,
│                                   #   codegen-drift, submodule-freshness, e2e-postgres;
│                                   #   hadolint step in workflow-lint; optional-components in
│                                   #   e2e-go matrix; restic warm-up step in e2e-web-live;
│                                   #   report needs/NEEDS_ORDER/JOB_MATCHERS
├── workflows/{publish-edge,release}.yaml   # Trivy before cosign sign
└── actions/{build-e2e-images,e2e-images}/  # + mcp-server, audit-syslog-bridge, tunnel-frp,
                                            #   api:e2e-postgres
.hadolint.yaml                      # threshold + trusted registries only
hack/
├── ci_scope.py, test_ci_scope.py   # new scope outputs
├── check-submodule-freshness.sh
├── check-coverage-gaps.sh
├── check-{govulncheck,image-scan,hadolint,codegen-drift,submodule-freshness,coverage-gaps}_test.sh
└── testdata/{govulncheck,hadolint,coverage-gaps}/{pass,fail}/   # + govulncheck/db (synthetic OSV)
api/
├── Dockerfile                      # ARG GO_TAGS="" (default unchanged)
├── internal/handlers/secrets_managed_test.go
└── internal/db/db_postgres_test.go # + TestPostgres_MigrateFromFrozenBaseline
deploy/kind/e2e.sh                  # + postgres profile
test/e2e/
├── optional_components_e2e_test.go # TestOptionalComponents_Lifecycle
├── fixtures/{syslog-sink,frps}.yaml
├── test_helpers_e2e_test.go        # + TestE2E_ResticRepoReady
└── buckets.sh                      # + bucket_optional_components; + restic helper in operator
web/
├── src/routes/tabs/ConsoleShell.test.tsx
├── src/components/server/{CloneServerDialog,ServerActionsMenu}.test.tsx  # top-up only if < 80 %
└── e2e/specs/live/{admin-notifications,users-and-roles,role-bindings,backup-restore,mod-registries}.spec.ts
(Dockerfiles across agent/, api/, operator/, … — hadolint fixes only)
```

**Structure Decision**: no new module or package. Gates live in `.github/` + `hack/` beside the existing `check-*` scripts and their `test-check-*`/`*_test.sh` proofs. Tests sit next to the code they cover, following each tier's existing layout.

## Phase sequencing (input to /speckit-tasks)

1. **Foundation**: seed `coverage-gaps.md` and its validator from R0, and add the `ci_scope.py` outputs. Everything else updates the record.
2. **US1 static gates** (P1), one PR per gate so each can be shown failing then passing in isolation. Order: codegen drift → govulncheck → hadolint (+ fixes) → Trivy (+ base bumps) → dependency-review (validation PR) → submodule freshness.
3. **US2 live specs** (P1), in parallel with US1. The restic helper lands first.
4. **US4 unit** (P2), independent; can run in parallel with anything.
5. **US3 optional components** (P2): image bake inputs first, then the lifecycle test.
6. **US5 Postgres** (P3): Dockerfile arg + bake, the kind profile, the job, and the DB upgrade test.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| FR-006 proof for dependency-review is a recorded, closed validation PR, not a script run on every change | The action evaluates the PR's diff through GitHub's dependency-graph API; no local or fixture input exists | A committed vulnerable `package.json`/`go.mod` fixture would trigger real Dependabot security alerts on `master` and could be pulled into builds |
| SC-005 "same scenarios" is met by a second execution of existing buckets, not by tests in a Postgres bucket | `buckets.sh verify` requires each test in exactly one bucket | Copying tests into a `postgres` bucket duplicates and drifts; a db matrix over all of `e2e-go` doubles the largest job |
| US5 scenario 3 (upgrade from a prior version) is proven at the DB layer, not in kind | No released API image was ever built with `-tags postgres`, so no prior Postgres deployment exists to upgrade from | Building an old commit's API with the tag would test a binary that never shipped |
