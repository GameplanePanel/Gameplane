# Research: 024 Documentation Refresh, Feature Catalog, and Roadmap Status

All decisions below rest on the spec, its Clarifications (2026-10-12) and the settled entries in [OPEN-DECISIONS.md](./OPEN-DECISIONS.md). Facts were read from `master` at `798b295` and the website `main` at `87b64a9` on 2026-10-12.

## R1. Feature catalog format

- **Decision**: `FEATURES.md` at the repo root has one `##` heading per area and, under each, one GitHub-flavoured Markdown table with the columns `Capability | Maturity | Default | Description | Docs`. One row per capability (Clarification Q3). Full grammar in [contracts/features-catalog.md](./contracts/features-catalog.md).
- **Rationale**: Tables render on GitHub and in Astro MDX unchanged (the website already uses GFM tables, e.g. `src/content/docs/comparison.mdx`), they are easy to parse line by line from a shell script, and 100 to 150 rows stay scannable when grouped by area.
- **Alternatives considered**: bullet lists (harder to parse labels reliably); a YAML/JSON data file rendered into both places (adds a generator and build step neither repo has; the user chose a CI check, not generation); one file per area (more link targets, nothing gained).

## R2. What the sync check compares

- **Decision**: For each row the check compares the tuple *(area heading, capability name, maturity, default)* between `FEATURES.md` and `website/src/content/docs/features.mdx`, in order. It does **not** compare the Description or Docs columns.
- **Rationale**: FR-015 names names, areas and labels. Docs links must differ (repo-relative `docs/x.md` versus website `./x/` links per the website CLAUDE.md), and descriptions may be lightly reworded for the site's MDX components; comparing them would force byte-identical files and fail on every legitimate link difference.
- **Alternatives considered**: whole-file diff after link rewriting (brittle); comparing names only (would miss a maturity label drifting, the main risk).

## R3. Where the sync check runs

- **Decision**: In the main repo as `hack/check-features-sync.sh`, reading the website page through the `website/` submodule. The `docs and repository checks` job in `.github/workflows/ci.yaml` gains a step that initialises only the `website` submodule (`git submodule update --init website`) before running the check. The `docs` path filter gains `FEATURES.md`, `website`, and the new hack files.
- **Rationale**: The main repo already holds both sides at a pinned revision (submodule pointer), so the check is deterministic, needs no network fetch of a moving branch, and runs where the other doc gates run. The website is a public repo, so the checkout needs no extra token.
- **Alternatives considered**: a check in the website repo that downloads `FEATURES.md` from `master` (network-dependent, compares against a moving target, and the website has no hack/ tooling); checking out all submodules (`modules` is large and not needed).
- **Consequence**: the website PR carrying `features.mdx` must merge first; the main repo PR then bumps the `website` pointer in the same change that adds `FEATURES.md` and the check. This matches the project rule that a pointer bump happens only after the submodule PR merges.

## R4. Sync check implementation and tests

- **Decision**: Bash plus `awk`, matching `hack/check-links.sh` and `hack/check-doc-versions.sh`. Fixture tests in `hack/test-check-features-sync.sh` with fixtures under `hack/testdata/check-features-sync/{pass,fail}/`, following the `hack/test-check-doc-versions.sh` harness (each `pass/` case exits 0, each `fail/` case exits non-zero with output matching its `.expected` file). Paths are overridable by env vars (`FEATURES_MD`, `FEATURES_MDX`) so fixtures can point at test files. Makefile targets `check-features-sync` and `test-features-sync`; `check-features-sync` joins the `lint` target.
- **Rationale**: Same language, harness shape and override pattern reviewers already know; no new runtime dependency in the docs job.
- **Alternatives considered**: Python (as `hack/ci_scope.py`): fine too, but the doc gates are all Bash and the job does not set up Python.

## R5. Evidence behind each maturity label

- **Decision**: The rule from Clarification Q1 is applied with these sources:
  - *Shipped*: present on `master` (code, chart, module repo).
  - *CI end-to-end coverage*: a test in `test/e2e/` that is registered in a bucket in `test/e2e/buckets.sh` (tests excluded from every bucket do not count).
  - *On by default*: `charts/gameplane/values.yaml` defaults and the module/CRD defaults; anything needing an opt-in value, a build tag (`-tags postgres`) or a separately installed component is off by default.
  - *Experimental*: the docs already label it experimental (today: the PostgreSQL driver).
  - Planned / in progress: listed on the roadmap; never given a shipped label.
- Each row's evidence (e2e test name or "none", default source) is recorded in the review list `feature-evidence.md` in this spec folder, so a reviewer can check any label (SC-001).
- **Alternatives considered**: maintainer judgment per row (not reproducible).

## R6. Building the capability inventory

- **Decision**: Inventory from these sources, deduplicated into capability rows: README Features and Mod Management, every `docs/*.md`, every website docs page, `CHANGELOG.md` (0.2.x, 0.3.0, Unreleased), finished `specs/done_*` folders, dashboard routes (`web/src/router/tree.tsx`), CRDs (`operator/api/v1alpha1/`), Helm values, API routes (`api/cmd/main.go`), the module catalog (29 directories in `GameplanePanel/module`) and the `gp-module` CLI commands. Areas: servers and lifecycle; console and admin actions; files; players; mods; backups and restore; game modules and catalog; module authoring; networking and tunnels; idle sleep and wake; multi-cluster; users, auth and access; audit and security; notifications; telemetry; observability and admin; localization; developer and operator tooling.
- **Rationale**: Covers every place a feature can be described or implemented, which is what SC-001 measures against.
- **Execution**: per CLAUDE.md rules 13 and 18, one scout per area at `haiku` writes rows plus evidence; one `sonnet` reviewer checks the combined diff against the sources.

## R7. Roadmap status sources

- **Decision**: Status date = the day the main docs PR opens. Open specs are those without the `done_` prefix on `master`; their status comes from their `tasks.md` counts and notes plus their open PRs. Snapshot on 2026-10-12 (to be refreshed at PR time):

  | Spec | Tasks done on master | Plain status |
  |---|---|---|
  | 002 Nuclear Option + IP pool | 136 / 143 | Shipped except 7 join tests waiting for a real-client packet capture |
  | 009 Dependabot remediation | 80 / 82 | Blocked: TypeScript 7 waits on `@typescript-eslint` accepting TS 7 (Dependabot #272) |
  | 013 Test coverage | 11 / 62 | In progress: setup merged, CI gate PRs open |
  | 019 Language packs | 0 / 105 on master | In progress on an open PR; French translation and the language picker still to come |
  | 021 Settings / game config | 20 / 24 | The 4 open tasks (T017–T019, T024) were built in PR 652 but not ticked; verify, tick and archive |
  | 023 Change-based test selection | 0 / 52 | Specified and planned; implementation not started |

- `v0.3.0` content comes from its CHANGELOG section; "since v0.3.0" comes from `[Unreleased]`.
- Stale roadmap items are corrected, e.g. "Game module template specifications (planned)" is re-checked against the module repo's `specs.md` files.

## R8. Existing doc gates must cover the new files

- **Decision**: Add `FEATURES.md` to the fixed audited-file lists in `hack/check-links.sh` and `hack/check-doc-versions.sh` (and their header comments), and to the `docs` path filter in `ci.yaml`. This only widens what is checked; no rule is relaxed (FR-011).
- **Rationale**: Both scripts check a fixed list of 18 files; without this, `FEATURES.md` links and version strings would go unchecked.

## R9. Stale-fact sweep method

- **Decision**: A fixed list of fact classes is grepped across the main repo docs, component READMEs, website docs and module README: module count (29), mod registry count (10), built-in role names, release/version strings, "beta" status wording versus "pre-v1 release", optional-component defaults, removed games (Hell Let Loose), component counts (15 Go modules in `GO_MODULES`, 16 in `go.work`), and dashboard page names. Each finding is recorded in `stale-facts.md` in this spec folder (page, old value, new value, source), satisfying FR-012. Already found: README and `docs/comparison-sources.md` say 30 templates (actual 29); the website roadmap headline says "Beta status".
- Competitor columns of the comparison table are not touched (FR-014). `CHANGELOG.md` is not rewritten.

## R10. Delivery order and pull requests

- **Decision**:
  1. Spec docs PR (spec, plan, tasks) to `master`, as for specs 019 and 023.
  2. Module repo PR, only if its README is stale.
  3. Website PR: `features.mdx` (new, section `reference`, group `releases-roadmap`), roadmap page, stale-fact fixes. Text only (OD-3).
  4. Main repo PR after 2 and 3 merge: `FEATURES.md`, `docs/roadmap.md`, README, `docs/`, component READMEs, sync check + fixtures + Makefile + CI wiring, and the `website`/`modules` pointer bumps.
- **Rationale**: The sync check in PR 4 needs the merged website page behind the bumped pointer.
- **Coordination**: spec 023 is changing CI job selection (`hack/ci_scope.py`, `ci.yaml`). Whichever lands second maps the new hack files and `FEATURES.md` into the other's structure.

## R11. Principle I (E2E-tested delivery)

- **Decision**: No Go e2e test (OD-4, valgul 2026-10-12). The spec takes the documented docs exception spec 012 used; verification is the sync check's fixture tests plus the existing doc gates in CI.
- **Alternatives considered**: a `test/e2e/` test running the sync check, as spec 023 OD-4 required for CI-only work; rejected by the ruling because the check compares two text files and has no cluster behaviour to exercise.
