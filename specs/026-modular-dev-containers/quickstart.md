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

Compare the generated `.devcontainer/devcontainer.json` against `git show 8a67f2e:.devcontainer/devcontainer.json`. Expected: same features, ports, extensions, settings and env. The only differences are the `postCreateCommand` path and the three OD-5 alignments (kubectl 1.36, setup-envtest `release-0.24`, pinned helm). Open Codespaces with default options. Expected: the full profile is used.

## 5. CI build matrix (FR-014, SC-001, SC-002, SC-004)

Open the `devcontainers` workflow run of the foundation PR. Expected: every profile is green on amd64 and arm64. The job summary shows setup seconds and disk use per profile, and every component profile is at or under half the full profile's numbers.

## 6. Submodule repos (OD-3)

Clone `GameplanePanel/website` on its own and open it in a dev container. Expected: `npm run check` works. Same for `GameplanePanel/module` with its validate command. Then bump `versions.toml` in the main repo without regenerating the submodule copies. Expected: `--check` fails, naming `website/.devcontainer/devcontainer.json`.

## 7. Docs (US4)

Give `docs/contributing.md` to a new reader with three example changes: a dashboard button, a new Valheim setting, a website typo. Expected picks: `dashboard-api` (or `web` if no live data is needed), `modules`, `website`.
