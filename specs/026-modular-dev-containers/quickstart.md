# Quickstart: validating spec 026

All runs happen in CI or in a dev container host. No local test or lint suites (CLAUDE.md rule 8).

## 1. Drift and format (US5, US6)

On a PR branch, edit `api/system-deps.toml` to give `go` a literal version `"1.26"`. Expected: the `devcontainers / drift` job fails, naming the file and rule 2. Revert.

Bump `kind` in `.devcontainer/versions.toml` and run `make devcontainers`. Expected: only the profiles that include `deploy/kind` change (`dashboard-api`, `control-plane`, `full`, and their `setup.env`), and `--check` passes.

## 2. Component profile (US1)

In VS Code, run "Dev Containers: Reopen in Container", pick `Gameplane · web`. Expected:
- setup ends with the web ready message;
- `cd web && npx tsc --noEmit` passes;
- `command -v kind go helm` prints nothing;
- `make dev-up` stops with the `need:` message naming `dashboard-api`/`control-plane`.

Repeat for `Gameplane · website`: `website/` is initialised and `modules/` is empty.

## 3. Part profile (US2)

Pick `Gameplane · dashboard + API`, run `make dev-up` and then `make web-dev`. Open the forwarded 5173 port. Expected: the dashboard loads, you can log in, and the sample servers from the kind cluster appear.

## 4. Full profile parity (US3)

Compare the generated `.devcontainer/devcontainer.json` against `git show 8a67f2e:.devcontainer/devcontainer.json`. Expected: same features, ports, extensions, settings and env. The only differences are the generated `name` (`Gameplane · <title>`, T006), the `postCreateCommand` path and the three OD-5 alignments (kubectl 1.36, setup-envtest `release-0.24`, pinned helm). Open Codespaces with default options. Expected: the full profile is used.

## 5. CI build matrix (FR-014, SC-001, SC-002, SC-004)

Open the `devcontainers` workflow run of the foundation PR. Expected: every profile is green on amd64 and arm64. The job summary shows setup seconds and disk use per profile, and every component profile is at or under half the full profile's numbers.

## 6. Submodule repos (OD-3)

Clone `GameplanePanel/website` on its own and open it in a dev container. Expected: `npm run check` works. Same for `GameplanePanel/module` with its validate command. Then bump `versions.toml` in the main repo without regenerating the submodule copies. Expected: `--check` fails, naming `website/.devcontainer/devcontainer.json`.

## 7. Docs (US4)

Give `docs/contributing.md` to a new reader with three example changes: a dashboard button, a new Valheim setting, a website typo. Expected picks: `dashboard-api` (or `web` if no live data is needed), `modules`, `website`.

## Results

Recorded on 2026-10-29 against master `b07e4d4f` (PR #809, which also carries PR #810). Evidence is from CI runs; steps that need an interactive editor or a person are marked as not run.

| Section | Result | Evidence |
| --- | --- | --- |
| §1 Drift and format | Pass (CI) | The `drift` job runs `--check` and `make test-gen-devcontainers` (fixture tests for the `system-deps.toml` rules, including literal versions). It caught real drift on PR #809 run 40607607262: after master moved to Go 1.27.3, it failed naming `modules/.devcontainer/devcontainer.json` until module PR #55 merged. The manual edit-and-revert in §1 was not run. |
| §2 Component profile | Pass (CI build); interactive steps not run | Every component profile built on amd64 and arm64 and ran its checks (`devcontainers` run 40609123253). The VS Code walk-through (`npx tsc`, `command -v`, `make dev-up` printing `need:`) was not run in an editor. `hack/need.sh kind` outside a dev container prints the `need:` message naming `dashboard-api` or `control-plane` and exits 127. |
| §3 Part profile | Pass (CI build); live dashboard login not run | `dashboard-api`, `control-plane` and `modules-gp` built and ran their checks on both architectures. `make dev-up` + `make web-dev` + login was not run. |
| §4 Full profile parity | Pass | The generated `.devcontainer/devcontainer.json` matches the pre-026 file except the name, `postCreateCommand`, and the OD-5 alignments (kubectl 1.36, helm 4.3.1, setup-envtest `release-0.24`). Setup also installs `python3` (the base image has none), which the old script assumed. Go 1.27.3 and golangci-lint v2.14.0 come from master (#765) through `versions.toml`. |
| §5 CI build matrix | Builds pass; size target partly met | All 22 profile × architecture builds green. Setup time (build plus checks, minutes, amd64 / arm64): web 3.1 / 2.7, website 2.4 / 2.3, modules 3.0 / 2.7, agent 5.4 / 3.8, api 5.1 / 4.7, operator 6.9 / 4.4, go-services 7.3 / 5.0, modules-gp 5.0 / 4.2, control-plane 7.1 / 5.9, dashboard-api 9.1 / 7.4, full 11.1 / 8.4. Against "component at or under half of full" (5.6 min amd64, 4.2 min arm64): web, website, modules and agent meet it on both; api meets it on amd64 only; operator and go-services do not, because their checks compile every Go module in scope. Disk use is in each job's step summary, which the API does not expose; it was not copied here. |
| §6 Submodule repos | Pass (CI) | The website (PR #26, #27) and module (PR #53, #54, #55) repos each build their own dev container in CI and run `npm run check` / the validate syntax check. The main-repo `--check` fails on a stale submodule copy (see §1). |
| §7 Docs | Not run | Needs a new reader. The table in `docs/contributing.md` maps the three examples to `dashboard-api` or `web`, `modules`, and `website`. |
