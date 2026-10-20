# Data Model: Documentation Refresh, Comparison Table, and Outreach

## Overview

This document defines the entities that feature 012 implementation will create, modify, and validate. The feature spans five major deliverables: comparison table in README, documentation accuracy audit, dashboard screenshots, external outreach tracking, and internal audit record. Each entity carries validation rules derived from the FRs, SCs, and orchestrator decisions (D-A through D-I).

---

## Entity: ComparisonTable

**Definition**: The side-by-side feature comparison matrix in README.md, positioned after "Why Gameplane?" and before "Features" (placement per D-H).

### Structure

**Location**: `README.md` lines [TBD by implementation]  
**Format**: Markdown table (GitHub-flavored)  
**Columns** (order per FR-002): Gameplane, Pterodactyl, CubeCoders AMP, Agones  
**Rows** (order per FR-002): (a) through (i), one per dimension

| Dimension | Gameplane Claim | Pterodactyl Source | CubeCoders Source | Agones Source |
|-----------|-----------------|-------------------|------------------|---------------|
| (a) Deployment/runtime model | [cell] | [cell] | [cell] | [cell] |
| (b) Scaling & auto-sleep | [cell] | [cell] | [cell] | [cell] |
| (c) Inbound connectivity (NAT/relay) | [cell] | [cell] | [cell] | [cell] |
| (d) Backup and restore | [cell] | [cell] | [cell] | [cell] |
| (e) Access control & authentication | [cell] | [cell] | [cell] | [cell] |
| (f) Game template distribution | [cell] | [cell] | [cell] | [cell] |
| (g) Multi-tenancy & multi-cluster | [cell] | [cell] | [cell] | [cell] |
| (h) Licensing | [cell] | [cell] | [cell] | [cell] |
| (i) Target operator scope | [cell] | [cell] | [cell] | [cell] |

### Constraints

**V-CT1 (Status Line, FR-004)**: Directly above the table MUST appear the status line:  
```
Status: **beta** (`v0.2.0-beta.8`). The operator, API, agent, and dashboard are 
feature-complete for the v1 scope and stabilized for external testing.
```
Evidence: README.md:8–9.

**V-CT2 (Cell Content, FR-006)**: Each cell contains a single factual claim (max ~25 words) describing feature presence, design choice, or "not applicable". No value judgments, speculation, or unverifiable claims. Evidence: SC-001, SC-002.

**V-CT3 (Gameplane Column, FR-003, FR-008, SC-002)**: Every Gameplane column cell MUST:
- Cite a code artifact or doc section as evidence (see ComparisonCell rules below)
- Include all applicable qualifiers from D-C vocabulary (e.g., [optional], [BETA], [local cluster only])
- First mention of a feature in Gameplane column carries the qualifiers (FR-012)

**V-CT4 (Competitor Columns, FR-005, SC-003)**: Every competitor column cell MUST:
- Link to or cite a dated source (SourceReference entity)
- Not make claims beyond what the official documentation states
- Mark "not applicable" if the competitor does not support the dimension

**V-CT5 (Dimensional Scope, FR-002)**: All nine dimensions (a–i) MUST be represented as rows. No omissions, no reordering of row sequence.

### Evidence Trail

- Table placement: D-H (after "## Why Gameplane?", before "## Features")
- Column order: Gameplane, Pterodactyl, AMP, Agones (per FR-002 order in spec)
- Row order: (a) through (i) in spec FR-002 sequence
- Status line source: README.md:8–9

---

## Entity: ComparisonCell

**Definition**: A single cell in the ComparisonTable, containing a brief feature description and qualifiers.

### Properties

| Property | Type | Required | Constraints |
|----------|------|----------|-------------|
| `product` | enum | Yes | "Gameplane" \| "Pterodactyl" \| "CubeCoders AMP" \| "Agones" |
| `dimension` | enum | Yes | (a) through (i) per FR-002 |
| `text` | string | Yes | 1–25 words; factual, no value judgments (V-CC1, FR-006) |
| `qualifiers` | array of string | Conditional | [optional], [experimental], [disabled by default], [BETA], [local cluster only]; Gameplane cells only (V-CC2) |
| `sourceReference` | SourceReference | Conditional | Competitor cells only; Gameplane cells have `repoEvidence` instead (V-CC3) |
| `repoEvidence` | string | Conditional | Gameplane cells only; path:line citation (e.g., "README.md:36; operator/api/v1alpha1/gameserver_types.go:137–195"); screenshot delivery executed via workflow_dispatch (OD-15) |
| `notApplicable` | boolean | Optional | If true, `text` is exactly "not applicable" and `sourceReference`/`repoEvidence` are absent; EXCEPT Agones column, which uses "not applicable (Agones is a Kubernetes operator library)" with a SourceReference anchor (see V-CC5) |

### Validation Rules

**V-CC1 (Factual Content, FR-006)**: Text MUST describe:
- A feature's presence or absence (e.g., "Idle auto-sleep with...wake-on-connect")
- A design choice (e.g., "Kubernetes-native CRDs...")
- Or state "not applicable"
  
MUST NOT: speculate, judge value, or claim unverified features. Evidence: SC-001.

**V-CC2 (Qualifiers, D-C, FR-003, FR-012)**: Gameplane cells only. Applicable qualifiers are:
- [optional] — feature disabled by default (values.yaml toggle or optional component)
- [experimental] — feature under development or in beta (declared in CLAUDE.md or code)
- [disabled by default] — synonym for [optional] in some contexts (both acceptable)
- [BETA] — feature shipped but not v1 GA
- [local cluster only] — feature has documented limitations
- Qualifier-specific patterns (e.g., "Minecraft/Terraria [handshake]; others [heuristic]")

All tags are sourced from D-C and CLAUDE.md. Evidence: SC-007, SC-008, R3 label registry.

**V-CC3 (Sourcing, SC-003)**: 
- **Gameplane cells**: Include `repoEvidence` as path:line citations (e.g., "README.md:36; operator/api/v1alpha1/gameserver_types.go:137–195"). All claims traceable to code, CLAUDE.md, or docs.
- **Competitor cells**: Include `sourceReference` (SourceReference entity) with URL, date checked, and what was verified.

**V-CC4 (Word Count, FR-006)**: Text field MUST be ≤25 words for readability in a rendered table.

**V-CC5 ("Not Applicable" Representation)**: Ruled 2026-09-02 (OD-11 option b — "Keep Agones with 'Not Applicable' cells"), if a dimension does not apply to a competitor (e.g., Agones lacks user authentication because it is a library, not a control panel), the cell MUST state "not applicable (Agones is a Kubernetes operator library)" and MUST include a SourceReference ([A-x] anchor) documenting the "operator-library" classification in `docs/comparison-sources.md`, even though the cell text is standardized. `notApplicable` boolean set to true indicates this is a not-applicable cell, but the SourceReference still links to the operator-library explanation. For CubeCoders AMP cells where a dimension is not publicly documented, use "not publicly documented (checked YYYY-MM-DD)".

---

## Entity: SourceReference

**Definition**: A dated, verifiable source for a claim in a competitor's ComparisonCell.

### Properties

| Property | Type | Required | Constraints |
|----------|------|----------|-------------|
| `id` | string | Yes | Unique identifier within `docs/comparison-sources.md` (letter-based format per comparison-table.md §8: e.g., "P-a", "A-h") |
| `product` | enum | Yes | "Pterodactyl" \| "CubeCoders AMP" \| "Agones" |
| `dimension` | enum | Yes | (a) through (i) per FR-002 |
| `url` | string | Yes | Canonical source URL (GitHub README, official docs, license file); defaults to "source URL unavailable (checked YYYY-MM-DD)" if URL hunt fails (OD-10) |
| `dateChecked` | date | Yes | ISO 8601 (YYYY-MM-DD) when source was verified (FR-005, SC-003) |
| `whatWasVerified` | string | Yes | Human-readable summary (1–2 sentences) of what was confirmed at this URL (FR-005) |
| `lastKnownUrl` | string | Optional | Note if source URL moved; cite where feature was last found (spec edge case, FR-005) |

### Storage

All SourceReference entries are stored in a single file: `docs/comparison-sources.md` (per D-A).

### Format in docs/comparison-sources.md

Each entry occupies 3–5 lines:

```
### Pterodactyl — (a) Deployment/runtime model
- **URL**: https://pterodactyl.io/wings/1.0/installing.html
- **Date Checked**: 2026-09-01
- **Verified**: Docker-native daemon model; requires systemd services or standalone Wings binary
- **Last Known URL**: (none; current as of 2026-09-01)
```

Entry order: product (Pterodactyl, then AMP, then Agones), then dimension (a–i).

### Validation Rules

**V-SR1 (URL Reachability, SC-003, FR-005)**: Each `url` MUST:
- Be publicly reachable (verified by WebFetch or browser at time of check)
- Point to the canonical, official documentation or license file
- Be cited in the comparison table cell (inline reference or note)

**V-SR2 (Date Freshness, SC-003)**: `dateChecked` MUST:
- Be within the implementation phase (expected 2026-09-01 through 2026-09-30)
- Be no older than the feature completion date
- Reflect when the source was last verified (not when it was originally published)

**V-SR3 (Moved Sources)**: If a source URL becomes inaccessible after implementation:
- `lastKnownUrl` MAY record where the feature was last confirmed (e.g., "Confirmed via GitHub at https://github.com/.../blob/commit/...")
- The entry is not removed from `docs/comparison-sources.md`; it documents the research trail
- If a URL hunt fails to locate a source, the `url` field defaults to "source URL unavailable (checked YYYY-MM-DD)" (ruled 2026-09-02, OD-10)

**V-SR4 (CubeCoders Special Case, R7, OD-9)**: CubeCoders AMP is proprietary and does not have a public GitHub repo. Its documentation site (https://www.cubecoders.com/AMP) requires JavaScript rendering and is not WebFetch-accessible. Ruled 2026-09-02 (OD-9 option b):
- For CubeCoders dimensions verifiable from an official source: fill cells with verified content and cite source with date
- For CubeCoders dimensions NOT verifiable from public sources: cell text MUST read exactly "not publicly documented (checked YYYY-MM-DD)"; no SourceReference needed

**V-SR5 (Screenshot Capture Source, OD-15)**: The capture source for the 11+ dashboard screenshots is CI-only execution (GitHub Actions workflow_dispatch or tag-triggered screenshot-refresh.yaml), never local Playwright execution (per CLAUDE.md rule 8 and Principle VI).

---

## Entity: AuditedFile

**Definition**: A documentation file subject to audit per FR-009 and the 17-file scope.

### Registry (Fixed)

Per FR-009 and the scope, these 17 files are audited:

1. README.md
2. docs/architecture.md
3. docs/contributing.md
4. docs/dependencies.md
5. docs/game-coverage.md
6. docs/install.md
7. docs/key-rotation.md
8. docs/module-authoring.md
9. docs/networking.md
10. docs/notifications.md
11. docs/oidc.md
12. docs/roadmap.md
13. docs/security.md
14. docs/tunnels.md
15. audit-syslog-bridge/README.md
16. mcp-server/README.md
17. telemetry-receiver/README.md

### Additional Files (Created During Implementation)

18. **docs/comparison-sources.md** — Created as part of D-A (comparison table sourcing), becomes an audited file in subsequent audits; uses explicit HTML anchors for row headings (corrected 2026-09-02 per maintainer-signed contract correction; `{#id}` heading attributes are not supported by GitHub-flavored Markdown, see comparison-table.md section 9)
19. **docs/img/*.jpg** (6 refreshed + at least 5 new) — Created during screenshot capture phase via CI workflow_dispatch (OD-15, ruled 2026-09-02)

### Exclusions

- docs/superpowers/** (dated design records; exempt per spec Out of Scope)
- website/ (submodule; exempt per spec Out of Scope except website screenshot sync per OD-16)
- modules/ (submodule; exempt per spec Out of Scope)

---

## References

- **Orchestrator Decisions**: `OPEN-DECISIONS.md` (OD-1 through OD-15, all ruled 2026-09-02)
- **Version History**: Audit performed 2026-09-01, decisions finalized 2026-09-02
- **Maintainer Approvals**: All fifteen open decisions ruled per system-wide authority 2026-09-02

### Exception: CHANGELOG.md

CHANGELOG.md is normally exempt as a historical record. However, ruled 2026-09-02 (OD-8), entries in CHANGELOG.md Unreleased that docs describe MUST be verified: if an entry shipped in v0.2.0-beta.8, move it into the beta.8 section; if it is unreleased, add "(unreleased; ships in the next release)" next to the doc mention. CHANGELOG.md becomes MODIFIED only if corrections are needed.

### Audit Scope per FR-010

Each file is checked for:

1. **Version strings** (FR-010.a): Gameplane versions MUST match appVersion 0.2.0-beta.8 or be marked as examples (SC-005). Evidence: R1-versions.md.
2. **Feature descriptions** (FR-010.b): Feature claims MUST match implementation (operator, api, agent code). Evidence: R5-gameplane-facts.md.
3. **Internal links** (FR-010.c): All `[text](url)` links to other docs or anchors MUST resolve to existing targets. Evidence: R2-links.md.
4. **Optional/experimental labeling** (FR-010.d + FR-012): Features marked optional/experimental/beta in CLAUDE.md or values.yaml MUST be consistently labeled in all audited files at first mention. Evidence: R3-labels.md.
5. **Roadmap shipped/planned markers** (FR-010 exception per OD-7): docs/roadmap.md entries MUST carry explicit "(shipped vX.Y.Z)" or "(planned)" markers for clarity. Evidence: OD-7 ruling, 2026-09-02.

---

## Entity: AuditFinding

**Definition**: A correction or gap identified during the audit, documented in audit-log.md (per D-F).

### Properties

| Property | Type | Required | Constraints |
|----------|------|----------|-------------|
| `file` | string | Yes | Relative path (e.g., "docs/install.md") |
| `lines` | string | Yes | Single line (e.g., "14") or range (e.g., "14–16") |
| `category` | enum | Yes | version \| feature-description \| link \| label \| unshipped |
| `finding` | string | Yes | Human-readable statement of the correction (1–2 sentences) |
| `evidenceChecked` | string | Yes | Path:line citation of what was verified (e.g., "charts/gameplane/Chart.yaml:6") |
| `resolution` | string | Yes | Action taken (e.g., "Updated version string from 0.2.0-beta.7 to 0.2.0-beta.8") |
| `commit` | string | Yes | Commit hash or message fragment where fix was applied |

### Category Definitions

| Category | Meaning | Example |
|----------|---------|---------|
| **version** | Version string mismatch against appVersion | Gameplane version in example is 0.2.0-beta.7; should be 0.2.0-beta.8 |
| **feature-description** | Feature claim doesn't match code or CLAUDE.md | Doc says feature X is shipped; code shows it's [experimental] |
| **link** | Broken or invalid internal link or anchor | Link to non-existent anchor; path resolves to wrong file |
| **label** | Optional/experimental/beta qualifier missing at first mention | Component listed without [optional] tag where required |
| **unshipped** | Feature presented as available that's not yet released | Feature listed as shipped but only in CHANGELOG Unreleased section |

### Storage & Validation Rules

**V-AF1 (Audit Log File, D-F)**: All findings are recorded in a single file:  
**Path**: `/home/user/Gameplane/specs/done_012-docs-refresh-and-outreach/audit-log.md`  
**Format**: Markdown table with columns matching Properties above  
**Order**: Chronological by implementation phase (findings discovered and fixed in order)

**V-AF2 (Evidence Traceability, FR-011, SC-001)**: Each finding MUST cite the source of the audit claim (path:line of code, spec, or CLAUDE.md) in `evidenceChecked`. Example:
- Version mismatch: cite charts/gameplane/Chart.yaml:6 (appVersion truth source)
- Feature claim: cite operator/api/v1alpha1/gameserver_types.go:1 (code evidence)
- Label missing: cite docs/install.md:191 (first mention) + CLAUDE.md:368 (optional marking)

**V-AF3 (Resolution Record, SC-001)**: Each finding must record what action was taken:
- "Updated version from X to Y"
- "Added [optional] qualifier"
- "Corrected link from A to B"
- "Marked as [BETA]"
- "Withdrew finding; feature is correctly documented as <reason>"

**V-AF4 (Commit Reference)**: Commit hash or conventional-commit subject (e.g., "fix: docs version string in install.md"). Allows git blame to trace the change back to its justification in the spec/audit.

### Relation to Audit Checklist

Research deliverables (R1–R8) identify findings:
- **R1** → version findings (category: version)
- **R2** → link findings (category: link)
- **R3** → label findings (category: label)
- **R4** → screenshot findings (separate entity; not AuditFinding)
- **R5** → feature-description findings (category: feature-description)
- **R6** → outreach findings (separate entity; not AuditFinding)
- **R7** → sourcing findings (ComparisonCell entity; not AuditFinding)
- **R8** → unshipped findings (category: unshipped)

---

## Entity: LabelRegistryEntry

**Definition**: A component or feature with optional/experimental/beta status, tracked across the 17 audited files for consistency (FR-012, SC-007, R3).

### Properties

| Property | Type | Required | Constraints |
|----------|------|----------|-------------|
| `component` | string | Yes | Component name (e.g., "sentinel", "capture-sidecar", "mcp-server", "postgres") |
| `toggleKey` | string | Conditional | values.yaml Helm value key (e.g., "operator.sentinelImage") or "N/A" if not configurable |
| `defaultEnabled` | boolean | Conditional | Default value of the toggle in values.yaml (e.g., false = disabled by default) |
| `derivedTags` | array of string | Yes | Applicable D-C tags: [optional], [experimental], [disabled by default], [read-only] |
| `firstMentionRule` | string | Yes | FR-012 requirement: "First mention in each file MUST carry the tag" |
| `seedData` | object | Yes | From R3 findings; includes CLAUDE.md line, values.yaml line, first-mention files |

### Examples

**Sentinel (wake-on-connect)**
```json
{
  "component": "sentinel",
  "toggleKey": "operator.sentinelImage",
  "defaultEnabled": false,
  "derivedTags": ["[optional]"],
  "firstMentionRule": "FR-012 applied at first mention in each of: README.md:110, docs/install.md:191, docs/architecture.md:268",
  "seedData": {
    "claudeMdLine": "CLAUDE.md:368 — 'optional wake-on-connect component'",
    "valuesYamlLine": "charts/gameplane/values.yaml:58 — operator.sentinelImage: ''",
    "notMentionedIn": ["docs/game-coverage.md", "docs/key-rotation.md", "docs/module-authoring.md", "docs/networking.md", "docs/notifications.md", "docs/oidc.md"]
  }
}
```

**PostgreSQL Driver**
```json
{
  "component": "Postgres driver",
  "toggleKey": "N/A (build tag: postgres)",
  "defaultEnabled": false,
  "derivedTags": ["[experimental]"],
  "firstMentionRule": "FR-012: Marked [experimental] at first mention in docs/architecture.md:18, docs/install.md:120",
  "seedData": {
    "claudeMdLine": "CLAUDE.md:architecture section — 'PostgreSQL (experimental, work-in-progress...'",
    "valuesYamlLine": "charts/gameplane/values.yaml:128–132 — api.db.driver toggle with postgres tag",
    "notMentionedIn": ["docs/oidc.md", "docs/tunnels.md", "README.md (not listed)"]
  }
}
```

### Registry Membership

Per R3 and D-C, the registry includes:
1. sentinel [optional]
2. capture-sidecar [optional]
3. mcp-server [optional] [read-only]
4. audit-syslog-bridge [optional]
5. telemetry-receiver [optional]
6. tunnel (relay supervisor) [optional] — ruled 2026-09-02 (OD-4: tunnel is included in the six optional components, tagged [optional] at first mention in each audited file)
7. Postgres driver [experimental]

### Validation Rules

**V-LR1 (Consistency, SC-007, SC-008)**: All 17 audited files that mention a component MUST tag it consistently with its registry entry's `derivedTags`. A component with [optional] MUST be marked [optional] (or equivalent like [disabled by default]) in every file where it appears (unless in a list context where a preamble covers all items).

**V-LR2 (First Mention, FR-012)**: The first mention of a component in each audited file MUST carry the applicable tag. Subsequent mentions do not require re-tagging if the context is clear (e.g., "the sentinel" after "sentinel [optional]" in the same section).

**V-LR3 (Source of Truth)**: Derived tags come from:
- CLAUDE.md marking (repo map section)
- values.yaml default values (false = disabled by default = [optional])
- Code marking (build tags, feature gate flags)

Evidence sourced from R3 findings.

---

## Entity: Screenshot

**Definition**: A dashboard UI image in docs/img/, showing a current feature or workflow.

### Properties

| Property | Type | Required | Constraints |
|----------|------|----------|-------------|
| `filename` | string | Yes | Kebab-case, .jpg extension (e.g., "login.jpg", "create-server-template-select.jpg") |
| `route` | string | Yes | Dashboard route where image was captured (e.g., "/login", "/servers/$name?tab=events") |
| `status` | enum | Yes | "refreshed" (existing six updated) \| "new" (added per FR-017) |
| `altText` | string | Yes | Descriptive alt text (one sentence) per FR-018; disclosure of mock-mode testing moved to gallery-intro sentence (OD-3d) |
| `purpose` | string | Yes | What the screenshot demonstrates (e.g., "Authentication entry point", "Server event timeline") |
| `dummyDataConstraints` | object | Conditional | For "new" screenshots, list allowed/forbidden data patterns per FR-019 |
| `format` | object | Yes | JPEG, 1920×1080 per convention (ruled 2026-09-02, OD-3a); all eleven images captured at 1920×1080 (existing six are 1568×773 until recaptured); max ~100 KB |
| `captureMethod` | enum | Yes | "playwright-mock" (OD-3b, OD-15; sole authorized capture path via GitHub Actions CI workflow_dispatch or tag-triggered run in `.github/workflows/screenshot-refresh.yaml`; GitHub Actions provenance validated per V-S4) |

### Registry: Existing Six (Refreshed)

| Filename | Route | Status | Alt Text Seed |
|----------|-------|--------|---|
| dashboard.jpg | / | refreshed | Fleet health at a glance: running/stopped/failed counts, cluster CPU/memory/storage, node list, recent activity |
| servers-list.jpg | /servers | refreshed | Every game server in a list: status, CPU, memory, node placement |
| server-overview.jpg | /servers/$name Overview | refreshed | Server detail Overview tab: CPU/memory/disk usage, quick actions, connection info |
| mods-registry-browse.jpg | /servers/$name Mods | refreshed | Mods registry browser: grid of game mods, download counts |
| server-console.jpg | /servers/$name Console | refreshed | Live streaming console: server stdout with xterm output |
| admin-mod-registries.jpg | /admin Mod registries | refreshed | Admin Settings Mod registries section: CurseForge and Steam Workshop configured |

### Registry: New Screenshots (Recommended per FR-017)

| Filename | Route | Status | Purpose | Alt Text Seed |
|----------|-------|--------|---------|---|
| login.jpg | /login | new | Authentication entry point | Sign-in form: username/password and SSO provider buttons, brand logo, static marketing pitch sidebar |
| create-server-template-select.jpg | /servers/new (Template step) | new | Game selection in wizard | Game template grid: Minecraft, Valheim, Terraria, Rust, Palworld, Factorio, CS2, ARK with icons and Select buttons |
| server-detail-events.jpg | /servers/$name?tab=events | new | K8s event diagnostics | Events tab: timeline of K8s events (image pull, scheduling, provisioning); shows reason and message for failures |
| admin-settings-general.jpg | /admin?section=general | new | Instance identity config | General Settings section: Instance Name, External URL, Default Namespace fields; sidebar with all 9 settings sections |
| cluster-nodes.jpg | /cluster | new | Multi-node management | Cluster page: node list with three nodes, CPU/memory/storage meters, uptime, capacity info, join wizard card |
| server-detail-logs.jpg | /servers/$name?tab=logs | new | Application log streaming | Logs tab: live server output (game server stdout/stderr lines), typical game-server output (world load, player joins) |

### Validation Rules

**V-S1 (Format Consistency, OD-3a)**: All screenshots MUST be:
- JPEG format (lossy compression, .jpg extension)
- 1920×1080 pixels (Desktop convention, ruled 2026-09-02; existing six are 1568×773 until recaptured at new size)
- File size ≤100 KB (existing files range 47–74 KB; new size target ~100 KB with lossy JPEG)

**V-S2 (Alt Text, FR-018, SC-010, OD-3d)**: Alt text MUST:
- Describe the screen's purpose in one sentence
- List key UI elements visible in the screenshot
- NOT be a raw label (e.g., not just "Login page", but "Sign-in form with local username/password and OAuth provider buttons")
- NOT mention "mock mode" or "mocked data" — disclosure of test/mock-mode testing is a single sentence above the README screenshot gallery (OD-3d), not per-image
- Be human-readable and accessible per WCAG standards

**V-S3 (Dummy Data, FR-019, SC-011)**: Screenshots MUST NOT display:
- Real user data (actual player names, real server hostnames, real IP addresses)
- Real cluster identities (production cluster names, real nodes, actual CPU/memory values if they reveal infrastructure)

Timestamps are not sensitive per FR-019; render as-is (see contracts/screenshot-set.md, Note on Timestamps).

Screenshots MAY display:
- Test/dummy data (e.g., "test-server-01", "Minecraft", "dummy-admin", "example@domain.com")
- Synthetic resource usage (e.g., 2.5 CPU cores, 4 GB memory)
- Mock player lists (e.g., "Player_1", "Player_2")
- Example configurations (e.g., "My Gameplane Cluster", mock Discord webhook URLs in redacted form)

**V-S4 (Capture Path, R4, OD-3b, OD-15)**: All screenshots (refreshed + new) MUST be captured via GitHub Actions CI only (OD-15, ruled 2026-09-02). The sole authorized capture method is Playwright mock mode via the tag-triggered or workflow_dispatch `.github/workflows/screenshot-refresh.yaml` workflow (MSW + Vite, no cluster required; source: web/e2e/specs/screenshots.spec.ts, OD-3b). No other capture path (local live mode, manual browser capture) is authorized. Any future capture path requires a new maintainer ruling.

**V-S5 (Route Validation, R4)**: Each screenshot's `route` MUST exist in web/src/router/tree.tsx and be routable by the dashboard. No screenshots of broken/unimplemented routes.

---

## Entity: OutreachEntry

**Definition**: A tracked submission to an external directory, with status lifecycle per D-D.

### Properties

| Property | Type | Required | Constraints |
|----------|------|----------|-------------|
| `target` | enum | Yes | "AlternativeTo.net" \| "Awesome-Selfhosted" \| "Awesome-Kubernetes" |
| `submissionUrl` | string | Conditional | URL of submission portal (GitHub PR, web form, etc.); omitted if status is "pending" |
| `status` | enum | Yes | "pending" \| "in-progress [YYYY-MM-DD]" \| "submitted [YYYY-MM-DD]" \| "rejected [YYYY-MM-DD]" \| "deferred [YYYY-MM-DD]" |
| `submittedRef` | string | Conditional | PR number, Issue number, or submission ID (e.g., "#456", "PR https://github.com/.../pull/789", "Form submission ID: xyz") |
| `notes` | string | Optional | Human-readable notes on status, blockers, or next steps |
| `history` | array of object | Optional | Changelog of status transitions (date, old status, new status, reason) |

### Status Enum & State Machine

Per D-D and SC-014:

```
INITIAL:    pending
TRANSITIONS:
  pending → in-progress [YYYY-MM-DD]        (submission work begun)
  pending → deferred [YYYY-MM-DD, reason]   (blocked; submission not attempted)
  
  in-progress → submitted [YYYY-MM-DD]      (submission completed; awaiting review)
  in-progress → deferred [YYYY-MM-DD, reason] (blocked mid-submission; paused)
  
TERMINAL STATES (per SC-014):
  submitted [YYYY-MM-DD]  — submission made; awaiting third-party acceptance (informational only)
  deferred [YYYY-MM-DD, reason] — blocked or scheduled for later; not submitted
```

### Registry: Three Targets

Per FR-020, FR-021, and R6:

| # | Target | Submission Portal | Blocker(s) | Recommended Initial Status | Evidence |
|---|--------|------------------|-----------|---------------------------|----------|
| 1 | AlternativeTo.net | Web form (account-based) | None identified | pending | R6: No blockers; eligible for immediate submission (OD-5 agents draft, maintainer submits) |
| 2 | Awesome-Selfhosted | PR to awesome-selfhosted-data repo | 4-month age minimum (first release 2026-06-22; eligible 2026-10-22) | deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22] | R6: Age blocker documented; CHANGELOG.md:656 (first release 2026-06-22); ruled 2026-09-02 (OD-6a) as terminal state (SC-014) |
| 3 | Awesome-Kubernetes | PR to ramitsurana/awesome-kubernetes | 25+ GitHub stars, 3+ contributors (metrics unknown; eligibility rule not verified) | deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release] | R6: Star/contributor metrics unknown; ruled 2026-09-02 (OD-6b) deferred WITHOUT pre-check |

### Storage & Validation Rules

**V-OE1 (Outreach List File, FR-020, SC-012)**: All entries are stored in a single file:  
**Path**: `/home/user/Gameplane/specs/done_012-docs-refresh-and-outreach/outreach.md`  
**Format**: Markdown list or table, one entry per target

**V-OE2 (Status Transitions, D-D, SC-014)**: Status MUST follow the state machine above. Transitions:
- MUST include the date of transition in [YYYY-MM-DD] format
- Terminal states (submitted, deferred) MUST include a reason if deferred (e.g., "[age blocker]", "[stars < 25]")
- Every status change MUST be committed to git (FR-022)

**V-OE3 (Acceptance NOT a Success State, FR-023, SC-014)**: If a third-party directory accepts the submission (e.g., Awesome-Selfhosted approves the PR), this is recorded in `notes` (informational only) and does NOT change the status. Success = submission made + tracked, not acceptance.

**V-OE4 (Linked from docs/contributing.md, FR-025)**: The outreach.md file MUST be linked from docs/contributing.md so maintainers can find it. Suggested line:  
"See [External Outreach Tracking](../specs/done_012-docs-refresh-and-outreach/outreach.md) for visibility into third-party directory submissions."

**V-OE5 (Git Commit per Transition, FR-022)**: Every status change is its own commit. Example:
```
docs: record AlternativeTo.net outreach submission [2026-09-15]
```

---

## Requirement-to-Entity Traceability Table

This table maps every FR (Functional Requirement) and SC (Success Criterion) to the entity or contract that satisfies it. No requirement may be unmapped.

| ID | Requirement | Entity | Implementation Notes |
|---|---|---|---|
| FR-001 | README comparison table in position | ComparisonTable | Placement: after "Why Gameplane?", before "Features" (D-H) |
| FR-002 | Nine dimensions (a–i) included | ComparisonTable | Rows in exact order (a) through (i); no omissions |
| FR-003 | Gameplane column accurate, with qualifiers | ComparisonCell | V-CC2, V-CC3 validate qualifiers and sourcing |
| FR-004 | Status line in table | ComparisonTable | V-CT1 requires exact status text from README.md:8–9 |
| FR-005 | Competitor cells sourced and dated | SourceReference | V-SR1, V-SR2 mandate URL reachability and date freshness |
| FR-006 | No disparaging claims, factual only | ComparisonCell | V-CC1 restricts content to factual descriptions, no value judgments |
| FR-007 | Markdown table format | ComparisonTable | GitHub-flavored markdown; column/row headers clear |
| FR-008 | Limited claims not overstated | ComparisonCell | V-CT3 requires qualifiers like [local cluster only] for scoped features |
| FR-009 | Audit 17 specified files | AuditedFile | Registry lists all 17 files; FR-010 defines audit scope |
| FR-010 | Audit verifies versions, descriptions, links, labels | AuditedFile | V-AF2, V-AF3, V-AF4 require evidence and resolution recording |
| FR-011 | Each correction cites evidence | AuditFinding | V-AF2 mandates path:line citations in `evidenceChecked` |
| FR-012 | Optional/experimental components labeled at first mention | LabelRegistryEntry | V-LR1, V-LR2 track first-mention consistency across all files |
| FR-013 | Outdated claims corrected | AuditFinding | Category "feature-description"; findings record corrections made |
| FR-014 | No unshipped features announced | AuditFinding | Category "unshipped"; findings identify and correct false positives |
| FR-015 | Refresh six existing + add new screenshots | Screenshot | Registry lists 6 refreshed + 5 mandatory + 1 recommended new screenshots |
| FR-016 | Refreshed screenshots match current UI | Screenshot | V-S5 validates routes exist; capture method ensures current rendering |
| FR-017 | New screenshots cover high-priority screens | Screenshot | Registry specifies six recommended new screenshots by priority |
| FR-018 | All screenshots have alt text | Screenshot | V-S2 specifies alt text requirements (purpose + key elements) |
| FR-019 | No real user/cluster data in screenshots | Screenshot | V-S3 lists forbidden patterns and allowed dummy data |
| FR-020 | To-do list created | OutreachEntry | outreach.md file; registry lists three targets |
| FR-021 | Three external directories listed | OutreachEntry | Registry members: AlternativeTo.net, Awesome-Selfhosted, Awesome-Kubernetes |
| FR-022 | Status updates committed to git | OutreachEntry | V-OE5 requires separate commit per status transition |
| FR-023 | Success = submission made, not acceptance | OutreachEntry | V-OE3 marks acceptance as informational, not a status change |
| FR-024 | Submission reference noted | OutreachEntry | `submittedRef` property; `notes` field for audit trail |
| FR-025 | To-do list linked from docs/contributing.md | OutreachEntry | V-OE4 specifies linking requirement |
| SC-001 | Evaluator sees three key differences per competitor | ComparisonTable + ComparisonCell | Table content per V-CT2, V-CC1 supports this |
| SC-002 | Gameplane claims traceable to code/docs | ComparisonCell | V-CC3 requires `repoEvidence` citations |
| SC-003 | Competitor claims dated and sourced | SourceReference | V-SR1, V-SR2 mandate reachability and date freshness |
| SC-004 | New self-hoster doesn't hit stale docs | AuditedFile + AuditFinding | Audit corrects version strings, links, descriptions |
| SC-005 | Version strings match 0.2.0-beta.8 or marked as examples | AuditFinding | Category "version"; V-AF2 traces to Chart.yaml:6 truth source |
| SC-006 | Internal links resolve (automated checking) | AuditFinding | Category "link"; findings identify broken links; R2 establishes baseline |
| SC-007 | Optional/experimental consistently labeled | LabelRegistryEntry | V-LR1 mandates consistency; V-LR2 requires first-mention tagging |
| SC-008 | Audit resolves version/link/label issues | AuditFinding | V-AF1 logs all findings; V-AF3 records resolutions made |
| SC-009 | Six refreshed screenshots show current UI | Screenshot | V-S4 (capture consistency), V-S5 (route validation) |
| SC-010 | ≥5 new screenshots with alt text | Screenshot | Registry specifies 5 mandatory + 1 recommended new; V-S2 requires alt text |
| SC-011 | No real user data in screenshots | Screenshot | V-S3 prohibits real data, allows dummy data |
| SC-012 | Outreach list created and committed | OutreachEntry | outreach.md file per V-OE1 |
| SC-013 | Outreach list linked and maintained | OutreachEntry | V-OE4 specifies linking; V-OE2 maintains status updates |
| SC-014 | Each target has terminal state by completion | OutreachEntry | V-OE2 state machine; V-OE3 terminal states defined |

---

## References

- **Feature Spec**: `/home/user/Gameplane/specs/done_012-docs-refresh-and-outreach/spec.md` (FR-001 through FR-025, SC-001 through SC-014)
- **Orchestrator Decisions**: D-A through D-I (D-H: table placement; D-C: label vocabulary; D-F: audit-log schema; D-A: comparison-sources.md); OD-1 through OD-15 (all ruled 2026-09-02 by maintainer; see MAINTAINER RULINGS section)
- **Research Findings**: R1-versions.md (version audit), R2-links.md (link verification), R3-labels.md (label registry), R4-screens.md (screenshot inventory), R5-gameplane-facts.md (Gameplane comparison cells), R6-outreach.md (directory requirements), R7-competitor-sources.md (competitor sourcing), R8-unshipped.md (unshipped-feature audit)
- **CLAUDE.md**: Rule 7 (wrap errors with %w), Rule 11 (commit regularly), Rule 12 (one branch per unit), Rule 15 (read whole spec folder), Rule 16 (rename done_ on completion)
- **Constitution Principle IV**: Spec-Driven Development; data-model.md is the artifact that survives context resets

---

## Validation Checklist for Implementers

Before publishing findings and artifacts:

- [ ] All 17 AuditedFiles listed in registry are checked (18th file docs/comparison-sources.md is created, not audited in v0.2.0-beta.8)
- [ ] Every AuditFinding has an `evidenceChecked` path:line citation
- [ ] Every AuditFinding is recorded in audit-log.md; category "unshipped" identifies features described as shipped that are in CHANGELOG Unreleased
- [ ] ComparisonTable has status line (FR-004, V-CT1)
- [ ] Every ComparisonCell includes qualifiers (Gameplane) or SourceReference (competitors); Agones "not applicable" cells (OD-11) and unverifiable CubeCoders cells (OD-9) use ruled text
- [ ] SourceReference entries stored in docs/comparison-sources.md; unavailable URLs default to "source URL unavailable (checked YYYY-MM-DD)" (OD-10)
- [ ] Every LabelRegistryEntry checked for first-mention compliance (FR-012); tunnel [optional] is now confirmed (OD-4)
- [ ] Six refreshed + ≥5 new screenshots exist in docs/img/ at 1920×1080 JPEG (OD-3a), no per-image mock-mode disclosure (OD-3d)
- [ ] All screenshots have alt text (V-S2) and no real user data (V-S3)
- [ ] docs/roadmap.md entries carry shipped/planned markers (OD-7); CHANGELOG.md corrected per OD-8 if needed
- [ ] OutreachEntry targets listed in outreach.md: AlternativeTo pending (OD-6c), Awesome-Selfhosted deferred [2026-09-02] (OD-6a), Awesome-Kubernetes deferred [2026-09-02] (OD-6b)
- [ ] outreach.md linked from docs/contributing.md; agents draft content per OD-5, maintainer submits
- [ ] Every status change in outreach.md is a separate git commit
