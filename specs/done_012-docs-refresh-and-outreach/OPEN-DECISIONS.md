# Open Decisions

**Status**: All sixteen decisions ruled (OD-1 through OD-15 on 2026-09-02; OD-16 on 2026-10-07; OD-2's two slug sub-questions on 2026-10-11); none remain open.

All open questions documented here arise from the feature spec (spec.md), CLAUDE.md guidance, constitution principles, research findings (R1–R8), or maintainer amendments that could not be resolved without maintainer judgment. This document is the authoritative list of everything blocking detailed planning and implementation.

---

## Seeded Decisions (OD-1 to OD-8)

### OD-1: CI auto-update of version strings on release tag

**Status**: RULED 2026-09-02

**Question**: When the v0.2.0-beta.N release is cut, who updates the version strings in the 17 audited documentation files? Specifically, when charts/gameplane/Chart.yaml appVersion is bumped, how should docs/install.md, docs/oidc.md, telemetry-receiver/README.md, and other references to the previous version be refreshed?

**Why it matters**: Spec FR-009 requires version strings to match the current release (SC-005: "all version strings in code examples match the current release or are explicitly marked as example version"). If version freshness is not automated, new releases will immediately create drift, and every session will discover staleness that should have been caught at release time.

**Evidence**: 
- spec.md:46 "maintainer idea: auto fetch latest tag in a ci to auto update the docs for version string?"
- R1-versions.md shows 4 STALE version strings: docs/install.md:14 (install example), telemetry-receiver/README.md:9 and :28 (example payloads), docs/dependencies.md:26 (snapshot date)
- SC-005 requirement: /home/user/Gameplane/specs/done_012-docs-refresh-and-outreach/spec.md:170

**Options**:
1. **(a) Release workflow auto-rewrite**: A GitHub Actions step in the release workflow rewrites version strings in docs when a release tag is pushed (e.g., sed replacing old version with new). Pros: automatic, release-time. Cons: script fragility, false positives (example vs. literal).
2. **(b) CI detection gate** (like `hack/check-specs.sh`): A shell script `hack/check-doc-versions.sh` runs in the lint job, fails if any non-example, non-date file references appVersion older than current, with an allowlist for intentional examples. Implementer fixes before merge. Pros: surgical, high signal, documented. Cons: manual per-release, blocks CI.
3. **(c) Manual, no tooling**: Implementer updates versions as part of refresh work; release cutoff becomes a pre-merge checklist. Pros: low infra cost. Cons: human error, no catch for new commits post-release.

**Recommended default**: **(b) CI detection gate** — a `hack/check-doc-versions.sh` script that fails CI if docs drift from appVersion, parallel to the existing `hack/check-specs.sh` precedent (CLAUDE.md rule 8, constitution Principle VI). Detects, does not auto-edit. Implementer fixes before merge. Simpler than (a), more reliable than (c).

**Ruling (2026-09-02)**: Option (b) is chosen. Implement a read-only POSIX script `hack/check-doc-versions.sh` that fails CI when an audited doc references an older Gameplane version not allowlisted as an example or historical reference. Runs as a step in the CI lint job like `hack/check-specs.sh` and is runnable locally as a pre-flight check (precedent: done_011 D6).

**Blocks**: 
- SC-005 pass/fail (version strings match current release)
- Task "Audit version strings" in tasks.md
- Release checklist (pre-merge validation of doc versions)

---

### OD-2: Link checking tooling and scope

**Status**: RULED 2026-09-02

**Question**: SC-006 states "all internal doc links in README.md and docs/ resolve to existing targets verified by automated link checking." Which link-check tool should be selected, and should it cover external links?

**Why it matters**: The project has no link validation in CI or pre-flight checks (R2-links.md confirms absence of lychee, markdown-link-check, linkinator, or remark). Without tooling, links drift silently, and the audit found 2 critical path errors in docs/networking.md (lines 13, 194). SC-006 explicitly requires automation.

**Evidence**:
- spec.md:170 SC-006: "verified by automated link checking"
- spec.md:200, Assumptions: "CI Link Validation: Automated link checking is available or manual spot-checking suffices for merge validation"
- R2-links.md: "No link-check tooling detected: lychee, markdown-link-check, linkinator, or remark validators absent from .github/workflows/, Makefile, web/package.json, hack/, or .pre-commit-config.yaml"
- R2-links.md identifies 2 path errors in docs/networking.md:13,194 (double-nesting `docs/install.md` instead of `install.md`)
- R2-links.md identifies 5 anchor verification issues with potential slug mismatches

**Options**:
1. **(a) lychee GitHub Action**: Third-party GitHub Action (`lychee-dev/lychee-action`) in CI. Covers both internal and external links. Pros: comprehensive, mature. Cons: requires external GitHub Action (dependency), may be slow on external links, failures in air-gapped environments.
2. **(b) markdown-link-check**: Npm package; can run in lint job or as a pre-flight script. Focuses on internal links + anchors. Pros: fast, no external action. Cons: anchor slug generation may not match all markdown processors.
3. **(c) Offline stdlib shell script** (hack/check-links.sh): Bash script using `grep`, `find`, and regex to validate internal links and anchors locally; runs as a pre-flight check (done_011 D6 precedent) and as a CI step in lint job. Pros: no external deps, full control, air-gap friendly. Cons: anchor slug generation must be manually coded; regex is fragile for complex markdown.

**Recommended default**: **(c) Offline shell script** — a `hack/check-links.sh` following the precedent of `hack/check-specs.sh` (CLAUDE.md rule 8, constitution Principle VI, rule 6 ruling D6 from done_011). Validates internal links and anchors only (external links are environment-dependent; air-gapped installs must not fail). Script is read-only; implementer runs before push and fixes errors. Integrates into lint job as a parallel check.

**Ruling (2026-09-02)**: Option (c) is chosen. Implement an offline shell script `hack/check-links.sh` that validates internal links and heading anchors in audited files with no external dependencies or external link checking. Runs locally as a pre-flight check and as a step in the CI lint job.

**Sub-questions** (clarification needed for script design):
- How does the markdown processor handle ampersand characters in heading slugs (e.g., "Beta Status & Limitations" → `beta-status--limitations` or `beta-status-limitations`)? (R2-links.md open question 1)
  - **Ruling (2026-10-11, valgul)**: GitHub style — `&` is dropped and its surrounding spaces stay, giving a double hyphen (`beta-status--limitations`). This is what hack/check-links.sh already implements and self-tests.
- Are same-file anchor references always required to use the full heading slug, or are shorthand/partial matches supported? (R2-links.md open question 2)
  - **Ruling (2026-10-11, valgul)**: Full slug only — a same-file `#anchor` must exactly match a heading slug (or explicit `<a id>`/`<a name>` anchor) in that file; no partial or prefix matches. This is what hack/check-links.sh already implements.

**Blocks**:
- SC-006 pass/fail (internal links verified)
- Task "Add link validation tooling" in tasks.md
- Implementation of hack/check-links.sh

---

### OD-3: Screenshot capture environment and method

**Status**: RULED 2026-09-02

**Question**: How should the 11 dashboard screenshots (six refreshed, five new) be captured? Three distinct methods exist, with different trade-offs on reproducibility, CI feasibility, and resource requirements.

**Why it matters**: FR-015 requires screenshots showing "current Gameplane dashboard UI"; they must be reproducible (so future maintainers can refresh), CI-integrated (Principle VI: CI bears heavy lifting), and free of real user data (FR-019). The capture environment determines feasibility and cost.

**Evidence**:
- spec.md:72 "at least 11 total screenshots covering key workflows"
- spec.md:202 Assumption "Test Cluster Available: Screenshots can be captured from a running kind cluster or test environment with test data"
- R4-screens.md:470–493 "Capture recommendation: Playwright Mock Mode (MSW + Vite)"
- Existing infrastructure: web/e2e/ with playwright.config.ts, MSW handlers in web/src/test/, Makefile targets test-web-e2e-mock and test-web-e2e-live already present

**Options**:
1. **(a) Manual on maintainer's kind cluster** (make dev-up): Maintainer runs local cluster, seeds test data (dummy server names, modules), captures via screenshot tool or browser dev tools. Pros: no infra cost, realistic live data. Cons: not reproducible across sessions, manual every release, cluster setup burden.
2. **(b) Playwright mock mode** (MSW + Vite, no cluster): A new Playwright spec file `web/e2e/specs/screenshots.spec.ts` uses MSW mocked API responses and deterministic test fixtures. Captures at 1920×1080 viewport (per OD-3a) from Vite dev server on localhost:5173. Pros: reproducible (data fixed in test factory), CI-native (no cluster needed), fast, data fully controlled. Cons: live streams (Console/Logs tabs) are mocked (alt text does not disclose mocking; see OD-3d), no real Kubernetes UI elements.
3. **(c) Playwright live mode** (against make dev-up in CI): CI job provisions a kind cluster, seeds data via `make dev-up`, runs Playwright against the live dashboard, captures. Pros: realistic full UI. Cons: slow, resource-intensive for CI (cluster provisioning ~3–5 min), flaky (depends on cluster health, timing).

**Recommended default**: **(b) Playwright mock mode** — MSW + Vite, no cluster needed. Rationale: 
- Reproducible across maintainers and CI runs (test data is code, not state-dependent)
- CI-native (Principle VI: no cluster provision needed)
- Fast (Vite dev server is instant; no K8s provisioning)
- Realistic UI (React components render the same way)
- Mock streams (Console, Logs tabs) are acceptable; disclosure is handled once in the README gallery intro (see OD-3d), not per alt text
- Aligns with existing web/e2e/ infrastructure (MSW handlers, Playwright config)

**Ruling (2026-09-02)**: Option (b) is chosen. Use Playwright with MSW mocking and Vite (no cluster). Screenshots captured at 1920×1080 JPEG in `web/e2e/specs/screenshots.spec.ts`, run on demand with `GAMEPLANE_E2E_TARGET=mock`. A tag-triggered GitHub Actions workflow regenerates MSW fixture data and screenshots, opening a pull request with new images on release tag push. Disclose mock mode once in the README screenshot gallery intro; individual alt texts do NOT mention mocking.

**Sub-decisions** (clarification for implementation):

**OD-3a (Viewport Resolution)**:  
Should viewport remain 1568×773 (inferred from existing files), or should a different resolution be preferred?
- Options: (a) Keep 1568×773, (b) Switch to 1280×720 (Desktop Chrome default), (c) Different resolution
- Recommended: **Keep 1568×773** (matches existing, professional aspect ratio)
- **Ruling (2026-09-02)**: Switch to 1920×1080 (overrides recommendation). All eleven images captured at 1920×1080 JPEG. Existing six files replaced at new size with filenames kept per FR-016. Update all mentions of 1568×773 as the target to 1920×1080; 1568×773 remains only when describing current files.

**OD-3b (Capture Specification vs. CI Job)**:  
Should screenshot capture be a Playwright test spec in `web/e2e/` or a separate CI job?
- Options: (i) Playwright spec (`web/e2e/specs/screenshots.spec.ts`) run by `npm run test:e2e:mock`, (ii) Standalone CI job
- Recommended: **(i) Playwright spec** for integration; (ii) is also acceptable
- **Ruling (2026-09-02)**: Option (i) is chosen. Screenshots captured in `web/e2e/specs/screenshots.spec.ts`, run on demand with `GAMEPLANE_E2E_TARGET=mock` tag or grep filter so the regular mock e2e job is unaffected. Output written to `docs/img/`.

**OD-3c (Data Freshness)**:  
Should recommended screenshot MSW fixture data (game templates, server names, module list) auto-update on release tag, or stay manually updated per feature release? (R4-screens.md open question 3)
- **Ruling (2026-09-02)**: Auto-update on tag (overrides recommendation for manual updates). A release-triggered GitHub Actions workflow regenerates MSW fixture data and screenshots, opening a pull request with new images on release tag push. Workflow file: `.github/workflows/screenshot-refresh.yaml`. See OD-13 for outstanding question about PR credential.

**OD-3d (Alt Text for Mocked Streams)**:  
Should alt text for live-stream tabs (Console, Logs) explicitly note "mocked data" when using mock-mode screenshots, or is the contract-level disclaimer sufficient?
- Options: (a) Explicit in every alt text, (b) Once in README intro, (c) In alt text only for live-stream tabs
- Recommended: **(c) Note mock mode in Console/Logs alt text only**
- **Ruling (2026-09-02)**: Option (b) is chosen (overrides recommendation). Disclosure once in README screenshot gallery intro (one sentence stating screenshots are captured against mocked data). Individual alt texts do NOT mention mocking. Remove per-alt-text "mock mode" wording from examples and rules.

**Blocks**:
- FR-015 (screenshots showing current UI)
- FR-016 (refreshed screenshots in docs/img/)
- FR-017 (new screenshots covering uncovered screens)
- Task "Implement screenshot capture" in tasks.md

---

### OD-4: Whether tunnel is in the FR-012 label set

**Status**: RULED 2026-09-02

**Question**: FR-012 requires optional/experimental/beta features to be "clearly marked as `[optional]` or `[experimental]` or `[disabled by default]` the first time mentioned in each doc file." The spec lists five components (sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver). Should tunnel (relay supervisor) also be tagged as [optional]?

**Why it matters**: SC-007 states "every feature marked as 'optional' or 'experimental' in CLAUDE.md or charts/gameplane/values.yaml is labelled consistently in all associated documentation files." CLAUDE.md:372 describes tunnel as "optional relay client supervisor", so if the rule applies, tunnel must be tagged wherever it appears in the audited files.

**Evidence**:
- spec.md:122 FR-012: "sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver" (five listed; tunnel not explicitly named)
- CLAUDE.md:372 "optional relay client supervisor" (tunnel marked optional in repo map)
- SC-007: "every feature marked as 'optional' or 'experimental' in CLAUDE.md or charts/gameplane/values.yaml is labelled consistently in all associated documentation files"
- R3-labels.md: "Tunnel confirmed [optional] per CLAUDE.md Repo map line 372"
- README.md:52 mentions "Built-in Relay Tunnels" without [optional] tag
- architecture.md cross-links tunnels.md but tunnels.md:9 has no [optional] qualifier

**Options**:
1. **(a) Include tunnel in FR-012 label set**: Treat tunnel as a sixth optional component; tag it [optional] the first time mentioned in each audited file (README.md, architecture.md, etc.). Aligns with SC-007 and CLAUDE.md's classification.
2. **(b) Exclude tunnel from FR-012; document separately**: Tunnel is a configuration option (Helm value `tunnel.enabled`, `tunnel.relayClient`) rather than a component like sentinel. Tag it only in docs/tunnels.md and docs/install.md Helm values sections, not throughout.
3. **(c) List tunnel as optional but separately from the five**: Expand FR-012's labeling scope to include tunnel explicitly, but note its different role (configuration, not component image).

**Recommended default**: **(a) Include tunnel in FR-012** — treat tunnel as a sixth optional component for consistent labeling. Rationale: SC-007 is clear ("every feature marked as optional in CLAUDE.md"), and tunnel is marked optional in CLAUDE.md. CLAUDE.md rule 15 states "read the whole specs/<feature>/ folder" — this decision was already settled in SC-007 and CLAUDE.md; FR-012 just implements it.

**Ruling (2026-09-02)**: Option (a) is chosen. Tunnel is included as a sixth optional component in the FR-012 label set. Tag tunnel [optional] at first mention in each audited file.

**Blocks**:
- FR-012 implementation (tag optional features consistently)
- Task "Apply D-C labels to first mentions" in tasks.md

---

### OD-5: Outreach submissions require maintainer-owned accounts

**Status**: RULED 2026-09-02

**Question**: FR-020 and FR-025 require tracking and linking an external outreach to-do list. Three directories (AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes) each require account credentials or PR authorship. Should agents draft submission content (PR text, account application text) into outreach.md for the maintainer to submit, or should agents attempt submission directly?

**Why it matters**: Submissions require authentication (Awesome-Selfhosted/Awesome-Kubernetes) or verified account ownership (AlternativeTo). The spec does not resolve who performs the final submission. Without clarification, implementers cannot proceed past drafting.

**Evidence**:
- spec.md:51 US3 "maintainer wants to submit Gameplane to three external directories"
- spec.md:154 FR-024 "submission tracking for audit trail" 
- spec.md:220 Out of Scope "Control over third-party acceptance: Awesome-Selfhosted and Awesome-Kubernetes maintain their own review processes"
- R6-outreach.md confirms all three require account/auth:
  - Awesome-Selfhosted: PR/Issue to awesome-selfhosted-data repo
  - Awesome-Kubernetes: PR to ramitsurana/awesome-kubernetes
  - AlternativeTo: Web form (account-based)

**Options**:
1. **(a) Agents draft, maintainer submits**: Agents write the exact submission text (PR title/body, account application fields) into outreach.md as "DRAFT SUBMISSION" blocks. Maintainer reviews, owns credentials, executes the PR or form submission, updates outreach.md with PR number/submission date/proof. Pros: security (no agent access to accounts), audit trail (maintainer signs off). Cons: requires maintainer follow-up post-feature.
2. **(b) Agents submit directly**: Agents use maintainer-provided credentials (stored in GitHub Secrets or plaintext in a secure context) to submit PRs or form responses. Pros: fully automated. Cons: credential management burden, audit trail less clear.
3. **(c) Agents attempt open/public submissions only**: AlternativeTo (public form, no auth needed) is submitted by agents. Awesome-Selfhosted and Awesome-Kubernetes (auth-required) are marked `pending [date] awaiting maintainer submission`. Pros: partial automation, clear boundaries. Cons: two-tier completion.

**Recommended default**: **(a) Agents draft, maintainer submits** — Per spec.md:53 ("maintainer wants to... track external outreach submissions"), the maintainer is the submitter. Agents produce the submission content (structured as decision artifacts in outreach.md), the maintainer reviews and executes. Rationale: aligns with SC-014 terminal states (submitted/deferred recorded by maintainer), keeps credentials out of agent context, preserves audit trail.

**Ruling (2026-09-02)**: Option (a) is chosen. Agents draft the exact submission content (PR title/body, account application fields) into `outreach.md` as "DRAFT SUBMISSION" blocks. Maintainer reviews, owns credentials, executes the PR or form submission, and updates `outreach.md` with PR number/submission date/proof.

**Blocks**:
- FR-020 (outreach to-do list with status tracking)
- FR-022 (status updates committed to git)
- FR-025 (list linked from docs/contributing.md)

---

### OD-6: Eligibility of projects for each external directory

**Status**: RULED 2026-09-02

**Question**: R6-outreach.md identified three sub-questions about which directories Gameplane can actually submit to:

1. **Awesome-Selfhosted**: Requires 4+ month project age. Gameplane first release 2026-06-22, about 2 months and 10 days old on 2026-09-01, eligible from 2026-10-22. Is a `deferred [2026-09-01, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]` terminal state acceptable in outreach.md, or should this target be removed entirely from the outreach list?

2. **Awesome-Kubernetes**: Requires 25+ GitHub stars and 3+ contributors (recognized org exception does not apply). Gameplane's current star/contributor count is unknown; metrics may be insufficient. Should a pre-submission check be added to outreach.md to confirm eligibility, or should the submission be attempted and permitted to fail during implementation?

3. **AlternativeTo**: No identified blockers. Public beta (v0.2.0-beta.8) is acceptable. Shall the entry be marked `pending [2026-09-01]` awaiting maintainer account creation and submission?

**Why it matters**: SC-014 requires "each of the three target directories (AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes) has a recorded terminal state in the outreach to-do list: either a submission actually made (with date and reference recorded), or an explicit, dated deferral reason." Determining which directories are actually eligible determines whether the outreach feature can be completed in this cycle.

**Evidence**:
- spec.md:188 SC-014: "By feature completion, each of the three target directories has a recorded terminal state"
- R6-outreach.md: Complete eligibility analysis with evidence for each directory
  - Awesome-Selfhosted: "4-month age minimum; first release 2026-06-22; eligible from 2026-10-22" (CHANGELOG.md:656 for first release date)
  - Awesome-Kubernetes: "Minimum 25 GitHub stars and 3+ contributors required (exception: recognized org); Gameplane metrics unknown, likely blocked"
  - AlternativeTo: "No identified blockers; accepts public beta"
- CHANGELOG.md:47 shows v0.2.0-beta.8 released 2026-08-22 (10 days before the 2026-09-01 audit date); the project's first release was 2026-06-22, about 2 months and 10 days before 2026-09-01

**Options**:

**For Awesome-Selfhosted:**
- **(a) Accept deferred state**: Outreach.md records `deferred [2026-09-01, age requirement; eligible from 2026-10-22]` as a terminal state per SC-014. Feature is complete; submission happens automatically on or after 2026-10-22 or in a follow-up release.
- **(b) Remove from outreach scope**: Delete the Awesome-Selfhosted entry; scope feature to two directories (Awesome-Kubernetes, AlternativeTo) only. Feature is narrower but unambiguous.

**For Awesome-Kubernetes:**
- **(a) Pre-check metrics in implementation**: Before crafting the PR, a research task queries GitHub API for current star count and contributor list. If insufficient, mark as `deferred [date, stars/contributors insufficient]` per SC-014.
- **(b) Attempt submission; permit failure**: Craft and submit the PR; if Awesome-Kubernetes maintainers reject for insufficient metrics, record as `rejected [date, insufficient stars/contributors]` per SC-014. Provides feedback for the project's future.

**For AlternativeTo:**
- **Record as `pending [2026-09-01]`**: No blockers identified. Awaits maintainer account creation and form submission per OD-5.

**Recommended defaults**:
- **Awesome-Selfhosted**: **(a) Accept deferred state** — SC-014 explicitly permits "explicit, dated deferral reason" as a terminal state. Recording the 2026-10-22 eligibility date is honest and allows automation later. Completes feature scope without requiring removal.
- **Awesome-Kubernetes**: **(a) Pre-check metrics in implementation** — Query GitHub API for current stats; if marginal, mark as `deferred [reason]` and document why for future reference. Reduces wasted effort on a likely-rejected PR. Alternative (b) is acceptable if maintainer prefers to attempt regardless.
- **AlternativeTo**: **Record as `pending [2026-09-01]`** — No blockers. Awaits maintainer submission per OD-5.

**Ruling (2026-09-02)**: All three targets are addressed:
- **Awesome-Selfhosted**: Deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22] as a terminal state per SC-014. Record the awesome-selfhosted-data YAML entry in outreach.md now so it is ready for submission on or after 2026-10-22.
- **Awesome-Kubernetes**: Defer WITHOUT pre-checking metrics. Record deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release]. No pre-check task, no PR attempt in this feature.
- **AlternativeTo**: Pending [2026-09-02] awaiting maintainer account creation and form submission per OD-5.

**Blocks**:
- FR-020 (outreach to-do list completeness)
- SC-014 (terminal states recorded)
- Task "Research and populate outreach.md" in tasks.md

---

### OD-7: FR-014 versus docs/roadmap.md unshipped features

**Status**: RULED 2026-09-02

**Question**: FR-014 states "README.md and docs MUST NOT reference features that are announced but not yet shipped." However, docs/roadmap.md legitimately lists unshipped work (v1 GA blockers, post-v1 aspirations). How should the rule apply?

**Why it matters**: The audit must determine whether roadmap.md's unshipped feature mentions violate FR-014, or whether roadmap.md is exempt as a category because it is explicitly about future work.

**Evidence**:
- spec.md:126 FR-014: "README.md and docs MUST NOT reference features that are announced but not yet shipped"
- docs/roadmap.md contains "shipped features" (lines 7–23) and "post-v1 aspirations" (lines 25–) with unshipped items
- R8-unshipped.md: "FR-014 PASS: No forward-looking language violates 'announced but not shipped' standard; all future-looking claims are in roadmap, beta messaging, or architectural reserves"
- Example: roadmap.md line 133 mentions "wake-on-connect" and line 167 mentions "MCP server" — both have unshipped aspects

**Options**:
1. **(a) Roadmap is exempt**: FR-014 applies to README.md and docs/* except roadmap.md. Roadmap's whole purpose is to list unshipped work; no qualification needed.
2. **(b) Roadmap items require "(shipped)" or "(planned)" labels**: Roadmap entries are tagged to distinguish shipped from unshipped. E.g., "Idle Auto-Sleep (shipped v0.2.0-beta.7)" vs. "Wake-on-Connect (planned, experimental)".
3. **(c) Roadmap entries are subject to FR-014; must be marked experimental or upcoming**: Any unshipped item in roadmap must carry a [BETA], [upcoming], or [experimental] tag.

**Recommended default**: **(a) Roadmap is exempt** — Rationale: roadmap.md's entire purpose is to describe future work; its presence in the repo is itself a disclaimer. Readers expecting shipped-features-only would not look there. For clarity, R8-unshipped.md already found no violation: "all future-looking claims are in roadmap, beta messaging, or architectural reserves." The roadmap's context makes labels redundant.

**Ruling (2026-09-02)**: Option (b) is chosen (overrides recommendation). Tag every roadmap.md entry with an explicit "(shipped vX.Y.Z)" or "(planned)" marker so the file is self-explanatory. docs/roadmap.md becomes MODIFIED per FR-014 (not only audited). This affects the implementation scope.

**Blocks**:
- FR-014 pass/fail (no unshipped features announced elsewhere)

---

### OD-8: Features merged after v0.2.0-beta.8 but documented on master

**Status**: RULED 2026-09-02

**Question**: Some features are shipped on master but not yet released (e.g., CHANGELOG.md Unreleased section). Example: Helm-seeded OIDC role mappings are documented in docs/oidc.md as available but listed in CHANGELOG.md under "Unreleased". Were they shipped in v0.2.0-beta.8 but misplaced in the changelog, or are they truly unreleased and need qualification in docs?

**Why it matters**: SC-004 states "a new self-hoster deploying Gameplane v0.2.0-beta.8 following README and docs does not encounter out-of-date version strings, stale feature descriptions, or broken links." If docs describe unreleased features as available in v0.2.0-beta.8, they are stale for that version. Either docs need a qualifier ("unreleased; coming in next release") or CHANGELOG needs a fix.

**Evidence**:
- R8-unshipped.md: "OD-8 ISSUE: Helm-seeded OIDC role mappings documented in docs/oidc.md as available but listed under Unreleased in CHANGELOG.md (lines 38-45); unclear if the feature was actually shipped in v0.2.0-beta.8 or is truly unreleased"
- docs/oidc.md discusses Helm-seeded role mappings without a qualifier
- CHANGELOG.md lists "Helm-seeded OIDC role mappings" under Unreleased, after v0.2.0-beta.8 entry

**Options**:
1. **(a) The feature shipped in v0.2.0-beta.8; fix CHANGELOG**: Move the OIDC role mapping entry from Unreleased to the v0.2.0-beta.8 section. Docs are correct; changelog was misplaced.
2. **(b) The feature is truly unreleased; qualify docs**: Add "(unreleased; ships in the next release)" to docs/oidc.md sections describing Helm-seeded role mappings. Changelog is correct; docs need qualification.
3. **(c) Adopt a post-release documentation policy**: For any feature on master post-release, docs must carry a label: "(unreleased; ships in the next release)". Applies ongoing to other features that may land before v0.2.0-beta.9 is cut.

**Recommended default**: **(a) Check implementation; if feature shipped, fix CHANGELOG** — The spec focuses on v0.2.0-beta.8 completeness. If Helm-seeded OIDC role mappings are actually shipped and working, the CHANGELOG entry should move. If the implementation finds it's truly unreleased, move to **(b)** and qualify docs. Decision depends on runtime verification, not a rule.

**Sub-decision OD-8b (if feature is unreleased)**: Should all post-beta.8 features on master be proactively labelled "(unreleased; ships in the next release)", or only features that are documented as available without a clear "coming soon" context? Recommendation: (c) — adopt a policy of proactive "(unreleased; ships in the next release)" labels for any feature appearing in docs that landed after the current release tag, to prevent confusion.

**Ruling (2026-09-02)**: For every CHANGELOG.md Unreleased entry that docs describe: verify whether it shipped in v0.2.0-beta.8. If it shipped, move the CHANGELOG entry into the beta.8 section (CHANGELOG.md becomes MODIFIED). If it is truly unreleased, add "(unreleased; ships in the next release)" next to the doc mention. No blanket policy. OD-8b is closed by this ruling: each unreleased feature is handled as needed, no proactive labeling policy required.

**Blocks**:
- SC-004 pass/fail (docs don't claim unreleased features as available)
- SC-005 pass/fail (version claims are accurate)
- Task "Audit feature status vs. release date" in tasks.md

---

## New Decisions (OD-9 to OD-12)

### OD-9: CubeCoders AMP comparison table sourcing and strategy

**Status**: RULED 2026-09-02

**Question**: CubeCoders AMP is a proprietary, closed-source product with no public GitHub repo and a JavaScript-heavy website (https://www.cubecoders.com/AMP) that WebFetch cannot render. The comparison table (FR-001–FR-005) must include CubeCoders columns for all nine dimensions. How should comparison data be sourced given that automated research cannot access the website?

**Why it matters**: FR-005 requires "each competitor column cell MUST be sourced and dated (e.g., 'Verified against <product>'s official documentation on <date-checked>')." CubeCoders cannot be verified by automated tools, and FR-006 forbids "disparaging, speculative, or unverifiable claims." Without a sourcing strategy, CubeCoders columns are either incomplete or unverifiable.

**Evidence**:
- spec.md:104–105 FR-005 requires dated sources for competitor claims; FR-006 forbids unverifiable claims
- R7-competitor-sources.md: "CubeCoders AMP: website https://www.cubecoders.com/AMP (HTTP 200 but no content rendered), proprietary closed-source, no GitHub repo, 0 of 9 dimension sources verified"
- R7-competitor-sources.md: "CubeCoders requires manual browser research; WebFetch cannot render JavaScript-heavy page content"
- All three competitors in the table (Pterodactyl, CubeCoders, Agones) require sourcing; Pterodactyl and Agones are fully verifiable; CubeCoders is not

**Options**:
1. **(a) Defer CubeCoders to implementation; accept manual research**: Implementer is tasked with manually visiting https://www.cubecoders.com/AMP in a browser (possibly hiring or outsourcing research), documenting findings, and populating the comparison table with dated sources. Dated "checked 2026-09-15 via cubecoders.com/AMP" is acceptable evidence even if not automated.
2. **(b) Include only verifiable data for CubeCoders**: Comparison table includes CubeCoders columns, but cells are marked "Not publicly documented" or "Unavailable" for dimensions that cannot be independently verified. Maintains sourcing rigor; trades completeness.
3. **(c) Agones-only trio**: Reduce the comparison table to Gameplane, Pterodactyl, and Agones (three competitors). CubeCoders is removed from scope due to unverifiable nature. Spec's intent (evaluator can compare Gameplane to similar tools) is partially met; CubeCoders (proprietary, closed-source) is arguably less comparable than open-source tools anyway.

**Recommended default**: **(a) Defer CubeCoders; manual research in implementation** — Rationale: The spec explicitly names CubeCoders (spec.md:96 "comparing Gameplane, Pterodactyl, CubeCoders AMP, and Agones"). Removing it (option c) requires spec change. Option (b) is incomplete. Option (a) is honest: implementer notes "checked [date] via cubecoders.com/AMP" and documents what was found. If CubeCoders feature pages become inaccessible, mark as "(site unavailable as of [date])". Sourcing is the audit trail, not the access method.

**Ruling (2026-09-02)**: Option (b) is chosen (overrides recommendation). Fill only what is verifiable for CubeCoders AMP. For dimensions that cannot be independently verified from fetchable official sources, mark cells "not publicly documented (checked YYYY-MM-DD)".

**Blocks**:
- FR-001 (comparison table with four competitors)
- FR-005 (sourced and dated claims)
- Task "Research CubeCoders AMP dimensions" in tasks.md

---

### OD-10: 404 dimension URLs for Pterodactyl and Agones — fallback sourcing strategy

**Status**: RULED 2026-09-02

**Question**: R7-competitor-sources.md identified three Pterodactyl URLs and three Agones URLs that return HTTP 404 when fetched during research. For example, Pterodactyl's deployment documentation was expected at one URL but is no longer there. Should implementation cite the base documentation root (fallback) as the source, or attempt to track down the correct dimension-specific URL?

**Why it matters**: FR-005 requires sources to be "publicly documented features from official documentation or GitHub READMEs." A 404 breaks the verifiability chain. How implementations re-source these claims affects SC-003 compliance ("competitor columns include a dated source reference").

**Evidence**:
- spec.md:104–105 FR-005: sources must be from official documentation or GitHub READMEs
- R7-competitor-sources.md: "Three Pterodactyl and three Agones dimension URLs returned HTTP 404; fallback to base docs URLs recommended"
- Fallback sources identified:
  - Pterodactyl docs root: https://pterodactyl.io/ (HTTP 200, reachable)
  - Agones docs root: https://agones.dev/site/docs/ (HTTP 200, reachable)

**Options**:
1. **(a) Use base documentation root as fallback**: If a specific dimension URL is 404, cite the base docs root and note the section/feature name (e.g., "Verified against Pterodactyl docs root at pterodactyl.io/docs, deployment section"). Pros: verifiable, dated. Cons: less precise than a direct link.
2. **(b) Defer and attempt to locate correct URL**: Implementer searches both websites (via browser, archive.org, or outreach) for the correct dimension URL. If found, cite it. If not, defer the dimension as "source URL unavailable". Pros: precise sourcing. Cons: time-consuming, may not yield results.
3. **(c) Accept that some dimensions are not verifiable in current documentation**: Mark those cells as "Feature documentation unavailable as of [date]". Maintains honesty about sourcing limits without blocking the table.

**Recommended default**: **(a) Use base documentation root + dimension notes** — Rationale: FR-005 asks for sources, not direct links. A base documentation URL with a note "deployment section" is a verifiable source. If the specific feature is documented somewhere in Pterodactyl or Agones docs, the base URL is sufficient sourcing. Dated, honest, and avoids false precision.

**Ruling (2026-09-02)**: Option (b) is chosen (overrides recommendation). Hunt for the correct URL: for each 404 dimension page, implementation searches the sites and archive.org for the moved page and cites it if found. If not found, the source entry reads "source URL unavailable (checked YYYY-MM-DD)".

**Blocks**:
- FR-005 (sourced claims)
- SC-003 (dated source references)
- Comparison table completeness for Pterodactyl and Agones

---

### OD-11: Agones is a Kubernetes library, not a control panel — scope and framing

**Status**: RULED 2026-09-02

**Question**: Agones is a Kubernetes operator/CRD library for running game servers at scale on K8s, not a control panel (no dashboard, no user auth, no template distribution). Many comparison table dimensions (e.g., "Backup and Restore", "Access Control & Authentication", "Template Distribution") do not map to Agones. Should Gameplane be compared against a tool with such different scope, and if so, how should non-applicable dimensions be handled?

**Why it matters**: User Story 1 (spec.md:13) describes an evaluator comparing Gameplane to "alternatives" — typically other game server control panels (Pterodactyl, CubeCoders). Agones is a library, not a panel. Including it may confuse evaluators or require unusual "not applicable" cells. However, Agones does compete for Kubernetes-native game server deployment in some scenarios.

**Evidence**:
- spec.md:96 lists "Gameplane, Pterodactyl, CubeCoders AMP, and Agones" (four products)
- spec.md:98 includes dimension "(g) Multi-tenancy & multi-cluster" — Agones has no multi-tenancy concept
- spec.md:98 includes dimension "(e) Access control & authentication" — Agones has no dashboard or user management
- spec.md:98 includes dimension "(f) Game template distribution" — Agones is not a template manager
- R7-competitor-sources.md notes: "Agones is a Kubernetes library, not a control panel; how should dimensions like user authentication and game template distribution (which don't map directly) be documented in the comparison table?"

**Options**:
1. **(a) Replace Agones with a control-panel alternative**: Substitute Agones with another self-hosted control panel (e.g., Lava Server, GameServers.com manager backend, a different open-source panel). Keeps the comparison table focused on panel-to-panel evaluation.
2. **(b) Keep Agones; mark "Not Applicable" cells**: Agones stays in the table; dimensions that don't apply (e.g., "Access Control", "Template Distribution") are marked "N/A — Agones is a library, not a control panel". Educates evaluators about the different category.
3. **(c) Keep Agones; explain the distinction in table notes**: Include Agones columns, fill all dimensions with brief explanations of where Agones differs (e.g., "Access Control: Agones delegates to Kubernetes RBAC; dashboard auth not applicable"). Provides full comparison while being honest about scope.

**Recommended default**: **(b) Keep Agones with "N/A" for non-applicable dimensions** — Rationale: The spec explicitly names Agones, and there is educational value in showing that Agones (a K8s library) and Gameplane (a control panel) serve different use cases, even in the same space. Evaluators benefit from understanding the distinction. "Not applicable" is honest and informative.

**Ruling (2026-09-02)**: Option (b) is chosen. Keep Agones in the table; mark "Not Applicable (Agones is a Kubernetes operator library)" for non-mapping dimensions like Access Control & Authentication and Template Distribution.

**Blocks**:
- FR-001 (comparison table with four products)
- FR-002–FR-008 (comparison accuracy and credibility)
- Task "Populate comparison table" in tasks.md

---

### OD-12: Notation for Agones in comparison table — clarifying the tool category

**Status**: RULED 2026-09-02

**Question**: Should the comparison table (specifically the Agones column header or introductory row) include a notation clarifying that Agones is a Kubernetes library/operator, not a control panel? This would prevent evaluators from assuming parity with Pterodactyl and CubeCoders.

**Why it matters**: An evaluator skimming the table without context might assume "Agones" is a panel offering the same features as Pterodactyl. A clear notation ("Agones: Kubernetes operator library" or similar) immediately sets expectations and prevents misreading.

**Evidence**:
- spec.md:98 states the table compares "feature dimensions" but does not require them to be applicable to all products
- SC-001 states "can identify at least three key architectural or operational differences between Gameplane and each competitor" — understanding Agones's role (library vs. panel) is a key difference
- R7-competitor-sources.md and OD-11 above establish that Agones is a library, not a panel

**Options**:
1. **(a) Add column header or footnote**: Agones column is titled "Agones (Kubernetes Operator Library)" or a footnote states "Agones is a Kubernetes operator; not a game server control panel." Separates it visually from the panels.
2. **(b) Include in intro text above the table**: A paragraph before the table notes: "Gameplane is compared to Pterodactyl and CubeCoders (both control panels) and Agones (a Kubernetes library). Agones and Gameplane both use Kubernetes-native constructs; comparison shows different architectural approaches." Educates without cluttering the table.
3. **(c) No notation; rely on product names and "N/A" cells**: Agones is identifiable by name; "Not applicable" cells implicitly convey the distinction. Assumes evaluators recognize Agones or infer the difference from the table.

**Recommended default**: **(b) Intro text above the table** — Rationale: A short paragraph explaining the scope ("Gameplane vs. other panels and Kubernetes-native tools") is clearer than header notation and educates evaluators upfront. It aligns with SC-001's emphasis on understanding differences. Prevents misreading.

**Ruling (2026-09-02)**: Option (b) is chosen. Add an intro paragraph above the comparison table stating the scope: Pterodactyl and CubeCoders AMP are control panels, Agones is a Kubernetes operator library, and why it is compared. Order in README: intro paragraph, then the FR-004 status line, then the table.

**Blocks**:
- FR-001 (readable, clear comparison table)
- SC-001 (evaluator can identify key differences)
- Task "Document table scope and notation" in tasks.md

---

## New Decision (OD-13)

### OD-13: Credential for the automatic screenshot-refresh pull request

**Status**: RULED 2026-09-02

**Question**: OD-3c specifies that a tag-triggered GitHub Actions workflow (`.github/workflows/screenshot-refresh.yaml`) regenerates MSW fixture data and screenshots, opening a pull request with new images when a release tag is pushed. GITHUB_TOKEN-authored PRs do not trigger CI runs (they lack the necessary trigger permissions to avoid fork-bomb scenarios). Should the workflow use GITHUB_TOKEN, a fine-grained PAT (Personal Access Token) stored as a repository secret, or a GitHub App token to open this PR so that CI runs automatically on the screenshot changes?

**Why it matters**: The screenshot refresh workflow's PR must trigger CI validation (lint, build, tests) before it is merged. Without the right credential, the PR opens with no checks running, breaking the release automation and requiring manual CI trigger or re-run.

**Evidence**:
- OD-3c ruling: "A release-triggered GitHub Actions workflow regenerates MSW fixture data and screenshots, opening a pull request with new images on release tag push."
- GitHub Actions documentation: PRs authored by GITHUB_TOKEN do not trigger on:push or on:pull_request workflows; this is a security feature.
- Standard practice: repos using automated PR creation for release artifacts use PATs or GitHub Apps to author PRs so CI runs normally.

**Options**:
1. **(a) Use GITHUB_TOKEN**: Simple, built-in, requires no credential setup. Cons: CI will not run; PRs require manual trigger or approval before merging. Breaks automation intent.
2. **(b) Use a fine-grained PAT stored in GitHub Secrets**: Maintainer creates a PAT with repo:write and content:read scope, stores it as `SCREENSHOT_BOT_PAT` secret. Workflow uses it to open PRs. Pros: CI runs normally. Cons: requires credential management; PAT renewal needed periodically.
3. **(c) Use a GitHub App token**: Deploy a lightweight GitHub App with repo:write scope; the workflow exchanges the app's private key for a temporary token. Pros: no credential renewal; scoped permissions; audit trail. Cons: requires app setup and GitHub App marketplace presence (or org-only).

**Recommended default**: **(b) Fine-grained PAT** — Rationale: Simplest approach that meets the constraint. PATs are maintainer-managed; renewal is straightforward. A GitHub App adds complexity without benefit for a single workflow. GITHUB_TOKEN fails the requirement entirely.

**Blocks**:
- OD-3c implementation (tag-triggered screenshot refresh workflow)
- FR-016 (automated screenshot updates on release)

**Ruling (2026-09-02)**: Option (b) is chosen. The tag-triggered screenshot-refresh workflow opens its pull request with a fine-grained personal access token scoped to this repository with contents and pull-requests write permission, stored as a repository secret (PRs it opens trigger CI normally; the token is rotated like the other repository secrets). Secret name confirmed 2026-09-02: `SCREENSHOT_BOT_PAT`.

**Amendment (2026-10-07)**: The `SCREENSHOT_BOT_PAT` secret has been extended to cover both the Gameplane repo (GameplanePanel/Gameplane) and the gameplane-website repo (GameplanePanel/website), with Contents + Pull requests read/write permissions. The single token is now used by both the docs step (this repo) and the website step (gameplane-website), replacing the originally separate `WEBSITE_SCREENSHOT_BOT_PAT` design. When the token cannot write to gameplane-website, the website step logs a `::warning::` and exits 0 (skip and stay green). When the token is empty, the website step logs a `::notice::` and exits 0.

---

## New Decisions (OD-14, OD-15)

### OD-14: Allowlist marker for historical version references

**Status**: RULED 2026-09-02

**Question**: The OD-1 gate must not fail on legitimate historical mentions (docs/install.md:54 "Pre-rotation releases (v0.2.0-beta.7 and earlier)", docs/install.md:603, docs/oidc.md:50/91/372 "from v0.2.0-beta.6+", README.md:195). contracts/docs-audit.md only defines example markers ((example version), (example), (placeholder), "# Example:" / "<!-- Example -->" code blocks). How should implementers mark historical version references that are not examples?

**Why it matters**: Without an allowlist marker, the OD-1 script fails CI when it encounters legitimate historical context ("Pre-rotation releases...were signed with the retired key") that mentions an old version. The marker prevents false positives while keeping the gate tight for actual staleness.

**Evidence**:
- research.md baseline list: 6 historical references reclassified as not stale (docs/install.md:54, :603; docs/oidc.md:50, :91, :372; README.md:195)
- contracts/docs-audit.md Version String Checking describes example markers but not historical qualifiers
- OD-1 ruling requires script to distinguish legitimate examples from stale claims

**Options**:
1. **(a) Inline HTML comment marker on the same line**: `<!-- doc-versions: historical -->` visible in source, invisible when rendered
2. **(b) Sidecar allowlist file**: hack/check-doc-versions.allow with path:pattern entries
3. **(c) Phrase heuristics in script**: Script learns patterns like "and earlier", "predate", "from v...+"
4. **(d) Reword the docs**: Eliminate the version literals from historical notes

**Recommended default**: **(a) Inline HTML comment marker on the same line**.

**Ruling (2026-09-02)**: Option (a) is chosen. A historical version literal is allowlisted by an inline HTML comment on the SAME line: `<!-- doc-versions: historical -->`. Visible in source, invisible when rendered, same mechanism family as the example markers. The script treats a line carrying that marker as allowlisted; no sidecar file, no heuristics.

**Blocks**:
- OD-1 script implementation (hack/check-doc-versions.sh must recognize the marker)
- SC-005 pass/fail (version strings match current release or are explicitly marked)

---

### OD-15: First screenshot capture path (CI dispatch only)

**Status**: RULED 2026-09-02

**Question**: Tasks default to dispatching .github/workflows/screenshot-refresh.yaml (workflow_dispatch) on the feature branch, which needs the PAT secret to exist. If blocked, is a one-off local `npm run screenshots` (Playwright, MSW mock mode, no cluster) acceptable, or does CLAUDE.md rule 8 forbid it?

**Why it matters**: Rule 8 restricts local test/lint runs to preserve CI as the source of truth. Screenshots are reproducible and durable artifacts that must be captured in CI (workflow_dispatch). If local execution were permitted as a fallback, the captures could drift between environments.

**Evidence**:
- CLAUDE.md rule 8: "Do NOT run the test or lint suites locally — CI is the source of truth"
- Constitution Principle VI: CI bears heavy lifting
- screenshot-set.md testing section currently says "Run `npm run test:e2e:mock` locally to verify"

**Options**:
1. **(a) CI dispatch only**: No local Playwright run; screenshot-refresh.yaml (workflow_dispatch) on branch; agents never run npm locally
2. **(b) Allow one-off local mock-mode capture as fallback**: If CI secret is missing, run `npm run screenshots` locally; record in audit-log.md
3. **(c) Maintainer captures locally**: Maintainer uses `make screenshots` on dev cluster; commits result

**Recommended default**: **(a) CI dispatch only**.

**Ruling (2026-09-02)**: Option (a) is chosen. CI dispatch only. Screenshots are produced by dispatching screenshot-refresh.yaml (workflow_dispatch) on branch 012-docs-refresh-and-outreach and merging the PR it opens into the feature branch. If the secret is missing the capture waits for the maintainer to create it; agents never run Playwright locally (rule 8, Principle VI). The screenshot-refresh workflow therefore carries a `workflow_dispatch` trigger in addition to `push: tags: ["v*"]`.

**Blocks**:
- screenshot-set.md Testing & Validation section (update instructions to CI-only dispatch)
- FR-016 (automated screenshot updates on release)
- First capture task (must dispatch workflow, not run locally)

---

## Amendment: OD-16

### OD-16: Website screenshot sync — credential and workflow scope

**Status**: RULED 2026-10-07

**Question**: The screenshot-refresh workflow (OD-3c, ruled 2026-09-02) captures dashboard screenshots for the main Gameplane repo (docs/img/). Should the workflow also capture screenshots for the public website (the separate gameplane-website repository) and open PRs there to keep them synchronized?

**Why it matters**: The website displays dashboard UI screenshots to market the product. Currently these are Pencil design renders (outdated). Automating website screenshot sync ensures both repos use current UI captures on every release, improving marketing consistency and reducing manual work.

**Evidence**:
- spec.md Out of Scope: "website/ is out of scope (exception: OD-16 website screenshot sync)"
- OD-3c (ruled 2026-09-02): Screenshot refresh workflow exists for main repo docs/img/
- Maintainer ruling 2026-10-07: Extend screenshot refresh to gameplane-website

**Ruling (2026-10-07)**: The screenshot-refresh workflow EXTENDS to the gameplane-website repo. Website screenshot capture is governed by the SAME fine-grained PAT secret `SCREENSHOT_BOT_PAT` used for the main-repo docs step, which has been extended by the maintainer to cover both GameplanePanel/Gameplane and GameplanePanel/website repositories with Contents + Pull requests write permission. This unified token eliminates the originally-planned separate `WEBSITE_SCREENSHOT_BOT_PAT` design. When the token cannot write to gameplane-website, the website step logs a `::warning::` and exits 0, keeping the run green (skip and stay green behavior per FR-028). When the token is empty, the website step logs a `::notice::` and exits 0. The workflow ALWAYS opens or updates a PR against gameplane-website main and NEVER pushes to main (main auto-deploys GitHub Pages and is protected). The five website screenshots are captured by a separate Playwright spec (web/e2e/specs/websiteScreenshots.spec.ts), distinct from the dashboard-screenshots spec, at 1440×900 viewport with deviceScaleFactor 2 (= 2880×1800 PNG RGBA), output to web/website-screenshots/ (repo path; gitignored). The workflow copies these PNGs to the website clone at src/assets/screenshots/ ({dashboard,servers,console,backups,modules}.png) and opens/updates a PR on gameplane-website main.

**Blocks**:
- .github/workflows/screenshot-refresh.yaml amendment (extend to website)
- web/e2e/specs/websiteScreenshots.spec.ts creation
- specs/done_012-docs-refresh-and-outreach amendment (OD-16, FRs, contracts, tasks)

---

## Summary Table

| ID | Title | Blocks | Status |
|---|---|---|---|
| OD-1 | CI auto-update of version strings on release tag | SC-005, version-refresh task | RULED |
| OD-2 | Link checking tooling and scope | SC-006, link-validation task | RULED |
| OD-3 | Screenshot capture environment and method | FR-015/016/017, screenshot task | RULED |
| OD-4 | Whether tunnel is in FR-012 label set | FR-012, labeling task | RULED |
| OD-5 | Outreach submissions require maintainer-owned accounts | FR-020/022/025, submission workflow | RULED |
| OD-6 | Eligibility of projects for each external directory | SC-014, outreach task | RULED |
| OD-7 | FR-014 versus docs/roadmap.md unshipped features | FR-014, unshipped-feature audit | RULED |
| OD-8 | Features merged after v0.2.0-beta.8 but documented on master | SC-004/005, feature-status audit | RULED |
| OD-9 | CubeCoders AMP comparison table sourcing strategy | FR-005, CubeCoders research task | RULED |
| OD-10 | 404 dimension URLs for Pterodactyl/Agones — fallback sourcing | FR-005, competitor-research task | RULED |
| OD-11 | Agones is a library, not a control panel — scope and framing | FR-001/002, table design | RULED |
| OD-12 | Notation for Agones in comparison table | FR-001, table clarity | RULED |
| OD-13 | Credential for the automatic screenshot-refresh pull request | OD-3c workflow credential | RULED |
| OD-14 | Allowlist marker for historical version references | OD-1 script, SC-005 gate | RULED |
| OD-15 | First screenshot capture path (CI dispatch only) | FR-015/016/017, capture task | RULED |
| OD-16 | Website screenshot sync — unified-token credential and workflow scope (amended 2026-10-07) | workflow-refresh amendment, websiteScreenshots.spec; single `SCREENSHOT_BOT_PAT` token covers both repos | RULED |

---

## Notes for Implementation

1. **All sixteen decisions ruled** — OD-1 through OD-15 ruled 2026-09-02; OD-16 ruled 2026-10-07; none remain open, ready for implementation.
2. **Recommended defaults are proposals, not decisions** — each recommendation is clearly labelled and may be overridden.
3. **Evidence is path:line traceable** — every claim cites the spec, research files, codebase, or constitution.
4. **Interdependencies exist** — OD-3 (screenshot environment) affects captured data freshness; OD-2 (link checking) enables SC-006 validation; OD-1/2 together determine pre-merge CI gates.
5. **Constitution Principle I (E2E Delivery) does not apply literally** — this is a docs-only feature (spec.md Assumption "No Design Changes Required"). Record in Complexity Tracking as PASS-WITH-JUSTIFICATION per done_011 precedent.
