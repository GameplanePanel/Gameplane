# Implementation Plan: Documentation Refresh and External Outreach

**Branch**: `012-docs-refresh-and-outreach` | **Date**: 2026-09-01 | **Spec**: [./spec.md](./spec.md)

**Input**: Feature specification from `specs/done_012-docs-refresh-and-outreach/spec.md`

## Summary

Conduct a systematic audit of all user-facing documentation (README.md, 13 docs/ files, 3 component READMEs) to identify and correct staleness (version strings, feature descriptions, broken links, inconsistent feature labels); add a sourced, side-by-side comparison table to README.md comparing Gameplane, Pterodactyl, CubeCoders AMP, and Agones across nine feature dimensions; capture six refreshed and five+ new dashboard screenshots; and maintain a tracked to-do list for external outreach submissions (AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes). The approach is audit-then-correct with an evidence log committing every fix, external sources tracked in a separate file per FR-005/SC-003, and screenshot candidates selected by user-impact priority per FR-017.

## Technical Context

**Language/Version**: GitHub-flavored Markdown documentation (.md) and JPEG screenshots (.jpg); no runtime code changes; POSIX shell (OD-1/OD-2) and Playwright 1.62 (OD-3) for testing and screenshot tooling

**Primary Dependencies**: The 17 audited files (README.md, 13 docs/*.md, 3 component READMEs); charts/gameplane/Chart.yaml and values.yaml (appVersion, feature toggles); operator/api/v1alpha1/*_types.go (CRD fields); CHANGELOG.md (release history); web/src/router/tree.tsx and web/src/routes/ (dashboard screens); official competitor documentation (external)

**Storage**: git-tracked files (docs/img/*.jpg, docs/comparison-sources.md, specs/done_012-docs-refresh-and-outreach/outreach.md, audit-log.md); no database, no external service state

**Testing**: Read-only verification scenarios in quickstart.md (grep/ls/file commands), docs-audit contract procedures (version match, link resolution, label consistency), and CI deployment (link check via hack/check-links.sh, OD-2); no local test or lint suites per rule 8 (CI is authoritative)

**Target Platform**: GitHub.com rendering of README.md and docs/; Kubernetes 1.28+ clusters following install.md

**Project Type**: Documentation refresh and repository hygiene; no product, CRD, chart, or UI changes

**Performance Goals**: An evaluator reading the comparison table can identify at least three key differences between Gameplane and each competitor within five minutes without consulting external sources (US1); all 17 audited files resolve to zero stale version strings, zero broken internal links, and consistent optional/experimental/beta labels (SC-008)

**Constraints**: No product code, CRD type, chart value, or website (submodule) changes; English documentation only; screenshot dummy data restricted to test server names and cluster identifiers (no real hostnames, IPs, or player names); Login page screenshot must contain no version strings, cluster names, or metrics per rule 3; every comparison table cell sourced and dated per FR-005/SC-003

**Scale/Scope**: 17 audited files; 9 comparison dimensions × 4 products = 36 cells of which 27 require external sources; 6 existing screenshots refreshed + at least 5 new screenshots added (1920×1080 JPEG); 3 external directory submission targets; baseline findings from research Phase 0 (R1–R8)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| **I. E2E-Tested Delivery** | PASS-WITH-JUSTIFICATION | This feature documents existing shipped functionality; no new user-facing runtime path is added (dashboard screenshots document current UI; docs describe shipped code). Principle I's E2E mandate cannot literally apply to documentation. Verification: the audit's positive path (all 17 files have version strings matching appVersion, no broken links, consistent labels) is verifiable by grep/ls/file on CI per quickstart.md scenarios; the read-only verification scenarios in quickstart.md (Scenarios 2–4: version strings, links, labels) may be run locally as pre-flight under the done_011 ruling D6 precedent, while CI remains the system of record. This mirrors specs/done_011-add-missing-module-specs/plan.md's approach to Principle I for non-runtime features. |
| **II. Design-First for User-Facing Change** | N/A | Screenshots document existing UI only (Assumption: "No Design Changes Required"); no Pencil designs are authored or modified. |
| **III. Language & Ecosystem Best Practice** | PASS | Markdown follows GitHub-flavored conventions (existing docs/ style); comparison table uses standard markdown syntax (FR-007); JPEG screenshots use 1920×1080 JPEG (refreshed from existing 1568×773 per OD-3a); shell scripts (OD-1/OD-2) and Playwright specs (OD-3) follow idioms of their respective languages. No suppression directives introduced. |
| **IV. Spec-Driven Development** | PASS | Feature is spec-driven; implementation follows spec.md requirements (FR-001 through FR-025, SC-001 through SC-014). No behavior changes to modules; documentation-only work. |
| **V. Delegate to Workflows & Subagents** | PASS | Implementation delegated via Workflow (per rule 13) at haiku tier with mandatory sonnet tier-up review before acceptance. Main loop performs decomposition, orchestration, and verification only. |
| **VI. CI Bears the Heavy Lifting** | PASS | Link checks, version checks, and CI-native screenshot capture run on CI as ruled (OD-1, OD-2, OD-3, OD-3c); verification is the read-only quickstart scenarios plus PR review. Local pre-flight reads (grep, ls, file on 17 files) are permitted per D6 precedent from done_011. No local test/lint suite runs. |

**Post-Design Re-check**: Principles I–VI remain satisfied after Phase 1 design.

## Project Structure

### Documentation (this feature)

```text
specs/done_012-docs-refresh-and-outreach/
├── plan.md                                  # This file
├── spec.md                                  # Feature specification
├── inventory.md                             # Sampled staleness audit (input to research)
├── research.md                              # Phase 0 research summary (Phase 0 output of /speckit-plan)
├── data-model.md                            # Phase 1 audit findings, comparison table schema, outreach taxonomy
├── quickstart.md                            # Phase 1 execution walkthrough
├── contracts/
│   ├── comparison-table.md                  # Markdown table syntax, sourcing rules, cell validation
│   ├── docs-audit.md                        # Version match criteria, link resolution, label consistency
│   ├── screenshot-set.md                    # Viewport, naming, alt text, forbidden-pattern list (FR-019)
│   └── outreach-todo.md                     # Status vocabulary, submission tracking, terminal states
├── OPEN-DECISIONS.md                        # All fifteen decisions ruled 2026-09-02 (OD-1 through OD-15)
└── tasks.md                                 # Phase 2 implementation tasks (created by /speckit-tasks)
```

### Source Code (repository root)

```text
README.md
├── MODIFIED: comparison table inserted after "Why Gameplane?" section (FR-001, D-H)
├── MODIFIED: screenshot gallery extended with 6 refreshed + 5+ new images

docs/
├── comparison-sources.md                    # NEW: Dated sources for all competitor table cells (D-A)
├── img/
│   ├── [6 REFRESHED]: dashboard.jpg, servers-list.jpg, server-overview.jpg, mods-registry-browse.jpg, server-console.jpg, admin-mod-registries.jpg
│   └── [5+ NEW]: login.jpg, create-server-template-select.jpg, server-detail-events.jpg, admin-settings-general.jpg, cluster-nodes.jpg, server-detail-logs.jpg (1920×1080 JPEG per FR-017 priority and OD-3a)

web/
├── website-screenshots/                     # NEW (OD-16): Five dashboard screenshots for gameplane-website (2880×1800 PNG per FR-026)
│   ├── dashboard.png
│   ├── servers.png
│   ├── console.png
│   ├── backups.png
│   └── modules.png
├── e2e/specs/websiteScreenshots.spec.ts    # NEW (OD-16): Dedicated Playwright spec capturing website screenshot set (1440×900 @2x)
├── architecture.md                          # AUDITED: version strings, feature descriptions, cross-references
├── contributing.md                          # MODIFIED: link to specs/done_012-docs-refresh-and-outreach/outreach.md (FR-025)
├── dependencies.md                          # AUDITED: snapshot date, version pins, accuracy
├── game-coverage.md                         # AUDITED: feature coverage claims
├── install.md                               # AUDITED: version examples, Helm values, feature labels
├── key-rotation.md                          # AUDITED: feature status, signature trust
├── module-authoring.md                      # AUDITED: CRD field references, build procedures
├── networking.md                            # AUDITED: cross-references, link anchors
├── notifications.md                         # AUDITED: feature descriptions, version context
├── oidc.md                                  # AUDITED: feature availability dates, version context
├── roadmap.md                               # MODIFIED: add shipped/planned markers per OD-7; FR-014 compliance
├── security.md                              # AUDITED: RBAC, threat model, pre-auth privacy (rule 3)
└── tunnels.md                               # AUDITED: relay feature status, configuration

audit-syslog-bridge/README.md                # AUDITED: version strings, deployment examples
mcp-server/README.md                         # AUDITED: feature claims, RBAC bounds
telemetry-receiver/README.md                 # AUDITED: version examples, configuration
CHANGELOG.md                                 # MODIFIED (only if an Unreleased entry proves shipped): move it into the v0.2.0-beta.8 section per OD-8

specs/done_012-docs-refresh-and-outreach/
├── outreach.md                              # NEW: to-do list tracking submissions (FR-020, FR-021, FR-022)
└── audit-log.md                             # NEW: evidence log of all corrections (FR-011, D-F)

hack/check-doc-versions.sh                  # NEW: Detection script for version string drift (OD-1); accepts historical-reference markers (OD-14)
hack/check-links.sh                         # NEW: Offline internal link and anchor validation (OD-2)
.github/workflows/ci.yaml                   # MODIFIED: lint job adds doc-version-check and link-check steps
web/e2e/specs/screenshots.spec.ts           # NEW: Playwright mock-mode screenshot capture (OD-3b)
.github/workflows/screenshot-refresh.yaml   # NEW: tag-triggered screenshot refresh and PR opening (OD-3c; credential for PR authoring is a fine-grained PAT repository secret `SCREENSHOT_BOT_PAT` (OD-13); also `workflow_dispatch` for on-demand capture (OD-15))
```

**Structure Decision**: 

Documentation artifacts (research.md, data-model.md, quickstart.md, contracts/) live in specs/done_012-docs-refresh-and-outreach/ per Principle IV, serving as the durable record of intent and validation criteria. This separation keeps implementation-specific details out of the main repo and makes the spec folder self-contained.

External source tracking (docs/comparison-sources.md) is a separate file rather than inline README links because (a) it grows independently as competitors are researched during implementation, (b) it remains authoritative for audits and updates, and (c) README embedding would make cells unreadably dense (D-A, SC-003).

The outreach to-do list (specs/done_012-docs-refresh-and-outreach/outreach.md) lives in the spec folder, not docs/, because it is a project-internal tracking artifact, not user-facing documentation (FR-020, SC-012).

Tooling decisions (OD-1 through OD-16): The maintainer ruled on 2026-09-02 that link checks (hack/check-links.sh), version checks (hack/check-doc-versions.sh with historical-reference markers), and screenshot capture (Playwright mock mode in web/e2e/specs/screenshots.spec.ts, CI dispatch only per OD-15) plus tag-triggered refresh workflow (.github/workflows/screenshot-refresh.yaml with workflow_dispatch trigger per OD-15) are adopted and run on CI. The PR credential for the tag-triggered workflow is a fine-grained PAT repository secret `SCREENSHOT_BOT_PAT` (OD-13, ruled 2026-09-02). On 2026-10-07, the maintainer extended the screenshot-refresh workflow to also capture and update website screenshots on gameplane-website repo (OD-16), using the SAME `SCREENSHOT_BOT_PAT` token extended to cover both repositories (GameplanePanel/Gameplane and GameplanePanel/website, Contents + Pull requests read/write), replacing the originally separate `WEBSITE_SCREENSHOT_BOT_PAT` design. All sixteen open decisions are now ruled. None of these are product code, CRDs, or chart changes — they are repository tooling, testing infrastructure, and website automation, so the Out of Scope section of spec.md is not violated (website screenshot sync is the sole exception per OD-16).

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Principle I exception: documentation feature, no E2E runtime | Feature is documentation + hygiene (zero runtime user path). Verification is by audit (version match, link resolution, label consistency), not by E2E test execution. | An artificial E2E test (e.g., a dashboard flow) would not validate docs; the docs audit itself is the verification. Mirrored from done_011 precedent (Principle I exception justified for non-runtime features). |

## Phase Summary

- **research.md**: Phase 0 research synthesis (R1–R8) covering version staleness patterns (4 stale items: 3 version strings + 1 date stamp across 3 files, 6 historical references reclassified as not stale, 19 examples/placeholders), internal link inventory (69 links, 2 critical path errors in networking.md), feature label consistency (7 components, 0 contradictions), dashboard screen coverage (16 uncovered screens, 6 recommended new), Gameplane capability facts (9 dimensions, all sourced), outreach directory eligibility (3 targets, 1 age-blocked until 2026-10-22 (first release 0.2.0-beta.1 on 2026-06-22 plus four months), 1 eligible pending account setup), competitor documentation sources (Pterodactyl/Agones verifiable, CubeCoders proprietary/unverifiable), and unshipped-feature audit (FR-014 PASS, 1 OD-8 issue on Helm OIDC role mappings).

- **data-model.md**: Audit findings schema (file, line, finding type, evidence, resolution plan), comparison table column/row definitions (9 dimensions × 4 products, 27 external-source cells), outreach status vocabulary (pending|in-progress|submitted|rejected|deferred per SC-014), screenshot viewport/naming/alt-text conventions, and label-application matrix (first mention of optional/experimental/beta components per FR-012).

- **quickstart.md**: Eight read-only validation scenarios (1 comparison table, 2 version strings, 3 internal links, 4 labels, 5 unshipped claims, 6 screenshots, 7 outreach list, 8 audit log) plus Local Execution Note, Done When checklist, Cleanup, and References section.

- **contracts/comparison-table.md**: Markdown table syntax (row/column headers, cell content limits), sourcing requirements (every non-Gameplane cell cited to official documentation + date per FR-005/SC-003), Gameplane column validation rules (all claims match code/docs, beta/optional qualifiers applied, status line per FR-004), and contradiction/speculation detection (FR-006).

- **contracts/docs-audit.md**: Audit criteria (version string truth = appVersion 0.2.0-beta.8 per D-B; feature descriptions match operator/api/agent implementation; internal links resolve; optional/experimental/beta labels match CLAUDE.md and values.yaml per D-C/SC-007; feature status matches feature code per FR-010/FR-013).

- **contracts/screenshot-set.md**: Viewport convention (1920×1080 JPEG per OD-3a; existing 1568×773 screenshots refreshed), alt text format (purpose + key UI elements per FR-018), forbidden patterns (real hostnames, IP addresses, real cluster names, real player names, version strings on Login per FR-019/rule 3), existing filenames preserved (FR-016), six refreshed + five+ new selection criteria, and gallery-intro disclosure per OD-3d (one sentence stating screenshots captured against mocked data).

- **contracts/outreach-todo.md**: Status field format (pending | in-progress [YYYY-MM-DD] | submitted [YYYY-MM-DD] | rejected [YYYY-MM-DD, reason] | deferred [YYYY-MM-DD, reason] per SC-014), three target directories (AlternativeTo pending, Awesome-Selfhosted deferred until 2026-10-22 per OD-6a, Awesome-Kubernetes deferred per OD-6b per FR-021), success measurement (submission made, not acceptance; per FR-023), and git commit requirement for status changes (FR-022).

---

**Constitution Re-check (post-Phase 1)**: Principles I–VI remain satisfied. Principle I exception documented in Complexity Tracking above. No further violations.
