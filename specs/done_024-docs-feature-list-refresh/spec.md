# Feature Specification: Documentation Refresh, Feature Catalog, and Roadmap Status

**Feature Branch**: `claude/project-thread-6pzhf7`

**Created**: 2026-10-12

**Status**: Draft

**Input**: User description: "Update the docs on both website, /docs, readme.md etc..... Create a feature.md with all feature. update the roadmap.md with new status."

## Context

Spec 012 (archived as `specs/done_012-docs-refresh-and-outreach/`) last refreshed the docs for `v0.2.0-beta.8`. Since then `v0.3.0` shipped, several specs finished (014–018, 020, 022) and more work is in flight (002, 009, 013, 019, 021, 023). The docs have drifted. Examples found while writing this spec:

- `README.md` says "30 ready-to-use game templates" (Features list and comparison table) and `docs/comparison-sources.md` repeats it, while the module catalog holds 29 games since Hell Let Loose was withdrawn (spec 015).
- `docs/roadmap.md` lists only items shipped up to `v0.2.0-beta.8` under "Shipped toward v1" and says nothing about the `v0.3.0` release or the specs in progress.
- The project has no single list of everything Gameplane can do; features are described in scattered places (README Features, website pages, 20+ docs files, CHANGELOG).

## Clarifications

### Session 2026-10-12

- Q: How should FEATURES.md decide which maturity label a feature gets? → A: Evidence rule: stable = shipped, on by default and covered by CI e2e tests; beta = shipped with documented caveats or no e2e test; experimental = the docs already call it experimental.
- Q: How should FEATURES.md and the website features page stay in sync? → A: A CI check: a new script fails CI when the two lists differ in feature names or labels; it ships with its own fixture tests.
- Q: How detailed should each FEATURES.md entry be? → A: One entry per capability (roughly 100 to 150 rows, one per thing a user or operator can do), grouped under area headings.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Evaluator reads one complete feature list (Priority: P1)

Someone deciding whether to use Gameplane wants one page that lists every capability the product has today, grouped by area (servers and lifecycle, console and admin actions, files, players, mods, backups, game modules, networking and tunnels, idle sleep and wake, multi-cluster, users and access, audit and security, notifications, telemetry, observability, localization, developer tooling), with each feature's maturity (stable, beta, experimental, optional/off by default, planned) and a link to where it is documented.

**Why this priority**: This is the new artifact the user asked for and the one evaluators and contributors will use most. Every other page can link to it instead of repeating feature lists.

**Independent Test**: Open the feature catalog, pick any 10 entries at random, and confirm each one exists in the shipped product with the stated maturity and that its documentation link resolves.

**Acceptance Scenarios**:

1. **Given** the feature catalog, **When** a reader looks for a capability that ships in the current release (for example wake-on-connect or audit log integrity), **Then** it is listed under the right area with its maturity label and a working link to its docs.
2. **Given** a feature that is opt-in or off by default (relay tunnels, packet capture, telemetry extended tier, PostgreSQL driver), **When** it appears in the catalog, **Then** its label says so, matching what the installer actually ships.
3. **Given** a capability that is planned but not shipped (service accounts, Users page identity-provider tab), **When** it appears in the catalog, **Then** it is clearly marked planned and links to the roadmap, never shown as available.
4. **Given** a feature in progress on an unmerged spec (for example language packs from spec 019), **When** the catalog is written, **Then** it is listed as in progress only if the roadmap lists it, and never as shipped before it is merged to the default branch.

---

### User Story 2 - Maintainer and users see the current roadmap status (Priority: P1)

A user or contributor opens the roadmap and sees where the project stands now: current release, what shipped in `v0.3.0` and since, what is in progress (each open spec with its state), what is blocked and on what, what stands between now and v1, and what is out of scope.

**Why this priority**: The user explicitly asked for the roadmap to carry the new status; the current one stops at `v0.2.0-beta.8`.

**Independent Test**: Compare each roadmap entry against the specs folder, the CHANGELOG and the merged history of the default branch on the day of the update; every entry's status must match.

**Acceptance Scenarios**:

1. **Given** the refreshed roadmap, **When** a reader looks for the `v0.3.0` release, **Then** it lists what shipped in it and links to the CHANGELOG.
2. **Given** an open spec (002, 009, 013, 019, 021, 023 at the time of writing), **When** a reader looks it up on the roadmap, **Then** it shows a plain-words status (in progress, blocked on X, waiting on packet capture) consistent with that spec's `tasks.md`.
3. **Given** an item that the old roadmap called planned or missing and that has since shipped, **When** the roadmap is refreshed, **Then** it moves to the shipped section with the release it shipped in.
4. **Given** the main repo roadmap and the website roadmap page, **When** both are read, **Then** they state the same status for every item.

---

### User Story 3 - Self-hoster follows docs that match the product (Priority: P2)

A self-hoster follows the README, the `docs/` guides, component READMEs and the website docs. Counts, version strings, feature descriptions, links and maturity labels all match what the current release does.

**Why this priority**: Stale facts cause failed installs and lost trust, but the existing docs are mostly right; this story corrects drift rather than writing new material.

**Independent Test**: Run the project's existing doc checks (version strings, internal links) in CI, and review a fixed list of known facts (module count, registry count, role names, defaults) against the code and the module catalog.

**Acceptance Scenarios**:

1. **Given** any page that states the number of shipped game modules or mod registries, **When** it is checked against the module catalog and the code, **Then** the number matches.
2. **Given** any page that describes a feature's default (on/off) or maturity, **When** it is checked against the shipped install defaults, **Then** it matches and uses the same label as the feature catalog.
3. **Given** the README, **When** a reader reaches its Features section, **Then** it gives a short summary and links to the full feature catalog instead of a second, diverging list.
4. **Given** every internal doc link and anchor in the main repo and the website, **When** CI link checks run, **Then** they all resolve.

---

### User Story 4 - Website visitor finds the same information (Priority: P2)

A visitor to the public website reads its docs pages and finds the same feature list, roadmap status and facts as the repository docs.

**Why this priority**: The website is the public face; a mismatch with the repo makes the project look abandoned or inaccurate.

**Independent Test**: For each fact checked in Story 3 and each roadmap item in Story 2, compare the website page and the repo page; they agree.

**Acceptance Scenarios**:

1. **Given** the website docs, **When** a visitor looks for a feature list, **Then** a features page exists in the docs navigation carrying the same content as the repo feature catalog.
2. **Given** the website roadmap page, **When** compared to `docs/roadmap.md`, **Then** statuses match.
3. **Given** website docs pages that cover the same topic as a `docs/` file, **When** both are read, **Then** they do not contradict each other.

---

### Edge Cases

- A feature exists in code but is hidden or unfinished in the dashboard (placeholder tabs): it is listed as planned, not shipped.
- A feature was withdrawn (Hell Let Loose module): it is removed from counts and lists; history stays only in the CHANGELOG.
- A spec merges while this refresh is in progress: the roadmap and catalog reflect the state of the default branch on the day the docs PR is opened, and the status date is printed on the roadmap.
- A fact cannot be verified from the code or docs (for example a competitor claim in the comparison table): it keeps its existing dated source and is not changed by this spec.
- Historical entries tagged `doc-versions: historical` keep their original version strings.
- A website page that needs a visual or layout change beyond text: the change is designed first in the website design file before any code, per the website rules.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The project MUST have one feature catalog file listing every shipped capability of Gameplane, with one entry per capability (one per thing a user or operator can do, roughly 100 to 150 entries) grouped under area headings, as a root `FEATURES.md` next to `README.md` (OD-1), mirrored by a features page on the website.
- **FR-002**: Each catalog entry MUST carry a one-line description, a maturity label from a fixed set (stable, beta, experimental, planned, in progress), a default (on, off, or n/a), and a link to the page that documents it. Maturity follows an evidence rule: **stable** = shipped, on by default and covered by a CI end-to-end test; **beta** = shipped but with documented caveats or no end-to-end test; **experimental** = the docs already call it experimental. An optional feature (one the install ships disabled) is never stable: it gets maturity beta or experimental with default **off**.
- **FR-003**: The catalog MUST cover the dashboard, operator, API, agent, optional components (sentinel, capture sidecar, tunnels, audit syslog bridge, telemetry receiver, MCP server, agent gateway), the game module catalog, and the module authoring tool.
- **FR-004**: The catalog MUST NOT list as shipped anything that is not merged to the default branch; planned and in-progress items MUST link to the roadmap.
- **FR-005**: `docs/roadmap.md` MUST state the current release, the date of the status, what shipped in `v0.3.0` and since (linking the CHANGELOG), each open spec with a plain-words status, blockers with what unblocks them, what remains for v1, and what is out of scope.
- **FR-006**: Roadmap items that have shipped MUST move to the shipped section with their release; stale "planned" claims MUST be corrected or removed.
- **FR-007**: The README MUST be corrected for stale facts (module count, status text, links) and its Features section MUST become a short summary linking to the feature catalog.
- **FR-008**: Every file under `docs/` and every component README in the main repo MUST be reviewed against the shipped product; stale counts, defaults, maturity labels, names and links MUST be fixed.
- **FR-009**: The website docs MUST be updated to match: a features page mirroring the catalog, the roadmap page mirroring `docs/roadmap.md`, and fixes to any website page carrying the same stale facts, as docs-page text only; the landing page is not changed (OD-3).
- **FR-010**: The module repository README MUST be checked for the module list and count and fixed if stale.
- **FR-011**: Every change MUST pass the project's existing documentation checks in CI (version strings, internal links and anchors, spec checks), with no checks loosened or skipped.
- **FR-012**: Each fact corrected MUST be traceable: the docs PR description or a review list in the spec folder MUST note what was stale, where, and what source confirmed the new value.
- **FR-013**: The roadmap MUST have an "In progress" section with one line per open spec in `specs/` on the status date, each with its plain-words status and blocker (OD-2).
- **FR-014**: The comparison table's competitor columns MUST NOT change unless re-verified with a dated source; only Gameplane-column facts are corrected.
- **FR-015**: A new CI check MUST fail when `FEATURES.md` and the website features page differ in their feature names, areas or maturity labels, and MUST ship with fixture tests covering the in-sync case and at least one mismatch of each kind. Existing checks are not loosened to make it pass.

### Key Entities

- **Feature catalog entry**: one capability (a single thing a user or operator can do); fields: area, name, one-line description, maturity label, default (on/off), documentation link, release first shipped (when known).
- **Roadmap item**: title, status (shipped, in progress, blocked, planned, out of scope), release or spec reference, blocker and what unblocks it, status date.
- **Stale fact record**: page, old value, new value, confirming source.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of shipped capabilities found in the docs, CHANGELOG and finished specs appear in the feature catalog, and a reviewer sampling 10 entries finds 10 correct (exists, right label, link resolves).
- **SC-002**: Every open spec in `specs/` on the status date appears on the roadmap with a status matching its `tasks.md`.
- **SC-003**: Zero known stale counts remain: every page stating the module count says 29 (or the catalog's count on the status date), and every page stating the registry count matches the code.
- **SC-004**: The repo roadmap and the website roadmap agree on 100% of item statuses; the repo catalog and the website features page list the same entries, enforced by the FR-015 check passing in CI.
- **SC-005**: The project's existing documentation checks pass in CI on the docs PRs with no rule changes, and the new sync check fails on a deliberately mismatched fixture in its own tests.
- **SC-006**: A new reader can answer "does Gameplane do X, and is it stable?" for any listed capability from the catalog alone in under one minute.

## Assumptions

- "Website" means the `GameplanePanel/website` docs pages (text content). Docs pages are added with frontmatter only, so adding a features page needs no design change. Landing page visuals are out of scope (OD-3).
- "/docs" means the main repo `docs/` folder, including `docs/design`, `docs/examples` and `docs/outreach` text where facts are stated; `docs/superpowers` and archived spec folders are not edited.
- "readme.md etc." means the root README, component READMEs (`audit-syslog-bridge`, `containers`, `mcp-server`, `telemetry-receiver`) and the module repo README.
- The CHANGELOG is a historical record and is not rewritten; only its link targets are checked.
- Dashboard screenshots are not retaken in this spec; capturing them needs the local design/frontend setup.
- Outreach and external publishing are out of scope (the maintainer does all outreach).
- Work lands as one PR per repository (main, website, module if needed), with the main repo submodule pointer bumped only after the website and module PRs merge.
- Status is measured against the default branch on the day the docs PRs are opened; specs that merge later are picked up by a follow-up edit, not by this spec.
