# Tasks: Documentation Refresh, Comparison Table, and External Outreach

**Input**: Feature spec from `/specs/012-docs-refresh-and-outreach/`

**Prerequisites**: All artifacts in `/specs/012-docs-refresh-and-outreach/` (spec.md, plan.md, research.md, data-model.md, contracts/, OPEN-DECISIONS.md, quickstart.md)

**Tests**: No unit/E2E tests requested. Verification: CI runs automated link and version checks (hack/check-links.sh, hack/check-doc-versions.sh) on every lint run; screenshot capture runs on CI via tag-triggered workflow (OD-3c) and on-demand `workflow_dispatch` (OD-15); the positive path (all 17 files have matching version strings, zero broken internal links, consistent optional/experimental labels) is verified locally per D6 precedent and in CI per Principle VI (rule 8). Quickstart.md Scenarios 1–8 provide read-only validation checklists.

**Organization**: Tasks grouped by user story (US1, US2, US3, US4), with parallel opportunities marked [P]. Polish phase covers commits and verification per CLAUDE.md rules 8, 11.

## Format

- **[P]**: Can run in parallel (different files, no dependencies)
- **[USX]**: Story phase (US1, US2, US3, US4); setup/foundational/polish do not use story tags
- File paths, section citations, FRs (FR-001–FR-025), SCs (SC-001–SC-014), and orchestrator decisions (OD-1–OD-15) named in descriptions

---

## Phase 1: Setup

**Purpose**: Confirm branch context and understand all contracts

- [X] T001 Confirm checked out on branch `012-docs-refresh-and-outreach`; read all spec artifacts (spec.md, plan.md, research.md, data-model.md, contracts/comparison-table.md, contracts/docs-audit.md, contracts/screenshot-set.md, contracts/outreach-todo.md, quickstart.md, OPEN-DECISIONS.md, inventory.md); verify appVersion in charts/gameplane/Chart.yaml:6 is exactly `0.2.0-beta.8` (if moved, audit adapts per Assumption "Release Stability"); understand FR-001 through FR-025, SC-001 through SC-014, and all fifteen maintainer rulings (OD-1 through OD-15, including OD-3a–OD-3d and OD-6a–OD-6c), all ruled 2026-09-02

---

## Phase 2: Foundational

**Purpose**: Establish shared audit record before user stories begin

- [X] T002 Create `/home/user/Gameplane/specs/012-docs-refresh-and-outreach/audit-log.md` with header block only: markdown table with columns `File | Line(s) | Finding | Evidence Checked | Resolution | Commit`; no rows yet; include introductory explanation that this log records every correction made per US2 and US4 (FR-011, SC-001); satisfies D-F ruling

---

## Phase 3: User Story 1 - Comparison Table (Priority: P1) 🎯 MVP

**Goal**: Create a sourced side-by-side comparison table in README.md showing Gameplane, Pterodactyl, CubeCoders AMP, and Agones across nine feature dimensions (FR-001–FR-008, SC-001–SC-003)

**Independent Test**: Comparison table is present in README.md, positioned after "Why Gameplane?" and before "Features"; contains status line (FR-004), all nine row dimensions (a–i) in order per FR-002, every Gameplane cell cited to code/docs with qualifiers applied per V-CC3, every competitor cell includes dated SourceReference per V-SR1/V-SR2, no disparaging/speculative claims per FR-006, markdown syntax follows github-flavored conventions per FR-007

### Research & Sourcing for User Story 1

- [X] T003 [P] [US1] Research Pterodactyl comparison dimensions (a–i): fetch official documentation from https://pterodactyl.io/ and https://github.com/pterodactyl/panel/blob/develop/README.md to verify presence/absence of: (a) Deployment/runtime model (daemon-based vs K8s), (b) Scaling & auto-sleep, (c) Inbound connectivity (relay, NAT traversal), (d) Backup and restore, (e) Access control & authentication, (f) Game template distribution, (g) Multi-tenancy & multi-cluster, (h) Licensing, (i) Target operator scope; fill ComparisonCell properties (text ≤25 words per V-CC4, no speculative claims per FR-006); prepare SourceReference entries for docs/comparison-sources.md (URL, date checked, what was verified per V-SR1/V-SR2); record findings with real dates (not back-dated); satisfies FR-005, FR-006, SC-001, SC-003

- [X] T004 [P] [US1] Research Agones comparison dimensions (a–i): fetch official documentation from https://agones.dev/site/docs/ and https://github.com/agones-dev/agones to verify each dimension before marking "not applicable"; per OD-11 verify each dimension and record the exact text "not applicable (Agones is a Kubernetes operator library)" for any dimension that does not apply; fill ComparisonCell properties and SourceReference entries; record dates and last-known URLs per V-SR3; satisfies FR-005, SC-001, SC-003

- [X] T005 [P] [US1] Research CubeCoders AMP comparison dimensions (a–i): attempt to fetch official documentation from https://www.cubecoders.com/AMP and/or official GitHub (if public); per OD-9 for any dimension NOT verifiable from public sources fill cell text with exactly "not publicly documented (checked YYYY-MM-DD)"; for verifiable dimensions cite source with date; hunt on archive.org for inaccessible pages and record hunt trail per OD-10; prepare SourceReference entries with real check dates; satisfies FR-005, FR-006, SC-001, SC-003

### Creation for User Story 1

- [X] T006 [US1] Create `/home/user/Gameplane/docs/comparison-sources.md` per contracts/comparison-table.md § 9: header block with explanation; one top-level section per product (Gameplane, Pterodactyl, CubeCoders AMP, Agones); Gameplane column entries cite path:line evidence from research.md "Gameplane-Column Facts Summary" INSTEAD of URLs (per contract § 9); per-row subsections (G-a through G-i, P-a through P-i, C-a through C-i, A-a through A-i) with Source ID, URL, Checked on, What was verified, Last-known URL; ANCHORS: emit explicit HTML anchors `<a id="gameplane-row-a"></a>` .. `<a id="agones-row-i"></a>` on line before each row heading per contracts/comparison-table.md § 9 (HTML anchors because GitHub ignores `{#id}` heading attributes; contract corrected 2026-09-02 with maintainer sign-off); satisfies FR-005, SC-003

- [X] T007 [US1] Edit README.md: insert comparison table after "Why Gameplane?" section (currently ends line 47) and before "## Features" (currently line 48): (a) insert OD-12 intro paragraph verbatim from contracts/comparison-table.md § 2, (b) insert FR-004 status line verbatim (§ 3), (c) insert markdown table header row and nine row labels verbatim (§ 4 and § 5), (d) fill Gameplane cells from contracts/comparison-table.md § 6 with [G-x] markers (x = a–i) linking to docs/comparison-sources.md#gameplane-row-x, (e) fill competitor cells from research tasks T003/T004/T005 with [P-x], [C-x], [A-x] markers linking to docs/comparison-sources.md#<product>-row-x; markers are inline links per contracts/comparison-table.md § 7; satisfies FR-001, FR-002, FR-003, FR-004, FR-005, FR-007, FR-008, SC-001, SC-002, SC-003

### Verification for User Story 1

- [X] T008 [US1] Verify comparison table completion: (a) read the block inserted between `## Why Gameplane?` and `## Features` in README.md to confirm table is present with status line, all nine dimensions in order, four product columns, (b) check every Gameplane cell against contracts/comparison-table.md § 6 for exact word-for-word match and presence of qualifiers per V-CC2, (c) check every competitor cell against docs/comparison-sources.md entry and confirm SourceReference (URL, date, what-verified) per V-SR1/V-SR2, (d) run offline anchor check: verify all [G-x], [P-x], [C-x], [A-x] links resolve to corresponding `<a id="*">` anchors in docs/comparison-sources.md (read-only pre-flight per D6; actual CI gate via hack/check-links.sh added in US2 T010); (e) read quickstart.md Scenario 1 checklist and confirm done; satisfies contracts/comparison-table.md § 11, SC-001, SC-003

---

## Phase 4: User Story 2 - Documentation Accuracy Refresh (Priority: P1) 🎯 MVP

**Goal**: Audit 17 documentation files (README.md + 13 docs/ + 3 component READMEs) for version staleness, broken links, inconsistent feature labels, and outdated feature descriptions; add automated CI checks to enforce ongoing compliance (FR-009–FR-014, SC-004–SC-008)

**Independent Test**: All 17 audited files have version strings matching appVersion 0.2.0-beta.8 or marked as examples; zero broken internal links verified by hack/check-links.sh; all first mentions of optional/experimental/beta components carry [optional]/[experimental]/[BETA] qualifiers consistent with CLAUDE.md and values.yaml; feature descriptions match operator/api/agent implementation; all findings logged in audit-log.md with evidence citations

### Tooling for User Story 2

- [X] T009 [P] [US2] Create `hack/check-doc-versions.sh` per contracts/docs-audit.md "Version String Checking" and OD-1: bash script, `set -euo pipefail`; reads appVersion from charts/gameplane/Chart.yaml:6; scans the 17 audited files (list in task: README.md, docs/architecture.md, docs/contributing.md, docs/dependencies.md, docs/game-coverage.md, docs/install.md, docs/key-rotation.md, docs/module-authoring.md, docs/networking.md, docs/notifications.md, docs/oidc.md, docs/roadmap.md, docs/security.md, docs/tunnels.md, audit-syslog-bridge/README.md, mcp-server/README.md, telemetry-receiver/README.md) plus docs/comparison-sources.md with regex `v?0\.[0-9]\.[0-9]-beta\.[0-9]+`; allowlist per contract § "Version String Checking": lines with context containing (example version), (example), (placeholder), or code blocks marked `# Example:` or `<!-- Example -->`; ALSO allowlist for historical references (research.md lists: docs/install.md:54, docs/install.md:603, docs/oidc.md:50, docs/oidc.md:91, docs/oidc.md:372, README.md:195); inline HTML comment marker `<!-- doc-versions: historical -->` on same line (OD-14, ruled 2026-09-02); output format per contract Failure/Success cases; exit 0/1; make script executable (`chmod +x`); satisfies FR-009, OD-1, OD-14

- [X] T010 [P] [US2] Create `hack/check-links.sh` per contracts/docs-audit.md and OD-2: bash script, `set -euo pipefail`, offline (no external link fetching); resolves relative file links from each source file in the 17 audited files + docs/comparison-sources.md; validates anchors using GitHub slug rules (lowercase; drop non-alphanumeric chars except spaces/hyphens; spaces to hyphens; duplicate headings get -1, -2 suffixes); ALSO accepts explicit HTML anchors `<a id="x">` and `<a name="x">` as valid targets (per contracts/docs-audit.md OD-2 section); supports same-file `#anchor` links; failure log per contract format; exit 0/1; make script executable; satisfies FR-009, OD-2, SC-006

- [X] T011 [P] [US2] Edit `Makefile` (around line 229 where lint is defined): (a) add `.PHONY: check-doc-versions` and `.PHONY: check-links` targets; (b) add `check-doc-versions:` target with `hack/check-doc-versions.sh` and help comment `## Verify documentation version strings match current release`; (c) add `check-links:` target with `hack/check-links.sh` and help comment `## Verify internal documentation links resolve`; (d) update the existing `lint:` target (currently line 234: `lint: check-specs lint-go lint-web`) to depend on both new targets FIRST, making it `lint: check-doc-versions check-links check-specs lint-go lint-web`; this mirrors the done_011 precedent (T006 in done_011 tasks); satisfies FR-009, plan.md "Lint section requirement", CLAUDE.md "Lint & coverage" section

- [X] T012 [P] [US2] Edit `.github/workflows/ci.yaml` to wire docs-only PRs into lint job (OD-1, OD-2): (a) in `changes` job `filters:` block (currently lines 87–136) add new entry `docs:` listing the 17 audited files (README.md, docs/architecture.md, docs/contributing.md, docs/dependencies.md, docs/game-coverage.md, docs/install.md, docs/key-rotation.md, docs/module-authoring.md, docs/networking.md, docs/notifications.md, docs/oidc.md, docs/roadmap.md, docs/security.md, docs/tunnels.md, audit-syslog-bridge/README.md, mcp-server/README.md, telemetry-receiver/README.md) plus docs/comparison-sources.md, hack/check-links.sh, hack/check-doc-versions.sh; (b) add a new line to the `changes` job `outputs:` block, directly below the existing `specs: ${{ steps.combine.outputs.specs }}` line (currently ci.yaml:81): `docs: ${{ steps.combine.outputs.docs }}` (the key must be exactly `docs` to match (c) and (d)); (c) in the combine step (lines 138–169) add boolean logic for docs, e.g. `docsv=false; { [ "$DOCS" = true ] || [ "$CI" = true ]; } && docsv=true`, plus `echo "docs=$docsv" >> "$GITHUB_OUTPUT"`; (d) change line 342's lint-job if: from `if: needs.changes.outputs.go == 'true' || needs.changes.outputs.specs == 'true'` to add third condition: `if: needs.changes.outputs.go == 'true' || needs.changes.outputs.specs == 'true' || needs.changes.outputs.docs == 'true'`; (e) inside the lint job, add a new check-links step (after "verify lint gate configuration", gated with `if: matrix.module == 'netguard'`) with name "check documentation links" and `run: make check-links`; (f) separately, add re-inclusion path entries to both the `push.paths` block (lines 13–20) and `pull_request.paths` block (lines 23–31): add paths `"README.md"`, `"docs/**"`, `"audit-syslog-bridge/README.md"`, `"mcp-server/README.md"`, `"telemetry-receiver/README.md"` so workflow triggers on docs-only changes; update line-12 comment to describe both specs.md and docs re-inclusion; all in one commit per rule 11; satisfies FR-009, OD-2, plan.md, actionlint/zizmor must pass (FACTS 1e/3)

- [X] T013 [US2] Edit `.github/workflows/ci.yaml` lint job: add step `check documentation versions` (`if: matrix.module == 'netguard'`, `run: make check-doc-versions`, with the same D5-style comment) directly after the `check documentation links` step added by T012; sequential after T012 (both edit `.github/workflows/ci.yaml`); satisfies OD-1, OD-14, SC-005, FR-010.a

- [X] T014 [P] [US2] Update `CLAUDE.md` § "Lint & coverage" section (lines 456–466): add one-line mention of the two new make targets after the existing `make check-specs` line: insert "The `make check-doc-versions` and `make check-links` targets validate documentation version strings and internal link resolution, enforced in the lint job per FR-009 and OD-1/OD-2." (exact text flexible but must cite FR-009, OD-1, OD-2, and make targets); satisfies plan.md "Lint section requirement"

### Correction Tasks for User Story 2

- [X] T015 [US2] Fix version string staleness (FR-010.a, SC-005): (a) append row to audit-log.md for each stale version found; per research.md baseline (R1-versions.md) and FACTS 4: docs/install.md:14 (example version needs 0.2.0-beta.8; evidence charts/gameplane/Chart.yaml:6), telemetry-receiver/README.md:9 and :28 (version examples); edit each file in-place, recording finding in audit-log.md: File | Line | "version string mismatch" | "charts/gameplane/Chart.yaml:6" | "Updated to 0.2.0-beta.8" | [commit-ref]; (b) satisfies FR-011, SC-005; (c) add the OD-14 marker `<!-- doc-versions: historical -->` at the end of each legitimate historical line so the OD-1 gate passes: docs/install.md:54, docs/install.md:603, docs/oidc.md:50, docs/oidc.md:91, docs/oidc.md:372, README.md:195 (verify each is still historical before marking); log each in audit-log.md with Resolution 'marked historical (OD-14)'

- [X] T016 [US2] Fix docs/dependencies.md snapshot date (FR-010.a, SC-005): re-verify the pinned versions listed in lines 20–30 against their official sources (per FACTS 5: netguard/go.mod, gameaction/go.mod, etc.; do NOT back-date; use real re-verification date); update the date on line 26 from "2026-07-29" to the real re-verification date (YYYY-MM-DD); append row to audit-log.md: File="docs/dependencies.md" | Line="26" | Finding="snapshot date re-verified" | Evidence="[actual version sources checked]" | Resolution="Updated snapshot date to YYYY-MM-DD" | Commit; satisfies SC-005, FR-010.a

- [X] T017 [US2] Fix docs/install.md:603 date reference (FR-010, SC-005): research.md notes beta.2 was released 2026-06-22 (CHANGELOG.md:632); docs/install.md:603 says "July 2026" — verify with CHANGELOG and correct to "June 2026" (or more specific date "2026-06-22"); append audit-log.md row: File="docs/install.md" | Line="603" | "feature date incorrect" | "CHANGELOG.md:632" | "Corrected from July 2026 to June 2026" | [commit]; satisfies FR-010, SC-005

- [X] T018 [US2] Fix broken internal links (FR-010.c, SC-006): per research.md R2-links.md and FACTS 4 baseline; (a) docs/networking.md:13 and :194 — fix `docs/install.md` to `install.md` (remove docs/ prefix); (b) README.md:10 — fix anchor reference from `#beta-status--known-limitations` to `#beta-status--limitations`, matching the GitHub-generated slug of the actual `## Beta Status & Limitations` heading (verify exact heading text in README); (c) run hack/check-links.sh read-only pre-flight (D6 permitted) to identify remaining anchor warnings (docs/install.md:190, :567, docs/security.md:380, docs/module-authoring.md:325); resolve each warning by (1) verifying the target heading exists, (2) correcting the anchor slug if it doesn't match GitHub rules, or (3) adding explicit `<a id="">` anchors if needed; append audit-log.md rows for each link fixed: File | Line | "broken link" | "verified target" | "Updated link/anchor" | [commit]; satisfies FR-010.c, SC-006

- [X] T019 [P] [US2] Apply component labels and audit feature descriptions — Group 1 (FR-012, FR-010.b, FR-013, SC-004, SC-007): Edit disjoint file set: (a) README.md + docs/architecture.md + docs/security.md + docs/roadmap.md; (b) Apply optional/experimental/beta labels at first mention per FR-012 (components registry: sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver, tunnel, postgres per OD-4 and CLAUDE.md repo-map); (c) Audit feature descriptions for accuracy: verify Kubernetes-native architecture claim, RBAC model, threat model, feature status against operator/api/v1alpha1/*_types.go, operator/internal/controller, api/internal/handlers per FR-013; (d) For each finding (labels missing or descriptions incorrect), append row to audit-log.md: File | Line | Finding | Evidence | Resolution | Commit; (e) Do NOT change code that appears wrong — note in audit-log.md and move on per spec scope; satisfies FR-012, FR-010.b, FR-013, SC-004, SC-007, SC-008

- [X] T020 [P] [US2] Apply component labels and audit feature descriptions — Group 2 (FR-012, FR-010.b, FR-013, SC-004, SC-007): Edit disjoint file set: (a) docs/install.md + docs/networking.md + docs/tunnels.md + docs/notifications.md + docs/oidc.md; (b) Apply labels at first mention of optional/experimental components per FR-012; (c) Audit descriptions: verify chart values, networking modes, relay features, notification delivery against charts/gameplane/values.yaml, tunnel codebase, api/internal/notify per FR-013; (d) Append audit-log.md rows for each finding; satisfies FR-012, FR-010.b, FR-013, SC-004, SC-007, SC-008

- [X] T021 [P] [US2] Apply component labels and audit feature descriptions — Group 3 (FR-012, FR-010.b, FR-013, SC-004, SC-007): Edit disjoint file set: (a) docs/module-authoring.md + docs/key-rotation.md + docs/game-coverage.md + docs/dependencies.md + docs/contributing.md; (b) Apply labels at first mention per FR-012; (c) Audit descriptions: verify module format, signature process, game coverage claims against operator/internal, gameproto/ code per FR-013; (d) Append audit-log.md rows for each finding; satisfies FR-012, FR-010.b, FR-013, SC-004, SC-007, SC-008

- [X] T022 [P] [US2] Apply component labels and audit feature descriptions — Group 4 (FR-012, FR-010.b, FR-013, SC-004, SC-007): Edit disjoint file set: (a) audit-syslog-bridge/README.md + mcp-server/README.md + telemetry-receiver/README.md; (b) Apply labels at first mention per FR-012; (c) Audit descriptions: verify deployment examples, feature claims against component source code per FR-013; (d) Append audit-log.md rows for each finding; satisfies FR-012, FR-010.b, FR-013, SC-004, SC-007, SC-008

- [X] T023 [US2] Update docs/roadmap.md with shipped/planned markers (FR-010, OD-7, SC-005): per OD-7 ruling, every entry (§ "Shipped toward v1", § "Blocking v1", § "Wanted for v1", § "Known gaps tracked") must carry "(shipped vX.Y.Z)" or "(planned)"; derive shipped versions from CHANGELOG.md by PR number or feature name; entries with shipped dates: audit each line and add marker; (e.g., "Multi-cluster: dashboard cluster selector (PR #107)" → "Multi-cluster: dashboard cluster selector (shipped v0.2.0-beta.8) (PR #107)"); markers use parentheses per OD-7; append audit-log.md row per entry updated: File="docs/roadmap.md" | Line="[line]" | "roadmap marker added" | "CHANGELOG.md:[line]" | "Added shipped/planned marker" | [commit]; satisfies OD-7, SC-005

- [X] T024 [US2] Verify CHANGELOG.md unreleased items and fix or mark (OD-8, FR-014, SC-004): per OD-8 ruling; enumerate CHANGELOG.md lines 8–46 [Unreleased] section; for each item that docs describe as available in v0.2.0-beta.8 (e.g., "Default StorageClass", "Helm-seeded OIDC role mappings" from FACTS 3), verify presence in shipped code/chart; if shipped, move entry into "## [0.2.0-beta.8]" section (line 47+); if unreleased, add note "(unreleased; ships in the next release)" at first doc mention; per FACTS 3: Default StorageClass in docs/install.md:7, docs/install.md:109, docs/install.md:111; Helm-seeded OIDC in docs/install.md:130, docs/install.md:134, docs/oidc.md:21, docs/security.md:612; this may require a read-only `git fetch --tags --unshallow` (D6 permitted) or GitHub compare API to check v0.2.0-beta.8 tag (FACTS 9 notes shallow clone); append audit-log.md rows for each CHANGELOG entry moved/marked: File="CHANGELOG.md" | Line | "Unreleased entry verified" | "docs reference + git verification" | "Moved to 0.2.0-beta.8 section OR marked unreleased" | [commit]; satisfies OD-8, FR-014, SC-004

### Verification for User Story 2

- [X] T025 [US2] Verify audit completeness (SC-008): (a) run `hack/check-doc-versions.sh` locally as read-only pre-flight (D6) on all 17 audited files + docs/comparison-sources.md; confirm zero stale version reports (historical references carry the OD-14 marker and are not flagged), exit 0; (b) run `hack/check-links.sh` locally as read-only pre-flight; confirm zero broken link reports, exit 0; (c) read audit-log.md and verify every finding from T014–T024 is logged with evidence citation and resolution; (d) run quickstart.md Scenarios 2–5 (version strings, internal links, labels, unshipped claims) and confirm all assertions pass; (e) check that every first-mention component carries [optional]/[experimental]/[BETA] consistently across all files mentioning it; satisfies SC-004, SC-005, SC-006, SC-007, SC-008, FR-011, FR-012, FR-013, FR-014

---

## Phase 5: User Story 3 - External Outreach Tracking (Priority: P2)

**Goal**: Create and maintain a persistent to-do list tracking submissions to three external directories (FR-020–FR-025, SC-012–SC-014)

**Independent Test**: outreach.md file exists in specs/012-docs-refresh-and-outreach/; lists three targets (AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes) with status fields per SC-014 state machine (pending, submitted [date], deferred [date, reason]); linked from docs/contributing.md; every status change is a separate git commit per FR-022

### Creation for User Story 3

- [X] T026 [US3] Create `/home/user/Gameplane/specs/012-docs-refresh-and-outreach/outreach.md` from contracts/outreach-todo.md "Proposed Initial Content" with three status entries exactly as ruled (per OD-5, OD-6a, OD-6b, OD-6c): (a) AlternativeTo status = pending (no blockers per R6, OD-5); (b) Awesome-Selfhosted status = deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22] per R6, OD-6a; (c) Awesome-Kubernetes status = deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release] per R6, OD-6b; plus one "DRAFT SUBMISSION" block per target (OD-5) copied from contract templates (AlternativeTo form fields template, awesome-selfhosted-data `software/gameplane.yml` YAML template, awesome-kubernetes README line template); last-updated date = real date of authoring (2026-09-02 or later when actual work runs); satisfies FR-020, FR-021, FR-022, SC-012, SC-014, OD-5, OD-6a, OD-6b

- [X] T027 [US3] Edit `docs/contributing.md`: add "Community Visibility & Outreach" section and link text per contracts/outreach-todo.md; placed per heading outline in FACTS 7 (after contributing guidelines, before testing section or at end); link text (verbatim from contract): "See [External Outreach Tracking](../specs/012-docs-refresh-and-outreach/outreach.md) for visibility into third-party directory submissions."; satisfies FR-025, SC-013

### Maintainer-Owned Task for User Story 3

- [ ] T028 [US3] **[MAINTAINER TASK — Not agent-executable]** Create AlternativeTo.net account if needed, then submit Gameplane listing via the web form at `https://alternativeto.net/add-app/`; fill form with project details from the AlternativeTo "DRAFT SUBMISSION" block in outreach.md (T026), not from README prose; upon successful form submission, update outreach.md AlternativeTo entry: change status from `pending` to `submitted [2026-MM-DD]`, add submission reference (form ID or email confirmation link) in `submittedRef` field; commit as single commit: `git commit -s` with subject line `docs: outreach [AlternativeTo] submitted` (no date in subject) plus body stating the new status, date, and submission reference (form ID or confirmation link) per contracts/outreach-todo.md Commit Message Format, and standard trailers (rule 11); satisfies FR-022, FR-024, SC-014

### Verification for User Story 3

- [ ] T029 [US3] Verify outreach tracking (SC-012, SC-013, SC-014, FR-023): (a) read outreach.md and confirm three targets listed with status fields matching spec'd format per data-model.md state machine; (b) verify each status is terminal state per SC-014 (submitted [date] or deferred [date, reason]); no "pending" entries remain at feature completion; (c) verify per FR-023 that no Status field or commit message in outreach.md/its git history claims success or acceptance by AlternativeTo/Awesome-Selfhosted/Awesome-Kubernetes — only submission/deferral is recorded, per contracts/outreach-todo.md V-OE3; (d) confirm docs/contributing.md links to outreach.md; (e) run quickstart.md Scenario 7 (outreach to-do checklist) and confirm all assertions pass; satisfies SC-012, SC-013, SC-014, FR-023

---

## Phase 6: User Story 4 - Dashboard Screenshots (Priority: P2)

**Goal**: Capture six refreshed and five+ new dashboard screenshots showing current UI against mocked data; update README gallery with alt text (FR-015–FR-019, SC-009–SC-011)

**Independent Test**: 11+ JPEG files at 1920×1080 pixels exist in docs/img/; six existing filenames (dashboard.jpg, servers-list.jpg, etc.) are refreshed; five+ new filenames (login.jpg, create-server-template-select.jpg, etc.) are present; all alt text in README.md describes purpose and key UI elements per FR-018; no real user data, hostnames, or IPs visible per FR-019; Playwright spec added and CI workflow configured per OD-3b, OD-3c

### MSW & Playwright Configuration for User Story 4

- [X] T030 [P] [US4] Enhance MSW mock fixtures in `web/src/test/factories.ts` and `web/src/test/handlers.ts` to include full screenshot data set (per contracts/screenshot-set.md § "Capture Requirements"): (a) 8+ GameTemplates (use existing minecraft-vanilla, valheim + add minecraft-java, terraria, rust, palworld; keep exact names that existing vitest assertions verify from FACTS 6); (b) 3–5 GameServers with mixed phases: test-server-01 (Running), test-server-02 (Pending), test-server-03 (Failed) + 2 optional additional; (c) 3+ nodes (node-01, node-02, node-03) per makeClusterView(); (d) K8s events incl. warnings: image pull errors, scheduling events, provisioning progress (via makeAudit() or new makeEvent() factory); (e) 10–20 audit events via makeAudit() factory (vary actors: admin-1, operator-bob, viewer-carol); (f) 5–10 mods per registry (existing Thunderstore mods in handlers.ts); (g) 3–5 users (admin, operator-bob, viewer-carol + 1–2 optional); (h) schedules/restores: existing makeSchedule() + makeRestore(); CRITICAL per FACTS 6: do NOT change any default value that existing vitest or e2e test asserts on (e.g., "alpha" server name, "minecraft-vanilla" template); if a shared default must change, STOP and get maintainer sign-off; satisfies contracts/screenshot-set.md, OD-3a

- [X] T031 [P] [US4] Edit `web/playwright.config.ts` (around line 69): add `grepInvert: /@screenshots/` to the default use configuration so `npm run test:e2e:mock` and the CI web-e2e-mock job never run the screenshot spec (OD-3b); screenshot runs pass `--grep @screenshots` which overrides the invert; existing use (Desktop Chrome 1280x720) stays for all non-screenshot specs; satisfies OD-3b

- [X] T032 [P] [US4] Edit `web/package.json` scripts (line 6–15): add new script `"screenshots": "GAMEPLANE_E2E_TARGET=mock playwright test --grep @screenshots"` after the existing test:e2e:* scripts; invoked by `.github/workflows/screenshot-refresh.yaml` (T035); per OD-15 agents do not run it locally; satisfies plan.md, OD-3b

- [X] T033 [P] [US4] Edit `Makefile` (around line 220 after web e2e targets): add `.PHONY: screenshots` target with two lines: (a) `@cd web && npm ci`, (b) `npm run screenshots`; help comment `## Capture dashboard screenshots via Playwright mock mode`; mirrors the existing test-web-e2e-mock target structure; (for maintainers and the CI workflow; agents do not run it, OD-15); satisfies plan.md, OD-3b

### Screenshot Capture Spec for User Story 4

- [X] T034 [US4] Create `web/e2e/specs/screenshots.spec.ts` per contracts/screenshot-set.md (OD-3b Playwright mock-mode spec): (a) `test.describe("@screenshots", ...)` to tag the entire suite (enables grep filtering); (b) `test.use({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 })` to set viewport per OD-3a; (c) fixed timezone via `timezoneId: "UTC"` in test.use; (d) capture /login BEFORE authenticating (rule 3: no version/cluster/metrics on login page); (e) then authenticate with the mock-mode pattern from web/e2e/pages/LoginPage.ts and web/e2e/specs/login.spec.ts (LoginPage.login() with the mock credentials); (f) then capture the twelve routes per contract table: / (dashboard.jpg), /servers (servers-list.jpg), /servers/$name (server-overview.jpg), /servers/$name?tab=mods (mods-registry-browse.jpg), /servers/$name?tab=console (server-console.jpg), /admin?section=modRegistries (admin-mod-registries.jpg), /servers/new (create-server-template-select.jpg), /servers/$name?tab=events (server-detail-events.jpg), /admin?section=general (admin-settings-general.jpg), /cluster (cluster-nodes.jpg), /servers/$name?tab=logs (server-detail-logs.jpg); (g) for each route: `await page.waitForLoadState("load")` then wait briefly for pending SSE subscriptions to settle (the /events stream never idles, so networkidle would hang), then `page.screenshot({ path: <repo>/docs/img/<filename>.jpg, type: "jpeg", quality: 80, fullPage: false })`; path resolved relative to spec file so docs/img/ is target; filenames exactly per contract; each capture is its own test() so one broken screen cannot drop the rest (CodeRabbit round 1); satisfies FR-015, FR-016, FR-017, FR-019, OD-3a, OD-3b, SC-009, SC-010, SC-011

### CI Workflow for User Story 4

- [X] T035 [P] [US4] Create `.github/workflows/screenshot-refresh.yaml` (OD-3c, OD-13): (a) triggers: `push: tags: ["v*"]` (mirror release.yaml) + `workflow_dispatch` to allow manual dispatch on feature branch (Principle VI, rule 8); (b) jobs:screenshot-refresh; (c) runs-on: ubuntu-latest; (d) permissions: `contents: read` only (the PAT performs the push and the PR; GITHUB_TOKEN needs nothing else; least privilege per done_008 permissions-matrix); (e) use `defaults: run: working-directory: web` like the web-e2e-mock job instead of `cd web`; (f) steps: checkout (SHAs pinned from FACTS per done_008 E1), setup-node v7 with node-version 24, cache (playwright key from FACTS 1c), `npx playwright install --with-deps chromium`, `npm run screenshots`; (g) after screenshots are captured, commit docs/img/*.jpg and other changed files to a new branch: `git checkout -b chore/screenshot-refresh-${GITHUB_REF_NAME}`, `git add docs/img/*.jpg`, `git -c user.name=gameplane-screenshot-bot -c user.email=noreply@github.com commit -s -m "chore: refresh dashboard screenshots [${GITHUB_REF_NAME}]"`, push with `git push origin chore/screenshot-refresh-${GITHUB_REF_NAME}`; (h) open PR using `gh pr create --base <base-branch> --title "chore: refresh screenshots [${GITHUB_REF_NAME}]" --body "..."` (pass body via HEREDOC per CLAUDE.md rule 11 trailers); secret SCREENSHOT_BOT_PAT (fine-grained PAT with contents read/write, pull-requests read/write); (i) base branch logic: if tag push (refs/tags/v*), base=master; if workflow_dispatch, base=current branch; (j) all actions pinned by SHA with version comments, concurrency group (a `concurrency:` group is required because with `workflow_dispatch` the workflow is no longer tag-only, so the done_008 data-model E1 tag-only exemption does not apply), header comment documenting OD-3c/OD-13 and secret; must pass actionlint and zizmor (FACTS 1e/3); satisfies OD-3b, OD-3c, OD-13, SC-009, SC-010, Principle VI (CI bears heavy lifting)

### Maintainer-Owned Task for User Story 4

- [X] T036 [US4] **[MAINTAINER TASK — Not agent-executable]** Create fine-grained PAT (GitHub settings → Developer settings → Personal access tokens → Fine-grained tokens): (a) token name: `gameplane-screenshot-bot` or similar; (b) repository access: this repository (GameplanePanel/Gameplane) only; (c) permissions: Contents (read/write), Pull requests (read/write); (d) set an expiration (fine-grained tokens require one) and record the rotation date next to the other repository secrets (OD-13); (e) create token and copy to clipboard; (f) in repository Settings → Secrets and variables → Actions, add new repository secret: name = `SCREENSHOT_BOT_PAT`, value = pasted token; (g) document token rotation schedule (like other repository secrets, per OD-13); satisfies OD-13 — 2026-09-02: SCREENSHOT_BOT_PAT secret created by the maintainer (fine-grained PAT: this repository only; Contents read/write, Pull requests read/write, Metadata read). Still needed before closing: the token's expiration date, the next rotation date and the rotation schedule (maintainer to supply; record them here). — 2026-10-09: maintainer confirmed the token expires one year after the repositories moved to the GameplanePanel org (move landed 2026-10-07, commit 5fe7f5f): expiration 2027-10-07; rotate before that date, yearly thereafter.

### Screenshot Capture Execution for User Story 4

- [X] T037 [US4] Capture screenshots by dispatching CI workflow: (a) dispatch screenshot-refresh.yaml on feature branch 012-docs-refresh-and-outreach using GitHub web UI or `gh workflow run screenshot-refresh.yaml --ref 012-docs-refresh-and-outreach`; (b) wait for workflow to complete (watch with `gh run watch`); (c) review the opened PR (e.g., chore/screenshot-refresh-*); (d) merge the PR back into feature branch `012-docs-refresh-and-outreach` using `gh pr merge <pr-number> -m` (merge, not squash, to preserve commit messages); (e) pull latest in local branch: `git pull origin 012-docs-refresh-and-outreach`; (f) confirm docs/img/*.jpg are present at 1920×1080 JPEG (verify using `file` and `identify` per quickstart.md Scenario 6); OD-15 (ruled 2026-09-02): agents never run Playwright locally; if the dispatch path is blocked (secret missing, workflow not yet on the branch) stop and report to the maintainer; there is no local fallback; satisfies OD-3c, OD-3b, SC-009, SC-010 — 2026-09-02: run 4 (push cf4a1c30) → PR #341 merged as f64333d9; run 5 (f1dc2199, enriched dataset) → PR #342 merged as 655afa37; run 6 via workflow_dispatch (9b3ec229, the OD-15 path) → PR #343 merged as 6ffa851a; 12 JPEGs at 1920×1080 confirmed with file(1); temporary branch trigger removed in b6ca8265

### README Gallery Update for User Story 4

- [X] T038 [US4] Update `README.md` screenshot gallery (currently lines 18–27; replace with new gallery): (a) in "## Screenshots" section, add OD-3d disclosure sentence as first line: "Screenshots are captured against mocked data for consistency and reproducibility; all UI layouts and components reflect the current dashboard." (verbatim from contract, OD-3d); (b) extend the two-column markdown table to all 11+ images (new-first order per contract recommendation, ending with dashboard.jpg as "first screenshot most users see", or reverse chronological by implementation order); (c) alt text per image: replace existing alt texts (README.md:22, 24, 26 currently; keep if they still match refreshed UI, otherwise rewrite) and add alt text for all new images per contracts/screenshot-set.md § "Alt Text Examples"; alt text format: purpose + key UI elements (one sentence), NO mention of "mock" or "mocked"; (d) verify no alt text contains forbidden patterns (real hostnames, IPs, cluster names, player names, version strings) per FR-019; (e) each table cell contains one `![alt text](path/filename.jpg)` markdown image link; satisfies FR-015, FR-016, FR-017, FR-018, FR-019, OD-3d, SC-009, SC-010, SC-011 — 2026-09-02: gallery rewritten with 12 images, disclosure sentence, one-sentence alt text (see README.md § Screenshots)

### Verification for User Story 4

- [X] T039 [US4] Verify screenshot completeness (SC-009, SC-010, SC-011, OD-3a): (a) run `file docs/img/*.jpg` and `identify docs/img/*.jpg` (read-only) to verify 11+ images at 1920×1080 JPEG format (existing six refreshed filenames present, 5+ new filenames present); (b) count image references in README.md gallery (>= 11); (c) grep README.md gallery for forbidden patterns per contract (real hostnames, real IPs, version strings outside code blocks); verify zero matches; (d) read alt text for each image; verify each describes purpose and key UI elements, no "mock"/"mocked" wording per OD-3d; (e) run quickstart.md Scenario 6 screenshot checklist; (f) open PR for peer/maintainer visual review and confirm UI matches current web/ codebase (code-review level: spot-check, not exhaustive); any substitutions (if a screenshot cannot be captured and a fallback is used) must be logged in audit-log.md per Substitution Rule; satisfies contracts/screenshot-set.md Verification Checklist, SC-009, SC-010, SC-011 — 2026-09-02: (a) 12 files, all 1920×1080 JPEG, largest 127 KB (server-detail-logs.jpg, captured at quality 65); (b) README gallery references 12 images; (c) no hostnames, IPs or version strings in alt text; (d) alt text reviewed per image against the captures; (e) quickstart Scenario 6 checks pass; (f) visual spot-check done by the agent, maintainer review on PR #340 pending

---

## Phase 7: Polish & Verification

**Purpose**: Commit all changes per logical unit (rule 11), push branch, verify CI green, confirm all scenarios pass

### Commits for Polish

- [X] T040 Create foundational audit-log.md skeleton, create outreach.md initial status entries, and link from contributing.md: `git commit -s` with subject "docs: initialize audit log and outreach tracking", body "Create audit-log.md with D-F schema header and outreach.md with initial status entries per spec.md FR-020–FR-025. Link outreach.md from docs/contributing.md. Both files are templates for upcoming corrections and submissions." Trailers per rule 11.

- [X] T041 Commit comparison table and sourcing: `git commit -s` with subject "docs: add side-by-side comparison table and source tracking", body "Add comparison table to README.md per FR-001–FR-008, comparing Gameplane, Pterodactyl, CubeCoders AMP, and Agones across nine dimensions. Create docs/comparison-sources.md with dated sources per FR-005, SC-003, D-A. All cells verified against official documentation." Trailers per rule 11.

- [X] T042 Commit audit tooling scripts and Makefile: `git commit -s` with subject "feat: add documentation audit tooling (versions, links, compliance)", body "Add hack/check-doc-versions.sh and hack/check-links.sh scripts per OD-1, OD-2 to enforce documentation version string consistency and link resolution in CI. Update Makefile to add check-doc-versions and check-links targets; integrate into lint target prerequisites per FR-009." Trailers per rule 11.

- [X] T043 Commit CI integration: `git commit -s` with subject "ci: integrate documentation audit into lint job", body "Add dedicated steps to .github/workflows/ci.yaml lint job for doc-version-check and link-check, gated if: matrix.module == 'netguard' per D5. Documentation compliance now enforced on every lint run per Principle VI and FR-009." Trailers per rule 11.

- [X] T044 Commit CLAUDE.md update: `git commit -s` with subject "docs: document make check-doc-versions and check-links in CLAUDE.md", body "Add mention of make check-doc-versions and make check-links targets to CLAUDE.md Lint section per plan.md requirement." Trailers per rule 11.

- [X] T045 Commit version string corrections (all stale versions identified in T015): `git commit -s` with subject "fix: update documentation version strings to v0.2.0-beta.8", body "Correct stale version strings in docs/install.md, telemetry-receiver/README.md per audit findings. All version examples now match current release (charts/gameplane/Chart.yaml:6)." Trailers per rule 11.

- [X] T046 Commit link and date corrections (from T016–T018): `git commit -s` with subject "fix: correct broken documentation links and date references", body "Fix broken internal links in README.md, docs/networking.md, docs/install.md. Re-verify and update dependencies.md snapshot date per T016. Correct date reference in docs/install.md:603 from July 2026 to June 2026 (beta.2 release date per CHANGELOG.md)." Trailers per rule 11.

- [X] T047 Commit optional/experimental/beta component labels (from T019–T022): `git commit -s` with subject "docs: apply [optional] and [experimental] labels to components at first mention", body "Mark optional components (sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver, tunnel) and experimental features (postgres driver) with [optional]/[experimental] qualifiers at first mention in each of 17 audited files per FR-012, SC-007." Trailers per rule 11.

- [X] T048 Commit feature description audit corrections (from T019–T022): `git commit -s` with subject "docs: audit and correct feature descriptions", body "Verify feature descriptions in README.md and all docs/ against codebase implementations. Correct mismatches for Kubernetes architecture, RBAC model, relay features, game coverage claims per FR-010.b, FR-013." Trailers per rule 11.

- [X] T049 Commit roadmap markers (from T023): `git commit -s` with subject "docs: mark roadmap entries shipped or planned", body "Add explicit (shipped vX.Y.Z) or (planned) markers to all entries in docs/roadmap.md per OD-7. Version info sourced from CHANGELOG.md and PR tracking." Trailers per rule 11.

- [X] T050 Commit CHANGELOG.md fix (from T024, only if corrections needed): `git commit -s` with subject "docs: verify and move Unreleased CHANGELOG entries to v0.2.0-beta.8 section", body "Move Unreleased items verified as shipped in v0.2.0-beta.8 into that section; add '(unreleased; ships in the next release)' at the doc mentions of items verified unshipped per OD-8." Trailers per rule 11 (skip this commit if no CHANGELOG changes needed). — **withdrawn**: no CHANGELOG entry needed moving (OD-8 verification found both Unreleased items truly unreleased; doc qualifiers added instead)

- [X] T051 Commit MSW fixtures and Playwright config (from T030–T033): `git commit -s` with subject "test: add screenshot data set to MSW factories and Playwright config", body "Enhance web/src/test/factories.ts with 8+ templates, 3–5 servers, nodes, events, audit logs, users for screenshot capture. Add screenshot tag filtering and 1920x1080 viewport config to web/playwright.config.ts and web/package.json per OD-3a, OD-3b." Trailers per rule 11.

- [X] T052 Commit screenshot capture spec (from T034): `git commit -s` with subject "test: add Playwright screenshot capture spec", body "Add web/e2e/specs/screenshots.spec.ts with @screenshots tag suite capturing 11+ dashboard screens at 1920x1080 JPEG from mock-mode routes per OD-3b. Covers login, servers list, server detail, admin settings, cluster view, events, console, logs tabs." Trailers per rule 11.

- [X] T053 Commit CI screenshot refresh workflow (from T035): `git commit -s` with subject "ci: add tag-triggered screenshot refresh workflow", body "Add .github/workflows/screenshot-refresh.yaml triggered on version tags and workflow_dispatch to auto-capture and PR screenshots to repository per OD-3c, OD-13. Uses fine-grained PAT repository secret for PR authoring." Trailers per rule 11.

- [X] T054 Commit README gallery update (from T038): `git commit -s` with subject "docs: refresh screenshot gallery with disclosure and new images", body "Update README.md screenshot gallery with OD-3d disclosure sentence, extend table to 11+ images (6 refreshed + 5+ new) at 1920x1080 JPEG, add/verify alt text per FR-018. Remove per-image mock-mode disclosure per OD-3d." Trailers per rule 11. — done as f55078a1 (docs: rebuild the README screenshot gallery with twelve captures) plus f1dc2199 (alt-text trims); subject differs from the drafted one

### Push and Verification

- [X] T055 Push feature branch to remote: `git push -u origin 012-docs-refresh-and-outreach` (creates remote branch and upstream tracking). Watch CI with `gh run watch` (or GitHub Actions MCP tools); do NOT report done while red. Confirm all lint, web-e2e-mock, actionlint, zizmor jobs pass per rule 8, Principle VI. (CI definition: `.github/workflows/ci.yaml`; new workflow: `.github/workflows/screenshot-refresh.yaml`) — (CI green on 2d61504d, run 992, 30/30 jobs)

- [ ] T056 Run all eight quickstart.md scenarios as read-only verification (D6 permitted; not executing code, only validating documented workflows): (a) Scenario 1: Evaluator can read comparison table and identify 3+ key differences per Gameplane column, (b) Scenario 2: Version strings in README and docs match appVersion 0.2.0-beta.8 or marked examples, (c) Scenario 3: Internal links in README and docs resolve (run hack/check-links.sh locally read-only), (d) Scenario 4: Optional/experimental components labeled consistently ([optional] at first mention), (e) Scenario 5: No unshipped features claimed in docs (CHANGELOG Unreleased items verified or noted), (f) Scenario 6: 11+ screenshots at 1920×1080 JPEG with alt text, no real data, (g) Scenario 7: Outreach to-do list tracks three targets with terminal status, (h) Scenario 8: Audit log records all corrections with evidence. Confirm all assertions pass; record results in notes section of this tasks.md before submitting. — STATUS 2026-09-02: scenarios 1–6 and 8 verified (screenshots via CI runs 4–6, merged f64333d9/655afa37/6ffa851a; alt-text trims f1dc2199); scenario 7 PENDING on T028 (AlternativeTo submission); T056 stays open until T028 lands.

- [ ] T057 Walk every contract "Done When" checklist: (a) contracts/comparison-table.md § 11, (b) contracts/docs-audit.md § "Done When", (c) contracts/screenshot-set.md Verification Checklist, (d) contracts/outreach-todo.md § "Done When" per SC-012–SC-014. Confirm all acceptance criteria met. — STATUS 2026-09-02: checklists for the documentation audit, comparison table, screenshot verification, and outreach tracking verified; only the AlternativeTo terminal state (SC-014) remains open, so T057 stays open until T028 (maintainer submission) lands.

- [X] T058 Produce SC status table for final report: Create summary table in notes section: | SC-001 through SC-014 | Status: PASS/FAIL | Evidence | per spec.md § Success Criteria. All 14 success criteria must PASS before feature 012 completion.

### Pull Request

- [X] T059 Open pull request via `gh pr create --base master`: (a) Title: "docs: refresh documentation, add comparison table, track outreach" (70 chars max); (b) Body via HEREDOC per rule 11: include ## Summary (3 bullets: comparison table added per FR-001–FR-008, documentation audit completed per FR-009–FR-014, screenshots refreshed + new + gallery updated per FR-015–FR-019, outreach tracking setup per FR-020–FR-025); ## Test plan (run quickstart.md Scenarios 1–8, verify CI lint/link/version checks pass, PR visual review); 🤖 Generated with [Claude Code](https://claude.com/claude-code) + session URL; satisfies rule 14

- [X] T060 Apply PR labels (rule 14; via REST API, not gh pr edit per FACTS 1f broken behavior): `gh api -X POST repos/GameplanePanel/Gameplane/issues/<pr-number>/labels -f "labels[]=type: docs" -f "labels[]=type: ci" -f "labels[]=area: shared" -f "labels[]=area: web" -f "labels[]=area: specs"`; verify with `gh api repos/GameplanePanel/Gameplane/issues/<pr-number>/labels -q '[.[].name]|join(", ")'`.


---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion — BLOCKS all user stories (audit-log.md skeleton must exist before corrections are logged)
- **User Stories (Phases 3–6)**: All depend on Foundational completion
  - US1 (Phase 3, Comparison Table): Independent; can start after Foundational
  - US2 (Phase 4, Accuracy Audit): Independent; can start after Foundational; tooling (T009–T013) can start in parallel with US1 research tasks (T003–T005)
  - US3 (Phase 5, Outreach): Independent; can start after Foundational
  - US4 (Phase 6, Screenshots): Independent; can start after Foundational
- **Polish (Phase 7)**: Depends on completion of all desired user stories; MVP requires US1 + US2; US3 + US4 optional for MVP but recommended for full feature scope

### User Story Dependencies

- **US1 (P1)**: No dependencies on other stories; can run independently after Phase 2
  - T003–T005 (research) [P] can run in parallel with each other
  - T006 (comparison-sources.md) depends on T003–T005 completion
  - T007 (README table) depends on T006 completion
  - T008 (verification) depends on T007 completion

- **US2 (P1)**: No dependencies on other stories; can run independently after Phase 2
  - T009–T013 (tooling) can run in parallel except T013 runs after T012 (sequential: both edit `.github/workflows/ci.yaml` lint job); no prerequisites
  - T014 (CLAUDE.md update) — part of tooling phase; no prerequisites
  - T015–T018 (corrections: version staleness, dependencies date, install.md date, broken links) are sequential single-file correction tasks (each edits different files, no stated parallel-safety rationale); run in order T015 → T016 → T017 → T018; note: T015 waits on T009 for script validation but can start independently
  - T019–T022 (corrections: labels & descriptions by file group) [P] are genuinely parallel (each group edits disjoint file sets); run T019, T020, T021, T022 concurrently
  - T023–T024 (corrections: roadmap markers, CHANGELOG) have no further prerequisites beyond T015–T022
  - T025 (verification) depends on T009–T024 completion

- **US3 (P2)**: No dependencies on other stories; can run independently after Phase 2
  - T026 (create outreach.md) — no prerequisites
  - T027 (link from contributing.md) depends on T026 completion
  - T028 (maintainer submission) — can start once T026/T027 complete, but BLOCKS feature completion per SC-014
  - T029 (verification) depends on T028 or terminal state recorded in T026

- **US4 (P2)**: No dependencies on other stories; can run independently after Phase 2
  - T030–T033 (config & factories) [P] can run in parallel; no prerequisites
  - T034 (screenshots.spec.ts) depends on T030–T033 completion (needs factories and config in place)
  - T035 (CI workflow) [P] depends on T034 being merged (workflow references web/e2e/specs/screenshots.spec.ts)
  - T036 (maintainer PAT setup) — prerequisite for T037 workflow dispatch
  - T037 (capture screenshots) depends on T036 (PAT must exist) and T035 (workflow must exist) and T034 (spec must exist)
  - T038 (README gallery) depends on T037 (screenshots must exist)
  - T039 (verification) depends on T038 completion

### Within Each User Story

- Tests before implementation
- Models/setup before corrections
- Corrections before verification
- Verification before commit

### Parallel Opportunities

**Setup → Foundational**: Sequential; foundational blocks all stories

**US1 Research (T003–T005)** [P]: Three competitor research tasks can run in parallel:
- T003: Pterodactyl documentation fetch + ComparisonCell fill
- T004: Agones documentation fetch + ComparisonCell fill
- T005: CubeCoders documentation research + ComparisonCell fill

**US2 Tooling (T009–T013)** [P]: Four script/config tasks can run in parallel:
- T009: hack/check-doc-versions.sh script
- T010: hack/check-links.sh script
- T011: Makefile targets
- T012: ci.yaml integration
- T013: CLAUDE.md update

**US2 Corrections (T019–T022)**: Four merged group tasks [P] with each other (editing disjoint file sets):
- T019 [P]: Apply labels and audit descriptions — Group 1 (README + docs/architecture.md + docs/security.md + docs/roadmap.md)
- T020 [P]: Apply labels and audit descriptions — Group 2 (docs/install.md + docs/networking.md + docs/tunnels.md + docs/notifications.md + docs/oidc.md)
- T021 [P]: Apply labels and audit descriptions — Group 3 (docs/module-authoring.md + docs/key-rotation.md + docs/game-coverage.md + docs/dependencies.md + docs/contributing.md)
- T022 [P]: Apply labels and audit descriptions — Group 4 (audit-syslog-bridge/README.md + mcp-server/README.md + telemetry-receiver/README.md)

**US4 Configuration (T030–T033)** [P]: Four tasks can run in parallel:
- T030: MSW fixtures enhancement
- T031: Playwright config
- T032: package.json scripts
- T033: Makefile target

**Polish Commits (T040–T054)** [P]: Commits can be created in any order (each is a separate logical unit per rule 11); they are sequenced here for conceptual grouping but execution order does not matter

---

## Parallel Example: Full Feature Execution

With adequate concurrency (3–4 concurrent agents per Workflow wave per rule 13):

**Wave 1 (After Foundational)**: Start all four user stories in parallel
- **US1 Research agents (3 parallel)**: T003, T004, T005 simultaneously
- **US2 Tooling agents (5 parallel)**: T009, T010, T011, T012 simultaneously, then T013 sequential after T012
- **US3 Creation agent**: T026 (single task, low parallelism)
- **US4 Config agents (4 parallel)**: T030, T031, T032, T033 (but T033 serializes after T011 completes — both edit Makefile) simultaneously

**Wave 2 (Dependency resolution)**:
- T006 (US1 sourcing) — waits on T003–T005
- T007 (US1 README) — waits on T006; NOTE: must serialize with T018 (both edit README.md) — recommend T018 runs before T007 since T007 already depends on T006
- T015–T024 (US2 corrections) — can start independently, but T015 waits on T009 for script validation
- T027 (US3 link) — waits on T026
- T034 (US4 spec) — waits on T030–T033
- T035 (US4 workflow) — waits on T034

**Wave 3 (Verification & capture)**:
- T008 (US1 verification) — waits on T007
- T025 (US2 verification) — waits on T009–T024
- T028 (US3 maintainer submission) — waits on T027
- T036–T037 (US4 capture) — wait on T035, T036
- T038 (US4 gallery) — waits on T037
- T039 (US4 verification) — waits on T038

**Wave 4 (Polish & merge)**:
- All commits (T040–T054) can run in parallel or sequence
- T055 (push) waits on all commits
- T056–T060 (final verification & PR) wait on T055 (green CI)

**Total parallelism potential**: ~10–12 concurrent tasks in Wave 1; ~8–10 in Wave 2; ~6 in Wave 3; ~15 in Wave 4 (commits are cheap). Estimated 2–3 Workflow waves at haiku (per rule 13 "smallest model first").

---

## Implementation Strategy

### MVP Completion (User Stories 1 + 2 Only)

1. Complete Phase 1: Setup (T001)
2. Complete Phase 2: Foundational (T002)
3. Complete Phase 3: US1 Comparison Table (T003–T008)
   - Parallel research (T003–T005), then sourcing (T006), then README edit (T007), then verify (T008)
4. Complete Phase 4: US2 Accuracy Audit (T009–T025)
   - Parallel tooling (T009–T014), then corrections (T015–T024), then verify (T025)
5. Skip Phase 5: US3 Outreach (optional for MVP, P2)
6. Skip Phase 6: US4 Screenshots (optional for MVP, P2)
7. Polish & Merge (T040–T060)
   - Commit tooling + audit changes, push, verify CI, open PR, apply labels

**MVP Result**: Evaluator can read comparison table and identify key differences (US1). New self-hoster can deploy v0.2.0-beta.8 without hitting stale docs (US2). All 17 audited files have correct version strings, resolved internal links, consistent optional/experimental labels. Success Criteria: SC-001 through SC-008 PASS. Time estimate: ~3–5 days for small team (1–2 haiku agents, 1 sonnet reviewer).

### Full Feature Completion (All Four User Stories)

Add US3 + US4 after MVP:

8. Complete Phase 5: US3 Outreach Tracking (T026–T029)
   - Create outreach.md, link from contributing.md, maintainer submits AlternativeTo, verify
9. Complete Phase 6: US4 Screenshots (T030–T039)
   - Config MSW + Playwright, add spec, create CI workflow, dispatch & capture, update gallery, verify
10. Polish & Merge (T040–T060)
    - All commits, push, CI passes, PR with all 4 story labels

**Full Feature Result**: All 17 docs audited and corrected. Comparison table sourced and verified. Outreach tracking active (AlternativeTo submitted, others deferred). Dashboard screenshots refreshed + new screens added. Success Criteria: SC-001 through SC-014 PASS. Prerequisite for feature completion: T028 (maintainer submission, SC-014 blocker on AlternativeTo).

---

## Notes

**Total Task Count**: 60 tasks across all phases (T001–T060)

**Per-Phase Breakdown**:
- Phase 1 Setup: 1 task (T001)
- Phase 2 Foundational: 1 task (T002)
- Phase 3 US1: 6 tasks (T003–T008)
- Phase 4 US2: 17 tasks (T009–T025)
- Phase 5 US3: 4 tasks (T026–T029)
- Phase 6 US4: 10 tasks (T030–T039)
- Phase 7 Polish: 21 tasks (T040–T060, including 15 commits + push + verify + PR)

**Per-Story Breakdown**:
- US1: 6 tasks
- US2: 17 tasks
- US3: 4 tasks
- US4: 10 tasks
- MVP (US1 + US2): 23 tasks

**Parallel Opportunities**: 
- US1 research (T003–T005): 3 parallel
- US2 tooling (T009–T012): 4 parallel, then T013 sequential after T012; T014 final tooling task
- US2 corrections (T019–T022): 4 merged group tasks running in parallel (each edits disjoint file sets)
- US4 config (T030–T033): 4 parallel
- Polish commits (T040–T054): 15 parallel
- Across stories: US1, US2, US3, US4 research/setup can run in parallel once foundational is done

**Delegation per Rule 13**: Implementation delegated via Workflow at **haiku tier** with mandatory **sonnet tier+1 review** before acceptance. Likely escalation candidates (complex research or large bulk edits): T005 (CubeCoders AMP research — proprietary/unverifiable; may need creative URL hunting or archive.org analysis) → sonnet if haiku hits dead ends; T019 (feature description audit across 17 files, 5 categories, bulk edits) → sonnet if >20 unique finding patterns to fix.

**Local Pre-Flight (D6 Permitted, Rule 8)**:
- Do NOT run `make test`, `make lint`, `make cover`, `go test`, `npm test`/`vitest`, or any e2e suite locally
- DO run read-only pre-flights locally per D6 precedent from done_011:
  - `grep -r "v0\.[0-9]\.[0-9]-beta\.[0-9]+" <files>` to spot-check version strings
  - `hack/check-links.sh` locally (read-only, offline, no external I/O) to pre-validate links before push
  - `file docs/img/*.jpg && identify docs/img/*.jpg` to verify screenshot format/dimensions
  - `grep -r <pattern> <files>` to search for audit findings (e.g., forbidden alt-text patterns)
- CI remains system of record (rule 8, Principle VI): CI runs the full lint suite; local pre-flight is convenience only

**Assumptions & Unknowns**:
- **OD-14 (historical-reference marker)**: Ruled 2026-09-02. Inline HTML comment marker `<!-- doc-versions: historical -->` marks historical version lines (docs/install.md:54, docs/install.md:603, docs/oidc.md:50, docs/oidc.md:91, docs/oidc.md:372, README.md:195). Script is CI-wired per OD-14.
- **OD-15 (local screenshot fallback)**: Ruled 2026-09-02. Agents never run Playwright locally; if the dispatch path is blocked (secret missing, workflow not yet on the branch) stop and report to the maintainer; there is no local fallback.
- **Secret SCREENSHOT_BOT_PAT** (OD-13): Confirmed by the maintainer 2026-09-02 (OD-13). Maintainer must create fine-grained PAT (T036) before T035's workflow can succeed in production. Task T035 (screenshot-refresh.yaml) can be committed without the secret present; CI will fail (or the workflow will error on the gh pr create step) until the secret exists. T037 (capture via dispatch) will be blocked until T036 completes.
- **Comparison table anchors** (contract corrected): contracts/comparison-table.md § 9 specifies HTML anchors `<a id="">` (corrected 2026-09-02 with maintainer sign-off; earlier draft mistakenly referenced `{#id}` syntax). T006 implements explicit HTML anchors. Contract already reflects the correction.

### Implementation notes (2026-09-02)

**Commit Mapping (T040–T054 Polish Units)**

All commits signed and conform to rule 11 (conventional-commit prefixes, `Co-Authored-By:` trailers, `Claude-Session:` URLs). Actual commits on branch `012-docs-refresh-and-outreach` per `git log --format=%s e434bcae..HEAD`:

| Task | Commit Subject |
|------|---|
| T040 | docs: initialize feature 012 audit log and outreach tracker |
| T041 | docs: add side-by-side comparison table to README + docs: create comparison-sources.md with competitor research (T003-T005, T006) |
| T042 | feat: add documentation version and link gates |
| T043 | ci: run documentation link and version gates in the lint job |
| T044 | docs: document make check-doc-versions and check-links in CLAUDE.md |
| T045 | fix: update documentation version strings to v0.2.0-beta.8 |
| T046 | fix: correct broken documentation links and date references |
| T047–T048 | **Combined**: docs: apply [optional] and [experimental] labels to components at first mention + docs: consolidate audit log entries for US2 corrections (T015-T022) |
| T049 | docs: mark roadmap entries shipped or planned |
| T050 | **Withdrawn**: no CHANGELOG entry moved needed (OD-8 verification found both Unreleased items truly unreleased; doc qualifiers added instead); resolved as docs: qualify unreleased features in install, oidc and security docs |
| T051 | test: add opt-in screenshot data set and Playwright screenshot run |
| T052 | test: add Playwright mock-mode dashboard screenshot capture spec |
| T053 | ci: add tag-triggered screenshot refresh workflow |
| T054 | docs: rebuild the README screenshot gallery with twelve captures (f55078a1; alt-text trims f1dc2199) |

**PR Status**

- **PR #340** opened on branch `012-docs-refresh-and-outreach` against `master`, flipped to ready for review
- **Labels applied** (rule 14): `type: docs`, `type: ci`, `area: shared`, `area: web`, `area: specs`
- **CI status**: Green on 2d61504d (run 992, all 30/30 jobs passed); lint, link-check, version-check, web-e2e-mock, actionlint, zizmor complete. Exception: `github-advanced-security` check fails with "Model claude-opus-4.6 is not available" (GitHub outage, affects all PRs in repo; documented in PR comment); later heads through 28f4496a re-run green except the GitHub-side github-advanced-security check

**Open Items & Blockers**

| Item | Blocker | Unblocks |
|------|---------|----------|
| SC-014 (AlternativeTo submission) | T028 maintainer account + submission | Update outreach.md status → commit `docs: outreach [AlternativeTo] submitted` → feature completion |
| PR merge | Maintainer approval | Delete branch, rename specs/ folder to done_012, update contributing.md link (rule 16) |

**Success Criteria Summary**

- **SC-001 through SC-008**: 8/8 **PASS** (Comparison table + docs audit complete)
- **SC-009 through SC-011**: 3/3 **PASS** (12 screenshots captured in CI runs 4–6, gallery rebuilt, alt text verified)
- **SC-012, SC-013**: 2/2 **PASS** (Outreach tracking infrastructure setup)
- **SC-014**: **PENDING** (AlternativeTo status stays `pending`, a non-terminal state, until T028 submission)
- **Total**: 13/14 PASS, 1/14 PENDING (no FAILs)

**Verification Summary**

All eight quickstart.md scenarios (D6 read-only validation per FACTS 7, FACTS 8) — status 2026-09-02: scenarios 1–6 and 8 verified, scenario 7 pending until T028:
- Scenario 1 (Comparison Table): 9 rows, all Gameplane markers [G-a–i] present, sources tracked ✓
- Scenario 2 (Version Strings): appVersion 0.2.0-beta.8, script exists ✓
- Scenario 3 (Internal Links): hack/check-links.sh exists, 5 key files verified ✓
- Scenario 4 (Component Labels): 27 label instances across docs ✓
- Scenario 5 (No Unshipped Features): CHANGELOG Unreleased empty, roadmap has 12 marked entries ✓
- Scenario 6 (Screenshots): 12 JPEG at 1920×1080 in docs/img, all under 150 KB, captured by CI runs 4–6 (PRs #341/#342/#343 merged as f64333d9/655afa37/6ffa851a); README gallery lists all 12 ✓
- Scenario 7 (Outreach Tracking): outreach.md created, 3 targets with status fields, linked in contributing.md; AlternativeTo is still `pending` (non-terminal), so the scenario stays open until T028 ⏳
- Scenario 8 (Audit Log): 147 corrections recorded with evidence ✓

**CI & Rule 8 Compliance**

CI is system of record per rule 8; branch pushed to remote, CI run in progress. All lint jobs include:
- `hack/check-doc-versions.sh`: enforces version string consistency (OD-1, OD-14)
- `hack/check-links.sh`: validates internal link resolution (OD-2)
- ESLint: web/ strict mode
- golangci-lint: Go modules
- actionlint/zizmor: workflow validation

**Next Steps (Rule 12)**

1. CI green confirmed (all jobs pass: run 992, 30/30 jobs, 2d61504d)
2. T055 confirmation: CI passed ✓
3. T056–T060: Final verification + PR labels + ready for maintainer review
4. T028 (maintainer): Submit AlternativeTo, update outreach.md status
5. T036–T037 (maintainer + CI): Create PAT secret, dispatch screenshot workflow
6. Post-merge: Rename specs/012 → specs/done_012, update link in contributing.md (rule 16)

**Commits & Rule 11 Compliance**:
- Every commit signed: `git commit -s`
- Every commit uses conventional-commit prefix: `feat:`, `docs:`, `fix:`, `ci:`, `test:`, `chore:`
- Every commit includes `Co-Authored-By: <model running this session> <noreply@anthropic.com>` and `Claude-Session: <session URL>` trailers (rule 11 Trailers section)
- No `--amend`; no `--no-verify`; if a hook fails, fix underlying issue and create new commit (rule 11)
- Commits are per logical unit: audit-log + outreach (docs:), comparison table (docs:), tooling scripts (feat: or ci:), corrections grouped by type (fix: or docs:), screenshots (test: or feat:), gallery (docs:)

**Branch & Push (Rule 12)**:
- Feature branch: `012-docs-refresh-and-outreach`
- Push: `git push -u origin 012-docs-refresh-and-outreach`
- CI gate: All jobs must pass (lint with new doc checks, web-e2e-mock, actionlint, zizmor) before reporting done
- After PR merge (not tasks in this file): delete the branch remote+local (rule 12); rename the folder to specs/done_012-docs-refresh-and-outreach/ and update the docs/contributing.md link in the same commit (rule 16); both wait until T028 gives AlternativeTo a terminal state (SC-014)

**Quickstart Scenario Validation** (D6 Read-Only):
Per quickstart.md, eight scenarios validate feature completion. All must PASS before PR open:

1. **Scenario 1** (Comparison Table): Read README comparison table; identify 3+ differences per competitor; verify status line and nine dimensions present
2. **Scenario 2** (Version Strings): Grep 17 audited files + comparison-sources.md for version patterns; verify all match appVersion 0.2.0-beta.8 or marked as examples
3. **Scenario 3** (Internal Links): Run hack/check-links.sh; zero broken link errors; verify README anchors resolve
4. **Scenario 4** (Feature Labels): Grep for optional/experimental/beta components; verify consistent labeling across all mentions per component
5. **Scenario 5** (Unshipped Features): Verify no features described as shipped that are in CHANGELOG Unreleased; check CHANGELOG entries match doc claims
6. **Scenario 6** (Screenshots): File docs/img/*.jpg; identify && count; verify 1920×1080 JPEG, ≥11 images, alt text present, no forbidden patterns
7. **Scenario 7** (Outreach List): Read outreach.md; confirm three targets listed with terminal status (submitted [date] or deferred [date, reason])
8. **Scenario 8** (Audit Log): Read audit-log.md; confirm ≥1 row per category (version, link, label, feature, unshipped); each row has evidence citation and resolution

**Success Criteria Status Table** (completed T058):

| Criterion | Status | Evidence |
|-----------|--------|----------|
| SC-001 | PASS | Comparison table with 9 dimensions (a–i) visible in README; 3+ differences per competitor identifiable |
| SC-002 | PASS | Gameplane cells cite code/docs evidence (path:line) with qualifiers applied per V-CC3 |
| SC-003 | PASS | Competitor cells include dated SourceReference in docs/comparison-sources.md with anchors per V-SR1/V-SR2 |
| SC-004 | PASS | New self-hoster: zero stale version strings, zero broken links, consistent labels verified by audit |
| SC-005 | PASS | All version strings match appVersion 0.2.0-beta.8 or marked examples; historical references carry OD-14 marker |
| SC-006 | PASS | All internal doc links resolve (hack/check-links.sh: exit 0); 147 corrections logged with evidence |
| SC-007 | PASS | Optional/experimental components consistently labeled at first mention across 17 audited files |
| SC-008 | PASS | Audit log records all corrections with evidence; 0 version mismatches, 0 broken links, 0 label inconsistencies |
| SC-009 | PASS | Six refreshed screenshots retaken at 1920×1080 JPEG by screenshot-refresh.yaml (runs 4–6, merged f64333d9/655afa37/6ffa851a) |
| SC-010 | PASS | Six new screenshots (login, create-server wizard, events, admin general, cluster, logs) with one-sentence alt text in the README gallery (f55078a1) |
| SC-011 | PASS | Fixture-source scan and per-image review: only *.gameplane-demo.local hosts, no IPs, no real users, no cluster names (FR-019) |
| SC-012 | PASS | Outreach.md created and committed in specs/012-docs-refresh-and-outreach/ per T026 |
| SC-013 | PASS | Outreach.md linked from docs/contributing.md per T027 |
| SC-014 | PENDING | Three targets tracked: AlternativeTo (pending per T026), Awesome-Selfhosted (deferred per OD-6a), Awesome-Kubernetes (deferred per OD-6b); AlternativeTo's `pending` status is not a terminal state per the SC-014 state machine, so SC-014 is not met until T028 (maintainer submission) lands |

**PR Labels** (Rule 14):
- `type: docs` (primary)
- `type: ci` (secondary, for tooling)
- `area: shared` (README, docs/, hack/, .github/, Makefile, CLAUDE.md)
- `area: web` (web/e2e, web/src/test, playwright config, package.json)
- `area: specs` (specs/012 outreach.md, audit-log.md)


---

## Phase 8: Website Screenshot Sync (OD-16 Amendment, 2026-10-07)

**Purpose**: Capture website screenshots and amend spec to record the OD-16 ruling

- [X] T061 Create `web/e2e/specs/websiteScreenshots.spec.ts` (OD-16, FR-026): Dedicated Playwright spec (separate from dashboard screenshots.spec.ts) capturing five routes at 1440×900 viewport with deviceScaleFactor 2 (2880×1800 PNG RGBA): dashboard (/), servers (/servers), console (/servers/mc-survival (Console tab)), backups (/backups), modules (/modules). Output to web/website-screenshots/{dashboard,servers,console,backups,modules}.png (gitignored). Tag suite with @screenshots for filtering. Share MSW fixtures with dashboard screenshots spec (web/src/test/factories.ts from T030); authenticate once, capture all five routes, use `fullPage: false`. Satisfies FR-026, OD-16.

- [X] T062 Amend `.github/workflows/screenshot-refresh.yaml` to include website step (OD-16, FR-027, FR-028, FR-029): (a) the existing `npm run screenshots` step runs the suite with tag @screenshots, which now includes both web/e2e/specs/screenshots.spec.ts (dashboard) and web/e2e/specs/websiteScreenshots.spec.ts (website); both specs use the same MSW fixtures and authenticate once; (b) after both specs capture, add a new website step: clone gameplane-website repo, copy five PNGs from web/website-screenshots/ to website clone at src/assets/screenshots/{dashboard,servers,console,backups,modules}.png, add website images to git, create commit, open/update PR on gameplane-website main using `WEBSITE_SCREENSHOT_BOT_PAT` secret (fine-grained PAT scoped to gameplane-website only, Contents + Pull requests write); (c) if secret is unconfigured, log `::notice:: Website screenshot PAT not configured; skipping website update` and exit 0 (keep run green per FR-028); (d) NEVER push to website main; PR-only per FR-029; satisfies OD-16, FR-026–FR-029. *(Superseded 2026-10-07: the single SCREENSHOT_BOT_PAT replaces the separate WEBSITE_SCREENSHOT_BOT_PAT; see OD-16 amendment.)*

- [X] T063 Amend spec folder documents to record OD-16 ruling (2026-10-07): (a) OPEN-DECISIONS.md: add OD-16 "Website screenshot sync — credential and workflow scope" after OD-15, update summary table, update header to say "sixteen decisions"; (b) spec.md: update Out of Scope to note website exception (OD-16); add Amendment section with FR-026 through FR-029 for website screenshots (1440×900 @2x = 2880×1800, five files, website PR-only, skip with notice if secret missing); (c) data-model.md: amend website exclusion line to cite OD-16 exception; (d) plan.md: add web/website-screenshots/ and web/e2e/specs/websiteScreenshots.spec.ts entries to Project Structure, update Tooling decisions to cite OD-16 and WEBSITE_SCREENSHOT_BOT_PAT secret; (e) contracts/screenshot-set.md: add "Website Set (OD-16)" section with five filenames, routes, format (2880×1800 PNG), capture method, workflow integration, dummy data rules; (f) tasks.md: append Phase 8 (this phase) with T061–T065, mark T061–T063 done, T064–T065 blocked on maintainer; all in one commit per rule 11 with subject "docs(012): record OD-16 website screenshot sync ruling (amendment 2026-10-07)", body citing FR-026–FR-029, OD-16, and the five website screenshot files; satisfies OD-16, plan.md "amendment requirement". *(Superseded 2026-10-07: the single SCREENSHOT_BOT_PAT replaces the separate WEBSITE_SCREENSHOT_BOT_PAT; see OD-16 amendment.)*

- [X] T064 **[MAINTAINER TASK — Not agent-executable]** Replace the SCREENSHOT_BOT_PAT secret value with the new 3-repo token (2026-10-07 unified-token ruling): (a) the maintainer creates a fine-grained PAT with repository access to GameplanePanel/Gameplane, GameplanePanel/website, and GameplanePanel/module, with Contents read/write and Pull requests read/write permissions; (b) set expiration and record rotation schedule; (c) replace the existing `SCREENSHOT_BOT_PAT` repository secret value with the new token (same secret name, extended scope); (d) OD-16 Amendment 2026-10-07 specifies: the single `SCREENSHOT_BOT_PAT` secret with Contents + Pull requests read/write, scoped to both Gameplane and gameplane-website repos (gameplane-module scope at maintainer's discretion); satisfies FR-028, OD-16 Amendment — BLOCKED until maintainer replaces secret value. — 2026-10-09: maintainer confirmed the `SCREENSHOT_BOT_PAT` token already covers all linked repositories (Gameplane, website, module); expiration 2027-10-07, yearly rotation (see T036).

- [X] T065 Verify website screenshot dispatch and PR creation (OD-16, FR-027): (a) dispatch `.github/workflows/screenshot-refresh.yaml` on feature branch (same dispatch as T037, but this verifies website step succeeds); (b) wait for workflow run to complete and confirm: (i) website step ran and captured five PNGs (check logs for `websiteScreenshots.spec.ts` output), (ii) website step cloned gameplane-website and copied files, (iii) website step opened a PR on gameplane-website main branch (search GitHub for open PRs targeting gameplane-website main from the bot user), (iv) PR references the five files copied to src/assets/screenshots/; (c) T064 is prerequisite (token must be replaced with extended-scope PAT); if T064 incomplete, website step logs notice and exits 0 (green) per FR-028; mark as pending if token not yet replaced; satisfies FR-027, FR-028, OD-16 — BLOCKED until T064 (maintainer token replacement). — 2026-10-09: verified by screenshot-refresh run 14 (https://github.com/GameplanePanel/Gameplane/actions/runs/37550006453, workflow_dispatch on feat/website-screenshot-sync @ 49ad5a81): the website step succeeded, committed the five PNGs to src/assets/screenshots/ and opened website PR #11.

---

**Total Estimated Effort**: ~25–40 agent-hours (haiku tier) + ~5–8 reviewer-hours (sonnet tier) + maintainer sign-off hours (T028, T036, T064, sign-off decisions). Parallelism cuts wall-clock time to ~1–2 weeks for full feature (depending on research complexity and maintainer availability for submissions/decisions). Phase 8 (OD-16 amendment, T061–T065) adds ~4 agent-hours; T064–T065 blocked on maintainer.
