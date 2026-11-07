# Research: Modular Dev Containers

## R1. How several dev containers are offered

- **Decision**: Keep the full profile at `.devcontainer/devcontainer.json`, and put every other profile at `.devcontainer/<name>/devcontainer.json` with a `name` field such as `Gameplane · web`.
- **Rationale**: The dev container spec allows one config per first-level subfolder of `.devcontainer/`. VS Code ("Reopen in Container") and Codespaces ("New with options") both list every config found. When no choice is made, Codespaces uses `.devcontainer/devcontainer.json`, which meets FR-001 and FR-003 without any custom tooling. A subfolder config sets `"workspaceFolder"` and keeps the default workspace mount, so every profile opens the same clone (FR-015).
- **Alternatives**: Docker Compose with one service per component, rejected because it splits one workspace into several containers. A single container with a "which components?" prompt at setup, rejected because dev container hosts have no built-in prompt for that, and it hides the choice from the editor's list.

## R2. Where the dependency lists and versions live

- **Decision**: Each component folder gets a `system-deps.toml` ([contract](contracts/system-deps-format.md)). Versions are named keys in `.devcontainer/versions.toml`, and a dependency points to a key rather than holding a version string.
- **Rationale**: This meets valgul's requirement that each component lists what it needs, in its own folder (FR-004a). Keeping versions as keys puts each one in a single place (FR-007), and when two components need the same tool it is installed once at one version (spec edge case). TOML allows comments and Python 3.11+ reads it with the standard library (`tomllib`), so the scripts need no extra package.
- **Alternatives**: YAML, rejected because it needs PyYAML, which `hack/` does not use today. JSON, rejected because it has no comments and the reason lines would be awkward. Putting the lists in `specs.md` or the README, rejected because those files are not machine-checked.

## R3. Generated files instead of hand-written ones

- **Decision**: `hack/gen-devcontainers.py` writes every profile's `devcontainer.json`, `devcontainer-lock.json`, `setup.env` and `ready.txt`. Its `--check` mode runs in CI and fails on a stale file ([contract](contracts/generator-cli.md)).
- **Rationale**: This follows the same pattern as `make manifests` and the codegen drift check. It is the only way to keep the parts as true unions of their components (FR-005) and to reject a tool that no list accounts for (FR-004b, FR-014).
- **Alternatives**: Dev container `features` as the composition unit, with one local feature per component. That was rejected because features cannot share a version file, and the composition would still be duplicated across 11 JSON files.

## R4. Lock files

- **Decision**: Keep the hand-maintained source lock at `.devcontainer/devcontainer-lock.json`, which covers every feature in use. Each generated profile gets the subset of that lock for the features it uses.
- **Rationale**: Feature digests stay pinned per profile, and one `devcontainer upgrade` on the full profile refreshes all of them.

## R5. Shared setup script

- **Decision**: `.devcontainer/setup.sh` replaces `post-create.sh`. It has one function per step (`submodules`, `kind`, `oras`, `golangci_lint`, `envtest`, `go_mod_download`, `npm_ci_web`, `npm_ci_website`, `uv`, `specify`, `ai_clis`, `summary`), and it runs only the steps and versions listed in the profile's generated `setup.env`. The `postCreateCommand` is `bash .devcontainer/setup.sh .devcontainer/<name>/setup.env` ([contract](contracts/setup-and-need.md)).
- **Rationale**: This keeps today's idempotent `command -v` guards and today's "warn, don't fail" handling of the AI CLIs (FR-011). The `go mod download` loop reads its module list from `setup.env` instead of the hard-coded list, which today must be kept in sync with `go.work` by hand.

## R6. Missing-tool guard (FR-010)

- **Decision**: `hack/need.sh <tool> [<tool>…]` looks up each tool in the generated index `.devcontainer/tool-index.tsv` (tool → owning profiles). If a tool is missing, it prints `<tool> is not installed in this dev container; open profile "<profile>" (see <dir>/system-deps.toml)` and exits 127. It is called first in the recipes that need tools a component profile may lack: `dev-up`, `dev-load`, `dev-install`, `modules-push`, `test-e2e*`, `lint-go`, `images`, `web-dev`, `build-web`.
- **Rationale**: The check runs inside the recipe, not when the Makefile is parsed, so CI, which has every tool, sees no change. Exit code 127 matches "command not found".

## R7. CI workflow

- **Decision**: Add `.github/workflows/devcontainers.yaml`, with:
  - `drift`: `python3 hack/gen-devcontainers.py --check` and `python3 hack/test-gen-devcontainers.py`.
  - `build`: a matrix of every affected profile × {amd64 on `ubuntu-latest`, arm64 on `ubuntu-24.04-arm`}, the same runner choice as the e2e jobs in `ci.yaml`. It uses `devcontainers/ci` pinned by SHA, with `push: never` and `runCmd` set to that profile's `checks` from the catalogue (compile checks only, such as `go build ./...` per module and `npx tsc --noEmit`). It writes setup seconds and `du -sx` disk use to `$GITHUB_STEP_SUMMARY` (SC-001, SC-002).
  - The workflow triggers on changes to `.devcontainer/**`, `**/system-deps.toml`, `hack/gen-devcontainers.py`, `hack/need.sh`, `Makefile`, `go.work`, `web/package*.json` and the workflow itself. "Affected" profiles come from the generator's `--affected <paths>` mode. A change to `versions.toml`, `profiles.toml` or `setup.sh` affects every profile.
  - `check-devcontainers` is added to `make lint`.
- **Rationale**: This meets FR-014 and SC-004. The workflow stays separate from `ci.yaml` so it does not tangle with spec 023's change-based selection, which has not been built yet.

## R8. Submodule repo profiles (OD-3)

- **Decision**: The main-repo generator also writes `website/.devcontainer/` and `modules/.devcontainer/` (with `--submodules`), using the same `versions.toml` values inlined. Each submodule repo commits its generated files and adds one build job. The main-repo `drift` job checks the submodule copies at the pinned commit, so a pointer bump with stale versions fails.
- **Rationale**: Each repo works when cloned on its own, and versions still come from one source. A submodule repo cannot read the main repo at build time, so the values are inlined and the main repo's check is what keeps them in step.
- **Alternatives**: Have each submodule repo fetch `versions.toml` from the main repo at setup time. This was rejected because it needs network access and breaks a clone used on its own.

## R9. System dependency inventory (input for tasks)

Known today, from `.devcontainer/devcontainer.json` and `post-create.sh`:
- Every Go module needs the Go toolchain (`go` 1.26, `GOTOOLCHAIN=local`). Every Dockerfile builds with `CGO_ENABLED=0`, so no C toolchain or OS libraries are needed.
- `operator`, `api`: envtest assets (`setup-envtest`, K8s 1.36.2), and `kubectl`/`helm` for codegen-adjacent work.
- `web`: Node 24 and npm, plus the Playwright Chromium the e2e mock suite uses (pending the scout check of `web/package.json`).
- `charts/gameplane`: `helm`. `deploy/kind`: Docker (docker-in-docker), `kind` v0.32.0, `kubectl`.
- `modules`: `oras` ≥ 1.2.0, plus whatever `modules/build.sh` calls (scout task).
- `website`: Node and npm (version from the website's `package.json` `engines`, scout task).
- Lint tooling: `golangci-lint` v2.12.2 goes with the Go component lists.
- Baseline: `common-utils` (zsh), `git`, `github-cli`, `uv`, `specify` v1.0.3, and the AI CLIs.

The scout task (rule 18) confirms each entry, adds anything a Makefile target or script runs, and writes the brief the `system-deps.toml` script is generated from.

## R10. Today's version drift (input for OD-5)

- `kubectl` 1.31 (dev container feature) against envtest and test clusters on Kubernetes 1.36.2 (`Makefile:166`).
- `setup-envtest@latest` (`post-create.sh`) against `setup-envtest@release-0.24` (`Makefile:171`).
- `helm: latest` (feature) against a pinned `azure/setup-helm@v5.0.1` in CI. The action pins the action's own version, not Helm's.

Decision (OD-5, valgul 2026-10-20): align all three. `versions.toml` gets `kubectl = "1.36"`, `setup_envtest = "release-0.24"`, `envtest_k8s = "1.36.2"`, and `helm` pinned to the current stable release on the day the foundation PR is written. The generator checks the Makefile's `ENVTEST_K8S_VERSION` and `setup-envtest@` ref against these keys.
