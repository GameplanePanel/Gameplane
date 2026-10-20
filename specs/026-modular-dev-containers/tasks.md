---

description: "Task list for spec 026 modular dev containers"
---

# Tasks: Modular Dev Containers

**Input**: Design documents from `specs/026-modular-dev-containers/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md, OPEN-DECISIONS.md (OD-1 to OD-5 settled by valgul, 2026-10-20)

**Tests**: The generator gets fixture tests (plan Technical Context). The E2E path is the `devcontainers.yaml` build job (OD-4); no `test/e2e/` test is added.

**Organization**: Tasks are grouped by user story. The PR column in the plan's "Phasing" section maps them: **PR-A** foundation (Phases 1–4), **PR-B** profiles and CI (Phases 5–7), **PR-C** website and module repos (Phase 8), **PR-D** docs, pointer bump and archive (Phases 9–10). Each PR is one branch.

**Binding rules for every task** (repeat them in every Workflow script and agent brief: CLAUDE.md rules 2, 4, 8, 11–14, 18):

- Locally, run only compile and syntax checks: `python3 -m py_compile hack/gen-devcontainers.py hack/test-gen-devcontainers.py`, `bash -n .devcontainer/setup.sh hack/need.sh`, `python3 hack/gen-devcontainers.py --check` (a generator run, not a test suite), `go build ./...`, `npx tsc --noEmit`. Never run `make test|lint|cover`, `go test`, `npm test`, `python3 hack/test-gen-devcontainers.py`, `devcontainer build`, `act`/`wrkflw` or any workflow locally. CI is the only verifier.
- `hack/gen-devcontainers.py` and its tests use the Python standard library only (`tomllib`, `json`, `argparse`, `pathlib`, `difflib`, `unittest`).
- Generated files are never hand-edited. Change the inputs (`system-deps.toml`, `.devcontainer/versions.toml`, `.devcontainer/profiles.toml`, the source lock), then run `make devcontainers`.
- No existing test, check or CI job is deleted, weakened or skipped. No `# noqa`, `//nolint` or `eslint-disable`. `.pen` files are never touched.
- No credentials, tokens or personal sign-ins in any file (FR-012).
- Signed commits (`git commit -s`), conventional prefix, `Co-Authored-By` trailer, never amend after push. No "Requested by" block and no session links in PRs or commits. PR labels via REST: `type: chore` plus `area: shared` (add `type: ci` when touching `.github/workflows/`, `type: docs` for PR-D).
- Implementation runs through Workflow scripts with `model:` set on every agent, starting at haiku. Review is one tier up, on the diff. Scout once, brief many (rule 18). Helper agents never call `mcp__hearthbot__` tools.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: the user story from spec.md (US1–US6)

---

## Phase 1: Setup (PR-A)

**Purpose**: the hand-written inputs every profile is generated from.

- [ ] T001 Scout (one agent, `haiku`, read-only): build the system dependency inventory from research R9. For each folder in `go.work`, plus `web/`, `charts/gameplane/`, `deploy/kind/`, `test/e2e/`, `website/` and `modules/`, list every toolchain, CLI, OS package, service and submodule that its Makefile targets, scripts (`modules/build.sh`, `deploy/kind/*.sh`, `web/package.json` scripts, `website/package.json` scripts and `engines`) and today's `.devcontainer/post-create.sh` invoke. Give `file:line` evidence for each entry. Also list each Go module's in-repo `require`/`replace` targets. Write the brief to `specs/026-modular-dev-containers/inventory.md`.
- [ ] T002 Create `.devcontainer/versions.toml` with one `[tools]` key per tool from today's `.devcontainer/devcontainer.json` and `post-create.sh` (go 1.26, node 24, kind v0.32.0, oras 1.2.0, golangci_lint v2.12.2, envtest_k8s 1.36.2, specify v1.0.3, …). Apply OD-5: `kubectl = "1.36"`, `setup_envtest = "release-0.24"`, and `helm` pinned to the current stable Helm release (record the version and date in a comment).
- [ ] T003 Create `.devcontainer/profiles.toml` per [contracts/profiles-catalogue.md](contracts/profiles-catalogue.md) and data-model "Profile" and "Baseline". The baseline carries the common-utils, git, github-cli, uv, specify and AI CLI deps (optional), `anthropic.claude-code`, and today's shared editor settings and env (`GOTOOLCHAIN=local`, `CI=1`). `[full].personal` carries today's theme and other-language extensions (Lunar Pink, darkpinkpro, Rust, Vue, Gemini, Pencil, …). It defines the 11 profiles (`web`, `api`, `operator`, `agent`, `go-services`, `website`, `modules`, `dashboard-api`, `control-plane`, `modules-gp`, `full`) with ports, extensions, checks and submodules from the contract.

---

## Phase 2: Foundational (PR-A)

**Purpose**: the generator, its tests and the shared setup script. They block every story.

- [ ] T004 Implement input loading and validation in `hack/gen-devcontainers.py`: parse `versions.toml`, `profiles.toml` and every `system-deps.toml`, and enforce every rule in [contracts/system-deps-format.md](contracts/system-deps-format.md) and the error list in [contracts/generator-cli.md](contracts/generator-cli.md). Each error names the file and the profile.
- [ ] T005 Implement composition in `hack/gen-devcontainers.py`: derive each component profile's library folders from `go.mod` in-repo `require`/`replace` targets, build part profiles as unions (FR-005), build the full profile as the union of all plus `[full].personal`, and collapse duplicate deps (data-model invariant).
- [ ] T006 Implement output in `hack/gen-devcontainers.py`: write `devcontainer.json` (tab-indented, sorted, trailing newline, `name` = `Gameplane · <title>`, `postCreateCommand` = `bash .devcontainer/setup.sh .devcontainer/<id>/setup.env`), the subset `devcontainer-lock.json` from the source lock (research R4), `setup.env`, `ready.txt` ([contracts/setup-and-need.md](contracts/setup-and-need.md)) and `.devcontainer/tool-index.tsv`. The full profile's JSON goes to `.devcontainer/devcontainer.json`, and its other files to `.devcontainer/full/`.
- [ ] T007 Implement `--check`, `--affected PATH...` and `--submodules` modes in `hack/gen-devcontainers.py` per [contracts/generator-cli.md](contracts/generator-cli.md), including the check that the `Makefile` `ENVTEST_K8S_VERSION` and `setup-envtest@` ref equal `versions.toml` (OD-5).
- [ ] T008 [P] Write fixture tests in `hack/test-gen-devcontainers.py` (stdlib `unittest`, temp-dir fixture trees). Cover: literal version rejected, unknown `kind`, empty `reason`, `none` together with deps, missing `system-deps.toml`, `go.work` module in no profile, unused version key, union rule for a part, duplicate collapse, hand-edited output detected by `--check`, `--affected` for a deps file and for `versions.toml`, and Makefile pin mismatch.
- [ ] T009 Create `.devcontainer/setup.sh` per [contracts/setup-and-need.md](contracts/setup-and-need.md): move each block of `.devcontainer/post-create.sh` into a step function with the same guards, read versions and `GO_MODULES` from `setup.env`, run only the listed steps, treat `ai_clis` as warn-only (FR-011), print versions of installed tools only, then print `ready.txt`.
- [ ] T010 Add Makefile targets `devcontainers` (runs `python3 hack/gen-devcontainers.py --submodules`) and `check-devcontainers` (`--check`), add `check-devcontainers` to the `lint:` prerequisites in `Makefile`, and add a `test-gen-devcontainers` target that runs the fixture tests (CI only).

**Checkpoint**: the generator runs and `--check` passes on the PR-A tree.

---

## Phase 3: User Story 6 - See what a component needs (Priority: P1) 🎯 MVP part 1 (PR-A)

**Goal**: every component folder lists its own system dependencies (FR-004a, FR-004b).

**Independent Test**: open any component folder and read its `system-deps.toml`. The `drift` CI job passes, and it fails when a tool is added to `profiles.toml` extras without a reason or when a list is removed.

- [ ] T011 [US6] Generate every main-repo `system-deps.toml` from the T001 brief with one deterministic script run by one `haiku` agent (rule 13 "scripts over fan-out"): `agent/`, `api/`, `audit-syslog-bridge/`, `capture-sidecar/`, `gameaction/`, `gameproto/`, `gp-module/`, `i18n/`, `mcp-server/`, `netguard/`, `operator/`, `sentinel/`, `svcutil/`, `telemetryschema/`, `telemetry-receiver/`, `test/e2e/`, `tunnel/`, `web/`, `charts/gameplane/`, `deploy/kind/`. Each entry uses a `versions.toml` key and a one-line reason that cites the brief's evidence.
- [ ] T012 [US6] Remove `.devcontainer/post-create.sh` (its steps now live in `.devcontainer/setup.sh`, T009) and update its references in `docs/dependencies.md`. The website page reference is changed in PR-D (T033).

---

## Phase 4: User Story 3 - Keep the full profile (Priority: P2) (PR-A)

**Goal**: the default container keeps everything from today (FR-003), with only the three OD-5 alignments.

**Independent Test**: quickstart §4. Diff the generated `.devcontainer/devcontainer.json` against `git show 8a67f2e:.devcontainer/devcontainer.json`.

- [ ] T013 [US3] Run `make devcontainers` and commit the generated `.devcontainer/devcontainer.json`, `.devcontainer/full/*` and `.devcontainer/tool-index.tsv`. In the PR body, include the diff against `8a67f2e`, showing that only the `postCreateCommand` path, the `name`, kubectl 1.36 and the pinned helm differ (setup-envtest lives in `setup.env`).
- [ ] T014 [US3] Add a temporary `full`-only build to `.github/workflows/devcontainers.yaml` (`drift` job plus one `build` job for `full` on amd64 and arm64, using `devcontainers/ci` pinned by SHA with `push: never`), so PR-A is verified before PR-B adds the matrix. PR-B (T021) extends this file rather than replacing it.

**Checkpoint (PR-A)**: `drift` green, `full` builds on both arches, `make lint` green. Open PR-A.

---

## Phase 5: User Story 1 - Small profile for one component (Priority: P1) 🎯 MVP part 2 (PR-B)

**Goal**: 7 component profiles, each with only its scope (FR-001, FR-004, FR-008, FR-009).

**Independent Test**: quickstart §2. Each component profile builds in CI and passes its checks, and tools outside its scope are absent.

- [ ] T015 [US1] Run `make devcontainers` and commit `.devcontainer/{web,api,operator,agent,go-services,website,modules}/*` (generated).
- [ ] T016 [P] [US1] Create `hack/need.sh` per [contracts/setup-and-need.md](contracts/setup-and-need.md) (reads `.devcontainer/tool-index.tsv`, exits 127, falls back to a plain message when the index is absent).
- [ ] T017 [US1] Call `hack/need.sh` as the first recipe line of the Makefile targets from research R6 (`dev-up`, `dev-load`, `dev-install`, `modules-push`, `test-e2e`, `test-e2e-bucket`, `lint-go`, `images`, `web-dev`, `build-web`), naming the tools each one runs. Recipe bodies stay otherwise unchanged.
- [ ] T018 [US1] In the `website` and `modules` component profiles, set submodule init to only that submodule (`SUBMODULES` in `setup.env`), and confirm the generated `setup.env` has no `go_mod_download` or `envtest` step (spec US1 scenarios 2 and 3).

---

## Phase 6: User Story 2 - Part profiles (Priority: P2) (PR-B)

**Goal**: `dashboard-api` (with kind, OD-2), `control-plane` and `modules-gp` (FR-005).

**Independent Test**: quickstart §3. `dashboard-api` runs `make dev-up` and `make web-dev`, and the dashboard shows the sample servers.

- [ ] T019 [US2] Run `make devcontainers` and commit `.devcontainer/{dashboard-api,control-plane,modules-gp}/*` (generated). Confirm `dashboard-api` has docker-in-docker, kind, kubectl and helm from its `extras` / `deploy/kind` and `charts/gameplane` lists, and forwards 5173, 8080, 8443 and 5001.
- [ ] T020 [US2] Make the `dashboard-api` ready message, generated from `profiles.toml` `checks` and notes, give the run order `make dev-up` → `make web-dev` → open the forwarded 5173 port.

---

## Phase 7: User Story 5 - Keep profiles in step (Priority: P3) (PR-B)

**Goal**: one version source and a CI check that builds every affected profile (FR-007, FR-014, SC-004, SC-005).

**Independent Test**: quickstart §1 and §5.

- [ ] T021 [US5] Extend `.github/workflows/devcontainers.yaml` per research R7: path triggers, a `profiles` job that runs `python3 hack/gen-devcontainers.py --affected` on the PR's changed files and emits a JSON matrix, and a `build` matrix of affected profile × {amd64 `ubuntu-latest`, arm64 `ubuntu-24.04-arm`} running each profile's `checks` with `devcontainers/ci` (SHA-pinned, `push: never`). Write setup seconds and `du -sx` of the workspace and image size to `$GITHUB_STEP_SUMMARY`. Every action is SHA-pinned with a version comment (spec 008 rules). Set `permissions: contents: read`.
- [ ] T022 [US5] Add a `drift` step that runs `make test-gen-devcontainers` (fixture tests) next to `--check` in `.github/workflows/devcontainers.yaml`.
- [ ] T023 [US5] Add `.github/workflows/devcontainers.yaml` to whatever lists hardened workflows today (the spec 008 action-pin check), if such a list exists. The T001 scout confirms where it is.

**Checkpoint (PR-B)**: every profile green on both arches. Job summary shows SC-001 and SC-002 numbers. Open PR-B.

---

## Phase 8: Submodule repos (OD-3, serves US1 and US5) (PR-C)

**Goal**: the website and module repos each work when cloned on their own (FR-017).

**Independent Test**: quickstart §6.

- [ ] T024 [P] [US1] In `GameplanePanel/website` (branch `claude/project-thread-427u3v`), add `system-deps.toml` from the T001 brief.
- [ ] T025 [P] [US1] In `GameplanePanel/module` (branch `claude/project-thread-427u3v`), add `system-deps.toml` from the T001 brief.
- [ ] T026 [US1] From the main repo, run `make devcontainers` (with `--submodules`) to write `website/.devcontainer/*` and `modules/.devcontainer/*` (devcontainer.json, lock, a copied `setup.sh`, setup.env, ready.txt), and commit them in each submodule repo.
- [ ] T027 [P] [US5] Add a dev container build job to `website/.github/workflows/ci.yaml` (amd64 and arm64, `devcontainers/ci` SHA-pinned, runs `npm run check`).
- [ ] T028 [P] [US5] Add a dev container build job to the module repo's CI workflow (amd64 and arm64, runs the validate command from T001).
- [ ] T029 [US5] Open the website and module PRs to `main`. After both merge, bump both submodule pointers in the main repo (`git add website modules`) on a fresh branch (memory: pointer bumps only after submodule PRs merge), and confirm the main-repo `drift` job checks the submodule copies.

---

## Phase 9: User Story 4 - Find the right profile (Priority: P3) (PR-D)

**Goal**: one profile table and the steps to add a profile (FR-016).

**Independent Test**: quickstart §7.

- [ ] T030 [P] [US4] Add a "Dev container profiles" section to `docs/contributing.md`: a table (name, size, scope, what it installs, typical setup time from the PR-B job summary, when to use it), how to pick one in VS Code, Codespaces and the CLI, how to add a component or change a version (edit inputs, then `make devcontainers`), and what a `need:` message means.
- [ ] T031 [P] [US4] Update `docs/dependencies.md` so the devcontainer pin lines point to `.devcontainer/versions.toml`, and drop the stale "built from source" note if T001 shows it is out of date.
- [ ] T032 [P] [US4] Update `CLAUDE.md` "Repository map" (`.devcontainer/` line, `system-deps.toml` mention) and "Commands" (`make devcontainers`, `make check-devcontainers`).
- [ ] T033 [US4] In `GameplanePanel/website`, edit `src/content/docs/local-development-setup.mdx`: replace the `.devcontainer/post-create.sh` reference with the profile list and a link to the `docs/contributing.md` table. Text only, no new visual component (Principle II exempt, plan). Open a website PR, then bump the pointer after it merges.

---

## Phase 10: Polish & cross-cutting (PR-D)

- [ ] T034 Remove any leftover reference to `post-create.sh` repo-wide (`grep -rn post-create`), excluding `specs/done_*`.
- [ ] T035 Run quickstart §1–§7 against the merged state and record the results (times, disk, pass/fail) in `specs/026-modular-dev-containers/quickstart.md` under a "Results" heading.
- [ ] T036 Archive once every task is done and the last PR is merged: `git mv specs/026-modular-dev-containers specs/done_026-modular-dev-containers` and update in-repo references in the same commit (CLAUDE.md rule 16).

---

## Dependencies & execution order

- Phase 1 → Phase 2 → Phase 3 (US6) → Phase 4 (US3) = **PR-A**. US6 comes first because every profile is generated from the lists.
- **PR-B** (Phases 5–7) needs PR-A merged. US1 and US2 are independent of each other once PR-A is in. US5's matrix (T021) needs the profiles from T015 and T019 to exist.
- **PR-C** (Phase 8) needs PR-A (the generator) and can run in parallel with PR-B.
- **PR-D** (Phases 9–10) needs PR-B and PR-C merged, so the table can quote real setup times.

## Parallel examples

```text
Workflow PR-A (haiku): T001 scout → [T002, T003] → T004 → T005 → T006 → T007; T008 and T009 in parallel with T005–T007; then T011 (script), T012, T013, T014. Review: sonnet on the PR-A diff against this tasks list and the contracts.
Workflow PR-B (haiku): T015, T016, T019 in parallel → T017, T018, T020 → T021, T022, T023. Review: sonnet.
Workflow PR-C (haiku): T024, T025 in parallel → T026 → T027, T028 in parallel → T029. Review: sonnet.
Workflow PR-D (haiku): T030, T031, T032 in parallel → T033 → T034 → T035. Review: sonnet.
```

## Implementation strategy

- **MVP** = PR-A plus the component half of PR-B (US6 + US3 + US1). Contributors can then open a small profile, and every component's needs are written down.
- Then the parts (US2), the full CI matrix (US5), the submodule repos, and the docs.
- At each checkpoint, stop and confirm CI is green before opening the next PR (Principle VI).
