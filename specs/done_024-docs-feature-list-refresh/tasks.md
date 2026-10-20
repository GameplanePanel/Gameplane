---

description: "Task list for spec 024: documentation refresh, feature catalog and roadmap status"
---

# Tasks: Documentation Refresh, Feature Catalog, and Roadmap Status

**Input**: Design documents from `specs/done_024-docs-feature-list-refresh/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md)

**Tests**: FR-015 requires fixture tests for the new sync check; those are included (US4). No `test/e2e/` test: Principle I docs exception (OD-4). Tests and lint run in CI only (CLAUDE.md rule 8); locally only `npm run build` / `npm run check` in the website and `bash -n` syntax checks.

**Organization**: Grouped by user story. Repos: **GP** = `GameplanePanel/Gameplane`, **WEB** = `GameplanePanel/website`, **MOD** = `GameplanePanel/module`. Paths without a prefix are in GP.

**Execution rules (every helper agent repeats these)**: follow the repo CLAUDE.md; implementation runs through `Workflow` scripts with `model:` set on every `agent()` call, starting at `haiku`, one tier-up `sonnet` review of the combined diff (rules 13, 18); signed commits (`git commit -s`), one branch per repo; never edit `.pen` files; never edit or weaken existing tests; competitor columns of the comparison table and `CHANGELOG.md` history are not changed (FR-014); website internal links use `withBase()` / relative docs links; submodule pointers are bumped only after the WEB/MOD PRs merge.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1–US4)

---

## Phase 1: Setup

**Purpose**: Branches and the status snapshot every story reads from.

- [X] T001 Create branch `claude/024-docs-refresh` (used the session branch `claude/project-thread-6pzhf7` instead) in GP from latest `master` (after the spec docs PR merges) and branch `claude/024-docs-refresh` in WEB from latest `main`
- [X] T002 Record the status date and source revisions (GP `master` SHA, WEB `main` SHA, MOD `master` SHA, chart `appVersion` from `charts/gameplane/Chart.yaml`) at the top of `specs/done_024-docs-feature-list-refresh/stale-facts.md`
- [X] T003 [P] Create `specs/done_024-docs-feature-list-refresh/feature-evidence.md` with the record header from [data-model.md](./data-model.md) (area, capability, maturity, e2e test, default source, notes)

---

## Phase 2: Foundational (blocks all stories)

**Purpose**: The fact base the catalog, roadmap and sweep all use. Each task writes a section of `specs/done_024-docs-feature-list-refresh/research-notes.md` (scratch input, committed for review).

- [X] T004 [P] List every e2e test registered in a bucket: parse `test/e2e/buckets.sh` and `test/e2e/*_test.go` into a table test name → bucket; note tests excluded from all buckets (they do not count as CI coverage, research R5)
- [X] T005 [P] List install defaults: every enable/disable toggle in `charts/gameplane/values.yaml` with its default, plus build-tag features (`-tags postgres`) and separately installed components (sentinel, capture sidecar, tunnels, audit-syslog-bridge, telemetry-receiver, mcp-server, agent gateway)
- [X] T006 [P] List the module catalog: game directories in MOD (expected 29, Hell Let Loose withdrawn) and the mod registries implemented in code (expected 10: Modrinth, CurseForge, Thunderstore, Hangar, Factorio Mod Portal, Steam Workshop, SpigotMC, GitHub Releases, uMod, Nexus Mods), citing the code location of the registry list
- [X] T007 [P] Snapshot open specs: for each `specs/<NNN>-*` folder without `done_` in GP `master`, record done/open task counts from its `tasks.md`, blockers named in it, and its open PRs (research R7 table refreshed)
- [X] T008 [P] Summarise shipped work: `CHANGELOG.md` sections `[0.3.0]` and `[Unreleased]`, plus each `specs/done_*` folder's one-line outcome

**Checkpoint**: research-notes.md complete; stories can start in parallel.

---

## Phase 3: User Story 1 - Evaluator reads one complete feature list (Priority: P1) 🎯 MVP

**Goal**: Root `FEATURES.md` lists every shipped capability, one row per capability, grouped by area, with evidence-based maturity, default and docs link (FR-001–FR-004, OD-1, Clarifications Q1/Q3).

**Independent Test**: [quickstart.md](./quickstart.md) scenario 1: 10 random rows each have a matching evidence record, a label that obeys the data-model validation, and a docs link that documents the capability.

- [X] T009 [US1] Write the `FEATURES.md` skeleton per [contracts/features-catalog.md](./contracts/features-catalog.md): title, intro explaining the labels (stable / beta / experimental / planned / in progress, and the on/off/n/a default column), status date, link to `docs/roadmap.md`, and the 18 area `##` headings from research R6, each with the catalog table header
- [X] T010 [P] [US1] Inventory rows for areas *Servers and lifecycle*, *Console and admin actions*, *Files*, *Players* from `web/src/router/tree.tsx`, `web/src/routes/`, `api/cmd/main.go`, README Features, `docs/architecture.md` and website pages `server-operations.mdx`, `console-events-logs.mdx`, `file-manager.mdx`, `player-administration.mdx`; write rows and evidence records to `specs/done_024-docs-feature-list-refresh/catalog-drafts/servers-console-files-players.md`
- [X] T011 [P] [US1] Inventory rows for *Mods*, *Backups and restore*, *Game modules and catalog*, *Module authoring* from README Mod Management, `docs/module-authoring.md`, `gp-module/`, `operator/api/v1alpha1/` (ModuleSource, GameTemplate, Backup*, Restore), website `mods-modpacks.mdx`, `backups-recovery.mdx`, `modules-sources.mdx`; write rows and evidence records to `specs/done_024-docs-feature-list-refresh/catalog-drafts/mods-backups-modules-authoring.md`
- [X] T012 [P] [US1] Inventory rows for *Networking and tunnels*, *Idle sleep and wake*, *Multi-cluster* from `docs/networking.md`, `docs/tunnels.md`, `docs/multicluster-*.md`, `docs/unified-dashboard.md`, `docs/remote-parity.md`, `sentinel/`, `tunnel/`; write rows and evidence records to `specs/done_024-docs-feature-list-refresh/catalog-drafts/networking-sleep-multicluster.md`
- [X] T013 [P] [US1] Inventory rows for *Users, auth and access*, *Audit and security*, *Notifications*, *Telemetry* from `docs/oidc.md`, `docs/security.md`, `docs/notifications.md`, `docs/telemetry-provider.md`, `docs/key-rotation.md`, `api/internal/rbac/`, `audit-syslog-bridge/README.md`, `telemetry-receiver/README.md`; write rows and evidence records to `specs/done_024-docs-feature-list-refresh/catalog-drafts/access-audit-notify-telemetry.md`
- [X] T014 [P] [US1] Inventory rows for *Observability and admin*, *Localization*, *Developer and operator tooling* from `docs/install.md`, `docs/gateway-install.md`, `mcp-server/README.md`, `capture-sidecar/`, `charts/gameplane/`, website `metrics-alerts-dashboards.mdx`, `system-log-viewer.mdx`; localization rows reflect only what is merged on `master` (spec 019 work is `in progress`, FR-004); write rows and evidence records to `specs/done_024-docs-feature-list-refresh/catalog-drafts/observability-l10n-tooling.md`
- [X] T015 [US1] Merge the five `catalog-drafts/*.md` files into `FEATURES.md` (rows under their area headings) and `feature-evidence.md` (single owner of both files; delete `catalog-drafts/` afterwards), then apply the maturity rule to every row using T004/T005 facts: `stable` only with `default = on` and a bucketed e2e test; `default = off` rows are `beta` or `experimental`; `experimental` only where docs already say so (PostgreSQL driver); planned/in-progress rows get `default = n/a` and a roadmap link; fill each evidence record (`FEATURES.md`, `feature-evidence.md`)
- [X] T016 [US1] Deduplicate and order rows within each area, check every Docs link target exists in GP, and confirm the row count is in the 100–150 range or note why not in `feature-evidence.md`
- [X] T017 [US1] Tier-up review (`sonnet`): check the combined `FEATURES.md` + `feature-evidence.md` diff against research-notes.md and the sources; fixes go to a new `haiku` fix wave

**Checkpoint**: `FEATURES.md` draft complete on the GP branch (not yet merged).

---

## Phase 4: User Story 2 - Current roadmap status (Priority: P1)

**Goal**: `docs/roadmap.md` states current release, status date, what shipped in v0.3.0 and since, one line per open spec, blockers, v1 items and out-of-scope (FR-005, FR-006, FR-013, OD-2).

**Independent Test**: quickstart scenario 2: every non-`done_` spec folder has a roadmap line whose status matches its `tasks.md` on the status date.

- [X] T018 [US2] Restructure `docs/roadmap.md` into the section order from [data-model.md](./data-model.md) (current release + status date; shipped in v0.3.0 and since; in progress; blocking v1; wanted for v1; known gaps; out of scope; join protocol coverage), keeping existing `<!-- doc-versions: historical -->` markers on historical headings
- [X] T019 [US2] Add the "Shipped in v0.3.0" and "Since v0.3.0" entries to `docs/roadmap.md` from T008, each linking `CHANGELOG.md`
- [X] T020 [US2] Add the "In progress" section to `docs/roadmap.md` with one line per open spec from T007 (002, 009, 013, 019, 021, 023 unless the snapshot differs): plain-words status, blocker and what unblocks it
- [X] T021 [US2] Re-check every existing "planned" / "known gap" item in `docs/roadmap.md` against `master` and MOD (e.g. "Game module template specifications" against `modules/<name>/specs.md` presence; Users page service-accounts / IdP tabs against `web/src/routes/Users.tsx`); move shipped items to the shipped section with their release, correct or remove stale ones, and log each change in `stale-facts.md`
- [X] T022 [US2] Update the join-coverage counts in the roadmap's last section from `docs/game-coverage.md` so they total 29 modules

**Checkpoint**: repo roadmap complete on the GP branch.

---

## Phase 5: User Story 3 - Docs match the product (Priority: P2)

**Goal**: README, `docs/`, component READMEs and the MOD README carry no stale facts; README Features becomes a summary linking `FEATURES.md` (FR-007, FR-008, FR-010, FR-012, FR-014).

**Independent Test**: quickstart scenarios 3 and 8: no stale counts remain, every fix is in `stale-facts.md`, README links the catalog, competitor columns unchanged.

- [X] T023 [US3] Grep GP (`README.md`, `docs/**/*.md` except `docs/superpowers/`, component READMEs) and MOD `README.md` for each fact class in research R9 (module count, registry count, role names, release/status wording, optional defaults, Hell Let Loose, Go module counts, page names); write every hit with its correct value and source into `stale-facts.md`
- [X] T024 [US3] Fix `README.md`: "30" → "29" templates in the Features list and the comparison table's Gameplane column, status wording, and replace the Features section with a short summary plus a link to `FEATURES.md`; leave competitor columns untouched
- [X] T025 [P] [US3] Fix `docs/comparison-sources.md` Gameplane-row facts (the "30 ready-to-use templates" claim → 29) without changing any competitor entry or its dated source
- [X] T026 [P] [US3] Apply the remaining `stale-facts.md` fixes in `docs/*.md` (one commit per logical group)
- [X] T027 [P] [US3] Apply the `stale-facts.md` fixes in `audit-syslog-bridge/README.md`, `containers/README.md`, `mcp-server/README.md`, `telemetry-receiver/README.md`
- [X] T028 [US3] MOD: if `README.md` module list or count is stale, branch `claude/024-readme-catalog` in MOD, fix it, open the MOD PR (signed commits); otherwise record "MOD README current" in `stale-facts.md`
- [X] T029 [US3] Add `FEATURES.md` to the audited-file lists and header comments in `hack/check-links.sh` and `hack/check-doc-versions.sh`, and to the `docs` path filter in `.github/workflows/ci.yaml` (research R8; widens coverage, relaxes nothing)
- [X] T030 [US3] Tier-up review (`sonnet`) of the combined US2 + US3 GP diff against `stale-facts.md` and research-notes.md; fixes in a new `haiku` wave

**Checkpoint**: GP docs refreshed on the branch.

---

## Phase 6: User Story 4 - Website shows the same information (Priority: P2)

**Goal**: WEB features page mirrors `FEATURES.md`, roadmap page mirrors `docs/roadmap.md`, stale facts fixed in docs pages only (FR-009, OD-3); GP sync check enforces catalog parity in CI (FR-015, Clarification Q2).

**Independent Test**: quickstart scenarios 4, 5 and 7: website builds with the Features page in Reference → Releases & Roadmap; `make test-features-sync` and `make check-features-sync` pass in CI; roadmaps agree.

### Website PR (WEB, merges before the GP PR)

- [X] T031 [US4] Create `src/content/docs/features.mdx` in WEB with the frontmatter from [contracts/features-catalog.md](./contracts/features-catalog.md) and the same area headings and tables as `FEATURES.md` (Capability, Maturity, Default identical; Docs cells rewritten to relative website docs links per the WEB CLAUDE.md); depends on T017
- [X] T032 [US4] Rewrite `src/content/docs/roadmap.mdx` in WEB to the same items and statuses as `docs/roadmap.md` (replace the "Beta status" headline with the current pre-v1 release wording); keep existing components (`DocsCallout`, `IconList`, `VERSION`); depends on T022
- [X] T033 [P] [US4] Grep WEB `src/content/docs/*.mdx` and `src/config.ts` for the R9 fact classes, add hits to `stale-facts.md`, and fix them in the docs pages (no landing page or component changes, OD-3)
- [X] T034 [US4] Run `npm run build` and `npm run check` in WEB (allowed locally); fix build errors only
- [X] T035 [US4] Open the WEB PR to `main` (signed commits), get WEB CI (`lint`, `check`, `build`) green, and wait for it to merge

### Sync check (GP)

- [X] T036 [P] [US4] Write fixtures in `hack/testdata/check-features-sync/` per [contracts/features-sync-check.md](./contracts/features-sync-check.md): `pass/in-sync`, `pass/code-fence`, and `fail/{missing-row-website,missing-row-repo,maturity-mismatch,default-mismatch,area-mismatch,order-differs,invalid-label,stable-off,missing-file}` each with `.expected` output
- [X] T037 [P] [US4] Write `hack/check-features-sync.sh` (Bash + awk, `set -euo pipefail`, env overrides `FEATURES_MD` / `FEATURES_MDX`, exit 0/1/2 and messages exactly as the contract), header comment citing spec 024 FR-015
- [X] T038 [US4] Write `hack/test-check-features-sync.sh` following the `hack/test-check-doc-versions.sh` harness (pass cases exit 0; fail cases exit non-zero with output equal to `.expected`); check both scripts with `bash -n` only
- [X] T039 [US4] Add Makefile targets `check-features-sync` and `test-features-sync` (with `##` help text) and add `check-features-sync` to the `lint` target in `Makefile`
- [X] T040 [US4] After T029 (both edit the `docs` path filter; extend T029's list, never replace it), in `.github/workflows/ci.yaml` `docs` job: add a step `git submodule update --init website` before the checks, then steps running `make check-features-sync` and `make test-features-sync`; add `FEATURES.md`, `website`, `hack/check-features-sync.sh`, `hack/test-check-features-sync.sh`, `hack/testdata/check-features-sync/**` to the `docs` path filter; if spec 023's selection script has landed, map the same paths to the docs job in `hack/ci_scope.py`
- [X] T041 [US4] Tier-up review (`sonnet`) of T036–T040 against the contract; fixes in a new `haiku` wave

**Checkpoint**: WEB merged; sync check ready on the GP branch.

---

## Phase 7: Polish & delivery

- [X] T042 Bump the `website` submodule pointer to the merged WEB commit (and `modules` if T028 merged a MOD PR) on the GP branch; after the bump, copy any final `FEATURES.md` edits into `features.mdx` via a WEB follow-up PR before bumping again, so the two stay identical
- [X] T043 Open the GP PR (signed commits, labels `type: docs`, `area: specs`, plus `area: shared` for the hack/CI change), body without attribution block or session links; drive CI green, including `check-links`, `check-doc-versions`, `check-features-sync`, `test-features-sync`
- [X] T044 Run quickstart scenarios 1–8 against the merged state and record results in `specs/done_024-docs-feature-list-refresh/stale-facts.md` (final section)
- [X] T045 After the GP PR merges: tick all tasks, `git mv specs/done_024-docs-feature-list-refresh specs/done_024-docs-feature-list-refresh`, update in-repo references, in its own `docs:` commit and PR (Constitution IV, CLAUDE.md rule 16)

---

## Dependencies & Execution Order

- **Setup (T001–T003)** → **Foundational (T004–T008)** → stories.
- **US1 (T009–T017)** and **US2 (T018–T022)** can run in parallel after Foundational.
- **US3 (T023–T030)** can start after Foundational; T024 needs `FEATURES.md` to exist (T009) for the link.
- **US4 website (T031–T035)** needs US1 (T017) and US2 (T022) done; T033 can start any time after Foundational.
- **US4 sync check (T036–T041)**: T036–T039 need only the contract and can run in parallel with everything else; T040 runs after T029 because both edit the `docs` path filter in `ci.yaml`; T041 reviews T036–T040.
- **Shared files**: T010–T014 each write their own `catalog-drafts/*.md` file; only T015 writes `FEATURES.md` and `feature-evidence.md`.
- **Polish (T042–T045)** needs the WEB PR merged (T035) and all GP work done.

Story order by value: US1 → US2 → US3 → US4. Delivery order is fixed by the submodule rule: MOD PR (if any) and WEB PR merge first, then the GP PR.

## Parallel Example

```text
After T008:
  Workflow wave A (haiku): T010, T011, T012, T013, T014   # catalog areas, separate sections
  Workflow wave B (haiku): T018–T022 (roadmap, one agent), T023 (grep sweep), T033 (website grep)
  Workflow wave C (haiku): T036, T037                      # fixtures and script
Then one sonnet review per wave (T017, T030, T041).
```

## Implementation Strategy

- **MVP**: Phases 1–3 (US1): a reviewed `FEATURES.md` with evidence. It is useful on its own and everything else links to it.
- **Increment 2**: US2 roadmap.
- **Increment 3**: US3 stale-fact sweep and doc-gate coverage.
- **Increment 4**: US4 website mirror, then the sync check lands in the GP PR together with the pointer bump.
- One GP PR for all GP changes (plus the separate archive PR), one WEB PR, at most one MOD PR (research R10).
