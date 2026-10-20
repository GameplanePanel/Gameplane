# Implementation Plan: Modular Dev Containers

**Branch**: `claude/project-thread-427u3v` | **Date**: 2026-10-20 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/026-modular-dev-containers/spec.md`

## Summary

Split today's single dev container into generated profiles at three sizes (component, part, full). Each component declares its system dependencies in a `system-deps.toml` file in its own folder. One catalogue (`.devcontainer/profiles.toml`) groups components into profiles and holds the shared baseline, and one version file (`.devcontainer/versions.toml`) holds every tool version. A stdlib-only Python generator (`hack/gen-devcontainers.py`) builds every profile's `devcontainer.json`, lock file, setup list and ready message from those three inputs. The output is committed, and a drift check fails when it is stale, like `make manifests`. One shared setup script installs only the steps a profile lists. A new CI workflow builds each affected profile on amd64 and arm64 and runs that profile's compile checks. The website and module repos get their own generated profile and build job (OD-3). Task-runner targets that need a missing tool stop with a message naming the profile that has it.

## Technical Context

**Language/Version**: Python 3.12 (stdlib only, `tomllib`) for the generator and checks, matching `hack/ci_scope.py`. Bash for the shared setup script, matching today's `post-create.sh`. JSON for dev container files, and TOML for the hand-written inputs.

**Primary Dependencies**: the dev container spec and its published features (`ghcr.io/devcontainers/features/*`, already in use), the `devcontainers/ci` GitHub Action (pinned by SHA per spec 008) and the `@devcontainers/cli` it wraps.

**Storage**: Files in git. No runtime storage.

**Testing**: Fixture tests for the generator in `hack/test-gen-devcontainers.py` (same pattern as `hack/test-check-portable-paths.py`), and the CI workflow `devcontainers.yaml` that builds real profiles (the E2E path, OD-4). All of this runs in CI only (Principle VI).

**Target Platform**: Dev container hosts: VS Code Dev Containers, GitHub Codespaces and the dev container CLI, on linux/amd64 and linux/arm64.

**Project Type**: Developer tooling (build and config scripts plus CI).

**Performance Goals**: SC-001: every component profile finishes setup in at most half the full profile's time. SC-002: every component profile uses at most half the full profile's disk. Both are measured in the CI build job and written to the job summary.

**Constraints**: No credentials in any profile (FR-012). Everything must work on arm64 (FR-013). Setup must be idempotent, and optional steps only warn (FR-011). Full profile parity with `master@8a67f2e` (FR-003), except the three OD-5 alignments. No local test or lint runs (Principle VI, CLAUDE.md rule 8).

**Scale/Scope**: 7 component profiles, 3 part profiles and 1 full profile in the main repo, plus 1 profile each in the website and module repos. About 22 `system-deps.toml` files: 17 Go modules, `web/`, `charts/gameplane/`, `deploy/kind/`, `website/` and `modules/`.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|---|---|---|
| I. E2E-Tested Delivery | PASS (exemption, OD-4) | valgul ruled on 2026-10-20 that the `devcontainers.yaml` CI build job is the E2E path: it builds every real profile on amd64 and arm64 and runs its compile checks. No `test/e2e/` Go test is added. |
| II. Design-First | PASS (exempt) | No dashboard or website screen changes. The website edit is text in `local-development-setup.mdx` and adds no new visual component. |
| III. Ecosystem Best Practice | PASS | The generator is stdlib Python like the other `hack/` scripts. The setup script uses `set -euo pipefail`. No suppressions. |
| IV. Spec-Driven | PASS (all decisions OD-1..OD-5 settled) | spec, plan and tasks come before any code. Open items stay in `OPEN-DECISIONS.md`. |
| V. Delegate | PASS | Implementation runs through `Workflow` scripts at `haiku`, with review one tier up. Writing the 22 dependency files is scripted from one scout brief (rule 18). |
| VI. CI Bears the Load | PASS | Profiles are verified only by CI build jobs. The ready messages point to CI for tests and lint. |

## Project Structure

### Documentation (this feature)

```text
specs/026-modular-dev-containers/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── OPEN-DECISIONS.md
├── contracts/
│   ├── system-deps-format.md
│   ├── profiles-catalogue.md
│   ├── generator-cli.md
│   └── setup-and-need.md
├── checklists/requirements.md
└── tasks.md            # /speckit-tasks
```

### Source Code (repository root)

```text
.devcontainer/
├── devcontainer.json          # GENERATED: full profile, stays the default (FR-003)
├── devcontainer-lock.json     # hand-kept: feature pins for all profiles (source)
├── versions.toml              # NEW: the single version source (FR-007)
├── profiles.toml              # NEW: catalogue, baseline, parts, editor extras
├── setup.sh                   # NEW: shared setup, replaces post-create.sh
├── full/                      # GENERATED: setup.env, ready.txt
├── web/ api/ operator/ agent/ website/ modules/ go-services/
│   ├── devcontainer.json      # GENERATED, one folder per component profile
│   ├── devcontainer-lock.json # GENERATED subset of the source lock
│   ├── setup.env              # GENERATED: steps + versions for setup.sh
│   └── ready.txt              # GENERATED: ready message (FR-009)
└── dashboard-api/ control-plane/ modules-gp/   # GENERATED part profiles, same files
<every Go module>/system-deps.toml  # NEW (17 files, FR-004a)
web/system-deps.toml
charts/gameplane/system-deps.toml
deploy/kind/system-deps.toml
hack/gen-devcontainers.py      # NEW generator + --check drift mode
hack/test-gen-devcontainers.py # NEW fixture tests (CI only)
hack/need.sh                   # NEW missing-tool guard (FR-010)
Makefile                       # devcontainers, check-devcontainers targets; need.sh guards
.github/workflows/devcontainers.yaml  # NEW: drift + build matrix (FR-014)
docs/contributing.md           # profile table + how to add a profile (FR-016)
docs/dependencies.md           # devcontainer pins now point at versions.toml
CLAUDE.md                      # repository map + commands lines for profiles

website/ (GameplanePanel/website)
├── system-deps.toml
├── .devcontainer/devcontainer.json (+ lock, setup.sh, ready.txt)   # GENERATED by main repo
├── .github/workflows/ci.yaml  # + devcontainer build job
└── src/content/docs/local-development-setup.mdx  # profiles section (FR-016)

modules/ (GameplanePanel/module)
├── system-deps.toml
├── .devcontainer/... (GENERATED by main repo)
└── .github/workflows/<ci>     # + devcontainer build job
```

**Structure Decision**: The dev container files are generated, and the hand-written inputs are the per-folder `system-deps.toml` files plus two catalogue files under `.devcontainer/`. The full profile stays at `.devcontainer/devcontainer.json`, so Codespaces and editors that don't ask still get today's container. Every other profile sits in a `.devcontainer/<name>/` folder, which all three hosts list as a choice (research R1).

## Phasing (PRs)

1. **Main repo, foundation**: `versions.toml`, `profiles.toml`, every main-repo `system-deps.toml`, the generator plus fixture tests, `setup.sh`, the generated full profile (parity with today plus the three OD-5 alignments), drift check, `make devcontainers` / `make check-devcontainers`.
2. **Main repo, profiles + CI**: the generated component and part profiles, `devcontainers.yaml` build matrix, `need.sh` guards.
3. **Website repo**: `system-deps.toml`, generated `.devcontainer/`, build job, docs page section. **Module repo**: same, minus the docs page. Then the main-repo submodule pointer bump (after both merge).
4. **Main repo, docs**: `docs/contributing.md` table, `docs/dependencies.md`, `CLAUDE.md`. Then archive the spec.

## Open items blocking tasks

None. OD-1 to OD-5 are settled (see `OPEN-DECISIONS.md`).

## Complexity Tracking

| Choice | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Generated dev container files plus a drift check | 11 main-repo profiles plus 2 submodule copies would drift by hand (US5, FR-007) | Hand-written JSON per profile: the version drift in today's single file shows this already fails |
| Copies inside the website and module repos | OD-3 (valgul): each repo must work when cloned on its own | Main-repo-only profiles were the rejected option on the OD-3 card |
