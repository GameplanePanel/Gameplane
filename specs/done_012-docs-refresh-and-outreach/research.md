# Research Phase: Documentation Refresh, Comparison Table, and External Outreach

**Feature:** 012 — Docs Refresh and Outreach  
**Date:** 2026-09-01  
**Status:** Phase 0 (Planning) — Research Complete

This document resolves the unknowns identified during feature specification. It is organized by decision area, with evidence citations, rationale, and a measured baseline of known staleness and gaps. One section per unknown; sections marked **OPEN** indicate items requiring a maintainer ruling before implementation can proceed.

---

## Decision: Version-String Truth Source and Audit Baseline

**Decision**: The authoritative version of Gameplane is `0.2.0-beta.8` (appVersion), released 2026-08-22. All version-string literals in the 17 audited files are verified against this value. Version strings fall into three classes: CURRENT (exact match to appVersion), STALE (older than appVersion), and EXAMPLE/THIRD-PARTY (placeholders or external dependency pins, exempt from audit). The audit baseline comprises 4 stale items (3 version strings, 1 date stamp) across 3 files.

**Rationale**: Version truth must come from a single, machine-readable canonical source. The Helm chart is the release artifact: `charts/gameplane/Chart.yaml:5–6` declares `appVersion: 0.2.0-beta.8` and release date 2026-08-22 (confirmed in `CHANGELOG.md:47`). SC-005 requires all version examples to match appVersion or be explicitly marked. Given that v0.2.0-beta.8 was released 2026-08-22 and the audit date is 2026-09-01 (10 days later), any Gameplane version older than beta.8 or any date stamp before the release is STALE.

Baseline stale instances (R1):
- `docs/install.md:14` — example version `0.2.0-beta.7` (STALE)
- `telemetry-receiver/README.md:9` — example version `0.2.0-beta.7` (STALE)
- `telemetry-receiver/README.md:28` — example version `0.2.0-beta.7` (STALE)
- `docs/dependencies.md:26` — snapshot date `2026-07-29`, predates release date 2026-08-22 by 24 days (STALE date)

Historical references, not stale (reclassified):
- `README.md:195` — pre-rotation reference (legitimate historical context)
- `docs/install.md:54` — "Pre-rotation releases (v0.2.0-beta.7 and earlier) were signed with the retired key" (legitimate historical note)
- `docs/install.md:603` — "installations that predate the Kestrel → Gameplane rename (v0.2.0-beta.2, July 2026)" (legitimate historical context; note for review: CHANGELOG.md:632 dates beta.2 as 2026-06-22, so "July 2026" may need correcting)
- `docs/oidc.md:50` — "from v0.2.0-beta.6+" (availability phrasing, not stale version example)
- `docs/oidc.md:91` — "from v0.2.0-beta.6+" (availability phrasing, not stale version example)
- `docs/oidc.md:372` — "from v0.2.0-beta.6+" (availability phrasing, not stale version example)

**Alternatives considered**: Accepting version strings >14 days stale would erode trust in docs; a stricter matching rule (must match appVersion exactly, no "examples" allowed) would force rewording of installation walkthroughs into ambiguity. The chosen rule (exact match or explicit "example version" label) balances practical documentation needs with accuracy.

---

## Decision: Internal Link Verification Method and Broken-Link Baseline

**Decision**: Internal markdown links are verified by existence of target files and, where applicable, correctness of anchor slugs using a two-pass approach: (1) file existence via `test -f`, (2) anchor presence via `grep -o` to extract heading lines and confirm slug-matching. No automated link-check tooling exists in the repo today (lychee, markdown-link-check, linkinator all absent from Makefile, package.json, CI workflows, or hack/). The baseline comprises 2 critical path reference errors and 5 anchor verification warnings across 17 audited files.

**Rationale**: SC-006 requires "verified by automated link checking." However, inventory shows no tooling is configured. Ruled 2026-09-02 (OD-2): read-only POSIX shell script `hack/check-links.sh` for internal links and heading anchors (no external links or dependencies), runnable locally and in CI per CLAUDE.md rule 8 precedent, following the `hack/check-specs.sh` model from done_011.

Baseline broken links (R2):
- `docs/networking.md:13,194` — incorrectly reference `docs/install.md` instead of `install.md` (double-nesting in relative path; resolves to `/docs/docs/install.md`)
- `README.md:10` — anchor `#beta-status--known-limitations` → heading is "Beta Status & Limitations" (the `&` is removed in slug generation; anchor should be `#beta-status--limitations`)
- `docs/install.md:190,567` and `docs/security.md:380` — anchor verification pending (headings exist; slug generation rules unclear per markdown processor variant)
- `docs/module-authoring.md:325` — anchor `#signing-official-bundles` (heading exists; verification pending)

**Alternatives considered**: Deferring link validation to CI only would allow stale links to reach master; manual spot-checking would miss edge cases. A read-only link-check script fits CLAUDE.md rule 8's compile-check exception model (already precedented in done_011 for `hack/check-specs.sh`).

---

## Decision: Label Registry, Tag Vocabulary, and First-Mention Rule

**Decision**: Seven components carry D-C tags (per orchestrator decision D-C): sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver, tunnel, and postgres driver. Tag vocabulary is:
- `[optional]` — Helm chart value defaults to disabled (empty or `enabled: false`); user must explicitly enable
- `[experimental]` — shipped but unstable; may change in patch versions
- `[disabled by default]` — shipped but opt-in at runtime
- `[BETA]` — feature-complete but tested in limited scenarios; breaking changes possible in v1 GA
- `[local cluster only]` — architectural limitation (e.g., multi-cluster console streaming)

FR-012 requires each optional/experimental component to carry a tag at its **first mention** in each audited file. The baseline shows 10 instances lacking explicit qualifiers (sentinel, tunnel in README and roadmap; component READMEs titles without context).

**Rationale**: SC-007 mandates "every feature marked as optional or experimental in CLAUDE.md or charts/gameplane/values.yaml is labelled consistently in all associated documentation files." R3 confirms zero contradictions where overlapping mentions exist, but 10 instances of missing qualifiers indicate incomplete application of the first-mention rule. Evidence:
- CLAUDE.md:372 (tunnel: "[optional] relay client supervisor")
- charts/gameplane/values.yaml:84–87 (tunnel default: empty)
- charts/gameplane/values.yaml:522 (capture.enabled: false)
- docs/architecture.md:268–290 (sentinel, capture, mcp-server, audit-syslog, telemetry all marked [optional])

Tunnel inclusion in FR-012: Ruled 2026-09-02 (OD-4): tunnel included in the label set (CLAUDE.md marks it [optional]).

**Alternatives considered**: Applying tags only to component READMEs would reduce first-mention burden but violate SC-007's "all associated documentation" requirement. A global qualifier list (glossary at top of README) would not satisfy the FR-012 requirement for "first mention in each doc file."

---

## Decision: Comparison Table Layout, Placement, and Cell Grammar

**Decision**: The comparison table is positioned in README.md immediately after the "Why Gameplane?" section (per D-H) and before "## Features". Columns: Gameplane, Pterodactyl, CubeCoders AMP, Agones (in that order). Rows: nine dimensions per FR-002 (a–i). Each cell is a single declarative sentence, max ~25 words, stating feature presence/absence or design choice without value judgment. Gameplane cells cite codebase evidence (CRD fields, code paths); competitor cells cite official documentation with dated sources per D-A.

**Rationale**: FR-001 and FR-007 require a readable, markdown-formatted table positioned early for evaluators. D-H specifies the exact layout. Cell grammar: "Kubernetes-native CRDs... scales from k3s to multi-node clusters" (feature description) rather than "Gameplane is Kubernetes-native" (promotional language). This supports SC-001 (evaluators identify three key differences per row) and SC-002 (claims are traceable to code/docs, not marketing copy). Ruled 2026-09-02 (OD-12): README table order is intro paragraph (explaining scope: Pterodactyl and CubeCoders AMP are control panels; Agones is a Kubernetes operator library; why it is compared), then FR-004 status line, then the table.

Source placement (D-A): Gameplane column cells cite repo evidence inline (e.g., "GameServer, Backup CRDs per operator/api/v1alpha1/gameserver_types.go:1"). Competitor cells reference a NEW file `docs/comparison-sources.md` with one dated entry per competitor cell (URL, date checked, what was verified). This satisfies SC-003 without cluttering the table. Ruled 2026-09-02 (OD-11): Agones is kept in the comparison; non-mapping dimensions for Agones read "not applicable (Agones is a Kubernetes operator library)" per OD-11 ruling.

**Alternatives considered**: Embedding sources as footnote links would require anchor generation for each cell (brittle); a separate source file is more maintainable. Placing the table at the end of README would delay evaluators; early placement (after intro and status, before "## Features") aligns with user flow.

---

## Decision: Competitor Source Registry and Reachability

**Decision**: Three competitors are compared:
1. **Pterodactyl** (MIT, open-source): docs.pterodactyl.io/, github.com/pterodactyl/panel. 6 of 9 dimensions have documented features; 3 return HTTP 404 (architecture, scaling, auth details). Fallback: base docs root.
2. **Agones** (Apache 2.0, open-source): agones.dev/site/docs/, github.com/agones-dev/agones. Library, not a control panel; some dimensions (template distribution, auth) are not applicable. 6 of 9 dimensions verified.
3. **CubeCoders AMP** (proprietary, closed-source): cubecoders.com/AMP. JavaScript-heavy; WebFetch cannot render. No GitHub repo. Manual browser research required during implementation.

**Rationale**: R7 confirmed Pterodactyl and Agones have reachable docs; CubeCoders is a known blocker. Ruled 2026-09-02 (OD-9): fill only what is verifiable from public sources; CubeCoders cells that cannot be verified from official documentation read "not publicly documented (checked YYYY-MM-DD)". Ruled 2026-09-02 (OD-10): for unreachable pages (404s), hunt for the correct URL via archive.org; if found, cite the moved page; if not found, cell reads "source URL unavailable (checked YYYY-MM-DD)".

**Alternatives considered**: Removing CubeCoders from the table would simplify sourcing but loses a key competitor in the user's decision space. Adding more competitors (ngrok, Cloudflare Tunnel) would scatter focus; three is the minimum meaningful set.

---

## Decision: Gameplane-Column Facts Summary

**Decision**: The Gameplane column is verified against codebase, README, docs, and CLAUDE.md. Nine dimensions with evidence citations (path:line):

| Dimension | Summary | Qualifier(s) | Evidence |
|---|---|---|---|
| (a) Deployment | K8s-native CRDs + controller-runtime | [BETA] | README.md:6; operator/api/v1alpha1/gameserver_types.go:1 |
| (b) Scaling & auto-sleep | Opt-in idle, configurable wake windows, wake-on-connect | [optional]; Minecraft/Terraria [handshake]; others [heuristic] | README.md:36; gameserver_types.go:137–195 |
| (c) NAT/relay | frp, Tailscale, playit sidecars; playit user-managed | [optional]; disabled by default | README.md:37,52; CLAUDE.md:372; values.yaml:84–87 |
| (d) Backup/restore | restic to S3-compatible; cron or on-demand; one-click restore | none required | README.md:56; backup_types.go:126 |
| (e) Auth/RBAC | Local argon2id + OIDC; three built-in roles + custom roles | none required | README.md:59; docs/security.md:17,20; migrations/003_roles.sql:38–41 |
| (f) Template distribution | OCI bundles via ModuleSource; optional cosign verification | none required | README.md:57; module-authoring.md:107,182,276 |
| (g) Multi-cluster | Cluster CRD; console/log streaming **local-cluster only** | [local cluster only] | README.md:35,60; cluster_types.go:8; roadmap.md:18–22 |
| (h) Licensing | AGPL-3.0-or-later | none required | LICENSE file:1 |
| (i) Self-hosted scope | K8s only; no managed SaaS | none required | README.md:6; CLAUDE.md operator-authoritative design |

**Rationale**: R5 verified all nine dimensions against codebase evidence. Multi-cluster streaming qualification is explicitly `[local cluster only]` per README.md:35 and roadmap.md:22, not [BETA], ensuring SC-002 compliance (no overstated feature claims). Tunnel included per CLAUDE.md:372 (OD-4, ruled 2026-09-02). Multi-cluster limitation documented as [local cluster only].

**Alternatives considered**: Labeling multi-cluster streaming as [BETA] would overstate availability; the code and roadmap both mark it as local-cluster-scoped for now, a designed limitation, not a beta feature.

---

## Decision: Screenshot Set — Existing, Recommended New, and Capture Method

**Decision**: Six existing screenshots (JPEG, replaced at 1920×1080) map to current dashboard screens:
1. `dashboard.jpg` — `/` Dashboard
2. `servers-list.jpg` — `/servers` Servers list
3. `server-overview.jpg` — `/servers/$name` Overview tab
4. `mods-registry-browse.jpg` — `/servers/$name` Mods tab
5. `server-console.jpg` — `/servers/$name` Console tab
6. `admin-mod-registries.jpg` — `/admin` Mod registries section

Recommended six new screenshots (kebab-case filenames, priority order per FR-017):
1. `login.jpg` — `/login` Login page
2. `create-server-template-select.jpg` — `/servers/new` Create Server wizard step 1
3. `server-detail-events.jpg` — `/servers/$name?tab=events` Server Events tab
4. `admin-settings-general.jpg` — `/admin?section=general` Admin General section
5. `cluster-nodes.jpg` — `/cluster` Cluster management page
6. `server-detail-logs.jpg` — `/servers/$name?tab=logs` Server Logs tab

**Screenshot Capture Method**: Ruled 2026-09-02 (OD-3): Playwright mock mode (MSW + Vite, no cluster required). Rationale: reproducible (MSW deterministic), CI-native (runs on localhost:5173), realistic data (existing MSW handlers), acceptable trade-off on live streams (mocked logs acceptable per FR-019). Capture specification: `web/e2e/specs/screenshots.spec.ts` (GAMEPLANE_E2E_TARGET=mock, tagged or grepped on demand to not affect regular mock e2e job), at 1920×1080 viewport, outputs to docs/img/. Ruled 2026-09-02 (OD-3c): tag-triggered GitHub Actions workflow `.github/workflows/screenshot-refresh.yaml` regenerates MSW fixture data and screenshots on tag push, opens a pull request with new images. Ruled 2026-09-02 (OD-13): the PR credential is a fine-grained PAT repository secret named `SCREENSHOT_BOT_PAT` (scoped to this repository with contents and pull-requests write permission, stored as a repository secret). Ruled 2026-09-02 (OD-15): first screenshot capture runs on CI only via workflow_dispatch; agents never run `npm run test:e2e:mock` locally (rule 8, Principle VI).

**Rationale**: R4 confirmed 12 primary routes, 25+ screens, and 16 uncovered screens. The six recommended new screens are highest priority for evaluators (login flow, first-run wizard, key admin sections, monitoring pages). D-E establishes convention: JPEG, 1920×1080, kebab-case filenames, alt text stating purpose + key UI elements. FR-019 forbids real user data; mocked data is acceptable. Ruled 2026-09-02 (OD-3d): disclosure once in the README gallery intro (one sentence above the screenshot gallery states that screenshots are captured against mocked data); individual alt texts do NOT mention mocking.

Alt text template: "Shows the [Tab/Section] with [description of key UI elements and functionality]" (e.g., "Shows the Events tab with Kubernetes event timeline and filtering options").

**Alternatives considered**: Manual screenshot capture (human effort, easy to go stale) vs. automated Playwright (reproducible, CI-integrated, scales to multiple screens). Mock mode avoids cluster dependency; live mode captures authentic UI but requires CI infrastructure.

---

## Decision: Outreach Targets, Submission Mechanics, and Eligibility Blockers

**Decision**: Three external directory targets (FR-021):

1. **AlternativeTo.net**: Eligible immediately; no identified blockers. Submission: web form (email-verified account required). Ruled 2026-09-02 (OD-5, OD-6c): agents draft content; maintainer submits from their own account and commits status and reference. Status: pending (authorization required for maintainer submission).

2. **Awesome-Selfhosted** (awesome-selfhosted-data repo): Blocked by 4-month project age minimum. Gameplane's first release was 2026-06-22 (per CHANGELOG.md); as of 2026-09-02, the project is under the 4-month minimum. Eligible from 2026-10-22. Ruled 2026-09-02 (OD-6a): **deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]** (per SC-014). YAML entry drafted and ready for submission on eligible date.

3. **Awesome-Kubernetes**: Minimum eligibility rule: 25 GitHub stars and 3+ contributors. Ruled 2026-09-02 (OD-6b): defer WITHOUT pre-checking metrics. Status: **deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release]**. No pre-check task in this feature; decision node for later release when metrics are revisited.

**Rationale**: R6 researched eligibility criteria from official directory guidelines. SC-014 requires a terminal state (submitted or deferred) by feature completion. Ruled 2026-09-02 (OD-5): account ownership remains with maintainer; agents draft submission content only. Agents write exact submission content into outreach.md; maintainer submits from their own accounts.

Outreach list location: `specs/done_012-docs-refresh-and-outreach/outreach.md` (FR-020). Linked from `docs/contributing.md` (FR-025). Status vocabulary per D-D: `pending | in-progress [YYYY-MM-DD] | submitted [YYYY-MM-DD] | rejected [YYYY-MM-DD, reason] | deferred [YYYY-MM-DD, reason]`.

**Alternatives considered**: Removing Awesome-Selfhosted from targets would only address 2 directories, reducing outreach impact. Deferring without a date would allow the task to be forgotten; explicit date marking ensures re-evaluation.

---

## Decision: FR-014 Policy and Unreleased Feature Documentation

**Decision**: FR-014 prohibits README and docs from referencing features "announced but not yet shipped." This is verified by cross-checking documentation against code/CRDs and CHANGELOG. Ruled 2026-09-02 (OD-7): every item in docs/roadmap.md gets an explicit "(shipped vX.Y.Z)" or "(planned)" marker so the file is self-explanatory. Docs/roadmap.md becomes MODIFIED (not only audited). Ruled 2026-09-02 (OD-8): for every CHANGELOG.md Unreleased entry that docs describe, verify whether it shipped in v0.2.0-beta.8; if shipped, move the entry into the beta.8 section (CHANGELOG.md becomes MODIFIED in that case); if truly unreleased, add "(unreleased; ships in the next release)" next to the doc mention.

**Rationale**: R8 audited 17 files and found zero FR-014 violations (no forward-looking claims violating "announced but not shipped" standard). However, R8 detected one OD-8 issue: Helm-seeded OIDC role mappings are documented as available in `docs/oidc.md` but listed under "Unreleased" in `CHANGELOG.md:38–45`. This feature may have been shipped in v0.2.0-beta.8 (CHANGELOG placement error) or is truly unreleased (docs need qualifier). Ruled 2026-09-02 (OD-8): implementation must verify each such case and apply the corresponding fix (move CHANGELOG entry or add qualifier to docs).

Multi-cluster console/log streaming is correctly documented as `[local cluster only]` (README.md:35, roadmap.md:22), not as a beta feature; this complies with FR-013 (identify and correct outdated claims about feature status).

**Alternatives considered**: Ignoring post-release documentation drift would erode clarity on which features are current vs. pending; labeling ensures users know what to expect at their installed version.

---

## Decision: Evidence Log Location and Schema

**Decision**: Evidence log location: `specs/done_012-docs-refresh-and-outreach/audit-log.md` (D-F). Schema (one row per correction):
- File
- Line(s)
- Finding (type: stale version string, broken link, inconsistent labeling, etc.)
- Evidence checked (path:line reference to what was verified against)
- Resolution (what was changed)
- Commit hash (for audit trail)

**Rationale**: D-F requires a permanent record of every correction for FR-011 compliance (audit trail). The log is created during implementation; this research phase establishes the contract and location.

**Alternatives considered**: Embedding evidence in commit messages alone would scatter the audit trail across git history; a centralized log provides a single source of truth for what was checked and why changes were made.

---

## Decision: CI Version-Drift Automation (OD-1)

**Decision**: Ruled 2026-09-02 (OD-1): read-only detection gate `hack/check-doc-versions.sh` (fails CI when docs drift from appVersion). Detection not auto-rewrite. Aligns with done_011 precedent (`hack/check-specs.sh`) and CLAUDE.md rule 8 (read-only scripts are permitted as compile checks). Runs as a step in the CI lint job like `hack/check-specs.sh`; runnable locally as pre-flight check. Historical references are allowlisted with an inline HTML comment marker `<!-- doc-versions: historical -->` (OD-14, ruled 2026-09-02).

**Rationale**: Detection is lower-risk than auto-rewrite; maintainer can then commit version updates as a single deliberate change. Automation scope is maintainer-decided; this resolves the decision to detection-only per the done_011 model. The historical marker prevents false positives for legitimate context (e.g., "Pre-rotation releases...were signed with the retired key").

**Alternatives considered**: Auto-rewrite would reduce manual burden but risks replacing version strings in contexts where an example version is intentional (e.g., "If you are upgrading from 0.2.0-beta.7...").

---

## Decision: Constitution Principle I Handling (E2E Testing)

**Decision**: Constitution Principle I (E2E-Tested Delivery) cannot literally apply to a docs-only feature. Gameplane documents existing UI; no new features are shipped. Following done_011 precedent (specs/done_011-add-missing-module-specs/plan.md, Complexity Tracking), this feature records Principle I as **PASS-WITH-JUSTIFICATION**: documentation changes are verifiable (links can be checked, version strings can be compared to appVersion) but do not require an E2E test in `test/e2e/`. Verification is performed by CI link-check and version-drift detection scripts (if implemented per OD-1/OD-2), plus manual review (screenshots vs. running dashboard).

**Rationale**: E2E tests verify user-facing behavior changes; documentation is static content. CI gates (link-check, version-drift detection) are sufficient verification. Screenshots are verified by human comparison to a running dashboard (acceptable per Assumption "Test Cluster Available").

**Alternatives considered**: Implementing a Playwright E2E suite to verify that dashboard screenshots match the running web/ codebase would be over-specified; a manual comparison during PR review is proportionate.

---

## Baseline (measured 2026-09-01)

The following table summarizes measured staleness and gaps across the 17 audited files. Implementers use this as the starting inventory.

| Category | Count | Research File | Notes |
|---|---|---|---|
| **Version Strings: STALE Gameplane versions** | 4 | R1 | docs/install.md:14; telemetry-receiver/README.md:9,28; docs/dependencies.md:26 (date stamp STALE) |
| **Version Strings: CURRENT** | 7 | R1 | README.md:8,31; docs/dependencies.md:26,28; roadmap.md; others correct |
| **Version Strings: THIRD-PARTY dependency pins** | 81 | R1 | Appropriately documented; not in scope for audit |
| **Links: Broken Internal (path errors)** | 2 | R2 | docs/networking.md:13,194 (double-nested docs/ prefix) |
| **Links: Anchor verification warnings** | 5 | R2 | README.md:10; install.md:190,567; security.md:380; module-authoring.md:325 |
| **Links: Verified working** | 11 sampled, all valid | R2 | Sample verification; no dead links found in tested subset |
| **Optional Component First-Mentions lacking qualifiers** | 10 | R3 | sentinel (2×), tunnel (2×), component README titles (3×), others with minimal context |
| **Optional Components with 100% consistency** | 5 | R3 | capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver, postgres (experimental) |
| **Files with zero optional-component mentions** | 6 | R3 | game-coverage.md, key-rotation.md, module-authoring.md, networking.md, notifications.md, oidc.md (scope question: intentional?) |
| **Dashboard Screens Covered by Existing Screenshots** | 6 | R4 | Dashboard, Servers List, Server Detail Overview, Mods, Console, Admin Mod Registries |
| **Dashboard Screens Uncovered** | 16 | R4 | Login, Create Wizard, Events/Logs/Files/Players/Backups/Capture/Settings tabs, Cluster, Users, Backups Schedules/Restores, Audit, System Logs |
| **Recommended New Screenshots** | 6 | R4 | login, create-server-template-select, server-detail-events, admin-settings-general, cluster-nodes, server-detail-logs |
| **Gameplane Column Dimensions Verified** | 9/9 | R5 | All traceable to codebase; qualifiers applied per status |
| **Competitor Sources: Reachable** | 2 (Pterodactyl, Agones) | R7 | 6 of 9 dimensions verified for each; fallback to base docs for HTTP 404 links |
| **Competitor Sources: Not Reachable** | 1 (CubeCoders AMP) | R7 | Proprietary; JavaScript-heavy; OD-9 ruled: fill only what is verifiable; cells read "not publicly documented (checked YYYY-MM-DD)" |
| **Outreach Targets: Eligible immediately** | 1 (AlternativeTo.net) | R6 | OD-5, OD-6c ruled: pending (agents draft, maintainer submits) |
| **Outreach Targets: Age-blocked** | 1 (Awesome-Selfhosted) | R6 | OD-6a ruled: deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22] |
| **Outreach Targets: Metrics-blocked** | 1 (Awesome-Kubernetes) | R6 | OD-6b ruled: deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release] |
| **FR-014 violations (announced but not shipped)** | 0 | R8 | All forward-looking language properly scoped (roadmap context, beta messaging) |
| **Unreleased features documented as available** | 1 | R8 | Helm-seeded OIDC role mappings (CHANGELOG vs. docs discrepancy; OD-8 ruled: verify each, move or add qualifier) |
| **Link-Check Tooling Present** | 0 | R2 | OD-2 ruled: `hack/check-links.sh` for internal links and anchors |
| **Version-Drift Detection Tooling Present** | 0 | OD-1 | OD-1 ruled: `hack/check-doc-versions.sh` detection gate |

---

## Open Decisions: Status Update

All fifteen decisions ruled on 2026-09-02: OD-1 (version-drift detection script), OD-2 (link-check script), OD-3a/OD-3b/OD-3c/OD-3d (screenshot capture, viewport, workflow, disclosure), OD-4 (tunnel included in labels), OD-5 (agents draft, maintainer submits), OD-6a (Awesome-Selfhosted deferred to 2026-10-22), OD-6b (Awesome-Kubernetes deferred without pre-check), OD-6c (AlternativeTo pending), OD-7 (roadmap markers), OD-8 (verify each CHANGELOG entry), OD-9 (CubeCoders fill-only-verifiable), OD-10 (404 pages hunt-then-unavailable), OD-11 (keep Agones with not-applicable), OD-12 (Agones notation and table intro placement), OD-13 (screenshot PR credential is a fine-grained PAT repository secret `SCREENSHOT_BOT_PAT`), OD-14 (historical-reference allowlist marker `<!-- doc-versions: historical -->`), OD-15 (first screenshot capture via CI workflow_dispatch only).

---

## Verification Against Spec and Constitution

This research aligns with:
- **Spec FR requirements** (FR-009 through FR-025): Each audit scope, success criterion, and deliverable is addressed.
- **Constitution Principle IV** (Spec-Driven Development): This document is the Phase 0 output that resolves unknowns before implementation.
- **Constitution Principle VI** (CI Bears the Heavy Lifting): Link-check and version-drift tooling (if implemented) are read-only static checks, permitted locally per CLAUDE.md rule 8.

---

## Next Steps for Implementation Phase

1. Implementation started 2026-09-02 (/speckit-implement); all fifteen OPEN-DECISIONS entries were ruled before kickoff — see OPEN-DECISIONS.md
2. Implement version-string audit and corrections (R1 baseline: 4 stale items across 3 files).
3. Implement internal-link verification (R2 baseline: 2 critical path errors, 5 anchor warnings).
4. Apply FR-012 labels to 10 missing-qualifier instances (R3 baseline).
5. Author comparison table with sourced cells (R5 facts + D-A source placement).
6. Capture 6 new screenshots (R4 recommended set + D-E format).
7. Create outreach.md with status entries (R6 targets + D-D vocabulary).
8. Create audit-log.md (D-F schema) documenting all corrections.
9. Commit regularly per rule 11 (logical units: one audit fix, one screenshot, one outreach status, etc.).

---
