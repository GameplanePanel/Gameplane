---

description: "Task list for spec 027: contributor docs and layout map"
---

# Tasks: Contributor Docs and Layout Map

**Input**: Design documents from `specs/027-contributor-docs-layout-map/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: no new tests. Docs-only spec; Principle I docs exception as specs 012/024. CI doc gates (`check-links`, `check-specs`, `check-doc-versions`) verify. Tests and lint run in CI only (CLAUDE.md rule 8); locally only `git ls-files -s` checks and `npm run build` in WEB.

**Organization**: grouped by user story. Repos: **GP** = `GameplanePanel/Gameplane`, **WEB** = `GameplanePanel/website`. Paths without a prefix are in GP.

**Gate**: OD-1..OD-5 are settled in [OPEN-DECISIONS.md](./OPEN-DECISIONS.md). No task below starts until the spec docs PR has merged. Tasks marked *(OD-n)* apply that decision.

**Execution rules (every helper agent repeats these)**: follow the repo CLAUDE.md; implementation runs through one `Workflow` script with `model:` set on every `agent()` call, starting at `haiku`, one tier-up `sonnet` review of the combined diff (rules 13, 18); helpers never call `mcp__hearthbot__` tools; signed commits (`git commit -s`), one branch per repo; no source file or package moves (FR-019); never edit `.pen` files; never edit or weaken tests; only compile checks locally, never test or lint suites; PR bodies carry no session links or attribution block; PRs get `type:` + `area:` labels via REST.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependencies)
- **[Story]**: US1–US5 from spec.md

---

## Phase 1: Setup

- [ ] T001 Create branch `claude/027-contributor-docs` in GP from latest `master` (after the spec docs PR merges) and `claude/027-contributing-pointer` in WEB from latest `main`
- [ ] T002 Re-measure on the new `master` the facts the docs will state (research R1–R4: symlink modes, `AGENT.md` references, `specs.md` count, module shapes, largest web files) and record the SHA and any drift at the top of `specs/027-contributor-docs-layout-map/research.md`

---

## Phase 2: User Story 1 - First PR without asking (P1) 🎯 MVP

**Goal**: a "Your first PR" section with owning folder, compile check and CI time per change type.

**Independent test**: three sample changes answered from the guide alone (SC-001).

- [ ] T003 [US1] Write the "Your first PR" section in `docs/contributing.md` directly after the opening paragraph per [contracts/first-pr-section.md](./contracts/first-pr-section.md): 6 steps, change-type table (8 rows) with CI times and source runs from research R5 dated 2026-10-20, gates list
- [ ] T004 [US1] Add a link to the new section in the opening paragraph of `docs/contributing.md` and in `README.md` "Contributing" (line ~303)
- [ ] T005 [P] [US1] WEB: add a short "Your first PR" pointer to `src/content/docs/contributing.mdx` linking to the GitHub `docs/contributing.md#your-first-pr` (absolute GitHub URL from `src/config.ts`), no copied table; local check `npm run build`
- [ ] T006 [US1] Commit GP (`docs: add first-PR guide to contributing`) and WEB (`docs: link first-PR guide`)

---

## Phase 3: User Story 2 - One real instructions file (P1)

**Goal**: exactly one regular-file instructions doc per repo, other kept names link to it, guide says so.

**Independent test**: `git ls-files -s AGENTS.md CLAUDE.md` shows `AGENTS.md` `100644`, `CLAUDE.md` `120000`, and `AGENT.md` is gone (SC-002).

- [ ] T007 [US2] Re-run the reference search for `AGENT.md` across GP (excluding `specs/done_*`); if any hit, stop and report
- [ ] T008 [US2] *(OD-1)* Swap the instruction files in one commit: `git rm AGENTS.md` (symlink), `git mv CLAUDE.md AGENTS.md`, `ln -s AGENTS.md CLAUDE.md && git add CLAUDE.md`, `git rm AGENT.md`; check with `git ls-files -s AGENTS.md CLAUDE.md` (`100644` and `120000`) and that the content of `AGENTS.md` is byte-identical to the old `CLAUDE.md`
- [ ] T009 [US2] *(OD-1)* Rewrite the "AI-assisted development" section of `docs/contributing.md`: `AGENTS.md` is the real file in both repos, `CLAUDE.md` is a link to it, plus the Windows `core.symlinks=false` note
- [ ] T009a [US2] Commit (`docs: make AGENTS.md the single agent instructions file`)

---

## Phase 4: User Story 3 - One rule for design docs (P2)

**Goal**: written docs rule; `docs/superpowers/` retired; no stale "not yet implemented" for shipped features.

**Independent test**: no design/plan docs outside the named homes except `docs/archive/` (SC-003).

- [ ] T010 [US3] Add "Where design docs go" to `docs/contributing.md` (plan D2: change → `specs/NNN-<slug>/`; how a module works today → `<module>/specs.md`; nothing else) and one matching line under rule 15 of `AGENTS.md`
- [ ] T011 [US3] *(OD-2)* `git mv docs/superpowers docs/archive/superpowers`; update the 5 internal `docs/superpowers/` references inside the moved plans to the new paths
- [ ] T012 [US3] *(OD-2)* Prepend the historical banner to each of the 5 moved files naming the `<module>/specs.md` that now describes the shipped behavior (console/actions → `operator/specs.md` and `agent/specs.md`; multi-category modules → `operator/specs.md` and `gp-module/specs.md`; wake-on-connect → `sentinel/specs.md`); write `docs/archive/README.md`
- [ ] T013 [US3] Search GP for any remaining `docs/superpowers` path (excluding `specs/done_*`, which keep historical text) and fix it
- [ ] T014 [US3] Commit (`docs: write design-doc homes rule and archive superpowers docs`)

---

## Phase 5: User Story 4 - Layout for the split spec (P2)

**Goal**: `docs/layout.md` with kinds, an 18-row map, exceptions, enforcement.

**Independent test**: every `go.work` module and `web` has a row with kind, target and gap (SC-004).

- [ ] T015 [US4] *(OD-3, OD-4)* Write `docs/layout.md` §1 Kinds per [contracts/layout-doc.md](./contracts/layout-doc.md) and plan D3 (service: `cmd/main.go` + `internal/<concern>/`; library: flat root package; CLI: `cmd/<binary>/`; web: `pages/<Screen>.tsx`, split into `pages/<Screen>/<Section>.tsx` over 500 lines, `routes/` only exposes pages, `components/` for parts used by 2+ screens)
- [ ] T016 [US4] Write §2 Layout map (18 rows) from the T002 re-measurement; the `web` row lists each screen in `web/src/routes/` over 500 lines as a gap, stating the `master` SHA; §3 Exceptions (`test/e2e`, `operator/api/v1alpha1`, `i18n`); §5 Not decided here
- [ ] T017 [US4] *(OD-5)* Write §4 Enforcement: spec 028 adds the folder-shape check to `make lint` once every module matches; until then review enforces the doc
- [ ] T018 [US4] Add `docs/layout.md` and `docs/archive/` to the repository map in `AGENTS.md`
- [ ] T019 [US4] Commit (`docs: add target layout map for Go modules and web`)

---

## Phase 6: User Story 5 - Task-to-doc map (P3)

**Goal**: one map row per CLAUDE.md common workflow, plus the layout doc.

**Independent test**: every "Common workflows" entry has a row; links resolve (SC-005).

- [ ] T020 [US5] Add rows to "Task to doc map" in `docs/agent-architecture.md`: CRD field, API route, dashboard screen, game module, website, DB migration, CI change, where new code goes (→ `docs/layout.md`); keep the i18n row; add a `docs/layout.md` pointer under "How to use this index"
- [ ] T021 [US5] Commit (`docs: extend task-to-doc map`)

---

## Phase 7: Review and delivery

- [ ] T022 Tier-up review (`sonnet`) of the combined GP and WEB diffs against spec FR-001..FR-020 and this task list; fixes go to a new `haiku` fix wave
- [ ] T023 Push both branches; open WEB PR (`type: docs`) and GP PR (`type: docs`, `area: specs`, `area: shared`); check CI green and every CodeRabbit finding fixed or settled before calling either ready
- [ ] T024 After the WEB PR merges: bump the `website` submodule pointer in GP (`chore: bump website submodule`) in its own PR
- [ ] T025 After every PR merges: mark tasks `[X]`, `git mv specs/027-contributor-docs-layout-map specs/done_027-contributor-docs-layout-map` and update references in the same commit (rule 16); tell spec 028 that `docs/layout.md` is on `master`

---

## Dependencies

- T001–T002 before everything. US1 (T003–T006) and US2 (T007–T009) both edit `docs/contributing.md`: run them in sequence, not in parallel. US3 also edits `docs/contributing.md` and CLAUDE.md: after US2. US4 depends on T002 only, so it can run in parallel with US1–US3 apart from T018 (CLAUDE.md, after T010). US5 after US4 (links `docs/layout.md`). T022 after all stories; T024 after the WEB merge; T025 last.

## MVP

US1 alone (T001, T003–T006) delivers the first-PR guide.
