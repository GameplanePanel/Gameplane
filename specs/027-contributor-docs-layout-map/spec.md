# Feature Specification: Contributor Docs and Layout Map

**Feature Branch**: `claude/project-thread-yulzhu`

**Created**: 2026-10-20

**Status**: Ready for implementation

**Input**: User description: "Write a speckit spec (spec, plan, tasks) for the Gameplane v1 cleanup 'contributor docs and layout map'. Scope: one canonical agent-instructions file (with what the others become), one home for design/spec docs (with what happens to the existing ones), a written target layout for Go modules and web that the split spec will apply, and a first-PR guide. Docs and structure rules only. No code moves in this spec."

## Context

v1 is all 20 audit issues (#712–#731) plus codebase cleanup, with no new features (maintainer, 2026-10-20). This spec is one of three cleanup specs. The second one, "split big packages and files", runs after this one and MUST follow the layout this spec writes down. The third, "speed up slow e2e buckets", waits for spec 023 and does not depend on this one.

Facts measured on `master` at `8a67f2e` (assessment: project note `notes/v1-readiness/2026-10-20-assessment.md`), re-checked while writing this spec:

- **Agent instruction files.** The assessment reported `AGENT.md`, `AGENTS.md` and `CLAUDE.md` as byte-identical copies. They are not copies: git stores `AGENT.md` and `AGENTS.md` as symlinks (mode `120000`) to `CLAUDE.md`, which is why the three hash the same. The duplication problem is therefore smaller than reported. What is still untidy: (a) the website submodule makes the opposite choice (`AGENTS.md` is the real file, `CLAUDE.md` links to it); (b) nothing in the repo, its tooling, or `.specify/` refers to `AGENT.md`; (c) no contributor doc says which file is the real one, so an edit "to AGENTS.md" lands in `CLAUDE.md` by surprise.
- **Design and spec material.** It lives in three places, but they do not all play the same role:
  1. `specs/NNN-<slug>/` and `specs/done_NNN-<slug>/`: speckit feature specs (what a change should do). Governed by constitution Principle IV and CLAUDE.md rules 15–16.
  2. 19 per-component `specs.md` files (the brief counted 21, which included two frozen copies inside `specs/done_018-…/audit/`) (`api/specs.md`, `web/specs.md`, `test/e2e/specs.md`, …; 67 to 1,274 lines): living reference docs that describe how each component works today. Constitution Principle IV requires one per module and `hack/check-specs.sh` (part of `make lint`) enforces it. `docs/agent-architecture.md` routes to them.
  3. `docs/superpowers/{plans,specs}/`: 5 files from July 2026 (3 plans with 103 unticked steps, 2 designs marked "approved, not yet implemented"). The features they describe have shipped (console modes, multi-category modules, action stdin, wake-on-connect via `sentinel/`), so their status lines are stale. They reference only each other.
- **Contributor guide.** `docs/contributing.md` (224 lines) covers dev loop, design-first, CI, testing, submitting and release, but has no "your first PR" path: which folder to touch for a given change, which `make` target compiles it, and how long CI takes to answer. Measured CI wall clock: docs-only PR 1.8 min, spec-only PR 5.3 min, web-only PR 18.3 min, any Go change median 37.5 min (29 to 75). The website has its own `contributing.mdx` page.
- **No written target layout.** Go modules use four shapes today:
  - `cmd/main.go` + `internal/<pkg>/`: `api`, `operator`, `agent`, `capture-sidecar`.
  - `cmd/<binary>/` + packages: `gp-module`, `i18n` (`cmd/i18n-check/`).
  - Root `package main`, one large file: `tunnel/main.go` 1,052 lines, `sentinel/main.go` 1,017 lines.
  - Root `package main`, many flat files: `telemetry-receiver` (12 source + 25 test files at the root), `audit-syslog-bridge` (3), `mcp-server` (3 + `internal/`).
  - Libraries (`netguard`, `gameaction`, `gameproto`, `svcutil`, `telemetryschema`) are a flat root package, which is normal for a library.
  The web app has `src/routes/` (49 flat files, screens up to 2,478 lines, plus `routes/tabs/`), `src/components/` (flat files plus `backups/`, `modules/`, `server/`, `settings/`, `ui/` folders), `src/lib/`, two type files (`src/api/types.ts` generated, `src/types.ts` hand-written).
- **Task-to-doc map.** `docs/agent-architecture.md` (53 lines) maps components to their `specs.md`, and has one "task to doc" row (i18n).

## Clarifications

### Session 2026-10-20

- Q: Which agent instructions file is canonical? → A: `AGENTS.md` in both repos; main repo `CLAUDE.md` becomes a link to it; `AGENT.md` deleted (OD-1).
- Q: What happens to `docs/superpowers/`, and do `specs.md` files keep their name? → A: archive to `docs/archive/superpowers/` with a banner; `specs.md` names kept (OD-2).
- Q: Folder shape for long-running Go services? → A: thin `cmd/main.go` + `internal/<concern>/`; libraries flat; CLIs `cmd/<binary>/` (OD-3).
- Q: Where does a big dashboard screen's code live? → A: `pages/<Screen>.tsx`, split into `pages/<Screen>/<Section>.tsx` over 500 lines; `routes/` only exposes pages; `components/` for parts used by 2+ screens (OD-4).
- Q: Does CI enforce the layout? → A: spec 028 adds the check once modules conform; none in this spec (OD-5).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - New contributor opens a first PR without asking (Priority: P1)

A first-time contributor wants to fix a small thing (a typo in a doc, a web label, a bug in one Go service). They open the contributing guide, find a "Your first PR" section, see a table that tells them which folder owns the change, which `make` target compiles it locally, which CI jobs will run and roughly how long CI takes for that kind of change, and the PR rules (signed commits, labels, no local test or lint runs). They open a PR that passes the label and sign-off gates on the first try.

**Why this priority**: it is the most direct contributor-experience gain and does not depend on any decision about files or layout.

**Independent Test**: give the guide to someone who has not worked in the repo and three sample changes (docs typo, web label, Go service bug); they can name the folder, the compile command and the expected CI wait for each using only the guide.

**Acceptance Scenarios**:

1. **Given** the contributing guide, **When** a reader looks for how to start, **Then** a "Your first PR" section is reachable from the guide's top and from the README's contributing link.
2. **Given** a change type in the first-PR table, **When** the reader follows its row, **Then** the row names the owning folder, the local compile command, and the measured CI wall-clock time with the date it was measured.
3. **Given** the website's contributing page, **When** a reader opens it, **Then** it links to the same first-PR section instead of repeating it.

---

### User Story 2 - Contributor and AI agent know which instructions file is real (Priority: P1)

A human or AI agent wants to change the house rules. They find one real instructions file, edit it, and every tool that reads a different name (`AGENTS.md`, `CLAUDE.md`) sees the same text. A contributor doc says which file is real and that the others are links.

**Why this priority**: every AI-assisted change starts by reading this file; a wrong edit target or a drifted copy silently changes the rules agents follow.

**Independent Test**: list the instruction files in the repo root and the website root; exactly one is a regular file per repo (per the recorded decision), the rest are links or removed, and the contributing guide names the real one.

**Acceptance Scenarios**:

1. **Given** the repo root, **When** listing instruction files, **Then** exactly one is a regular file and every other kept name is a link to it.
2. **Given** a name the decision removes, **When** searching the repo for references to it, **Then** none remain.
3. **Given** the contributing guide's AI-assisted development section, **When** a reader reads it, **Then** it names the canonical file and what the other names are.

---

### User Story 3 - Author knows where a design or spec doc goes (Priority: P2)

Someone writing a design for a new feature, or updating how a component works, finds one written rule: feature specs go in `specs/NNN-<slug>/` through speckit; how a component works today goes in that component's reference doc. Old design docs outside those homes are moved or archived per the recorded decision, so nobody finds a stale "not yet implemented" design for a shipped feature.

**Why this priority**: it stops new material from landing in a fourth place, but the stale docs cause less harm than the first-PR gap.

**Independent Test**: search the repo for design or plan docs outside the homes the rule names; none remain except the ones the rule explicitly allows.

**Acceptance Scenarios**:

1. **Given** the docs rule, **When** an author asks "where does a new design go", **Then** the contributing guide and CLAUDE.md give the same one answer.
2. **Given** `docs/superpowers/`, **When** this spec is done, **Then** its five files are handled per the recorded decision and nothing links to their old paths.
3. **Given** the per-component reference docs, **When** this spec is done, **Then** their role is written down next to the feature-spec role, so the two are not mistaken for competing homes.

---

### User Story 4 - The split spec has a layout to apply (Priority: P2)

The author of the later "split big packages and files" spec opens one layout document that says, for each kind of Go module (service, library, CLI) and for the web app, which folders exist, what goes in each, where tests live, and which current modules already match. They can write their tasks as "move module X from shape A to the target shape" without making layout decisions of their own.

**Why this priority**: it blocks the split spec, but nothing in v1 runs faster or safer until that spec applies it.

**Independent Test**: for every module in `go.work` and for `web/src`, the layout doc says which target shape applies and whether the module already matches it today.

**Acceptance Scenarios**:

1. **Given** the layout doc, **When** a reader looks up any of the 17 Go modules or `web`, **Then** it names its kind, its target shape and its current gap (or "matches").
2. **Given** a new Go service added after this spec, **When** its author reads the layout doc, **Then** the doc tells them its folder shape without needing another source.
3. **Given** the CI enforcement decision, **When** a module drifts from the layout, **Then** what happens (CI fails, or review only) matches the recorded decision.

---

### User Story 5 - Agents find the right doc for a task (Priority: P3)

An AI agent starting a task reads the task-to-doc map and finds a row for its kind of task (add an API route, change a CRD, add a dashboard screen, add a game module, change the website, add a DB migration, change CI), pointing at the reference docs and the layout doc.

**Why this priority**: the map exists and works for component lookup; more task rows are a modest gain.

**Independent Test**: for each common workflow listed in CLAUDE.md, the map has a matching row whose links resolve.

**Acceptance Scenarios**:

1. **Given** the task-to-doc map, **When** an agent looks up any CLAUDE.md "Common workflows" entry, **Then** a row exists and every path in it resolves.
2. **Given** the layout doc, **When** an agent reads the map, **Then** the map links to it.

### Edge Cases

- A tool that reads only a name the decision removes: the decision names any such tool before removal; if one is found later, adding the link back is a one-line fix.
- Symlinks on Windows checkouts without symlink support show as small text files holding the target path; the contributing guide notes this so a Windows contributor edits the real file.
- The website submodule makes its own choice today; changing it is a website PR plus a pointer bump, so the decision says whether it is in scope.
- A `done_` spec folder or the constitution that cites a moved or archived path: every in-repo reference is updated in the same commit (constitution IV rule for renames, applied here too).
- A module that fits none of the layout kinds (for example `test/e2e`, a test-only module): the layout doc lists it as an explicit exception with its reason.
- CI times change after spec 023 lands: the first-PR table states its measurement date and source run, and the doc says where to re-measure.
- `hack/check-specs.sh` requires a `specs.md` per module: if the spec-home decision renames or moves those files, the check, constitution Principle IV and CLAUDE.md change in the same PR.

## Requirements *(mandatory)*

### Functional Requirements

**Agent instructions**

- **FR-001**: The main repo MUST have exactly one regular-file agent instructions document, chosen by OD-1; every other kept instruction-file name MUST be a link to it.
- **FR-002**: Any instruction-file name OD-1 removes MUST have no remaining in-repo references, checked by search before removal.
- **FR-003**: The contributing guide's AI-assisted development section MUST name the canonical file and say what the other names are.
- **FR-004**: The website submodule's instruction files MUST be handled as OD-1 records (either aligned with the main repo or left as they are, with the reason written down).

**Spec and design homes**

- **FR-005**: A written docs rule MUST name where each kind of design material lives: feature specs, component reference docs, and anything else OD-2 keeps.
- **FR-006**: The rule MUST appear in the contributing guide and in CLAUDE.md with the same meaning, and MUST agree with constitution Principle IV (amending the constitution in the same PR if OD-2 requires it).
- **FR-007**: The five `docs/superpowers/` files MUST be moved, archived or removed as OD-2 records, with every in-repo reference updated in the same commit.
- **FR-008**: If OD-2 renames the per-component reference docs, `hack/check-specs.sh`, `docs/agent-architecture.md`, constitution Principle IV and CLAUDE.md MUST change in the same PR. *(Applies only if OD-2 picks a rename.)*

**Target layout**

- **FR-009**: A layout doc MUST define target shapes for three Go module kinds (long-running service, library, CLI tool) per OD-3, naming which folders exist, what goes in each, and where tests live.
- **FR-010**: The layout doc MUST define the target shape for `web/src` per OD-4, including where a screen's sub-sections, its tests, shared components, hooks and API helpers live, and the 500-line size at which a screen is split into sections.
- **FR-011**: The layout doc MUST list every `go.work` module and `web` with its kind, its target shape and its current gap or "matches", measured from `master`.
- **FR-012**: The layout doc MUST list explicit exceptions (at least `test/e2e`) with a reason each.
- **FR-013**: Layout enforcement MUST match OD-5 (no check, or a CI check and when it lands). This spec MUST NOT add a check that fails on current `master`.

**First-PR guide**

- **FR-014**: `docs/contributing.md` MUST gain a "Your first PR" section reachable from the guide's opening lines.
- **FR-015**: The section MUST have a table with one row per change type (at least: docs only, spec only, web only, one Go service, operator/CRD, API route, game module, website), each naming the owning folder, the local compile command, and the measured CI wall-clock time with its date and source.
- **FR-016**: The section MUST restate the PR gates a first PR trips on: signed commits, `type:` + `area:` labels, no local test or lint runs, design-first for UI, submodule PRs for `modules/` and `website/`.
- **FR-017**: The website's contributing page MUST link to the first-PR section instead of repeating it (website PR plus submodule pointer bump).

**Task-to-doc map**

- **FR-018**: `docs/agent-architecture.md` MUST gain one task-to-doc row per CLAUDE.md "Common workflows" entry and MUST link to the layout doc.

**Boundaries**

- **FR-019**: This spec MUST NOT move, rename or split any source file or package. Only docs, instruction-file links, and (if OD-5 says so) a check script are in scope.
- **FR-020**: Every path written in the new or changed docs MUST resolve (the existing `check-links` gate verifies it in CI).

### Key Entities

- **Canonical instructions file**: the one real agent-instructions document per repo; other names link to it.
- **Feature spec**: a `specs/NNN-<slug>/` folder made through speckit; describes a change.
- **Component reference doc**: a per-module document that describes how the module works today (today named `specs.md`).
- **Layout kind**: a category of Go module (service, library, CLI) or the web app, each with one target folder shape.
- **Layout map row**: one module, its kind, target shape, and current gap.
- **First-PR row**: one change type, its owning folder, compile command and measured CI time.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A reader new to the repo can name the owning folder, compile command and expected CI wait for three sample changes in under 5 minutes using only the contributing guide.
- **SC-002**: Exactly 1 regular-file agent instructions document per repo; 0 references to removed names.
- **SC-003**: 0 design or plan docs outside the homes the docs rule names (other than listed exceptions); 0 stale "not yet implemented" status lines for shipped features.
- **SC-004**: 18 of 18 layout units (17 Go modules plus `web`) have a kind, target shape and gap in the layout map.
- **SC-005**: Every CLAUDE.md "Common workflows" entry has a task-to-doc row; 0 broken links in changed docs.
- **SC-006**: The split spec's author can write its tasks without adding a layout decision of their own (checked when that spec's OPEN-DECISIONS has no layout items).

## Assumptions

- The first-PR guide lives as a section of `docs/contributing.md`, since the ask names that file as the gap; the website page links to it.
- CI times come from the 2026-10-20 assessment; spec 023 will change them, so the table carries its date and source and is refreshed when 023 is done.
- The constitution stays the higher authority; any rule here that changes Principle IV amends the constitution in the same PR with a version bump and Sync Impact Report.
- Principle I (e2e-tested delivery): this is a docs-only spec. It takes the same documented docs exception specs 012 and 024 used; verification is the existing doc gates in CI (`check-links`, `check-specs`, `check-doc-versions`).
- The "split big packages and files" spec, not this one, moves code, sets any file-size threshold, and decides per-package split boundaries inside the target shapes.
- No `.pen` design work is needed: no dashboard or website visuals change.
