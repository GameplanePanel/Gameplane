# Feature Specification: Documentation Refresh, Comparison Table, and External Outreach

**Feature Branch**: `012-docs-refresh-and-outreach`

**Created**: 2026-09-01

**Status**: Draft

**Input**: User description: Refresh the project documentation (README.md, docs/ directory, and component READMEs) to remove staleness, add a side-by-side comparison table of Gameplane vs. competitors in the README, refresh dashboard screenshots, and track external outreach submissions for visibility and discoverability.

## User Scenarios & Testing *(mandatory)*

### User Story 1: Evaluator Comparing Alternatives (Priority: P1)

An evaluator searching for a Kubernetes-native game server control panel or alternative to Pterodactyl/CubeCoders AMP discovers the Gameplane README. They need to quickly understand how Gameplane's Kubernetes-native architecture, idle auto-sleep, and NAT traversal differ from competitors. They expect a structured comparison table in the README so they can make an informed choice in under 5 minutes.

**Why this priority**: This is the entry point for new users; an outdated or missing comparison directly loses evaluators to competitors. It is the primary value prop delivery for high-signal users.

**Independent Test**: Can be verified by reading README.md and confirming the comparison table is present, complete, sourced, and accurately reflects Gameplane's capabilities as implemented in the codebase and documented in README.md and docs/, and the documented beta status.

**Acceptance Scenarios**:

1. **Given** an evaluator viewing the README, **When** they scan the comparison table, **Then** they can identify at least three key differences between Gameplane and each competitor without reading external sources.
2. **Given** a claim in the Gameplane column (e.g., "Idle auto-sleep with wake-on-connect"), **When** the evaluator reviews it, **Then** it matches the actual capability status (including beta/opt-in qualifiers).
3. **Given** a claim in a competitor column, **When** implementation team checks the source, **Then** the source is documented and dated (e.g., "verified against [Competitor] docs on [DATE]").
4. **Given** the table's Gameplane column states "beta" or "optional" for a feature, **When** an evaluator visits the docs, **Then** the same feature is labelled consistently.

---

### User Story 2: New Self-Hoster Following Documentation (Priority: P1)

A user deploying Gameplane v0.2.0-beta.8 for the first time follows the README, installation guide, and feature documentation. They expect version strings in examples to match their release, links to resolve correctly, feature descriptions to match what they see in the dashboard, and optional features to be clearly marked. Docs should not contain outdated claims or broken references.

**Why this priority**: Stale documentation directly causes deployment failures, user frustration, and support overhead. It is the primary signal of project maturity.

**Independent Test**: Can be verified by running automated link checks against all docs, comparing version strings against the current release, and spot-checking feature descriptions against the shipped code.

**Acceptance Scenarios**:

1. **Given** installation docs reference a Helm chart version, **When** the version is checked, **Then** it matches the current release.
2. **Given** docs describe a feature (e.g., idle auto-sleep), **When** checked against operator reconcilers, **Then** the description matches the implementation.
3. **Given** docs mark a feature as "optional" or "experimental", **When** the chart values are checked, **Then** the feature is truly disabled by default.
4. **Given** a link in README or docs points to another doc file, **When** the link is followed, **Then** the target exists.
5. **Given** component READMEs (telemetry-receiver, audit-syslog-bridge, mcp-server), **When** version strings are checked, **Then** they match the current release.

**maintainer idea**: auto fetch latest tag in a ci to auto update the docs for version string?

---

### User Story 3: Maintainer Tracking External Outreach (Priority: P2)

The maintainer wants to submit Gameplane to three external directories (AlternativeTo.net, Awesome-Selfhosted, Awesome-Kubernetes) and needs a persistent, trackable to-do list showing status for each submission. The list should be part of the repo, updated as submissions are made, and clear about what "success" means (submission made, not dependent on acceptance).

**Why this priority**: External visibility grows the user base. Tracked outreach ensures submissions are actually made and history is documented for future maintainers.

**Independent Test**: Can be verified by reading the to-do list file and confirming all three target directories are listed with status fields and submission tracking.

**Acceptance Scenarios**:

1. **Given** the maintainer reviews the outreach to-do list, **When** they check a status entry, **Then** they can tell whether the submission has been made and when.
2. **Given** a submission is rejected, **When** the maintainer updates the list, **Then** the change is committed to git.
3. **Given** a future maintainer inherits the project, **When** they read the to-do list, **Then** they understand which directories have been targeted and the submission history.

---

### User Story 4: Evaluator Reviewing Dashboard Screenshots (Priority: P2)

An evaluator exploring the Gameplane README wants to see what the dashboard looks like before diving into installation docs. They expect a series of high-quality screenshots showing the key screens (login, server list, server detail, console, admin settings) so they can assess the UI quality and feature surface quickly. Up-to-date screenshots also instil confidence that the docs reflect the current product.

**Why this priority**: Screenshots are a primary first-impression tool for evaluators. Outdated or missing screenshots signal that the product is not actively maintained or that the UI has diverged from documentation.

**Independent Test**: Can be verified by confirming six refreshed screenshots and at least five newly-added screenshots are present in docs/img/, linked in README.md with alt text, and showing current dashboard UI without real user data.

**Acceptance Scenarios**:

1. **Given** an evaluator scrolls through the README, **When** they encounter the screenshot gallery, **Then** they see at least 11 total screenshots covering key workflows.
2. **Given** a screenshot in the README, **When** they compare it to the running dashboard, **Then** the UI layout, components, and styling match the current web/ codebase.
3. **Given** alt text under a screenshot, **When** they read it, **Then** it clearly describes the screen's purpose and key UI elements.
4. **Given** a screenshot showing server data or names, **When** they review it, **Then** it contains only test data (e.g., "test-server-01", dummy cluster names) with no real hostnames or IP addresses.

---

### Edge Cases

- What if docs claim a feature (e.g., "multi-cluster console streaming") that is actually beta-limited? **Response**: Documentation must explicitly state the limitation with a [BETA] tag.
- What if a new release ships before this feature is complete? **Response**: The audit adapts to the new version; scope and standards remain unchanged.
- What if a screenshot can't be captured because the feature is not available? **Response**: Substitute a lower-priority screen; document why if truly unreachable.
- What if a competitor's documentation moves or goes offline? **Response**: Sources are dated at implementation time; note the date and last-known URL.

---

## Requirements *(mandatory)*

### Deliverable 1: Comparison Table

**FR-001**: README MUST include a side-by-side comparison table positioned after the "Why Gameplane?" section, comparing Gameplane, Pterodactyl, CubeCoders AMP, and Agones across at least eight dimensions.

**FR-002**: The comparison table MUST include dimensions: (a) Deployment/runtime model, (b) Scaling & auto-sleep, (c) Inbound connectivity (NAT traversal, relay), (d) Backup and restore, (e) Access control & authentication, (f) Game template distribution, (g) Multi-tenancy & multi-cluster, (h) Licensing, (i) Target operator scope (self-hosted vs. managed SaaS).

**FR-003**: The Gameplane column MUST accurately reflect capabilities as implemented in the codebase and documented in README.md and docs/, including all optional/opt-in/beta qualifiers (e.g., "Wake-on-connect [BETA: Minecraft/Terraria only]", "Sentinel [optional, disabled by default]").

**FR-004**: The Gameplane column MUST state the project's beta status explicitly using the status line from README.md: "Status: **beta** (`v0.2.0-beta.8`). The operator, API, agent, and dashboard are feature-complete for the v1 scope and stabilized for external testing."

**FR-005**: Each competitor column cell MUST be sourced and dated (e.g., "Verified against <product>'s official documentation on <date-checked>"). Sources MUST reference publicly documented features from official documentation or GitHub READMEs.

**FR-006**: The comparison table MUST NOT include disparaging, speculative, or unverifiable claims. Cells MUST describe factual feature presence/absence or design choices, not value judgments.

**FR-007**: The comparison table MUST be formatted as a readable markdown table with clear row/column headers.

**FR-008**: Gameplane columns MUST NOT overstate features in beta or limited deployment (e.g., multi-cluster console streaming MUST be qualified with "[local cluster only]").

---

### Deliverable 2: Documentation Accuracy Refresh

**FR-009**: A documentation audit MUST verify across: (a) README.md, (b) all 13 files in docs/ directory, (c) audit-syslog-bridge/README.md, (d) mcp-server/README.md, (e) telemetry-receiver/README.md.

**FR-010**: The audit MUST verify acceptance standards: (a) Version strings (e.g., "v0.2.0-beta.8") match the current release, (b) Feature descriptions match the implementation in operator/, api/, agent/, (c) Internal links resolve to existing targets, (d) Features marked as "optional", "experimental", or "beta" in code/charts are labelled consistently in docs.

**FR-011**: For each correction identified, the change MUST cite evidence (file and line numbers checked, what was verified against).

**FR-012**: Optional and experimental features (sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver) MUST be clearly marked as "[optional]" or "[experimental]" or "[disabled by default]" the first time mentioned in each doc file.

**FR-013**: The audit MUST identify and correct outdated claims about feature status (e.g., multi-cluster console/log streaming is scoped to local cluster only).

**FR-014**: README.md and docs MUST NOT reference features that are announced but not yet shipped.

---

### Deliverable 3: Screenshots

**FR-015**: The README MUST display screenshots showing the current Gameplane dashboard UI. This has two parts: (a) refresh the existing six screenshots to match the current UI in web/src/routes/, (b) add new screenshots covering additional screens currently uncovered.

**FR-016**: Refreshed screenshots MUST be retaken against the current web/ codebase and stored in docs/img/ with the same filenames.

**FR-017**: New screenshots MUST cover at least five currently uncovered screens selected from the gap list (e.g., Login, Server Detail Events/Logs tabs, Create Server wizard, Admin Settings sections, Cluster page). Selection MUST prioritize high-value screens that new users encounter first.

**FR-018**: All screenshots MUST include alt text in README.md describing the screen's purpose and key UI elements.

**FR-019**: Screenshots MUST NOT display real user data, real server hostnames, real cluster names, or real IP addresses. Test/dummy data is acceptable.

---

### Deliverable 4: External Outreach To-Do List

**FR-020**: A persistent to-do list MUST be created in specs/done_012-docs-refresh-and-outreach/ listing the three target external directories and the status of each submission.

**FR-021**: The to-do list MUST include entries for: (a) AlternativeTo.net submission, (b) Awesome-Selfhosted PR, (c) Awesome-Kubernetes submission.

**FR-022**: Each entry MUST track a status field (e.g., "pending", "submitted [DATE]", "in-progress [DATE]", "rejected [DATE, reason]"). Status updates MUST be committed to git.

**FR-023**: The to-do list MUST NOT claim success based on third-party acceptance. Success MUST be measured by the submission being made and tracked.

**FR-024**: The to-do list MUST include a note for each entry describing what was submitted (link, PR number, or email date) for audit trail.

**FR-025**: The to-do list MUST be linked from docs/contributing.md so maintainers can find it easily.

---

## Success Criteria *(mandatory)*

**SC-001**: A potential Gameplane evaluator can read the README comparison table and identify at least three key architectural or operational differences between Gameplane and each competitor without consulting external sources.

**SC-002**: Every feature claim in the Gameplane column is traceable to the codebase, README.md, or docs/ and explicitly qualified with "[BETA]", "[optional]", or "[local cluster only]" if applicable.

**SC-003**: Every feature claim in competitor columns includes a dated source reference committed in the README or linked source file.

**SC-004**: A new self-hoster deploying Gameplane v0.2.0-beta.8 following README and docs does not encounter out-of-date version strings, stale feature descriptions, or broken links that would block deployment.

**SC-005**: All version strings in code examples match the current release (v0.2.0-beta.8) or are explicitly marked as "example version".

**SC-006**: All internal doc links in README.md and docs/ resolve to existing targets verified by automated link checking.

**SC-007**: Every feature marked as "optional" or "experimental" in CLAUDE.md or charts/gameplane/values.yaml is labelled consistently in all associated documentation files.

**SC-008**: The audit of all 17 audited files (README.md, 13 docs/, 3 component READMEs) resolves: (a) 0 version-string mismatches against the current release, (b) 0 broken internal links, and (c) consistent labelling of optional/experimental/beta features across all mentions.

**SC-009**: All six refreshed screenshots show the current Gameplane dashboard UI state and are updated in docs/img/ with matching filenames.

**SC-010**: At least five new screenshots are added showing screens currently not covered, with alt text describing each screen's purpose.

**SC-011**: All screenshots contain no real user data, real hostnames, real cluster names, or real IP addresses.

**SC-012**: The outreach to-do list is created, committed to specs/done_012-docs-refresh-and-outreach/, and includes entries for all three target directories with status fields.

**SC-013**: The outreach to-do list is linked from docs/contributing.md and is maintained as the authoritative record of submission status.

**SC-014**: By feature completion, each of the three target directories (AlternativeTo.net, Awesome-Selfhosted, Awesome-Kubernetes) has a recorded terminal state in the outreach to-do list: either a submission actually made (with date and reference recorded), or an explicit, dated deferral reason recorded in the to-do list.

---

## Assumptions

- **Competitor Research**: Competitor capabilities in the comparison table are researched during implementation from official public documentation and GitHub repos, with sources and dates recorded. This spec defines the audit scope; cells are filled during implementation.

- **Release Stability**: v0.2.0-beta.8 remains stable throughout the work. If a new release ships, the audit adapts; scope and standards remain unchanged.

- **No Design Changes Required**: Comparison table, documentation updates, and to-do list do not require new Pencil designs. Screenshots require retakes of existing screens only.

- **CI Link Validation**: Automated link checking is available or manual spot-checking suffices for merge validation.

- **Test Cluster Available**: Screenshots can be captured from a running kind cluster or test environment with test data (dummy server names, test modules).

- **No Feature Churn**: Core product features remain stable during documentation refresh. New features shipped during this work are documented in a follow-up or explicitly excluded.

- **Documentation Refresh Does Not Trigger Code Changes**: The audit identifies stale docs but does NOT require code changes. Actual bugs or incomplete features found are tracked separately and out of scope.

---

## Out of Scope

- **Changes to website/ submodule**: The public marketing website (gameplane-website repo) is out of scope, except the website screenshot sync (OD-16), which captures and updates website screenshots on the gameplane-website repo via a separate workflow step.

- **Changes to product code, CRDs, or charts**: This feature documents what ships, not what will ship.

- **Paid promotion or advertising**: No paid placements on external services.

- **Translation and localization**: All documentation remains in English.

- **Control over third-party acceptance**: Awesome-Selfhosted and Awesome-Kubernetes maintain their own review processes. Acceptance is outside Gameplane's control and NOT a success criterion.

- **New Pencil designs or UI redesigns**: Screenshots document existing UI only. UI improvements are a separate feature.

- **Audit of optional components' expanded coverage**: capture-sidecar, tunnel, audit-syslog-bridge, telemetry-receiver, mcp-server, and sentinel component READMEs are audited for accuracy, but expanding their coverage in core docs is not required.

- **Full rewrite of architectural documentation**: docs/architecture.md is audited for accuracy, but complete restructuring is not required unless the existing structure is inaccurate.

---

## Amendment (2026-10-07): Website Screenshot Sync

This amendment extends the screenshot-refresh workflow to the public website repo (gameplane-website) per maintainer ruling OD-16. The following requirements apply exclusively to the website screenshot capture phase and do not affect the main-repo screenshot deliverables (FR-015 through FR-019).

**FR-026**: The screenshot-refresh workflow MUST include a dedicated step that captures five dashboard screenshots for the gameplane-website repo at 1440×900 viewport with deviceScaleFactor 2 (2880×1800 PNG RGBA): dashboard home, servers list, console, backups index, modules catalog. Output directory: `web/website-screenshots/` (repo-local, gitignored).

**FR-027**: The workflow step MUST open or update a pull request on the gameplane-website main branch, copying the five captured PNGs to `src/assets/screenshots/{dashboard,servers,console,backups,modules}.png` in the website clone.

**FR-028**: If the `SCREENSHOT_BOT_PAT` repository secret cannot write to gameplane-website, the website step MUST log a `::warning::` message and exit 0, keeping the overall workflow run green (skip and stay green behavior). If the secret is empty, the website step MUST log a `::notice::` message and exit 0. The secret is a fine-grained PAT with Contents + Pull requests read/write permission, scoped to both GameplanePanel/Gameplane and GameplanePanel/website.

**FR-029**: The workflow MUST NEVER push directly to the gameplane-website main branch. All changes MUST be submitted via pull request, which is the only path to main (main branch is protected and auto-deploys GitHub Pages).

---

## Key Entities

**Comparison Table**: A markdown table in README.md with rows representing feature dimensions and columns representing Gameplane, Pterodactyl, CubeCoders AMP, and Agones. Each cell contains a brief feature description or "not applicable".

**Documentation Audit**: The process of checking all docs/ files and READMEs against the current codebase, verifying version strings, feature descriptions, internal links, and optional/beta labelling match implemented reality.

**Screenshot Set**: The collection of dashboard UI images showing Gameplane features. Includes six refreshed images and at least five new images covering currently uncovered screens.

**Outreach To-Do List**: A tracked markdown file listing three external directory submission targets (AlternativeTo.net, Awesome-Selfhosted, Awesome-Kubernetes) with status fields and submission dates.
